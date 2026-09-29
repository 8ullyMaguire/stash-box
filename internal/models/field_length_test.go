package models

import (
	"strings"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stashapp/stash-box/pkg/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Regression tests for issue #660, "Limit Field Lengths on Form Fields".
//
// The report: a submission form accepts metadata longer than the database
// column, so the edit passes review and only fails at apply time with an opaque
// Postgres error (or, for a column with no explicit limit, silently truncates
// nothing and stores a value the schema never promised to hold).
//
// The real limits live in the schema, not in Go:
//
//	tags.name          varchar(255)   <- 01_initial.up.sql
//	tags.description   varchar(255)
//	scenes.title       varchar(255)
//	studios.name       varchar(255)
//
// With no application-level check, the failure surfaces as
// `pq: value too long for type character varying(255)` from a background cron
// sweep, far from the contributor who typed it. These tests pin the fix: reject
// at edit-creation time with a message that names the field and the limit.
//
// A round-trip test alone would not prove this: the mutation is that the check
// is ABSENT, and a test that only exercised the happy path would still pass
// without it. So each case asserts the rejection directly.

const maxFieldLength = 255

func longString(n int) *string {
	s := strings.Repeat("x", n)
	return &s
}

// TestTagNameLengthRejected covers the reported case: a tag name over the
// varchar(255) limit.
func TestTagNameLengthRejected(t *testing.T) {
	orig := Tag{Name: "ok"}
	input := TagEditDetailsInput{Name: longString(maxFieldLength + 1)}

	// TagEditFromDiff has no error return today; the fix adds one. Until it
	// does, this test cannot compile — which is the point: it pins the shape
	// of the fix rather than silently passing.
	_, err := input.TagEditFromDiffChecked(orig, utils.ArgumentsQuery{})
	require.Error(t, err, "a 256-character tag name must be rejected")
	assert.Contains(t, err.Error(), "name",
		"the error should name the offending field")
}

func TestTagNameAtLimitAccepted(t *testing.T) {
	orig := Tag{Name: "ok"}
	input := TagEditDetailsInput{Name: longString(maxFieldLength)}

	_, err := input.TagEditFromDiffChecked(orig, utils.ArgumentsQuery{})
	require.NoError(t, err, "exactly 255 characters must be accepted")
}

func TestTagDescriptionLengthRejected(t *testing.T) {
	orig := Tag{Name: "ok"}
	input := TagEditDetailsInput{Description: longString(maxFieldLength + 1)}

	_, err := input.TagEditFromDiffChecked(orig, utils.ArgumentsQuery{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "description")
}

func TestSceneTitleLengthRejected(t *testing.T) {
	orig := Scene{}
	input := SceneEditDetailsInput{Title: longString(maxFieldLength + 1)}

	_, err := input.SceneEditFromDiffChecked(orig, utils.ArgumentsQuery{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "title")
}

func TestSceneTitleAtLimitAccepted(t *testing.T) {
	orig := Scene{}
	input := SceneEditDetailsInput{Title: longString(maxFieldLength)}

	_, err := input.SceneEditFromDiffChecked(orig, utils.ArgumentsQuery{})
	require.NoError(t, err)
}

func TestStudioNameLengthRejected(t *testing.T) {
	orig := Studio{}
	input := StudioEditDetailsInput{Name: longString(maxFieldLength + 1)}

	_, err := input.StudioEditFromDiffChecked(orig, utils.ArgumentsQuery{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "name")
}

func TestPerformerNameLengthRejected(t *testing.T) {
	orig := Performer{}
	input := PerformerEditDetailsInput{Name: longString(maxFieldLength + 1)}

	_, err := input.PerformerEditFromDiffChecked(orig, utils.ArgumentsQuery{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "name")
}

func TestPerformerDisambiguationLengthRejected(t *testing.T) {
	orig := Performer{}
	input := PerformerEditDetailsInput{Disambiguation: longString(maxFieldLength + 1)}

	_, err := input.PerformerEditFromDiffChecked(orig, utils.ArgumentsQuery{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "disambiguation")
}

// TestNilFieldsAreNotLengthChecked guards the common false positive: a field
// the edit does not mention arrives as nil and must not trip the check.
func TestNilFieldsAreNotLengthChecked(t *testing.T) {
	orig := Performer{Name: "ok"}

	_, err := PerformerEditDetailsInput{}.PerformerEditFromDiffChecked(orig, utils.ArgumentsQuery{})
	require.NoError(t, err, "an empty details input must not fail a length check")
}

// TestExplicitNullIsNotLengthChecked is the #802 regression guard: a field set
// to null (a deletion) must not be measured as a string of some length.
func TestExplicitNullIsNotLengthChecked(t *testing.T) {
	orig := Tag{Name: "ok", Description: longString(maxFieldLength)}

	// A deletion is nil, and nil is what "not proposed" also looks like. The
	// check must not conflate them into a spurious error.
	_, err := TagEditDetailsInput{Description: nil}.TagEditFromDiffChecked(orig, utils.ArgumentsQuery{})
	require.NoError(t, err)
}

var _ = uuid.Nil
