//go:build integration

package api_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/99designs/gqlgen/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbtest "github.com/stashapp/stash-box/internal/database/testutil"
)

// Session state over GraphQL: "where I left off" (feature-discovery SPEC section 11, phase 7
// slice 7).
//
// THE CLAIM THIS FILE EXISTS TO BREAK IS "it stays on your device". A test that saves with
// storage: LOCAL and reads back a null cursor proves the API does not RETURN the payload; it does
// not prove the payload is not STORED, and a resolver that wrote it and then hid it would pass.
// So the local cases below read the COLUMN, and one of them inserts a local row with a payload
// directly to prove the database refuses it. The guarantee lives in the schema (migration 116);
// these tests are how you find out whether it actually does.

const sessionStateQuery = `
	query($kind: String!, $key: String!) {
		sessionState(kind: $kind, key: $key) { kind key cursor storage updatedAt }
	}
`

type sessionStateRow struct {
	Kind      string
	Key       string
	Cursor    *string
	Storage   string
	UpdatedAt *string
}

func sessionState(t *testing.T, runner *testRunner, kind, key string) *sessionStateRow {
	t.Helper()
	var resp struct {
		SessionState *sessionStateRow
	}
	runner.client.MustPost(sessionStateQuery, &resp, client.Var("kind", kind), client.Var("key", key))
	return resp.SessionState
}

func saveSessionState(t *testing.T, runner *testRunner, kind, key string, cursor *string, storage string) sessionStateRow {
	t.Helper()
	var resp struct {
		SaveSessionState sessionStateRow
	}
	input := map[string]any{"kind": kind, "key": key, "storage": storage}
	if cursor != nil {
		input["cursor"] = *cursor
	}
	runner.client.MustPost(`mutation($input: SessionStateInput!) {
		saveSessionState(input: $input) { kind key cursor storage updatedAt }
	}`, &resp, client.Var("input", input))
	return resp.SaveSessionState
}

// cursorIsNull reads the COLUMN rather than the API: `cursor IS NULL` is the fact, and the API's
// null could be either a null column or a resolver that hides a value.
func cursorIsNull(t *testing.T, userID string, kind, key string) bool {
	t.Helper()
	var isNull bool
	require.NoError(t, dbtest.DB().QueryRow(t.Context(),
		"SELECT cursor IS NULL FROM session_states WHERE user_id = $1 AND kind = $2 AND key = $3",
		userID, kind, key).Scan(&isNull))
	return isNull
}

func sessionStateRowExists(t *testing.T, userID, kind, key string) bool {
	t.Helper()
	var count int
	require.NoError(t, dbtest.DB().QueryRow(t.Context(),
		"SELECT count(*) FROM session_states WHERE user_id = $1 AND kind = $2 AND key = $3",
		userID, kind, key).Scan(&count))
	return count > 0
}

// A LOCAL SAVE STORES NO PAYLOAD, even when the client sends one.
func TestSessionStateLocalSaveStoresNoPayload(t *testing.T) {
	viewer := asRead(t)
	const kind, key = "test-local", "browse"

	cursor := `{"page":7,"filter":"unwatched"}`
	saved := saveSessionState(t, viewer, kind, key, &cursor, "LOCAL")

	assert.Equal(t, "LOCAL", saved.Storage)
	assert.Nil(t, saved.Cursor,
		"a local save must not give the payload back: the server was not asked to hold it")

	require.True(t, sessionStateRowExists(t, userDB.read.ID.String(), kind, key),
		"the CHOICE is recorded, which is the point of the row")
	assert.True(t, cursorIsNull(t, userDB.read.ID.String(), kind, key),
		"the payload a local save sent must not be in the column, however the API renders it")

	assert.Nil(t, sessionState(t, viewer, kind, key),
		"and the read that means \"the server holds nothing\" says so")
}

// A SYNCED save round-trips, and it is the USER's own: another user asking the same (kind, key)
// gets nothing.
func TestSessionStateSyncedRoundTripsAndIsScopedToTheUser(t *testing.T) {
	owner := asRead(t)
	other := asModerate(t)
	const kind, key = "test-synced", "browse"

	cursor := `{"page":3,"sort":"recent"}`
	saved := saveSessionState(t, owner, kind, key, &cursor, "SYNCED")
	require.NotNil(t, saved.Cursor)
	// JSONEq rather than Equal: the column is jsonb, so the VALUE is preserved and the TEXT need
	// not be -- Postgres normalises whitespace and key order. "Opaque" means the server does not
	// interpret the cursor, not that it stores the bytes the client typed.
	assert.JSONEq(t, cursor, *saved.Cursor)
	assert.Equal(t, "SYNCED", saved.Storage)
	require.NotNil(t, saved.UpdatedAt, "a client needs to know its write landed")

	readBack := sessionState(t, owner, kind, key)
	require.NotNil(t, readBack)
	require.NotNil(t, readBack.Cursor)
	assert.JSONEq(t, cursor, *readBack.Cursor)

	// The subject, counted: the owner's row exists. So the null below is about the SCOPE.
	require.True(t, sessionStateRowExists(t, userDB.read.ID.String(), kind, key))
	assert.Nil(t, sessionState(t, other, kind, key),
		"a cursor is one person's position, keyed by the user as well as the kind")
}

// TURNING SYNC OFF REMOVES WHAT WAS SYNCED. A user who switches a surface to local must not leave
// their position on the server: this is the difference between a setting and a leak.
func TestSwitchingToLocalRemovesTheSyncedPayload(t *testing.T) {
	viewer := asRead(t)
	const kind, key = "test-optout", "browse"

	cursor := `{"page":12}`
	saveSessionState(t, viewer, kind, key, &cursor, "SYNCED")
	require.False(t, cursorIsNull(t, userDB.read.ID.String(), kind, key),
		"the payload is stored to begin with, so its removal is a real change")

	saveSessionState(t, viewer, kind, key, &cursor, "LOCAL")

	assert.True(t, cursorIsNull(t, userDB.read.ID.String(), kind, key),
		"switching to local must remove the synced payload rather than leave it behind")
	assert.Nil(t, sessionState(t, viewer, kind, key))
}

// The validation is refused, and NOTHING IS WRITTEN when it is: an error that still left a row
// behind would be worse than no error.
func TestSessionStateRejectsBadInput(t *testing.T) {
	viewer := asRead(t)
	const kind = "test-invalid"

	bad := func(label string, cursor *string, storage string, key string) {
		t.Helper()
		var resp map[string]any
		input := map[string]any{"kind": kind, "key": key, "storage": storage}
		if cursor != nil {
			input["cursor"] = *cursor
		}
		input["cursor"] = input["cursor"] // keep the shape explicit for the no-cursor case
		err := viewer.client.Post(`mutation($input: SessionStateInput!) {
			saveSessionState(input: $input) { key }
		}`, &resp, client.Var("input", input))
		require.Error(t, err, label+": must be refused")
		assert.False(t, sessionStateRowExists(t, userDB.read.ID.String(), kind, key),
			label+": a refused write must not leave a row")
	}

	notJSON := "page=7"
	bad("a synced cursor that is not JSON", &notJSON, "SYNCED", "not-json")

	huge := `{"data":"` + strings.Repeat("x", 5000) + `"}`
	bad("a cursor over the 4096 byte cap", &huge, "SYNCED", "too-large")

	empty := ""
	bad("a synced save with no cursor at all", &empty, "SYNCED", "empty-cursor")
	bad("an empty key", nil, "SYNCED", "")

	// And a LOCAL save of the same invalid payload IS accepted, because a local save stores
	// nothing to validate: the rule follows the storage, not the request's honesty.
	saved := saveSessionState(t, viewer, kind, "local-invalid", &notJSON, "LOCAL")
	assert.Nil(t, saved.Cursor)
	assert.True(t, cursorIsNull(t, userDB.read.ID.String(), kind, "local-invalid"))
}

// Listing a kind returns that user's synced positions, and clearing is honest about whether there
// was one.
func TestSessionStateListsAndClears(t *testing.T) {
	viewer := asRead(t)
	const kind = "test-list"

	cursorA, cursorB := `{"page":1}`, `{"page":2}`
	saveSessionState(t, viewer, kind, "alpha", &cursorA, "SYNCED")
	saveSessionState(t, viewer, kind, "beta", &cursorB, "SYNCED")
	saveSessionState(t, viewer, kind, "gamma", &cursorA, "LOCAL")

	var resp struct {
		SessionStates []sessionStateRow
	}
	viewer.client.MustPost(`query($kind: String!) {
		sessionStates(kind: $kind) { kind key cursor storage }
	}`, &resp, client.Var("kind", kind))

	require.Len(t, resp.SessionStates, 2,
		"only the synced positions: a second device has nothing to continue from a local one")
	keys := map[string]string{}
	for _, row := range resp.SessionStates {
		keys[row.Key] = row.Storage
	}
	assert.Equal(t, "SYNCED", keys["alpha"])
	assert.Equal(t, "SYNCED", keys["beta"])
	assert.NotContains(t, keys, "gamma")

	clear := func(key string) bool {
		t.Helper()
		var out struct {
			ClearSessionState bool
		}
		viewer.client.MustPost(`mutation($input: ClearSessionStateInput!) {
			clearSessionState(input: $input)
		}`, &out, client.Var("input", map[string]any{"kind": kind, "key": key}))
		return out.ClearSessionState
	}

	assert.True(t, clear("alpha"), "there was a position to forget")
	assert.False(t, clear("alpha"), "and forgetting it twice is not an error")
	assert.False(t, sessionStateRowExists(t, userDB.read.ID.String(), kind, "alpha"))
	// The others are untouched: a clear is one key, not the kind.
	assert.True(t, sessionStateRowExists(t, userDB.read.ID.String(), kind, "beta"))
}

// THE DATABASE ITSELF REFUSES a local row with a payload: the promise is the schema's, and this
// test bypasses the service to prove the service is not what keeps it.
func TestTheDatabaseRefusesALocalRowWithAPayload(t *testing.T) {
	asAdmin(t)

	_, err := dbtest.DB().Exec(t.Context(), `
		INSERT INTO session_states (user_id, kind, key, cursor, storage)
		VALUES ($1, 'test-check', 'bypass', '{"page":1}'::jsonb, 'local')`,
		userDB.read.ID)
	require.Error(t, err,
		"'local' must be unable to carry a cursor at the schema level, not only in the service")

	// And the same insert with a null cursor is accepted, so the failure above is the CHECK
	// rather than a missing column or a bad type in this test.
	_, err = dbtest.DB().Exec(t.Context(), `
		INSERT INTO session_states (user_id, kind, key, cursor, storage)
		VALUES ($1, 'test-check', 'bypass', NULL, 'local')
		ON CONFLICT (user_id, kind, key) DO NOTHING`,
		userDB.read.ID)
	require.NoError(t, err, "the insert shape is valid, so the refusal above was the payload rule")
}

// The cursor is opaque: whatever the client puts in it comes back as the same VALUE, including
// structures the server has no schema for.
//
// The text is not preserved, because the column is jsonb: whitespace and key order are the
// database's. That is worth stating rather than discovering -- a client comparing cursor strings
// byte for byte would see its own write come back different.
func TestSessionStateCursorIsOpaque(t *testing.T) {
	viewer := asRead(t)
	const kind, key = "test-opaque", "browse"

	// Nested, with a unicode key and a null: nothing the server has a schema for.
	cursor := `{"deep":{"list":[1,null,"ü"]},"flag":true}`
	saved := saveSessionState(t, viewer, kind, key, &cursor, "SYNCED")
	require.NotNil(t, saved.Cursor)
	assert.JSONEq(t, cursor, *saved.Cursor)

	// It is stored as JSON rather than as a string of JSON: the column is jsonb, so this is the
	// same value with the whitespace the database normalised away, not a re-quoted blob.
	var stored map[string]any
	require.NoError(t, dbtest.DB().QueryRow(t.Context(),
		"SELECT cursor FROM session_states WHERE user_id = $1 AND kind = $2 AND key = $3",
		userDB.read.ID, kind, key).Scan(&stored))
	deep, ok := stored["deep"].(map[string]any)
	require.True(t, ok, "the stored value is an object, not a JSON string")
	require.Contains(t, deep, "list")

	var parsed struct {
		Deep struct {
			List []any `json:"list"`
		} `json:"deep"`
		Flag bool `json:"flag"`
	}
	require.NoError(t, json.Unmarshal([]byte(*saved.Cursor), &parsed))
	assert.True(t, parsed.Flag)
	assert.Len(t, parsed.Deep.List, 3)
}
