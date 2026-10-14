package api

import (
	"context"

	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/auth"
	"github.com/stashapp/stash-box/internal/models"
)

// Saved searches are personal data, so every one of these is scoped to the caller: the SERVICE
// takes the user id, not the resolver, and the queries behind it cannot be called without it.
// A resolver that fetched by id and checked ownership afterwards is the shape that leaks once
// somebody adds a second caller.

func (r *queryResolver) SavedSearches(ctx context.Context) ([]models.SavedSearch, error) {
	currentUser := auth.GetCurrentUser(ctx)
	return r.services.SavedSearch().FindByUser(ctx, currentUser.ID)
}

func (r *queryResolver) FindSavedSearch(ctx context.Context, id uuid.UUID) (*models.SavedSearch, error) {
	currentUser := auth.GetCurrentUser(ctx)
	return r.services.SavedSearch().FindForUser(ctx, currentUser.ID, id)
}

func (r *mutationResolver) CreateSavedSearch(ctx context.Context, input models.SavedSearchInput) (*models.SavedSearch, error) {
	currentUser := auth.GetCurrentUser(ctx)
	return r.services.SavedSearch().Create(ctx, currentUser.ID, input)
}

func (r *mutationResolver) UpdateSavedSearch(ctx context.Context, input models.SavedSearchUpdateInput) (*models.SavedSearch, error) {
	currentUser := auth.GetCurrentUser(ctx)
	return r.services.SavedSearch().Update(ctx, currentUser.ID, input)
}

func (r *mutationResolver) DeleteSavedSearch(ctx context.Context, id uuid.UUID) (bool, error) {
	currentUser := auth.GetCurrentUser(ctx)
	return r.services.SavedSearch().Delete(ctx, currentUser.ID, id)
}

// savedSearchResolver resolves what is computed rather than stored.
type savedSearchResolver struct{ *Resolver }

// Results runs the stored query through the SAME path `queryScenes` takes -- parse, resolve,
// lower, builder -- because a saved search with its own query path could answer differently from
// the statement it stores, and the difference would look like an empty result rather than a bug.
//
// The stored text goes in as `query`, so the contract is a live query's contract: a term the
// filter cannot apply returns EMPTY and reports itself in `diagnostics` instead of being
// dropped. A tag merged away since the search was written still resolves, via the redirect
// fallback in internal/service/scene/query_language.go.
func (r *savedSearchResolver) Results(
	ctx context.Context,
	obj *models.SavedSearch,
	page *int,
	perPage *int,
	sort *models.SceneSortEnum,
	direction *models.SortDirectionEnum,
) (*models.SceneQuery, error) {
	input := models.SceneQueryInput{
		Query: &obj.Query,

		// `SceneQueryInput`'s OWN schema defaults, spelled out. A client sending `queryScenes`
		// gets them from gqlgen; an input built here does not, and the zero values are not
		// "unset" -- `page: 0` is a negative offset and `sort: ""` is no sort at all, so leaving
		// them out would run a different query than the same text through `queryScenes`.
		Page:      1,
		PerPage:   25,
		Sort:      models.SceneSortEnumDate,
		Direction: models.SortDirectionEnumDesc,
	}

	// The schema's own defaults are applied by gqlgen for a field with a default value, and
	// these pointers are nil only when a client could not send them; keeping the struct's
	// defaults is right in both cases.
	if page != nil {
		input.Page = *page
	}
	if perPage != nil {
		input.PerPage = *perPage
	}
	if sort != nil {
		input.Sort = *sort
	}
	if direction != nil {
		input.Direction = *direction
	}

	return &models.SceneQuery{
		Filter: input,
	}, nil
}
