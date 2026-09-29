package api

import (
	"context"

	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/auth"
	"github.com/stashapp/stash-box/internal/models"
)

func (r *mutationResolver) UserCreate(ctx context.Context, input models.UserCreateInput) (*models.User, error) {
	return r.services.User().Create(ctx, input)
}

func (r *mutationResolver) UserUpdate(ctx context.Context, input models.UserUpdateInput) (*models.User, error) {
	return r.services.User().Update(ctx, input)
}

func (r *mutationResolver) UserDestroy(ctx context.Context, input models.UserDestroyInput) (bool, error) {
	err := r.services.User().Delete(ctx, input)
	return err == nil, err
}

func (r *mutationResolver) RegenerateAPIKey(ctx context.Context, userID *uuid.UUID) (string, error) {
	return r.services.User().RegenerateAPIKey(ctx, userID)
}

func (r *mutationResolver) ResetPassword(ctx context.Context, input models.ResetPasswordInput) (bool, error) {
	err := r.services.User().ResetPassword(ctx, input)
	return err == nil, err
}

func (r *mutationResolver) ChangePassword(ctx context.Context, input models.UserChangePasswordInput) (bool, error) {
	err := r.services.User().ChangePassword(ctx, input)
	return err == nil, err
}

func (r *mutationResolver) NewUser(ctx context.Context, input models.NewUserInput) (*uuid.UUID, error) {
	return r.services.User().NewUser(ctx, input.Email, input.InviteKey)
}

func (r *mutationResolver) ActivateNewUser(ctx context.Context, input models.ActivateNewUserInput) (*models.User, error) {
	return r.services.User().ActivateNewUser(ctx, input)
}

func (r *mutationResolver) GenerateInviteCodes(ctx context.Context, input *models.GenerateInviteCodeInput) ([]uuid.UUID, error) {
	return r.services.User().GenerateInviteCodes(ctx, input)
}

func (r *mutationResolver) GenerateInviteCode(ctx context.Context) (*uuid.UUID, error) {
	return r.services.User().GenerateInviteCode(ctx)
}

func (r *mutationResolver) RescindInviteCode(ctx context.Context, inviteKeyID uuid.UUID) (bool, error) {
	err := r.services.User().RescindInviteCode(ctx, inviteKeyID)
	return err == nil, err
}

func (r *mutationResolver) GrantInvite(ctx context.Context, input models.GrantInviteInput) (int, error) {
	return r.services.User().GrantInvite(ctx, input)
}

func (r *mutationResolver) RevokeInvite(ctx context.Context, input models.RevokeInviteInput) (int, error) {
	return r.services.User().RevokeInvite(ctx, input)
}

func (r *mutationResolver) RequestChangeEmail(ctx context.Context) (models.UserChangeEmailStatus, error) {
	return r.services.User().RequestChangeEmail(ctx)
}

func (r *mutationResolver) ValidateChangeEmail(ctx context.Context, tokenID uuid.UUID, email string) (models.UserChangeEmailStatus, error) {
	return r.services.User().ValidateChangeEmail(ctx, tokenID, email)
}

func (r *mutationResolver) ConfirmChangeEmail(ctx context.Context, tokenID uuid.UUID) (models.UserChangeEmailStatus, error) {
	return r.services.User().ConfirmChangeEmail(ctx, tokenID)
}

// SetContentViewingOptIn records the current user's own choice about viewing
// content (SPEC section 6).
//
// Self-service with no role requirement, which is the point: SPEC says
// high-trust users explicitly opt in, and a user approaching Archivist must be
// able to express the preference BEFORE they get there. Requiring a role here
// would make the "opted in but not yet eligible" state unreachable, which is
// the intended flow rather than an edge case.
//
// It takes no user id: the subject is always the caller. Accepting one would
// make it possible to set another user's preference, which is not a thing a user
// should be able to do for anyone.
func (r *mutationResolver) SetContentViewingOptIn(ctx context.Context, enabled bool) (*models.UserTrust, error) {
	user := auth.GetCurrentUser(ctx)
	if user == nil {
		// The schema has no @hasRole directive here, so an anonymous caller
		// reaches this far. Trust is per-account, so there is nothing to record
		// and nothing to return. auth.ErrUnauthorized is the same error the
		// authorization helpers return, so clients see one shape for "no".
		return nil, auth.ErrUnauthorized
	}

	trustSvc := r.services.Trust()
	if _, err := trustSvc.SetContentViewingOptIn(ctx, user.ID, enabled); err != nil {
		return nil, err
	}

	// Return the resulting standing so the client does not need a second round
	// trip to see whether the opt-in actually took effect. CanViewContent in
	// particular is derived, and the caller wants to know that.
	level, err := trustSvc.Level(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	totals, err := trustSvc.TotalsFor(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	canView, err := trustSvc.CanViewContent(ctx, user.ID)
	if err != nil {
		return nil, err
	}

	return &models.UserTrust{
		Level:                int(level),
		ApprovedEdits:        totals.ApprovedEdits,
		RejectedEdits:        totals.RejectedEdits,
		IdentificationSolves: totals.IdentificationSolves,
		QuestsCompleted:      totals.QuestsCompleted,
		ReplicasHosted:       totals.ReplicasHosted,
		ContentViewingOptIn:  enabled,
		CanViewContent:       canView,
	}, nil
}
