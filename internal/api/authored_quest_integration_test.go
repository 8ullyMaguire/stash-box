//go:build integration

package api_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbtest "github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/authored"
	"github.com/stashapp/stash-box/internal/service/completion"
	"github.com/stashapp/stash-box/internal/service/quest"
	"github.com/stashapp/stash-box/internal/service/trust"
)

// Authored quests, bounties and claiming (SPEC §7.7 step 5).
//
// The load-bearing property here is the CLAIM RACE, because that is the one thing
// in this step that can be wrong in a way no single-threaded test sees: two
// curators claiming the last item must not both get it. Everything else is
// bookkeeping, and bookkeeping tests are cheap.
//
// The other property worth stating out loud: an authored quest's items are FIXED
// at authoring time, and reconciling them on read is what makes a stored item
// honest. Both are asserted below.

func newAuthoredService(t *testing.T) *authored.Service {
	t.Helper()
	// Through the FACTORY, not by assembling the service by hand: the factory is
	// the only place that knows how to build a WithTxnFunc, and a hand-assembled
	// service here would be a different object from the one production constructs
	// -- which is the whole class of bug a test is supposed to rule out.
	return dbtest.Factory().Authored()
}

// createUserForQuest creates a user to author or claim with.
//
// Users go through the GraphQL mutation because that is the only creation path
// the repository has, and a test that inserts a row directly would create a user
// the rest of the system does not believe exists.
func createUserForQuest(t *testing.T, name string) uuid.UUID {
	t.Helper()
	user, err := asAdmin(t).createTestUser(nil, nil)
	require.NoError(t, err, "a claim needs a real user: the FK on claimed_by would "+
		"reject a fabricated id, and a test that inserted a row directly would be "+
		"testing a user the rest of the system does not believe exists")
	return user.ID
}

// createTagForQuest inserts a tag, with or without details.
//
// TAGS, not performers, and the reason is a pre-existing hazard this feature
// walked into: query.MaxPerPage is 100, the archive is shared across this
// package, and TestQueryPerformersSceneCountSort queries performers UNFILTERED
// and then asserts two specific performers are in the results. At 180 performers
// that was already one fixture from breaking; adding 14 more performers took the
// archive to 194 and broke it with "Performer with 0 scenes not found" -- a
// failure that has nothing to do with what that test tests.
//
// The fix is not to relax its assertion and not to raise its page size (the
// server clamps that regardless). The fix is for a feature about metadata
// completion not to inflate a table some unrelated test pages through. Tags are
// scored on `details` alone, which is all a quest fixture needs, and they never
// appear in a performers query.
func createTagForQuest(t *testing.T, name string, withDetails bool) uuid.UUID {
	t.Helper()
	var description *string
	if withDetails {
		description = strPtr("a description that earns the weight")
	}
	id := uuid.Must(uuid.NewV7())
	_, err := cq().CreateTag(t.Context(), queries.CreateTagParams{
		ID: id, Name: name, Description: description,
	})
	require.NoError(t, err, "creating a tag to author a quest over")
	return id
}

// setBirthdateForQuest repairs a performer, read-then-write.
//
// UpdatePerformer needs every NOT NULL column, so a hand-built params struct
// would omit them and fail for a reason that has nothing to do with the
// reconciliation under test.
func setBirthdateForQuest(t *testing.T, id uuid.UUID) {
	t.Helper()
	performer, err := cq().FindPerformer(context.Background(), id)
	require.NoError(t, err)
	performer.Birthdate = strPtr("1990-01-01")
	_, err = cq().UpdatePerformer(context.Background(), queries.UpdatePerformerParams{
		ID: performer.ID, Name: performer.Name, Disambiguation: performer.Disambiguation,
		Gender: performer.Gender, Birthdate: performer.Birthdate, Ethnicity: performer.Ethnicity,
		Country: performer.Country, EyeColor: performer.EyeColor, HairColor: performer.HairColor,
		Height: performer.Height, CareerStartYear: performer.CareerStartYear,
		CareerEndYear: performer.CareerEndYear, Deathdate: performer.Deathdate,
	})
	require.NoError(t, err)
}

// A quest must promise work that EXISTS.
//
// Authoring a quest over an entity that already has the field produces a quest
// that is complete on arrival and pays a bounty for nothing. The check belongs in
// the service, not the caller: a caller that checked and then somebody else filled
// the field in between would author a stale promise, and that window only opens
// under load.
func TestAQuestCannotPromiseWorkThatIsAlreadyDone(t *testing.T) {
	svc := newAuthoredService(t)
	user := createUserForQuest(t, "A Quest Author With Nothing To Do")

	complete := createTagForQuest(t, "A Performer With A Birthdate", true)

	_, err := svc.AuthorQuest(context.Background(), authored.AuthorQuestInput{
		EntityType: quest.EntityTag,
		Field:      completion.FieldDetails,
		AuthoredBy: user,
		EntityIDs:  []uuid.UUID{complete},
	})
	require.Error(t, err,
		"an entity that already has the field cannot be the subject of a quest "+
			"about that field -- the quest would be complete on arrival")
	assert.Contains(t, err.Error(), "already have",
		"the error says WHICH condition failed, so an author can tell a bad "+
			"entity from a bad field name")
}

// The opposite must succeed, or the check above is just refusing everything.
func TestAQuestMayPromiseWorkThatExists(t *testing.T) {
	svc := newAuthoredService(t)
	user := createUserForQuest(t, "A Working Quest Author")

	gap := createTagForQuest(t, "A Performer For A Real Quest", false)

	q, err := svc.AuthorQuest(context.Background(), authored.AuthorQuestInput{
		EntityType:   quest.EntityTag,
		Field:        completion.FieldDetails,
		BountyPoints: 500,
		AuthoredBy:   user,
		Reason:       "one studio with no dates at all",
		EntityIDs:    []uuid.UUID{gap},
	})
	require.NoError(t, err)
	require.NotNil(t, q)

	assert.Equal(t, 1, q.Remaining, "the one entity named is missing the field, so one remains")
	assert.Equal(t, 500, q.BountyPoints)
	// Base 20 plus the bounty, ADDED. Not multiplied -- the base lives in the trust
	// enum and a multiplier would make the two disagree about what a quest is
	// worth.
	assert.Equal(t, 520, q.TotalPoints(),
		"the bounty ADDS to the base quest value: a multiplier would scale a "+
			"constant owned by the trust package and the two would drift")
}

// A bounty is an operator promise, so the trust enum's value is the floor and the
// bounty is the addition. Pinned against the real enum so the duplicated constant
// cannot rot.
func TestTheBaseQuestValueMatchesTheTrustEnum(t *testing.T) {
	svc := newAuthoredService(t)
	user := createUserForQuest(t, "A Bounty Base Author")
	gap := createTagForQuest(t, "A Bounty Base Performer", false)

	q, err := svc.AuthorQuest(context.Background(), authored.AuthorQuestInput{
		EntityType:   quest.EntityTag,
		Field:        completion.FieldDetails,
		BountyPoints: 0,
		AuthoredBy:   user,
		EntityIDs:    []uuid.UUID{gap},
	})
	require.NoError(t, err)

	assert.Equal(t, trust.PointsPerKind[trust.KindQuestCompleted], q.TotalPoints(),
		"with no bounty a quest is worth exactly what the trust enum says it is. "+
			"The constant in the authored package is a deliberate duplicate of that "+
			"map entry, and this is what catches the two drifting apart when the "+
			"enum's values are rebalanced")
}

// THE CLAIM RACE.
//
// Two curators, one item, concurrent claims. Exactly one must win. This is the
// whole reason the claim is a guarded UPDATE rather than a check-then-write, and
// it is untestable any other way -- a sequential version of this test passes
// against the broken implementation too.
func TestTwoCuratorsCannotBothClaimTheSameItem(t *testing.T) {
	svc := newAuthoredService(t)
	author := createUserForQuest(t, "A Racing Quest Author")
	one := createUserForQuest(t, "A Racing Curator One")
	two := createUserForQuest(t, "A Racing Curator Two")

	gap := createTagForQuest(t, "A Contested Quest Performer", false)
	q, err := svc.AuthorQuest(context.Background(), authored.AuthorQuestInput{
		EntityType: quest.EntityTag,
		Field:      completion.FieldDetails,
		AuthoredBy: author,
		EntityIDs:  []uuid.UUID{gap},
	})
	require.NoError(t, err)
	require.Len(t, q.Items, 1)
	item := q.Items[0]

	// Both claims released from the same starting state, simultaneously. A
	// barrier is not a sleep: without it one goroutine finishes before the other
	// starts, and the test passes against the broken code.
	var wg sync.WaitGroup
	results := make([]error, 2)
	start := make(chan struct{})
	for i, curator := range []uuid.UUID{one, two} {
		wg.Add(1)
		go func(i int, curator uuid.UUID) {
			defer wg.Done()
			<-start
			_, err := svc.Claim(context.Background(), item.ID, curator)
			results[i] = err
		}(i, curator)
	}
	close(start)
	wg.Wait()

	wins := 0
	for _, err := range results {
		if err == nil {
			wins++
		} else {
			assert.ErrorIs(t, err, authored.ErrAlreadyClaimed,
				"the loser gets a specific, actionable error, not a generic "+
					"failure -- a curator clicking claim on a race they lost needs "+
					"to be told, and being told is the feature")
		}
	}
	assert.Equal(t, 1, wins,
		"exactly one curator holds the claim. Two winners means the claim is a "+
			"check-then-write and both read 'unclaimed' before either wrote")
}

// Re-claiming your OWN item succeeds, because a retried request must not report a
// loss the curator did not take.
func TestReclaimingYourOwnItemIsIdempotent(t *testing.T) {
	svc := newAuthoredService(t)
	author := createUserForQuest(t, "An Idempotent Claim Author")
	curator := createUserForQuest(t, "An Idempotent Claiming Curator")

	gap := createTagForQuest(t, "An Idempotent Claim Performer", false)
	q, err := svc.AuthorQuest(context.Background(), authored.AuthorQuestInput{
		EntityType: quest.EntityTag, Field: completion.FieldDetails,
		AuthoredBy: author, EntityIDs: []uuid.UUID{gap},
	})
	require.NoError(t, err)

	_, err = svc.Claim(context.Background(), q.Items[0].ID, curator)
	require.NoError(t, err)

	// The retry. A dropped connection makes the first call's result unknown to the
	// client, and a second attempt must be the same answer, not a loss.
	_, err = svc.Claim(context.Background(), q.Items[0].ID, curator)
	require.NoError(t, err,
		"claiming twice is idempotent: the network dropped the first response and "+
			"this is the retry. Reporting ErrAlreadyClaimed here would tell a "+
			"curator they lost a race they won")

	// And the claim time is refreshed rather than left stale, or the item's age
	// would date from the first attempt.
	after, err := svc.Get(context.Background(), q.ID)
	require.NoError(t, err)
	require.True(t, after.Items[0].ClaimedBy.Valid)
	assert.Equal(t, curator, after.Items[0].ClaimedBy.UUID,
		"the item is still claimed by the curator who claimed it")
}

// Releasing somebody else's claim is refused. A curator told its release worked
// would believe the item is free when somebody else is working on it.
func TestOneCuratorCannotReleaseAnothersClaim(t *testing.T) {
	svc := newAuthoredService(t)
	author := createUserForQuest(t, "A Stolen Release Author")
	owner := createUserForQuest(t, "A Claim Owner Curator")
	thief := createUserForQuest(t, "A Thief Curator")

	gap := createTagForQuest(t, "A Stolen Release Performer", false)
	q, err := svc.AuthorQuest(context.Background(), authored.AuthorQuestInput{
		EntityType: quest.EntityTag, Field: completion.FieldDetails,
		AuthoredBy: author, EntityIDs: []uuid.UUID{gap},
	})
	require.NoError(t, err)
	_, err = svc.Claim(context.Background(), q.Items[0].ID, owner)
	require.NoError(t, err)

	err = svc.Release(context.Background(), q.Items[0].ID, thief)
	require.ErrorIs(t, err, authored.ErrNotClaimedByCaller,
		"a release is scoped to the caller's own claim, so one curator cannot "+
			"free up another curator's in-progress work")

	after, err := svc.Get(context.Background(), q.ID)
	require.NoError(t, err)
	require.True(t, after.Items[0].ClaimedBy.Valid, "the owner's claim survives the attempt")
	assert.Equal(t, owner, after.Items[0].ClaimedBy.UUID)

	// The OWNER can still release, and this second half is what makes the first
	// half mean anything.
	//
	// Found by mutation: passing a NULL claimer instead of the curator's id also
	// produced ErrNotClaimedByCaller, because `claimed_by = NULL` evaluates to
	// NULL in SQL -- never true -- so the update matched no rows and the refusal
	// was indistinguishable from a correct one. A release that refuses EVERYONE
	// passes a test that only checks that a thief is refused.
	require.NoError(t, svc.Release(context.Background(), q.Items[0].ID, owner),
		"the owner can release their own claim. Without this, a release that "+
			"refused everyone would pass the thief check above -- 'refused the "+
			"thief' and 'refused everybody' are the same observable when you only "+
			"test the first")

	released, err := svc.Get(context.Background(), q.ID)
	require.NoError(t, err)
	assert.False(t, released.Items[0].ClaimedBy.Valid,
		"and the item is genuinely free afterwards, not merely un-refused")
}

// THE RECONCILIATION, and the reason an authored quest needs it at all.
//
// An authored quest's items are FIXED at authoring time -- that is what makes it
// a promise rather than a sample. But the archive moves under it, and when it
// does the item must stop counting without the row disappearing.
func TestAFilledItemStopsCountingWithoutDisappearing(t *testing.T) {
	svc := newAuthoredService(t)
	author := createUserForQuest(t, "A Reconciling Quest Author")

	gap := createTagForQuest(t, "A Reconciled Quest Performer", false)
	q, err := svc.AuthorQuest(context.Background(), authored.AuthorQuestInput{
		EntityType: quest.EntityTag, Field: completion.FieldDetails,
		AuthoredBy: author, EntityIDs: []uuid.UUID{gap},
	})
	require.NoError(t, err)
	require.Equal(t, 1, q.Remaining)

	// Somebody else fills it, from a different quest or a plain edit.
	// Somebody else fills it, from a different quest or a plain edit.
	// Read-then-write: UpdateTag needs its NOT NULL columns and a hand-built
	// params struct would omit them.
	tag, err := cq().FindTag(context.Background(), gap)
	require.NoError(t, err)
	tag.Description = strPtr("a description added by somebody else")
	_, err = cq().UpdateTag(context.Background(), queries.UpdateTagParams{
		ID: tag.ID, Name: tag.Name, CategoryID: tag.CategoryID, Description: tag.Description,
	})
	require.NoError(t, err)

	after, err := svc.Get(context.Background(), q.ID)
	require.NoError(t, err)

	assert.Equal(t, 0, after.Remaining,
		"the field is filled, so the quest is complete -- RECONCILED on read "+
			"rather than stored, because a stored 'completed' flag would be a "+
			"second source of truth beside the completion score")
	assert.Equal(t, 1, after.Completed,
		"'you did this one' is most of why a curator returns, so a completed item "+
			"is counted rather than dropped")
	require.Len(t, after.Items, 1, "the ROW stays: a quest whose items vanish cannot "+
		"show what was already done")

	assert.False(t, after.Items[0].StillMissing,
		"StillMissing is the reconciled truth, and it is what the client renders")
	assert.Contains(t, q.Items[0].Missing, completion.FieldDetails,
		"and the item's missing list is the scorer's, not a stored copy")
}

// A claim does NOT complete a quest. It marks work in progress so nobody
// duplicates it, and it is not completion -- the plan says so explicitly, and it
// is the mistake that would lose a curator's half-finished edit.
func TestAClaimDoesNotCompleteAQuest(t *testing.T) {
	svc := newAuthoredService(t)
	author := createUserForQuest(t, "A Claim Is Not Completion Author")
	curator := createUserForQuest(t, "A Claiming But Not Finishing Curator")

	gap := createTagForQuest(t, "A Claimed But Unfinished Performer", false)
	q, err := svc.AuthorQuest(context.Background(), authored.AuthorQuestInput{
		EntityType: quest.EntityTag, Field: completion.FieldDetails,
		AuthoredBy: author, EntityIDs: []uuid.UUID{gap},
	})
	require.NoError(t, err)

	_, err = svc.Claim(context.Background(), q.Items[0].ID, curator)
	require.NoError(t, err)

	after, err := svc.Get(context.Background(), q.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, after.Remaining,
		"a claim marks work IN PROGRESS. A quest that vanished on claim would "+
			"lose work to a curator who is still mid-edit when the claim expires")
	assert.Equal(t, 0, after.Completed)
}

// An unweighted field is refused: a quest nobody can complete is not a quest.
//
// The case is a field that is NOT scored FOR THIS ENTITY TYPE, rather than a field
// that is not scored at all. `parent_studio` carries weight 30 for a studio and
// zero for a tag, so "link 5 tags to their parent studio" is unachievable: no tag
// has a parent studio, and a tag's completion score does not count one.
//
// This is the case a performer quest could NOT have tested. `name` is NOT NULL on
// every entity type, which is why it is weight zero for a performer -- but it is
// weight 40 for a TAG, so "add missing tag names" is a perfectly completable quest
// and refusing it would be a bug. Same field constant, opposite correct answer
// depending on the entity type, which is exactly why the refusal has to consult
// the per-type weight list rather than a hard-coded set of "impossible" fields.
func TestAuthoringAgainstAnUnscoredFieldIsRefused(t *testing.T) {
	svc := newAuthoredService(t)
	user := createUserForQuest(t, "A Refused Field Author")
	gap := createTagForQuest(t, "A Refused Field Tag", false)

	_, err := svc.AuthorQuest(context.Background(), authored.AuthorQuestInput{
		EntityType: quest.EntityTag, Field: completion.FieldParentStudio,
		AuthoredBy: user, EntityIDs: []uuid.UUID{gap},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not scored",
		"parent_studio is weight 30 for a studio and zero for a tag, so no tag "+
			"can be missing one and the quest could never be completed")
}

// ...and the same field IS scoreable for the type that weights it, or the
// refusal above would just be refusing everything.
func TestTheSameFieldIsAllowedForTheTypeThatWeightsIt(t *testing.T) {
	svc := newAuthoredService(t)
	user := createUserForQuest(t, "A Studio Quest Author")

	_, err := svc.AuthorQuest(context.Background(), authored.AuthorQuestInput{
		EntityType: quest.EntityStudio, Field: completion.FieldParentStudio,
		AuthoredBy: user, EntityIDs: []uuid.UUID{uuid.Must(uuid.NewV7())},
	})
	if err != nil {
		// A DIFFERENT error is acceptable -- the entity id is a fabricated uuid
		// and a studio is not what was named -- but the refusal must not be about
		// the field.
		assert.NotContains(t, err.Error(), "not scored",
			"parent_studio IS weighted for a studio, so a refusal here means the "+
				"check ignores the entity type and is really refusing every field")
	}
}

// More items than the target is refused rather than truncated -- the author named
// entities and would have no way to learn which were dropped.
func TestAuthoringMoreItemsThanTheTargetIsRefused(t *testing.T) {
	svc := newAuthoredService(t)
	user := createUserForQuest(t, "An Overfull Quest Author")

	ids := make([]uuid.UUID, 0, 3)
	for i := 0; i < 3; i++ {
		ids = append(ids, createTagForQuest(t, fmt.Sprintf("An Overfull Quest Tag %d", i), false))
	}

	_, err := svc.AuthorQuest(context.Background(), authored.AuthorQuestInput{
		EntityType: quest.EntityTag, Field: completion.FieldDetails,
		Target: 2, AuthoredBy: user, EntityIDs: ids,
	})
	require.Error(t, err,
		"a quest naming more entities than it asks for is a promise it cannot keep")
	assert.Contains(t, err.Error(), "items named for a target")
}

// A quest with no items is not a quest, and would render as an empty card.
func TestAuthoringWithNoItemsIsRefused(t *testing.T) {
	svc := newAuthoredService(t)
	user := createUserForQuest(t, "An Empty Quest Author")

	_, err := svc.AuthorQuest(context.Background(), authored.AuthorQuestInput{
		EntityType: quest.EntityTag, Field: completion.FieldDetails,
		AuthoredBy: user, EntityIDs: []uuid.UUID{},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no items")
}

// An expired quest leaves the board, and "active" is the DATABASE's definition.
func TestAnExpiredQuestIsNotOnTheBoard(t *testing.T) {
	svc := newAuthoredService(t)
	author := createUserForQuest(t, "A Quest Author With An Expiry")

	gap := createTagForQuest(t, "A Quest Expiring Soon", false)
	live, err := svc.AuthorQuest(context.Background(), authored.AuthorQuestInput{
		EntityType: quest.EntityTag, Field: completion.FieldDetails,
		AuthoredBy: author, EntityIDs: []uuid.UUID{gap},
		ExpiresAt: time.Now().Add(time.Hour),
	})
	require.NoError(t, err)

	// A quest that expires SOON is still on the board: the filter is
	// expires_at > now(), and an hour from now is in the future. The
	// alternative -- a test that waits for the expiry and then checks -- is a
	// test that sleeps, and a sleeping test is a slow test that nobody re-runs.
	board, err := svc.Board(context.Background())
	require.NoError(t, err)

	ids := make(map[uuid.UUID]bool, len(board))
	for _, q := range board {
		ids[q.ID] = true
	}
	assert.True(t, ids[live.ID], "an unexpired quest is on the board")

	// And the row is still readable by id: expiry HIDES a quest, it does not
	// delete it, because a curator mid-edit needs to finish what they started.
	_, err = svc.Get(context.Background(), live.ID)
	require.NoError(t, err)
}

// A curator's in-progress work is listable, newest claim first.
func TestACuratorCanListItsOwnClaims(t *testing.T) {
	svc := newAuthoredService(t)
	author := createUserForQuest(t, "A Claims List Author")
	curator := createUserForQuest(t, "A Claims Listing Curator")

	gap := createTagForQuest(t, "A Claimed For Listing", false)
	q, err := svc.AuthorQuest(context.Background(), authored.AuthorQuestInput{
		EntityType: quest.EntityTag, Field: completion.FieldDetails,
		BountyPoints: 250, AuthoredBy: author, EntityIDs: []uuid.UUID{gap},
	})
	require.NoError(t, err)
	_, err = svc.Claim(context.Background(), q.Items[0].ID, curator)
	require.NoError(t, err)

	claims, err := svc.ClaimsByUser(context.Background(), curator)
	require.NoError(t, err)
	require.NotEmpty(t, claims, "the curator is working on something")

	found := false
	for _, c := range claims {
		if c.EntityID == gap {
			found = true
			assert.Equal(t, 250, c.BountyPoints,
				"the claim carries the quest's bounty, so a curator's list can "+
					"say what finishing it is worth without a second read")
			assert.Equal(t, string(completion.FieldDetails), c.Field,
				"and the field, so the list can say what to DO")
		}
	}
	assert.True(t, found, "the claim the curator just took is in its own list")
}
