// Package sessionstate stores "where I left off" cursors (discovery SPEC section 11, aggregator
// phase 7 slice 7).
//
// LOCAL-FIRST, AND THE SCHEMA ENFORCES IT (migration 116): a `local` row holds NO payload, and
// this service cannot give it one -- the insert writes NULL for a local write whatever the caller
// sent, and the table's CHECK rejects anything else. So "nothing leaves the device unless the user
// turned sync on" is a property of the data rather than a promise about the code path, and the
// promise survives an edit here.
//
// THE CURSOR IS OPAQUE. The client decides what is in it; the server validates that it is JSON,
// bounds its size, stores it and hands it back. Interpreting it would make every client change a
// server change, which is the opposite of what a cursor is for.
//
// OPAQUE IS ABOUT MEANING, NOT BYTES: the column is jsonb, so the VALUE is preserved and the text
// is Postgres's -- whitespace and key order may differ from what the client sent.
package sessionstate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/errutil"
)

// Storage is where the state lives. 'local' is the DEFAULT: a user has to ask for their position
// to leave their device.
type Storage string

const (
	// StorageLocal means the browser keeps it. The server records the CHOICE and no payload.
	StorageLocal Storage = "local"
	// StorageSynced means a second device may continue where this one stopped.
	StorageSynced Storage = "synced"
)

// Bounds. The same limits the columns have, applied here so a caller gets a message rather than a
// constraint violation, and applied to the JSON because a cursor is small by nature and an
// unbounded column is how a "cursor" becomes a file upload.
const (
	MaxKindLength  = 32
	MaxKeyLength   = 128
	MaxCursorBytes = 4096
)

var (
	// ErrUnknownStorage is returned for a storage value the schema would reject. Refused rather
	// than defaulted: a typo'd 'sync' silently becoming 'local' would look like sync not working,
	// and silently becoming 'synced' would upload state a user meant to keep back.
	ErrUnknownStorage = errors.New("unknown session storage")

	// ErrKindTooLong and ErrKeyTooLong exist because the columns have caps and a caller deserves
	// the reason rather than a constraint name.
	ErrKindTooLong = fmt.Errorf("kind is longer than %d characters", MaxKindLength)
	ErrKeyTooLong  = fmt.Errorf("key is longer than %d characters", MaxKeyLength)

	// ErrEmptyKind and ErrEmptyKey: an empty key is not a cursor, it is a bug at the call site,
	// and storing it would create a row no reader asks for.
	ErrEmptyKind = errors.New("kind is required")
	ErrEmptyKey  = errors.New("key is required")

	// ErrCursorInvalid is returned when a cursor that is being SYNCED is not valid JSON.
	ErrCursorInvalid = errors.New("cursor is not valid JSON")

	// ErrCursorTooLarge is the size cap, in bytes of the JSON.
	ErrCursorTooLarge = fmt.Errorf("cursor is larger than %d bytes", MaxCursorBytes)
)

// State is one stored cursor.
type State struct {
	Kind string
	Key  string
	// Cursor is the opaque payload. Nil for a local row, and nil is the whole point of a local
	// row rather than a small detail of it.
	Cursor    json.RawMessage
	Storage   Storage
	UpdatedAt time.Time
}

// Service is the session-state service.
type Service struct {
	queries *queries.Queries
}

// New creates the service.
func New(queries *queries.Queries) *Service {
	return &Service{queries: queries}
}

// ValidStorage reports whether a storage value is one the schema accepts.
func ValidStorage(storage Storage) bool {
	return storage == StorageLocal || storage == StorageSynced
}

// Save stores a cursor for (user, kind, key), upserting.
//
// A LOCAL SAVE STORES NO PAYLOAD. The cursor argument is ignored for it -- deliberately, and not
// as a convenience: a client that says "this stays on my device" and then sends the state anyway
// must not have it stored, or the promise is decoration.
func (s *Service) Save(ctx context.Context, userID uuid.UUID, kind, key string, cursor json.RawMessage, storage Storage) (*State, error) {
	if kind == "" {
		return nil, ErrEmptyKind
	}
	if key == "" {
		return nil, ErrEmptyKey
	}
	if len(kind) > MaxKindLength {
		return nil, ErrKindTooLong
	}
	if len(key) > MaxKeyLength {
		return nil, ErrKeyTooLong
	}
	if !ValidStorage(storage) {
		return nil, ErrUnknownStorage
	}

	payload := json.RawMessage(nil)
	if storage == StorageSynced {
		if len(cursor) == 0 {
			// A synced row with no payload is a contradiction the schema also refuses: there
			// would be nothing for a second device to continue from.
			return nil, ErrCursorInvalid
		}
		if !json.Valid(cursor) {
			return nil, ErrCursorInvalid
		}
		if len(cursor) > MaxCursorBytes {
			return nil, ErrCursorTooLarge
		}
		payload = cursor
	}

	err := s.queries.UpsertSessionState(ctx, queries.UpsertSessionStateParams{
		UserID:  userID,
		Kind:    kind,
		Key:     key,
		Storage: string(storage),
		Cursor:  payload,
	})
	if err != nil {
		return nil, fmt.Errorf("saving session state: %w", err)
	}

	state, err := s.Get(ctx, userID, kind, key)
	if err != nil {
		return nil, err
	}
	if state == nil {
		// The row exists but is local, so it has no payload and no cursor to return. The caller
		// still learns the choice was recorded, which is the honest answer to "what did you
		// store" for a local write.
		return &State{Kind: kind, Key: key, Storage: storage}, nil
	}
	return state, nil
}

// Get returns the SYNCED cursor for (user, kind, key), or nil.
//
// Nil for a local row rather than a local state with no payload: the difference matters to a
// caller deciding whether to restore a position, and "there is a row but it is none of your
// business" is a distinction the API should not have to make.
func (s *Service) Get(ctx context.Context, userID uuid.UUID, kind, key string) (*State, error) {
	row, err := s.queries.FindSessionState(ctx, queries.FindSessionStateParams{
		UserID: userID,
		Kind:   kind,
		Key:    key,
	})
	if err != nil {
		if errutil.IgnoreNotFound(err) == nil {
			return nil, nil
		}
		return nil, fmt.Errorf("reading session state: %w", err)
	}
	return &State{
		Kind:      row.Kind,
		Key:       row.Key,
		Cursor:    row.Cursor,
		Storage:   Storage(row.Storage),
		UpdatedAt: row.UpdatedAt.Time,
	}, nil
}

// ListByKind returns everything this user syncs for one kind of surface: what a second device
// reads on arrival, so it does not need one request per key.
func (s *Service) ListByKind(ctx context.Context, userID uuid.UUID, kind string) ([]State, error) {
	rows, err := s.queries.FindSessionStatesByKind(ctx, queries.FindSessionStatesByKindParams{
		UserID: userID,
		Kind:   kind,
	})
	if err != nil {
		return nil, fmt.Errorf("listing session state: %w", err)
	}
	out := make([]State, 0, len(rows))
	for _, row := range rows {
		out = append(out, State{
			Kind:      row.Kind,
			Key:       row.Key,
			Cursor:    row.Cursor,
			Storage:   Storage(row.Storage),
			UpdatedAt: row.UpdatedAt.Time,
		})
	}
	return out, nil
}

// Clear deletes the row. The bool reports whether one was there, so a caller can tell "forgotten"
// from "there was nothing to forget" without a second read.
func (s *Service) Clear(ctx context.Context, userID uuid.UUID, kind, key string) (bool, error) {
	rows, err := s.queries.DeleteSessionState(ctx, queries.DeleteSessionStateParams{
		UserID: userID,
		Kind:   kind,
		Key:    key,
	})
	if err != nil {
		return false, fmt.Errorf("clearing session state: %w", err)
	}
	return rows > 0, nil
}
