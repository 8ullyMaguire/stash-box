//go:build integration

package vouch_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/trust"
	"github.com/stashapp/stash-box/internal/service/vouch"
)

// The vouch WRITE path against a real database (SPEC §5.4 as the roadmap has it, plan phase 6 slice 6d).
//
// The pure tests next door prove the rules are right. These prove the rules are ENFORCED, which is a
// different claim: a cap that is decided but never consulted passes its own unit tests forever. So every
// assertion here is about what the database holds after real calls, and the fixture proves the standing
// it claims to have set up rather than assuming it.
//
// TestMain brings up the test database.
func TestMain(m *testing.M) {
	testutil.TestWithDatabase(m, nil)
}

// testTime is a fixed clock. Nothing here is about elapsed time, but a vouch records when it was given,
// so the instant is supplied rather than measured.
var testTime = time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)

func q() *queries.Queries {
	return queries.New(testutil.DB())
}

func createWithTxn() queries.WithTxnFunc {
	pool := testutil.DB()
	return func(fn func(*queries.Queries) error) error {
		return pgx.BeginFunc(context.Background(), pool, func(tx pgx.Tx) error {
			return fn(queries.New(tx))
		})
	}
}

func newTrust() *trust.Trust {
	return trust.NewTrust(q(), createWithTxn()).WithClock(func() time.Time { return testTime })
}

func newVouches() *vouch.Service {
	return vouch.NewService(q(), createWithTxn(), newTrust())
}

func createUser(t *testing.T) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	_, err := q().CreateUser(t.Context(), queries.CreateUserParams{
		ID:           id,
		Name:         "Vouch User " + id.String(),
		PasswordHash: "not-a-real-hash-this-test-never-logs-in",
		Email:        id.String() + "@vouch.invalid",
	})
	require.NoError(t, err, "creating a member")
	return id
}

// contributor returns a member whose standing is enough to vouch, and PROVES IT.
//
// Five approved edits is fifty points, which is exactly LevelContributor's threshold -- so the fixture
// asserts the level it just arranged. A fixture that assumed its own effect is how a correct refusal
// gets read as a broken feature.
func contributor(t *testing.T) uuid.UUID {
	t.Helper()
	userID := createUser(t)
	svc := newTrust()
	for range 5 {
		entityID := uuid.Must(uuid.NewV7())
		_, err := svc.RecordEvent(t.Context(), trust.Event{
			UserID:     userID,
			Kind:       trust.KindEditApproved,
			Delta:      1,
			EntityType: "performer",
			EntityID:   &entityID,
		})
		require.NoError(t, err, "recording an approved edit")
	}
	level, err := svc.Level(t.Context(), userID)
	require.NoError(t, err)
	require.Equal(t, trust.LevelContributor, level, "fixture: five approved edits must reach the vouching threshold")
	return userID
}

// THE CAP IS THE FEATURE: three are accepted and the fourth is refused, and the refusal is about the
// TARGET rather than about the fourth voucher.
func TestThreeVouchesAreAcceptedAndTheFourthIsRefused(t *testing.T) {
	target := createUser(t)
	svc := newVouches()

	for i := range vouch.MaxActiveVouches {
		got, err := svc.Vouch(t.Context(), contributor(t), target, testTime)
		require.NoError(t, err, "vouch %d of %d", i+1, vouch.MaxActiveVouches)
		assert.True(t, got.Active(testTime), "a fresh vouch counts")
		assert.Equal(t, target, got.TargetID)
		// Equal on the INSTANT rather than assert.Equal on the value: the driver returns timestamptz in
		// time.Local, so comparing the structs compares wall clocks and zones rather than the moment.
		assert.True(t, got.ExpiresAt.Equal(testTime.AddDate(0, 0, vouch.VouchValidityDays)),
			"expires half a year later, which is what a client shows the member: got %s", got.ExpiresAt)
	}

	fourth := contributor(t)
	_, err := svc.Vouch(t.Context(), fourth, target, testTime)
	assert.ErrorIs(t, err, vouch.ErrVouchLimit, "the fourth is refused")

	// AND THE REFUSAL DID NOT HALF-HAPPEN. The count is the same three, so a refusal cannot leave a
	// partial write behind for the next reader to find.
	held, err := svc.ActiveForTarget(t.Context(), target, testTime)
	require.NoError(t, err)
	assert.Len(t, held, vouch.MaxActiveVouches)
	slots, err := svc.SlotsAvailable(t.Context(), target, testTime)
	require.NoError(t, err)
	assert.Equal(t, 0, slots)
}

// A member cannot vouch for themselves, which is neither a cap question nor a standing one.
func TestAMemberCannotVouchForThemselves(t *testing.T) {
	me := contributor(t)
	_, err := newVouches().Vouch(t.Context(), me, me, testTime)
	assert.ErrorIs(t, err, vouch.ErrSelfVouch)
}

// The standing requirement is on the VOUCHER, and a target with three vouchers is a different question
// from a target vouched for by somebody with no standing.
func TestAFreshAccountCannotVouch(t *testing.T) {
	fresh := createUser(t) // no contributions: LevelPublic
	target := createUser(t)

	_, err := newVouches().Vouch(t.Context(), fresh, target, testTime)
	assert.ErrorIs(t, err, vouch.ErrVoucherStanding)

	held, err := newVouches().ActiveForTarget(t.Context(), target, testTime)
	require.NoError(t, err)
	assert.Empty(t, held, "and nothing was recorded")
}

// The same member cannot vouch for the same target twice, which the partial unique index enforces and
// the service reports as its own error rather than as a database failure.
func TestTheSameMemberCannotVouchTwice(t *testing.T) {
	voucher := contributor(t)
	target := createUser(t)
	svc := newVouches()

	_, err := svc.Vouch(t.Context(), voucher, target, testTime)
	require.NoError(t, err)
	_, err = svc.Vouch(t.Context(), voucher, target, testTime)
	assert.ErrorIs(t, err, vouch.ErrAlreadyVouched)

	held, err := svc.ActiveForTarget(t.Context(), target, testTime)
	require.NoError(t, err)
	assert.Len(t, held, 1, "the second attempt did not add a row")
}

// WITHDRAWING FREES THE SLOT, which is the whole reason the cap counts ACTIVE vouches rather than rows.
// A member at the cap whose voucher changes their mind is not stuck at the cap.
func TestWithdrawingAVouchFreesTheSlot(t *testing.T) {
	target := createUser(t)
	svc := newVouches()
	vouchers := make([]uuid.UUID, 0, vouch.MaxActiveVouches)
	for range vouch.MaxActiveVouches {
		v := contributor(t)
		vouchers = append(vouchers, v)
		_, err := svc.Vouch(t.Context(), v, target, testTime)
		require.NoError(t, err)
	}

	require.NoError(t, svc.Withdraw(t.Context(), vouchers[0], target, testTime))

	held, err := svc.ActiveForTarget(t.Context(), target, testTime)
	require.NoError(t, err)
	assert.Len(t, held, vouch.MaxActiveVouches-1)

	// Withdrawing something that is not there is an error, not a silent success: the caller asked for a
	// state change that did not happen. (Checked HERE, while the member really has nothing to withdraw --
	// asserted after the re-vouch below it would be asserting about a vouch that exists.)
	assert.ErrorIs(t, svc.Withdraw(t.Context(), vouchers[0], target, testTime), vouch.ErrNoVouch)

	// WITHDRAWING IS NOT A LIFE SENTENCE: the partial unique index is partial for this reason, and the
	// member who withdrew can give the vouch again. (The first version of this test filled the freed
	// slot with a NEW member first and then expected the original to also get back in -- which is four,
	// which is correctly refused. The cap was right and the sequence was wrong.)
	_, err = svc.Vouch(t.Context(), vouchers[0], target, testTime)
	require.NoError(t, err, "the member who withdrew can vouch again")

	// And a slot freed on a DIFFERENT target is usable by somebody new -- withdrawing one vouch is not
	// a trade against the cap.
	require.NoError(t, svc.Withdraw(t.Context(), vouchers[1], target, testTime))
	_, err = svc.Vouch(t.Context(), contributor(t), target, testTime)
	require.NoError(t, err, "a freed slot is usable by somebody new")
}

// A MEMBER CANNOT WITHDRAW SOMEBODY ELSE'S VOUCH.
//
// The query takes the voucher as well as the target for this reason, and the test exists because writing
// the probe for it is what showed there was nothing asserting it: "withdraw" scoped to a vouch id would
// let any member end anybody's vouches, which is the one way this feature could be turned against the
// member it is meant to help.
func TestAMemberCannotWithdrawSomebodyElsesVouch(t *testing.T) {
	target := createUser(t)
	voucher := contributor(t)
	svc := newVouches()
	_, err := svc.Vouch(t.Context(), voucher, target, testTime)
	require.NoError(t, err)

	// A member with an active vouch of their OWN for a different target, so the refusal below cannot be
	// explained by them having no vouches at all.
	other := createUser(t)
	_, err = svc.Vouch(t.Context(), voucher, other, testTime)
	require.NoError(t, err)

	prowler := contributor(t)
	assert.ErrorIs(t, svc.Withdraw(t.Context(), prowler, target, testTime), vouch.ErrNoVouch)

	held, err := svc.ActiveForTarget(t.Context(), target, testTime)
	require.NoError(t, err)
	assert.Len(t, held, 1, "the vouch is untouched")

	// And the member who withdrew their own vouch for ONE target did not touch their other one.
	require.NoError(t, svc.Withdraw(t.Context(), voucher, target, testTime))
	stillHeld, err := svc.ActiveForTarget(t.Context(), other, testTime)
	require.NoError(t, err)
	assert.Len(t, stillHeld, 1, "the voucher's other vouch survives")
}

// AN EXPIRED VOUCH DOES NOT HOLD A SLOT, and nothing had to run for that to be true.
//
// This is the claim SPEC §7.17's "attestation weight decays and requires re-signing" makes in the local
// case, so it is checked by moving the clock rather than by sweeping anything: the cap is a property of
// the read, and a member whose three vouchers all lapsed can be vouched for again by somebody who means
// it now.
func TestAnExpiredVouchDoesNotHoldASlot(t *testing.T) {
	target := createUser(t)
	svc := newVouches()
	for range vouch.MaxActiveVouches {
		_, err := svc.Vouch(t.Context(), contributor(t), target, testTime)
		require.NoError(t, err)
	}

	after := testTime.AddDate(0, 0, vouch.VouchValidityDays+1)
	_, err := svc.Vouch(t.Context(), contributor(t), target, after)
	assert.NoError(t, err, "every vouch lapsed, so the slots are free")

	held, err := svc.ActiveForTarget(t.Context(), target, after)
	require.NoError(t, err)
	assert.Len(t, held, 1, "and only the new one counts")

	// The lapsed ones are still in the record, which is what an operator looks at.
	history, err := svc.HistoryForTarget(t.Context(), target)
	require.NoError(t, err)
	assert.Len(t, history, vouch.MaxActiveVouches+1, "the lapsed vouches are history, not noise")
}

// THE DATABASE REFUSES A SELF-VOUCH EVEN WHEN NOTHING ELSE DOES. The service checks it, and a check is
// only a rule if the thing underneath also has it: a second write path, a backfill or a manual insert
// would otherwise be able to record a member vouching for themselves.
func TestTheDatabaseRefusesASelfVouchDirectly(t *testing.T) {
	me := contributor(t)
	_, err := testutil.DB().Exec(t.Context(),
		`INSERT INTO member_vouches (id, voucher_id, target_id, expires_at) VALUES ($1, $2, $2, $3)`,
		uuid.Must(uuid.NewV7()), me, testTime.AddDate(0, 0, 1))
	require.Error(t, err, "the CHECK constraint must refuse it")
	assert.Contains(t, err.Error(), "member_vouches_not_self")
}
