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

// THE SWEEP HAS NO API SURFACE YET (it is a cron job, slice 9b), so this drives the service directly.
// It lives in this package because this is where the migrated test database and its harness are.
type fakeChecker struct{ err error }

func (f fakeChecker) Check(context.Context, string) error { return f.err }

// recordingChecker answers like fakeChecker and remembers WHICH urls it was asked about, because the
// sweep's job is a decision about a row: "three sources were checked" is not the assertion worth
// making, "these three and not those two" is.
type recordingChecker struct {
	err     error
	checked []string
}

func (c *recordingChecker) Check(_ context.Context, url string) error {
	c.checked = append(c.checked, url)
	return c.err
}

// The status machine at the documented thresholds, driven through the real write statement.
//
// What this asserts that the unit table cannot: that the ROW ends up with the status the rule chose,
// that `last_ok` SURVIVES a failure (the only evidence a resurrection queue has to work from, and the
// reason the write statement uses COALESCE), and that a success resets the counter.
func TestCheckSourceMovesTheStatusAtTheDocumentedThresholds(t *testing.T) {
	db := dbtest.DB()
	svc := content.NewService(queries.New(db), nil)
	ctx := context.Background()
	now := time.Now()

	var id uuid.UUID
	require.NoError(t, db.QueryRow(ctx,
		`INSERT INTO content_sources (entity_type, entity_id, url) VALUES ('scene', $1, $2) RETURNING id`,
		uuid.Must(uuid.NewV4()), "https://example.invalid/health-"+uuid.Must(uuid.NewV4()).String()).Scan(&id))
	t.Cleanup(func() {
		_, err := db.Exec(ctx, `DELETE FROM content_sources WHERE id = $1`, id)
		require.NoError(t, err)
	})

	// 1. A success from `unknown` makes it live, and records when.
	source, err := svc.CheckSource(ctx, id, now, fakeChecker{})
	require.NoError(t, err)
	assert.Equal(t, content.StatusLive, source.Status)
	require.NotNil(t, source.LastOK, "a success records WHEN it was alive")
	firstOK := *source.LastOK
	assert.Equal(t, 0, source.CheckFailures)

	// 2. Two failures are not enough; the third is, at the migration's threshold.
	//
	//    THE CLOCK ADVANCES between the success and the failures, and it has to: with a constant clock a
	//    driver that overwrote last_ok on a failure would write the same instant back and every
	//    assertion here would still pass. (It did, until a mutation survived and said so.)
	failing := fakeChecker{err: errors.New("connection refused")}
	for i := 1; i <= 2; i++ {
		source, err = svc.CheckSource(ctx, id, now.Add(time.Duration(i)*time.Hour), failing)
		require.NoError(t, err)
		assert.Equal(t, content.StatusLive, source.Status, "failure %d is short of intermittent", i)
		assert.Equal(t, i, source.CheckFailures)
	}
	source, err = svc.CheckSource(ctx, id, now.Add(3*time.Hour), failing)
	require.NoError(t, err)
	assert.Equal(t, content.StatusIntermittent, source.Status, "three consecutive failures")
	require.NotNil(t, source.LastOK)
	assert.WithinDuration(t, firstOK, *source.LastOK, time.Second,
		"a failure must not overwrite when the source was last alive -- it is the resurrection evidence")

	// 3. A success from intermittent is live again with the counter reset.
	source, err = svc.CheckSource(ctx, id, now.Add(time.Minute), fakeChecker{})
	require.NoError(t, err)
	assert.Equal(t, content.StatusLive, source.Status)
	assert.Equal(t, 0, source.CheckFailures, "a success resets the counter")
	require.NotNil(t, source.LastOK)
	assert.True(t, source.LastOK.After(firstOK), "and moves last_ok forward")

	// 4. Aged past the unreachable threshold, a failure is death -- and the clock is an argument
	//    precisely so this does not take a week.
	source, err = svc.CheckSource(ctx, id, now.Add(8*24*time.Hour), failing)
	require.NoError(t, err)
	assert.Equal(t, content.StatusDead, source.Status)

	// 5. A REFUSAL observes nothing: `last_checked` moves, and the status and counter do not. Walked
	//    through the driver rather than the checker so the "which column changed" question is real.
	//
	//    The count is compared against what it WAS, not against zero: a refusal must not increment it,
	//    and asserting zero would also pass for a driver that reset it -- a different bug.
	before := source.CheckFailures
	checkedBefore := time.Now()
	source, err = svc.CheckSource(ctx, id, now.Add(9*24*time.Hour), fakeChecker{err: content.ErrCheckInconclusive})
	require.NoError(t, err)
	assert.Equal(t, content.StatusDead, source.Status, "a refusal does not resurrect")
	require.NotNil(t, source.LastChecked, "but the check happened")
	assert.False(t, source.LastChecked.Before(checkedBefore), "and it is stamped when it happened")
	assert.Equal(t, before, source.CheckFailures, "and a refusal is not a failure either")

	// 6. A TERMINAL status is not moved by a success: whatever the URL answers, the instance has
	//    decided not to serve it.
	_, err = db.Exec(ctx, `UPDATE content_sources SET status = 'dmca' WHERE id = $1`, id)
	require.NoError(t, err)
	source, err = svc.CheckSource(ctx, id, now.Add(10*24*time.Hour), fakeChecker{})
	require.NoError(t, err)
	assert.Equal(t, content.StatusDMCA, source.Status, "a takedown is not resurrected by a 200")
}

// The sweep's selection and its cadence (slice 9b).
//
// What this asserts that a unit test cannot: that the query, the ordering and the due-check agree
// with each other. A sweep that fetched the right rows and then checked all of them would pass a
// "were any sources checked" test and hammer a site it was told to leave alone, so the assertion is
// the SET of urls checked, not the count alone.
func TestSweepChecksTheDueSourcesAndOnlyThose(t *testing.T) {
	db := dbtest.DB()
	svc := content.NewService(queries.New(db), nil)
	ctx := context.Background()
	now := time.Now()

	seed := func(url, status string, failures int, lastChecked *time.Time) uuid.UUID {
		var id uuid.UUID
		require.NoError(t, db.QueryRow(ctx, `
			INSERT INTO content_sources (entity_type, entity_id, url, status, check_failures, last_checked)
			VALUES ('scene', $1, $2, $3, $4, $5) RETURNING id`,
			uuid.Must(uuid.NewV4()), url, status, failures, lastChecked).Scan(&id))
		t.Cleanup(func() {
			_, err := db.Exec(ctx, `DELETE FROM content_sources WHERE id = $1`, id)
			require.NoError(t, err)
		})
		return id
	}

	prefix := "https://sweep-test.invalid/" + uuid.Must(uuid.NewV4()).String()
	at := func(d time.Duration) *time.Time { t := now.Add(-d); return &t }

	// Never checked: the NULLS FIRST case, and the only rows a fresh instance has.
	never := prefix + "/never"
	seed(never, content.StatusUnknown, 0, nil)
	// Two failures and last checked five days ago: interval is four days, so it is overdue.
	overdue := prefix + "/overdue"
	seed(overdue, content.StatusIntermittent, 2, at(5*24*time.Hour))
	// Reliable and checked thirty hours ago: interval is one day, so it is due by six hours.
	due := prefix + "/due"
	seed(due, content.StatusLive, 0, at(30*time.Hour))
	// Checked an hour ago: NOT due, and with a limit of three it is not even a candidate.
	recent := prefix + "/recent"
	seed(recent, content.StatusLive, 0, at(time.Hour))
	// DEAD is the resurrection queue's work, not the sweep's: it must not be touched here even
	// though its last_checked is null and it would otherwise look maximally overdue.
	dead := prefix + "/dead"
	seed(dead, content.StatusDead, 40, nil)

	checker := &recordingChecker{}
	checked, err := svc.Sweep(ctx, now, 3, checker)
	require.NoError(t, err)

	assert.Equal(t, 3, checked)
	assert.ElementsMatch(t, []string{never, overdue, due}, checker.checked,
		"the sweep must check the due sources and leave the recent one and the dead one alone")
	assert.NotContains(t, checker.checked, dead, "a dead source is the resurrection queue's, not the sweep's")
	assert.NotContains(t, checker.checked, recent, "a source checked an hour ago is not due")
}

// The bound is honoured, and the bound is what keeps one tick from holding the process while making
// third-party requests. Asserted by asking for fewer candidates than are due.
func TestSweepHonoursItsBatchLimit(t *testing.T) {
	db := dbtest.DB()
	svc := content.NewService(queries.New(db), nil)
	ctx := context.Background()
	now := time.Now()

	prefix := "https://sweep-limit-test.invalid/" + uuid.Must(uuid.NewV4()).String()
	for i := 0; i < 4; i++ {
		var id uuid.UUID
		require.NoError(t, db.QueryRow(ctx, `
			INSERT INTO content_sources (entity_type, entity_id, url, status, check_failures, last_checked)
			VALUES ('scene', $1, $2, 'live', 0, $3) RETURNING id`,
			uuid.Must(uuid.NewV4()), prefix+"/"+string(rune('a'+i)), now.Add(-48*time.Hour)).Scan(&id))
		t.Cleanup(func() {
			_, err := db.Exec(ctx, `DELETE FROM content_sources WHERE id = $1`, id)
			require.NoError(t, err)
		})
	}

	checker := &recordingChecker{}
	checked, err := svc.Sweep(ctx, now, 2, checker)
	require.NoError(t, err)
	assert.Equal(t, 2, checked, "one tick considers at most the batch it was given")
	assert.Len(t, checker.checked, 2)
}

// A candidate whose interval has NOT elapsed is fetched and skipped. This is the case the due-check
// exists for, and it is asserted on its own because the previous test reaches it only through the
// ordering: with nothing else due, a single recent source must be looked at and left alone.
func TestSweepLeavesARecentlyCheckedSourceAlone(t *testing.T) {
	db := dbtest.DB()
	svc := content.NewService(queries.New(db), nil)
	ctx := context.Background()
	now := time.Now()

	url := "https://sweep-skip-test.invalid/" + uuid.Must(uuid.NewV4()).String()
	var id uuid.UUID
	require.NoError(t, db.QueryRow(ctx, `
		INSERT INTO content_sources (entity_type, entity_id, url, status, check_failures, last_checked)
		VALUES ('scene', $1, $2, 'live', 0, $3) RETURNING id`,
		uuid.Must(uuid.NewV4()), url, now.Add(-time.Hour)).Scan(&id))
	t.Cleanup(func() {
		_, err := db.Exec(ctx, `DELETE FROM content_sources WHERE id = $1`, id)
		require.NoError(t, err)
	})

	checker := &recordingChecker{err: errors.New("this checker must never be called")}
	checked, err := svc.Sweep(ctx, now, 10, checker)
	require.NoError(t, err)
	assert.Equal(t, 0, checked)
	assert.Empty(t, checker.checked,
		"a source checked an hour ago is inside its interval, and calling the site anyway is the bug "+
			"the cadence exists to prevent")
}

// The resurrection queue (slice 9c): its own cadence, its own candidate list, and the one piece of
// evidence that the resurrection itself destroys.
func TestSweepResurrectionsBringsBackADeadSourceThatAnswers(t *testing.T) {
	db := dbtest.DB()
	svc := content.NewService(queries.New(db), nil)
	ctx := context.Background()
	now := time.Now()

	url := "https://resurrect-test.invalid/" + uuid.Must(uuid.NewV4()).String()
	lastAlive := now.Add(-50 * 24 * time.Hour)
	var id uuid.UUID
	require.NoError(t, db.QueryRow(ctx, `
		INSERT INTO content_sources (entity_type, entity_id, url, status, check_failures, last_checked, last_ok, first_seen)
		VALUES ('scene', $1, $2, 'dead', 12, $3, $4, $5) RETURNING id`,
		uuid.Must(uuid.NewV4()), url, now.Add(-40*24*time.Hour), lastAlive, now.Add(-200*24*time.Hour)).Scan(&id))
	t.Cleanup(func() {
		_, err := db.Exec(ctx, `DELETE FROM content_sources WHERE id = $1`, id)
		require.NoError(t, err)
	})

	resurrected, err := svc.SweepResurrections(ctx, now, 10, fakeChecker{})
	require.NoError(t, err)
	require.Len(t, resurrected, 1)
	assert.Equal(t, url, resurrected[0].URL)
	require.NotNil(t, resurrected[0].LastAliveAt)
	assert.WithinDuration(t, lastAlive, *resurrected[0].LastAliveAt, time.Second,
		"the gap is the evidence, and the successful check is about to overwrite the only record of it")

	var status string
	var failures int
	var lastOK time.Time
	require.NoError(t, db.QueryRow(ctx,
		`SELECT status, check_failures, last_ok FROM content_sources WHERE id = $1`, id).
		Scan(&status, &failures, &lastOK))
	assert.Equal(t, content.StatusLive, status, "it answered, so it is live")
	assert.Equal(t, 0, failures, "and the counter resets with the resurrection")
	assert.True(t, lastOK.After(lastAlive), "last_ok now means 'alive as of this check'")
}

func TestSweepResurrectionsLeavesARecentlyDeadSourceAlone(t *testing.T) {
	db := dbtest.DB()
	svc := content.NewService(queries.New(db), nil)
	ctx := context.Background()
	now := time.Now()

	url := "https://resurrect-recent.invalid/" + uuid.Must(uuid.NewV4()).String()
	var id uuid.UUID
	require.NoError(t, db.QueryRow(ctx, `
		INSERT INTO content_sources (entity_type, entity_id, url, status, check_failures, last_checked)
		VALUES ('scene', $1, $2, 'dead', 12, $3) RETURNING id`,
		uuid.Must(uuid.NewV4()), url, now.Add(-2*24*time.Hour)).Scan(&id))
	t.Cleanup(func() {
		_, err := db.Exec(ctx, `DELETE FROM content_sources WHERE id = $1`, id)
		require.NoError(t, err)
	})

	checker := &recordingChecker{}
	resurrected, err := svc.SweepResurrections(ctx, now, 10, checker)
	require.NoError(t, err)
	assert.Empty(t, resurrected)
	assert.Empty(t, checker.checked,
		"a dead source is asked about once a month: asking a site that has been gone for two days "+
			"every tick is exactly what this cadence exists to stop")
}

// A dead source that is asked and still does not answer stays dead, keeps its counter, and keeps the
// date it was last alive. The alternative -- resetting anything on a failed retry -- would slowly
// erase the record that this link ever worked.
func TestSweepResurrectionsKeepsAFailingSourceDead(t *testing.T) {
	db := dbtest.DB()
	svc := content.NewService(queries.New(db), nil)
	ctx := context.Background()
	now := time.Now()

	url := "https://resurrect-still-dead.invalid/" + uuid.Must(uuid.NewV4()).String()
	lastAlive := now.Add(-90 * 24 * time.Hour)
	var id uuid.UUID
	require.NoError(t, db.QueryRow(ctx, `
		INSERT INTO content_sources (entity_type, entity_id, url, status, check_failures, last_checked, last_ok)
		VALUES ('scene', $1, $2, 'dead', 12, $3, $4) RETURNING id`,
		uuid.Must(uuid.NewV4()), url, now.Add(-40*24*time.Hour), lastAlive).Scan(&id))
	t.Cleanup(func() {
		_, err := db.Exec(ctx, `DELETE FROM content_sources WHERE id = $1`, id)
		require.NoError(t, err)
	})

	resurrected, err := svc.SweepResurrections(ctx, now, 10, fakeChecker{err: errors.New("still gone")})
	require.NoError(t, err)
	assert.Empty(t, resurrected)

	var status string
	var failures int
	var lastOK time.Time
	require.NoError(t, db.QueryRow(ctx,
		`SELECT status, check_failures, last_ok FROM content_sources WHERE id = $1`, id).
		Scan(&status, &failures, &lastOK))
	assert.Equal(t, content.StatusDead, status, "dead is sticky: only answering leaves it")
	assert.Greater(t, failures, 12, "the failed attempt is counted")
	assert.WithinDuration(t, lastAlive, lastOK, time.Second,
		"and the date it was last alive survives the attempt")
}
