package api

import (
	"context"

	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/auth"
	"github.com/stashapp/stash-box/internal/models"
)

// Derived lists: the mutations for a list whose entries are a saved search's current results
// (plan phase 8 slice 8d).
//
// `sourceSavedSearch` has NO resolver here, and that is deliberate rather than an omission: gqlgen maps
// a field with no arguments straight onto the model, so a resolver for it would be dead code. It is
// filled EAGERLY in hydrate, which is where every other computed field on a list already lives -- see
// the note at the top of resolver_list.go, which states the rule this follows.

// listAfterChange re-reads a list a mutation has just altered.
//
// Through the same Get/hydrate path every other read takes, so a mutation's payload cannot show a
// different list from a subsequent query: a derived list's entries must be computed by the code that
// will compute them next time, or the mutation's own response is the one place the feature appears to
// behave differently.
func (r *Resolver) listAfterChange(ctx context.Context, listID uuid.UUID) (*models.List, error) {
	user := auth.GetCurrentUser(ctx)

	row, err := r.services.List().Get(ctx, listID, user.ID)
	if err != nil {
		return nil, mapListError(err)
	}
	m, err := r.listToModel(ctx, row)
	if err != nil {
		return nil, err
	}
	if err := r.hydrate(ctx, m); err != nil {
		return nil, err
	}
	return m, nil
}

func (r *mutationResolver) ListSetSourceSearch(ctx context.Context, input models.ListSourceSearchInput) (*models.List, error) {
	user := auth.GetCurrentUser(ctx)

	if err := r.services.List().SetSourceSearch(ctx, input.ListID, user.ID, input.SavedSearchID); err != nil {
		return nil, mapListError(err)
	}
	return r.listAfterChange(ctx, input.ListID)
}

func (r *mutationResolver) ListClearSourceSearch(ctx context.Context, listID uuid.UUID) (*models.List, error) {
	user := auth.GetCurrentUser(ctx)

	if err := r.services.List().ClearSourceSearch(ctx, listID, user.ID); err != nil {
		return nil, mapListError(err)
	}
	return r.listAfterChange(ctx, listID)
}
