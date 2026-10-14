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
