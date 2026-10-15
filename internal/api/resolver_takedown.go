package api

import (
	"context"

	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/auth"
	"github.com/stashapp/stash-box/internal/dataloader"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/service/content"
)

// The takedown path's API surface: the three mutations an operator answers a notice with, and the
// list that says what the instance refuses (migration 134).
//
// EVERY ONE OF THESE IS ADMIN, and the reason is the shape of the thing rather than secrecy about a
// list. A takedown is the instance's response to a legal complaint; a host block is a decision that
// a whole site may not be pointed at; releasing either undoes that. Moderators decide about EDITS,
// which is a question about this database's own content, and these are decisions about other
// people's. The `@hasRole(role: ADMIN)` directive is the gate; there is no second check inside,
// because two checks for one rule is two places for the rule to say different things.

// TakedownContentURL takes down every record of a URL and refuses it from now on.
//
// The URL is normalized inside the service rather than here, so a notice can be pasted verbatim and
// the key it produces is the pipeline's own (pkg/urlkey). A URL that cannot be keyed at all comes
// back as an error naming the normalizer's reason: there is nothing to block and nothing to match.
func (r *mutationResolver) TakedownContentURL(ctx context.Context, input models.TakedownContentURLInput) (*models.ContentTakedownResult, error) {
	user := auth.GetCurrentUser(ctx)

	takedown, err := r.services.Content().TakedownURL(ctx, input.URL, user.ID, input.Reference)
	if err != nil {
		return nil, err
	}
	return takedownResult(takedown), nil
}

// BlockContentHost refuses a whole host and takes down every source on it.
//
// The host is parsed out of whatever the report gave the operator, URL or bare host, by the same
// function the pipeline uses; a string with no host in it at all is refused rather than stored,
// because a block on "" would match nothing and read as applied.
func (r *mutationResolver) BlockContentHost(ctx context.Context, input models.BlockContentHostInput) (*models.ContentTakedownResult, error) {
	user := auth.GetCurrentUser(ctx)

	takedown, err := r.services.Content().BlockHost(ctx, input.Host, user.ID, input.Reason)
	if err != nil {
		return nil, err
	}
	return takedownResult(takedown), nil
}

// ReleaseContentBlock stops refusing a key, reporting whether there was one.
//
// The boolean is not decoration: "released a block" and "there was nothing to release" are different
// answers to the operator's question, and the second one usually means they are looking at the wrong
// instance or the wrong key.
func (r *mutationResolver) ReleaseContentBlock(ctx context.Context, kind string, value string) (bool, error) {
	user := auth.GetCurrentUser(ctx)
	return r.services.Content().ReleaseBlock(ctx, kind, value, user.ID)
}

// ContentSourceBlocks lists what this instance currently refuses, newest first.
//
// The defaults are here rather than left to the caller because the honest answer to "what do we
// block?" is the whole list: an instance accumulates a handful of these, and a caller who has to
// know that would otherwise ask for a page size to see all of it.
func (r *queryResolver) ContentSourceBlocks(ctx context.Context, limit *int, offset *int) ([]models.ContentSourceBlock, error) {
	limitValue := int32(100)
	if limit != nil {
		limitValue = int32(*limit)
	}
	offsetValue := int32(0)
	if offset != nil {
		offsetValue = int32(*offset)
	}

	return r.services.Content().Blocks(ctx, limitValue, offsetValue)
}

func takedownResult(takedown content.Takedown) *models.ContentTakedownResult {
	block := takedown.Block
	return &models.ContentTakedownResult{
		Block:          &block,
		Changed:        len(takedown.Sources),
		Sources:        takedown.Sources,
		AlreadyBlocked: takedown.AlreadyBlocked,
	}
}

type contentSourceBlockResolver struct{ *Resolver }

// ID converts the model's id to the scalar's Go type.
//
// gqlgen asks for this as a resolver rather than binding the field, because the model carries
// `models.ID` while the scalar's Go type is `uuid.UUID` -- the same conversion
// `contentSourceResolver.ID` does, and for the same reason: the model does not have to know how the
// scalar is wired.
func (r *contentSourceBlockResolver) ID(ctx context.Context, obj *models.ContentSourceBlock) (uuid.UUID, error) {
	return uuid.UUID(obj.ID), nil
}

// CreatedBy resolves the operator behind a decision.
//
// Bulk-loaded like every other user reference on a list (dataloader), so a page of blocks is one
// query rather than one per row. Null when the author was deleted: the block survives them, which is
// deliberate (the column is ON DELETE SET NULL), and a resolver that invented a user here would be
// attributing a decision to somebody who never made it.
func (r *contentSourceBlockResolver) CreatedBy(ctx context.Context, obj *models.ContentSourceBlock) (*models.User, error) {
	if obj.CreatedByID == models.ID(uuid.Nil) {
		return nil, nil
	}
	return dataloader.For(ctx).UserByID.Load(uuid.UUID(obj.CreatedByID))
}
