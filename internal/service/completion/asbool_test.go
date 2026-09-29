package completion

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// asBool is the single place a sqlc boolean is converted, and it is where a
// three-valued-logic bug would be decided rather than merely reflected.
//
// The hazard it exists for: the SQL wraps every expression in COALESCE(..., FALSE)
// so a NULL never reaches the arithmetic, but sqlc still types those columns as
// `interface{}` because it cannot prove the expression is boolean. So the row
// arrives dynamically typed and the NULL case is decided HERE. Every assertion
// below is about that decision.

func TestAsBool(t *testing.T) {
	cases := []struct {
		name  string
		input interface{}
		want  bool
	}{
		{"a real true", true, true},
		{"a real false", false, false},

		// The case the COALESCE is supposed to prevent, tested anyway: a nil that
		// reaches here anyway must not read as PRESENT. Reading it as true would
		// make an entity look complete and send a curator to fix nothing.
		{"nil is missing, not present", nil, false},

		{"a pointer to true", ptrTo(true), true},
		{"a pointer to false", ptrTo(false), false},
		{"a nil pointer is missing", (*bool)(nil), false},

		// Deliberately not an error. This runs on a read path for a value a client
		// renders as a progress bar; a malformed column must not become a 500. A
		// conservative score is the safe direction to be wrong in -- an
		// over-reported completion is a lie, an under-reported one is a prompt.
		{"a non-boolean is treated as missing", 42, false},
		{"a string is treated as missing", "true", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, asBool(tc.input),
				"a field this code cannot confirm is present must count as "+
					"missing: the cost of each false 'present' is a curator sent "+
					"to fix something that is already fixed")
		})
	}
}

// The performance trap this guards against, stated as a test.
//
// A caller that reached for a type assertion with `, ok` and then IGNORED ok
// would panic on a nil interface rather than returning false -- and this runs on
// every entity read, so one entity with an unusual column shape would take down
// the request for a page of 50.
func TestAsBoolNeverPanicsOnNil(t *testing.T) {
	assert.NotPanics(t, func() {
		_ = asBool(nil)
		_ = asBool((*bool)(nil))
		_ = asBool(struct{}{})
	})
}

func ptrTo(v bool) *bool { return &v }
