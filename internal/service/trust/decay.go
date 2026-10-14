package trust

import "time"

// Decay with grace and recovery (SPEC §5.3, plan phase 6 slice 6b-1).
//
// THE RULE, IN THREE PARTS, because each answers a different fear:
//
//	GRACE    an event that would drop a member below their level does not drop them yet. Trust
//	         earned over months should not vanish over one bad afternoon, and a member being
//	         brigaded must not be demotable on demand.
//	DEMOTION once the grace deadline passes with the shortfall still standing, the level falls to
//	         what the totals actually justify. A grace period that never expires is a level that
//	         means nothing.
//	RECOVERY after a demotion the level is HELD DOWN for a recovery window, even if the totals
//	         climb back inside it. Without this half, a demoted member who immediately re-earns the
//	         points is back the same hour, and a level that flips twice a day teaches everyone that
//	         it is noise. "Regains it in 14 days" is the verification sentence, and it is the
//	         RECOVERY window it is describing -- not an automatic restore, because trust is never
//	         given back on a timer. At the end of the window the level simply follows the totals
//	         again; if the points are back, so is the level, and if they are not, they are not.
//
// A PROMOTION IS IMMEDIATE and takes no notice of any of this. Grace exists to protect a member from
// losing standing for a moment, and there is no such moment on the way up.
//
// This is a PURE FUNCTION of (previous state, totals, now): the clock is a parameter, not a call to
// time.Now, so every case below is a test rather than a wait. Persisting the result and running it on
// a schedule is a separate concern (slice 6b-2), which is also why nothing here reads a database.

// GracePeriod is how long a member keeps a level they no longer justify.
//
// Seven days: long enough that a bad week does not strip standing, short enough that a level still
// says something about the last few days rather than about some year the member once had.
const GracePeriod = 7 * 24 * time.Hour

// RecoveryPeriod is how long a demotion stands before the level may follow the totals upward again.
//
// Fourteen days, the period SPEC §5.3 names. It is a floor on the demotion, not a schedule for a
// restore: at the end of it the level follows the totals, wherever they are by then.
const RecoveryPeriod = 14 * 24 * time.Hour

// TrustState is what the level depends on beyond the totals: the timestamps that make a shortfall and
// a demotion into things that elapse.
//
// The zero time is "this is not happening": ShortSince is zero when no demotion is pending, and
// DemotedAt is zero when the member is not inside a recovery window. They are distinct states and not
// two spellings of one, because a member can be inside a recovery window that is nearly over while a
// NEW shortfall has begun -- which is the case that makes running the decision twice in a row do
// nothing rather than start a second clock.
type TrustState struct {
	// Level is the level the member is currently AT, which during a grace period or a recovery
	// window is deliberately not the level their totals justify.
	Level LevelEnum
	// ShortSince is when the current shortfall was first observed, or the zero time.
	ShortSince time.Time
	// DemotedAt is when the member was last dropped, or the zero time.
	DemotedAt time.Time
}

// Decision is what the decision function did, and why -- for the caller that has to write it down.
//
// It is returned as a value rather than inferred by comparing states, because "nothing changed" and
// "the grace clock started and the level happens to be the same" are different events: the second one
// has a deadline, and a caller that has to tell a member when a demotion will land cannot recover it
// from two identical levels.
type Decision struct {
	// Kind is the transition that happened.
	Kind DecisionKind
	// At is the instant the decision was made (the `now` it was given).
	At time.Time
	// EffectiveAt is when the decision takes effect: the grace deadline for a started or ongoing
	// shortfall, the end of the recovery window for a demotion still standing, the decision time
	// itself for anything immediate. Zero when it means nothing (KindUnchanged).
	EffectiveAt time.Time
	// From and To are the level before and after. Equal means the level did not move.
	From, To LevelEnum
}

// DecisionKind names the transitions. The zero value is KindUnchanged on purpose: a caller that
// forgets to handle an outcome treats it as "nothing to do", which is the safe default.
type DecisionKind string

const (
	// KindUnchanged: nothing to record. This includes the ordinary case of a member whose level
	// already matches their totals.
	KindUnchanged DecisionKind = ""
	// KindGraceStarted: a shortfall was observed for the first time. The member keeps their level
	// until EffectiveAt.
	KindGraceStarted DecisionKind = "grace_started"
	// KindGraceStanding: a shortfall observed earlier is still there and the deadline has not
	// passed. Reported rather than folded into KindUnchanged so a caller can show the deadline.
	KindGraceStanding DecisionKind = "grace_standing"
	// KindShortfallCleared: the totals rose back to the member's level before the deadline. Note
	// that nothing had to be undone -- the level never moved.
	KindShortfallCleared DecisionKind = "shortfall_cleared"
	// KindDemoted: the grace deadline passed with the shortfall standing.
	KindDemoted DecisionKind = "demoted"
	// KindHeld: inside a recovery window, the level is being held down against totals that justify
	// more. EffectiveAt is when the window ends.
	KindHeld DecisionKind = "held"
	// KindRecovered: the recovery window ended. The level now follows the totals again -- which is
	// a regain only if the totals went back up; the next call is what decides that.
	KindRecovered DecisionKind = "recovered"
	// KindPromoted: the totals justify more than the member has, and nothing else is going on.
	// Immediate: no grace, no window.
	KindPromoted DecisionKind = "promoted"
)

// Decide applies the decay policy to one member, one instant at a time.
//
// It is idempotent at a single instant: called twice with the same state and the same `now`, the
// second call returns KindUnchanged (or the same standing kind) and the same state. That property is
// what makes it safe to run on a schedule that can overlap itself, and it is why a grace clock is
// started only when ShortSince is zero rather than whenever a shortfall is seen.
func Decide(prev TrustState, totals Totals, now time.Time) (TrustState, Decision) {
	raw := LevelForTotals(totals)

	if !prev.DemotedAt.IsZero() {
		return decideWhileDemoted(prev, raw, now)
	}

	if raw >= prev.Level {
		next := prev
		next.Level = raw
		// A shortfall that was standing has cleared itself: the level never moved, so there is
		// nothing to undo, only a clock to stop.
		if !prev.ShortSince.IsZero() {
			next.ShortSince = time.Time{}
			return next, Decision{Kind: KindShortfallCleared, At: now, From: prev.Level, To: raw}
		}
		if raw > prev.Level {
			return next, Decision{Kind: KindPromoted, At: now, From: prev.Level, To: raw}
		}
		return next, Decision{Kind: KindUnchanged, At: now, From: raw, To: raw}
	}

	// raw < prev.Level: a shortfall, either new or older than the last look.
	if prev.ShortSince.IsZero() {
		next := prev
		next.ShortSince = now
		return next, Decision{
			Kind:        KindGraceStarted,
			At:          now,
			EffectiveAt: now.Add(GracePeriod),
			From:        prev.Level,
			To:          prev.Level,
		}
	}

	deadline := prev.ShortSince.Add(GracePeriod)
	if now.Before(deadline) {
		return prev, Decision{
			Kind:        KindGraceStanding,
			At:          now,
			EffectiveAt: deadline,
			From:        prev.Level,
			To:          prev.Level,
		}
	}

	next := prev
	next.Level = raw
	next.ShortSince = time.Time{}
	next.DemotedAt = now
	return next, Decision{
		Kind:        KindDemoted,
		At:          now,
		EffectiveAt: now.Add(RecoveryPeriod),
		From:        prev.Level,
		To:          raw,
	}
}

// decideWhileDemoted holds the level down until the recovery window closes.
//
// The level is NOT recomputed here, and that is the whole point: a member whose totals come back
// during the window stays at the level they were dropped to, because the alternative is a level that
// changes twice in an hour and means nothing. At the end of the window the level follows the totals
// again -- a regain if the totals recovered, a further fall if they did not.
func decideWhileDemoted(prev TrustState, raw LevelEnum, now time.Time) (TrustState, Decision) {
	window := prev.DemotedAt.Add(RecoveryPeriod)
	if now.Before(window) {
		return prev, Decision{
			Kind:        KindHeld,
			At:          now,
			EffectiveAt: window,
			From:        prev.Level,
			To:          prev.Level,
		}
	}

	next := prev
	next.Level = raw
	next.DemotedAt = time.Time{}
	return next, Decision{Kind: KindRecovered, At: now, From: prev.Level, To: raw}
}
