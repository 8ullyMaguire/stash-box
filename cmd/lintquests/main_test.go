package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The import pass's two inputs are checked for the same reason: a pass that silently skipped an id, or
// answered about a type no detector implements, reports an import as clean without having looked at it.
//
// The type arm is the one that was missing. `-entity studio -ids <uuid>` used to print
// "validated 1 studio id(s)" and exit 0, because the type was passed straight through to
// ValidateEntities, which groups ids by type and skips every detector whose type has none.
func TestParseRefsRefusesAnEntityTypeNothingImplements(t *testing.T) {
	const id = "01a0f15f-809e-7841-9252-88d1adf5dafb"

	_, err := parseRefs("studio", id)
	require.Error(t, err, "a studio is not something this build has detectors for")
	assert.Contains(t, err.Error(), "studio", "the refusal names what it was given")
	assert.Contains(t, err.Error(), "performer", "and names what it can check")

	// The control: the two types it DOES have detectors for must not be refused. Without this, a
	// parseRefs that refused everything would pass the assertions above.
	for _, entityType := range []string{"performer", "scene"} {
		refs, err := parseRefs(entityType, id)
		require.NoErrorf(t, err, "%s is a supported type", entityType)
		require.Len(t, refs, 1)
		assert.Equal(t, entityType, refs[0].EntityType)
	}
}

func TestParseRefsRefusesIdsItCannotRead(t *testing.T) {
	_, err := parseRefs("performer", "")
	assert.ErrorContains(t, err, "-ids is required with -entity", "an empty id list is not a pass")

	_, err = parseRefs("performer", "not-a-uuid")
	assert.ErrorContains(t, err, "not a UUID", "an unreadable id is not a skipped one")
}

func TestParseRefsTakesSeveralIds(t *testing.T) {
	refs, err := parseRefs("scene", "01a0f15f-809e-7841-9252-88d1adf5dafb, 01a0f1a2-1a77-7f8d-994a-653fa934929b")
	require.NoError(t, err)
	require.Len(t, refs, 2, "a comma-separated batch is one pass over two records")
	for _, ref := range refs {
		assert.Equal(t, "scene", ref.EntityType)
	}
}
