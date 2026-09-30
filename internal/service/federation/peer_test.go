package federation

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// The staleness rule (SPEC F3) decides which peers get asked, so its boundary is
// pinned here. A boundary test that has never been inverted is a comment; the
// plan requires inverting the comparison to confirm this test can fail.

func TestFresh(t *testing.T) {
	// The clock is INJECTED. Fresh() as first written called time.Since
	// internally, which made the boundary case untestable rather than merely
	// untested: capturing `now := time.Now()` and asking about
	// `now - PeerStaleness` means time.Since re-reads the clock microseconds
	// later, so the elapsed time is always a hair OVER the window and the
	// inclusive boundary can never be observed. The first run of this test
	// failed for exactly that reason and the failure looked like a code bug.
	now := time.Now()

	cases := []struct {
		name string
		seen *time.Time
		want bool
		why  string
	}{
		{"never contacted", nil, false,
			"an unknown peer is not asked; it is usually a misconfiguration"},
		{"seen just now", ptr(now.Add(-time.Minute)), true, "recent contact"},
		{"seen one hour ago", ptr(now.Add(-time.Hour)), true, "well inside the window"},
		{"seen exactly PeerStaleness ago", ptr(now.Add(-PeerStaleness)), true,
			"the boundary is INCLUSIVE: exactly one window old is still fresh"},
		{"seen one nanosecond past the boundary", ptr(now.Add(-PeerStaleness - time.Nanosecond)), false,
			"one nanosecond past the window is stale"},
		{"seen a day past the boundary", ptr(now.Add(-PeerStaleness - 24*time.Hour)), false,
			"long past the window"},
		{"seen far in the FUTURE", ptr(now.Add(24 * time.Hour)), true,
			"a clock skew must not make a peer look stale"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// The assertion runs against the SAME `now` the fixture was built
			// from, so the boundary case means what it says.
			if got := FreshAt(c.seen, now); got != c.want {
				t.Errorf("FreshAt(%s) = %v, want %v — %s",
					describe(c.seen, now), got, c.want, c.why)
			}
		})
	}
}

func describe(seen *time.Time, now time.Time) string {
	if seen == nil {
		return "nil"
	}
	return time.Since(*seen).Round(time.Second).String()
}

func ptr(t time.Time) *time.Time { return &t }

// A NULL last_seen_at must become a nil pointer, not the zero time.
//
// The failure this guards is quiet: time.Time{} is far in the past, so Fresh
// would return false for the right ANSWER by the wrong route, and a peer
// contacted exactly at the epoch would be indistinguishable from a peer never
// contacted at all.
func TestPGTimeDistinguishesNullFromZero(t *testing.T) {
	null := pgTime(pgtype.Timestamptz{Valid: false})
	if null != nil {
		t.Errorf("a NULL column must convert to nil, got %v", *null)
	}

	// The zero TIME is a valid timestamp, and must survive as a non-nil pointer
	// even though Fresh will call it stale. Conflating the two is the bug.
	epoch := pgTime(pgtype.Timestamptz{Time: time.Unix(0, 0), Valid: true})
	if epoch == nil {
		t.Error("a valid epoch timestamp must convert to a non-nil pointer, not nil")
	}
	if Fresh(epoch) {
		t.Error("an epoch timestamp is stale, so Fresh must say so")
	}
}

// Askable is three separate checks, and they fail for different reasons an
// operator needs to tell apart: a decision (disabled), a condition (stale), and
// a data error (weight out of range).
func TestAskableRowChecks(t *testing.T) {
	now := time.Now()

	fresh := func() pgtype.Timestamptz {
		return pgtype.Timestamptz{Time: now.Add(-time.Hour), Valid: true}
	}
	neverSeen := pgtype.Timestamptz{Valid: false}

	cases := []struct {
		name string
		peer peerRow
		want bool
		why  string
	}{
		{"healthy peer", peerRow{enabled: true, weight: 0.5, seen: fresh()}, true, "the ordinary case"},
		{"disabled", peerRow{enabled: false, weight: 0.5, seen: fresh()}, false,
			"disabled is a decision and outranks freshness"},
		{"disabled AND stale", peerRow{enabled: false, weight: 0.5, seen: neverSeen}, false,
			"both reasons point the same way"},
		{"never contacted", peerRow{enabled: true, weight: 0.5, seen: neverSeen}, false,
			"no evidence the peer is alive"},
		{"weight zero", peerRow{enabled: true, weight: 0, seen: fresh()}, false,
			"a zero weight is a data error, not a disablement"},
		{"weight above one", peerRow{enabled: true, weight: 1.5, seen: fresh()}, false,
			"a peer must never outrank a local vote"},
		{"weight exactly one", peerRow{enabled: true, weight: 1, seen: fresh()}, true,
			"one is the ceiling and is allowed"},
		{"negative weight", peerRow{enabled: true, weight: -0.5, seen: fresh()}, false,
			"a negative weight is nonsense and must not be asked"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.peer.askable(); got != c.want {
				t.Errorf("Askable = %v, want %v — %s", got, c.want, c.why)
			}
		})
	}
}

// peerRow is a minimal stand-in for the generated row, so the table above reads
// as the RULE it is testing rather than as a wall of struct literals.
type peerRow struct {
	enabled bool
	weight  float64
	seen    pgtype.Timestamptz
}

func (p peerRow) askable() bool {
	return askableFields(p.enabled, p.weight, p.seen)
}
