//go:build integration

package api_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/config"
	dbtest "github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/service/content"
)

// The transparency report (phase 16, slice 16a), verified the way `docs/goal-check.py` says it has to be:
// EVERY FIGURE IS RECOMPUTED FROM THE TABLES BY THE TEST ITSELF.
//
// WHY THAT IS THE ASSERTION RATHER THAN A TABLE OF EXPECTED NUMBERS. A transparency report is the one
// surface in this programme whose entire value is that a reader can check it; a test asserting `Refusals ==
// 3` against a fixture would pass for a report that counted the wrong table, counted nothing at all, or had
// the number written into it. So each assertion below puts the report's field beside a `SELECT COUNT(*)`
// written here, over the table the field claims to describe — the two agree, or the report is wrong about
// itself. The figures are ALSO asserted as exact DELTAS against a reading taken before the fixture, because
// this package shares one database: the totals carry whatever the other tests left behind, and a delta says
// the report moved BY the right amount.
//
// THE THREE ARMS, each with a fixture a broken report cannot pass:
//
//  1. STATE VERSUS ACTIVITY. One refusal and its audit row are planted 120 DAYS before the window. It must
//     appear in the in-force figures — a refusal does not expire — and must not appear in the windowed ones,
//     so the deltas differ by exactly that row (3 blocks in force moved, 2 recorded in the window; 2 refusals
//     recorded rather than 3). A report that dropped its window, or that merged the two kinds of number,
//     fails this in one direction or the other.
//  2. A RELEASE IS NOT A NEW REFUSAL, AND NOT DOUBLE. The fixture blocks four hosts (one of which is then
//     released) and grants two appeals — and a grant performs a release. A correct report therefore counts
//     FIVE refusals and THREE releases from those facts, not three and five. Five is what the first version of this query returned, and
//     this test is what caught it: an appeal's own decision row also carries `released = true`, so counting
//     every such row reported one release as two. The last assertion in the window block MEASURES that
//     difference (the same period counted without the action restriction is larger by exactly the two
//     grants), so the exclusion is evidence rather than a comment.
//  3. GRANTED IS NOT DENIED. Two granted appeals and one denied, so the deltas are 2 and 1: a swapped
//     mapping cannot produce both.

func moderationReport(t *testing.T) content.Report {
	t.Helper()
	report, err := dbtest.Factory().Content().TransparencyReport(t.Context())
	require.NoError(t, err)
	return report
}

// sqlCount is the test's own arithmetic: the figure the report should be showing.
func sqlCount(t *testing.T, sql string, args ...any) int64 {
	t.Helper()
	var count int64
	require.NoError(t, dbtest.DB().QueryRow(t.Context(), sql, args...).Scan(&count))
	return count
}

// plantOldBlock writes a refusal and its audit row OUTSIDE any retention window the test configures, which is
// what arm 1 needs. Written directly because the subject is the AGE of the row, and no verb in this
// application can backdate a decision.
func plantOldBlock(t *testing.T, host string, ageDays int) {
	t.Helper()
	aged := time.Now().UTC().AddDate(0, 0, -ageDays)

	var blockID, auditID uuid.UUID
	require.NoError(t, dbtest.DB().QueryRow(t.Context(),
		`INSERT INTO content_source_block (kind, value, reason, created_at) VALUES ('host', $1, $2, $3) RETURNING id`,
		host, "planted outside the window", aged).Scan(&blockID))
	require.NoError(t, dbtest.DB().QueryRow(t.Context(),
		`INSERT INTO mod_audit (id, action, target_type, target_id, data, created_at)
		 VALUES (gen_random_uuid(), 'SOURCE_BLOCK', 'SOURCE_BLOCK', $1, '{}'::jsonb, $2)
		 RETURNING id`, blockID, aged).Scan(&auditID))

	// BY ID, not by shape: a cleanup that deleted "old audit rows" would delete whatever else the suite (or a
	// walk) had planted for the same reason, and a fixture that removes somebody else's evidence is worse than
	// one that leaves its own.
	t.Cleanup(func() {
		_, _ = dbtest.DB().Exec(context.Background(), `DELETE FROM content_source_block WHERE id = $1`, blockID)
		_, _ = dbtest.DB().Exec(context.Background(), `DELETE FROM mod_audit WHERE id = $1`, auditID)
	})
}

// plantSourceRecords writes one catalogue record OUT OF SERVICE and one still in service.
//
// Planted directly, and the reason is the subject: what is under test here is the REPORT's reading of
// `content_sources.status`, not the takedown verb's marking of it — that verb has its own suite, and a fixture
// that went through it would make this test fail whenever THAT behaviour changed for an unrelated reason. Two
// rows rather than one, too, because "out of service" is a SUBSET of the table: with only a dmca row present, a
// report that counted every record would be indistinguishable from a correct one, and a mutation doing exactly
// that survived this test until the second row existed.
func plantSourceRecords(t *testing.T, outOfServiceURL, liveURL string) {
	t.Helper()
	var outOfServiceID, liveID uuid.UUID
	require.NoError(t, dbtest.DB().QueryRow(t.Context(),
		`INSERT INTO content_sources (entity_type, entity_id, url, url_normalized, status)
		 VALUES ('scene', gen_random_uuid(), $1, LOWER($1), 'dmca') RETURNING id`,
		outOfServiceURL).Scan(&outOfServiceID))
	require.NoError(t, dbtest.DB().QueryRow(t.Context(),
		`INSERT INTO content_sources (entity_type, entity_id, url, url_normalized, status)
		 VALUES ('scene', gen_random_uuid(), $1, LOWER($1), 'live') RETURNING id`,
		liveURL).Scan(&liveID))
	t.Cleanup(func() {
		for _, id := range []uuid.UUID{outOfServiceID, liveID} {
			_, _ = dbtest.DB().Exec(context.Background(), `DELETE FROM content_sources WHERE id = $1`, id)
		}
	})
}

// setModAuditRetention sets the retention for the duration of the test.
//
// The window is read from the configuration rather than passed in, deliberately: an instance has ONE
// retention, and a report that could be asked for a different one than the cleanup uses would be reporting a
// period the database does not actually hold.
func setModAuditRetention(t *testing.T, days int) {
	t.Helper()
	previous := config.C.ModAuditRetentionDays
	t.Cleanup(func() { config.C.ModAuditRetentionDays = previous })
	config.C.ModAuditRetentionDays = days
}

// mustBlockHost refuses a host through the operator's own verb and returns the key it recorded, so a fixture
// failure is reported where it happened instead of as a wrong number three assertions later.
func mustBlockHost(t *testing.T, r *testRunner, host string) string {
	t.Helper()
	result, err := blockContentHost(t, r, host, "report fixture")
	require.NoError(t, err)
	return result.Block.Value
}

func TestTheTransparencyReportRecomputesEveryFigureFromTheInstancesOwnTables(t *testing.T) {
	fetchSourcesForTest(t)
	setModAuditRetention(t, 30)

	// BEFORE ANYTHING, see this file's comment on why every arm below is a delta.
	before := moderationReport(t)
	// And before anything in SQL too: the double-count arm below is measured as a delta of the UNRESTRICTED
	// count, because the absolute one carries every other test's granted appeals. The first version of this
	// assertion compared a global count against a fixture-scoped "+2" and passed only when this test ran
	// alone -- measured, in the full-package run, where the difference was 4.
	beforeUnrestricted := sqlCount(t,
		`SELECT COUNT(*) FROM mod_audit WHERE (data ->> 'released') = 'true' AND created_at >= $1`, before.Since)

	// Arm 1's fixture: a refusal and its audit row aged well past the window.
	plantOldBlock(t, "report-old-"+uuid.Must(uuid.NewV4()).String()[:8]+".invalid", 120)

	suffix := uuid.Must(uuid.NewV4()).String()[:8]
	plantSourceRecords(t,
		"https://report-out-of-service-"+suffix+".invalid/watch",
		"https://report-in-service-"+suffix+".invalid/watch")

	blocker := asAdmin(t)

	// Two refusals inside the window: a URL taken down, and a host blocked and then released. Plus the three
	// appeals below, two of them granted — and a grant releases its block, which is where the third release
	// comes from (arm 2).
	_, err := takedownContentURL(t, blocker,
		"https://report-refused-"+uuid.Must(uuid.NewV4()).String()[:8]+".invalid/watch", "report fixture")
	require.NoError(t, err)

	releasedHost := mustBlockHost(t, blocker, "report-released-"+uuid.Must(uuid.NewV4()).String()[:8]+".invalid")
	released, err := releaseContentBlock(t, blocker, "host", releasedHost)
	require.NoError(t, err)
	require.True(t, released)

	// Arm 3's fixture: the filer is a member, the decider is somebody else — the rule phase 14 enforces, and
	// the reason this test can create decisions at all.
	filer := asRead(t)
	decider := asAdminUser(t, createAdminUser(t, "report-decider"))

	for i := 0; i < 2; i++ {
		host := mustBlockHost(t, blocker, "report-grant-"+uuid.Must(uuid.NewV4()).String()[:8]+".invalid")
		appeal, err := fileContentAppeal(t, filer, "host", host, "report fixture")
		require.NoError(t, err)
		_, err = decideContentAppeal(t, decider, appeal.ID, "granted", "report fixture")
		require.NoError(t, err)
	}

	deniedHost := mustBlockHost(t, blocker, "report-deny-"+uuid.Must(uuid.NewV4()).String()[:8]+".invalid")
	deniedAppeal, err := fileContentAppeal(t, filer, "host", deniedHost, "report fixture")
	require.NoError(t, err)
	_, err = decideContentAppeal(t, decider, deniedAppeal.ID, "denied", "report fixture")
	require.NoError(t, err)

	report := moderationReport(t)

	// --- THE DELTAS: EXACTLY WHAT THE FIXTURE WAS SUPPOSED TO MOVE -------------------------------------
	// Arm 1 as arithmetic: THREE blocks are in force that were not before (the URL, the denied appeal's host,
	// and the row planted 120 days ago — a refusal does not expire), while only TWO were recorded inside the
	// window (the old row's creation is outside it). Likewise TWO refusals were recorded in the window, not
	// three. A report that ignored the window would answer 3 to both.
	assert.Equal(t, int64(3), report.InForce.Blocks-before.InForce.Blocks,
		"three refusals in force: the URL, the denied appeal's host, and the row planted 120 days ago")
	assert.Equal(t, int64(2), report.Activity.BlocksRecorded-before.Activity.BlocksRecorded,
		"and only two recorded INSIDE the window: the old row's creation is outside it")
	// FIVE, and the count is readable only if you know what a refusal record is: the URL's takedown, plus
	// FOUR host blocks — the released one and the three appeals' hosts. A block IS a refusal, and a release
	// does not erase it: the release is a separate row beside it, which is exactly why refusals (5) and
	// releases (3) are different numbers and why a report that counted one for the other could not pass this.
	assert.Equal(t, int64(5), report.Activity.Refusals-before.Activity.Refusals,
		"five refusals recorded in the window: the URL's takedown and four host blocks (the released one and "+
			"the three appeals' hosts). The planted 120-day-old refusal is NOT among them even though it is "+
			"still in force, which is the whole difference between the two blocks of figures")
	assert.Equal(t, int64(3), report.Activity.Releases-before.Activity.Releases,
		"three releases: the explicit one, plus one for each GRANTED appeal — a grant performs a release. It "+
			"is 3 and not 5 because an appeal's own decision row is not counted as a second release (arm 2)")
	assert.Equal(t, int64(3), report.Activity.AppealsFiled-before.Activity.AppealsFiled, "three appeals filed")
	assert.Equal(t, int64(3), report.Activity.AppealsDecided-before.Activity.AppealsDecided, "all three decided")
	assert.Equal(t, int64(3), report.InForce.AppealsFiled-before.InForce.AppealsFiled, "three filed, all time")
	assert.Equal(t, int64(2), report.InForce.AppealsGranted-before.InForce.AppealsGranted, "two granted")
	assert.Equal(t, int64(1), report.InForce.AppealsDenied-before.InForce.AppealsDenied, "one denied")

	// --- IN FORCE NOW: STATE, NO WINDOW -----------------------------------------------------------------
	assert.Equal(t,
		sqlCount(t, `SELECT COUNT(*) FROM content_source_block`),
		report.InForce.Blocks,
		"every refusal in force, including the one planted 120 days ago: a refusal does not expire")
	assert.Equal(t, int64(1), report.InForce.RecordsOutOfService-before.InForce.RecordsOutOfService,
		"one record was taken out of service by the fixture, and the figure moved by exactly that")
	// THE FIGURE'S SUBJECT IS PINNED BY THE FIXTURE. "Out of service" is a SUBSET of the table, and a report
	// that counted every record would pass an absolute comparison whenever the two agreed -- a mutation making
	// the query count the whole table survived exactly that way until the fixture planted a record of each
	// kind. A failure here says the fixture stopped being able to tell the two apart.
	require.GreaterOrEqual(t, sqlCount(t, `SELECT COUNT(*) FROM content_sources WHERE status = 'dmca'`), int64(1),
		"the fixture must have put at least one record out of service")
	require.Greater(t, sqlCount(t, `SELECT COUNT(*) FROM content_sources`),
		sqlCount(t, `SELECT COUNT(*) FROM content_sources WHERE status = 'dmca'`),
		"and there must be a record still IN service, or 'out of service' would be indistinguishable from 'all'")
	assert.Equal(t,
		sqlCount(t, `SELECT COUNT(*) FROM content_sources WHERE status = 'dmca'`),
		report.InForce.RecordsOutOfService,
		"catalogue records taken out of service, which is a different fact from a refusal")
	assert.Equal(t,
		sqlCount(t, `SELECT COUNT(*) FROM content_source_block_appeal`),
		report.InForce.AppealsFiled, "appeals filed, all time")
	assert.Equal(t,
		sqlCount(t, `SELECT COUNT(*) FROM content_source_block_appeal WHERE decided_at IS NULL`),
		report.InForce.AppealsPending, "and how many are still waiting to be heard")
	assert.Equal(t,
		sqlCount(t, `SELECT COUNT(*) FROM content_source_block_appeal WHERE decided_at IS NOT NULL`),
		report.InForce.AppealsDecided, "and how many were decided")
	assert.Equal(t,
		sqlCount(t, `SELECT COUNT(*) FROM content_source_block_appeal WHERE outcome = 'granted'`),
		report.InForce.AppealsGranted, "of which granted")
	assert.Equal(t,
		sqlCount(t, `SELECT COUNT(*) FROM content_source_block_appeal WHERE outcome = 'denied'`),
		report.InForce.AppealsDenied, "and denied")

	// --- IN THE WINDOW: ACTIVITY ------------------------------------------------------------------------
	assert.Equal(t, 30, report.WindowDays,
		"the window IS the audit retention: an activity count cannot see further back than the rows exist")
	assert.True(t, report.RetentionBounded, "and with a positive retention it is a limit rather than the history")
	assert.False(t, report.RecordsKeptForever)

	assert.Equal(t,
		sqlCount(t, `SELECT COUNT(*) FROM mod_audit WHERE action IN ('SOURCE_TAKEDOWN','SOURCE_BLOCK')
		               AND (data ->> 'released') IS DISTINCT FROM 'true' AND created_at >= $1`, report.Since),
		report.Activity.Refusals,
		"refusals recorded in the window — the planted 120-day-old audit row is OUTSIDE it (arm 1)")
	assert.Equal(t,
		sqlCount(t, `SELECT COUNT(*) FROM mod_audit WHERE action IN ('SOURCE_TAKEDOWN','SOURCE_BLOCK')
		               AND (data ->> 'released') = 'true' AND created_at >= $1`, report.Since),
		report.Activity.Releases,
		"releases in the window, restricted to the block-side actions (arm 2)")
	assert.Equal(t,
		report.Activity.Releases-before.Activity.Releases+int64(2),
		sqlCount(t, `SELECT COUNT(*) FROM mod_audit WHERE (data ->> 'released') = 'true' AND created_at >= $1`,
			report.Since)-beforeUnrestricted,
		"and the FIXTURE's share of the same period counted WITHOUT that restriction is larger by exactly the "+
			"two granted appeals' decision rows — the double-count this query excludes, measured rather than "+
			"asserted. A DELTA, because the absolute count carries every other test's granted appeals too")
	assert.Equal(t,
		sqlCount(t, `SELECT COUNT(*) FROM content_source_block WHERE created_at >= $1`, report.Since),
		report.Activity.BlocksRecorded, "newly refused addresses and hosts in the window")
	assert.Equal(t,
		sqlCount(t, `SELECT COUNT(*) FROM content_source_block_appeal WHERE filed_at >= $1`, report.Since),
		report.Activity.AppealsFiled, "appeals filed in the window")
	assert.Equal(t,
		sqlCount(t, `SELECT COUNT(*) FROM content_source_block_appeal
		               WHERE decided_at IS NOT NULL AND decided_at >= $1`, report.Since),
		report.Activity.AppealsDecided, "appeals decided in the window")

	// --- THE WINDOW IS THE RETENTION, AND SAYS SO WHEN THERE IS NONE ------------------------------------
	// The SAME rows read with no retention: the planted old row now counts, because with retention off
	// nothing is ever deleted and the window covers everything the instance holds.
	setModAuditRetention(t, 0)
	unbounded := moderationReport(t)
	require.True(t, unbounded.RecordsKeptForever,
		"a retention of 0 means rows are never deleted, and the report says so rather than implying a window")
	assert.False(t, unbounded.RetentionBounded)
	assert.Greater(t, unbounded.WindowDays, report.WindowDays,
		"and the nominal window is wider, so the figures reach back further")
	assert.Equal(t, int64(1), unbounded.Activity.Refusals-report.Activity.Refusals,
		"exactly the one planted 120-day-old refusal becomes visible -- the SAME rows, two different answers, "+
			"which is what makes the window a real filter rather than a decoration")

	// A NEGATIVE retention is treated as off, like every other `> 0` guard in this repository, and not as a
	// window that ends in the future (which would count nothing at all).
	setModAuditRetention(t, -1)
	negative := moderationReport(t)
	assert.True(t, negative.RecordsKeptForever)
	assert.Equal(t, unbounded.Activity.Refusals, negative.Activity.Refusals)
}
