package user

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stashapp/stash-box/internal/auth"
	"github.com/stashapp/stash-box/internal/config"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/pkg/utils"
)

func validateCreate(input models.UserCreateInput) error {
	// username must be set
	err := validateUserName(input.Name, &input.Email)
	if err != nil {
		return err
	}

	// email must be valid
	err = validateUserEmail(input.Email)
	if err != nil {
		return err
	}

	// password must be valid according to policy
	err = validatePassword(input.Name, input.Email, input.Password)
	if err != nil {
		return err
	}

	return nil
}

func validateUpdate(ctx context.Context, input models.UserUpdateInput, current queries.User) error {
	currentName := current.Name
	currentEmail := current.Email

	if currentName == modUserName {
		return ErrChangeModUser
	}

	if currentName == rootUserName {
		if input.Name != nil && *input.Name != rootUserName {
			return ErrChangeRootName
		}

		// TODO - this means that we must include roles in the input
		if len(input.Roles) != len(rootUserRoles) || input.Roles[0] != rootUserRoles[0] {
			return ErrChangeRootRoles
		}
	}

	if input.Name != nil {
		currentName = *input.Name

		err := validateUserName(*input.Name, input.Email)
		if err != nil {
			return err
		}
	}

	// email must be valid
	if input.Email != nil {
		currentEmail = *input.Email
		err := validateUserEmail(*input.Email)
		if err != nil {
			return err
		}
	} else if current.Email == unsetEmail {
		return ErrEmptyEmail
	}

	// password must be valid according to policy
	if input.Password != nil {
		err := validatePassword(currentName, currentEmail, *input.Password)
		if err != nil {
			return err
		}
	}

	if input.Name != nil && *input.Name != current.Name {
		if err := auth.ValidateAdmin(ctx); err != nil {
			return err
		}
	}

	return nil
}

func validateDelete(user queries.User) error {
	if user.Name == rootUserName || user.Name == modUserName || user.Name == deletedUserName {
		return ErrDeleteSystemUser
	}

	return nil
}

func validateUserName(username string, email *string) error {
	if username == "" {
		return ErrEmptyUsername
	}

	// username must not have leading or trailing whitespace
	trimmed := strings.TrimSpace(username)

	if trimmed != username {
		return ErrUsernameHasWhitespace
	}

	if email != nil && *email == trimmed {
		return ErrUsernameMatchesEmail
	}

	return nil
}

func validateUserEmail(emailAddr string) error {
	if emailAddr == "" {
		return ErrEmptyEmail
	}

	// email must not have leading or trailing whitespace
	trimmed := strings.TrimSpace(emailAddr)

	if trimmed != emailAddr {
		return ErrEmailHasWhitespace
	}

	// from https://stackoverflow.com/a/201378
	const emailRegex = "(?:[a-z0-9!#$%&'*+/=?^_`{|}~-]+(?:\\.[a-z0-9!#$%&'*+/=?^_`{|}~-]+)*|\"(?:[\x01-\x08\x0b\x0c\x0e-\x1f\x21\x23-\x5b\x5d-\x7f]|\\[\x01-\x09\x0b\x0c\x0e-\x7f])*\")@(?:(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\\.)+[a-z0-9](?:[a-z0-9-]*[a-z0-9])?|\\[(?:(?:(2(5[0-5]|[0-4][0-9])|1[0-9][0-9]|[1-9]?[0-9]))\\.){3}(?:(2(5[0-5]|[0-4][0-9])|1[0-9][0-9]|[1-9]?[0-9])|[a-z0-9-]*[a-z0-9]:(?:[\x01-\x08\x0b\x0c\x0e-\x1f\x21-\x5a\x53-\x7f]|\\[\x01-\x09\x0b\x0c\x0e-\x7f])+)\\])"
	re := regexp.MustCompile(emailRegex)

	if !re.MatchString(emailAddr) {
		return ErrInvalidEmail
	}

	return nil
}

func countUniqueCharacters(str string) int {
	chars := make(map[rune]bool)
	for _, r := range str {
		chars[r] = true
	}

	return len(chars)
}

func validatePassword(username string, emailAddr string, password string) error {
	// TODO - hardcode these policies for now. We may want to make these
	// configurable in future

	if len(password) < minPasswordLength {
		return ErrPasswordTooShort
	}

	if len(password) > maxPasswordLength {
		return ErrPasswordTooLong
	}

	if countUniqueCharacters(password) < minUniqueChars {
		return ErrPasswordInsufficientUniqueChars
	}

	// ensure password doesn't match the top 10,000 passwords over the minimum length
	if utils.IsBannedPassword(password) {
		return ErrBannedPassword
	}

	// ensure password doesn't match the username or email
	if password == username {
		return ErrPasswordUsername
	}

	if password == emailAddr {
		return ErrPasswordEmail
	}

	return nil
}

func validateExistingEmail(ctx context.Context, tx *queries.Queries, emailAddr string) error {
	// Check if a user with this email already exists
	_, err := tx.FindUserByEmail(ctx, emailAddr)
	if errors.Is(err, pgx.ErrNoRows) {
		// No existing user, now check for pending activations
		tokens, err := tx.FindUserTokensByEmail(ctx, emailAddr)
		if err != nil {
			return err
		}

		// Filter to only new user tokens (pending activations)
		for _, token := range tokens {
			if token.Type == models.UserTokenTypeNewUser {
				return errors.New("email already has a pending activation")
			}
		}

		return nil
	}

	if err != nil {
		return err
	}

	return errors.New("email already in use")
}

// validateInviteKey refuses a signup whose key is not a usable key.
//
// TWO HALVES, AND THEY BELONG ON DIFFERENT SIDES OF THE FLAG. This function used to wrap all of
// itself in `if config.GetRequireInvite()`, which conflated them:
//
//   - WHETHER A KEY IS REQUIRED is policy about admission, and it is the flag's business: an
//     instance that requires invites refuses a signup with none, and one that does not require them
//     accepts it.
//   - WHETHER THE KEY YOU WERE HANDED IS ANY GOOD is a fact about the key, and it is equally true
//     in both configurations. Gating it on the flag meant that on an instance that does not require
//     invites -- the default is the opposite, but the configuration exists and is a supported one --
//     a key that had already signed somebody up could sign up the next, and an EXPIRY was enforced
//     nowhere. (Measured: with the flag off, a `uses = 1` key took a second signup, and a key with
//     `expire_time` a day in the past was accepted.)
//
// A key that does not resolve is refused rather than ignored, and that is the ONE case where the
// two halves interact: a spent key is DELETED once its last use is consumed at activation, so "no
// such key" and "this key is used up" are the same state -- and previously it leaked out as
// "no rows in result set", a driver error a member cannot act on and an operator cannot read.
func validateInviteKey(ctx context.Context, tx *queries.Queries, inviteKey *uuid.UUID) error {
	if inviteKey == nil {
		if config.GetRequireInvite() {
			return errors.New("invite key required")
		}
		return nil
	}

	key, err := tx.FindInviteKey(ctx, *inviteKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrInvalidInviteKey
	}
	if err != nil {
		return err
	}

	// ensure invite key is not expired
	if key.ExpireTime != nil && key.ExpireTime.Before(time.Now()) {
		return errors.New("invite key expired")
	}

	// A USE IS CONSUMED AT ACTIVATION, so a key with uses left may already have all of them
	// committed by signups that have not activated yet. The pending tokens are what "already
	// promised" looks like, and a key with one use and one pending activation has none left.
	tokens, err := tx.FindUserTokensByInviteKey(ctx, *inviteKey)
	if err != nil {
		return err
	}
	if key.Uses != nil && len(tokens) >= int(*key.Uses) {
		return errors.New("invite key used")
	}

	return nil
}
