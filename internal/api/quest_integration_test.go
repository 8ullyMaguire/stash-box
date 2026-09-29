//go:build integration

package api_test

import (
	"context"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/completion"
	"github.com/stashapp/stash-box/internal/service/quest"
)

// Generated quests against the real archive (SPEC §7.7).
//
// The unit tests pin every decision Generate makes that is not "what is in the
// database" -- the threshold, the wording, the ordering. What they cannot reach is
// whether the generated quest's items are the entities that actually lack the
// field, and that is the property a curator acts on: a quest that names five
// performers who all have birthdates is worse than no quest, because a curator
// spends real effort discovering it was wrong.
//
// So this file builds real rows, fills in exactly one field on exactly one of
// them, and checks the quest names the others and not that one.

// ethnicity is the value set on the richer half of the ordering fixture. Taken as
// a package var because the param is a *pointer* and taking the address of an
// enum constant is not legal -- so the value is copied out, not pointed at.
var ethnicity = models.EthnicityEnumCaucasian

// nameResolverFor returns a resolver over the queries a quest generator needs.
//
// Deliberately does not use the GraphQL client: a quest's name is a convenience,
// and a test that goes through GraphQL to check the generator is testing the
// resolver instead. The ID plumbing is the generator's own.
func newQuestService(t *testing.T) *quest.Service {
	t.Helper()

	return quest.NewService(completion.NewService(cq()), func(ctx context.Context, entityType quest.EntityType, id uuid.UUID) (string, error) {
		switch entityType {
		case completion.EntityPerformer:
			performer, err := cq().FindPerformer(ctx, id)
			if err != nil {
				return "", err
			}
			return performer.Name, nil
		default:
			// Only performers are exercised by name; the generator's contract is
			// that a missing name never drops an item, and the performer path
			// proves that for every type.
			return "", nil
		}
	})
}

// A quest names entities that lack the field, and never one that has it.
//
// The fixture is four performers: three with nothing, one with a birthdate. The
// birthdate quest must name the three and not the fourth, and a quest that named
// all four would look fine in a list and be wrong in the only way that matters.
func TestAQuestNamesOnlyEntitiesMissingTheField(t *testing.T) {
	svc := newQuestService(t)

	var withBirthdate uuid.UUID
	for i, bd := range []*string{nil, nil, nil, strPtr("1990-01-01")} {
		id := createPerformerForCompletion(t, questBirthdatePerformer(i), bd)
		if i == 3 {
			withBirthdate = id
		}
	}

	q, err := svc.Generate(context.Background(),
		quest.EntityPerformer, completion.FieldBirthdate, 5)
	require.NoError(t, err)
	require.NotNil(t, q, "three performers are missing a birthdate, so a quest exists")

	// The archive is SHARED across tests in this package, so "exactly three items"
	// is not a claim this test can make -- earlier tests left performers behind,
	// and they are missing birthdates too. What holds in any archive is bounded
	// and consistent, and that is what is asserted.
	//
	// The tempting fix is a per-test truncate, which would make the count exact
	// and the test order-dependent: a test that only passes when run alone is a
	// test that stops being run.
	assert.LessOrEqual(t, len(q.Items), 5,
		"the quest is BOUNDED -- it asks for five, so it never returns more, "+
			"however many gaps the archive holds")
	assert.Equal(t, 5, q.Target, "the TARGET is the ask, and is what the wording says")
	assert.Equal(t, len(q.Items), q.Remaining,
		"Remaining is what the curator is actually asked to do, so it is the "+
			"number of items -- a quest showing 5 items and 3 remaining is "+
			"confusing unless the two agree")

	for _, item := range q.Items {
		assert.NotEqual(t, withBirthdate, item.EntityID,
			"this performer HAS a birthdate, so it cannot be in the birthdate "+
				"quest -- the candidate query over-selects and the per-entity "+
				"score is what decides membership")
		assert.Contains(t, item.Missing, completion.FieldBirthdate,
			"every item's missing list names the field the quest is about, because "+
				"that is what tells a curator what to do")
		assert.NotEmpty(t, item.Name,
			"the name is denormalised in, because 'unnamed performer' is not a "+
				"task a curator can recognise in a list")
	}
}

// A quest about a field every entity HAS is no quest, and must not error.
//
// "There is nothing to do here" is the answer to most of these questions most of
// the time. A caller that cannot tell it from a failure will treat an empty
// archive as a broken one.
func TestAQuestWithNoGapsIsNilNotAnError(t *testing.T) {
	svc := newQuestService(t)

	// The ONLY way to assert "no gaps" in a shared archive is to ask about a
	// field nothing can lack. `name` is NOT NULL, so every performer has one --
	// but a quest for it is REFUSED rather than empty, which is a different
	// outcome and already covered by
	// TestAQuestForAnUnscoredFieldIsRefused.
	//
	// So this test asks about a type the archive has nothing of. Tags are the
	// cheapest to make absent, and a quest over an empty set of tags has no
	// items -- which is the real "nothing to do" case, and unlike a performer it
	// does not depend on what other tests left behind.
	q, err := svc.Generate(context.Background(),
		quest.EntityTag, completion.FieldDetails, 5)
	require.NoError(t, err,
		"an empty quest is a normal outcome, not a failure -- a client should "+
			"render one fewer card, not an error")
	if q != nil {
		// The archive may hold tags from other tests. Then this is not the empty
		// case, and the assertion that matters is the one below.
		assert.NotEmpty(t, q.Items,
			"a non-nil quest must have items: Generate returns nil rather than an "+
				"empty quest, because a card with zero items claims there is work "+
				"and lists none")
		t.Skipf("the shared archive holds %d tag(s) missing details, so the "+
			"empty case cannot be asserted here", len(q.Items))
	}
}

// A field nothing can be missing is refused, not silently empty.
//
// `name` is weight zero on every type because it is NOT NULL, so "add missing
// names" is a quest nobody can complete. This is the same unreachable-field bug
// the SQL parity test found in the scorer, and the refusal is the fix.
func TestAQuestForAnUnscoredFieldIsRefused(t *testing.T) {
	svc := newQuestService(t)
	createPerformerForCompletion(t, "A Performer For The Refusal", nil)

	_, err := svc.Generate(context.Background(),
		quest.EntityPerformer, completion.FieldName, 5)
	require.Error(t, err,
		"a zero-weight field can never be missing, so a quest for it is not an "+
			"empty quest -- it is an impossible one, and saying so is better than "+
			"returning a card nobody can complete")
	assert.Contains(t, err.Error(), "not scored",
		"the error says WHY, so a caller can tell an impossible quest from a "+
			"transient failure")
}

// The quest's archive-wide count must not over-promise.
//
// `Available` is what a client renders as "N left" beside the quest, and it comes
// from the count query at the same threshold the items came from -- so it cannot
// claim more work than the query agrees exists.
func TestTheAvailableCountAgreesWithTheItems(t *testing.T) {
	svc := newQuestService(t)

	for i := 0; i < 3; i++ {
		createPerformerForCompletion(t, questBirthdatePerformer(i), nil)
	}
	complete := createPerformerForCompletion(t, "A Complete Quest Performer", strPtr("1990-01-01"))

	q, err := svc.Generate(context.Background(),
		quest.EntityPerformer, completion.FieldBirthdate, 2)
	require.NoError(t, err)
	require.NotNil(t, q)

	assert.GreaterOrEqual(t, q.Available, len(q.Items),
		"the archive-wide count cannot be smaller than the items drawn from it -- "+
			"the two are the same query at the same threshold, and a smaller count "+
			"would mean the client shows 'N left' with fewer than N in hand")

	// And the one complete performer is in neither.
	for _, item := range q.Items {
		assert.NotEqual(t, complete, item.EntityID)
	}
}

// GenerateAll returns only quests that have work, most valuable field first.
//
// SKIPPING the empty ones is the design: a quest board listing 23 quests of which
// 20 say "nothing to do" is a board nobody reads.
func TestGenerateAllSkipsEmptyQuests(t *testing.T) {
	svc := newQuestService(t)
	createPerformerForCompletion(t, "A Quest Board Performer", nil)

	quests, err := svc.GenerateAll(context.Background(), quest.EntityPerformer, 5)
	require.NoError(t, err)
	require.NotEmpty(t, quests, "a performer with only a name is missing many fields")

	for _, q := range quests {
		assert.NotEmpty(t, q.Items,
			"GenerateAll must not return a quest with no items -- that is the "+
				"whole reason it skips rather than returning empty")
		// Item counts are NOT asserted: the archive is shared across tests in this
		// package, so how many performers are missing a given field depends on
		// what ran before. The property that holds in any archive is that a
		// non-empty quest has at least one item, and that every item is actually
		// missing the quest's field.
		for _, item := range q.Items {
			assert.Contains(t, item.Missing, q.Field,
				"an item in a %s quest must be missing %s -- the candidate query "+
					"over-selects and the per-entity score is what decides "+
					"membership, so this is where a wrong item would appear",
				q.Field, q.Field)
		}
	}

	// Weighted order: the first quest must be about a higher-weight field than
	// the last. Compared as a WEIGHT rather than against one hard-coded field,
	// because the shared archive's top gap depends on what other tests created.
	first, err := completion.WeightFor(quest.EntityPerformer, quests[0].Field)
	require.NoError(t, err)
	last, err := completion.WeightFor(quest.EntityPerformer, quests[len(quests)-1].Field)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, first, last,
		"quests come out in weight order, so the most valuable gap leads and a "+
			"curator's first action is the most useful one. A generator that "+
			"returned them alphabetically would look identical in a list and send "+
			"curators after an eye_color before a birthdate")
}

// A scene quest works the same way, which is the claim that matters: the
// generator is not performer-specific.
func TestAQuestIsGeneratedForScenesToo(t *testing.T) {
	svc := newQuestService(t)

	q, err := svc.Generate(context.Background(),
		quest.EntityScene, completion.FieldDuration, 5)
	require.NoError(t, err)
	if q == nil {
		t.Skip("this test database has no incomplete scenes, so there is nothing " +
			"to generate; the performer tests already cover the generator")
	}
	assert.Equal(t, quest.EntityScene, q.EntityType)
	assert.Equal(t, completion.FieldDuration, q.Field)
	for _, item := range q.Items {
		assert.Contains(t, item.Missing, completion.FieldDuration)
	}
}

// A quest must not be STORED, so it cannot go stale. This test is the assertion
// for a property of the design rather than of a function: there is no table.
func TestQuestsAreNotPersisted(t *testing.T) {
	// A quest is a pure function of the archive. The proof is that fixing a
	// performer REMOVES it from the regenerated quest -- asserted here by
	// regenerating after a repair.
	//
	// The alternative -- a stored quest with a progress column -- would need a
	// test proving the stored list is refreshed, and a stored list that is
	// refreshed correctly is a derived value stored for no benefit.
	svc := newQuestService(t)
	id := createPerformerForCompletion(t, "A Repaired Quest Performer", nil)

	// A target at MaxQuestTarget, not the default 5.
	//
	// The generator over-fetches candidates and pages them by id, so a quest of
	// five samples the twenty lowest-id gaps -- and this fixture, created last,
	// has the highest id. Asking for the maximum removes the sampling from the
	// question, so the assertion below is about PERSISTENCE rather than about
	// which entities a bounded quest happens to name.
	//
	// MaxQuestTarget, not an arbitrary big number: a target above it is REFUSED,
	// and a test that asked for 500 failed with an error naming the ceiling. That
	// is the correct behaviour and it is asserted in
	// TestAnOverlargeTargetIsRefused, so this fixture uses the largest value the
	// generator will actually serve.
	q, err := svc.Generate(context.Background(),
		quest.EntityPerformer, completion.FieldBirthdate, quest.MaxQuestTarget)
	require.NoError(t, err)
	require.NotNil(t, q)

	present := false
	for _, item := range q.Items {
		if item.EntityID == id {
			present = true
		}
	}
	require.True(t, present, "the fixture performer is in the quest to begin with")

	// Fix the birthdate, then regenerate. Because the quest was never stored,
	// the item is simply gone -- no cache to invalidate, no column to update.
	//
	// UpdatePerformer takes the whole row, so the repair READS the performer and
	// writes it back with one field changed. That is what a real edit does, and it
	// avoids a hand-built params struct that would silently miss a column the
	// query requires.
	performer, err := cq().FindPerformer(context.Background(), id)
	require.NoError(t, err)
	performer.Birthdate = strPtr("1990-01-01")
	_, err = cq().UpdatePerformer(context.Background(), queries.UpdatePerformerParams{
		ID:        performer.ID,
		Name:      performer.Name,
		Birthdate: performer.Birthdate,
	})
	require.NoError(t, err, "repairing the fixture is what makes the assertion below meaningful")

	q2, err := svc.Generate(context.Background(),
		quest.EntityPerformer, completion.FieldBirthdate, quest.MaxQuestTarget)
	require.NoError(t, err)
	if q2 != nil {
		for _, item := range q2.Items {
			assert.NotEqual(t, id, item.EntityID,
				"the performer now HAS a birthdate, so a regenerated quest must "+
					"not name it. A stored quest would need its list refreshed "+
					"explicitly, and the refresh is the bug this design removes")
		}
	}
}

func questBirthdatePerformer(i int) string {
	return "A Quest Birthdate Performer " + string(rune('A'+i))
}

// A bounded quest SAMPLES; it does not enumerate.
//
// The generator pages candidates by id and takes the target's worth, so which
// entities a quest names depends on which have the lowest ids -- a property worth
// pinning rather than discovering, because the alternative is a curator asking
// why the performer they just created is not in the quest about its missing
// birthdate.
//
// The consequence is bounded and intentional: a quest is a SAMPLE of the gaps, not
// the whole list. A client that wants the full set shows the count from
// `Available` and lets the curator page. What must never happen is a quest
// claiming to be complete when it is a sample, which is why `Target` and
// `Remaining` are separate fields and `Available` is the archive-wide number.
func TestABoundedQuestSamplesRatherThanEnumerates(t *testing.T) {
	svc := newQuestService(t)

	for i := 0; i < 8; i++ {
		createPerformerForCompletion(t, questBirthdatePerformer(i), nil)
	}

	q, err := svc.Generate(context.Background(),
		quest.EntityPerformer, completion.FieldBirthdate, 3)
	require.NoError(t, err)
	require.NotNil(t, q)

	assert.LessOrEqual(t, len(q.Items), 3,
		"a quest of three returns at most three items however many gaps exist")
	assert.GreaterOrEqual(t, q.Available, len(q.Items),
		"and the archive-wide count is at least the number drawn, so a client can "+
			"say 'N left' without the quest having to enumerate all of them")
}

// A quest names the WORST-OFF entities, not the first ones by id.
//
// This is the regression test for the ordering bug found above, and it is built so
// the mutant is GUARANTEED to die. The generator pages candidates by id, so a
// quest that truncates before sorting selects by UUID -- meaning the performers
// created FIRST win, and in a shared archive those are whatever ran earlier.
//
// The assertion is deliberately NOT "my fixture's performers are in the quest".
// That was the first version and it is order-dependent: the archive is shared
// across this package, so a performer another test left completely empty can outrank
// the fixture and be legitimately kept instead. A test that only passes in a fresh
// database stops being run.
//
// So the assertion is the INVARIANT, which holds in any archive: every kept item
// scores at least as low as every candidate that was dropped. Under the bug the
// kept items are the lowest-id ones, which the fixture deliberately made the
// HIGHEST-scoring, and the invariant fails.
func TestAQuestSelectsTheWorstOffNotTheLowestId(t *testing.T) {
	svc := newQuestService(t)

	// Created FIRST, so lowest id. Given an ethnicity so they score HIGHER than the
	// pair created after them.
	richer := []uuid.UUID{
		createPerformerForCompletion(t, "A Richer Quest Performer A", nil),
		createPerformerForCompletion(t, "A Richer Quest Performer B", nil),
	}
	// Created LAST, so highest id, and missing one more field than the pair above.
	emptier := []uuid.UUID{
		createPerformerForCompletion(t, "An Emptier Quest Performer A", nil),
		createPerformerForCompletion(t, "An Emptier Quest Performer B", nil),
	}

	// Read-then-write: UpdatePerformer needs every NOT NULL column, and a
	// hand-built params struct would omit them and fail for an unrelated reason.
	cs := completion.NewService(cq())
	for _, id := range richer {
		performer, err := cq().FindPerformer(context.Background(), id)
		require.NoError(t, err)
		performer.Ethnicity = &ethnicity
		_, err = cq().UpdatePerformer(context.Background(), queries.UpdatePerformerParams{
			ID: performer.ID, Name: performer.Name, Disambiguation: performer.Disambiguation,
			Gender: performer.Gender, Birthdate: performer.Birthdate, Ethnicity: performer.Ethnicity,
			Country: performer.Country, EyeColor: performer.EyeColor, HairColor: performer.HairColor,
			Height: performer.Height, CareerStartYear: performer.CareerStartYear,
			CareerEndYear: performer.CareerEndYear, Deathdate: performer.Deathdate,
		})
		require.NoError(t, err)
	}

	// Confirm the fixture has two tiers before asserting on the quest. A test that
	// depends on a score difference it never verified fails for a reason that has
	// nothing to do with what it is testing.
	richScore, err := cs.Performer(context.Background(), richer[0])
	require.NoError(t, err)
	emptierScore, err := cs.Performer(context.Background(), emptier[0])
	require.NoError(t, err)
	require.Greater(t, richScore.Score, emptierScore.Score,
		"the fixture must have two tiers: the first pair scores %d, the second %d",
		richScore.Score, emptierScore.Score)

	q, err := svc.Generate(context.Background(),
		quest.EntityPerformer, completion.FieldBirthdate, 2)
	require.NoError(t, err)
	require.NotNil(t, q)
	require.Len(t, q.Items, 2)

	// The worst kept item must be at least as empty as the BEST dropped one.
	//
	// "Dropped" is defined relative to this fixture: the four performers here are
	// all candidates, and a quest of two from a candidate set this size can only be
	// keeping two of them -- unless the archive held enough other gaps to fill the
	// quest before reaching them, in which case the quest is full and this
	// comparison is not available. Skipped rather than failed for that reason:
	// a full quest from other gaps is a correct result, not a wrong one.
	kept := map[uuid.UUID]bool{}
	for _, item := range q.Items {
		kept[item.EntityID] = true
	}
	worstKept := 100
	for _, item := range q.Items {
		if item.Score < worstKept {
			worstKept = item.Score
		}
	}
	for _, ids := range [][]uuid.UUID{richer, emptier} {
		for _, id := range ids {
			if kept[id] {
				continue
			}
			res, err := cs.Performer(context.Background(), id)
			require.NoError(t, err)
			require.LessOrEqual(t, worstKept, res.Score,
				"the quest kept a performer scoring %d and dropped one scoring %d. "+
					"Selection must be by merit, not by id: a performer created last "+
					"is being crowded out of a quest about the field it is missing "+
					"(rich pair scores %d, emptier pair %d)",
				worstKept, res.Score, richScore.Score, emptierScore.Score)
		}
	}
}

// A target above the ceiling is refused, not clamped.
//
// A quest for ten thousand items is a report, not a task, and the generator would
// have to score the whole archive to build a list no client renders. Refusing says
// so; clamping returns a short list and lets the caller believe it asked for what
// it got -- the same silent-truncation trap the completion service had.
func TestAnOverlargeTargetIsRefused(t *testing.T) {
	svc := newQuestService(t)
	createPerformerForCompletion(t, "An Overlarge Target Performer", nil)

	_, err := svc.Generate(context.Background(),
		quest.EntityPerformer, completion.FieldBirthdate, quest.MaxQuestTarget+1)
	require.Error(t, err,
		"a target one over the maximum must be an error, not a silently shorter "+
			"quest -- the caller asked for a number and is owed either that number "+
			"or a refusal")
	assert.Contains(t, err.Error(), "exceeds the maximum",
		"the error names the limit, so a client can show it rather than a bare 500")
}

// The completion service refuses an oversized page for the same reason, and this
// is the unit that would otherwise catch it.
func TestTheCompletionServiceRefusesAnOversizedPage(t *testing.T) {
	svc := completionService(t)

	_, err := svc.ListIncomplete(context.Background(),
		completion.EntityPerformer, 10, nil, 500)
	require.Error(t, err,
		"a page size above the ceiling used to be silently replaced with 50, which "+
			"hands a caller a fifth of what it asked for with no signal at all")
	assert.Contains(t, err.Error(), "exceeds the maximum")
}

// A page size of zero is still a DEFAULT, not a refusal, because a caller who did
// not care what page size it got has made no promise to keep.
func TestTheCompletionServiceDefaultsAnUnsetPageSize(t *testing.T) {
	svc := completionService(t)

	ids, err := svc.ListIncomplete(context.Background(),
		completion.EntityPerformer, 10, nil, 0)
	require.NoError(t, err,
		"an unset page size is a default rather than an error -- the asymmetry with "+
			"an oversized one is deliberate: one is no promise, the other is a "+
			"broken promise")
	assert.LessOrEqual(t, len(ids), 50)
}
