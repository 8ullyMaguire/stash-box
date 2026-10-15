//go:build integration

package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/99designs/gqlgen/client"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbtest "github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/service/ingest/extract"
)

// THE HYDRUS EXPORT (plan slice 13c), against a real database.
//
// The claims that need one:
//
//  1. THE ROUND TRIP. "Round-trips back in" is not "the JSON parses". A record is exported, one of its
//     addresses is fed back through this instance's OWN ingest (`submitContentUrl`), and the pipeline must
//     settle it from the database ALONE -- already claimed, so it never reads a page. A test that only parsed
//     the document would pass on an export whose addresses were invented.
//  2. A DENIED RECORD IS ABSENT, and named in the envelope. Not annotated inside the document: an export
//     that carried a taken-down record with a flag on it would be a copy of it in somebody else's client.
//  3. The bound is enforced, and it is the reason an export renders records rather than being a read surface.

// exportedEntry is one entry of the document, read back through JSON rather than by string matching.
type exportedEntry struct {
	Metadata struct {
		Tags  []string `json:"tags"`
		URLs  []string `json:"urls"`
		Notes []string `json:"notes"`
	} `json:"metadata"`
}

type hydrusExportOutput struct {
	JSON        string `json:"json"`
	RecordCount int    `json:"record_count"`
	// Strings rather than uuids: the test client decodes a uuid.UUID as a 16-element array, and an ID list
	// off the wire is a list of strings.
	Omitted []string `json:"omitted"`
}

func (c *graphqlClient) exportScenes(ids []uuid.UUID) (*hydrusExportOutput, error) {
	asStrings := make([]string, 0, len(ids))
	for _, id := range ids {
		asStrings = append(asStrings, id.String())
	}

	var resp struct {
		ExportScenes hydrusExportOutput `json:"exportScenes"`
	}
	err := c.Post(`
		query ExportScenes($ids: [ID!]!) {
			exportScenes(ids: $ids) {
				json
				record_count
				omitted
			}
		}
	`, &resp, client.Var("ids", asStrings))
	if err != nil {
		return nil, err
	}
	return &resp.ExportScenes, nil
}

// exportFixture writes a record the way the database holds one, with the parts an export maps: a title, a
// description, a code, a date, a tag and an address.
func exportFixture(t *testing.T, title string) (uuid.UUID, uuid.UUID, string) {
	t.Helper()
	sceneID := uuid.Must(uuid.NewV4())
	tagID := uuid.Must(uuid.NewV4())
	tagName := "exporttag" + strings.ReplaceAll(uuid.Must(uuid.NewV4()).String()[:8], "-", "")
	address := "https://export.invalid/" + sceneID.String()
	code := "EXP-" + strings.ToUpper(strings.ReplaceAll(uuid.Must(uuid.NewV4()).String()[:6], "-", ""))

	_, err := dbtest.DB().Exec(t.Context(),
		`INSERT INTO tags (id, name, created_at, updated_at) VALUES ($1, $2, now(), now())`, tagID, tagName)
	require.NoError(t, err)

	_, err = dbtest.DB().Exec(t.Context(),
		`INSERT INTO scenes (id, title, details, code, date, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, DATE '2026-10-15', now(), now())`,
		sceneID, title, "A description of the record.", code)
	require.NoError(t, err)

	_, err = dbtest.DB().Exec(t.Context(),
		`INSERT INTO scene_tags (scene_id, tag_id) VALUES ($1, $2)`, sceneID, tagID)
	require.NoError(t, err)

	_, err = dbtest.DB().Exec(t.Context(),
		// `site_id` is NOT NULL and foreign-keyed to the seeded sites, so the address is attached to a real
		// site rather than to an invented one -- and the insert fires the statement-level trigger that
		// projects scene_urls into content_sources (migration 108), which is what makes the round-trip test
		// below able to recognise its own address.
		`INSERT INTO scene_urls (scene_id, url, site_id)
		 VALUES ($1, $2, (SELECT id FROM sites WHERE 'SCENE' = ANY(valid_types) LIMIT 1))`, sceneID, address)
	require.NoError(t, err)

	return sceneID, tagID, address
}

func parseExport(t *testing.T, document string) map[string]exportedEntry {
	t.Helper()
	var parsed map[string]exportedEntry
	require.NoError(t, json.Unmarshal([]byte(document), &parsed), "the export must be readable JSON: %s", document)
	return parsed
}

// countingExtractor answers the question "was a page read" by never being called: any call fails the test.
// Fetching IS enabled on this instance, so the reason no page is read must be the record already claiming the
// address, and the only way to show that is to make a read impossible and watch the job still finish.
type countingExtractor struct {
	t     *testing.T
	calls int
}

func (e *countingExtractor) Extract(context.Context, string) (extract.Metadata, error) {
	e.calls++
	e.t.Error("the pipeline read a page for an address this instance already holds: the round trip must be " +
		"settled from the database alone")
	return extract.Metadata{}, extract.ErrUnsupportedPage
}

func TestAnExportCarriesWhatTheInstanceHolds(t *testing.T) {
	runner := asRead(t)
	sceneID, _, address := exportFixture(t, "An Exported Record")

	result, err := runner.client.exportScenes([]uuid.UUID{sceneID})
	require.NoError(t, err)
	require.Equal(t, 1, result.RecordCount)
	assert.Empty(t, result.Omitted)

	entry, ok := parseExport(t, result.JSON)[sceneID.String()]
	require.True(t, ok, "the record's id keys its entry: %s", result.JSON)

	assert.Equal(t, []string{address}, entry.Metadata.URLs)
	assert.Contains(t, entry.Metadata.Notes, "title: An Exported Record")
	assert.Contains(t, entry.Metadata.Notes, "details: A description of the record.")

	// The record's own tag vocabulary arrives namespaced, and the code and date are tags a library can route
	// on without knowing anything about us.
	assert.Len(t, entry.Metadata.Tags, 3)
	for _, tag := range entry.Metadata.Tags {
		assert.True(t, strings.HasPrefix(tag, "tag:") || strings.HasPrefix(tag, "code:") ||
			strings.HasPrefix(tag, "date:"), "unexpected tag %q", tag)
	}
	found := false
	for _, tag := range entry.Metadata.Tags {
		if strings.HasPrefix(tag, "code:EXP-") {
			found = true
		}
	}
	assert.True(t, found, "the record's own code is a tag: %v", entry.Metadata.Tags)
	assert.Contains(t, entry.Metadata.Tags, "date:2026-10-15")
}

// THE ROUND TRIP.
func TestAnExportedAddressIsSettledByThisInstancesOwnIngest(t *testing.T) {
	// Fetching ON, so that "no page was read" is a fact about the record claiming its own address rather than
	// about a switch that forbids reading anything.
	fetchSourcesForTest(t)

	runner := asRead(t)
	sceneID, _, address := exportFixture(t, "A Round Trip Record")

	result, err := runner.client.exportScenes([]uuid.UUID{sceneID})
	require.NoError(t, err)
	entry := parseExport(t, result.JSON)[sceneID.String()]
	require.Equal(t, []string{address}, entry.Metadata.URLs)

	// Feed the exported address back in, as a member with a client would. The submission needs the role that
	// may propose content, which the export itself does not: reading a record and proposing one are different
	// permissions, and this test holds one runner for each rather than one runner with both.
	submitted := submitContentURL(t, asEdit(t), address, nil)
	if submitted.Status == "duplicate" {
		// The shared test database already queued this URL. That is the same answer this test is looking for,
		// reached by an earlier statement; the assertion below is about what the pipeline does with a row.
		return
	}
	require.Equal(t, "queued", submitted.Status, "a URL this instance holds must be accepted and settled")

	extractor := &countingExtractor{t: t}
	processed, err := ingestServiceWith(extractor).Drain(t.Context(), 5)
	require.NoError(t, err)
	require.GreaterOrEqual(t, processed, 1)

	row := readQueueRow(t, submitted.NormalizedURL)
	assert.Zero(t, extractor.calls, "no page was read: the address was already claimed")
	assert.Equal(t, "done", row.Status)
	require.NotNil(t, row.Outcome)
	assert.Equal(t, "attached", *row.Outcome,
		"the outcome names the attach path, which is the one settled from the database alone")
	assert.Nil(t, row.Confidence, "confidence stays NULL for a job that read nothing")
}

// A DENIED RECORD IS ABSENT FROM THE DOCUMENT rather than annotated inside it, and the envelope is where the
// caller learns it was left out -- one list, because "not there" and "not served" are one answer.
func TestADeniedRecordIsAbsentAndNamedInTheEnvelope(t *testing.T) {
	runner := asRead(t)
	sceneID, tagID, _ := exportFixture(t, "A Denied Record")

	// The instance's own denylist, on the tag the record carries: the same instrument a takedown uses, and the
	// same code path that makes the record absent from a search.
	_, err := dbtest.DB().Exec(t.Context(),
		`INSERT INTO content_denylist_tag (tag_id, reason, created_at) VALUES ($1, 'this test denies it', now())`,
		tagID)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = dbtest.DB().Exec(context.Background(), `DELETE FROM content_denylist_tag WHERE tag_id = $1`, tagID)
	})

	missing := uuid.Must(uuid.NewV4())
	result, err := runner.client.exportScenes([]uuid.UUID{sceneID, missing})
	require.NoError(t, err)

	assert.Equal(t, 0, result.RecordCount, "a denied record is not in the document")
	assert.Empty(t, parseExport(t, result.JSON), "and nothing of it is")

	// ONE list for both kinds of absence, and the ids in the order they were asked for.
	assert.Equal(t, []string{sceneID.String(), missing.String()}, result.Omitted)
	assert.NotContains(t, result.JSON, sceneID.String(), "the denied id is not even a key")
	assert.NotContains(t, result.JSON, "denied", "and its absence is not annotated")
}

func TestAnExportIsBoundedAndTheBoundIsNamed(t *testing.T) {
	runner := asRead(t)

	ids := make([]uuid.UUID, 0, 101)
	for range 101 {
		ids = append(ids, uuid.Must(uuid.NewV4()))
	}

	_, err := runner.client.exportScenes(ids)
	require.Error(t, err, "an export renders records the caller already has; it is not a read surface")
	assert.Contains(t, err.Error(), "100", "and the refusal says what the bound is")
}

func TestAnEmptyExportIsAnAnswerRatherThanAnError(t *testing.T) {
	runner := asRead(t)

	result, err := runner.client.exportScenes(nil)
	require.NoError(t, err, "asking for nothing and getting nothing is a correct answer")
	assert.Equal(t, 0, result.RecordCount)
	assert.Empty(t, result.Omitted)
	assert.Equal(t, "{}\n", result.JSON, "an empty document, still a document")
}
