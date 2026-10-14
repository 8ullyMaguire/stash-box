package vouch

import (
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/stashapp/stash-box/internal/service/trust"
)

// The pure half of vouching (plan phase 6 slice 6d).
//
// The cap, the standing requirement and the self-vouch refusal are all decisions, so they are
// decided in one place that takes no database: what is left in the service is the lock, the count
// and the insert, and those are exercised against a real database next door.

func ids(t *testing.T) (uuid.UUID, uuid.UUID) {
	t.Helper()
	return uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
}

// ACTIVE IS DERIVED FROM TWO FACTS, and the boundary is the half that is easy to get wrong: a vouch
// that expires at exactly this instant has expired. `expires_at > now` and not `>=`, so a member
// reading their own list at the moment one lapses does not see it for one more nanosecond.
func TestAVouchIsActiveUntilWithdrawnOrExpired(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	withdrawn := now.Add(-time.Hour)

	tests := []struct {
		name string
		v    Vouch
		want bool
	}{
		{"a fresh vouch counts", Vouch{ExpiresAt: now.Add(time.Hour)}, true},
		{"a withdrawn vouch does not", Vouch{ExpiresAt: now.Add(time.Hour), WithdrawnAt: &withdrawn}, false},
		{"an expired vouch does not", Vouch{ExpiresAt: now.Add(-time.Nanosecond)}, false},
		{"a vouch expiring right now does not", Vouch{ExpiresAt: now}, false},
		{"a withdrawn vouch cannot be rescued by a later expiry", Vouch{ExpiresAt: now.Add(24 * time.Hour), WithdrawnAt: &withdrawn}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.v.Active(now))
		})
	}
}

// THE FOUR REFUSALS, of which three are decisions. ErrAlreadyVouched is deliberately absent: it is
// discovered by the insert against the partial unique index, not by looking at anything, and a
// "decision" that claimed to foresee it would be a second implementation of the same rule.
func TestTheVouchDecision(t *testing.T) {
	voucher, target := ids(t)

	t.Run("nobody vouches for themselves", func(t *testing.T) {
		assert.ErrorIs(t, decide(voucher, voucher, trust.LevelArchivist, 0), ErrSelfVouch)
	})

	t.Run("a fresh account cannot vouch", func(t *testing.T) {
		assert.ErrorIs(t, decide(voucher, target, trust.LevelPublic, 0), ErrVoucherStanding)
		assert.ErrorIs(t, decide(voucher, target, trust.LevelRegistered, 0), ErrVoucherStanding,
			"one level below the requirement is still below it")
	})

	t.Run("standing alone is not a licence past the cap", func(t *testing.T) {
		assert.ErrorIs(t, decide(voucher, target, trust.LevelSteward, MaxActiveVouches), ErrVouchLimit,
			"the most trusted member in the instance is refused the fourth vouch too")
	})

	t.Run("a member below the cap can be vouched for", func(t *testing.T) {
		assert.NoError(t, decide(voucher, target, VoucherMinLevel, MaxActiveVouches-1))
	})

	t.Run("an over-full target is refused rather than yielding a negative", func(t *testing.T) {
		// Reachable if the cap is ever LOWERED: rows written under the old cap do not disappear.
		assert.ErrorIs(t, decide(voucher, target, trust.LevelContributor, MaxActiveVouches+2), ErrVouchLimit)
	})
}
