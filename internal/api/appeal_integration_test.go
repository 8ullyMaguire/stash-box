//go:build integration

package api_test

import (
	"context"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/99designs/gqlgen/client"

	dbtest "github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/models"
)

// The appeal, driven the way a member and a moderator drive it (migration 141, spec §9).
//
// THE SUBJECT HERE IS THE RULE, NOT THE PLUMBING. Every other half of this feature has a test: the block
// list has its own, the release has its own, and the pipeline's refusal has its own. What none of them
// could have is the one thing slice 2 exists for -- that a refusal can be undone by SOMEBODY ELSE, and
// that "somebody else" is enforced by IDENTITY rather than by a role. So each test below plants the exact
// person the rule excludes and asserts the refusal, and then hands the same decision to somebody who is
// not excluded and asserts it lands.
//
// THE FIXTURE IS PART OF THE ASSERTION. A test of "the blocker cannot decide" needs the blocker to hold
// ADMIN, or the role directive refuses first and the identity rule is never reached -- a green test about
// a line of code that never ran. Every decider below is an admin on purpose, so the only thing that can
// refuse them is the rule under test.

// POINTERS FOR EVERY DECISION FIELD, and the reason is the defect this suite MISSED once: with plain
// strings, `assert.Empty` passes both for `null` and for `""`, so a deployed instance that serialised a
// pending appeal's outcome as an empty string passed every test here. The live walk caught it. Pointers
// make the two distinguishable, which is what the assertions below now rely on.
type appealResult struct {
	ID           string  `json:"id"`
	BlockID      string  `json:"block_id"`
	Kind         string  `json:"kind"`
	Value        string  `json:"value"`
	Statement    string  `json:"statement"`
	Outcome      *string `json:"outcome"`
	DecisionNote *string `json:"decision_note"`
	DecidedAt    *string `json:"decided_at"`
	Released     bool    `json:"released"`
	FiledBy      *struct {
		ID string `json:"id"`
	} `json:"filed_by"`
	BlockedBy *struct {
		ID string `json:"id"`
	} `json:"blocked_by"`
	DecidedBy *struct {
		ID string `json:"id"`
	} `json:"decided_by"`
}

const appealFields = `id block_id kind value statement outcome decision_note decided_at released
	filed_by { id } blocked_by { id } decided_by { id }`

func fileContentAppeal(t *testing.T, r *testRunner, kind, value, statement string) (appealResult, error) {
	t.Helper()
	var resp struct {
		FileContentAppeal appealResult `json:"fileContentAppeal"`
	}
	err := r.client.Post(`
		mutation($input: FileContentSourceAppealInput!) {
			fileContentAppeal(input: $input) { `+appealFields+` }
		}
	`, &resp, client.Var("input", map[string]any{"kind": kind, "value": value, "statement": statement}))
	return resp.FileContentAppeal, err
}

// decideContentAppeal decides, and takes the note as a plain string: an empty note is sent as NULL, which
// is what the schema means by optional -- passing a pointer through every call site would make the tests
// read as if "no reasoning" and "empty reasoning" were different things, and they are not.
func decideContentAppeal(t *testing.T, r *testRunner, id, outcome, note string) (appealResult, error) {
	t.Helper()
	var noteValue *string
	if note != "" {
		noteValue = &note
	}
	var resp struct {
		DecideContentAppeal appealResult `json:"decideContentAppeal"`
	}
	err := r.client.Post(`
		mutation($id: ID!, $outcome: String!, $note: String) {
			decideContentAppeal(id: $id, outcome: $outcome, note: $note) { `+appealFields+` }
		}
	`, &resp, client.Var("id", id), client.Var("outcome", outcome), client.Var("note", noteValue))
	return resp.DecideContentAppeal, err
}

func contentAppeals(t *testing.T, r *testRunner, pendingOnly bool) ([]appealResult, error) {
	t.Helper()
	var resp struct {
		ContentAppeals []appealResult `json:"contentAppeals"`
	}
	err := r.client.Post(`
		query($pendingOnly: Boolean!) {
			contentAppeals(pending_only: $pendingOnly) { `+appealFields+` }
		}
	`, &resp, client.Var("pendingOnly", pendingOnly))
	return resp.ContentAppeals, err
}

// appealByID reads one appeal from the moderator's own surface, which is the strongest place to assert
// what it looks like after a release: the operator answering "what happened here" reads the queue, not a
// database.
func appealByID(t *testing.T, r *testRunner, id string) appealResult {
	t.Helper()
	all, err := contentAppeals(t, r, false)
	require.NoError(t, err)
	found := []appealResult{}
	for _, appeal := range all {
		if appeal.ID == id {
			found = append(found, appeal)
		}
	}
	require.Len(t, found, 1, "the appeal must be readable after it was decided")
	return found[0]
}

// rawAppeal reads an appeal from the moderator's queue WITHOUT a typed struct, so the assertions can be
// about the BYTES a client receives. That distinction is the whole reason this helper exists: `null` and `""`
// are the same thing to `assert.Empty` and different things to every client on the wire, and the deployed
// instance was sending the empty string for every pending appeal while this suite was green.
func rawAppeal(t *testing.T, r *testRunner, id string) map[string]any {
	t.Helper()
	var resp struct {
		ContentAppeals []map[string]any `json:"contentAppeals"`
	}
	require.NoError(t, r.client.Post(`
		query { contentAppeals(pending_only: false) { id block_id outcome decision_note decided_at } }
	`, &resp))

	found := []map[string]any{}
	for _, appeal := range resp.ContentAppeals {
		if appeal["id"] == id {
			found = append(found, appeal)
		}
	}
	require.Len(t, found, 1, "the appeal must be readable from the moderator's own surface")
	return found[0]
}

// rawString reads a scalar out of a raw response map, dereferencing the pointer gqlgen's own client decodes
// scalars into. Written after the first version of this assertion compared a `string` with a `*string` and
// failed on a decision that HAD been recorded correctly -- a failure that names the code while describing the
// decoder.
func rawString(t *testing.T, value any) string {
	t.Helper()
	switch typed := value.(type) {
	case string:
		return typed
	case *string:
		require.NotNil(t, typed)
		return *typed
	default:
		t.Fatalf("expected a string or *string in the raw response, got %T", value)
		return ""
	}
}

// createAdminUser makes another operator, which is what makes the identity rule testable at all: the rule
// is a comparison between two DIFFERENT people, and the shared fixture has one admin.
func createAdminUser(t *testing.T, name string) *models.User {
	t.Helper()
	user, err := dbtest.Factory().User().Create(context.Background(), models.UserCreateInput{
		Name:     name + "-" + uuid.Must(uuid.NewV4()).String()[:8],
		Password: "TestPassword#2024",
		Email:    name + "@example.invalid",
		Roles:    []models.RoleEnum{models.RoleEnumAdmin},
	})
	require.NoError(t, err)
	return user
}

func asAdminUser(t *testing.T, user *models.User) *testRunner {
	t.Helper()
	return createTestRunner(t, user, []models.RoleEnum{models.RoleEnumAdmin})
}

// appealURL makes a URL nothing else in the suite uses, and removes the rows this test wrote when it ends.
//
// The cleanup is per-VALUE rather than per-table because the package shares one database: deleting every
// appeal would delete another test's fixture and turn a green suite red depending on order.
func appealURL(t *testing.T, host string) string {
	t.Helper()
	raw := "https://" + host + "/watch/" + uuid.Must(uuid.NewV4()).String()
	t.Cleanup(func() {
		_, _ = dbtest.DB().Exec(context.Background(),
			`DELETE FROM content_source_block_appeal WHERE value = $1 OR value = $2`, raw, raw)
		_, _ = dbtest.DB().Exec(context.Background(),
			`DELETE FROM content_source_block WHERE value = $1`, raw)
	})
	return raw
}

// THE RULE, WITH BOTH ARMS: the operator who recorded the block cannot hear the appeal against it, and the
// member who filed it cannot decide it either. Then a third moderator can -- without which the refusals
// would be indistinguishable from a feature that never grants anything.
func TestAnAppealIsDecidedBySomebodyWhoNeitherBlockedNorFiledIt(t *testing.T) {
	fetchSourcesForTest(t)
	raw := appealURL(t, "appeal-identity.invalid")

	blocker := asAdmin(t)
	block, err := takedownContentURL(t, blocker, raw, "notice 2026-10-15-appeal-identity")
	require.NoError(t, err)

	// The filer holds ADMIN deliberately: see this file's comment.
	filerUser := createAdminUser(t, "appeal-filer")
	filer := asAdminUser(t, filerUser)

	// Filed against the URL AS SUBMITTED, which is all a member has: the block list is an operator
	// surface, and a row id is not something they were ever shown.
	appeal, err := fileContentAppeal(t, filer, "url", raw, "this is my own work, uploaded by somebody else")
	require.NoError(t, err)

	assert.Equal(t, block.Block.ID, appeal.BlockID, "the appeal names the block it contests")
	assert.Equal(t, "url", appeal.Kind)
	require.NotNil(t, appeal.FiledBy)
	assert.Equal(t, filerUser.ID.String(), appeal.FiledBy.ID, "filed by the member who contested it")
	require.NotNil(t, appeal.BlockedBy, "who recorded the block is carried on the appeal, not read back later")
	assert.Equal(t, userDB.admin.ID.String(), appeal.BlockedBy.ID,
		"and it is the operator who recorded it -- the value the different-moderator rule compares against")
	assert.Nil(t, appeal.Outcome,
		"a filed appeal is pending and reports NO outcome -- null, not an empty string, which a client's "+
			"`if (outcome)` test would read as a decision. This assertion is the one the live walk had to teach: "+
			"the deployed instance sent `\"outcome\": \"\"` for every pending appeal while this suite was green")
	assert.Nil(t, appeal.DecidedAt, "and no decision time -- not the zero time, which renders as year 1")
	assert.Nil(t, appeal.DecidedBy)

	// THE WIRE, NOT THE STRUCT, and read as a PAIR on either side of the decision below. A pending appeal's
	// decision fields must be JSON null: the field has to be PRESENT (a client checking for the key finds it)
	// and its value has to be null rather than `""`, which reads as a decision to `if (outcome)`.
	pendingRaw := rawAppeal(t, blocker, appeal.ID)
	require.Contains(t, pendingRaw, "outcome", "the key is present, so a null is about absence of a decision")
	assert.NotNil(t, pendingRaw["block_id"], "and the block it contests is named, because that block is live")
	assert.Nil(t, pendingRaw["outcome"], `a pending appeal's outcome is null on the wire, not ""`)
	assert.Nil(t, pendingRaw["decision_note"])
	assert.Nil(t, pendingRaw["decided_at"])

	// ARM 1: the operator who recorded the block.
	_, err = decideContentAppeal(t, blocker, appeal.ID, "denied", "")
	require.Error(t, err, "the operator who recorded the block must not decide the appeal against it")
	assert.Contains(t, err.Error(), "you recorded the block",
		"and the refusal says which arm fired: a role check would have passed for this same person")

	// ARM 2: the member who filed it.
	_, err = decideContentAppeal(t, filer, appeal.ID, "denied", "")
	require.Error(t, err, "the appellant must not decide their own appeal")
	assert.Contains(t, err.Error(), "you filed this appeal")

	// NEITHER REFUSAL DECIDED ANYTHING: a refusal that half-wrote a decision would be worse than none.
	pending, err := contentAppeals(t, blocker, true)
	require.NoError(t, err)
	stillPending := 0
	for _, candidate := range pending {
		if candidate.ID == appeal.ID {
			stillPending++
			assert.Nil(t, candidate.Outcome, "and it is still in the queue claiming no outcome")
		}
	}
	require.Equal(t, 1, stillPending, "a refused decision must leave the appeal in the queue")

	// A THIRD moderator -- neither of the two excluded people -- grants it.
	third := asAdminUser(t, createAdminUser(t, "appeal-third"))
	granted, err := decideContentAppeal(t, third, appeal.ID, "granted", "the notice named the wrong uploader")
	require.NoError(t, err)
	require.NotNil(t, granted.Outcome)
	assert.Equal(t, "granted", *granted.Outcome)
	require.NotNil(t, granted.DecidedBy)
	assert.NotEqual(t, userDB.admin.ID.String(), granted.DecidedBy.ID)
	assert.NotEqual(t, filerUser.ID.String(), granted.DecidedBy.ID)
	require.NotNil(t, granted.DecisionNote)
	assert.Equal(t, "the notice named the wrong uploader", *granted.DecisionNote)
	require.NotNil(t, granted.DecidedAt, "a decided appeal reports WHEN it was decided")

	// The same reader, after the decision: the nulls above became the values, which is what makes the pair an
	// assertion about THIS appeal rather than about a field that is always null.
	decidedRaw := rawAppeal(t, third, appeal.ID)
	assert.Equal(t, "granted", rawString(t, decidedRaw["outcome"]))
	assert.Equal(t, "the notice named the wrong uploader", rawString(t, decidedRaw["decision_note"]))
	assert.NotNil(t, decidedRaw["decided_at"], "and a decision time, which was null before the decision")
	assert.True(t, granted.Released, "a granted appeal releases the block")

	// AND ONLY ONCE. The first outcome is the outcome.
	_, err = decideContentAppeal(t, third, appeal.ID, "denied", "")
	require.Error(t, err, "an appeal is decided once")
	assert.Contains(t, err.Error(), "already been decided")
}

// A GRANT RELEASES THROUGH THE OPERATOR'S OWN PATH, AND THE APPEAL OUTLIVES THE BLOCK.
//
// Two claims in one test because they are the two halves of the same sentence: the block is really gone
// (asserted by re-blocking the same URL and getting a NEW block rather than the old one back -- which is
// what tells a delete apart from a flag), and the appeal that contested it is still readable, still naming
// what it was about, with its reference null and its blocked_by intact. That last field is the reason
// migration 141 carries the blocker on the appeal at all: after a release there is no block to read it
// from, and the rule has to hold for an appeal that is still pending.
func TestAGrantedAppealReleasesTheBlockAndTheAppealOutlivesIt(t *testing.T) {
	fetchSourcesForTest(t)
	raw := appealURL(t, "appeal-outlives.invalid")

	blocker := asAdmin(t)
	block, err := takedownContentURL(t, blocker, raw, "notice 2026-10-15-appeal-outlives")
	require.NoError(t, err)

	filer := asRead(t)
	appeal, err := fileContentAppeal(t, filer, "url", raw, "please review this refusal")
	require.NoError(t, err)

	moderator := asAdminUser(t, createAdminUser(t, "appeal-outsider"))
	granted, err := decideContentAppeal(t, moderator, appeal.ID, "granted", "accepted")
	require.NoError(t, err)
	require.True(t, granted.Released)

	// The block is GONE, not flagged: blocking the same URL again is a first block, with a new id.
	again, err := takedownContentURL(t, blocker, raw, "second notice after the grant")
	require.NoError(t, err)
	assert.False(t, again.AlreadyBlocked,
		"the release removed the row, so there was nothing for the operator to reuse")
	assert.NotEqual(t, block.Block.ID, again.Block.ID, "a new block, not the released one coming back")

	// The appeal survived the block it named, and now reads as history.
	// The reference is null ON THE WIRE too, not just in the row: the same raw reader as above, so a client
	// that checks `if (block_id)` is told the block is no longer in force rather than shown a zero uuid.
	releasedRaw := rawAppeal(t, moderator, appeal.ID)
	assert.Nil(t, releasedRaw["block_id"], "a released block leaves the appeal's reference null on the wire")

	survived := appealByID(t, moderator, appeal.ID)
	assert.Empty(t, survived.BlockID, "the reference goes null when the block is deleted (ON DELETE SET NULL)")
	assert.Equal(t, "url", survived.Kind, "and the appeal keeps the key: it still says what it was about")
	assert.Equal(t, appeal.Value, survived.Value)
	require.NotNil(t, survived.Outcome)
	assert.Equal(t, "granted", *survived.Outcome)
	require.NotNil(t, survived.BlockedBy,
		"the blocker survives the block, which is what makes the rule enforceable on a pending appeal")
	assert.Equal(t, userDB.admin.ID.String(), survived.BlockedBy.ID)

	// THE TRAIL, IN TWO PLACES: the appeal's own rows and the operator's release, each against its own
	// subject. The release is recorded against the BLOCK (that is the operator's surface) and the decision
	// against the APPEAL (that is the appeal's), which is why they are two lookups and not one.
	appealAudits := takedownAuditsFor(t, uuid.FromStringOrNil(appeal.ID))
	actions := []string{}
	for _, row := range appealAudits {
		actions = append(actions, row.Action)
		assert.Equal(t, "SOURCE_APPEAL", row.TargetType)
	}
	assert.Equal(t, []string{"SOURCE_APPEAL", "SOURCE_APPEAL_DECISION"}, actions,
		"filing and deciding are both recorded, in that order, against the appeal")

	releaseAudits := takedownAuditsFor(t, uuid.FromStringOrNil(block.Block.ID))
	require.NotEmpty(t, releaseAudits, "the release wrote its own row against the block it removed")

	// COMPARED WITH A LIVE OPERATOR RELEASE, NOT WITH A CONSTANT. The claim under test is that a grant and
	// an operator's own release are the SAME code path, and two assertions against the same literal would
	// both pass if a grant had quietly grown a release of its own. So a second URL is blocked and released
	// by the operator, and the two trails are required to say the identical thing.
	controlURL := appealURL(t, "appeal-outlives-control.invalid")
	control, err := takedownContentURL(t, blocker, controlURL, "control notice")
	require.NoError(t, err)
	controlReleased, err := releaseContentBlock(t, blocker, "url", controlURL)
	require.NoError(t, err)
	require.True(t, controlReleased, "the control release must itself have worked, or the comparison is vacuous")

	controlAudits := takedownAuditsFor(t, uuid.FromStringOrNil(control.Block.ID))
	require.NotEmpty(t, controlAudits)
	assert.Equal(t, controlAudits[len(controlAudits)-1].Action, releaseAudits[len(releaseAudits)-1].Action,
		"a granted appeal writes what an operator's own release writes, because it is the same path")
}

// A DENIAL LEAVES THE REFUSAL IN FORCE, and it says why.
func TestADeniedAppealLeavesTheBlockInForceAndTheDecisionRecorded(t *testing.T) {
	fetchSourcesForTest(t)
	raw := appealURL(t, "appeal-denied.invalid")

	blocker := asAdmin(t)
	block, err := takedownContentURL(t, blocker, raw, "notice 2026-10-15-appeal-denied")
	require.NoError(t, err)

	filer := asRead(t)
	appeal, err := fileContentAppeal(t, filer, "url", raw, "this is my own work")
	require.NoError(t, err)

	note := "the notice was verified against the rights holder's catalogue"
	moderator := asAdminUser(t, createAdminUser(t, "appeal-denier"))
	denied, err := decideContentAppeal(t, moderator, appeal.ID, "denied", note)
	require.NoError(t, err)
	require.NotNil(t, denied.Outcome)
	assert.Equal(t, "denied", *denied.Outcome)
	assert.False(t, denied.Released, "a denial releases nothing")
	require.NotNil(t, denied.DecisionNote)
	assert.Equal(t, note, *denied.DecisionNote)
	assert.NotEmpty(t, denied.BlockID, "the block is still there, and the appeal still names it")

	// The refusal is STILL IN FORCE, asserted where it counts: re-taking-down the same URL finds the
	// existing block rather than creating one.
	again, err := takedownContentURL(t, blocker, raw, "notice 2026-10-15-appeal-denied (again)")
	require.NoError(t, err)
	assert.True(t, again.AlreadyBlocked, "a denied appeal leaves the address refused")
	assert.Equal(t, block.Block.ID, again.Block.ID)

	// And the appeal has left the queue.
	pending, err := contentAppeals(t, moderator, true)
	require.NoError(t, err)
	for _, candidate := range pending {
		assert.NotEqual(t, appeal.ID, candidate.ID, "a decided appeal is not pending")
	}
}

// FILING: it needs something to contest, something to say, and one per person per block.
func TestAnAppealNeedsALiveBlockAStatementAndOnePerMember(t *testing.T) {
	fetchSourcesForTest(t)
	raw := appealURL(t, "appeal-filing.invalid")

	filer := asRead(t)

	// Nothing is blocked under this key: an appeal against it would sit in a queue forever answering a
	// question about a string that is not refused.
	_, err := fileContentAppeal(t, filer, "url", raw, "please review this")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nothing is blocked",
		"the refusal names the key it found nothing under")

	blocker := asAdmin(t)
	_, err = takedownContentURL(t, blocker, raw, "notice 2026-10-15-appeal-filing")
	require.NoError(t, err)

	// An appeal that does not say what should be reviewed is not one.
	_, err = fileContentAppeal(t, filer, "url", raw, "   ")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must say what should be reviewed")

	appeal, err := fileContentAppeal(t, filer, "url", raw, "I hold the rights to this upload")
	require.NoError(t, err)

	// One per person per block, refused BY NAME rather than silently merged: a member who has already
	// appealed is told so, not shown the appeal they already filed as if it were new.
	_, err = fileContentAppeal(t, filer, "url", raw, "and another thing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already been appealed by this member")

	// ANOTHER MEMBER MAY STILL FILE. The limit is one per person, not one per block: a second person's
	// objection is a second objection, and collapsing them would hide the fact that more than one member
	// disagrees.
	other := asModerate(t)
	second, err := fileContentAppeal(t, other, "url", raw, "and I hold rights to a different cut of it")
	require.NoError(t, err)
	assert.NotEqual(t, appeal.ID, second.ID)

	// Both are in the queue, and the queue is ordered by when people waited.
	pending, err := contentAppeals(t, blocker, true)
	require.NoError(t, err)
	position := map[string]int{}
	for i, candidate := range pending {
		if candidate.ID == appeal.ID || candidate.ID == second.ID {
			position[candidate.ID] = i
		}
	}
	require.Len(t, position, 2, "both appeals are pending")
	assert.Less(t, position[appeal.ID], position[second.ID],
		"the queue is answered in the order people filed")
}
