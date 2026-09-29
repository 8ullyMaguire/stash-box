//go:build integration

package api_test

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/database/testutil"
)

// Migration 77 (Elo) applies, and its constraints actually bite.
//
// The CHECK constraints are the point. Without them this table accepts a
// performer voting against itself, which is a free +1 to a rating and a
// guaranteed way to top a leaderboard -- so each constraint gets a test that
// fails when the constraint is dropped, rather than a test that only proves the
// migration ran.

func TestEloMigrationApplied(t *testing.T) {
	var version int
	var dirty bool
	err := testutil.DB().QueryRow(t.Context(),
		`SELECT version, dirty FROM schema_migrations`).Scan(&version, &dirty)
	require.NoError(t, err)
	assert.False(t, dirty, "the schema must not be left mid-migration")
	assert.GreaterOrEqual(t, version, 77,
		"migration 77 must be applied; a lower version means this test is "+
			"passing against a schema that lacks the tables it goes on to use")
}

// Existing performers are seeded with a default rating at migration time.
//
// This one test is worth the trouble of re-running the migration by hand,
// because the alternative is asserting nothing. The first version created a
// performer AFTER the migration had run and asserted it had NO rating row --
// which passes, and stays passing, with the seeding INSERT deleted entirely
// (mutation-checked: the no-op mutant is green).
//
// The seeding is a real behaviour: a performer that predates the migration
// appears in every leaderboard at 1500 from day one rather than being invisible
// until somebody votes. To observe it, the migration must run when a performer
// already exists, so the test drops the elo tables, inserts a performer, and
// re-applies the migration file. It restores the tables afterwards so the rest
// of the suite is unaffected.
func TestEloRatingsSeededForExistingPerformers(t *testing.T) {
	migration := readMigration(t, "77_add_elo_ratings.up.sql")

	// Tear the elo tables down, leaving the rest of the schema alone.
	_, err := testutil.DB().Exec(t.Context(),
		`DROP TABLE IF EXISTS elo_votes, elo_ratings, taste_vectors CASCADE`)
	require.NoError(t, err, "precondition: the elo tables exist and can be dropped")

	// A performer that predates the migration.
	existing := createEloPerformer(t)

	_, err = testutil.DB().Exec(t.Context(), migration)
	require.NoError(t, err, "the migration must apply cleanly to a database that "+
		"already has performers; if it does not, the migration is not idempotent "+
		"against real data")

	var rating int
	var deviation float64
	err = testutil.DB().QueryRow(t.Context(),
		`SELECT rating, deviation FROM elo_ratings
		 WHERE entity_type = 'performer' AND entity_id = $1`, existing).
		Scan(&rating, &deviation)
	require.NoError(t, err,
		"a performer that existed BEFORE the migration must be seeded with a "+
			"default rating, so it appears in leaderboards before anyone votes")

	assert.Equal(t, 1500, rating)
	assert.Greater(t, deviation, 200.0, "a seeded rating must be uncertain, not "+
		"confident: nobody has voted on it yet")
}

// readMigration loads a migration file from disk.
//
// The pattern is already used by the search-ranking tests, so this is not a new
// convention. The path is derived from the test file's own location rather than
// the working directory, because `go test` runs from the package directory and a
// relative path silently breaks if that ever changes.
func readMigration(t *testing.T, name string) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller must work to locate the migration directory")
	path := filepath.Join(filepath.Dir(thisFile), "..", "database", "migrations", "postgres", name)
	data, err := os.ReadFile(path)
	require.NoError(t, err, "reading migration %s from %s", name, path)
	return string(data)
}

// A default rating is 1500 with a wide deviation.
//
// A narrow default deviation would make an unvoted performer look like a
// well-observed one, and every confidence filter downstream would be wrong
// before a single vote is cast.
func TestEloDefaultRatingIs1500WithWideDeviation(t *testing.T) {
	// Insert the rating row directly rather than relying on migration-time
	// seeding, which has nothing to seed on a fresh database.
	fresh := createEloPerformer(t)
	_, err := testutil.DB().Exec(t.Context(),
		`INSERT INTO elo_ratings (entity_type, entity_id) VALUES ('performer', $1)`,
		fresh)
	require.NoError(t, err)

	var rating int
	var deviation float64
	err = testutil.DB().QueryRow(t.Context(),
		`SELECT rating, deviation FROM elo_ratings
		 WHERE entity_type = 'performer' AND entity_id = $1`, fresh).Scan(&rating, &deviation)
	require.NoError(t, err)

	assert.Equal(t, 1500, rating, "the Glicko starting rating is 1500")
	assert.Greater(t, deviation, 200.0,
		"the default deviation must be wide (Glicko uses 350), or an unvoted "+
			"entity looks as well-observed as a heavily-voted one")
}

// A vote between two different entities is accepted.
func TestEloVoteAcceptsDistinctParticipants(t *testing.T) {
	voter, err := asAdmin(t).createTestUser(nil, nil)
	require.NoError(t, err)

	_, err = insertEloVote(t, voter.ID, distinctEloEntities(t), 0)
	require.NoError(t, err, "a vote between two distinct performers must be accepted")
}

// A vote between an entity and itself is rejected.
//
// This is a free +1 to a rating if it is allowed, and a guaranteed way to reach
// the top of a leaderboard. The CHECK constraint is the only thing stopping it.
func TestEloVoteRejectsSelfVote(t *testing.T) {
	voter, err := asAdmin(t).createTestUser(nil, nil)
	require.NoError(t, err)

	entity := distinctEloEntities(t)[0]
	_, err = insertEloVote(t, voter.ID, [2]string{entity, entity}, 0)
	require.Error(t, err,
		"a performer voted against itself must be rejected: it is a free +1 to "+
			"the rating and a guaranteed way to top a leaderboard. The CHECK "+
			"constraint elo_votes_distinct_participants is the only guard.")
}

// picked_side outside {0, 1} is rejected.
func TestEloVoteRejectsInvalidSide(t *testing.T) {
	voter, err := asAdmin(t).createTestUser(nil, nil)
	require.NoError(t, err)

	_, err = insertEloVote(t, voter.ID, distinctEloEntities(t), 7)
	require.Error(t, err,
		"picked_side must be 0 or 1; anything else is a bug, not a future "+
			"extension point, and the CHECK constraint says so")
}

// Mixing entity types in one matchup is rejected.
//
// A performer rated against a studio has no shared scale to update, so the
// rating would be meaningless rather than merely wrong.
func TestEloVoteRejectsMixedEntityTypes(t *testing.T) {
	voter, err := asAdmin(t).createTestUser(nil, nil)
	require.NoError(t, err)

	_, err = testutil.DB().Exec(t.Context(),
		`INSERT INTO elo_votes (id, user_id, winner_id, loser_id, winner_type,
		                        loser_type, picked_side)
		 VALUES (gen_random_uuid(), $1, gen_random_uuid(), gen_random_uuid(),
		         'performer', 'studio', 0)`,
		voter.ID)
	require.Error(t, err,
		"a performer rated against a studio has no shared scale, so the rating "+
			"is meaningless; elo_votes_same_entity_type must reject it")
}

// distinctEloEntities returns two distinct, currently-unrated performers.
//
// Unrated on purpose: the rating service asserts on the delta, and reusing a
// performer that an earlier test already voted on would make the expected
// movement depend on test order. Two fresh performers keep the migration tests
// independent of the service tests.
func distinctEloEntities(t *testing.T) [2]string {
	t.Helper()
	ids := make([]string, 2)
	for i := range ids {
		row := testutil.DB().QueryRow(t.Context(),
			`INSERT INTO performers (id, name, created_at, updated_at)
			 VALUES (gen_random_uuid(), $1, NOW(), NOW())
			 RETURNING id`,
			fmt.Sprintf("elo_migration_fixture_%s_%d", uuid.Must(uuid.NewV7()).String(), i))
		require.NoError(t, row.Scan(&ids[i]))
	}
	require.NotEqual(t, ids[0], ids[1], "the two entities must actually differ")
	return [2]string{ids[0], ids[1]}
}

// insertEloVote writes a raw elo_votes row, bypassing the service on purpose.
//
// The migration tests are about whether the DATABASE accepts a row, so they must
// not go through the service that will eventually validate the same thing --
// otherwise a service bug could make a constraint look like it works.
func insertEloVote(t *testing.T, userID uuid.UUID, entities [2]string, side int) (uuid.UUID, error) {
	// entities is a [2]string deliberately rather than two loose args: the
	// self-vote case is the one that matters most, and passing the same value
	// twice reads unmistakably at the call site.
	t.Helper()
	var id uuid.UUID
	err := testutil.DB().QueryRow(t.Context(),
		`INSERT INTO elo_votes (id, user_id, winner_id, loser_id, winner_type,
		                        loser_type, picked_side)
		 VALUES (gen_random_uuid(), $1, $2, $3, 'performer', 'performer', $4)
		 RETURNING id`,
		userID, entities[0], entities[1], side).Scan(&id)
	return id, err
}

// createEloPerformer inserts a bare performer and returns its id.
//
// Minimal columns on purpose: these tests are about the elo tables, and pulling
// in the full performer creation path would couple them to unrelated changes.
func createEloPerformer(t *testing.T) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := testutil.DB().QueryRow(t.Context(),
		`INSERT INTO performers (id, name, created_at, updated_at)
		 VALUES (gen_random_uuid(), $1, NOW(), NOW()) RETURNING id`,
		fmt.Sprintf("elo_fixture_%s", uuid.Must(uuid.NewV7()).String())).Scan(&id)
	require.NoError(t, err)
	return id
}
