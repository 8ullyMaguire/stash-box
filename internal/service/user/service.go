package user

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/stashapp/stash-box/internal/auth"
	"github.com/stashapp/stash-box/internal/config"
	"github.com/stashapp/stash-box/internal/converter"
	"github.com/stashapp/stash-box/internal/email"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/errutil"
	"github.com/stashapp/stash-box/internal/service/loadutil"
	"github.com/stashapp/stash-box/internal/service/trust"
	"github.com/stashapp/stash-box/pkg/utils"
)

// User handles user-related operations
type User struct {
	queries  *queries.Queries
	withTxn  queries.WithTxnFunc
	emailMgr *email.Manager
	// trust is the one implementation of "what level is this user": the invite
	// allowance is derived from it, so a second reading of the rollup here would
	// be a second answer to the same question (phase 11, slice 11a).
	trust *trust.Trust
}

// NewUser creates a new user service
func NewUser(queries *queries.Queries, withTxn queries.WithTxnFunc, emailMgr *email.Manager, trustSvc *trust.Trust) *User {
	return &User{
		queries:  queries,
		withTxn:  withTxn,
		emailMgr: emailMgr,
		trust:    trustSvc,
	}
}

// WithTxn executes a function within a transaction
func (s *User) WithTxn(fn func(*queries.Queries) error) error {
	return s.withTxn(fn)
}

// Queries

func (s *User) FindByID(ctx context.Context, id uuid.UUID) (*models.User, error) {
	user, err := s.queries.FindUser(ctx, id)
	if err != nil {
		return nil, errutil.IgnoreNotFound(err)
	}

	return converter.UserToModelPtr(user), nil
}

// FindWithRoles fetches the user and their roles in a single query.
// Returns (nil, nil, nil) when the user does not exist.
func (s *User) FindWithRoles(ctx context.Context, id uuid.UUID) (*models.User, []models.RoleEnum, error) {
	row, err := s.queries.FindUserWithRoles(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, nil
		}
		return nil, nil, err
	}

	roles := make([]models.RoleEnum, 0, len(row.Roles))
	for _, r := range row.Roles {
		roles = append(roles, models.RoleEnum(r))
	}

	return converter.UserToModelPtr(row.User), roles, nil
}

// Dataloader method — batches multiple FindByID lookups in one query
func (s *User) LoadIds(ctx context.Context, ids []uuid.UUID) ([]*models.User, []error) {
	return loadutil.One(ids,
		func(ids []uuid.UUID) ([]queries.User, error) { return s.queries.GetUsers(ctx, ids) },
		func(user queries.User) uuid.UUID { return user.ID },
		converter.UserToModelPtr,
	)
}

func (s *User) FindByName(ctx context.Context, name string) (*models.User, error) {
	user, err := s.queries.FindUserByName(ctx, strings.ToUpper(name))
	if err != nil {
		return nil, errutil.IgnoreNotFound(err)
	}
	return converter.UserToModelPtr(user), nil
}

func (s *User) Count(ctx context.Context) (int, error) {
	count, err := s.queries.CountUsers(ctx)
	return int(count), err
}

func (s *User) CountVotesByType(ctx context.Context, userID uuid.UUID) (*models.UserVoteCount, error) {
	rows, err := s.queries.CountVotesByType(ctx, uuid.NullUUID{UUID: userID, Valid: true})
	if err != nil {
		return nil, err
	}

	var result models.UserVoteCount
	for _, row := range rows {
		count := int(row.Count)

		switch row.Vote {
		case "ACCEPT":
			result.Accept = count
		case "REJECT":
			result.Reject = count
		case "ABSTAIN":
			result.Abstain = count
		case "IMMEDIATE_ACCEPT":
			result.ImmediateAccept = count
		case "IMMEDIATE_REJECT":
			result.ImmediateReject = count
		}
	}

	return &result, nil
}

func (s *User) CountEditsByStatus(ctx context.Context, userID uuid.UUID) (*models.UserEditCount, error) {
	rows, err := s.queries.CountUserEditsByStatus(ctx, uuid.NullUUID{UUID: userID, Valid: true})
	if err != nil {
		return nil, err
	}

	var result models.UserEditCount
	for _, row := range rows {
		count := int(row.Count)

		if row.Bot {
			switch row.Status {
			case "ACCEPTED":
				result.AcceptedBot = count
			case "REJECTED":
				result.RejectedBot = count
			case "PENDING":
				result.PendingBot = count
			case "IMMEDIATE_ACCEPTED":
				result.ImmediateAcceptedBot = count
			case "IMMEDIATE_REJECTED":
				result.ImmediateRejectedBot = count
			case "FAILED":
				result.FailedBot = count
			case "CANCELED":
				result.CanceledBot = count
			}
		} else {
			switch row.Status {
			case "ACCEPTED":
				result.Accepted = count
			case "REJECTED":
				result.Rejected = count
			case "PENDING":
				result.Pending = count
			case "IMMEDIATE_ACCEPTED":
				result.ImmediateAccepted = count
			case "IMMEDIATE_REJECTED":
				result.ImmediateRejected = count
			case "FAILED":
				result.Failed = count
			case "CANCELED":
				result.Canceled = count
			}
		}
	}

	return &result, nil
}

func (s *User) GetNotificationSubscriptions(ctx context.Context, userID uuid.UUID) ([]models.NotificationEnum, error) {
	rows, err := s.queries.GetUserNotificationSubscriptions(ctx, userID)
	if err != nil {
		return nil, err
	}

	var notifications []models.NotificationEnum
	for _, row := range rows {
		notifications = append(notifications, models.NotificationEnum(row))
	}

	return notifications, nil
}

func (s *User) GetRoles(ctx context.Context, userID uuid.UUID) ([]models.RoleEnum, error) {
	roleStrings, err := s.queries.GetUserRoles(ctx, userID)
	if err != nil {
		return nil, err
	}

	return converter.StringsToRoleEnums(roleStrings), nil
}

// LoadRoles fetches roles for multiple users in one query.
func (s *User) LoadRoles(ctx context.Context, userIDs []uuid.UUID) ([][]string, []error) {
	return loadutil.Many(userIDs,
		func(ids []uuid.UUID) ([]queries.UserRole, error) {
			return s.queries.GetUserRolesByUserIDs(ctx, ids)
		},
		func(role queries.UserRole) uuid.UUID { return role.UserID },
		func(role queries.UserRole) string { return role.Role },
	)
}

// NewUser registers a new user. It returns the activation key only if
// email verification is not required, otherwise it returns nil.
func (s *User) NewUser(ctx context.Context, emailAddr string, inviteKey *uuid.UUID) (*uuid.UUID, error) {
	// ensure user or pending activation with email does not already exist
	if err := validateUserEmail(emailAddr); err != nil {
		return nil, err
	}

	var err error
	var activationKey *uuid.UUID
	err = s.withTxn(func(tx *queries.Queries) error {
		if err := validateExistingEmail(ctx, tx, emailAddr); err != nil {
			return err
		}

		if err := validateInviteKey(ctx, tx, inviteKey); err != nil {
			return err
		}

		// generate an activation key and email
		activationToken, err := generateActivationKey(ctx, tx, emailAddr, inviteKey)
		if err != nil {
			return err
		}

		// if activation is not required, return the activation key to the frontend
		// so it can auto-navigate to the activation page
		if !config.GetRequireActivation() {
			activationKey = &activationToken.ID
			return nil
		}

		// if activation is required, send the email and return nil
		return email.SendNewUserEmail(emailAddr, activationToken.ID, s.emailMgr)
	})

	return activationKey, err
}

func (s *User) Create(ctx context.Context, input models.UserCreateInput) (*models.User, error) {
	var user *models.User
	err := s.withTxn(func(tx *queries.Queries) error {
		createdUser, err := createUser(ctx, tx, input, true)
		if createdUser != nil {
			user = converter.UserToModelPtr(*createdUser)
		}
		return err
	})

	return user, err
}

func (s *User) Update(ctx context.Context, input models.UserUpdateInput) (*models.User, error) {
	var user queries.User
	err := s.withTxn(func(tx *queries.Queries) error {
		existingUser, err := tx.FindUser(ctx, input.ID)
		if err != nil {
			return err
		}

		// Validate changes and permissions
		if err := validateUpdate(ctx, input, existingUser); err != nil {
			return err
		}

		hash := existingUser.PasswordHash
		if input.Password != nil {
			hash, err = hashPassword(*input.Password)
			if err != nil {
				return fmt.Errorf("error updating user")
			}
		}

		params := converter.UpdateUserFromUpdateInput(existingUser, input, hash)
		user, err = tx.UpdateUser(ctx, params)
		if err != nil {
			return err
		}

		// Update roles
		// TODO - only do this if provided
		return updateRoles(ctx, tx, user.ID, input.Roles)
	})

	if err == nil {
		auth.CacheInvalidate(input.ID)
	}

	return converter.UserToModelPtr(user), err
}

func (s *User) Delete(ctx context.Context, input models.UserDestroyInput) error {
	err := s.withTxn(func(tx *queries.Queries) error {
		existingUser, err := tx.FindUser(ctx, input.ID)
		if err != nil {
			return err
		}

		if err := validateDelete(existingUser); err != nil {
			return err
		}

		// Retain the user's unique fingerprints by reassigning them to the sentinel user
		deletedUser, err := tx.FindUserByName(ctx, deletedUserName)
		if err != nil {
			return err
		}
		if err := tx.ReassignUniqueSceneFingerprints(ctx, queries.ReassignUniqueSceneFingerprintsParams{
			TargetUserID: deletedUser.ID,
			SourceUserID: input.ID,
		}); err != nil {
			return err
		}

		if err := tx.DeleteUser(ctx, input.ID); err != nil {
			return err
		}

		return tx.CancelUserEdits(ctx, uuid.NullUUID{UUID: input.ID, Valid: true})
	})

	if err == nil {
		auth.CacheInvalidate(input.ID)
	}

	return err
}

func (s *User) RegenerateAPIKey(ctx context.Context, userID *uuid.UUID) (string, error) {
	currentUser := auth.GetCurrentUser(ctx)

	if userID != nil {
		if currentUser.ID != *userID {
			// changing another user api key
			// must be admin
			if err := auth.ValidateAdmin(ctx); err != nil {
				return "", err
			}
		}
	} else {
		// changing current user api key
		userID = &currentUser.ID
	}

	key := ""
	err := s.withTxn(func(tx *queries.Queries) error {
		user, err := tx.FindUser(ctx, *userID)
		if err != nil {
			return fmt.Errorf("error finding user: %w", err)
		}

		key, err = generateAPIKey(user.ID.String())
		if err != nil {
			return fmt.Errorf("error generating APIKey: %w", err)
		}

		return tx.UpdateUserAPIKey(ctx, queries.UpdateUserAPIKeyParams{
			ID:     *userID,
			ApiKey: key,
		})
	})

	if err == nil {
		auth.CacheInvalidate(*userID)
	}

	return key, err
}

func (s *User) ResetPassword(ctx context.Context, input models.ResetPasswordInput) error {
	return s.withTxn(func(tx *queries.Queries) error {
		u, err := tx.FindUserByEmail(ctx, input.Email)

		// Sleep between 500-1500ms to avoid leaking email presence
		n := rand.Intn(1000)
		time.Sleep(time.Duration(500+n) * time.Millisecond)

		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		} else if err != nil {
			return err
		}

		// generate an activation key and email
		key, err := generateResetPasswordActivationKey(ctx, tx, u.ID)
		if err != nil {
			return err
		}

		return email.SendResetPasswordEmail(u, *key, s.emailMgr)
	})
}

func (s *User) ChangePassword(ctx context.Context, input models.UserChangePasswordInput) error {
	currentUser := auth.GetCurrentUser(ctx)

	if input.ResetKey != nil {
		return s.withTxn(func(tx *queries.Queries) error {
			return activateResetPassword(ctx, tx, *input.ResetKey, input.NewPassword)
		})
	}

	// just setting password
	if currentUser == nil {
		return auth.ErrUnauthorized
	}

	if input.ExistingPassword == nil {
		return ErrCurrentPasswordIncorrect
	}

	return s.withTxn(func(tx *queries.Queries) error {
		return changePassword(ctx, tx, currentUser.ID, *input.ExistingPassword, input.NewPassword)
	})
}

func (s *User) ActivateNewUser(ctx context.Context, input models.ActivateNewUserInput) (*models.User, error) {
	var user queries.User
	err := s.withTxn(func(tx *queries.Queries) error {
		token, err := tx.FindUserToken(ctx, input.ActivationKey)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrInvalidActivationKey
			}
			return err
		}
		if token.Type != models.UserTokenTypeNewUser {
			return ErrInvalidActivationKey
		}

		data, err := getNewUserTokenData(token)
		if err != nil {
			return err
		}

		// WHO INVITED THIS MEMBER IS RECORDED WHENEVER A KEY WAS USED, whether or not this
		// instance REQUIRES invites (plan phase 11 slice 11b).
		//
		// It used to be recorded only when require_invite was on, and that made half a fact
		// out of one key: `InviteKeyUsed` consumes the key's use at activation on EVERY
		// instance (see below), while the inviter was written only on an instance that
		// demanded a key. So a signup that carried a key on the default configuration spent
		// the key and recorded nobody, and the referral payment built on this column would
		// have been a feature that pays nothing, on every instance that does not require
		// invites -- with no error and nothing to notice it by.
		//
		// The key is the evidence either way: it names its generator, and the signup that
		// carried it is the strongest statement of "this member came in through that
		// member". require_invite decides whether a key is MANDATORY, which is a policy
		// about admission; who invited whom is a fact about the key, and it is now recorded
		// in both configurations.
		var invitedBy *uuid.UUID
		switch {
		case data.InviteKey != nil:
			invite, err := tx.FindInviteKey(ctx, *data.InviteKey)
			switch {
			case errors.Is(err, pgx.ErrNoRows):
				// A key that no longer exists. Without require_invite this activation
				// proceeds with no inviter (which is what it did before this change);
				// with it, a key that cannot be resolved is exactly the invalid-key
				// refusal the signup is supposed to get.
				if config.GetRequireInvite() {
					return ErrInvalidInviteKey
				}
			case err != nil:
				return err
			default:
				invitedBy = &invite.GeneratedBy
			}
		case config.GetRequireInvite():
			return errors.New("cannot find invite key")
		}

		createInput := models.UserCreateInput{
			Name:        input.Name,
			Email:       data.Email,
			Password:    input.Password,
			InvitedByID: invitedBy,
			Roles:       getDefaultUserRoles(),
		}

		if _, err := tx.FindUserByName(ctx, createInput.Name); !errors.Is(err, pgx.ErrNoRows) {
			if err != nil {
				return err
			}
			return errors.New("username already used")
		}

		createdUser, err := createUser(ctx, tx, createInput, true)
		if err != nil {
			return err
		}
		user = *createdUser

		// delete the activation
		if err := tx.DeleteUserToken(ctx, token.ID); err != nil {
			return err
		}

		// CONSUMING A PRESENTED KEY IS A FACT ABOUT THE KEY, and it belongs on the same side of the
		// flag as validating one (plan phase 11, the invitation-integrity finding).
		//
		// It used to be gated on require_invite, so on an instance that does not require invites --
		// where a key is optional and therefore easy to hand around -- a key was NEVER CONSUMED:
		// `uses` stayed at its minted value forever, no key was ever deleted for being spent, and
		// the "is this key used up" question had no record to read. The flag decides whether a key
		// is mandatory; it must not decide whether using one counts.
		//
		// A key that no longer resolves is still a quiet no-op here (InviteKeyUsed matches no rows),
		// which is the same tolerance ActivateNewUser applies to the inviter above: the activation
		// of a member who carried a key that was rescinded in the meantime is not this function's
		// business to refuse.
		if data.InviteKey != nil {
			usesLeft, err := tx.InviteKeyUsed(ctx, *data.InviteKey)
			// Ignore "no rows" error - this happens when the invite key has unlimited uses (NULL)
			// and when the key has gone.
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}

			// if all used up, then delete the invite key
			if usesLeft != nil && *usesLeft <= 0 {
				// delete the invite key
				if err := tx.DeleteInviteKey(ctx, *data.InviteKey); err != nil {
					return err
				}
			}
		}

		return nil
	})

	return converter.UserToModelPtr(user), err
}

func (s *User) GenerateInviteCodes(ctx context.Context, input *models.GenerateInviteCodeInput) ([]uuid.UUID, error) {
	// INVITE role allows generating invite keys without tokens: that is the
	// exemption the allowance carries, unchanged from before phase 11.
	exempt := auth.ValidateInvite(ctx) == nil

	currentUser := auth.GetCurrentUser(ctx)
	var ret []uuid.UUID
	err := s.withTxn(func(tx *queries.Queries) error {
		keys, err := generateInviteKeys(ctx, tx, s.trust, currentUser.ID, input, exempt)
		if err != nil {
			return err
		}

		ret = append(ret, keys...)

		return nil
	})

	return ret, err
}

func (s *User) GenerateInviteCode(ctx context.Context) (*uuid.UUID, error) {
	// INVITE role allows generating invite keys without tokens, as above.
	exempt := auth.ValidateInvite(ctx) == nil

	currentUser := auth.GetCurrentUser(ctx)

	var ret *uuid.UUID
	err := s.withTxn(func(tx *queries.Queries) error {

		keys := 1
		uses := 1
		input := &models.GenerateInviteCodeInput{
			Keys: &keys,
			Uses: &uses,
		}

		inviteKeys, txnErr := generateInviteKeys(ctx, tx, s.trust, currentUser.ID, input, exempt)
		if txnErr != nil {
			return txnErr
		}

		if len(inviteKeys) == 0 {
			return errors.New("no invite code generated")
		}

		ret = &inviteKeys[0]

		return nil
	})

	return ret, err
}

func (s *User) RescindInviteCode(ctx context.Context, inviteKeyID uuid.UUID) error {
	// A member WITHOUT the INVITE role is the one who had to pay a token for a
	// key, so they are the one a rescind refunds. An INVITE-role mint never spent
	// a grant, so there is nothing to give back -- this is the pre-phase-11
	// rule (it was spelled `requireToken`), kept deliberately and named for what
	// it means at the refund site.
	paidForKeys := auth.ValidateInvite(ctx) != nil

	tokenManagerErr := auth.ValidateManageInvites(ctx)

	currentUser := auth.GetCurrentUser(ctx)
	return s.withTxn(func(tx *queries.Queries) error {
		userID := currentUser.ID

		// Non-token managers may only rescind their own invite code
		if tokenManagerErr == nil {
			inviteKey, err := tx.FindInviteKey(ctx, inviteKeyID)
			if err != nil {
				return err
			}

			userID = inviteKey.GeneratedBy
		}

		err := rescindInviteKey(ctx, tx, inviteKeyID, userID, paidForKeys)
		if err != nil {
			return err
		}

		return nil
	})
}

func (s *User) GrantInvite(ctx context.Context, input models.GrantInviteInput) (int, error) {
	if err := auth.ValidateManageInvites(ctx); err != nil {
		return 0, err
	}

	var ret int
	err := s.withTxn(func(tx *queries.Queries) error {
		count, err := grantInviteTokens(ctx, tx, input.UserID, input.Amount)
		ret = count

		return err
	})

	return ret, err
}

func (s *User) RevokeInvite(ctx context.Context, input models.RevokeInviteInput) (int, error) {
	if err := auth.ValidateManageInvites(ctx); err != nil {
		return 0, err
	}

	var ret int
	err := s.withTxn(func(tx *queries.Queries) error {
		count, err := repealInviteTokens(ctx, tx, input.UserID, input.Amount)
		ret = count

		return err
	})

	return ret, err
}

func (s *User) RequestChangeEmail(ctx context.Context) (models.UserChangeEmailStatus, error) {
	currentUser := auth.GetCurrentUser(ctx)

	// ConfirmOldEmail needs Email (the recipient) — the cached AuthUser only
	// carries auth-relevant fields, so fetch the full row here.
	fullUser, err := s.FindByID(ctx, currentUser.ID)
	if err != nil {
		return models.UserChangeEmailStatusError, err
	}

	err = s.withTxn(func(tx *queries.Queries) error {
		return email.ConfirmOldEmail(ctx, tx, *fullUser, s.emailMgr)
	})

	if err != nil {
		return models.UserChangeEmailStatusError, err
	}
	return models.UserChangeEmailStatusConfirmOld, nil
}

func (s *User) ValidateChangeEmail(ctx context.Context, tokenID uuid.UUID, emailAddr string) (models.UserChangeEmailStatus, error) {
	err := s.withTxn(func(tx *queries.Queries) error {

		token, err := tx.FindUserToken(ctx, tokenID)
		if err != nil {
			return err
		}

		data, err := getUserTokenData(token)
		if err != nil {
			return err
		}

		currentUser := auth.GetCurrentUser(ctx)
		if data.UserID != currentUser.ID {
			return fmt.Errorf("invalid token")
		}

		// ConfirmNewEmail reads .Name for the email template; fetch the full
		// row instead of relying on the slim cached AuthUser.
		fullUser, err := tx.FindUser(ctx, currentUser.ID)
		if err != nil {
			return err
		}
		return email.ConfirmNewEmail(ctx, tx, *converter.UserToModelPtr(fullUser), emailAddr, s.emailMgr)
	})

	if err != nil {
		return models.UserChangeEmailStatusError, err
	}
	return models.UserChangeEmailStatusConfirmNew, nil
}

func (s *User) ConfirmChangeEmail(ctx context.Context, tokenID uuid.UUID) (models.UserChangeEmailStatus, error) {
	err := s.withTxn(func(tx *queries.Queries) error {
		token, err := tx.FindUserToken(ctx, tokenID)
		if err != nil {
			return err
		}

		data, err := getChangeEmailTokenData(token)
		if err != nil {
			return err
		}

		currentUser := auth.GetCurrentUser(ctx)
		if data.UserID != currentUser.ID {
			return fmt.Errorf("invalid token")
		}

		return email.ChangeEmail(ctx, tx, data)
	})

	if err != nil {
		return models.UserChangeEmailStatusError, err
	}
	return models.UserChangeEmailStatusSuccess, nil
}

func (s *User) CreateSystemUsers(ctx context.Context) {
	// if there are no users present, then create a root user with a
	// generated password and api key, outputting them
	var rootPassword string
	var createdUser *models.User

	err := s.withTxn(func(tx *queries.Queries) error {
		_, err := tx.FindUserByName(ctx, rootUserName)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			panic(fmt.Errorf("error getting root user: %w", err))
		}

		if errors.Is(err, pgx.ErrNoRows) {
			rootPassword, err = utils.GenerateRandomPassword(16)
			if err != nil {
				panic(fmt.Errorf("error creating root user: %w", err))
			}
			newUser := models.UserCreateInput{
				Name:     rootUserName,
				Password: rootPassword,
				Email:    unsetEmail,
				Roles:    rootUserRoles,
			}

			dbUser, err := createUser(ctx, tx, newUser, false)
			if err != nil {
				return err
			}
			createdUser = converter.UserToModelPtr(*dbUser)
		}

		_, err = tx.FindUserByName(ctx, modUserName)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			panic(fmt.Errorf("error getting mod user: %w", err))
		}

		if errors.Is(err, pgx.ErrNoRows) {
			password, err := utils.GenerateRandomPassword(32)
			if err != nil {
				panic(fmt.Errorf("error creating root user: %w", err))
			}
			newUser := models.UserCreateInput{
				Name:     modUserName,
				Password: password,
				Email:    "stashbot@example.com",
				Roles:    modUserRoles,
			}

			if _, err = createUser(ctx, tx, newUser, false); err != nil {
				return err
			}
		}

		_, err = tx.FindUserByName(ctx, deletedUserName)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			panic(fmt.Errorf("error getting deleted user: %w", err))
		}

		if errors.Is(err, pgx.ErrNoRows) {
			password, err := utils.GenerateRandomPassword(32)
			if err != nil {
				panic(fmt.Errorf("error creating deleted user: %w", err))
			}
			newUser := models.UserCreateInput{
				Name:     deletedUserName,
				Password: password,
				Email:    deletedUserEmail,
				Roles:    deletedUserRoles,
			}

			if _, err = createUser(ctx, tx, newUser, false); err != nil {
				return err
			}
		}

		return nil
	})

	if err != nil {
		panic(fmt.Errorf("error creating system users: %w", err))
	}

	if createdUser != nil {
		// print (not log) the details of the created user
		// Skip output during tests
		if flag.Lookup("test.v") == nil {
			fmt.Printf("root user has been created.\nUser: %s\nPassword: %s\nAPI Key: %s\n", rootUserName, rootPassword, createdUser.APIKey)
			fmt.Print("These credentials have not been logged. The email should be set and the password should be changed after logging in.\n")
		}
	}
}

// Authenticate validates the provided username and password. If correct, it
// returns the id of the user.
func (s *User) Authenticate(ctx context.Context, username string, password string) (string, error) {
	user, err := s.queries.FindUserByName(ctx, username)
	if err != nil {
		return "", err
	}

	if !isPasswordCorrect(user.PasswordHash, password) {
		return "", ErrAccessDenied
	}

	return user.ID.String(), nil
}
