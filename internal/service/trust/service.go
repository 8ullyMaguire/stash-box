// Package trust implements the trust-level model from SPEC §6.
//
// Trust is a reputation score earned through contribution, kept strictly
// separate from models.RoleEnum. A role is a permission the auth middleware
// checks; trust is what a user has actually done. They are separate because
// conflating them is unrecoverable once data exists -- "this user may not
// moderate" and "this user has not earned trust yet" are different statements,
// and one table cannot hold both without losing the distinction.
//
// The design in one paragraph: trust_events is an append-only log and the source
// of truth; user_trust is a denormalised rollup of it so that reading a level
// does not replay history. Recording an event writes both, and the rollup can
// always be rebuilt by replaying the log.
package trust

import (
	"context"
	"errors"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/stashapp/stash-box/internal/queries"
)

// ErrUnknownKind is returned when an event names a kind the rollup cannot count.
//
// The event is still recorded in trust_events before this is returned, because
// the log is the audit trail and a kind this version does not understand may
// well be understood by a later one. Refusing to record it would lose history
// that a threshold rebuild would then never see.
var ErrUnknownKind = errors.New("unknown trust event kind")

// ReferralFunc pays whoever invited a member, once that member reaches
// Registered-or-above (plan phase 11 slice 11b).
//
// A FUNCTION rather than a field holding the referral service, because the
// dependency runs one way only: the invite package needs trust.LevelEnum for the
// invite allowance, so a trust that imported invite would be a cycle. The factory
// closes the loop with a closure that resolves the referral lazily; and a nil
// function means "this instance pays no referrals", which is what every existing
// test constructs and what a deployment with the feature off gets.
type ReferralFunc func(ctx context.Context, inviteeID uuid.UUID, level LevelEnum) error

// Trust manages trust levels and the events that produce them.
type Trust struct {
	queries *queries.Queries
	withTxn queries.WithTxnFunc
	// now is injectable so tests can hold elapsed time still. The default is
	// time.Now.
	now func() time.Time
	// referral is optional: nil means no referral payment is made at all.
	referral ReferralFunc
}

// NewTrust creates a new trust service.
func NewTrust(queries *queries.Queries, withTxn queries.WithTxnFunc) *Trust {
	return &Trust{queries: queries, withTxn: withTxn, now: time.Now}
}

// WithClock overrides the clock, for tests.
//
// There is deliberately no config path for this. The decay policy's whole
// behaviour turns on elapsed time -- a grace period, a recovery window -- so a
// caller who could set the clock could hand themselves any level they liked,
// and a configurable clock in a reputation system is a configurable reputation
// system.
func (s *Trust) WithClock(now func() time.Time) *Trust {
	s.now = now
	return s
}

// WithTxn executes a function within a transaction.
func (s *Trust) WithTxn(fn func(*queries.Queries) error) error {
	return s.withTxn(fn)
}

// WithReferral attaches the referral payment, which is asked after every event
// this service records through RecordEvent.
func (s *Trust) WithReferral(fn ReferralFunc) *Trust {
	s.referral = fn
	return s
}

// ContentDenylist returns the per-entity content denylist as a rule.
//
// This is the ONE access rule this instance enforces, and it is loadable from
// real data -- which is what separates it from the region, device-class and MFA
// rules in accessrules.go, which are declarations of intent rather than
// configuration read from anywhere.
//
// A nil set means "nothing is denylisted", which is distinct from an empty one
// for the same reason taste_vectors has no row for a user who has not voted:
// absence of configuration is the permissive case, and the gate's job is to
// remove access, not to add a default.
func (s *Trust) ContentDenylist(ctx context.Context) (PerEntityRule, error) {
	tags, err := s.queries.ListDeniedTagIDs(ctx)
	if err != nil {
		return PerEntityRule{}, err
	}
	studios, err := s.queries.ListDeniedStudioIDs(ctx)
	if err != nil {
		return PerEntityRule{}, err
	}
	performers, err := s.queries.ListDeniedPerformerIDs(ctx)
	if err != nil {
		return PerEntityRule{}, err
	}
	return NewPerEntityRule(idsToSet(tags), idsToSet(studios), idsToSet(performers)), nil
}

func idsToSet(ids []uuid.UUID) map[string]struct{} {
	if len(ids) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		out[id.String()] = struct{}{}
	}
	return out
}

// knownKind reports whether the rollup knows how to count this kind.
// knownKind reports whether the service understands a kind well enough to roll it
// up.
//
// PointsPerKind alone can no longer answer this: KindBountyBonus is deliberately
// absent from that map (it is a points-only kind and has no per-contribution
// weight), so keying off the map would reject it as unknown and RecordEvent would
// record it and then decline to roll it up -- the exact silent-invisible-
// contribution failure the enum is meant to prevent.
func knownKind(k KindEnum) bool {
	// The POINTS-ONLY kinds are known without being counted: they move bonus_points, carry their
	// magnitude in the delta, and have no entry in PointsPerKind -- which is what AllKinds is summed
	// over, so an entry there would either double-credit them or need a zero weight.
	if k == KindBountyBonus || k == KindSeedFastTrack {
		return true
	}
	_, ok := PointsPerKind[k]
	return ok
}

// RecordEvent appends a trust event and updates the rollup.
//
// The two writes happen in one transaction. Writing the event without the
// rollup leaves a user whose level does not reflect their contribution;
// writing the rollup without the event leaves a number that no longer has a
// justification and cannot be rebuilt. Both failure modes are worse than a
// failed request, so they fail together.
//
// A duplicate event -- the same user, kind and entity -- is not an error. The
// dedup index in migration 76 rejects it and the rollup is left alone, so
// retrying a request that already succeeded is safe.
//
// An unrecognised kind IS an error, but only after the event has been recorded,
// so the audit trail keeps a kind this version does not understand.
func (s *Trust) RecordEvent(ctx context.Context, event Event) (*queries.UserTrust, error) {
	return s.record(ctx, event, true)
}

// RecordEventWithoutChain records an event that must NOT itself trigger a referral
// payment (plan phase 11 slice 11b).
//
// This is where the referral chain's ONE-LEVEL DEPTH is enforced, and it is enforced
// here rather than in the invite package because this is the only layer that can see
// it: the payment is itself a trust event, so a payment that went through
// RecordEvent would raise the referrer's level, and the referrer's own referrer would
// be paid for it -- and a chain of accounts would pay every ancestor. The depth limit
// is a property of the recorder, not of the payment.
//
// Everything else is identical: the delta check, the unknown-kind branch, the rollup
// write and the policy. The kind it carries is an ordinary counted kind, so the
// rollup and its rebuild read it like any other.
func (s *Trust) RecordEventWithoutChain(ctx context.Context, event Event) (*queries.UserTrust, error) {
	return s.record(ctx, event, false)
}

func (s *Trust) record(ctx context.Context, event Event, chain bool) (*queries.UserTrust, error) {
	_, rollup, err := s.recordReporting(ctx, event, chain)
	return rollup, err
}

// recordReporting is the recording path WITH the one fact RecordEvent throws away: whether the event was
// new. Both entry points come through here, so there is no second implementation of the rules to drift.
func (s *Trust) recordReporting(ctx context.Context, event Event, chain bool) (bool, *queries.UserTrust, error) {
	if event.Delta == 0 {
		// A zero delta would insert a row that moves no counter, and would still
		// occupy the dedup slot -- so a later real event for the same entity
		// would be silently discarded as a "duplicate".
		return false, nil, errors.New("trust event delta must be non-zero")
	}

	if !knownKind(event.Kind) {
		// Record it anyway. The log is the source of truth and a later version
		// may know this kind; refusing to log it would make the contribution
		// invisible to every future rebuild.
		if _, err := s.queries.RecordTrustEvent(ctx, toRecordParams(event)); err != nil &&
			!errors.Is(err, pgx.ErrNoRows) {
			return false, nil, err
		}
		return false, nil, ErrUnknownKind
	}

	return s.withRecordTx(ctx, event, chain)
}

// RecordEventIfNew is RecordEvent that also reports whether the event was NEW.
//
// ONE FACT, ASKED BY ONE CALLER (plan phase 11 slice 11d). A seed campaign's run reports "paid N,
// skipped M" to the operator who ran it, and the only fact separating those two is whether the event
// already existed. Every other caller does not care -- a duplicate is a silent no-op and the answer
// would be noise -- so RecordEvent does not say, and this is the same path with the answer kept.
//
// It is not a second way to record: the rules (a non-zero delta, an unknown kind, the dedup, the
// policy, the referral chain) all live in recordReporting, which both call.
func (s *Trust) RecordEventIfNew(ctx context.Context, event Event) (bool, *queries.UserTrust, error) {
	return s.recordReporting(ctx, event, true)
}

func (s *Trust) withRecordTx(ctx context.Context, event Event, chain bool) (bool, *queries.UserTrust, error) {
	var inserted bool
	var out *queries.UserTrust
	err := s.withTxn(func(tx *queries.Queries) error {
		// ON CONFLICT DO NOTHING makes a duplicate report no rows, which
		// pgx surfaces as ErrNoRows. That is the "already recorded" signal, not
		// a failure, and it must be distinguished from a real insert below.
		_, err := tx.RecordTrustEvent(ctx, toRecordParams(event))
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			// This exact event already exists. Leave the rollup alone rather
			// than incrementing a second time for one contribution, and leave
			// `inserted` false: the caller that reports "paid" versus "already
			// paid" reads that flag and nothing else.
			existing, gerr := tx.GetUserTrust(ctx, event.UserID)
			switch {
			case gerr == nil:
				out = &existing
			case errors.Is(gerr, pgx.ErrNoRows):
				// The event exists but no rollup row does, which the schema
				// permits. Recompute rather than returning nothing, so the
				// invariant "event implies rollup" is repaired on read.
				rebuilt, rerr := tx.RecomputeUserTrustTotals(ctx, event.UserID)
				if rerr != nil {
					return rerr
				}
				out = &rebuilt
			default:
				return gerr
			}
			return nil
		case err != nil:
			return err
		}

		// EventDelta is the caller's SIGNED delta, and passing it is what lets a
		// single event carry a magnitude. It used to be omitted, because the
		// query hardcoded a literal 1 per kind and the count columns could only
		// ever tally whole contributions. A bounty needs 500 to arrive as 500
		// points and a reversal needs -1 to come back out, so the magnitude has
		// to travel with the event rather than being discarded here.
		rollup, err := tx.ApplyTrustEvent(ctx, queries.ApplyTrustEventParams{
			UserID:     event.UserID,
			EventKind:  string(event.Kind),
			EventDelta: event.Delta,
		})
		if err != nil {
			return err
		}
		// THE ONE BRANCH THAT MAKES THIS EVENT NEW: the insert returned a row, so the dedup index did
		// not reject it. The duplicate branch above leaves this false, and that is the whole answer the
		// campaign's report is built from.
		inserted = true
		out = &rollup
		return nil
	})
	if err != nil {
		return inserted, nil, err
	}

	updated, _, err := s.applyPolicy(ctx, out)
	if err != nil {
		return inserted, nil, err
	}
	if chain && s.referral != nil {
		if err := s.payReferral(ctx, updated); err != nil {
			return inserted, nil, err
		}
	}
	return inserted, updated, nil
}

// payReferral asks the instance's referral payment whether this event completed an
// invite.
//
// IT RUNS AFTER THE LEVEL IS WRITTEN and on the STORED level, which is the number the
// instance shows the member, policy included: a member whose level the decay policy is
// still holding down is not paid yet, and is paid at their next event once the window
// closes. Reading the totals-derived level instead would pay a member whose level the
// policy has taken away, which is a payment for standing they do not have.
//
// ONCE PER EVENT, not once per transition, and that is cheaper than it looks: the
// payment's own dedup key -- the invitee, in the event log -- makes every call after
// the first a no-op, so there is no transition state to keep and nothing to get wrong
// when the level oscillates, which it does every time decay moves it.
//
// The LEVEL CHECK here is an optimisation, not the rule: it saves a database read on
// every event by every member who has not reached Registered, and the rule itself
// (eligibility, a named referrer, and the self-invite refusal) lives in the invite
// package's DecideReferral, where it is table-tested without a database.
//
// An error from the payment is RETURNED rather than swallowed. The member's own event
// is already committed, so a failure means "their contribution stands and the referrer
// was not paid" -- a fact an operator needs to see, and the opposite of the silent
// failure this repository keeps finding.
func (s *Trust) payReferral(ctx context.Context, rollup *queries.UserTrust) error {
	if rollup == nil || LevelEnum(rollup.Level) < LevelRegistered {
		return nil
	}
	return s.referral(ctx, rollup.UserID, LevelEnum(rollup.Level))
}

// toRecordParams converts an Event into the generated query parameters.
//
// The nullable entity columns are pointers because migration 76's dedup index
// is NULLS NOT DISTINCT: an entity-less event must be recorded as SQL NULL, not
// as an empty string, or the dedup key differs and a retry double-counts.
func toRecordParams(event Event) queries.RecordTrustEventParams {
	params := queries.RecordTrustEventParams{
		UserID:     event.UserID,
		Kind:       string(event.Kind),
		EntityType: &event.EntityType,
		Delta:      event.Delta,
	}
	if event.EntityType == "" {
		params.EntityType = nil
	}
	if event.EntityID != nil {
		params.EntityID = uuid.NullUUID{UUID: *event.EntityID, Valid: true}
	}
	return params
}

// recomputeLevel derives the level from the rollup and persists it if it moved.
//
// The level is derived in Go and written back rather than computed in SQL,
// because the curve is a product decision and belongs in one readable table
// (see thresholds in models.go).
// repairLevel writes the level the totals justify, taking no notice of grace or of a recovery window.
//
// This is the REBUILD path's writer, and the distinction from applyPolicy is the point: a rebuild
// repairs a rollup that drifted -- corruption, a failed migration, a restored backup -- and a repair
// that left the level where the corruption put it would not be a repair. Its existing test says so:
// "the corrupted level 5 must be recomputed, not left in place". The decay state is deliberately
// untouched, which is safe in both directions: a stale shortfall against a level that now matches the
// totals reads as CLEARED on the next sweep (the policy compares levels, not history), and a member
// inside a genuine recovery window keeps the window they had.
//
// A THRESHOLD change goes through here too, because RebuildLevels is what an operator runs after one.
// That means a raised threshold demotes a cohort at once rather than after a grace period -- which is
// the operator's own decision, taken with the numbers in front of them, and not the same thing as a
// member's bad afternoon. A config-driven clock would blur that; an explicit "apply thresholds with
// grace" path can exist later if anyone wants one.
func (s *Trust) repairLevel(ctx context.Context, rollup *queries.UserTrust) (*queries.UserTrust, error) {
	if rollup == nil {
		return nil, nil
	}
	want := LevelForTotals(totalsOf(rollup))
	if int(rollup.Level) == int(want) {
		return rollup, nil
	}
	updated, err := s.queries.SetUserTrustState(ctx, queries.SetUserTrustStateParams{
		UserID:           rollup.UserID,
		Level:            int(want),
		ShortSince:       nullableTime(timeOf(rollup.ShortSince)),
		DemotedAt:        nullableTime(timeOf(rollup.DemotedAt)),
		DemotedFromLevel: rollup.DemotedFromLevel,
	})
	if err != nil {
		return nil, err
	}
	return &updated, nil
}

// timeOf reads a nullable column as a time, with the zero time standing for NULL.
func timeOf(ts pgtype.Timestamptz) time.Time {
	if !ts.Valid {
		return time.Time{}
	}
	return ts.Time
}

// applyPolicy derives the level from the rollup through the decay policy and persists the result.
//
// THE POLICY IS ON THE WRITE PATH, NOT ONLY IN THE SWEEP, and this is the whole reason the slice is
// more than a scheduled job: a level that still followed the totals immediately would make the grace
// period a decoration. The member's level does not fall here -- the shortfall is RECORDED, and the
// fall happens when a sweep finds the deadline passed.
//
// It is the single place the level is written, for both the event path and RebuildLevels, which is
// what keeps a threshold change from demoting anybody instantly either.
func (s *Trust) applyPolicy(ctx context.Context, rollup *queries.UserTrust) (*queries.UserTrust, Decision, error) {
	if rollup == nil {
		return nil, Decision{}, nil
	}

	next, decision := Decide(stateOf(rollup), totalsOf(rollup), s.now())
	if !decision.Changed() {
		return rollup, decision, nil
	}

	updated, err := s.queries.SetUserTrustState(ctx, queries.SetUserTrustStateParams{
		UserID:           rollup.UserID,
		Level:            int(next.Level),
		ShortSince:       nullableTime(next.ShortSince),
		DemotedAt:        nullableTime(next.DemotedAt),
		DemotedFromLevel: nullableLevel(next.DemotedFrom),
	})
	if err != nil {
		return nil, decision, err
	}
	return &updated, decision, nil
}

// stateOf reads the persisted decay state, with the zero time standing for "not happening" -- which is
// exactly what the NULL columns mean, and why they are NULL: a column that used the zero time as a
// sentinel could not tell "no shortfall on record" from "short since the year 1".
func stateOf(rollup *queries.UserTrust) TrustState {
	state := TrustState{Level: LevelEnum(rollup.Level)}
	if rollup.ShortSince.Valid {
		state.ShortSince = rollup.ShortSince.Time
	}
	if rollup.DemotedAt.Valid {
		state.DemotedAt = rollup.DemotedAt.Time
	}
	if rollup.DemotedFromLevel != nil {
		state.DemotedFrom = LevelEnum(*rollup.DemotedFromLevel)
	}
	return state
}

// totalsOf reads every counted column, BonusPoints included.
//
// The bonus was MISSING here before this slice -- the rollup carried it, ApplyTrustEvent incremented
// it, and Totals.Points() added it, but this function built the Totals by hand and left it out, so a
// quest bounty raised nothing. Reading the whole row is the fix and also the reason not to build a
// Totals field by field: a column added to the rollup later is counted here without an edit, and the
// failure mode of the hand-built version was silence.
func totalsOf(rollup *queries.UserTrust) Totals {
	return Totals{
		ApprovedEdits:        rollup.ApprovedEdits,
		RejectedEdits:        rollup.RejectedEdits,
		IdentificationSolves: rollup.IdentificationSolves,
		QuestsCompleted:      rollup.QuestsCompleted,
		ReplicasHosted:       rollup.ReplicasHosted,
		InvitesActivated:     rollup.InvitesActivated,
		BonusPoints:          rollup.BonusPoints,
	}
}

// nullableTime converts a zero time back to NULL.
func nullableTime(t time.Time) pgtype.Timestamptz {
	if t.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: t, Valid: true}
}

// nullableLevel converts a zero level back to NULL: a level of 0 is "public", which is a real standing,
// so the column cannot use 0 to mean "not demoted".
func nullableLevel(l LevelEnum) *int {
	if l == 0 {
		return nil
	}
	level := int(l)
	return &level
}

// DecaySweep counts what a sweep did, so the caller can log it rather than guess.
type DecaySweep struct {
	Examined  int
	Started   int
	Cleared   int
	Demoted   int
	Held      int
	Recovered int
}

// SweepDecay applies the decay policy to every member with something in flight.
//
// "In flight" is the floor of who this can affect: a member's totals only fall through an event, and
// every event goes through applyPolicy, which records the shortfall the moment it appears. So the
// members this sweep must reach are exactly the members it is asked for -- and a member whose totals
// are steady is not examined at all.
//
// The clock is the sweep's, not the row's: a caller that runs late still applies the deadlines it has
// passed, because Decide compares timestamps rather than counting intervals.
func (s *Trust) SweepDecay(ctx context.Context) (DecaySweep, error) {
	rows, err := s.queries.ListUserTrustDecayPending(ctx)
	if err != nil {
		return DecaySweep{}, err
	}

	sweep := DecaySweep{Examined: len(rows)}
	for _, rollup := range rows {
		row := rollup
		_, decision, err := s.applyPolicy(ctx, &row)
		if err != nil {
			return sweep, err
		}
		switch decision.Kind {
		case KindGraceStarted:
			sweep.Started++
		case KindShortfallCleared:
			sweep.Cleared++
		case KindDemoted:
			sweep.Demoted++
		case KindHeld:
			sweep.Held++
		case KindRecovered:
			sweep.Recovered++
		}
	}
	return sweep, nil
}

// Level returns a user's current trust level.
//
// A user with no rollup row has no recorded contributions and is therefore
// LevelPublic. That is the common case, not an error: it is every new account.
func (s *Trust) Level(ctx context.Context, userID uuid.UUID) (LevelEnum, error) {
	rollup, err := s.queries.GetUserTrust(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return LevelPublic, nil
		}
		return LevelPublic, err
	}
	return LevelEnum(rollup.Level), nil
}

// Rollup returns the raw rollup row, or nil if the user has none.
//
// This is the one accessor that exposes the storage type, and it exists for
// callers that need a field Totals deliberately omits -- currently only
// ContentViewingOptIn, which is a user preference rather than a contribution and
// so is not part of a "totals" value.
//
// Prefer Level, TotalsFor and CanViewContent for anything that can be expressed
// through them; they encode the derivation rules, whereas this hands back
// whatever is stored.
func (s *Trust) Rollup(ctx context.Context, userID uuid.UUID) (*queries.UserTrust, error) {
	rollup, err := s.queries.GetUserTrust(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &rollup, nil
}

// TotalsFor returns a user's rollup totals, or zeroes if they have none.
func (s *Trust) TotalsFor(ctx context.Context, userID uuid.UUID) (Totals, error) {
	rollup, err := s.queries.GetUserTrust(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Totals{}, nil
		}
		return Totals{}, err
	}
	return Totals{
		ApprovedEdits:        rollup.ApprovedEdits,
		RejectedEdits:        rollup.RejectedEdits,
		IdentificationSolves: rollup.IdentificationSolves,
		QuestsCompleted:      rollup.QuestsCompleted,
		ReplicasHosted:       rollup.ReplicasHosted,
		InvitesActivated:     rollup.InvitesActivated,
		// Carried across, and it has to be: a quest's bounty is in this field
		// and in no other, so a Totals that dropped it would report a curator
		// with the right counts and the wrong score, and the two would disagree
		// with nothing reporting an error.
		BonusPoints: rollup.BonusPoints,
	}, nil
}

// CanViewContent reports whether a user may view content.
//
// Eligibility (level >= LevelContentViewing) AND the user's own opt-in are both
// required, per SPEC §6: high-trust users "explicitly opt in".
//
// Eligibility is derived here and never stored. A stored copy would go stale the
// moment a threshold changed, and the failure would be invisible -- a revoked
// or newly-qualified user silently keeping access they no longer qualify for.
func (s *Trust) CanViewContent(ctx context.Context, userID uuid.UUID) (bool, error) {
	rollup, err := s.queries.GetUserTrust(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}

	if LevelEnum(rollup.Level) < LevelContentViewing {
		// Not eligible at all. Checked before the opt-in so an ineligible
		// user's opt-in flag can never be the thing that grants access.
		return false, nil
	}
	return rollup.ContentViewingOptIn, nil
}

// SetContentViewingOptIn records the user's own choice.
//
// The opt-in can be set at any level; it only takes effect once the user is
// eligible. Storing it early is deliberate: a user approaching Archivist can
// express the preference, and the choice is theirs to make rather than something
// the system grants silently.
func (s *Trust) SetContentViewingOptIn(ctx context.Context, userID uuid.UUID, enabled bool) (*queries.UserTrust, error) {
	updated, err := s.queries.SetContentViewingOptIn(ctx, queries.SetContentViewingOptInParams{
		UserID:              userID,
		ContentViewingOptIn: enabled,
	})
	if err == nil {
		return &updated, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	// No rollup row yet. This is the NORMAL case, not an edge case: a user
	// opting in before they are eligible is exactly what SPEC §6 describes, and
	// the UPDATE cannot create the row. Create a zeroed rollup and set the flag
	// on it. The level stays 0 — this records a preference, it grants nothing.
	if _, err := s.queries.RecomputeUserTrustTotals(ctx, userID); err != nil {
		return nil, err
	}
	updated, err = s.queries.SetContentViewingOptIn(ctx, queries.SetContentViewingOptInParams{
		UserID:              userID,
		ContentViewingOptIn: enabled,
	})
	if err != nil {
		return nil, err
	}
	return &updated, nil
}

// MaxContentTier is the highest tier the model defines (migration 120).
//
// Exported because the page paths skip their tier predicate entirely at this
// value: a viewer who has opted into everything has nothing to exclude, and
// paying a NOT EXISTS per row to exclude nothing is a cost with no answer.
const MaxContentTier = 3

// VisibleTier is the highest tier a user has opted into, 0 when they have
// expressed no preference or have no rollup row at all.
//
// Zero for "never asked" is deliberate: the gate compares it against an entity's
// tier, and an unasked user must be treated as having consented to nothing.
// T3CoolingHours is the cooling period for tier 3, in hours.
//
// A named constant and not a setting: the plan's own principle is that a policy knob
// with no operator surface is a guess, and this instance has no operator surface for
// it. It is passed into the ceiling query rather than written into the SQL so the
// number has exactly one home.
const T3CoolingHours = 24

func (s *Trust) VisibleTier(ctx context.Context, userID uuid.UUID) (int, error) {
	tier, err := s.queries.UserVisibleTier(ctx, queries.UserVisibleTierParams{
		UserID:       userID,
		CoolingHours: T3CoolingHours,
	})
	if err != nil {
		return 0, err
	}
	return int(tier), nil
}

// VisibleTiers is the array as stored -- the record of what the user consented
// to, in contrast to VisibleTier, which is the ceiling the gate compares
// against. Exported separately because they answer different questions: "what
// did I agree to" is the array, "what may I see" is the maximum.
func (s *Trust) VisibleTiers(ctx context.Context, userID uuid.UUID) ([]int16, error) {
	rollup, err := s.queries.GetUserTrust(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return rollup.VisibleTiers, nil
}

// SetVisibleTiers records the tiers a user has opted into.
//
// The array is stored AS SENT rather than collapsed to its maximum: the record
// of what was consented to is the thing an audit reads, and the gate's ceiling
// is derived from it. A user with no rollup row yet is the NORMAL case here (the
// opt-in can be expressed before eligibility, SPEC §6), and the UPDATE cannot
// create the row -- so a zeroed rollup is created first, exactly as
// SetContentViewingOptIn does. Storing a preference grants nothing.
func (s *Trust) SetVisibleTiers(ctx context.Context, userID uuid.UUID, tiers []int16) ([]int16, error) {
	updated, err := s.queries.SetVisibleTiers(ctx, queries.SetVisibleTiersParams{
		UserID:       userID,
		VisibleTiers: tiers,
	})
	if err == nil {
		return updated.VisibleTiers, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	if _, err := s.queries.RecomputeUserTrustTotals(ctx, userID); err != nil {
		return nil, err
	}
	updated, err = s.queries.SetVisibleTiers(ctx, queries.SetVisibleTiersParams{
		UserID:       userID,
		VisibleTiers: tiers,
	})
	if err != nil {
		return nil, err
	}
	return updated.VisibleTiers, nil
}

// SetTagContentTier is a moderator's classification of a tag.
//
// Both columns are written together because they are two refusals set by one
// decision: the tier ("this is above your ceiling") and the opt-in ("this needs
// content viewing at all"). Reading them from two statements is how they drift.
func (s *Trust) SetTagContentTier(ctx context.Context, tagID uuid.UUID, tier int16, requiresOptIn bool) (*queries.Tag, error) {
	tag, err := s.queries.SetTagContentTier(ctx, queries.SetTagContentTierParams{
		ID:            tagID,
		ContentTier:   tier,
		RequiresOptIn: requiresOptIn,
	})
	if err != nil {
		return nil, err
	}
	return &tag, nil
}

// RebuildLevels recomputes every user's rollup from their event log and
// re-derives their level.
//
// This is the migration path for a threshold change: the curve lives in Go, so
// changing it does not change the database, and every already-computed level
// would otherwise keep the old value forever.
func (s *Trust) RebuildLevels(ctx context.Context) error {
	rows, err := s.queries.GetAllUserTrust(ctx)
	if err != nil {
		return err
	}

	for _, rollup := range rows {
		rebuilt, err := s.queries.RecomputeUserTrustTotals(ctx, rollup.UserID)
		if err != nil {
			return err
		}
		if _, err := s.repairLevel(ctx, &rebuilt); err != nil {
			return err
		}
	}
	return nil
}

// History returns a user's trust events, newest first.
func (s *Trust) History(ctx context.Context, userID uuid.UUID) ([]queries.TrustEvent, error) {
	return s.queries.ListUserTrustEvents(ctx, userID)
}
