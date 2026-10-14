// Package vouch records one member attesting to another's standing.
//
// A vouch is the LOCAL counterpart of the signed cross-instance attestations in
// SPEC §7.17, and deliberately not the same thing: those travel between instances
// and are weighted by the receiving operator's trust in the sender, while this is
// one account saying "I know this one" inside a single instance. Building the mesh
// version first would have meant designing a signature scheme for a feature whose
// local half had not been decided.
//
// THE CAP IS THE FEATURE. A member may hold at most MaxActiveVouches active
// vouches and the next one is refused, which is what stops a new account being
// vouched into standing by five friends in an afternoon. Everything else here --
// expiry, withdrawal, the standing requirement on the VOUCHER -- exists to make
// "active" mean something a reader can check.
//
// A VOUCH GRANTS NO POINTS. It is a record, not an input to the level curve: the
// curve's inputs are contributions, and a path from "somebody said so" to a level
// would be a brigading vector with a friendly name.
package vouch

import (
	"context"
	"errors"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/trust"
)

// MaxActiveVouches is how many active vouches one member may hold.
//
// Three. The number has to be small enough that a vouch is a statement a member
// would defend -- vouching for a fourth person has to cost something, and the cost
// is that one of the existing three has to go. A higher cap makes the list a
// contact book.
const MaxActiveVouches = 3

// VouchValidityDays is how long a vouch stays active without being renewed.
//
// Half a year, following SPEC §7.17's rule that attestation weight DECAYS and
// requires re-signing: a vouch is a claim its author still makes, so it expires
// and has to be given again. Nothing runs to enforce it -- "active" is derived
// from the expiry on every read, the same way the streak is derived from the log,
// so there is no stored status to go stale.
const VouchValidityDays = 180

// VoucherMinLevel is the standing a member needs before their word counts.
//
// LevelContributor, which is fifty points: a member who has had several edits
// applied and has been around long enough to be wrong in public. A fresh account
// cannot vouch for anybody, which is the other half of the anti-brigading story --
// the cap limits how many vouches a target can hold, and this limits who can give
// them at all.
const VoucherMinLevel = trust.LevelContributor

var (
	// ErrSelfVouch: nobody vouches for themselves.
	ErrSelfVouch = errors.New("a member cannot vouch for themselves")
	// ErrNoSuchTarget: the member being vouched for does not exist.
	ErrNoSuchTarget = errors.New("no such member")
	// ErrAlreadyVouched: this voucher already has an active vouch for this target.
	ErrAlreadyVouched = errors.New("this member already vouches for that one")
	// ErrVouchLimit: the target already holds MaxActiveVouches active vouches.
	ErrVouchLimit = errors.New("this member already holds the maximum active vouches")
	// ErrVoucherStanding: the voucher has not earned the standing to vouch.
	ErrVoucherStanding = errors.New("vouching requires a contribution standing")
	// ErrNoVouch: there is no active vouch from this voucher to this target.
	ErrNoVouch = errors.New("no active vouch to withdraw")
)

// Vouch is one accepted attestation.
type Vouch struct {
	ID          uuid.UUID
	VoucherID   uuid.UUID
	TargetID    uuid.UUID
	CreatedAt   time.Time
	ExpiresAt   time.Time
	WithdrawnAt *time.Time
}

// Active reports whether a vouch still counts at a given moment.
//
// Derived rather than stored, and the withdrawal half of it is deliberately NOT
// filtered out of the struct: a caller that wants the history (an operator looking
// at a pattern of drive-by vouching) gets the withdrawn rows with their dates
// attached, and a caller that wants the count asks this.
func (v Vouch) Active(now time.Time) bool {
	return v.WithdrawnAt == nil && v.ExpiresAt.After(now)
}

// Service records and ends vouches.
type Service struct {
	queries *queries.Queries
	withTxn queries.WithTxnFunc
	trust   *trust.Trust
}

// NewService builds a vouch service.
func NewService(q *queries.Queries, withTxn queries.WithTxnFunc, trustSvc *trust.Trust) *Service {
	return &Service{queries: q, withTxn: withTxn, trust: trustSvc}
}

// decide returns the reason a vouch cannot be given, or nil.
//
// Everything that can be known without asking the database. The two facts that need
// a query -- whether this voucher already has an active vouch for this member, and
// how full the target is -- are not here, and deliberately: a pure function that
// guessed at them would be a second implementation of the index and the count.
func decide(voucherID, targetID uuid.UUID, voucherLevel trust.LevelEnum, held int) error {
	if err := decideVoucher(voucherID, targetID, voucherLevel); err != nil {
		return err
	}
	return decideCap(held)
}

// decideVoucher is the half of the decision that needs NO QUERY, and it is separated for a reason that
// a probe found: a self-vouch used to be refused from inside the transaction, so "you cannot vouch for
// yourself" came back as a database error when the database was unreachable, and cost a row lock when it
// was not. A refusal that can be made without asking anything should not depend on anything.
func decideVoucher(voucherID, targetID uuid.UUID, voucherLevel trust.LevelEnum) error {
	if voucherID == targetID {
		return ErrSelfVouch
	}
	if voucherLevel < VoucherMinLevel {
		return ErrVoucherStanding
	}
	return nil
}

// decideCap is the half that needs the count, which is why it can only run inside the transaction that
// holds the target's row lock.
func decideCap(held int) error {
	if held >= MaxActiveVouches {
		return ErrVouchLimit
	}
	return nil
}

// Vouch records that voucher attests to target, and refuses the fourth active vouch.
//
// THE CHECKS ARE IN ONE TRANSACTION WITH A ROW LOCK ON THE TARGET, and that is not
// decoration: the cap is a count followed by an insert, so two simultaneous vouches
// can each see two and both land. A unique index cannot express "at most three", so
// the serialisation has to come from a lock, and the lock is on the member being
// vouched for because that is what the cap is about.
func (s *Service) Vouch(ctx context.Context, voucherID, targetID uuid.UUID, now time.Time) (Vouch, error) {
	// The voucher's standing, read OUTSIDE the transaction on purpose: it cannot
	// change as a result of this vouch, so holding the lock while asking the trust
	// service for it would be holding it for no reason. The DECISION is made inside,
	// where the real count is, so that there is exactly one implementation of the
	// rules and it runs against the number the write will act on.
	level, err := s.trust.Level(ctx, voucherID)
	if err != nil {
		return Vouch{}, err
	}
	if err := decideVoucher(voucherID, targetID, level); err != nil {
		return Vouch{}, err
	}

	var out Vouch
	err = s.withTxn(func(tx *queries.Queries) error {
		if _, err := tx.LockUserForVouch(ctx, targetID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNoSuchTarget
			}
			return err
		}

		held, err := tx.CountActiveVouchesForTarget(ctx, queries.CountActiveVouchesForTargetParams{
			TargetID:  targetID,
			ExpiresAt: timestamptz(now),
		})
		if err != nil {
			return err
		}
		// Only the cap is left to decide: decideVoucher already ran, outside the lock.
		if err := decideCap(int(held)); err != nil {
			return err
		}

		row, err := tx.RecordVouch(ctx, queries.RecordVouchParams{
			ID:        uuid.Must(uuid.NewV7()),
			VoucherID: voucherID,
			TargetID:  targetID,
			ExpiresAt: timestamptz(now.AddDate(0, 0, VouchValidityDays)),
		})
		if err != nil {
			// ON CONFLICT DO NOTHING reports no rows for the partial unique index
			// over active pairs: this voucher already vouches for this member.
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrAlreadyVouched
			}
			return err
		}
		out = toVouch(row)
		return nil
	})
	if err != nil {
		return Vouch{}, err
	}
	return out, nil
}

// Withdraw ends the voucher's own vouch, freeing the slot it held.
func (s *Service) Withdraw(ctx context.Context, voucherID, targetID uuid.UUID, now time.Time) error {
	_, err := s.queries.WithdrawVouch(ctx, queries.WithdrawVouchParams{
		VoucherID:   voucherID,
		TargetID:    targetID,
		WithdrawnAt: timestamptz(now),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoVouch
		}
		return err
	}
	return nil
}

// ActiveForTarget lists a target's active vouches, newest first.
func (s *Service) ActiveForTarget(ctx context.Context, targetID uuid.UUID, now time.Time) ([]Vouch, error) {
	rows, err := s.queries.ListActiveVouchesForTarget(ctx, queries.ListActiveVouchesForTargetParams{
		TargetID:  targetID,
		ExpiresAt: timestamptz(now),
	})
	if err != nil {
		return nil, err
	}
	return mapVouches(rows), nil
}

// HistoryForTarget lists every vouch a target has held, withdrawn and expired ones
// included.
func (s *Service) HistoryForTarget(ctx context.Context, targetID uuid.UUID) ([]Vouch, error) {
	rows, err := s.queries.ListVouchesForTarget(ctx, targetID)
	if err != nil {
		return nil, err
	}
	return mapVouches(rows), nil
}

// SlotsAvailable is how many more active vouches the target can hold.
func (s *Service) SlotsAvailable(ctx context.Context, targetID uuid.UUID, now time.Time) (int, error) {
	held, err := s.queries.CountActiveVouchesForTarget(ctx, queries.CountActiveVouchesForTargetParams{
		TargetID:  targetID,
		ExpiresAt: timestamptz(now),
	})
	if err != nil {
		return 0, err
	}
	left := MaxActiveVouches - int(held)
	if left < 0 {
		// A lowered cap, or a hand-edited row. A negative answer reads as a debt.
		left = 0
	}
	return left, nil
}

func toVouch(row queries.MemberVouch) Vouch {
	v := Vouch{
		ID:        row.ID,
		VoucherID: row.VoucherID,
		TargetID:  row.TargetID,
		CreatedAt: row.CreatedAt.Time,
		ExpiresAt: row.ExpiresAt.Time,
	}
	if row.WithdrawnAt.Valid {
		at := row.WithdrawnAt.Time
		v.WithdrawnAt = &at
	}
	return v
}

func mapVouches(rows []queries.MemberVouch) []Vouch {
	out := make([]Vouch, 0, len(rows))
	for _, row := range rows {
		out = append(out, toVouch(row))
	}
	return out
}

func timestamptz(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}
