// Collaborative lists at the wire (plan phase 8 slice 8e).
//
// This file holds the two FIELD resolvers the List type gained -- the write set and whether the asker
// may write -- and the two mutations that change the set. They live here rather than in
// resolver_list.go because they are one feature (a list's write set) and because the field resolvers
// need to be declared in gqlgen.yml to exist at all.
package api

import (
	"context"

	"github.com/stashapp/stash-box/internal/auth"
	"github.com/stashapp/stash-box/internal/models"
)

// List returns the field resolver for the List type.
//
// The accessor is what makes gqlgen call the methods below; without it the schema would answer with
// the generated STRUCT FIELDS, which for these two are never filled -- and an unfilled
// `collaborators: [User!]!` renders as an empty list, which is a valid-looking answer to a question
// nobody asked. That failure is silent at compile time and silent in Go tests, which is why the
// integration test asserts the SET and not merely "no error".
func (r *Resolver) List() models.ListResolver {
	return &listResolver{r}
}

// listResolver is the field resolver for the List type.
type listResolver struct{ *Resolver }

// Collaborators is who may change this list's contents, besides its owner.
//
// The read rule is the service's (owner or member, and nobody else -- a reader of a published list
// gets ErrNotFound, which mapListError renders as "no such list"). The effect at the wire is that the
// field answers for exactly the people for whom `canEdit` can be true, and a client cannot use either
// one to learn about somebody else's list.
func (r *listResolver) Collaborators(ctx context.Context, obj *models.List) ([]models.User, error) {
	if obj == nil {
		return nil, nil
	}
	viewer := auth.GetCurrentUser(ctx)

	rows, err := r.services.List().Collaborators(ctx, obj.ID, viewer.ID)
	if err != nil {
		return nil, mapListError(err)
	}

	// A slice that is non-nil even when empty: the schema says [User!]!, and gqlgen renders nil as
	// null, so an owner with no collaborators would fail the query's own type contract.
	out := make([]models.User, 0, len(rows))
	for i := range rows {
		// A row whose member is gone cannot exist: the foreign key CASCADEs on user delete. The
		// error is still returned rather than skipped, because "I could not list them" and "there is
		// nobody" must not look the same -- the same reasoning as the audit trail's null actor, in
		// the opposite direction: there, absence IS the fact being recorded.
		member, err := r.services.User().FindByID(ctx, rows[i].UserID)
		if err != nil {
			return nil, err
		}
		out = append(out, *member)
	}
	return out, nil
}

// CanEdit answers whether the person asking may write this list's contents.
//
// It exists because the client CANNOT compute the answer: the owner half is readable from `owner`, and
// the collaborator half is the field above, which a client can only read if it is already a
// collaborator. Without this a UI would either withhold the editing controls from a collaborator who
// may write, or offer them to a reader who may not.
//
// A refusal is `false`, not an error: "you may not write this" is exactly the fact the field exists to
// state. (A list that does not exist, or that this viewer may not see, still reports not-found -- that
// is a different question, answered by the parent query.)
func (r *listResolver) CanEdit(ctx context.Context, obj *models.List) (bool, error) {
	if obj == nil {
		return false, nil
	}
	viewer := auth.GetCurrentUser(ctx)

	can, err := r.services.List().CanEdit(ctx, obj.ID, viewer.ID)
	if err != nil {
		return false, mapListError(err)
	}
	return can, nil
}

// ListAddCollaborator adds a member to a list's write set.
//
// Returns Boolean rather than the list: the act changes the SET, not the list, and a client that wants
// to re-render the set asks for `collaborators` -- one query, which it needs anyway to show the new
// name.
func (r *mutationResolver) ListAddCollaborator(ctx context.Context, input models.ListCollaboratorInput) (bool, error) {
	viewer := auth.GetCurrentUser(ctx)

	if err := r.services.List().AddCollaborator(ctx, input.ListID, input.UserID, viewer.ID); err != nil {
		return false, mapListError(err)
	}
	return true, nil
}

// ListRemoveCollaborator removes a member from a list's write set.
//
// The owner may remove anybody; a member may remove THEMSELVES. The service decides which of those two
// the caller is, so this cannot drift from the rule by being restated here.
func (r *mutationResolver) ListRemoveCollaborator(ctx context.Context, input models.ListCollaboratorInput) (bool, error) {
	viewer := auth.GetCurrentUser(ctx)

	if err := r.services.List().RemoveCollaborator(ctx, input.ListID, input.UserID, viewer.ID); err != nil {
		return false, mapListError(err)
	}
	return true, nil
}
