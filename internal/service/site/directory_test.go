package site

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The directory has no unit tests of its own, and the two helpers here exist
// because their behaviour was a MUTATION SURVIVOR twice each:
//
//   - clampLimit's removal is invisible to any test that runs a real search,
//     because a clamped and an unclamped query over nine fixture sites return the
//     SAME ROWS. Only the number itself can be asserted.
//   - searchText's removal is invisible to any OUTPUT-comparing test, for a worse
//     reason: `query = ''` and `query = NULL` return identical results. The
//     difference is a scan, and a scan is not a result.
//
// So both are pure functions and both are asserted directly. That is the only
// shape of test that can see either defect.

func TestClampLimitBoundsBothEnds(t *testing.T) {
	cases := []struct {
		name string
		in   int32
		want int32
	}{
		// Zero is NOT "no rows". It is a client that omitted the field, and
		// answering it with an empty page is what makes a directory render blank
		// on first load.
		{"zero is a missing field, not an empty page", 0, maxDirectoryLimit},
		{"negative is nonsense and is treated the same", -5, maxDirectoryLimit},
		{"over the cap clamps down", 5000, maxDirectoryLimit},
		{"at the cap is unchanged", maxDirectoryLimit, maxDirectoryLimit},
		{"in range is passed through", 25, 25},
		{"one below the cap is passed through", maxDirectoryLimit - 1, maxDirectoryLimit - 1},
		{"one is the smallest real page", 1, 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, clampLimit(tc.in))
		})
	}

	// The invariant that makes this worth a test at all: whatever the input, the
	// result is a page size the database can actually serve.
	for _, in := range []int32{-1000, -1, 0, 1, 50, 100, 101, 1 << 20} {
		got := clampLimit(in)
		assert.Greater(t, got, int32(0), "input %d", in)
		assert.LessOrEqual(t, got, maxDirectoryLimit, "input %d", in)
	}
}

// An empty search box is NULL, not the empty string.
//
// The test asserts the POINTER, not the query results, because the two are
// indistinguishable from the output: `ILIKE '%%'` matches every row exactly as no
// predicate does. What differs is whether the database scans with a predicate
// attached, and that is invisible except in latency -- which is the hardest kind
// of regression to notice and the easiest to ship.
func TestSearchTextMapsEmptyToNull(t *testing.T) {
	assert.Nil(t, searchText(""),
		"nil, so sqlc's narg binds NULL and the SQL takes the no-filter branch")

	q := searchText("vr")
	require.NotNil(t, q)
	assert.Equal(t, "vr", *q)
}

// Whitespace is a search, not an empty one. A user who typed a space is refining
// the query, and trimming it for them is a decision this function should not make
// silently -- the UI is where trimming belongs, and this is the layer that decides
// what the DATABASE sees.
func TestSearchTextKeepsWhitespaceSearches(t *testing.T) {
	q := searchText(" ")
	require.NotNil(t, q,
		"a lone space is a real query the user typed. Treating it as empty "+
			"silently discards what they asked for")
	assert.Equal(t, " ", *q)
}
