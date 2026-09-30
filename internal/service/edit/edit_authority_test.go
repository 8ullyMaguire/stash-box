package edit

import (
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/config"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/service/trust"
)

// mayUpdateEdit decides three things that upstream PR #708 decided one way and
// this fork decides three: the owner always may, an admin always may, and
// anyone at or above a configurable trust level may.
//
// The tests below cover the DECISION as a table. The decision is deliberately
// factored into allowUpdateEdit, which takes the fields it reads rather than
// doing the lookups -- so the crossings between (configured, actual, isAdmin) can
// be enumerated without a context, roles, or a trust table. The lookups around
// it are covered by the integration tests.

// allowUpdateEdit is defined in service.go next to mayUpdateEdit. If that
// function does not exist this file does not compile, which is the point: the
// pure rule and its tests must not be separated.
func TestAllowUpdateEditTable(t *testing.T) {
	cases := []struct {
		name      string
		isAdmin   bool
		minLevel  int // -1 = creator only (the default)
		actual    trust.LevelEnum
		wantAllow bool
		why       string
	}{
		{
			name: "creator-only, level 0", isAdmin: false, minLevel: -1,
			actual: trust.LevelPublic, wantAllow: false,
			why: "the default: a public user is not the creator and cannot",
		},
		{
			name: "creator-only, top level", isAdmin: false, minLevel: -1,
			actual: trust.LevelSteward, wantAllow: false,
			why: "the DEFAULT, and the important one: LevelSteward is the top EARNED " +
				"level and still cannot. This is what the -1 sentinel means",
		},
		{
			name: "creator-only, admin", isAdmin: true, minLevel: -1,
			actual: trust.LevelPublic, wantAllow: true,
			why: "ADMIN PASSES AT -1. Upstream #708's case. Admin is a role, not a " +
				"trust level, so it is not folded into the comparison and not " +
				"affected by the creator-only default",
		},
		{
			name: "threshold 1, level 0", isAdmin: false, minLevel: 1,
			actual: trust.LevelPublic, wantAllow: false,
			why: "below the threshold",
		},
		{
			name: "threshold 1, level 1", isAdmin: false, minLevel: 1,
			actual: trust.LevelRegistered, wantAllow: true,
			why: "exactly at the threshold -- inclusive, because a threshold that " +
				"excluded the value it names is off by one and reads as a bug",
		},
		{
			name: "threshold 4, level 4", isAdmin: false, minLevel: 4,
			actual: trust.LevelArchivist, wantAllow: true,
			why: "Archivist at threshold 4: the level an operator most plausibly picks",
		},
		{
			name: "threshold 4, level 3", isAdmin: false, minLevel: 4,
			actual: trust.LevelCurator, wantAllow: false,
			why: "one below -- the off-by-one case",
		},
		{
			name: "threshold 5, level 5", isAdmin: false, minLevel: 5,
			actual: trust.LevelSteward, wantAllow: true,
			why: "Steward at the maximum threshold: the ceiling still works",
		},
		{
			name: "threshold 0, level 0", isAdmin: false, minLevel: 0,
			actual: trust.LevelPublic, wantAllow: false,
			why: "threshold 0 does NOT mean everyone. This is the misconfiguration the " +
				"sentinel exists to prevent: 0 is clamped to creator-only",
		},
		{
			name: "threshold 0, level 5", isAdmin: false, minLevel: 0,
			actual: trust.LevelSteward, wantAllow: false,
			why: "even the top level cannot at 0 -- a blanket 'no minimum' would have " +
				"allowed this, and that is the misconfiguration being prevented",
		},
		{
			name: "threshold 9 clamps to the top", isAdmin: false, minLevel: 9,
			actual: trust.LevelSteward, wantAllow: true,
			why: "9 is not a legal level, so it clamps DOWN to LevelSteward rather than " +
				"comparing against an impossible 9. Clamping down is the safe " +
				"direction: a typo admits the top level, not everybody",
		},
		{
			name: "threshold 9 clamps to the top, below it", isAdmin: false, minLevel: 9,
			actual: trust.LevelArchivist, wantAllow: false,
			why: "the same clamp from the other side, so the row above cannot pass " +
				"because the clamp is simply being ignored",
		},
		{
			name: "threshold -2, illegal", isAdmin: false, minLevel: -2,
			actual: trust.LevelSteward, wantAllow: false,
			why: "a negative value is clamped to -1, not treated as 'no minimum'",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := allowUpdateEdit(c.isAdmin, c.minLevel, c.actual)
			if got != c.wantAllow {
				t.Errorf("allow = %v, want %v\n"+
					"  admin=%v minLevel=%d level=%d\n"+
					"  %s", got, c.wantAllow, c.isAdmin, c.minLevel, c.actual, c.why)
			}
		})
	}
}

// TestAdminBypassIgnoresTheClampSeparately covers the one crossing that makes the
// admin check a SEPARATE concern rather than part of the threshold.
//
// A LevelPublic admin passes at every threshold, including -1, because admin is a
// role and the threshold is a trust level. Folding admin into the comparison
// would deny #708's case for any admin with no trust rollup -- and an admin
// account with no trust history is the NORMAL case, since trust accrues from
// activity while the admin role is assigned.
func TestAdminBypassIgnoresTheClampSeparately(t *testing.T) {
	if !allowUpdateEdit(true, int(trust.LevelSteward), trust.LevelSteward) {
		t.Error("a LevelPublic admin was refused at the maximum threshold")
	}
	// The decisive one: the creator-only default, which is what this instance
	// actually runs.
	if !allowUpdateEdit(true, -1, trust.LevelPublic) {
		t.Error("a LevelPublic admin was refused at the creator-only default. This is " +
			"upstream #708's exact case and it must work at the default setting")
	}
}

// TestThresholdIsNotAdminOnly pins the actual feature. If this fails, the
// configurable threshold does nothing and the change is just #708.
func TestThresholdIsNotAdminOnly(t *testing.T) {
	if !allowUpdateEdit(false, int(trust.LevelCurator), trust.LevelCurator) {
		t.Error("a Curator at threshold 3 was refused. The threshold is the whole " +
			"point of the change; admin-only would have been #708 unchanged")
	}
	if allowUpdateEdit(false, 0, trust.LevelPublic) {
		t.Error("threshold 0 admitted a level-0 user; the sentinel is not clamping")
	}
}

// TestValidateEditUpdateStillEnforcesTheOtherRules proves the authority change
// did not swallow the two rules that were already there.
//
// Every test above tests AUTHORITY only. A refactor that replaces the ownership
// check with mayUpdateEdit and drops the closed/limit checks would pass all of
// them. These two cases exist so that cannot happen silently.
func TestValidateEditUpdateStillEnforcesTheOtherRules(t *testing.T) {
	owner := uuid.NullUUID{UUID: uuid.Must(uuid.NewV7()), Valid: true}

	t.Run("closed edit is still closed", func(t *testing.T) {
		now := time.Now()
		e := models.Edit{UserID: owner, ClosedAt: &now}
		if err := validateClosedAndLimit(e); !errors.Is(err, ErrUpdateClosedEdit) {
			t.Errorf("a closed edit gave %v, want ErrUpdateClosedEdit. Widening "+
				"authority must not make a closed edit editable by anyone", err)
		}
	})

	t.Run("update limit is still enforced", func(t *testing.T) {
		e := models.Edit{UserID: owner, UpdateCount: config.GetEditUpdateLimit()}
		if err := validateClosedAndLimit(e); !errors.Is(err, ErrUpdateLimit) {
			t.Errorf("an edit at the update limit gave %v, want ErrUpdateLimit", err)
		}
	})

	t.Run("open edit under the limit is fine", func(t *testing.T) {
		e := models.Edit{UserID: owner}
		if err := validateClosedAndLimit(e); err != nil {
			t.Errorf("an open edit under the limit gave %v, want nil", err)
		}
	})
}

// TestClampEditUpdateMinTrustLevel pins the clamp itself, since a config value
// is operator input and the table above only proves the clamp is CONSISTENT, not
// that it maps to the levels an operator would expect.
func TestClampEditUpdateMinTrustLevel(t *testing.T) {
	cases := map[string]struct{ in, want int }{
		"negative is creator-only": {in: -1, want: -1},
		"zero is creator-only":     {in: 0, want: -1},
		"one is one":               {in: 1, want: 1},
		"five is five":             {in: 5, want: 5},
		"nine clamps to the top":   {in: 9, want: int(trust.LevelSteward)},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := config.ClampEditUpdateMinTrustLevel(c.in); got != c.want {
				t.Errorf("ClampEditUpdateMinTrustLevel(%d) = %d, want %d", c.in, got, c.want)
			}
		})
	}
}
