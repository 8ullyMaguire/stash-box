//go:build integration

package api_test

import (
	"context"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/models"
)

// Migration 76: user_trust and trust_events (SPEC §6 trust levels).
//
// These verify the SCHEMA, not the service — the service lands with the
// threshold table. A migration is the one deliverable where a passing service
// test can hide a broken constraint, so the constraints are asserted directly
// against the database.
//
// The one that matters most is the dedup index, and it is the one whose naive
// form is silently wrong: entity_type and entity_id are nullable, and in a plain
// UNIQUE index Postgres treats NULL as distinct from NULL. So the obvious
// implementation — a plain unique index on (user_id, kind, entity_type,
// entity_id) — would let an entity-less event be inserted again on every retry,
// double-counting trust for exactly the events most likely to be retried. The
// index is therefore NULLS NOT DISTINCT (PostgreSQL 15+; the project targets 18).
//
// TestTrustEventsDeduplicateEntitylessEvents is the test that proves it.

// trustDB runs a query against the test database with the boilerplate removed.
//
// pgx takes a context as the first argument to every call, and threading it
// through each assertion would bury the SQL. One helper keeps the assertions
// readable; there is no equivalent elsewhere in the repo because no other test
// needs raw SQL.
func trustDB(t *testing.T) func(sql string, args ...any) pgx.Row {
	t.Helper()
	db := testutil.DB()
	require.NotNil(t, db, "testutil.DB() is nil: this test must run under TestWithDatabase")
	return func(sql string, args ...any) pgx.Row {
		return db.QueryRow(context.Background(), sql, args...)
	}
}

// trustCount returns the number of rows matching a count query.
func trustCount(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, trustDB(t)(sql, args...).Scan(&n), sql)
	return n
}

// A user_trust row must be creatable with no trust at all, and level 0.
func TestUserTrustDefaultsToLevelZero(t *testing.T) {
	admin := asAdmin(t)
	user, err := admin.createTestUser(nil, nil)
	require.NoError(t, err)

	var level int
	var approved, rejected, solves, quests, replicas int
	var optIn bool

	err = trustDB(t)(
		`SELECT level, approved_edits, rejected_edits, identification_solves,
		        quests_completed, replicas_hosted, content_viewing_opt_in
		   FROM user_trust WHERE user_id = $1`, user.ID).
		Scan(&level, &approved, &rejected, &solves, &quests, &replicas, &optIn)

	require.Error(t, err, "a new user has no user_trust row until an event occurs")
	assert.ErrorIs(t, err, pgx.ErrNoRows,
		"trust must not be auto-created for every user: the row is a rollup "+
			"that exists once something has happened, and pre-creating it for "+
			"the whole user table would make 'has this user earned anything' "+
			"unanswerable")
}

// After an event, the rollup row exists and starts at the implied level.
func TestUserTrustRowIsCreatable(t *testing.T) {
	admin := asAdmin(t)
	user, err := admin.createTestUser(nil, nil)
	require.NoError(t, err)

	var inserted bool
	err = trustDB(t)(
		`INSERT INTO user_trust (user_id, level)
		 VALUES ($1, 0)
		 ON CONFLICT (user_id) DO NOTHING
		 RETURNING TRUE`, user.ID).Scan(&inserted)

	require.NoError(t, err, "user_trust must be insertable for a real user")
	assert.True(t, inserted, "the first insert must win, not be swallowed by the conflict clause")

	var level int
	require.NoError(t, trustDB(t)(
		`SELECT level FROM user_trust WHERE user_id = $1`, user.ID).Scan(&level))
	assert.Equal(t, 0, level, "a fresh rollup row is level 0")
}

// The dedup guard, for an event that DOES reference an entity.
func TestTrustEventsDeduplicateEntityEvents(t *testing.T) {
	admin := asAdmin(t)
	user, err := admin.createTestUser(nil, nil)
	require.NoError(t, err)

	const (
		kind       = "edit_approved"
		entityType = "performer"
	)
	entityID := user.ID // any UUID; the constraint does not care what it points at

	// Returns nil if the row was inserted, and an error if the dedup index
	// rejected it. ON CONFLICT DO NOTHING means the conflict surfaces as
	// "no rows returned", which Scan reports as pgx.ErrNoRows.
	insert := func() error {
		return trustDB(t)(
			`INSERT INTO trust_events (user_id, kind, entity_type, entity_id, delta)
			 VALUES ($1, $2, $3, $4, 1)
			 ON CONFLICT DO NOTHING
			 RETURNING id`, user.ID, kind, entityType, entityID).Scan(new(int64))
	}

	require.NoError(t, insert(), "the first event must insert")

	require.Error(t, insert(), "the identical event must be rejected by the unique index, "+
		"otherwise a retried apply double-counts trust")

	assert.Equal(t, 1, trustCount(t, `SELECT count(*) FROM trust_events WHERE user_id = $1`, user.ID),
		"exactly one row must exist after two identical inserts (#76 dedup index)")
}

// The dedup guard, for an event with NO entity — the case a plain UNIQUE index
// gets wrong, because NULL != NULL in Postgres.
func TestTrustEventsDeduplicateEntitylessEvents(t *testing.T) {
	admin := asAdmin(t)
	user, err := admin.createTestUser(nil, nil)
	require.NoError(t, err)

	const kind = "manual_adjustment"

	insert := func() error {
		return trustDB(t)(
			`INSERT INTO trust_events (user_id, kind, delta)
			 VALUES ($1, $2, 1)
			 ON CONFLICT DO NOTHING
			 RETURNING id`, user.ID, kind).Scan(new(int64))
	}

	require.NoError(t, insert(), "the first entity-less event must insert")

	require.Error(t, insert(),
		"an entity-less event must ALSO be deduplicated; a plain UNIQUE index "+
			"treats the NULL entity columns as distinct and would let this "+
			"insert a second time on every retry (#76 requires NULLS NOT DISTINCT)")

	assert.Equal(t, 1, trustCount(t, `SELECT count(*) FROM trust_events WHERE user_id = $1`, user.ID),
		"exactly one row must exist after two identical entity-less inserts")
}

// Different kinds for the same user are different events, and must not collide.
func TestTrustEventsDistinguishKinds(t *testing.T) {
	admin := asAdmin(t)
	user, err := admin.createTestUser(nil, nil)
	require.NoError(t, err)

	for _, kind := range []string{"edit_approved", "edit_rejected", "quest_completed"} {
		err := trustDB(t)(
			`INSERT INTO trust_events (user_id, kind, delta) VALUES ($1, $2, 1)
			 ON CONFLICT DO NOTHING RETURNING id`, user.ID, kind).Scan(new(int64))
		require.NoError(t, err, "distinct kinds must each insert: %s", kind)
	}

	assert.Equal(t, 3, trustCount(t, `SELECT count(*) FROM trust_events WHERE user_id = $1`, user.ID),
		"three distinct kinds must produce three events")
}

// A negative delta must be storable: rejections and reversals are how trust is
// lost, and a schema that only accepted positive deltas could never revoke one.
func TestTrustEventsAcceptNegativeDelta(t *testing.T) {
	admin := asAdmin(t)
	user, err := admin.createTestUser(nil, nil)
	require.NoError(t, err)

	var delta int
	require.NoError(t, trustDB(t)(
		`INSERT INTO trust_events (user_id, kind, entity_type, entity_id, delta)
		 VALUES ($1, 'edit_rejected', 'performer', $2, -1)
		 RETURNING delta`, user.ID, user.ID).Scan(&delta))

	assert.Equal(t, -1, delta, "trust must be losable: a rejected edit is a -1 event")
}

// Deleting a user must take their trust with it, or the rollup outlives the
// account it describes.
func TestUserTrustCascadesOnUserDelete(t *testing.T) {
	admin := asAdmin(t)
	user, err := admin.createTestUser(nil, nil)
	require.NoError(t, err)

	require.NoError(t, trustDB(t)(
		`INSERT INTO trust_events (user_id, kind, delta)
		 VALUES ($1, 'quest_completed', 1) RETURNING id`, user.ID).Scan(new(int64)))
	require.NoError(t, trustDB(t)(
		`INSERT INTO user_trust (user_id, level) VALUES ($1, 0) RETURNING user_id`,
		user.ID).Scan(new(uuid.UUID)))

	_, err = admin.resolver.Mutation().UserDestroy(admin.ctx, models.UserDestroyInput{ID: user.ID})
	require.NoError(t, err)

	assert.Zero(t, trustCount(t, `SELECT count(*) FROM trust_events WHERE user_id = $1`, user.ID),
		"trust events must cascade on user delete")
	assert.Zero(t, trustCount(t, `SELECT count(*) FROM user_trust WHERE user_id = $1`, user.ID),
		"the trust rollup must cascade on user delete")
}
