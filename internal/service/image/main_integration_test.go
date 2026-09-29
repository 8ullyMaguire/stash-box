//go:build integration

package image_test

import (
	"testing"

	dbtest "github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/service"
)

// This package needs its own TestMain because dbtest.initPostgres drops every
// table before the tests run, so the database cannot be shared with another
// test package in the same binary. internal/api does the same thing; a test
// here must not be added to internal/api, where the tables are already
// populated and the image service is reachable only through its unexported
// resolver wiring.
func TestMain(m *testing.M) {
	dbtest.TestWithDatabase(m, nil)
}

var _ service.Factory
