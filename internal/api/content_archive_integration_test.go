//go:build integration

package api_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbtest "github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/content"
)

// The archive queue (slice 9e): what this instance preserves before it vanishes, and what it
// deliberately does not.
type recordingArchiver struct {
	calls     []string
	archived  string
	failFirst bool
}

func (a *recordingArchiver) Archive(_ context.Context, target string) (string, error) {
	a.calls = append(a.calls, target)
	if a.failFirst && len(a.calls) == 1 {
		return "", errors.New("the archive refused")
	}
	return a.archived + "?u=" + target, nil
}

func seedSource(t *testing.T, url, status string, archived *string) uuid.UUID {
	t.Helper()

	var id uuid.UUID
	require.NoError(t, dbtest.DB().QueryRow(t.Context(), `
		INSERT INTO content_sources (entity_type, entity_id, url, status, check_failures, last_checked, archived_url, archived_at)
		VALUES ('scene', $1, $2, $3, 5, $4, $5, CASE WHEN $5::text IS NULL THEN NULL ELSE now() END) RETURNING id`,
		uuid.Must(uuid.NewV4()), url, status, time.Now().Add(-48*time.Hour), archived).Scan(&id))
	t.Cleanup(func() {
		_, err := dbtest.DB().Exec(context.Background(), `DELETE FROM content_sources WHERE id = $1`, id)
		require.NoError(t, err)
	})
	return id
}

func archivedPair(t *testing.T, id uuid.UUID) (*string, *time.Time) {
	t.Helper()

	var url *string
	var at *time.Time
	require.NoError(t, dbtest.DB().QueryRow(t.Context(),
		`SELECT archived_url, archived_at FROM content_sources WHERE id = $1`, id).Scan(&url, &at))
	return url, at
}

func TestArchiveSourcesPreservesAGoneSourceAndRecordsBothColumns(t *testing.T) {
	svc := content.NewService(queries.New(dbtest.DB()), nil)

	url := "https://archive-test.invalid/" + uuid.Must(uuid.NewV4()).String()
	id := seedSource(t, url, content.StatusDead, nil)

	archiver := &recordingArchiver{archived: "https://web.archive.org/web/1/https://archive-test.invalid/"}
	attempts, err := svc.ArchiveSources(context.Background(), 10, archiver)
	require.NoError(t, err)

	var mine *content.ArchiveAttempt
	for i := range attempts {
		if attempts[i].URL == url {
			mine = &attempts[i]
		}
	}
	require.NotNil(t, mine, "the dead source is a candidate")
	require.NoError(t, mine.Err)
	assert.Contains(t, mine.ArchivedURL, "https://web.archive.org/")

	stored, at := archivedPair(t, id)
	require.NotNil(t, stored, "the URL is recorded")
	require.NotNil(t, at, "and so is WHEN, which the CHECK constraint pairs with it")
	assert.Equal(t, mine.ArchivedURL, *stored)
}

// An archive is made once. A source that already has one is not submitted again -- the Internet
// Archive is somebody else's service and resubmitting the same URL forever is a way to be uninvited.
func TestArchiveSourcesDoesNotResubmitAnArchivedSource(t *testing.T) {
	svc := content.NewService(queries.New(dbtest.DB()), nil)

	url := "https://archive-test.invalid/" + uuid.Must(uuid.NewV4()).String()
	existing := "https://web.archive.org/web/2/https://archive-test.invalid/old"
	seedSource(t, url, content.StatusDead, &existing)

	archiver := &recordingArchiver{archived: "https://web.archive.org/web/3/"}
	attempts, err := svc.ArchiveSources(context.Background(), 10, archiver)
	require.NoError(t, err)

	for _, attempt := range attempts {
		assert.NotEqual(t, url, attempt.URL, "an already-archived source is not a candidate")
	}
	assert.NotContains(t, archiver.calls, url, "and it is not submitted")
}

// DEAD AND INTERMITTENT ONLY. A takedown is a decision the instance has taken, and sending a URL it
// has decided not to serve to a third party for permanent storage would be passing it around -- the
// same reasoning that stops a mirror (slice 9d).
func TestArchiveSourcesLeavesTakedownsAlone(t *testing.T) {
	svc := content.NewService(queries.New(dbtest.DB()), nil)

	url := "https://archive-test.invalid/" + uuid.Must(uuid.NewV4()).String()
	seedSource(t, url, content.StatusDMCA, nil)

	archiver := &recordingArchiver{archived: "https://web.archive.org/web/4/"}
	attempts, err := svc.ArchiveSources(context.Background(), 10, archiver)
	require.NoError(t, err)

	for _, attempt := range attempts {
		assert.NotEqual(t, url, attempt.URL)
	}
	assert.NotContains(t, archiver.calls, url, "a dmca source is not sent to the archive")
}

// A refusal is a result about ONE url. The batch continues and reports it, because one host's
// availability says nothing about the next one's.
func TestArchiveSourcesReportsARefusalAndCarriesOn(t *testing.T) {
	svc := content.NewService(queries.New(dbtest.DB()), nil)

	first := "https://archive-test.invalid/" + uuid.Must(uuid.NewV4()).String()
	second := "https://archive-test.invalid/" + uuid.Must(uuid.NewV4()).String()
	firstID := seedSource(t, first, content.StatusIntermittent, nil)
	secondID := seedSource(t, second, content.StatusIntermittent, nil)

	archiver := &recordingArchiver{archived: "https://web.archive.org/web/5/", failFirst: true}
	attempts, err := svc.ArchiveSources(context.Background(), 10, archiver)
	require.NoError(t, err, "a refusal is not an error from the batch")

	var failed, succeeded *content.ArchiveAttempt
	for i := range attempts {
		switch attempts[i].URL {
		case first:
			failed = &attempts[i]
		case second:
			succeeded = &attempts[i]
		}
	}
	require.NotNil(t, failed, "the refused url is reported")
	require.Error(t, failed.Err)
	assert.Empty(t, failed.ArchivedURL)

	if succeeded != nil {
		require.NoError(t, succeeded.Err)
	} else {
		// The ORDER is the query's (oldest-checked first, and both were seeded alike), so which url
		// came first is not this test's business; what is: exactly one was refused, and the other was
		// archived -- i.e. the refusal did not stop the run.
		assert.GreaterOrEqual(t, len(archiver.calls), 1)
	}

	storedFirst, atFirst := archivedPair(t, firstID)
	if failed.Err != nil {
		assert.Nil(t, storedFirst, "a refused submission leaves BOTH columns alone")
		assert.Nil(t, atFirst)
	}
	storedSecond, _ := archivedPair(t, secondID)
	assert.NotNil(t, storedSecond, "and the batch reached the next candidate")
}
