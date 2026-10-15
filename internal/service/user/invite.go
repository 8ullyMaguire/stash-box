package user

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/invite"
	"github.com/stashapp/stash-box/internal/service/trust"
)

// ErrNoInviteTokens is the refusal a member gets when neither their level's key
// slots nor an operator's grant can pay for a key. It is the phase-11 package's
// own value, re-exported here because this package is where callers have always
// looked for it: one definition, so the two cannot drift.
var ErrNoInviteTokens = invite.ErrNoInviteTokens

// ErrLevelAllocationExhausted is the refusal a member gets when their level
// grants them slots and all of them are in use.
var ErrLevelAllocationExhausted = invite.ErrLevelAllocationExhausted

var ErrInvalidInviteKey = errors.New("invalid or expired invite key")

type Finder interface {
	Find(id uuid.UUID) (*models.User, error)
}

type FinderUpdater interface {
	Finder
	UpdateFull(updatedUser models.User) (*models.User, error)
}

// GrantInviteTokens increments the invite token count for a user by up to 10.
func grantInviteTokens(ctx context.Context, tx *queries.Queries, userID uuid.UUID, tokens int) (int, error) {
	u, err := tx.FindUser(ctx, userID)

	if err != nil {
		return 0, err
	}

	// don't accept negative numbers
	if tokens < 0 {
		return int(u.InviteTokens), nil
	}

	// put a sensible limit on the number of tokens that can be granted at a time
	const maxTokens = 10
	if tokens > maxTokens {
		tokens = maxTokens
	}

	u.InviteTokens += tokens

	err = tx.UpdateUserInviteTokenCount(ctx, queries.UpdateUserInviteTokenCountParams{
		ID:           u.ID,
		InviteTokens: u.InviteTokens,
	})

	return int(u.InviteTokens), err
}

// RepealInviteTokens decrements a user's invite token count by the provided
// amount. Invite tokens are constrained to a minimum of 0.
func repealInviteTokens(ctx context.Context, tx *queries.Queries, userID uuid.UUID, tokens int) (int, error) {
	u, err := tx.FindUser(ctx, userID)

	if err != nil {
		return 0, err
	}

	// don't accept negative numbers
	if tokens < 0 {
		return int(u.InviteTokens), nil
	}

	// no limit on the tokens to repeal
	u.InviteTokens -= tokens

	// don't allow to go negative
	if u.InviteTokens < 0 {
		u.InviteTokens = 0
	}

	err = tx.UpdateUserInviteTokenCount(ctx, queries.UpdateUserInviteTokenCountParams{
		ID:           u.ID,
		InviteTokens: u.InviteTokens,
	})

	return int(u.InviteTokens), err
}

// allowanceFor reads the two facts an allowance is made of: the member's LEVEL,
// and how many keys they hold USABLE right now.
//
// Both come from an existing single implementation rather than from a new rule.
// The level is the trust service's answer, because it owns the one curve that
// decides what level anyone is -- a second reading of user_trust here would be a
// second answer to that question. The count comes from
// FindActiveInviteKeysForUser, the same query behind the `invite_codes` field,
// which already excludes keys that expired and keys whose uses were consumed by
// live user_tokens rows: "usable" therefore has one definition, and an expired
// or consumed key frees its slot because it stops being counted, not because
// anything cleans it up.
func allowanceFor(ctx context.Context, tx *queries.Queries, trustSvc *trust.Trust, userID uuid.UUID, exempt bool) (invite.Allowance, error) {
	level, err := trustSvc.Level(ctx, userID)
	if err != nil {
		return invite.Allowance{}, err
	}

	active, err := tx.FindActiveInviteKeysForUser(ctx, userID)
	if err != nil {
		return invite.Allowance{}, err
	}

	u, err := tx.FindUser(ctx, userID)
	if err != nil {
		return invite.Allowance{}, err
	}

	return invite.Allowance{
		Level:      level,
		ActiveKeys: len(active),
		Granted:    int(u.InviteTokens),
		Exempt:     exempt,
	}, nil
}

// InviteAllowance is what a member (or their client) may ask about their own
// invites: the level the allowance comes from, the slots that level grants, how
// many are in use, the operator's grant, and whether their role exempts them.
//
// It is derived per call from the same two facts generateInviteKeys decides on,
// so a member cannot be shown an allowance that disagrees with the refusal the
// mutation would give them.
func (s *User) InviteAllowance(ctx context.Context, userID uuid.UUID, exempt bool) (invite.Allowance, error) {
	return allowanceFor(ctx, s.queries, s.trust, userID, exempt)
}

// generateInviteKeys creates and returns invite keys, within the member's
// allowance.
//
// The allowance is re-read INSIDE the loop, which is what makes a batch obey the
// cap: the key minted in the previous iteration is counted by the next check, so
// asking for four keys with one slot left mints one and then refuses. The
// existing transaction makes that refusal all-or-nothing rather than leaving a
// partial batch behind.
//
// The 50-key clamp is a ceiling on a single request, not the rule: the allowance
// is what decides, and it is smaller for every level but Steward.
func generateInviteKeys(ctx context.Context, tx *queries.Queries, trustSvc *trust.Trust, userID uuid.UUID, input *models.GenerateInviteCodeInput, exempt bool) ([]uuid.UUID, error) {
	keys := 1
	if input.Keys != nil {
		keys = *input.Keys
	}

	// don't allow more than 50 keys to be generated at a time
	if keys > 50 {
		keys = 50
	}

	var ret []uuid.UUID

	for i := 0; i < keys; i++ {
		allowance, err := allowanceFor(ctx, tx, trustSvc, userID, exempt)
		if err != nil {
			return nil, err
		}

		decision := allowance.Decide()
		if !decision.Allowed {
			return nil, decision.Refusal
		}

		// Only a grant is spent, and only when the level had no free slot: a
		// member who earned the key must not pay an operator's grant for it.
		if decision.SpendsToken {
			if _, err := repealInviteTokens(ctx, tx, userID, 1); err != nil {
				return nil, err
			}
		}

		// create the invite key
		UUID, err := uuid.NewV4()
		if err != nil {
			return nil, err
		}

		newKey := queries.CreateInviteKeyParams{
			ID:          UUID,
			GeneratedBy: userID,
		}

		if input != nil {
			if input.Uses != nil && *input.Uses > 0 {
				uses := *input.Uses
				newKey.Uses = &uses
			}
			if input.TTL != nil {
				expires := time.Now().Add(time.Duration(*input.TTL) * time.Second)
				newKey.ExpireTime = &expires
			}
		}

		key, err := tx.CreateInviteKey(ctx, newKey)
		if err != nil {
			return nil, err
		}

		ret = append(ret, key.ID)
	}

	return ret, nil
}

// RescindInviteKey makes an invite key invalid, refunding the invite token if
// one was spent on it.
//
// The refund is conditional on the key never having been activated, which is the
// half of this function that was missing: an unconditional refund lets a key
// that brought a real member in be rescinded for a second token, and with a
// level-derived cap in place that is not untidiness -- it is the cap's hole,
// because the rescind would buy a slot it never freed.
//
// "Never activated" is answered by FindUserTokensByInviteKey, which returns
// EVERY token ever minted for the key regardless of expiry: an activation whose
// token has since expired still signed a person up, so it is still not a refund.
func rescindInviteKey(ctx context.Context, tx *queries.Queries, key uuid.UUID, userID uuid.UUID, refundToken bool) error {
	// ensure userID matches that of the invite key
	k, err := tx.FindInviteKey(ctx, key)
	if err != nil {
		return err
	}

	if k.GeneratedBy != userID {
		return fmt.Errorf("invalid key")
	}

	activated := true
	if refundToken {
		tokens, err := tx.FindUserTokensByInviteKey(ctx, key)
		if err != nil {
			return err
		}
		activated = len(tokens) > 0
	}

	// destroy the key
	err = tx.DeleteInviteKey(ctx, key)
	if err != nil {
		return err
	}

	// refund the invite token if it was spent and the key was never activated
	if refundToken && !activated {
		_, err := tx.FindUser(ctx, userID)
		if err != nil {
			return err
		}

		_, err = grantInviteTokens(ctx, tx, userID, 1)
		if err != nil {
			return err
		}
	}

	return nil
}
