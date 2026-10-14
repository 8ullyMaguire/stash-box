package completeness

import (
	"context"

	"github.com/gofrs/uuid"
)

// UnverifiablePenalty is how much of a completeness percentage an entity gives up when a claim's
// evidence is gone.
//
// Ten points, and ONCE per entity rather than once per unverifiable total: what is compromised is
// the record's provenance, not each individual number. A studio with four dead-source denominators is
// not four times less complete than a studio with one -- both are "we cannot check this claim".
const UnverifiablePenalty = 10

// Verdict is what a reader gets asked for: how much of what the sources claim this entity holds, and
// whether that answer is still checkable.
type Verdict struct {
	Totals []Total

	Expected int64
	Observed int64
}

// HasClaim reports whether any source has claimed anything about this entity.
func (v Verdict) HasClaim() bool { return v.Expected > 0 }

// Percent is the observed share of the claimed total, rounded, and ZERO when nothing is claimed.
//
// Not 100. The completion scorer answers 100 for an entity type with no scored fields, because there
// the question is "is there anything left to do" and the answer is no. Here the question is "how much
// of the claim do we hold", and an entity nobody has claimed anything about does not hold all of it --
// it has no ratio at all. Reporting 100 would make a dashboard read the archive as more complete the
// LESS is known about it, which is the exact direction a completeness score must never move.
//
// Integer arithmetic, and it FLOORS where the completion scorer ROUNDS -- a deliberate difference,
// not an oversight. That scorer answers "how much of the work is done", where 399 of 400 scoring 100 is
// a rounding artefact of a job essentially finished. This answers "how much of the claim do we hold",
// and there a percentage that reaches 100 while the claim is UNMET is the same lie as 100% for a claim
// nobody has checked, one step smaller. Completeness must never overstate: only observed == expected
// reports 100, and then the cap below is what decides whether it may be said at all.
func (v Verdict) Percent() int {
	if !v.HasClaim() {
		return 0
	}
	return int(v.Observed * 100 / v.Expected)
}

// Unverifiable reports whether ANY total's evidence is gone. Any rather than all: one claim nobody can
// check makes the entity's completeness unverifiable, however many of its other claims still stand.
func (v Verdict) Unverifiable() bool {
	for _, total := range v.Totals {
		if total.Unverifiable() {
			return true
		}
	}
	return false
}

// LostSource names the first total whose evidence is gone, which is what a reader needs to act on. The
// second return is false when nothing is unverifiable.
func (v Verdict) LostSource() (Total, bool) {
	for _, total := range v.Totals {
		if total.Unverifiable() {
			return total, true
		}
	}
	return Total{}, false
}

// Score is the percentage to show: Percent, CEILINGED at 100 - UnverifiablePenalty when a claim's
// evidence is gone.
//
// A CEILING, not a subtraction. Subtracting would let a studio at 25% fall to 15% for a page dying --
// reporting less completeness than the archive verifiably holds, which is a worse lie than the one the
// cap exists to prevent. The cap says only this: an entity whose denominators cannot all be checked is
// not a complete entity, however high the fraction of them we hold.
//
// The case it exists for is the claim FULLY held (100%) whose source is gone: 100 is a defensible
// statement about the archive and an indefensible one about the record, so the answer is 90.
func (v Verdict) Score() int {
	percent := v.Percent()
	if v.Unverifiable() {
		if ceiling := 100 - UnverifiablePenalty; percent > ceiling {
			return ceiling
		}
	}
	return percent
}

// StudioVerdict reads a studio's denominators and reduces them to one verdict.
func (s *Service) StudioVerdict(ctx context.Context, studioID uuid.UUID) (Verdict, error) {
	totals, err := s.StudioTotals(ctx, studioID)
	if err != nil {
		return Verdict{}, err
	}

	verdict := Verdict{Totals: totals}
	for _, total := range totals {
		// Only ASSERTED totals count towards the ratio -- every row here is one, and the sum is
		// over what the sources claim rather than over what the instance holds. An entity with no
		// rows lands at zero claimed, which HasClaim reports as "no ratio" rather than as 0%.
		verdict.Expected += total.Expected
		verdict.Observed += total.Observed
	}
	return verdict, nil
}
