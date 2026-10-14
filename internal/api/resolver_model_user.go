package api

import (
	"context"

	"github.com/gofrs/uuid"

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

func (r *userResolver) ImageTypePreferences(ctx context.Context, user *models.User) ([]models.ImageTypeEnum, error) {
	return r.services.ImageType().Preferences(ctx, user.ID)
}

func (r *userResolver) ImageTypeGroupPreferences(ctx context.Context, user *models.User) ([]models.ImageTypeGroupEnum, error) {
	return r.services.ImageType().GroupPreferences(ctx, user.ID)
}

func (r *userResolver) NotificationSubscriptions(ctx context.Context, user *models.User) ([]models.NotificationEnum, error) {
	return r.services.User().GetNotificationSubscriptions(ctx, user.ID)
}

// TrustLevel returns a user's trust LEVEL, and nothing else (SPEC §5.2, plan phase 6).
//
// THE DECISION THIS IMPLEMENTS: level public, counters private. The draft made every sub-score
// public; the owner-gated `trust` object below says a member's standing is their own business. Both
// are right about different things -- a level is what their peers already see in what they
// contribute, while approved and rejected edits together are an ERROR RATE.
//
// It is a SEPARATE field rather than `trust` with its directive removed, because that object carries
// the counters: publishing it to make the level visible would publish the rejection count with it.
// There is no shape in which one field holds both answers.
//
// No visibility check here, unlike the gated fields: this is the field that is SUPPOSED to be
// readable by anybody who can see the user at all, and a second check would be a rule to keep in
// sync for no reader's benefit.
func (r *userResolver) TrustLevel(ctx context.Context, obj *models.User) (int, error) {
	level, err := r.services.Trust().Level(ctx, obj.ID)
	if err != nil {
		return 0, err
	}
	return int(level), nil
}

// Trust returns a user's trust standing (SPEC section 6).
//
// The field is @isUserOwner in the schema, so the directive has already
// established that the caller is this user or an admin by the time this runs.
// Unlike Roles, there is no second visibility check here: the schema directive
// is the authorization, and duplicating it in Go would give two places to keep
// in sync for one rule.
func (r *userResolver) Trust(ctx context.Context, obj *models.User) (*models.UserTrust, error) {
	// A user with no rollup row has contributed nothing, which is level 0 with
	// zero totals -- not an error, and not a null field. Returning a zeroed
	// standing means a client never has to handle a missing trust object for a
	// perfectly normal new account.
	return r.userTrustModel(ctx, obj.ID)
}

// userTrustModel builds the standing the schema describes for one member.
//
// One implementation, because there are now three callers that must agree: the owner-gated
// `User.trust` field, the leaderboard-participation mutation, and setContentViewingOptIn's
// own copy (which predates this helper and is left as it is -- the counters it maps are the
// same, and rewriting a working path to save a line is not this slice's business).
//
// THE DEFAULTS ARE PART OF THE CONTRACT. A member with no rollup row is a new account, so
// the standing is zeroed rather than null, and `LeaderboardListed` answers with the
// COLUMN's default (true) rather than Go's zero value (false) -- otherwise every member who
// has never touched the setting would read as having opted out of the board.
func (r *Resolver) userTrustModel(ctx context.Context, userID uuid.UUID) (*models.UserTrust, error) {
	trustSvc := r.services.Trust()

	rollup, err := trustSvc.TotalsFor(ctx, userID)
	if err != nil {
		return nil, err
	}
	level, err := trustSvc.Level(ctx, userID)
	if err != nil {
		return nil, err
	}

	canView, err := trustSvc.CanViewContent(ctx, userID)
	if err != nil {
		return nil, err
	}

	// The opt-in is a column on the rollup, and TotalsFor does not carry it, so
	// read it back rather than inventing a second source. It stays false when
	// there is no rollup row, which is correct: no row means no recorded
	// preference.
	optIn := false
	// Listed defaults to TRUE, and the default is the trap this line exists to avoid: the
	// column is NOT NULL DEFAULT true, but a Go bool left unset is false, so a member who
	// has done nothing wrong would read as having opted out of the leaderboard. Read the
	// column when there is a row, and answer with the column's own default when there is
	// not.
	listed := true
	if stored, err := trustSvc.Rollup(ctx, userID); err == nil && stored != nil {
		optIn = stored.ContentViewingOptIn
		listed = stored.LeaderboardListed
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
		LeaderboardListed:    listed,
		CanViewContent:       canView,
	}, nil
}
