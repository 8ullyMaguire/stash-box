//go:build integration

package api_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbtest "github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/identification"
)

// idUserSeq keeps fixture names unique within a test binary.
var idUserSeq atomic.Int64

// containsQueryID is a membership test over ids.
//
// assert.Contains on a slice of POINTERS compares pointer values, so it compares
// addresses and can never match -- which is what my first draft did, and it
// failed with a dump of addresses rather than a useful message.
func containsQueryID(queries []*identification.Query, id uuid.UUID) bool {
	for _, q := range queries {
		if q.ID == id {
			return true
		}
	}
	return false
}

// The identification board at the database level (SPEC §5).
//
// The unit tests cover the tie rule, which is pure arithmetic. What they cannot
// reach is the set of rules the DATABASE has to hold: the resolution/solved
// pairing, one-suggestion-per-entity, one-vote-per-user, and the fact that a vote
// is never authority.

// idService builds the service over the test database.
//
// Through the Factory, because that is the accessor production uses and a test
// that assembled the service by hand would not notice a broken accessor.
func idService(t *testing.T) *identification.Service {
	t.Helper()
	return dbtest.Factory().Identification()
}

// idUser creates a real user row.
//
// A real user rather than a random UUID, because the vote table has a foreign key
// to users. My first draft passed uuid.NewV7() as a voter and the failure was a
// foreign key violation rather than the behaviour under test -- which is the worst
// kind of test failure, because it looks like the service is broken. Every
// identity in this file therefore goes through here.
func idUser(t *testing.T) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	// A counter, not the uuid's own prefix. uuid.NewV7 is time-ordered, so
	// consecutive ids share their leading bytes and id.String()[:8] collided
	// across tests in the same millisecond -- which surfaced as a unique
	// violation on users.name and looked like a service bug.
	name := fmt.Sprintf("id-%d-%s", idUserSeq.Add(1), id.String()[8:12])
	_, err := queries.New(dbtest.DB()).CreateUser(t.Context(), queries.CreateUserParams{
		ID:    id,
		Name:  name,
		Email: name + "@example.com",
		// A hash is never verified here -- this user exists only to satisfy a
		// foreign key and to have an id to vote with.
		PasswordHash: "x",
		ApiKey:       id.String(),
		// InvitedBy stays NULL: nothing in this file asks who invited a voter, and
		// inventing an inviter would be a fixture for a fact that does not matter.
		InviteTokens: 1,
	})
	require.NoError(t, err, "creating a user to vote or resolve as")
	return id
}

// postQuery creates an open query as a given user.
func postQuery(t *testing.T, target identification.TargetType, description string, by uuid.NullUUID) *identification.Query {
	t.Helper()
	q, err := idService(t).Post(t.Context(), identification.PostQuery{
		TargetType:  target,
		Description: description,
		CreatedBy:   by,
	})
	require.NoError(t, err, "posting an identification query")
	return q
}

// suggestOne adds a candidate and returns it.
func suggestOne(t *testing.T, queryID uuid.UUID, entity identification.TargetType, entityID uuid.UUID, by uuid.NullUUID) *identification.Candidate {
	t.Helper()
	c, err := idService(t).Suggest(t.Context(), queryID, entity, entityID, by)
	require.NoError(t, err, "suggesting a candidate")
	return c
}

// A query needs a question. A description of whitespace is not one.
func TestPostRequiresADescription(t *testing.T) {
	s := idService(t)

	for _, blank := range []string{"", "   ", "\n\t "} {
		_, err := s.Post(t.Context(), identification.PostQuery{
			TargetType:  identification.TargetScene,
			Description: blank,
		})
		assert.ErrorIs(t, err, identification.ErrEmptyDescription,
			"%q asks nothing, and a query with no question in it can never be "+
				"answered", blank)
	}
}

// A text-only query is legitimate: §5 lists description, collage, snapshot,
// frame and quote as ALTERNATIVES, not as a form with required fields.
func TestPostAcceptsATextOnlyQuery(t *testing.T) {
	q := postQuery(t, identification.TargetPerformer,
		"brown hair, maybe blonde, a heart tattoo on her left ankle", uuid.NullUUID{})

	assert.Equal(t, identification.StatusOpen, q.Status)
	assert.Equal(t, identification.TargetPerformer, q.TargetType)
	assert.Nil(t, q.ResolvedID, "a fresh query is not resolved")
}

// A type this version does not know is refused, not stored.
func TestPostRejectsAnUnknownTargetType(t *testing.T) {
	_, err := idService(t).Post(t.Context(), identification.PostQuery{
		TargetType:  identification.TargetType("galaxy"),
		Description: "a shape I have not catalogued",
	})

	assert.ErrorIs(t, err, identification.ErrUnknownTargetType,
		"a query about a type this code cannot suggest candidates for would sit "+
			"in the queue forever, matching nothing")
}

// The per-query candidate cap. Not testing the exact boundary by inserting 50
// rows, but the rule is here so the limit is not a number that appeared from
// nowhere.
func TestSuggestRejectsAMismatchedType(t *testing.T) {
	s := idService(t)
	sceneQuery := postQuery(t, identification.TargetScene, "which scene is this", uuid.NullUUID{})

	_, err := s.Suggest(t.Context(), sceneQuery.ID, identification.TargetPerformer,
		uuid.Must(uuid.NewV7()), uuid.NullUUID{})

	assert.ErrorIs(t, err, identification.ErrMismatchedCandidateType,
		"a performer in a list of possible SCENES is a category error the "+
			"community would have to diagnose one thread at a time")
}

// One user suggests a given entity for a given query once. Re-suggesting is not
// more signal, and allowing it would let one person weight the vote.
func TestSuggestIsIdempotentPerEntity(t *testing.T) {
	s := idService(t)
	q := postQuery(t, identification.TargetScene, "which scene", uuid.NullUUID{})
	entity := uuid.Must(uuid.NewV7())

	suggestOne(t, q.ID, identification.TargetScene, entity, uuid.NullUUID{})
	_, err := s.Suggest(t.Context(), q.ID, identification.TargetScene, entity, uuid.NullUUID{})

	assert.Error(t, err,
		"the same entity suggested twice would be two rows the vote tally counts "+
			"separately, letting one person suggest a candidate and vote it")
}

// A vote is one per user per candidate. The composite primary key IS the rule.
func TestVoteIsOnePerUser(t *testing.T) {
	s := idService(t)
	q := postQuery(t, identification.TargetScene, "which scene", uuid.NullUUID{})
	c := suggestOne(t, q.ID, identification.TargetScene, uuid.Must(uuid.NewV7()), uuid.NullUUID{})
	voter := idUser(t)

	require.NoError(t, s.Vote(t.Context(), c.ID, voter))

	err := s.Vote(t.Context(), c.ID, voter)
	assert.Error(t, err,
		"a second vote from the same user would double their own tally, and the "+
			"tally is what §5's leaderboards and the leading-candidate rule read")

	// And the refusal must be specific, not a generic error.
	assert.Contains(t, err.Error(), "duplicate key",
		"the constraint is doing the work, so the error should say so rather "+
			"than surface as an opaque failure")
}

// Unvote works, and lowers the tally.
func TestUnvoteLowersTheTally(t *testing.T) {
	s := idService(t)
	q := postQuery(t, identification.TargetScene, "which scene", uuid.NullUUID{})
	c := suggestOne(t, q.ID, identification.TargetScene, uuid.Must(uuid.NewV7()), uuid.NullUUID{})
	voter := idUser(t)

	require.NoError(t, s.Vote(t.Context(), c.ID, voter))
	voted, err := s.HasVoted(t.Context(), c.ID, voter)
	require.NoError(t, err)
	assert.True(t, voted, "HasVoted is what the UI renders a vote button from")

	require.NoError(t, s.Unvote(t.Context(), c.ID, voter))
	voted, err = s.HasVoted(t.Context(), c.ID, voter)
	require.NoError(t, err)
	assert.False(t, voted, "an accidental tap should be undoable; a vote the user "+
		"cannot take back is a vote they will not make")

	candidates, err := s.ListCandidates(t.Context(), q.ID)
	require.NoError(t, err)
	assert.Equal(t, 0, candidates[0].VoteCount, "the tally must fall with the vote")
}

// Voting on a resolved query is refused. The vote would count for nothing, and
// worse it would count in the voter's Detective score, which is the one place a
// vote has lasting effect.
func TestVoteOnAResolvedQueryIsRefused(t *testing.T) {
	s := idService(t)
	q := postQuery(t, identification.TargetScene, "which scene", uuid.NullUUID{})
	c := suggestOne(t, q.ID, identification.TargetScene, uuid.Must(uuid.NewV7()), uuid.NullUUID{})

	_, err := s.Resolve(t.Context(), q.ID, identification.TargetScene, c.EntityID,
		idUser(t), nil)
	require.NoError(t, err)

	err = s.Vote(t.Context(), c.ID, idUser(t))
	assert.ErrorIs(t, err, identification.ErrQueryClosed,
		"a vote on a resolved query is counted for nothing and would farm the "+
			"Detective score for effort that changed no outcome")
}

// A vote is EVIDENCE, not authority. Resolving requires a named human, and
// nothing in this path writes to the metadata tables.
func TestResolveRecordsWhoDecidedAndWhat(t *testing.T) {
	s := idService(t)
	q := postQuery(t, identification.TargetScene, "which scene is this", uuid.NullUUID{})
	c := suggestOne(t, q.ID, identification.TargetScene, uuid.Must(uuid.NewV7()), uuid.NullUUID{})
	solver := idUser(t)

	trustRecorded := false
	resolved, err := s.Resolve(t.Context(), q.ID, identification.TargetScene, c.EntityID,
		solver, func(context.Context) error {
			trustRecorded = true
			return nil
		})
	require.NoError(t, err)

	assert.Equal(t, identification.StatusSolved, resolved.Status)
	require.NotNil(t, resolved.ResolvedID, "a solved query must name what it resolved "+
		"to, or every consumer of the solved view has to re-verify it")
	assert.Equal(t, c.EntityID, *resolved.ResolvedID)
	require.NotNil(t, resolved.ResolvedBy)
	assert.Equal(t, solver, *resolved.ResolvedBy, "§5's detective reputation and "+
		"audit trail are built on knowing WHO resolved it")
	assert.True(t, trustRecorded,
		"picking the community's own suggestion is the act §5 rewards with trust")
}

// Resolving to something nobody suggested does NOT earn trust. It is a different
// act: the resolver identified it themselves, and the reputation is for reading
// the community's suggestions and picking right.
func TestResolveToAnUnsuggestedEntityEarnsNoTrust(t *testing.T) {
	s := idService(t)
	q := postQuery(t, identification.TargetScene, "which scene", uuid.NullUUID{})
	suggestOne(t, q.ID, identification.TargetScene, uuid.Must(uuid.NewV7()), uuid.NullUUID{})

	trustRecorded := false
	_, err := s.Resolve(t.Context(), q.ID, identification.TargetScene,
		uuid.Must(uuid.NewV7()), idUser(t), func(context.Context) error {
			trustRecorded = true
			return nil
		})
	require.NoError(t, err, "a resolver may know better than the suggestions")

	assert.False(t, trustRecorded,
		"§5's Detective reputation is for identifying something from the "+
			"community's candidates, and rewarding any resolve would let a user "+
			"farm trust by resolving their own queries to arbitrary entities")
}

// A query with nothing suggested has no evidence, and resolving it would record a
// conclusion with nothing behind it.
func TestResolveRefusesAQueryWithNoCandidates(t *testing.T) {
	s := idService(t)
	q := postQuery(t, identification.TargetScene, "which scene", uuid.NullUUID{})

	_, err := s.Resolve(t.Context(), q.ID, identification.TargetScene,
		uuid.Must(uuid.NewV7()), idUser(t), nil)

	assert.ErrorIs(t, err, identification.ErrNoCandidates,
		"resolving a query nobody answered records a conclusion with no evidence "+
			"behind it, which is the failure this package exists to prevent")
}

// The resolution type must match what the query asked about.
func TestResolveRejectsAMismatchedType(t *testing.T) {
	s := idService(t)
	q := postQuery(t, identification.TargetScene, "which scene", uuid.NullUUID{})
	c := suggestOne(t, q.ID, identification.TargetScene, uuid.Must(uuid.NewV7()), uuid.NullUUID{})

	_, err := s.Resolve(t.Context(), q.ID, identification.TargetPerformer,
		c.EntityID, idUser(t), nil)

	assert.ErrorIs(t, err, identification.ErrMismatchedResolutionType)
}

// A resolved query is not resolved twice. Two people clicking accept on different
// candidates at once is a real race, and last-write-wins would silently discard
// one person's work.
func TestResolveIsNotReentrant(t *testing.T) {
	s := idService(t)
	q := postQuery(t, identification.TargetScene, "which scene", uuid.NullUUID{})
	first := suggestOne(t, q.ID, identification.TargetScene, uuid.Must(uuid.NewV7()), uuid.NullUUID{})
	suggestOne(t, q.ID, identification.TargetScene, uuid.Must(uuid.NewV7()), uuid.NullUUID{})

	_, err := s.Resolve(t.Context(), q.ID, identification.TargetScene, first.EntityID,
		idUser(t), nil)
	require.NoError(t, err)

	other, err := s.Get(t.Context(), q.ID)
	require.NoError(t, err)
	require.NotNil(t, other.ResolvedID)
	winner := *other.ResolvedID

	_, err = s.Resolve(t.Context(), q.ID, identification.TargetScene,
		uuid.Must(uuid.NewV7()), idUser(t), nil)
	assert.ErrorIs(t, err, identification.ErrQueryClosed,
		"a second resolution must not overwrite the first: the UPDATE is guarded "+
			"on status='open' so no rows means someone else got there first")

	after, err := s.Get(t.Context(), q.ID)
	require.NoError(t, err)
	require.NotNil(t, after.ResolvedID)
	assert.Equal(t, winner, *after.ResolvedID, "the first resolution stands")
}

// ABANDONED is a distinct state from open, and a dead query must leave the queue.
func TestAbandonRemovesTheQueryFromTheQueue(t *testing.T) {
	s := idService(t)
	q := postQuery(t, identification.TargetScene, "which scene", uuid.NullUUID{})

	open, err := s.ListOpen(t.Context(), 100)
	require.NoError(t, err)
	assert.True(t, containsQueryID(open, q.ID), "a newly posted query must appear "+
		"in the board's queue immediately, or posting it did nothing visible")

	require.NoError(t, s.Abandon(t.Context(), q.ID))

	open, err = s.ListOpen(t.Context(), 100)
	require.NoError(t, err)
	assert.False(t, containsQueryID(open, q.ID),
		"an abandoned query is one the community gave up on; leaving it in the "+
			"queue forever is how a board fills with questions nobody wants")

	got, err := s.Get(t.Context(), q.ID)
	require.NoError(t, err)
	assert.Equal(t, identification.StatusAbandoned, got.Status)
}

// A solved query is findable from the entity it resolved to. This is §5's
// canonical link: the board's conclusions indexed against real metadata.
func TestResolvedForGivesAnEntityItsReputation(t *testing.T) {
	s := idService(t)
	entity := uuid.Must(uuid.NewV7())

	q := postQuery(t, identification.TargetScene, "which scene is this", uuid.NullUUID{})
	c := suggestOne(t, q.ID, identification.TargetScene, entity, uuid.NullUUID{})
	_, err := s.Resolve(t.Context(), q.ID, identification.TargetScene, c.EntityID,
		idUser(t), nil)
	require.NoError(t, err)

	found, err := s.ResolvedFor(t.Context(), identification.TargetScene, entity, 20)
	require.NoError(t, err)
	require.Len(t, found, 1, "an entity page must be able to show what the "+
		"community worked out about it")
	assert.Equal(t, q.ID, found[0].ID)

	// An unsolved query is not a canonical link, so it must not appear.
	other := postQuery(t, identification.TargetScene, "another scene", uuid.NullUUID{})
	suggestOne(t, other.ID, identification.TargetScene, entity, uuid.NullUUID{})
	found, err = s.ResolvedFor(t.Context(), identification.TargetScene, entity, 20)
	require.NoError(t, err)
	assert.Len(t, found, 1, "an OPEN query is a question, not an answer, and "+
		"listing it as a canonical link would report an identification nobody made")
}

// The Detective score counts votes on OPEN queries only, so voting on questions
// that were answered without you stops counting.
func TestDetectiveScoreCountsOnlyOpenQueries(t *testing.T) {
	s := idService(t)
	voter := idUser(t)

	// One vote on an open query.
	openQuery := postQuery(t, identification.TargetScene, "still open", uuid.NullUUID{})
	openCand := suggestOne(t, openQuery.ID, identification.TargetScene, uuid.Must(uuid.NewV7()), uuid.NullUUID{})
	require.NoError(t, s.Vote(t.Context(), openCand.ID, voter))

	// One vote on a query that is then resolved.
	closedQuery := postQuery(t, identification.TargetScene, "about to be solved", uuid.NullUUID{})
	closedCand := suggestOne(t, closedQuery.ID, identification.TargetScene, uuid.Must(uuid.NewV7()), uuid.NullUUID{})
	require.NoError(t, s.Vote(t.Context(), closedCand.ID, voter))
	_, err := s.Resolve(t.Context(), closedQuery.ID, identification.TargetScene,
		closedCand.EntityID, idUser(t), nil)
	require.NoError(t, err)

	score, err := s.DetectiveScore(t.Context(), voter)
	require.NoError(t, err)
	assert.Equal(t, 1, score,
		"a vote on a resolved query is evidence about a question that no longer "+
			"exists, and a leaderboard that keeps counting it rewards voting on "+
			"questions that were answered without the voter")
}

// The queue is bounded, and the default applies rather than an unbounded read.
func TestListOpenIsBounded(t *testing.T) {
	s := idService(t)
	for i := 0; i < 3; i++ {
		postQuery(t, identification.TargetScene, "a scene worth identifying", uuid.NullUUID{})
	}

	for _, limit := range []int{-1, 0, 5000} {
		open, err := s.ListOpen(t.Context(), limit)
		require.NoError(t, err, "limit %d", limit)
		assert.LessOrEqual(t, len(open), 50,
			"an out-of-range limit must fall back to the default, not read the "+
				"whole table")
	}

	open, err := s.ListOpen(t.Context(), 1)
	require.NoError(t, err)
	assert.Len(t, open, 1, "a bounded limit must actually bound")
}

// Two reentrancy guards, and finding out which one is load-bearing.
//
// Resolve checks status twice: once in Go, and once in the SQL (`WHERE
// status = 'open'` on the UPDATE). I assumed the SQL one was the real guard and
// that the Go one was belt-and-braces, because the Go check runs first and reads
// like tidiness. Two mutations settled it:
//
//   remove the Go status re-check     -> SURVIVES
//   remove the SQL status guard       -> SURVIVES
//   remove both                       -> TestResolveIsNotReentrant FAILS
//
// So each covers the other, and the test is only load-bearing when both are
// present. The Go check is not redundant: the transaction reads the row, validates
// the resolution type, and counts candidates BEFORE it writes, so a query that
// changed state in between must be caught at the read. The SQL guard is not
// redundant either: it is what makes the write itself conditional, which is the
// only thing that survives a true concurrent pair of transactions where both
// reads happen before either write.
//
// The lesson worth keeping: "there are two guards so either one suffices" is a
// guess, and it was wrong in both directions here. Two guards on the same
// invariant are usually not independent -- removing one and seeing the suite stay
// green is the only way to tell, and I had to remove both before the test failed.
