package api

import (
	"context"

	"github.com/stashapp/stash-box/internal/auth"
	"github.com/stashapp/stash-box/internal/converter"
	"github.com/stashapp/stash-box/internal/dataloader"
	"github.com/stashapp/stash-box/internal/models"
)

type userResolver struct{ *Resolver }

func (r *userResolver) ID(ctx context.Context, user *models.User) (string, error) {
	return user.ID.String(), nil
}

func (r *userResolver) Roles(ctx context.Context, user *models.User) ([]models.RoleEnum, error) {
	// Limit user role visibility to admins and user themself
	if err := auth.ValidateOwner(ctx, user.ID); err != nil {
		if err := auth.ValidateRole(ctx, models.RoleEnumAdmin); err != nil {
			return nil, nil
		}
	}

	roleStrings, err := dataloader.For(ctx).UserRolesByID.Load(user.ID)
	if err != nil {
		return nil, err
	}

	return converter.StringsToRoleEnums(roleStrings), nil
}

func (r *userResolver) VoteCount(ctx context.Context, obj *models.User) (*models.UserVoteCount, error) {
	return r.services.User().CountVotesByType(ctx, obj.ID)
}

func (r *userResolver) EditCount(ctx context.Context, obj *models.User) (*models.UserEditCount, error) {
	return r.services.User().CountEditsByStatus(ctx, obj.ID)
}

func (r *userResolver) InvitedBy(ctx context.Context, user *models.User) (*models.User, error) {
	if !user.InvitedByID.Valid {
		return nil, nil
	}

	return dataloader.For(ctx).UserByID.Load(user.InvitedByID.UUID)
}

func (r *userResolver) ActiveInviteCodes(ctx context.Context, user *models.User) ([]string, error) {
	// only show if current user or invite manager
	currentUser := auth.GetCurrentUser(ctx)

	if currentUser.ID != user.ID {
		if err := auth.ValidateRole(ctx, models.RoleEnumManageInvites); err != nil {
			return nil, nil
		}
	}

	codes, err := r.InviteCodes(ctx, user)
	if err != nil {
		return nil, err
	}
	var inviteCodes []string
	for _, code := range codes {
		inviteCodes = append(inviteCodes, code.ID.String())
	}

	return inviteCodes, err
}

func (r *userResolver) InviteCodes(ctx context.Context, user *models.User) ([]models.InviteKey, error) {
	// only show if current user or invite manager
	currentUser := auth.GetCurrentUser(ctx)

	if currentUser.ID != user.ID {
		if err := auth.ValidateRole(ctx, models.RoleEnumManageInvites); err != nil {
			return nil, nil
		}
	}

	return r.services.UserToken().FindActiveInviteKeysForUser(ctx, user.ID)
}

func (r *userResolver) NotificationSubscriptions(ctx context.Context, user *models.User) ([]models.NotificationEnum, error) {
	return r.services.User().GetNotificationSubscriptions(ctx, user.ID)
}

// Trust returns a user's trust standing (SPEC section 6).
//
// The field is @isUserOwner in the schema, so the directive has already
// established that the caller is this user or an admin by the time this runs.
// Unlike Roles, there is no second visibility check here: the schema directive
// is the authorization, and duplicating it in Go would give two places to keep
// in sync for one rule.
func (r *userResolver) Trust(ctx context.Context, obj *models.User) (*models.UserTrust, error) {
	trustSvc := r.services.Trust()

	// A user with no rollup row has contributed nothing, which is level 0 with
	// zero totals -- not an error, and not a null field. Returning a zeroed
	// standing means a client never has to handle a missing trust object for a
	// perfectly normal new account.
	rollup, err := trustSvc.TotalsFor(ctx, obj.ID)
	if err != nil {
		return nil, err
	}
	level, err := trustSvc.Level(ctx, obj.ID)
	if err != nil {
		return nil, err
	}

	canView, err := trustSvc.CanViewContent(ctx, obj.ID)
	if err != nil {
		return nil, err
	}

	// The opt-in is a column on the rollup, and TotalsFor does not carry it, so
	// read it back rather than inventing a second source. It stays false when
	// there is no rollup row, which is correct: no row means no recorded
	// preference.
	optIn := false
	if stored, err := trustSvc.Rollup(ctx, obj.ID); err == nil && stored != nil {
		optIn = stored.ContentViewingOptIn
	} else if err != nil {
		return nil, err
	}

	return &models.UserTrust{
		Level:                int(level),
		ApprovedEdits:        rollup.ApprovedEdits,
		RejectedEdits:        rollup.RejectedEdits,
		IdentificationSolves: rollup.IdentificationSolves,
		QuestsCompleted:      rollup.QuestsCompleted,
		ReplicasHosted:       rollup.ReplicasHosted,
		ContentViewingOptIn:  optIn,
		CanViewContent:       canView,
	}, nil
}
