//go:build integration

package api_test

import (
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/config"
	"github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/service/trust"
)

// Trust events emitted by the edit lifecycle (SPEC §6, Phase 1 Step 1.4).
//
// The service is already covered; what matters here is the WIRING, because the
// whole point of the step is that trust actually moves. A service that works
// and a service that is called are different things, and only the second one
// makes a user's trust level mean anything.
//
// So the tests below are mostly about two properties that a plausible-looking
// implementation gets wrong:
//
//  1. EXACTLY ONCE. Every accept path -- moderator immediate-accept, a vote
//     that tips the tally, the cron sweep -- must emit exactly one event. A
//     missing call site is an invisible bug: trust just quietly never rises.
//  2. EXACTLY ON THE RIGHT THING. Bot edits, failed applies, and the author's
//     own cancellations must emit nothing.

// countTrustEvents returns how many events of a kind a user has, for a given
// entity.
//
// Scoped by user, kind AND entity. A global count(*) would be meaningless
// because the integration database is shared and not isolated -- rows from other
// tests in the same run would be counted, which is the trap that has already
// bitten #829, #1007 and the anonymous-trust test.
func countTrustEvents(t *testing.T, userID uuid.UUID, kind trust.KindEnum, entityID uuid.UUID) int {
	t.Helper()
	var count int
	row := testutil.DB().QueryRow(t.Context(),
		`SELECT count(*) FROM trust_events
		 WHERE user_id = $1 AND kind = $2 AND entity_type = 'edit' AND entity_id = $3`,
		userID, string(kind), entityID)
	require.NoError(t, row.Scan(&count))
	return count
}

func trustTotal(t *testing.T, userID uuid.UUID, kind trust.KindEnum) int {
	t.Helper()
	totals, err := testutil.Factory().Trust().TotalsFor(t.Context(), userID)
	require.NoError(t, err)
	switch kind {
	case trust.KindEditApproved:
		return totals.ApprovedEdits
	case trust.KindEditRejected:
		return totals.RejectedEdits
	}
	t.Fatalf("no totals column for kind %s", kind)
	return 0
}

// An edit applied by a moderator emits exactly one approval event.
func TestAppliedEditEmitsOneApprovalEvent(t *testing.T) {
	author, err := asAdmin(t).createTestUser(nil, nil)
	require.NoError(t, err)
	authorRunner := createTestRunner(t, author, nil)

	edit, err := authorRunner.createTestTagEdit(models.OperationEnumCreate, nil, nil)
	require.NoError(t, err)

	_, err = asAdmin(t).approveEdit(edit.ID)
	require.NoError(t, err)

	assert.Equal(t, 1, countTrustEvents(t, author.ID, trust.KindEditApproved, edit.ID),
		"an applied edit must emit exactly one edit_approved event for its author")
	assert.Equal(t, 1, trustTotal(t, author.ID, trust.KindEditApproved),
		"the rollup must move by one, not by one per caller of ApplyEdit")
}

// The vote path emits exactly one event too.
//
// This is the case the first test does NOT cover: CreateVote -> resolveEditStatus
// -> ApplyEdit. A vote reaching the threshold is a different call chain from a
// moderator's immediate accept, and the requirement is "one call site per event
// kind", which means the shared funnel has to actually be shared.
func TestVoteAcceptedEditEmitsOneApprovalEvent(t *testing.T) {
	author, err := asAdmin(t).createTestUser(nil, nil)
	require.NoError(t, err)
	authorRunner := createTestRunner(t, author, nil)

	edit, err := authorRunner.createTestTagEdit(models.OperationEnumCreate, nil, nil)
	require.NoError(t, err)

	// The default vote_application_threshold is 3, so ONE vote leaves the edit
	// PENDING and nothing is applied at all. The first version of this test cast
	// a single vote and asserted the funnel fired; the wiring was never reached,
	// and I read the resulting 0 events as a missing call site rather than an
	// edit that had not closed.
	threshold := config.GetVoteApplicationThreshold()
	require.Positive(t, threshold, "precondition: a threshold is configured")

	for range threshold {
		voter, err := asAdmin(t).createTestUser(nil, nil)
		require.NoError(t, err)
		voterRunner := createTestRunner(t, voter, nil)
		_, err = voterRunner.resolver.Mutation().EditVote(voterRunner.ctx, models.EditVoteInput{
			ID:   edit.ID,
			Vote: models.VoteTypeEnumAccept,
		})
		require.NoError(t, err)
	}

	assert.Equal(t, 1, countTrustEvents(t, author.ID, trust.KindEditApproved, edit.ID),
		"an edit accepted by vote must emit exactly one event, not zero and not "+
			"one per voter")
}

// A rejected edit emits a rejection event.
func TestRejectedEditEmitsOneRejectionEvent(t *testing.T) {
	author, err := asAdmin(t).createTestUser(nil, nil)
	require.NoError(t, err)
	authorRunner := createTestRunner(t, author, nil)

	edit, err := authorRunner.createTestTagEdit(models.OperationEnumCreate, nil, nil)
	require.NoError(t, err)

	threshold := config.GetVoteApplicationThreshold()
	require.Positive(t, threshold, "precondition: a threshold is configured")

	for range threshold {
		voter, err := asAdmin(t).createTestUser(nil, nil)
		require.NoError(t, err)
		voterRunner := createTestRunner(t, voter, nil)
		_, err = voterRunner.resolver.Mutation().EditVote(voterRunner.ctx, models.EditVoteInput{
			ID:   edit.ID,
			Vote: models.VoteTypeEnumReject,
		})
		require.NoError(t, err)
	}

	assert.Equal(t, 1, countTrustEvents(t, author.ID, trust.KindEditRejected, edit.ID),
		"a rejected edit must emit exactly one edit_rejected event")
	assert.Zero(t, trustTotal(t, author.ID, trust.KindEditApproved),
		"a rejected edit must not also count as an approval")
}

// Cancelling your own edit earns nothing.
//
// CANCELED means the author withdrew it, which is a decision the system
// supports. Charging trust for it would punish a user for changing their mind.
func TestCanceledEditEmitsNoTrustEvent(t *testing.T) {
	author, err := asAdmin(t).createTestUser(nil, nil)
	require.NoError(t, err)
	authorRunner := createTestRunner(t, author, nil)

	edit, err := authorRunner.createTestTagEdit(models.OperationEnumCreate, nil, nil)
	require.NoError(t, err)

	_, err = authorRunner.resolver.Mutation().CancelEdit(authorRunner.ctx, models.CancelEditInput{
		ID: edit.ID,
	})
	require.NoError(t, err)

	assert.Zero(t, countTrustEvents(t, author.ID, trust.KindEditRejected, edit.ID),
		"cancelling your own edit must emit nothing: the author withdrawing "+
			"their own work is a decision the system supports, not a failure")
	assert.Zero(t, countTrustEvents(t, author.ID, trust.KindEditApproved, edit.ID),
		"a cancelled edit is not an approval either")
}

// A bot edit earns nobody trust.
//
// This is the security property. A bot edit is machine-generated and
// auto-applied, so it does not represent human curation. If bot edits counted,
// anyone who could get a bot editing for them could farm level 4 and unlock
// content viewing -- which is exactly the attack the steep part of the level
// curve (750 points, 3x the step below) exists to prevent. One test elsewhere
// would not catch that; it has to be asserted here.
func TestBotEditEmitsNoTrustEvent(t *testing.T) {
	// validateBotEdit calls auth.ValidateBot, which requires the BOT role
	// specifically -- not ADMIN and not MODERATE. The first version of this test
	// used a plain admin author and failed with "you do not have permission to
	// submit bot edits", so the edit was never created and the assertion below
	// would have passed vacuously had it been reached.
	//
	// BOT alone cannot create an edit either, so the author holds MODERATE (to
	// submit) plus BOT (to set the flag), and the ADMIN is a separate runner that
	// only applies the edit.
	author, err := asAdmin(t).createTestUser(nil, []models.RoleEnum{
		models.RoleEnumModerate, models.RoleEnumBot,
	})
	require.NoError(t, err)
	authorRunner := createTestRunner(t, author, []models.RoleEnum{
		models.RoleEnumModerate, models.RoleEnumBot,
	})

	bot := true
	edit, err := authorRunner.createTestTagEdit(models.OperationEnumCreate, nil,
		&models.EditInput{Operation: models.OperationEnumCreate, Bot: &bot})
	require.NoError(t, err)
	require.True(t, edit.Bot, "precondition: this edit is a bot edit")

	_, err = asAdmin(t).approveEdit(edit.ID)
	require.NoError(t, err)

	assert.Zero(t, countTrustEvents(t, author.ID, trust.KindEditApproved, edit.ID),
		"a bot edit must earn nobody trust: it is auto-applied and represents no "+
			"human curation, and counting it would let a bot farm level 4 to unlock "+
			"content viewing")
}

// Five applied edits earn exactly five events, not one per path that happens to
// touch the funnel.
func TestRepeatedApprovalsAccumulateOneEach(t *testing.T) {
	author, err := asAdmin(t).createTestUser(nil, nil)
	require.NoError(t, err)
	authorRunner := createTestRunner(t, author, nil)

	const editCount = 5
	for i := range editCount {
		edit, err := authorRunner.createTestTagEdit(models.OperationEnumCreate, nil, nil)
		require.NoError(t, err)
		_, err = asAdmin(t).approveEdit(edit.ID)
		require.NoError(t, err, "edit %d", i)
	}

	assert.Equal(t, editCount, trustTotal(t, author.ID, trust.KindEditApproved),
		"each applied edit must contribute exactly one approval; a duplicated "+
			"call site would show up here as a count above %d", editCount)

	// 5 x 10 = 50 points, which is exactly LevelContributor.
	level, err := testutil.Factory().Trust().Level(t.Context(), author.ID)
	require.NoError(t, err)
	assert.Equal(t, trust.LevelContributor, level,
		"50 points must be exactly LevelContributor, so this test also pins the "+
			"boundary rather than sitting safely inside a level")
}
