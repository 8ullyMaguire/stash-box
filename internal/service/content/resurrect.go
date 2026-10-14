package content

import (
	"context"
	"fmt"
	"time"
)

// Resurrection is one source this instance had recorded as dead and has just found alive.
//
// LastAliveAt is when it was last known good BEFORE this check. It is the field that says how long the
// gap was, and it cannot be recovered afterwards: a successful check sets `last_ok` to now, which is
// correct -- it is alive now -- and would erase the only record of the silence.
type Resurrection struct {
	URL         string
	LastAliveAt *time.Time
}

// ResurrectionMultiplier is how many ordinary check intervals a dead source waits before it is asked
// again. Thirty quiet-source intervals is a month.
const ResurrectionMultiplier = 30

// ResurrectionInterval is how long a dead source waits between attempts.
//
// RELIABILITY IS DELIBERATELY NOT APPLIED, and that is the whole reason this is not just
// CheckInterval(failures, ...) * 30: a dead source's failure count is saturated by definition (death
// requires seven days of them), so feeding it back in would push the next attempt past the cap --
// sixteen days -- times thirty, i.e. more than a year, and a source nobody asks is a source that
// never comes back. Popularity still applies: a dead link on a URL many scenes claim is worth asking
// about sooner.
func ResurrectionInterval(claimants int) time.Duration {
	return CheckInterval(0, claimants) * ResurrectionMultiplier
}

// SweepResurrections asks the dead sources whose cadence says it is time.
//
// A success here is an ordinary resurrection: NextStatus moves the row to `live` and the counter
// resets with it. `last_ok` is NOT touched on the way in -- it is the record of when this source was
// last alive, which is the evidence this whole queue works from, so a row that comes back after a
// month still says how long it was gone.
//
// It returns what it resurrected, because a resurrection is news: something this instance had recorded
// as dead is serving again, and "which one, and how long was it gone" is the whole content of that.
func (s *Service) SweepResurrections(ctx context.Context, now time.Time, limit int, checker Checker) ([]Resurrection, error) {
	candidates, err := s.queries.FindContentSourcesDueForResurrection(ctx, int32(limit))
	if err != nil {
		return nil, fmt.Errorf("reading the sources due for a resurrection attempt: %w", err)
	}

	var resurrected []Resurrection
	for _, candidate := range candidates {
		if candidate.LastChecked.Valid && now.Sub(candidate.LastChecked.Time) < ResurrectionInterval(int(candidate.ClaimantCount)) {
			// Not due yet, and nothing further down the list is either.
			break
		}

		// Read the evidence BEFORE checking: the check overwrites `last_ok` on success -- correctly,
		// it is alive now -- and "when was it last alive before this" is the one thing the
		// resurrection itself destroys.
		wasAliveAt := timePtr(candidate.LastOk)

		row, err := s.CheckSource(ctx, candidate.ID, now, checker)
		if err != nil {
			return resurrected, err
		}
		if row.Status == StatusLive {
			resurrected = append(resurrected, Resurrection{URL: row.URL, LastAliveAt: wasAliveAt})
		}
	}

	return resurrected, nil
}
