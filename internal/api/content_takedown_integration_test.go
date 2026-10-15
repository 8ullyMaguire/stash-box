//go:build integration

package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/99designs/gqlgen/client"

	dbtest "github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/pkg/urlkey"
)

// The takedown path, driven the way an operator drives it (migration 134).
//
// WHAT THESE TESTS ARE FOR, given that every CONSEQUENCE of a takedown already had a test: those
// tests set `status = 'dmca'` with a raw UPDATE, so the instance's whole ability to answer a notice
// rested on somebody typing SQL by hand. The subject here is the verb -- that an operator can tell
// the instance, that the telling lands in the same machine the rest of the programme reads, and that
// it lands ATOMICALLY with the record of who said so.
//
// The refusal half is asserted at its strongest point: a blocked URL submitted afterwards is not
// merely refused, it is refused BEFORE the network is touched (the poisoned extractor counts the
// calls), which is the property the pipeline's ordering comment claims.

type takedownResult struct {
	Block struct {
		ID     string `json:"id"`
		Kind   string `json:"kind"`
		Value  string `json:"value"`
		Reason string `json:"reason"`
	} `json:"block"`
	Changed        int  `json:"changed"`
	AlreadyBlocked bool `json:"already_blocked"`
	Sources        []struct {
		URL    string `json:"url"`
		Status string `json:"status"`
	} `json:"sources"`
}

func takedownContentURL(t *testing.T, r *testRunner, url, reference string) (takedownResult, error) {
	t.Helper()
	var resp struct {
		TakedownContentURL takedownResult `json:"takedownContentUrl"`
	}
	err := r.client.Post(`
		mutation($input: TakedownContentURLInput!) {
			takedownContentUrl(input: $input) {
				block { id kind value reason }
				changed
				already_blocked
				sources { url status }
			}
		}
	`, &resp, client.Var("input", map[string]any{"url": url, "reference": reference}))
	return resp.TakedownContentURL, err
}

func blockContentHost(t *testing.T, r *testRunner, host, reason string) (takedownResult, error) {
	t.Helper()
	var resp struct {
		BlockContentHost takedownResult `json:"blockContentHost"`
	}
	err := r.client.Post(`
		mutation($input: BlockContentHostInput!) {
			blockContentHost(input: $input) {
				block { id kind value reason }
				changed
				already_blocked
				sources { url status }
			}
		}
	`, &resp, client.Var("input", map[string]any{"host": host, "reason": reason}))
	return resp.BlockContentHost, err
}

func releaseContentBlock(t *testing.T, r *testRunner, kind, value string) (bool, error) {
	t.Helper()
	var resp struct {
		ReleaseContentBlock bool `json:"releaseContentBlock"`
	}
	err := r.client.Post(`
		mutation($kind: String!, $value: String!) {
			releaseContentBlock(kind: $kind, value: $value)
		}
	`, &resp, client.Var("kind", kind), client.Var("value", value))
	return resp.ReleaseContentBlock, err
}

// insertSource writes a content_sources row directly, which is how the other content tests build
// their fixtures: entity_id has no foreign key (the column is polymorphic by design), so a scene
// does not have to exist for a source record to.
//
// `normalized` is a parameter and not derived, because the two arms of the takedown statement are
// the point: a row written by the ingest path carries the key, and a row the scene_urls trigger
// mirrored carries NULL.
func insertSource(t *testing.T, url string, normalized *string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, dbtest.DB().QueryRow(t.Context(),
		`INSERT INTO content_sources (entity_type, entity_id, url, url_normalized) VALUES ('scene', $1, $2, $3) RETURNING id`,
		uuid.Must(uuid.NewV4()), url, normalized).Scan(&id))
	t.Cleanup(func() {
		_, _ = dbtest.DB().Exec(context.Background(), `DELETE FROM content_sources WHERE id = $1`, id)
	})
	return id
}

func sourceStatus(t *testing.T, id uuid.UUID) string {
	t.Helper()
	var status string
	require.NoError(t, dbtest.DB().QueryRow(t.Context(),
		`SELECT status FROM content_sources WHERE id = $1`, id).Scan(&status))
	return status
}

type auditRow struct {
	Action     string
	TargetType string
	Reason     *string
	Data       []byte
}

// auditsFor reads every audit row against a block, oldest first: a block can be the subject of more
// than one decision (a second notice about a URL already removed reuses the block and writes its own
// row), and the ORDER is part of what is being asserted.
func takedownAuditsFor(t *testing.T, targetID uuid.UUID) []auditRow {
	t.Helper()
	rows, err := dbtest.DB().Query(t.Context(),
		`SELECT action::text, target_type, reason, data FROM mod_audit WHERE target_id = $1 ORDER BY created_at ASC, id`, targetID)
	require.NoError(t, err)
	defer rows.Close()

	var out []auditRow
	for rows.Next() {
		var row auditRow
		require.NoError(t, rows.Scan(&row.Action, &row.TargetType, &row.Reason, &row.Data))
		out = append(out, row)
	}
	require.NoError(t, rows.Err())
	return out
}

// THE WHOLE POINT, IN ONE TEST. A notice names one URL; the instance holds two records of it (the
// ingest-written one with the key, the mirrored one without); both stop being served, the URL is
// refused from now on, and the decision is recorded against the operator who made it.
func TestTakedownContentURLTakesDownEveryRecordOfTheURLAndBlocksIt(t *testing.T) {
	fetchSourcesForTest(t)
	raw := "https://takedown.invalid/watch/" + uuid.Must(uuid.NewV4()).String()
	normalized, err := urlkey.Normalize(raw)
	require.NoError(t, err)

	// The case-variant second record is the arm that matters, and it is what the scene_urls trigger
	// writes: the same URL with no `url_normalized`, which only the statement's LOWER(url) arm can
	// find. A same-host-different-path row and a different host are the control.
	withKey := insertSource(t, raw, &normalized)
	mirrored := insertSource(t, strings.Replace(raw, "://takedown.invalid", "://TAKEDOWN.invalid", 1), nil)
	sameHostOtherURL := insertSource(t, "https://takedown.invalid/other/"+uuid.Must(uuid.NewV4()).String(), nil)
	untouched := insertSource(t, "https://other.invalid/watch/"+uuid.Must(uuid.NewV4()).String(), nil)

	r := asAdmin(t)
	result, err := takedownContentURL(t, r, raw, "notice 2026-10-15-A")
	require.NoError(t, err)

	assert.Equal(t, "url", result.Block.Kind)
	assert.Equal(t, normalized, result.Block.Value,
		"the block's key is the NORMALIZED URL, which is what makes it comparable to a submission")
	assert.Equal(t, "notice 2026-10-15-A", result.Block.Reason)

	assert.False(t, result.AlreadyBlocked, "this notice is what blocked the URL")
	assert.Equal(t, 2, result.Changed, "both records of this URL, and only they, were moved")
	assert.Equal(t, "dmca", sourceStatus(t, withKey), "the record carrying the key is taken down")
	assert.Equal(t, "dmca", sourceStatus(t, mirrored),
		"and so is the mirrored record, which has no key and is found by the LOWER(url) arm alone")
	assert.Equal(t, "unknown", sourceStatus(t, sameHostOtherURL), "another path on the host is another URL")
	assert.Equal(t, "unknown", sourceStatus(t, untouched), "and so is another host")

	// A SECOND NOTICE FOR THE SAME URL CHANGES NOTHING, and the count is how the operator learns
	// that. Without the statement's `status <> 'dmca'` guard this would report the same rows again,
	// which reads as a removal that happened twice.
	again, err := takedownContentURL(t, r, raw, "notice 2026-10-15-A (duplicate)")
	require.NoError(t, err, "a repeated notice must be a success and not a unique-constraint error")
	assert.Equal(t, 0, again.Changed, "nothing was served by this URL any more, so nothing changed")
	assert.True(t, again.AlreadyBlocked, "and the operator is told it was already refused")
	assert.Equal(t, result.Block.ID, again.Block.ID, "the instance reports the block it already had")

	blockID := uuid.FromStringOrNil(result.Block.ID)
	require.NotEqual(t, uuid.Nil, blockID, "the mutation must return the block it wrote")
	assert.Equal(t, blockID, uuid.FromStringOrNil(again.Block.ID), "the duplicate reused the same block row")

	rows := takedownAuditsFor(t, blockID)
	require.Len(t, rows, 2, "both notices are recorded against the block they acted on")
	assert.Equal(t, "SOURCE_TAKEDOWN", rows[0].Action)
	assert.Equal(t, "SOURCE_BLOCK", rows[0].TargetType)
	require.NotNil(t, rows[0].Reason)
	assert.Equal(t, "notice 2026-10-15-A", *rows[0].Reason)
	require.NotNil(t, rows[1].Reason)
	assert.Equal(t, "notice 2026-10-15-A (duplicate)", *rows[1].Reason,
		"the second notice is a row of its own -- the letters received are the instance's evidence")

	var first map[string]any
	require.NoError(t, json.Unmarshal(rows[0].Data, &first))
	assert.Equal(t, float64(2), first["changed"], "and the first row records what it changed")
	assert.Equal(t, raw, first["url"], "with the URL as the notice wrote it")

	// Cleanup the block this test created, or every later run of this file inherits a refusal that
	// makes its own submissions test the wrong thing.
	t.Cleanup(func() {
		_, _ = dbtest.DB().Exec(context.Background(),
			`DELETE FROM content_source_block WHERE value = $1`, normalized)
	})
}

// THE REASON THE BLOCK EXISTS. A notice about a URL this instance has never recorded used to be a
// no-op: nothing matched, nothing was refused, and the URL arrived the next day through a member's
// submission. Now the refusal precedes the record.
//
// Asserted where it is strongest: the queue row says why, no source was created, and the extractor
// was never asked to read the page.
func TestTakedownOfAnUnrecordedURLRefusesTheSubmissionBeforeTheFetch(t *testing.T) {
	fetchSourcesForTest(t)
	raw := "https://unrecorded.invalid/watch/" + uuid.Must(uuid.NewV4()).String()
	normalized, err := urlkey.Normalize(raw)
	require.NoError(t, err)
	cleanupIngestFixtures(t, normalized)

	r := asAdmin(t)
	result, err := takedownContentURL(t, r, raw, "notice 2026-10-15-B")
	require.NoError(t, err)

	assert.Equal(t, 0, result.Changed,
		"nothing was serving it: an empty list is the honest answer and not a failure")
	assert.Empty(t, result.Sources)

	t.Cleanup(func() {
		_, _ = dbtest.DB().Exec(context.Background(), `DELETE FROM content_source_block WHERE value = $1`, normalized)
	})

	// Now a member submits exactly that URL.
	editor := asEdit(t)
	require.Equal(t, "queued", submitContentURL(t, editor, raw, nil).Status,
		"the queue row is still created -- the refusal is the pipeline's decision, and the row is "+
			"where this design records decisions")

	extractor := &poisonedExtractor{t: t}
	processed, err := ingestServiceWith(extractor).Drain(t.Context(), 10)
	require.NoError(t, err)
	require.Equal(t, 1, processed)

	assert.Zero(t, extractor.calls, "a refused URL must not be fetched: the instance stops asking")
	row := readQueueRow(t, normalized)
	require.NotNil(t, row.Reason)
	assert.Equal(t, "rejected", row.Status)
	assert.Contains(t, *row.Reason, "takedown",
		"the queue says the URL is on the takedown list, not that the host was broken")
	assert.Nil(t, row.ResultSceneID, "no record was created for a refused URL")

	var sources int
	require.NoError(t, dbtest.DB().QueryRow(t.Context(),
		`SELECT count(*) FROM content_sources WHERE url_normalized = $1`, normalized).Scan(&sources))
	assert.Zero(t, sources, "the URL the notice named was never recorded")
}

// A report about a SITE. Every record on the host goes, including the ones spelled with `www`
// (the column holds the URL as submitted, and the block's value is the host without it), and the
// near-miss hosts are left alone -- which is the difference between a real host match and a
// substring one.
func TestBlockingAHostTakesDownEverySourceOnItAndRefusesNewOnes(t *testing.T) {
	fetchSourcesForTest(t)
	host := "blocked-" + uuid.Must(uuid.NewV4()).String() + ".invalid"

	bare := insertSource(t, "https://"+host+"/a", nil)
	www := insertSource(t, "https://www."+host+"/b", nil)
	uppercase := insertSource(t, "HTTPS://"+host+":8443/c", nil)
	trapSubdomain := insertSource(t, "https://sub."+host+"/d", nil)
	trapSuffix := insertSource(t, "https://not"+host+"/e", nil)
	trapOther := insertSource(t, "https://other.invalid/f", nil)

	r := asAdmin(t)
	result, err := blockContentHost(t, r, host, "site report: minors")
	require.NoError(t, err)

	assert.Equal(t, "host", result.Block.Kind)
	assert.Equal(t, host, result.Block.Value)
	assert.Equal(t, 3, result.Changed, "bare, www and a port are all the same host")
	for _, id := range []uuid.UUID{bare, www, uppercase} {
		assert.Equal(t, "dmca", sourceStatus(t, id))
	}
	for _, id := range []uuid.UUID{trapSubdomain, trapSuffix, trapOther} {
		assert.Equal(t, "unknown", sourceStatus(t, id),
			"a subdomain, a name that ends with the host, and an unrelated host are not the host")
	}

	t.Cleanup(func() {
		_, _ = dbtest.DB().Exec(context.Background(),
			`DELETE FROM content_source_block WHERE kind = 'host' AND value = $1`, host)
	})

	// And a URL nobody has seen on that host is refused before the fetch, by the host arm.
	raw := "https://" + host + "/brand-new/" + uuid.Must(uuid.NewV4()).String()
	normalized, err := urlkey.Normalize(raw)
	require.NoError(t, err)
	cleanupIngestFixtures(t, normalized)

	editor := asEdit(t)
	require.Equal(t, "queued", submitContentURL(t, editor, raw, nil).Status)

	extractor := &poisonedExtractor{t: t}
	_, err = ingestServiceWith(extractor).Drain(t.Context(), 10)
	require.NoError(t, err)

	assert.Zero(t, extractor.calls, "a blocked host must not be fetched")
	row := readQueueRow(t, normalized)
	require.NotNil(t, row.Reason)
	assert.Contains(t, *row.Reason, "host is blocked")
}

// Releasing stops the refusal and does NOT put anything back.
//
// Both halves are asserted because they pull in opposite directions and only one of them is
// obvious: an implementation that restored the sources on release would pass a naive "is it
// unblocked" test while asserting, on the instance's behalf, that the complaint was withdrawn.
func TestReleasingABlockStopsRefusingTheKeyButLeavesTheSourcesDown(t *testing.T) {
	fetchSourcesForTest(t)
	raw := "https://release.invalid/watch/" + uuid.Must(uuid.NewV4()).String()
	normalized, err := urlkey.Normalize(raw)
	require.NoError(t, err)
	cleanupIngestFixtures(t, normalized)
	id := insertSource(t, raw, &normalized)

	r := asAdmin(t)
	result, err := takedownContentURL(t, r, raw, "notice 2026-10-15-C")
	require.NoError(t, err)
	require.Equal(t, 1, result.Changed)

	released, err := releaseContentBlock(t, r, "url", normalized)
	require.NoError(t, err)
	assert.True(t, released, "there was a block, and the operator is told the release landed")

	// Releasing a key that is not blocked is not an error: the operator wanted it unblocked.
	releasedAgain, err := releaseContentBlock(t, r, "url", normalized)
	require.NoError(t, err)
	assert.False(t, releasedAgain, "and the second call reports there was nothing to release")

	assert.Equal(t, "dmca", sourceStatus(t, id),
		"a release does not restore: the source stays down until something re-submits it")

	audits := takedownAuditsFor(t, uuid.FromStringOrNil(result.Block.ID))
	require.NotEmpty(t, audits)
	audit := audits[len(audits)-1]

	// Parsed rather than substring-matched: `mod_audit.data` is jsonb, so the database decides the
	// spacing, and an assertion on `"released":true` fails against a correctly stored value.
	var data map[string]any
	require.NoError(t, json.Unmarshal(audit.Data, &data))
	assert.Equal(t, true, data["released"],
		"the release is recorded against the key it released -- the table is the policy, mod_audit is the history")
	assert.Equal(t, normalized, data["value"], "and against the key it released, not the one it was asked about")
}

// THE TRANSACTION, tested by making the second statement fail.
//
// A takedown is three writes -- the rows, the block, the audit -- and the failure this test creates
// is the block's own unique constraint, by blocking the URL first with the same key. The takedown
// must then fail WHOLE: if the update were not rolled back, an operator whose blocking attempt
// errored would be left with sources taken down by a decision the instance refused to record, and a
// notice would have been acted on by accident.
func TestATakedownThatCannotWriteItsBlockLeavesTheSourcesAlone(t *testing.T) {
	fetchSourcesForTest(t)
	raw := "https://atomic.invalid/watch/" + uuid.Must(uuid.NewV4()).String()
	normalized, err := urlkey.Normalize(raw)
	require.NoError(t, err)
	cleanupIngestFixtures(t, normalized)

	id := insertSource(t, raw, &normalized)

	// The failure is created at the THIRD statement and not the first: an actor who does not exist
	// makes the block's `created_by_id` foreign key fail, and by then the sources have already been
	// marked. That is what makes this a test of the transaction rather than of the ordering.
	svc := dbtest.Factory().Content()
	_, err = svc.TakedownURL(t.Context(), raw, uuid.Must(uuid.NewV4()), "notice 2026-10-15-G")
	require.Error(t, err, "the block cannot be attributed to a user who does not exist")

	assert.Equal(t, "unknown", sourceStatus(t, id),
		"the sources were NOT taken down: the mark is part of the same transaction as the block that "+
			"could not be written, and a half-applied takedown is a removal nobody decided")

	var blocks int
	require.NoError(t, dbtest.DB().QueryRow(t.Context(),
		`SELECT count(*) FROM content_source_block WHERE value = $1`, normalized).Scan(&blocks))
	assert.Zero(t, blocks, "and no block row was left behind")
}

// The two inputs a takedown cannot work without, and the role gate.
//
// A reference is required because the audit row is the instance's answer to a complaint six months
// later, and "takedown" with no reason cannot give one. The role is required because this is a
// decision about other people's content rather than about this database's own edits.
func TestTakedownNeedsAnOperatorAndAReference(t *testing.T) {
	fetchSourcesForTest(t)
	raw := "https://perms.invalid/watch/" + uuid.Must(uuid.NewV4()).String()
	normalized, err := urlkey.Normalize(raw)
	require.NoError(t, err)

	// A member who may EDIT -- the highest role below ADMIN, and the one a moderator holds.
	editor := asEdit(t)
	_, err = takedownContentURL(t, editor, raw, "notice")
	require.Error(t, err, "a takedown is not a moderation action")

	admin := asAdmin(t)
	_, err = takedownContentURL(t, admin, raw, "   ")
	require.Error(t, err, "a takedown with no reference is refused")
	assert.Contains(t, err.Error(), "record the notice")

	_, err = takedownContentURL(t, admin, "not a url at all", "notice")
	require.Error(t, err, "a takedown needs a URL the instance can key on")

	_, err = blockContentHost(t, admin, "   ", "reason")
	require.Error(t, err, "a host block with no host in it is refused")

	_, err = takedownContentURL(t, admin, raw, "notice 2026-10-15-D")
	require.NoError(t, err, "and the same call with everything filled in lands")

	// Nothing was left behind by the refused attempts: one decision, one block row.
	var blocks int
	require.NoError(t, dbtest.DB().QueryRow(t.Context(),
		`SELECT count(*) FROM content_source_block WHERE value = $1`, normalized).Scan(&blocks))
	assert.Equal(t, 1, blocks, "the refused calls wrote nothing")

	t.Cleanup(func() {
		_, _ = dbtest.DB().Exec(context.Background(), `DELETE FROM content_source_block WHERE value = $1`, normalized)
	})
}

// THE CHAIN, from the verb into the machine the rest of the programme reads.
//
// The sweep's candidate list is a partial index over ('unknown', 'live', 'intermittent'), so a row
// this verb moved is absent from it -- and absent from the archive queue for the same reason. Both
// are asserted through the real queries rather than by reading the status column again, because the
// claim being tested is "the rest of the programme sees this decision", not "a string changed".
func TestATakenDownSourceIsAbsentFromTheSweepAndArchiveQueues(t *testing.T) {
	fetchSourcesForTest(t)
	raw := "https://queues.invalid/watch/" + uuid.Must(uuid.NewV4()).String()
	normalized, err := urlkey.Normalize(raw)
	require.NoError(t, err)

	live := insertSource(t, raw, &normalized)
	_, err = dbtest.DB().Exec(t.Context(), `UPDATE content_sources SET status = 'live' WHERE id = $1`, live)
	require.NoError(t, err)

	q := queries.New(dbtest.DB())
	ctx := t.Context()

	due, err := q.FindContentSourcesDueForCheck(ctx, 5000)
	require.NoError(t, err)
	assert.True(t, containsSource(due, live), "the fixture is a candidate while it is live")

	r := asAdmin(t)
	_, err = takedownContentURL(t, r, raw, "notice 2026-10-15-E")
	require.NoError(t, err)

	due, err = q.FindContentSourcesDueForCheck(ctx, 5000)
	require.NoError(t, err)
	assert.False(t, containsSource(due, live), "a taken-down source is not a candidate for a health check")

	toArchive, err := q.FindSourcesToArchive(ctx, 5000)
	require.NoError(t, err)
	for _, row := range toArchive {
		assert.NotEqual(t, live, row.ID, "and the same decision keeps it out of the archive queue")
	}

	t.Cleanup(func() {
		_, _ = dbtest.DB().Exec(context.Background(), `DELETE FROM content_source_block WHERE value = $1`, normalized)
	})
}

func containsSource(rows []queries.FindContentSourcesDueForCheckRow, id uuid.UUID) bool {
	for _, row := range rows {
		if row.ID == id {
			return true
		}
	}
	return false
}

// The list is what an operator reads to answer "do we block that host?", so it is asserted as a
// reader would see it and not only through the boolean the release path returns.
func TestTheBlockListShowsWhatIsRefused(t *testing.T) {
	fetchSourcesForTest(t)
	host := "listed-" + uuid.Must(uuid.NewV4()).String() + ".invalid"
	raw := "https://" + host + "/watch/" + uuid.Must(uuid.NewV4()).String()
	normalized, err := urlkey.Normalize(raw)
	require.NoError(t, err)

	r := asAdmin(t)
	_, err = takedownContentURL(t, r, raw, "notice 2026-10-15-F")
	require.NoError(t, err)
	_, err = blockContentHost(t, r, host, "site report")
	require.NoError(t, err)

	var resp struct {
		ContentSourceBlocks []struct {
			Kind   string `json:"kind"`
			Value  string `json:"value"`
			Reason string `json:"reason"`
		} `json:"contentSourceBlocks"`
	}
	require.NoError(t, r.client.Post(`{ contentSourceBlocks(limit: 50) { kind value reason } }`, &resp))

	seen := map[string]string{}
	for _, row := range resp.ContentSourceBlocks {
		seen[row.Kind+"|"+row.Value] = row.Reason
	}
	assert.Equal(t, "notice 2026-10-15-F", seen["url|"+normalized])
	assert.Equal(t, "site report", seen["host|"+host])

	t.Cleanup(func() {
		_, _ = dbtest.DB().Exec(context.Background(),
			`DELETE FROM content_source_block WHERE value IN ($1, $2)`, normalized, host)
	})
}
