//go:build integration

package api_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbtest "github.com/stashapp/stash-box/internal/database/testutil"
)

// The client half of the lint board contract, written as a client would write it: a query, decoded into
// local structs carrying the camelCase names the server actually returns. Declared here rather than
// reusing the models package's types, because a resolver-level struct would decode even if the response
// did not match -- which is the thing this test exists to notice.
type lintCandidateOutput struct {
	EntityId   string `json:"entityId"`
	EntityName string `json:"entityName"`
	Detail     string `json:"detail"`
}

type lintReportOutput struct {
	Slug         string                `json:"slug"`
	Description  string                `json:"description"`
	EntityType   string                `json:"entityType"`
	BountyPoints int                   `json:"bountyPoints"`
	Count        int                   `json:"count"`
	LimitNotice  *string               `json:"limitNotice"`
	Candidates   []lintCandidateOutput `json:"candidates"`
}

type lintDefinitionOutput struct {
	Slug         string `json:"slug"`
	Description  string `json:"description"`
	Enabled      bool   `json:"enabled"`
	BountyPoints int    `json:"bountyPoints"`
	IsBuiltin    bool   `json:"isBuiltin"`
	Implemented  bool   `json:"implemented"`
}

// lintQuestBoard queries the board. A nil limit sends no argument at all, which is the client-side case
// worth covering: the service's own default is what a client that omits it must get.
func (c *graphqlClient) lintQuestBoard(limit *int) ([]lintReportOutput, error) {
	args := ""
	if limit != nil {
		// An int, interpolated by fmt: there is no path from a caller's string into this query.
		args = fmt.Sprintf("(limit: %d)", *limit)
	}
	q := `query LintQuestBoard {
		lintQuestBoard` + args + ` {
			slug
			description
			entityType
			bountyPoints
			count
			limitNotice
			candidates { entityId entityName detail }
		}
	}`

	var resp struct {
		LintQuestBoard []lintReportOutput `json:"lintQuestBoard"`
	}
	if err := c.Post(q, &resp); err != nil {
		return nil, err
	}
	return resp.LintQuestBoard, nil
}

func (c *graphqlClient) lintQuestDefinitions() ([]lintDefinitionOutput, error) {
	q := `query LintQuestDefinitions {
		lintQuestDefinitions {
			slug description enabled bountyPoints isBuiltin implemented
		}
	}`

	var resp struct {
		LintQuestDefinitions []lintDefinitionOutput `json:"lintQuestDefinitions"`
	}
	if err := c.Post(q, &resp); err != nil {
		return nil, err
	}
	return resp.LintQuestDefinitions, nil
}

// findReport returns the report for one detector, and fails BY NAME when it is missing: an index into an
// unordered slice turns a missing detector into the wrong detector's assertions.
// seedLintRegistry mirrors what the server's startup does before it serves anything, and the graphql
// harness does NOT do it: createTestRunner builds an in-process server around the resolvers, so
// `cmd/stash-box`'s initLintRegistry never runs and the registry would be empty for every test here.
//
// That is not a workaround for a fixture problem, it is the deployment's own order made explicit -- and
// the harness finding it is why the service now REFUSES an empty registry rather than answering with a
// board that has nothing on it. Without both halves, every test in this file would have "passed" against
// a board that never ran a detector.
func seedLintRegistry(t *testing.T) {
	t.Helper()
	_, err := lintService().EnsureBuiltins(context.Background())
	require.NoError(t, err, "seeding the lint registry the way startup does")
}

func findReport(t *testing.T, reports []lintReportOutput, slug string) lintReportOutput {
	t.Helper()
	for _, r := range reports {
		if r.Slug == slug {
			return r
		}
	}
	require.FailNowf(t, "no such report on the board",
		"the board returned %d report(s) and none was %q", len(reports), slug)
	return lintReportOutput{}
}

func candidateNames(r lintReportOutput) []string {
	names := make([]string, 0, len(r.Candidates))
	for _, c := range r.Candidates {
		names = append(names, c.EntityName)
	}
	return names
}

// The board is a work queue for people who can act on it. A READ-only viewer has nothing to do with a
// list of unmerged duplicates and mis-entered identities, and this asserts the gate at the API rather
// than trusting the annotation on the field.
func TestLintQuestBoardIsRefusedWithoutVoteRole(t *testing.T) {
	seedLintRegistry(t)
	_, err := asRead(t).client.lintQuestBoard(nil)
	require.Error(t, err, "a read-only viewer must not be served the lint board")
	assert.Contains(t, err.Error(), "not authorized")
}

// The whole chain in one test: two performers sharing a URL, read back through GraphQL, with the fields
// a curator needs in order to act -- the entity's name, and a sentence saying WHY it is a finding.
func TestLintQuestBoardServesFindingsThroughGraphQL(t *testing.T) {
	seedLintRegistry(t)
	runner := asAdmin(t)

	shared := "https://lint-graphql.test/" + lintSuffix()
	site := lintSite(t)
	firstName := "Lint Gql One " + lintSuffix()
	secondName := "Lint Gql Two " + lintSuffix()
	first := lintPerformer(t, firstName, nil)
	second := lintPerformer(t, secondName, nil)
	lintPerformerURL(t, first, site, shared)
	lintPerformerURL(t, second, site, shared)

	reports, err := runner.client.lintQuestBoard(nil)
	require.NoError(t, err)

	report := findReport(t, reports, "duplicate_performer_url")
	assert.Equal(t, "performer", report.EntityType, "the client routes a candidate by this field")
	assert.Positive(t, report.BountyPoints, "a finding with no price is not an offer")
	assert.Equal(t, len(report.Candidates), report.Count, "count is the length of this response, by definition")

	// THE FIXTURE'S NEIGHBOURS ARE PART OF THE REPORT. This package shares one database, so the board
	// also carries whatever other tests left behind -- an earlier run of this very file included, since
	// the URLs are unique per run and neither test cleans up. The assertions are therefore scoped to the
	// two records THIS test created, by id, rather than to the report's contents as a whole: an
	// ElementsMatch on the whole list fails for a reason that has nothing to do with the rule.
	mine := map[string]string{first.String(): shared, second.String(): shared}
	found := map[string]bool{}
	for _, c := range report.Candidates {
		want, isMine := mine[c.EntityId]
		if !isMine {
			continue
		}
		found[c.EntityId] = true
		assert.NotEmpty(t, c.EntityName, "a finding with no name is a row a curator must look up themselves")
		// The detail names the SHARED VALUE, not just the attribute: "these share a URL" still leaves a
		// curator to go and find which one.
		assert.Contains(t, c.Detail, want, "the detail names the URL the two records share")
	}
	assert.Len(t, found, 2, "both holders of the URL are findings, each named with the shared value")
}

// A board that is one detector short must not look like a board where that detector found nothing. The
// live deployment is where this was found: `-validate` refused while the board answered with five
// detectors instead of six.
func TestLintQuestBoardRefusesARegistryThisBuildCannotHonour(t *testing.T) {
	seedLintRegistry(t)
	runner := asAdmin(t)

	slug := "not_a_detector_this_build_has"
	// is_builtin true and authored_by NULL: the table's check constraint requires an author on a
	// non-builtin row, and this is the shape a rollback to an older build leaves behind.
	_, err := dbtest.DB().Exec(t.Context(), `
		INSERT INTO lint_quest_definitions (slug, description, enabled, bounty_points, is_builtin, authored_by)
		VALUES ($1, 'planted by the resolver test', true, 1, true, NULL)
		ON CONFLICT (slug) DO UPDATE SET enabled = true`, slug)
	require.NoError(t, err, "planting an enabled row this build does not implement")
	// The package shares one database, so the row has to be gone for every other test in it.
	t.Cleanup(func() {
		_, _ = dbtest.DB().Exec(context.Background(), `DELETE FROM lint_quest_definitions WHERE slug = $1`, slug)
	})

	_, err = runner.client.lintQuestBoard(nil)
	require.Error(t, err, "the board must not answer from a registry it cannot honour")
	assert.Contains(t, err.Error(), slug, "the error has to name the row an operator must fix")
	assert.Contains(t, err.Error(), "duplicate_performer_url", "and what this build DOES implement")

	// The registry list still answers, and marks the row unimplemented: an operator needs to see the
	// thing that broke the board, and hiding it behind the same error would leave them with nothing.
	defs, err := runner.client.lintQuestDefinitions()
	require.NoError(t, err)
	var planted *lintDefinitionOutput
	for i := range defs {
		if defs[i].Slug == slug {
			planted = &defs[i]
		}
	}
	require.NotNil(t, planted, "the planted row must appear in the registry list")
	assert.True(t, planted.Enabled)
	assert.False(t, planted.Implemented, "no build ships this slug, and the field has to say so")

	// Retiring it is the fix, and it is allowed on purpose: a row a rollback left behind must not be
	// able to keep an instance from serving.
	_, err = dbtest.DB().Exec(t.Context(), `UPDATE lint_quest_definitions SET enabled = false WHERE slug = $1`, slug)
	require.NoError(t, err)
	_, err = runner.client.lintQuestBoard(nil)
	assert.NoError(t, err, "a DISABLED row this build does not implement is not a reason to refuse")
}

// The registry list is an operator's table rather than a curator's work list, and it is the answer to
// "what is this instance actually running" -- legible without a database client.
func TestLintQuestDefinitionsAreAdminOnlyAndNameWhatThisBuildShips(t *testing.T) {
	seedLintRegistry(t)
	_, err := asModify(t).client.lintQuestDefinitions()
	require.Error(t, err, "the registry is an operator surface")
	assert.Contains(t, err.Error(), "not authorized")

	defs, err := asAdmin(t).client.lintQuestDefinitions()
	require.NoError(t, err)
	require.Len(t, defs, 5, "every detector this build implements has a row, and this is the list of them")

	for _, def := range defs {
		assert.True(t, def.IsBuiltin, "%s is a fork builtin", def.Slug)
		assert.True(t, def.Implemented, "%s is implemented by this build, or the board would refuse", def.Slug)
		assert.True(t, def.Enabled, "%s is enabled by default", def.Slug)
		assert.NotEmpty(t, def.Description, "%s needs a description an operator can read", def.Slug)
		assert.Positive(t, def.BountyPoints, "%s needs a price", def.Slug)
	}
}

// A detector that filled its limit has to say so, because a board showing exactly N of a detector that
// found thousands reads as an archive in good shape.
func TestLintQuestBoardSaysWhenADetectorFilledItsLimit(t *testing.T) {
	seedLintRegistry(t)
	runner := asAdmin(t)

	site := lintSite(t)
	shared := "https://lint-graphql-limit.test/" + lintSuffix()
	for i := 0; i < 2; i++ {
		p := lintPerformer(t, fmt.Sprintf("Lint Gql Limit %d %s", i, lintSuffix()), nil)
		lintPerformerURL(t, p, site, shared)
	}

	limit := 1
	reports, err := runner.client.lintQuestBoard(&limit)
	require.NoError(t, err)

	report := findReport(t, reports, "duplicate_performer_url")
	require.Len(t, report.Candidates, 1, "the limit is the limit")
	require.NotNil(t, report.LimitNotice, "a truncated board must not look like a complete one")
	assert.Contains(t, *report.LimitNotice, "1", "the notice says how many it stopped at")
}
