package api

import (
	"context"
	"errors"

	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/auth"
	"github.com/stashapp/stash-box/internal/dataloader"
	"github.com/stashapp/stash-box/internal/models"
)

// ErrAppealNeedsAMember refuses an appeal action with no authenticated actor.
//
// Both mutations need a person, and the reasons are different: an appeal with no author could be decided by
// anybody without matching the identity rule, and a decision has to be attributable to a moderator who is
// not one of the two people involved. The transport authenticates /graphql, so this is a guard against a
// future unauthenticated path rather than a live branch -- which is exactly the kind of guard that is only
// ever exercised by the test that plants the condition.
var ErrAppealNeedsAMember = errors.New("an appeal needs a signed-in member")

// The appeal's API surface: one mutation any member may run, the decision only an operator may make, and
// the queue that decision is made from (migration 141, spec §9).
//
// THE ASYMMETRY IS THE POINT. Filing is gated on being signed in and nothing else -- an operator cannot
// know whose submission an address was, and asking an appellant to prove ownership would turn a complaint
// about a block into a claim about a person. Deciding is ADMIN, matching `takedownContentUrl`: the same
// authority answers for the decision and for the objection to it. What neither directive can express is
// the rule the service enforces -- the decider is neither the operator who recorded the block nor the
// member who filed the appeal -- because that is an identity, not a role.

// FileContentAppeal records a contest against a refusal.
//
// The actuator is the CURRENT USER, and a missing one is refused here rather than written as a null: an
// appeal whose author is unknown could be decided by anybody without matching the identity rule, which is
// the one thing this table exists to prevent. (The transport authenticates /graphql, so this is a guard
// against a future unauthenticated path rather than a live branch.)
func (r *mutationResolver) FileContentAppeal(ctx context.Context, input models.FileContentSourceAppealInput) (*models.ContentSourceAppeal, error) {
	user := auth.GetCurrentUser(ctx)
	if user.ID == uuid.Nil {
		return nil, ErrAppealNeedsAMember
	}

	appeal, err := r.services.Content().FileAppeal(ctx, input.Kind, input.Value, input.Statement, user.ID)
	if err != nil {
		return nil, err
	}
	return &appeal, nil
}

// DecideContentAppeal grants or denies an appeal, once, by somebody other than the people involved.
//
// The outcome is passed through as the string the schema documents ("granted" or "denied") and validated
// in the service, so the closed vocabulary lives in one place; `note` is optional, and an operator who
// skips it is choosing not to write down a reason, which is their call to make and to live with.
func (r *mutationResolver) DecideContentAppeal(ctx context.Context, id uuid.UUID, outcome string, note *string) (*models.ContentSourceAppeal, error) {
	user := auth.GetCurrentUser(ctx)
	if user.ID == uuid.Nil {
		return nil, ErrAppealNeedsAMember
	}

	noteValue := ""
	if note != nil {
		noteValue = *note
	}

	appeal, err := r.services.Content().DecideAppeal(ctx, id, outcome, noteValue, user.ID)
	if err != nil {
		return nil, err
	}
	return &appeal, nil
}

// ContentAppeals lists appeals, oldest first, pending by default.
//
// The defaults live here rather than with the caller for the reason the block list's do: the honest answer
// to "what is waiting on us" is the whole queue, and an instance accumulates a handful of appeals rather
// than thousands -- a caller who had to know that would otherwise be inventing a page size to see it.
func (r *queryResolver) ContentAppeals(ctx context.Context, pendingOnly bool, limit *int, offset *int) ([]models.ContentSourceAppeal, error) {
	limitValue := int32(100)
	if limit != nil {
		limitValue = int32(*limit)
	}
	offsetValue := int32(0)
	if offset != nil {
		offsetValue = int32(*offset)
	}
	return r.services.Content().Appeals(ctx, pendingOnly, limitValue, offsetValue)
}

// ContentSourceAppeal hands gqlgen the resolver for the appeal type's derived fields.
func (r *Resolver) ContentSourceAppeal() models.ContentSourceAppealResolver {
	return &contentSourceAppealResolver{r}
}

type contentSourceAppealResolver struct{ *Resolver }

// ID converts the model's id to the scalar's Go type, like every other ID on this schema.
func (r *contentSourceAppealResolver) ID(ctx context.Context, obj *models.ContentSourceAppeal) (uuid.UUID, error) {
	return uuid.UUID(obj.ID), nil
}

// BlockID is the block this contested, and it goes NULL once that block is released.
//
// Returned as a POINTER rather than the zero uuid because the null is meaningful: the appeal outlives the
// block it names (the column is ON DELETE SET NULL), and a client that saw the nil uuid instead would be
// told the appeal names block 00000000-0000-0000-0000-000000000000 rather than that the block is gone.
func (r *contentSourceAppealResolver) BlockID(ctx context.Context, obj *models.ContentSourceAppeal) (*uuid.UUID, error) {
	if obj.BlockID == models.ID(uuid.Nil) {
		return nil, nil
	}
	id := uuid.UUID(obj.BlockID)
	return &id, nil
}

// FiledBy is who appealed. Null only when that account has since been deleted.
func (r *contentSourceAppealResolver) FiledBy(ctx context.Context, obj *models.ContentSourceAppeal) (*models.User, error) {
	return appealUser(ctx, obj.FiledByID)
}

// BlockedBy is who recorded the block being contested.
//
// This is the field the different-moderator rule compares against, so a reader can see for themselves why
// an operator was refused the decision, rather than taking the refusal's word for it.
func (r *contentSourceAppealResolver) BlockedBy(ctx context.Context, obj *models.ContentSourceAppeal) (*models.User, error) {
	return appealUser(ctx, obj.BlockedByID)
}

// DecidedBy is the moderator who answered it, null while pending.
func (r *contentSourceAppealResolver) DecidedBy(ctx context.Context, obj *models.ContentSourceAppeal) (*models.User, error) {
	return appealUser(ctx, obj.DecidedByID)
}

// NO RESOLVERS FOR outcome, decision_note OR decided_at, and that is the fix rather than an omission.
//
// The first version of this file had one method per nullable decision field, reconstructing each null from
// the model's zero value. They were NEVER CALLED: gqlgen generates a resolver only for a field whose model
// type does not already match the schema, so a plain `string` bound straight through and the three methods
// were dead code -- which Go does not complain about, and which every test here passed, because the tests
// read those fields through non-pointer structs of their own. The live walk found it in one verdict: a
// pending appeal came back with `"outcome": ""`. The nulls now live in `models.ContentSourceAppeal` as
// pointers, so there is no method to forget to call.

// appealUser loads a user reference by id, or nil when there is none.
//
// Bulk-loaded through the dataloader like every other user reference on a list, so a queue of appeals is
// one query for the appellants rather than one per row.
func appealUser(ctx context.Context, id models.ID) (*models.User, error) {
	if id == models.ID(uuid.Nil) {
		return nil, nil
	}
	return dataloader.For(ctx).UserByID.Load(uuid.UUID(id))
}
