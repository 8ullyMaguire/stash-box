package metrics

import (
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/queries"
)

// The metric list, tested as a structure rather than as numbers.
//
// The numbers are the database's business, and `internal/api/metrics_integration_test.go` recomputes every
// one of them with its own SQL against a real instance. What cannot be checked there is the WIRING: that every
// figure the statement returns is served, that none is served twice, and that each one carries the definition
// that decides what it means. That is what this file is for, and it is why `metricList` is a pure function of
// the row.

// goldenKeys is the list, written out. A golden sequence rather than a count, so a DROPPED metric or a
// REORDERED one fails rather than merely a length mismatch — and so a reviewer of a diff sees exactly which
// figure moved.
var goldenKeys = []string{
	"members_total",
	"members_active_window",
	"members_created_window",
	"members_invited",
	"scenes_total",
	"scenes_created_window",
	"scenes_with_sources",
	"scenes_with_fingerprint",
	"fingerprints_total",
	"tags_total",
	"tag_implications_total",
	"sources_total",
	"sources_checked_window",
	"sources_unknown",
	"edits_pending",
	"edits_decided_window",
	"identification_candidates_pending",
	"invites_issued",
	"invites_unused",
}

func keys(list []Metric) []string {
	out := make([]string, 0, len(list))
	for _, m := range list {
		out = append(out, m.Key)
	}
	return out
}

func TestTheMetricListServesEveryColumnTheStatementReturns(t *testing.T) {
	list := metricList(queries.InstanceMetricsRow{})

	// THE STRUCTURAL HALF, and the one that cannot be written by hand: the statement has N figures and the
	// list must serve N. A figure added to the SQL and forgotten here would otherwise be computed on every
	// request and delivered to nobody, which no test of a value could notice.
	fieldCount := reflect.TypeOf(queries.InstanceMetricsRow{}).NumField()
	assert.Equal(t, fieldCount, len(list),
		"the statement returns %d figures and the list serves %d: every one it computes must be served, or the "+
			"instance pays for a count nobody can read", fieldCount, len(list))

	assert.Equal(t, goldenKeys, keys(list),
		"the served keys, in order: a difference here is a metric dropped, duplicated or moved")
}

func TestEveryMetricCarriesItsUnitAndItsDefinition(t *testing.T) {
	seen := map[string]bool{}
	for _, m := range metricList(queries.InstanceMetricsRow{}) {
		require.False(t, seen[m.Key], "the key %q is served twice: two figures under one name is one figure "+
			"nobody can check", m.Key)
		seen[m.Key] = true

		assert.NotEmpty(t, m.Key, "a metric with no key cannot be read by a client")
		assert.Equal(t, UnitCount, m.Unit,
			"every figure here is a count; the unit is served so a client does not have to know that")

		// A DEFINITION IS A SENTENCE, and these three assertions are what a sentence has that a placeholder
		// does not: length, a full stop, and a capital. The failure they are for is a metric wired in a hurry
		// with 'TODO' or an empty string, which renders as a number with no meaning -- the exact defect this
		// package's whole design is against.
		assert.NotEmpty(t, strings.TrimSpace(m.Definition), "%s carries no definition", m.Key)
		assert.True(t, strings.HasSuffix(m.Definition, "."),
			"%s's definition is not a sentence: %q", m.Key, m.Definition)
		assert.Greater(t, len(m.Definition), 30,
			"%s's definition is too short to decide what it counts: %q", m.Key, m.Definition)
	}
}

func TestAWindowedMetricSaysSoInItsOwnSentence(t *testing.T) {
	list := metricList(queries.InstanceMetricsRow{})
	byKey := map[string]Metric{}
	for _, m := range list {
		byKey[m.Key] = m
	}

	// THE WINDOW IS THE ONE THING A READER CANNOT GUESS, and it is not in the key's name for the figures whose
	// names are about state rather than activity. So the rule is: a metric whose key says "window" must mention
	// the window in its definition, and the metric whose name would otherwise read as a lifetime total
	// (members_active) must say which span it covers. A metric keyed "window" whose sentence did not say so
	// would be a figure an operator reads as "now".
	for _, m := range list {
		if !strings.Contains(m.Key, "window") {
			continue
		}
		assert.Contains(t, m.Definition, "window",
			"%s is a windowed figure and its definition does not say so: %q", m.Key, m.Definition)
	}

	assert.Contains(t, byKey["members_active_window"].Definition, "last_api_call",
		"the activity figure names the SIGNAL it uses, because this instance keeps no session log and a reader "+
			"would otherwise assume it counts every visit")
	assert.Contains(t, byKey["sources_unknown"].Definition, "unknown",
		"and the sweep's coverage figure names the status it counts, so a zero is not read as 'no sources'")

	// And the figures that are NOT windowed do not claim to be, in both directions: a definition mentioning a
	// window on a lifetime total is as wrong as the reverse.
	assert.NotContains(t, byKey["scenes_total"].Definition, "window",
		"a lifetime total must not describe itself as bound to the window")
}
