//go:build integration

package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbtest "github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/ingest"
	"github.com/stashapp/stash-box/internal/service/ingest/extract"
)

// Performer profile snapshots (SPEC §7.4, plan phase 9 slice 9g).
//
// The rule this file exists for is the LAST one: a second capture must not overwrite the first. A
// cache keeps the newest answer, and the interesting question about a source page is what it said
// BEFORE it changed -- so the tests below are mostly about which capture survives what.

const snapshotRawURL = "HTTPS://EXAMPLE.invalid/Performers/Ada/?utm_source=news"
const snapshotCanonical = "https://example.invalid/Performers/Ada"

func seedPerformerRow(t *testing.T, name string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, dbtest.DB().QueryRow(t.Context(), `
		INSERT INTO performers (id, name, created_at, updated_at)
		VALUES ($1, $2, now(), now()) RETURNING id`,
		uuid.Must(uuid.NewV4()), name).Scan(&id))
	return id
}

func snapshotService(t *testing.T, byURL map[string]extract.Metadata) *extract.Fake {
	t.Helper()
	fake := extract.NewFake(byURL)
	t.Cleanup(func() {
		_, err := dbtest.DB().Exec(context.Background(),
			`DELETE FROM performer_profile_snapshots WHERE source_url = $1`, snapshotRawURL)
		require.NoError(t, err)
	})
	return fake
}

func pageSaying(title string) map[string]extract.Metadata {
	return map[string]extract.Metadata{
		snapshotRawURL: {Title: title, Studio: "A Studio", PerformerNames: []string{"Ada"}, Source: "fake"},
	}
}

func TestPerformerProfileSnapshotIsARecordNotACache(t *testing.T) {
	performerID := seedPerformerRow(t, "snap-"+uuid.Must(uuid.NewV4()).String())
	fake := snapshotService(t, pageSaying("Ada, first cut"))
	svc := ingest.NewService(queries.New(dbtest.DB()), fake)

	first, err := svc.SnapshotPerformerProfile(t.Context(), performerID, snapshotRawURL)
	require.NoError(t, err)
	assert.False(t, first.CapturedAt.IsZero(), "the capture carries the instant it was seen")
	assert.Equal(t, snapshotRawURL, first.SourceURL, "the string the curator used is kept verbatim")
	assert.Equal(t, snapshotCanonical, first.URLNormalized)

	// The content round-trips: what the page SAID is readable back out.
	var said extract.Metadata
	require.NoError(t, json.Unmarshal(first.Content, &said))
	assert.Equal(t, "Ada, first cut", said.Title)
	assert.NotEmpty(t, first.ContentHash)

	// The same page saying the same thing again is ONE record, and its timestamp does not move: a
	// capture whose captured_at followed the re-read would be a cache with a fresh-looking date.
	again, err := svc.SnapshotPerformerProfile(t.Context(), performerID, snapshotRawURL)
	require.NoError(t, err)
	assert.Equal(t, first.ID, again.ID, "a second capture of the same content is the same record")
	assert.Equal(t, first.CapturedAt, again.CapturedAt, "and it does not restamp it")

	// The page CHANGES. That is a new row beside the old one -- and the old one still says what it
	// said, which is the whole point of keeping it.
	fake.ByURL[snapshotRawURL] = extract.Metadata{Title: "Ada, retitled", PerformerNames: []string{"Ada"}}
	second, err := svc.SnapshotPerformerProfile(t.Context(), performerID, snapshotRawURL)
	require.NoError(t, err)
	assert.NotEqual(t, first.ID, second.ID, "changed content is a second record")

	snapshots, err := svc.ListPerformerProfileSnapshots(t.Context(), performerID)
	require.NoError(t, err)
	require.Len(t, snapshots, 2, "both captures are kept")
	assert.Equal(t, second.ID, snapshots[0].ID, "newest first")
	assert.Equal(t, first.ID, snapshots[1].ID)

	var older extract.Metadata
	require.NoError(t, json.Unmarshal(snapshots[1].Content, &older))
	assert.Equal(t, "Ada, first cut", older.Title, "the FIRST capture was not overwritten by the second")

	// The same page written differently is the SAME page: a tracking parameter and an upper-case host
	// must not create a third record.
	//
	// The fake has to know the variant, and that is not test noise: the fetch happens on the string
	// the curator gave (the extractor follows shorteners and reports where it landed), so a re-paste
	// costs one real request and is deduped at the STORE. Saying that here keeps the next reader from
	// "fixing" the fake to be clever about URLs, which would hide the dedup this test is about.
	fake.ByURL["HTTPS://EXAMPLE.invalid/Performers/Ada/?utm_campaign=x"] = extract.Metadata{
		Title: "Ada, retitled", PerformerNames: []string{"Ada"},
	}
	variant, err := svc.SnapshotPerformerProfile(t.Context(), performerID, "HTTPS://EXAMPLE.invalid/Performers/Ada/?utm_campaign=x")
	require.NoError(t, err)
	assert.Equal(t, second.ID, variant.ID, "the key is the normalised URL, not the string pasted")
	assert.Equal(t, snapshotRawURL, variant.SourceURL, "and the FIRST capture's own string stands")

	after, err := svc.ListPerformerProfileSnapshots(t.Context(), performerID)
	require.NoError(t, err)
	assert.Len(t, after, 2, "still two records")
}

// A failed fetch records NOTHING. A table of claims nobody can check is not made better by rows
// describing fetches that did not happen, and the caller needs to know WHICH failure it was: a gone
// page is a loss to accept, an unreachable host is one to retry.
func TestPerformerProfileSnapshotRecordsNothingWhenTheFetchFails(t *testing.T) {
	performerID := seedPerformerRow(t, "snap-fail-"+uuid.Must(uuid.NewV4()).String())
	fake := snapshotService(t, map[string]extract.Metadata{})
	svc := ingest.NewService(queries.New(dbtest.DB()), fake)

	_, err := svc.SnapshotPerformerProfile(t.Context(), performerID, snapshotRawURL)
	assert.ErrorIs(t, err, extract.ErrGone, "the reason survives, because the caller acts on it differently")

	var count int
	require.NoError(t, dbtest.DB().QueryRow(t.Context(),
		`SELECT count(*) FROM performer_profile_snapshots WHERE performer_id = $1`, performerID).Scan(&count))
	assert.Zero(t, count, "a fetch that failed is not evidence and must not be recorded as any")

	// An unreachable host is a different answer, and neither writes.
	fake.ByURLError[snapshotRawURL] = extract.ErrUnreachable
	_, err = svc.SnapshotPerformerProfile(t.Context(), performerID, snapshotRawURL)
	assert.ErrorIs(t, err, extract.ErrUnreachable)
}

// A snapshot of a performer this instance does not have is refused by NAME, before the foreign key
// gets a chance to say "constraint performer_profile_snapshots_performer_id_fkey".
func TestPerformerProfileSnapshotOfAnUnknownPerformerIsNamed(t *testing.T) {
	fake := snapshotService(t, pageSaying("nobody"))
	svc := ingest.NewService(queries.New(dbtest.DB()), fake)

	_, err := svc.SnapshotPerformerProfile(t.Context(), uuid.Must(uuid.NewV4()), snapshotRawURL)
	assert.ErrorIs(t, err, ingest.ErrNoSuchPerformer)
	assert.Empty(t, fake.Calls, "the page is not even fetched: there is nothing to record it against")

	_, err = svc.SnapshotPerformerProfile(t.Context(), uuid.Must(uuid.NewV4()), "not a url at all")
	assert.True(t, errors.Is(err, ingest.ErrInvalidURL), "a URL that cannot be normalised is refused first: %v", err)
}
