//go:build integration

package api_test

import (
	"testing"
	"time"

	"github.com/99designs/gqlgen/client"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The identification board end to end through GraphQL.
//
// The service tests cover the rules and the database constraints. What only a
// test at this layer can catch is a resolver that is never reached, an enum that
// maps to the wrong service type, a role gate that leaks, and a field that is
// declared non-null and returns zero. All four compile fine and all four are
// invisible to a service test.

type idQueryResponse struct {
	PostIdentificationQuery struct {
		ID          string
		TargetType  string
		Description string
		Status      string
		Candidates  []struct {
			ID          string
			EntityID    string
			VoteCount   int
			VotedByMe   bool
			Entity      *performerOutput
			SuggestedBy *userOutput
		}
	}
}

type idSuggestResponse struct {
	SuggestIdentificationCandidate struct {
		ID        string
		EntityID  string
		VoteCount int
		VotedByMe bool
	}
}

type idVoteResponse struct {
	VoteIdentificationCandidate struct {
		VoteCount int
		VotedByMe bool
	}
	UnvoteIdentificationCandidate struct {
		VoteCount int
		VotedByMe bool
	}
}

type idResolveResponse struct {
	ResolveIdentificationQuery struct {
		ID           string
		Status       string
		ResolvedType *string
		ResolvedID   *string
		ResolvedBy   *userOutput
	}
}

type idListResponse struct {
	ListOpenIdentificationQueries []struct {
		ID     string
		Status string
	}
	ResolvedIdentificationQueries []struct {
		ID string
	}
	MyIdentificationDetectiveScore *struct {
		Score int
	}
}

type idAbandonResponse struct {
	AbandonIdentificationQuery struct {
		ID     string
		Status string
	}
}

// gqlPostQuery posts a query and returns its id.
func gqlPostQuery(t *testing.T, c *client.Client, targetType, description string) string {
	t.Helper()
	var resp idQueryResponse
	c.MustPost(`
		mutation Post($input: IdentificationPostInput!) {
			postIdentificationQuery(input: $input) {
				id targetType description status
			}
		}
	`, &resp, client.Var("input", map[string]any{
		"targetType":  targetType,
		"description": description,
	}))
	require.NotEmpty(t, resp.PostIdentificationQuery.ID,
		"a posted query must come back with an id, or the board is write-only")
	return resp.PostIdentificationQuery.ID
}

// gqlSuggest adds a candidate and returns its id.
func gqlSuggest(t *testing.T, c *client.Client, queryID, entityID string) string {
	t.Helper()
	var resp idSuggestResponse
	c.MustPost(`
		mutation Suggest($input: IdentificationSuggestInput!) {
			suggestIdentificationCandidate(input: $input) { id entityId voteCount }
		}
	`, &resp, client.Var("input", map[string]any{
		"queryId":    queryID,
		"entityType": "scene",
		"entityId":   entityID,
	}))
	require.NotEmpty(t, resp.SuggestIdentificationCandidate.ID)
	return resp.SuggestIdentificationCandidate.ID
}

// The full round trip, through the API boundary.
func TestIdentificationBoardFlowsThroughGraphQL(t *testing.T) {
	admin := asAdmin(t)
	scene := createSceneWithDuration(t, "Identification Flow Scene", intPtr(600))

	// Post a question.
	var post idQueryResponse
	admin.client.MustPost(`
		mutation Post($input: IdentificationPostInput!) {
			postIdentificationQuery(input: $input) {
				id targetType description status
			}
		}
	`, &post, client.Var("input", map[string]any{
		"targetType":  "scene",
		"description": "dark room, one dancer, a mirrored wall",
	}))

	assert.Equal(t, "open", post.PostIdentificationQuery.Status,
		"a posted query is open until a human resolves it")
	assert.Equal(t, "scene", post.PostIdentificationQuery.TargetType)
	queryID := post.PostIdentificationQuery.ID

	// Suggest a candidate. A fresh candidate has zero votes and this viewer has
	// not voted, so both are falsy and BOTH are declared non-null in the schema.
	// A resolver returning zero for a declared value compiles and passes every
	// service test.
	var suggest idSuggestResponse
	admin.client.MustPost(`
		mutation Suggest($input: IdentificationSuggestInput!) {
			suggestIdentificationCandidate(input: $input) {
				id entityId voteCount votedByMe
			}
		}
	`, &suggest, client.Var("input", map[string]any{
		"queryId":    queryID,
		"entityType": "scene",
		"entityId":   scene.String(),
	}))
	candidateID := suggest.SuggestIdentificationCandidate.ID
	assert.Equal(t, 0, suggest.SuggestIdentificationCandidate.VoteCount)
	assert.False(t, suggest.SuggestIdentificationCandidate.VotedByMe)

	// Vote, and the returned tally must be the RECOUNTED one.
	var vote idVoteResponse
	admin.client.MustPost(`
		mutation Vote($id: ID!) {
			voteIdentificationCandidate(candidateId: $id) { voteCount votedByMe }
		}
	`, &vote, client.Var("id", candidateID))
	// Read the tally through a SEPARATE query as well as through the mutation's
	// own return value. Comparing the mutation's return against itself is the
	// "read back your own value" trap: it cannot tell a correct tally from a
	// resolver that returns zero every time, because both are self-consistent.
	// The independent read is what makes the mutation's return value mean
	// something.
	var stored struct {
		IdentificationQuery *struct {
			Candidates []struct {
				VoteCount int
			}
		}
	}
	require.NoError(t, admin.client.Post(`
		query One($id: ID!) {
			identificationQuery(id: $id) { candidates { voteCount } }
		}
	`, &stored, client.Var("id", queryID)))
	require.NotNil(t, stored.IdentificationQuery)
	require.Len(t, stored.IdentificationQuery.Candidates, 1)
	assert.Equal(t, 1, stored.IdentificationQuery.Candidates[0].VoteCount,
		"the vote must be STORED: a vote that returns 200 and leaves no row "+
			"behind is the single most damaging failure this board has, because "+
			"the user has been told their effort counted")

	assert.Equal(t, stored.IdentificationQuery.Candidates[0].VoteCount,
		vote.VoteIdentificationCandidate.VoteCount,
		"the mutation must return the state NOW; returning the candidate the "+
			"caller already held would report a tally nobody recounted")
	assert.True(t, vote.VoteIdentificationCandidate.VotedByMe,
		"votedByMe is what the UI renders the button from, and it must be true "+
			"in the very response that casts the vote")

	// Unvote, and the tally must fall.
	var unvote idVoteResponse
	admin.client.MustPost(`
		mutation Unvote($id: ID!) {
			unvoteIdentificationCandidate(candidateId: $id) { voteCount votedByMe }
		}
	`, &unvote, client.Var("id", candidateID))
	assert.Equal(t, 0, unvote.UnvoteIdentificationCandidate.VoteCount)
	assert.False(t, unvote.UnvoteIdentificationCandidate.VotedByMe)

	// Vote again so the candidate is the community's pick, then resolve.
	admin.client.MustPost(`
		mutation Vote($id: ID!) { voteIdentificationCandidate(candidateId: $id) { voteCount } }
	`, &vote, client.Var("id", candidateID))

	var resolve idResolveResponse
	admin.client.MustPost(`
		mutation Resolve($input: IdentificationResolveInput!) {
			resolveIdentificationQuery(input: $input) {
				id status resolvedType resolvedId resolvedBy { id }
			}
		}
	`, &resolve, client.Var("input", map[string]any{
		"queryId":      queryID,
		"resolvedType": "scene",
		"resolvedId":   scene.String(),
	}))

	assert.Equal(t, "solved", resolve.ResolveIdentificationQuery.Status)
	require.NotNil(t, resolve.ResolveIdentificationQuery.ResolvedID,
		"a solved query must name what it resolved to")
	assert.Equal(t, scene.String(), *resolve.ResolveIdentificationQuery.ResolvedID)
	require.NotNil(t, resolve.ResolveIdentificationQuery.ResolvedType)
	assert.Equal(t, "scene", *resolve.ResolveIdentificationQuery.ResolvedType,
		"the resolution type is explicit in the schema, and a client rendering "+
			"the canonical link needs to know it")
	assert.NotNil(t, resolve.ResolveIdentificationQuery.ResolvedBy,
		"§5's detective reputation and audit trail start with knowing WHO")

	// The canonical-link read: the scene now has a solved query about it.
	var list idListResponse
	admin.client.MustPost(`
		query Resolved($type: IdentificationTargetType!, $id: ID!) {
			resolvedIdentificationQueries(entityType: $type, entityId: $id) { id }
		}
	`, &list, client.Var("type", "scene"), client.Var("id", scene.String()))
	assert.Len(t, list.ResolvedIdentificationQueries, 1)
}

// The role gates. §6 level 1 is where the board begins, so posting and voting must
// be refused below VOTE while reading stays open.
func TestIdentificationBoardRoleGates(t *testing.T) {
	none := asNone(t)
	read := asRead(t)

	var post idQueryResponse

	// A user with no roles is refused the read.
	err := none.client.Post(`
		mutation Post($input: IdentificationPostInput!) {
			postIdentificationQuery(input: $input) { id }
		}
	`, &post, client.Var("input", map[string]any{
		"targetType":  "scene",
		"description": "something I half remember",
	}))
	assert.Error(t, err, "a user with no roles must not be able to post")

	// READ is below VOTE, so a read-only user is refused too.
	err = read.client.Post(`
		mutation Post($input: IdentificationPostInput!) {
			postIdentificationQuery(input: $input) { id }
		}
	`, &post, client.Var("input", map[string]any{
		"targetType":  "scene",
		"description": "something I half remember",
	}))
	assert.Error(t, err,
		"posting is a contribution and belongs at §6 level 1 (Registered); a "+
			"read-only user posting would make the board spam-able by anyone who "+
			"can browse")
}

// An empty board is an empty list, not a GraphQL error.
func TestIdentificationBoardEmptyStatesAreEmptyNotErrors(t *testing.T) {
	admin := asAdmin(t)
	// A limit of 0 is out of contract and falls back to the default, which must
	// still be a successful empty-or-not list rather than an error.
	var list idListResponse
	require.NoError(t, admin.client.Post(`
		query Empty($limit: Int) {
			listOpenIdentificationQueries(limit: $limit) { id status }
		}
	`, &list, client.Var("limit", 0)),
		"an out-of-contract limit must fall back to a bound, not error: every "+
			"client renders a board with nothing in it")
	assert.NotNil(t, list.ListOpenIdentificationQueries)

	// The detective score is personal, so a logged-in user always gets a number.
	require.NoError(t, admin.client.Post(`
		query { myIdentificationDetectiveScore { score } }
	`, &list))
	require.NotNil(t, list.MyIdentificationDetectiveScore,
		"a personal score is never null: a null would read as 'not available' "+
			"when the real answer is a number, possibly zero")
}

// Reading a query that does not exist is null, not an error: a stale link is a
// normal outcome of following a link from an old thread.
func TestIdentificationQueryNotFoundIsNullNotAnError(t *testing.T) {
	admin := asAdmin(t)

	var resp struct {
		IdentificationQuery *struct {
			ID string
		}
	}
	require.NoError(t, admin.client.Post(`
		query One($id: ID!) { identificationQuery(id: $id) { id } }
	`, &resp, client.Var("id", uuid.Must(uuid.NewV7()).String())),
		"a deleted or never-existing query is null, not an error; every client "+
			"already renders an empty state for a null and none should have to "+
			"handle an exception for a stale link")
	assert.Nil(t, resp.IdentificationQuery)
}

// Abandoning a query takes it out of the queue and changes its reported status.
func TestIdentificationAbandonThroughGraphQL(t *testing.T) {
	admin := asAdmin(t)
	queryID := gqlPostQuery(t, admin.client.Client, "scene", "a scene nobody will ever solve")

	var resp idAbandonResponse
	admin.client.MustPost(`
		mutation Abandon($id: ID!) { abandonIdentificationQuery(id: $id) { id status } }
	`, &resp, client.Var("id", queryID))

	assert.Equal(t, "abandoned", resp.AbandonIdentificationQuery.Status,
		"an abandoned query is a distinct state from open, and reporting it as "+
			"open would put it back in the queue")
}

// A query read carries its candidates, and a candidate with zero votes still
// appears: it is exactly when someone needs to see it.
func TestIdentificationQueryReadIncludesZeroVoteCandidates(t *testing.T) {
	admin := asAdmin(t)
	scene := createSceneWithDuration(t, "Zero Vote Candidate Scene", intPtr(600))
	queryID := gqlPostQuery(t, admin.client.Client, "scene", "which of my two scenes is this")
	candidateID := gqlSuggest(t, admin.client.Client, queryID, scene.String())

	var resp struct {
		IdentificationQuery *struct {
			ID         string
			Candidates []struct {
				ID        string
				VoteCount int
				VotedByMe bool
			}
		}
	}
	require.NoError(t, admin.client.Post(`
		query One($id: ID!) {
			identificationQuery(id: $id) { id candidates { id voteCount votedByMe } }
		}
	`, &resp, client.Var("id", queryID)))

	require.NotNil(t, resp.IdentificationQuery)
	require.Len(t, resp.IdentificationQuery.Candidates, 1)
	assert.Equal(t, candidateID, resp.IdentificationQuery.Candidates[0].ID)
	assert.Equal(t, 0, resp.IdentificationQuery.Candidates[0].VoteCount,
		"a candidate nobody has voted for must still be listed; an inner join "+
			"would hide it, which is exactly when it needs to be seen")
}

// Every NON-NULL field the board renders must survive the row -> service ->
// resolver round trip.
//
// This test exists because the board was broken in the live app while every
// service test passed. `toModelQuery` dropped CreatedAt, and CreatedAt is
// `Time!` in the schema, so gqlgen rejected the whole field and
// listOpenIdentificationQueries returned
//
//	the requested element is null which the schema does not allow
//
// with data:null. The board rendered as an error page. The existing board test
// above asked for `{ id status }` only, so it never touched the field and could
// not see the break; the frontend tests mocked the response for the same reason.
//
// The lesson generalises: a test that selects a SUBSET of a type's non-null
// fields is not a test of that type. So this one asks for every field the UI
// reads, which is the set that actually has to work.
func TestIdentificationBoardReturnsEveryNonNullFieldTheUIRenders(t *testing.T) {
	admin := asAdmin(t)

	var post idQueryResponse
	require.NoError(t, admin.client.Post(`
		mutation Post($input: IdentificationPostInput!) {
			postIdentificationQuery(input: $input) { id }
		}
	`, &post, client.Var("input", map[string]any{
		"targetType":  "scene",
		"description": "every non-null field check",
	})))
	require.NotNil(t, post.PostIdentificationQuery)
	queryID := post.PostIdentificationQuery.ID

	// The exact field set IdentificationBoard.tsx and IdentificationQuery.tsx
	// request, plus the detail page's resolution fields. createdAt and voteCount
	// are the two that have each been dropped by a resolver at least once.
	var list struct {
		ListOpenIdentificationQueries []struct {
			ID          string
			TargetType  string
			Description string
			Status      string
			// string, not time.Time: the `Time` scalar is graphql-go/graphql's
			// (generated_exec.go calls graphql.UnmarshalTime), which serialises as
			// an RFC3339 string. Decoding it into time.Time fails with "expected a
			// map or struct, got string" -- which is a test bug that looks exactly
			// like a server bug, so it is worth stating.
			CreatedAt  string
			Candidates []struct {
				ID          string
				EntityType  string
				EntityID    string
				VoteCount   int
				VotedByMe   bool
				Note        *string
				CreatedAt   string
				SuggestedBy *struct {
					ID string
				}
			}
		}
	}

	require.NoError(t, admin.client.Post(`
		query Board($limit: Int) {
			listOpenIdentificationQueries(limit: $limit) {
				id targetType description status createdAt
				candidates { id entityType entityId voteCount votedByMe note createdAt suggestedBy { id } }
			}
		}
	`, &list, client.Var("limit", 50)),
		"asking for createdAt must not fail the query: it is Time!, so a resolver "+
			"that leaves it zero takes the entire list down with it")

	require.NotEmpty(t, list.ListOpenIdentificationQueries)

	var found bool
	for _, q := range list.ListOpenIdentificationQueries {
		if q.ID != queryID {
			continue
		}
		found = true
		assert.Equal(t, "scene", q.TargetType)
		assert.Equal(t, "every non-null field check", q.Description)
		assert.Equal(t, "open", q.Status)
		assert.NotEmpty(t, q.CreatedAt,
			"createdAt came back empty, which is the exact shape of the bug this "+
				"test was written for: the value exists in the row and never crossed "+
				"the resolver")
		// And it must be a real timestamp, not a placeholder that merely satisfies
		// the non-null contract. A resolver that wrote time.Time{} would serialise
		// as year 1 and pass an emptiness check.
		parsed, perr := time.Parse(time.RFC3339, q.CreatedAt)
		require.NoError(t, perr, "createdAt must be a parseable RFC3339 timestamp")
		assert.False(t, parsed.IsZero())
		assert.False(t, parsed.After(time.Now().Add(time.Hour)),
			"createdAt is in the future, so it is not the row's real timestamp")
	}
	assert.True(t, found, "the query just posted must appear in the board list")

	// The detail page, which additionally reads the resolution fields.
	var one struct {
		IdentificationQuery *struct {
			ID           string
			CreatedAt    string
			ResolvedAt   *string
			ResolvedBy   *struct{ ID string }
			SnapshotID   *string
			ResolvedID   *string
			ResolvedType *string
		}
	}
	require.NoError(t, admin.client.Post(`
		query One($id: ID!) {
			identificationQuery(id: $id) {
				id createdAt resolvedAt resolvedBy { id } snapshotId resolvedId resolvedType
			}
		}
	`, &one, client.Var("id", queryID)))
	require.NotNil(t, one.IdentificationQuery)
	assert.NotEmpty(t, one.IdentificationQuery.CreatedAt)
	// An open query has no resolution, and resolvedAt is nullable in the schema,
	// so this must be null rather than the zero time. A zero time here would mean
	// the old updatedAt-based mapping had been reintroduced.
	assert.Nil(t, one.IdentificationQuery.ResolvedAt,
		"an open query has not been resolved; a non-nil zero time means "+
			"resolvedAt is being derived from updated_at again")
}
