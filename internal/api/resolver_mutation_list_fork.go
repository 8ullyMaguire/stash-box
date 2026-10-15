package api

import (
	"context"

	"github.com/stashapp/stash-box/internal/auth"
	"github.com/stashapp/stash-box/internal/models"
)

// Forking a list (plan phase 8 slice 8c).
func (r *mutationResolver) ListFork(ctx context.Context, input models.ListForkInput) (*models.List, error) {
	user := auth.GetCurrentUser(ctx)

	// No description argument exists on ListForkInput: a fork is "that list, mine", so it inherits
	// the source's description rather than making the forker retype it. A caller who wants a
	// different one edits the fork afterwards, through the same audited path as any other edit.
	row, err := r.services.List().Fork(ctx, input.ID, user.ID, input.Name, "")
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
