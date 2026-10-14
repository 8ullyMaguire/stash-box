package sessionstate

import (
	"strings"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The validation, without a database. The Service is built with nil queries on purpose: every case
// here must return BEFORE a query runs, and a case that stopped doing that would panic rather than
// quietly reaching for a database this test does not serve.

func nilService() *Service { return New(nil) }

func TestValidStorage(t *testing.T) {
	assert.True(t, ValidStorage(StorageLocal))
	assert.True(t, ValidStorage(StorageSynced))
	for _, bad := range []Storage{"", "LOCAL", "sync", "local ", "server"} {
		assert.False(t, ValidStorage(bad), "%q is not a storage the schema accepts", bad)
	}
}

func TestSaveRefusesBadInputBeforeTouchingTheDatabase(t *testing.T) {
	userID := uuid.Must(uuid.NewV4())
	validCursor := []byte(`{"page":1}`)
	longKind := strings.Repeat("k", MaxKindLength+1)
	longKey := strings.Repeat("k", MaxKeyLength+1)
	longCursor := []byte(`{"data":"` + strings.Repeat("x", MaxCursorBytes) + `"}`)

	cases := []struct {
		name     string
		kind     string
		key      string
		cursor   []byte
		storage  Storage
		expected error
	}{
		{"an empty kind", "", "browse", validCursor, StorageSynced, ErrEmptyKind},
		{"an empty key", "scenes", "", validCursor, StorageSynced, ErrEmptyKey},
		{"a kind over the column cap", longKind, "browse", validCursor, StorageSynced, ErrKindTooLong},
		{"a key over the column cap", "scenes", longKey, validCursor, StorageSynced, ErrKeyTooLong},
		{"an unknown storage", "scenes", "browse", validCursor, Storage("sync"), ErrUnknownStorage},
		{"a synced cursor with no payload", "scenes", "browse", nil, StorageSynced, ErrCursorInvalid},
		{"a synced cursor that is not JSON", "scenes", "browse", []byte("page=7"), StorageSynced, ErrCursorInvalid},
		{"a synced cursor over the cap", "scenes", "browse", longCursor, StorageSynced, ErrCursorTooLarge},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := nilService().Save(t.Context(), userID, c.kind, c.key, c.cursor, c.storage)
			require.Error(t, err, "must be refused")
			require.ErrorIs(t, err, c.expected)
		})
	}
}

func TestStorageConstantsMatchTheSchema(t *testing.T) {
	// The strings the service writes are the strings in migration 116's CHECK. An assertion, so a
	// rename here fails a test rather than a user's save.
	assert.Equal(t, "local", string(StorageLocal))
	assert.Equal(t, "synced", string(StorageSynced))
	assert.Equal(t, 4096, MaxCursorBytes)
}

// The bounds are the COLUMN's, so a value that passes here cannot fail the insert.
func TestBoundsFitTheColumns(t *testing.T) {
	require.LessOrEqual(t, MaxKindLength, 32)
	require.LessOrEqual(t, MaxKeyLength, 128)
}
