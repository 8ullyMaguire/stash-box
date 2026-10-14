package api

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/auth"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/service/vouch"
)

// vouchResolver resolves the fields of Vouch that need a lookup of their own.
type vouchResolver struct{ *Resolver }

// Voucher is the member who gave the vouch.
//
// One lookup per vouch, which is correct here and would be wrong for a list: a
// member holds at most three ACTIVE vouches, so the N in this N+1 is bounded by the
// cap itself. The history view is deliberately not exposed over GraphQL for the
// same reason -- it is unbounded, and it is an operator's view rather than a
// reader's.
func (r *vouchResolver) Voucher(ctx context.Context, obj *models.Vouch) (*models.User, error) {
	return r.services.User().FindByID(ctx, obj.VoucherID)
}

// Vouches returns the member's active vouches. The @isUserOwner directive has
// already rejected anybody else, so there is no check here -- the same shape as
// the owner-gated trust object next door.
func (r *userResolver) Vouches(ctx context.Context, user *models.User) ([]models.Vouch, error) {
	rows, err := r.services.Vouches().ActiveForTarget(ctx, user.ID, time.Now())
	if err != nil {
		return nil, err
	}
	out := make([]models.Vouch, 0, len(rows))
	for _, v := range rows {
		out = append(out, vouchModel(v))
	}
	return out, nil
}

// VouchForUser records that the caller vouches for a member.
func (r *mutationResolver) VouchForUser(ctx context.Context, userID uuid.UUID) (*models.Vouch, error) {
	caller := auth.GetCurrentUser(ctx)
	if caller == nil {
		return nil, auth.ErrUnauthorized
	}
	created, err := r.services.Vouches().Vouch(ctx, caller.ID, userID, time.Now())
	if err != nil {
		return nil, err
	}
	out := vouchModel(created)
	return &out, nil
}

// WithdrawVouch ends the caller's own vouch.
func (r *mutationResolver) WithdrawVouch(ctx context.Context, userID uuid.UUID) (bool, error) {
	caller := auth.GetCurrentUser(ctx)
	if caller == nil {
		return false, auth.ErrUnauthorized
	}
	if err := r.services.Vouches().Withdraw(ctx, caller.ID, userID, time.Now()); err != nil {
		return false, err
	}
	return true, nil
}

// vouchModel maps the service's vouch onto the schema's.
//
// The timestamps are RFC3339, unlike `lastActiveDay` on the streak: those are
// CALENDAR DAYS dressed as timestamps, and this is an instant, so formatting this
// one as a date would throw away the only part that matters.
func vouchModel(v vouch.Vouch) models.Vouch {
	return models.Vouch{
		ID:        v.ID,
		VoucherID: v.VoucherID,
		CreatedAt: v.CreatedAt.Format(time.RFC3339),
		ExpiresAt: v.ExpiresAt.Format(time.RFC3339),
		Active:    v.Active(time.Now()),
	}
}
