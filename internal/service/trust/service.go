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

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/stashapp/stash-box/internal/queries"
)

// ErrUnknownKind is returned when an event names a kind the rollup cannot count.
//
// The event is still recorded in trust_events before this is returned, because
// the log is the audit trail and a kind this version does not understand may
// well be understood by a later one. Refusing to record it would lose history
// that a threshold rebuild would then never see.
var ErrUnknownKind = errors.New("unknown trust event kind")

// Trust manages trust levels and the events that produce them.
type Trust struct {
	queries *queries.Queries
	withTxn queries.WithTxnFunc
}

// NewTrust creates a new trust service.
func NewTrust(queries *queries.Queries, withTxn queries.WithTxnFunc) *Trust {
	return &Trust{queries: queries, withTxn: withTxn}
}

// WithTxn executes a function within a transaction.
func (s *Trust) WithTxn(fn func(*queries.Queries) error) error {
	return s.withTxn(fn)
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
	if k == KindBountyBonus {
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
	if event.Delta == 0 {
		// A zero delta would insert a row that moves no counter, and would still
		// occupy the dedup slot -- so a later real event for the same entity
		// would be silently discarded as a "duplicate".
		return nil, errors.New("trust event delta must be non-zero")
	}

	if !knownKind(event.Kind) {
		// Record it anyway. The log is the source of truth and a later version
		// may know this kind; refusing to log it would make the contribution
		// invisible to every future rebuild.
		if _, err := s.queries.RecordTrustEvent(ctx, toRecordParams(event)); err != nil &&
			!errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		return nil, ErrUnknownKind
	}

	return s.withRecordTx(ctx, event)
}

func (s *Trust) withRecordTx(ctx context.Context, event Event) (*queries.UserTrust, error) {
	var out *queries.UserTrust
	err := s.withTxn(func(tx *queries.Queries) error {
		// ON CONFLICT DO NOTHING makes a duplicate report no rows, which
		// pgx surfaces as ErrNoRows. That is the "already recorded" signal, not
		// a failure, and it must be distinguished from a real insert below.
		_, err := tx.RecordTrustEvent(ctx, toRecordParams(event))
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			// This exact event already exists. Leave the rollup alone rather
			// than incrementing a second time for one contribution.
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
		out = &rollup
		return nil
	})
	if err != nil {
		return nil, err
	}

	return s.recomputeLevel(ctx, out)
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
func (s *Trust) recomputeLevel(ctx context.Context, rollup *queries.UserTrust) (*queries.UserTrust, error) {
	if rollup == nil {
		return nil, nil
	}

	want := LevelForTotals(Totals{
		ApprovedEdits:        rollup.ApprovedEdits,
		RejectedEdits:        rollup.RejectedEdits,
		IdentificationSolves: rollup.IdentificationSolves,
		QuestsCompleted:      rollup.QuestsCompleted,
		ReplicasHosted:       rollup.ReplicasHosted,
	})
	if int(rollup.Level) == int(want) {
		return rollup, nil
	}

	updated, err := s.queries.SetUserTrustLevel(ctx, queries.SetUserTrustLevelParams{
		UserID: rollup.UserID,
		Level:  int(want),
	})
	if err != nil {
		return nil, err
	}
	return &updated, nil
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
		if _, err := s.recomputeLevel(ctx, &rebuilt); err != nil {
			return err
		}
	}
	return nil
}

// History returns a user's trust events, newest first.
func (s *Trust) History(ctx context.Context, userID uuid.UUID) ([]queries.TrustEvent, error) {
	return s.queries.ListUserTrustEvents(ctx, userID)
}
