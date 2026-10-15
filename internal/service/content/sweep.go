package content

import (
	"context"
	"fmt"
	"time"

	"github.com/stashapp/stash-box/internal/queries"
)

// The cadence's numbers, named once, with the rule that uses them in `CheckInterval` below.
const (
	// BaseCheckInterval is how often a quiet, reliable source is looked at.
	BaseCheckInterval = 24 * time.Hour
	// MaxCheckBackoff caps how far reliability may push a source out. Without a cap, nine failures
	// would schedule the next look 256 days away -- and a source nobody looks at cannot come back,
	// which is the failure mode this whole phase is about. Four doublings is sixteen days.
	MaxCheckBackoff = 4
	// MinCheckInterval is how often the most-claimed URL on the instance is looked at. A floor,
	// because "popular" must not mean "checked every tick": the site being asked is somebody else's.
	MinCheckInterval = 3 * time.Hour
	// SweepBatch is how many candidates one tick considers. Bounded because the sweep shares the
	// process with the edit closer and the ingest drain, and because every candidate it acts on is a
	// request to a third-party site.
	SweepBatch = 50
)

// CheckInterval is the cadence: "frequency by popularity x reliability" from the plan.
//
// Reliability LENGTHENS the interval -- consecutive failures mean the site is probably down, and
// asking every hour changes nothing except how often this instance hears no. Popularity SHORTENS
// it -- a URL that many scenes claim is one a reader is more likely to need, and a dead link on it
// is a worse experience than a dead link on a one-scene curiosity.
//
// Both are pure arithmetic on two ints, so the whole policy is a table test.
func CheckInterval(failures, claimants int) time.Duration {
	interval := BaseCheckInterval

	// Doubling, capped. (A loop rather than a shift, because MaxCheckBackoff has to bound the
	// EXPONENT and a shift of a large failures value would overflow.)
	for i := 0; i < failures && i < MaxCheckBackoff; i++ {
		interval *= 2
	}

	// Halving by claim, floored. `claimants-1` halvings because one claimant means "as scheduled".
	for i := 0; i < claimants-1; i++ {
		if interval <= MinCheckInterval {
			break
		}
		interval /= 2
	}

	if interval < MinCheckInterval {
		interval = MinCheckInterval
	}
	return interval
}

// Sweep checks the sources that are due, considering at most `limit` candidates.
//
// It returns how many it checked. An error stops the run and reports what was done before it: a
// failure here is a database failure (a checker's own error is data, not an error), and continuing
// to hammer a database that just refused a write is not a kindness.
//
// `limit` is a parameter rather than the constant so a test can prove the bound is honoured.
func (s *Service) Sweep(ctx context.Context, now time.Time, limit int, checker Checker) (int, error) {
	// Refused before the query rather than after it: a sweep that read the due list and then
	// refused would report a database read as the cost of a posture it was always going to
	// refuse. See policy.go.
	if err := RequireFetching(); err != nil {
		return 0, err
	}

	candidates, err := s.queries.FindContentSourcesDueForCheck(ctx, int32(limit))
	if err != nil {
		return 0, fmt.Errorf("reading the sources due for a check: %w", err)
	}

	checked := 0
	for _, candidate := range candidates {
		if candidate.LastChecked.Valid {
			interval := CheckInterval(int(candidate.CheckFailures), int(candidate.ClaimantCount))
			if now.Sub(candidate.LastChecked.Time) < interval {
				// Not due yet. Every candidate after this one is at least as recently checked, so
				// there is nothing further down the list worth looking at either.
				break
			}
		}

		if _, err := s.CheckSource(ctx, candidate.ID, now, checker); err != nil {
			return checked, err
		}
		checked++
	}

	return checked, nil
}

// The query's row type is asserted here so that a change to the SQL which drops the claimant count
// -- the popularity half of the cadence -- fails to compile rather than silently scheduling every
// source as if it had one claimant.
var _ = func(row queries.FindContentSourcesDueForCheckRow) int { return int(row.ClaimantCount) }
