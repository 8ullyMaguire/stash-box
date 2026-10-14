package content

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/queries"
)

// The status machine, exactly as migration 108 documents it:
//
//	live -> intermittent (3+ consecutive failures) -> dead (7+ days unreachable)
//	dead -> resurrected is NOT a status: a resurrected source is 'live' again with its failure count
//	        reset, because the row's identity is the URL
//	any  -> dmca, which is terminal as far as serving is concerned
const (
	StatusUnknown      = "unknown"
	StatusLive         = "live"
	StatusIntermittent = "intermittent"
	StatusDead         = "dead"
	StatusDMCA         = "dmca"
	StatusArchived     = "archived"

	// FailuresToIntermittent and UnreachableToDead are the thresholds the migration names. They live
	// here, once, with the comment above as the specification: a second copy of a threshold is a
	// second rule, and the two would be wrong in different ways on the day one of them moved.
	FailuresToIntermittent = 3
	UnreachableToDead      = 7 * 24 * time.Hour
)

// checkTimeout is the ingest drain's budget for a third-party request, used here rather than a second
// number: the site being checked is the same kind of site, and two timeouts would drift.
const checkTimeout = 20 * time.Second

// ErrCheckInconclusive means the check observed nothing: the site refused the request rather than
// answering it. A refusal is not evidence of death -- a bot wall answers 403 to us and 200 to a reader
// in a browser -- and it is not evidence of life either. The driver records that it looked and changes
// nothing else, which is the honest third outcome.
var ErrCheckInconclusive = errors.New("the source could not be observed")

// timePtr converts a nullable timestamp from the row into the stdlib type the rule uses, so
// NextStatus stays a pure function over types every Go reader already knows.
func timePtr(ts pgtype.Timestamptz) *time.Time {
	if !ts.Valid {
		return nil
	}
	t := ts.Time
	return &t
}

// terminal reports whether a status may never be moved by a health check.
//
// DMCA and ARCHIVED are decisions, not measurements: whatever a URL answers afterwards, the instance
// has already decided not to serve it. A sweep that flipped a takedown back to `live` on the first 200
// would be the worst bug in this phase, which is why the guard is the FIRST branch of NextStatus.
func terminal(status string) bool {
	return status == StatusDMCA || status == StatusArchived
}

// NextStatus is the transition rule: pure, no I/O, so every boundary is a unit test.
//
// failures is the count BEFORE this check, and ok is this check's outcome.
func NextStatus(current string, failures int, lastOK *time.Time, firstSeen time.Time, now time.Time, ok bool) string {
	if terminal(current) {
		return current
	}
	if ok {
		// Including from `dead`: that IS the resurrection. The caller resets the counter with it.
		return StatusLive
	}

	// DEAD IS STICKY ON FAILURE. A dead source only leaves `dead` by answering, and the alternative --
	// downgrading it to `intermittent` on the next failed retry -- would move the record BACKWARDS
	// (intermittent is less severe) and erase the age evidence that made it dead.
	if current == StatusDead {
		return StatusDead
	}

	// Unreachable since the last time it answered, or since it was first seen when it never has. The
	// fallback matters: a source that has never been ok and has been failing for eight days is dead,
	// and treating a null last_ok as "just now" would keep it intermittent forever.
	since := firstSeen
	if lastOK != nil {
		since = *lastOK
	}
	if !since.IsZero() && now.Sub(since) >= UnreachableToDead {
		return StatusDead
	}
	if failures+1 >= FailuresToIntermittent {
		return StatusIntermittent
	}
	return current
}

// Checker performs one health check. It is an interface so the tests are hermetic: the rule above is
// what this slice is about, and a test that needed the internet would test the internet.
type Checker interface {
	Check(ctx context.Context, url string) error
}

// HTTPChecker is the production checker.
type HTTPChecker struct {
	Client *http.Client
}

// NewHTTPChecker builds the checker the sweep uses.
func NewHTTPChecker() HTTPChecker {
	return HTTPChecker{Client: &http.Client{Timeout: checkTimeout}}
}

// Check reports nil when the URL answered as alive, ErrCheckInconclusive when the site refused to
// answer at all, and another error when the URL itself answered badly (404, 410, a 5xx).
//
// The refusal codes are separated deliberately. 401, 403 and 429 say something about THIS request --
// a bot wall, a paywall, a rate limit -- and nothing about whether the content is gone. Counting them
// as failures would walk a perfectly alive site to `dead` one sweep at a time.
func (c HTTPChecker) Check(ctx context.Context, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return fmt.Errorf("building a health request for %s: %w", url, err)
	}

	resp, err := c.Client.Do(req)
	if err != nil {
		return fmt.Errorf("checking %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		return nil
	}
	if resp.StatusCode == http.StatusUnauthorized ||
		resp.StatusCode == http.StatusForbidden ||
		resp.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("%w: %s answered %d", ErrCheckInconclusive, url, resp.StatusCode)
	}
	return fmt.Errorf("%s answered %d", url, resp.StatusCode)
}

// CheckSource checks one source and writes the outcome.
//
// The write goes through RecordSourceCheck, which migration 108 built as the only statement that may
// move `status` and which takes the failure count as an ARGUMENT rather than incrementing it, so that
// the rule above stays in Go where it can be tested.
//
// The clock is an argument rather than time.Now() so that a test can age a source into `dead` without
// waiting a week.
func (s *Service) CheckSource(ctx context.Context, id uuid.UUID, now time.Time, checker Checker) (models.ContentSource, error) {
	source, err := s.queries.FindContentSourceByID(ctx, id)
	if err != nil {
		return models.ContentSource{}, fmt.Errorf("reading the source to check: %w", err)
	}

	status := source.Status
	failures := int(source.CheckFailures)
	lastOK := timePtr(source.LastOk)
	firstSeen := time.Time{}
	if source.FirstSeen.Valid {
		firstSeen = source.FirstSeen.Time
	}
	var succeededAt *time.Time

	switch err := checker.Check(ctx, source.Url); {
	case err == nil:
		status = NextStatus(source.Status, failures, lastOK, firstSeen, now, true)
		failures = 0
		succeededAt = &now
	case errors.Is(err, ErrCheckInconclusive):
		// Looked, learned nothing: last_checked moves, and nothing else does. The status stays what it
		// was, so a refusal neither kills a live source nor postpones a failing one's age.
	default:
		status = NextStatus(source.Status, failures, lastOK, firstSeen, now, false)
		failures++
	}

	// NULL on failure, so the statement's COALESCE keeps the previous value: "when was this last alive"
	// is a fact about the past that a failure does not change, and it is the only evidence a
	// resurrection queue has to work from.
	var succeeded pgtype.Timestamptz
	if succeededAt != nil {
		succeeded = pgtype.Timestamptz{Time: *succeededAt, Valid: true}
	}

	row, err := s.queries.RecordSourceCheck(ctx, queries.RecordSourceCheckParams{
		ID:            id,
		Status:        status,
		LastOk:        succeeded,
		CheckFailures: failures,
	})
	if err != nil {
		return models.ContentSource{}, fmt.Errorf("recording the health check: %w", err)
	}

	return ContentSourceToModel(row), nil
}
