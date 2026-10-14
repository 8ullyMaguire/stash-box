package api

import (
	"context"
	"encoding/json"

	"github.com/stashapp/stash-box/internal/auth"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/service/sessionstate"
)

// Session-state resolvers: "where I left off" (feature-discovery SPEC section 11, phase 7
// slice 7).
//
// EVERY OPERATION IS SCOPED TO THE AUTHENTICATED USER, with no id argument anywhere: a cursor is
// one person's position, and an api that let a caller name another user would make the local-first
// promise unenforceable at exactly the layer that has to enforce it.
//
// The cursor crosses this boundary as a STRING and is stored as JSON: the server checks that it is
// well-formed and bounded and never looks inside it, which is what keeps a client free to change
// what a cursor means without a server release.

// SessionState returns the synced cursor for (kind, key), or null.
func (r *queryResolver) SessionState(ctx context.Context, kind string, key string) (*models.SessionState, error) {
	currentUser := auth.GetCurrentUser(ctx)
	state, err := r.services.SessionState().Get(ctx, currentUser.ID, kind, key)
	if err != nil {
		return nil, err
	}
	if state == nil {
		return nil, nil
	}
	return sessionStateModel(*state), nil
}

// SessionStates returns everything this user syncs for one kind of surface.
func (r *queryResolver) SessionStates(ctx context.Context, kind string) ([]models.SessionState, error) {
	currentUser := auth.GetCurrentUser(ctx)
	states, err := r.services.SessionState().ListByKind(ctx, currentUser.ID, kind)
	if err != nil {
		return nil, err
	}
	out := make([]models.SessionState, 0, len(states))
	for _, state := range states {
		out = append(out, *sessionStateModel(state))
	}
	return out, nil
}

// SaveSessionState stores a cursor for this user.
//
// A LOCAL SAVE STORES NO PAYLOAD. The cursor is still passed through to the service rather than
// dropped here, so the rule lives in exactly one place -- and so a client that sends the state
// anyway with `storage: LOCAL` gets the same result as one that sends nothing, which is the
// observable meaning of "this stays on my device".
func (r *mutationResolver) SaveSessionState(ctx context.Context, input models.SessionStateInput) (*models.SessionState, error) {
	currentUser := auth.GetCurrentUser(ctx)

	storage := storedStorage(input.Storage)
	var cursor json.RawMessage
	if input.Cursor != nil {
		cursor = json.RawMessage(*input.Cursor)
	}

	state, err := r.services.SessionState().Save(ctx, currentUser.ID, input.Kind, input.Key, cursor, storage)
	if err != nil {
		return nil, err
	}
	return sessionStateModel(*state), nil
}

// storedStorage maps the GraphQL enum to the value the COLUMN holds, and graphqlStorage maps it
// back. BOTH DIRECTIONS EXIST BECAUSE BOTH ARE NEEDED and neither is a conversion: the enum is
// upper case by GraphQL convention and the column is lower case to match the CHECK in migration
// 116, so `string(...)` in either direction is wrong -- the naive cast sends 'LOCAL' to a
// constraint that accepts 'local', and returns 'local' as an enum value that does not exist.
// This is the second enum in this slice with two spellings; the integration tests catch it only
// because they read the value back.
func storedStorage(storage models.SessionStorage) sessionstate.Storage {
	switch storage {
	case models.SessionStorageSynced:
		return sessionstate.StorageSynced
	default:
		// LOCAL, and anything else: the service refuses an unknown storage rather than
		// defaulting, so the default arm here can only be reached by a value we cannot name,
		// which the schema's closed enum makes unreachable from a client.
		return sessionstate.StorageLocal
	}
}

func graphqlStorage(storage sessionstate.Storage) models.SessionStorage {
	switch storage {
	case sessionstate.StorageSynced:
		return models.SessionStorageSynced
	default:
		return models.SessionStorageLocal
	}
}

// ClearSessionState forgets a cursor. False means there was nothing there, which is not an error.
func (r *mutationResolver) ClearSessionState(ctx context.Context, input models.ClearSessionStateInput) (bool, error) {
	currentUser := auth.GetCurrentUser(ctx)
	return r.services.SessionState().Clear(ctx, currentUser.ID, input.Kind, input.Key)
}

// sessionStateModel renders a stored state for GraphQL.
//
// `updatedAt` is set from the row rather than left zero for a local write: a client that just
// saved needs to know whether the server recorded the choice, and the timestamp is how it knows
// the answer is about its own write rather than a row from last week. A local row's cursor is
// empty in the model, and the schema documents null there as MEANING "kept on the device".
func sessionStateModel(state sessionstate.State) *models.SessionState {
	out := &models.SessionState{
		Kind:    state.Kind,
		Key:     state.Key,
		Storage: graphqlStorage(state.Storage),
	}
	if len(state.Cursor) > 0 {
		cursor := string(state.Cursor)
		out.Cursor = &cursor
	}
	if !state.UpdatedAt.IsZero() {
		updated := state.UpdatedAt
		out.UpdatedAt = &updated
	}
	return out
}
