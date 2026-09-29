package identification

import (
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The pure parts: type validation and the leading-candidate rule.
//
// The persistence behaviour is in the integration test, because what matters there
// is what the database ends up holding.

// Every target type in the slice must validate, and near-misses must not.
func TestTargetTypeValidation(t *testing.T) {
	for _, valid := range AllTargetTypes {
		assert.True(t, valid.Valid(), "%q is in AllTargetTypes so it must be valid", valid)
	}
	// The cases that would actually arrive: a missing GraphQL argument, a typo, and
	// the plural form a client author would reach for.
	for _, bogus := range []TargetType{"", "performers", "Performer", "SCENE", "scene "} {
		assert.False(t, bogus.Valid(),
			"%q must be rejected: a query naming an unknown type would be stored "+
				"and then matched against no candidates, ever", bogus)
	}
}

// §5 says a solved identification becomes a canonical link. A tie in the vote is
// NOT a consensus, and reporting one of the tied candidates as the leader would be
// reporting a conclusion the community did not reach.
func TestLeadingCandidateReturnsNothingOnATie(t *testing.T) {
	candidates := []Candidate{
		{EntityID: uuid.Must(uuid.NewV7()), VoteCount: 5},
		{EntityID: uuid.Must(uuid.NewV7()), VoteCount: 5},
		{EntityID: uuid.Must(uuid.NewV7()), VoteCount: 2},
	}

	assert.Nil(t, LeadingCandidate(candidates),
		"two candidates level at the top means the community has not agreed, and "+
			"picking one would invent a consensus that does not exist")
}

// A tie BELOW the leader is not a tie at the top.
func TestLeadingCandidateIgnoresTiesBelowTheTop(t *testing.T) {
	top := uuid.Must(uuid.NewV7())
	candidates := []Candidate{
		{EntityID: top, VoteCount: 9},
		{EntityID: uuid.Must(uuid.NewV7()), VoteCount: 4},
		{EntityID: uuid.Must(uuid.NewV7()), VoteCount: 4},
	}

	leader := LeadingCandidate(candidates)
	require.NotNil(t, leader, "a clear leader is a leader even when the rest are level")
	assert.Equal(t, top, leader.EntityID)
}

// A later candidate overtaking the current best VOIDS an earlier tie. This is the
// subtle case: the input is not sorted, so "5, 5, 9" must report the 9.
func TestLeadingCandidateHandlesUnsortedInput(t *testing.T) {
	first := uuid.Must(uuid.NewV7())
	last := uuid.Must(uuid.NewV7())
	candidates := []Candidate{
		{EntityID: first, VoteCount: 5},
		{EntityID: uuid.Must(uuid.NewV7()), VoteCount: 5},
		{EntityID: last, VoteCount: 9},
	}

	leader := LeadingCandidate(candidates)
	require.NotNil(t, leader,
		"a candidate with more votes than any other is the leader even if two "+
			"earlier candidates were level: the tie was provisional")
	assert.Equal(t, last, leader.EntityID)
}

// A single candidate with ZERO votes is not a leader, because nobody has said
// anything about it.
func TestLeadingCandidateWithNoVotesIsNotALeader(t *testing.T) {
	candidates := []Candidate{
		{EntityID: uuid.Must(uuid.NewV7()), VoteCount: 0},
		{EntityID: uuid.Must(uuid.NewV7()), VoteCount: 0},
	}

	assert.Nil(t, LeadingCandidate(candidates),
		"zero votes is not a consensus; it is a query nobody has answered yet")
}

func TestLeadingCandidateOnAnEmptyList(t *testing.T) {
	assert.Nil(t, LeadingCandidate(nil), "no candidates means no leader, which is "+
		"the ordinary state of a freshly posted query")
}
