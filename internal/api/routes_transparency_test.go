package api

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/stashapp/stash-box/internal/service/content"
	"github.com/stashapp/stash-box/internal/service/mediaplane"
)

// The transparency report's PAGE (phase 16, slice 16a), tested as a function of the report it renders.
//
// No database and no HTTP server here on purpose: the numbers themselves are verified against the tables in
// `transparency_integration_test.go`, where they can be recomputed. What is left is the one thing a
// rendering test is uniquely able to catch — a page that disagrees with the report it was handed.

func renderedReport(report content.Report) string {
	return renderTransparencyPage(report)
}

func TestTheTransparencyPageTellsTheTruthAboutItsOwnWindow(t *testing.T) {
	bounded := content.Report{
		GeneratedAt:      time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC),
		WindowDays:       30,
		RetentionBounded: true,
	}
	page := renderedReport(bounded)

	assert.Contains(t, page, "2026-10-16T12:00:00Z",
		"the report's age is stated: a reader of a cached page can see when it was computed")
	assert.Contains(t, page, "the last 30 days", "and the window it covers is named")
	assert.Contains(t, page, "mod_audit_retention_days",
		"with the key that decides it, so the reader can check the claim against the configuration they run")
	assert.NotContains(t, page, "keeps its moderation records indefinitely",
		"a bounded retention must NOT be described as indefinite")

	// The other half of the same pair: an instance with no retention describes itself differently, and the
	// figures are labelled as everything it holds rather than as a period. A page that said "the last 3650
	// days" here would be technically true and useless.
	unbounded := content.Report{
		GeneratedAt:        time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC),
		WindowDays:         3650,
		RetentionBounded:   false,
		RecordsKeptForever: true,
	}
	unboundedPage := renderedReport(unbounded)
	assert.Contains(t, unboundedPage, "keeps its moderation records indefinitely")
	assert.NotContains(t, unboundedPage, "in the last 3650 days",
		"the nominal window is not presented as a period the instance chose")
}

func TestTheTransparencyPageShowsThePostureTheProcessLogsAndEveryFigureTheReportCarries(t *testing.T) {
	report := content.Report{
		GeneratedAt: time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC),
		WindowDays:  30,
		InForce: content.ReportInForce{
			Blocks: 1234, RecordsOutOfService: 7, AppealsFiled: 12,
			AppealsPending: 2, AppealsDecided: 10, AppealsGranted: 4, AppealsDenied: 6,
		},
		Activity: content.ReportActivity{
			BlocksRecorded: 5, AppealsFiled: 3, AppealsDecided: 1, Refusals: 9, Releases: 2,
		},
	}
	page := renderedReport(report)

	// THE FIGURES, EACH ONE, IN THE FORM A READER READS: 1,234 rather than 1234, and the two appeals that
	// were denied shown apart from the four that were granted (a page showing only "10 decided" would answer
	// the question an appellant actually has with a number that does not address it).
	for _, expected := range []string{">1,234<", ">7<", ">12<", ">4<", ">6<", ">9<", ">2<"} {
		assert.Contains(t, page, expected, "the report's figure is on the page")
	}

	// THE POSTURE IS READ FROM THE SOURCE, NOT RESTATED: this assertion compares the page against the very
	// function the process logs at startup, so the two cannot drift while both look correct in isolation.
	assert.Contains(t, page, mediaplane.PostureLine(),
		"the page states this instance's content posture, from the same source the startup log uses")

	// And it is a PAIR WITH THE POLICY rather than a page on its own: the reader who wants to know what the
	// process is, or how to contest a refusal, is one click from it -- and the figures are offered as JSON
	// so they can be checked rather than believed.
	assert.Contains(t, page, `href="/policy"`)
	assert.Contains(t, page, `href="/transparency/json"`)
}

func TestFiguresAreGroupedSoASixFigureCountCanBeRead(t *testing.T) {
	// Small enough to look trivial, and it is the difference between a number and a smear of digits on the
	// one page in this programme whose job is to be read by somebody checking up on the operator.
	assert.Equal(t, "0", count(0))
	assert.Equal(t, "9", count(9))
	assert.Equal(t, "999", count(999))
	assert.Equal(t, "1,000", count(1000))
	assert.Equal(t, "12,345", count(12345))
	assert.Equal(t, "1,234,567", count(1234567))
	assert.Equal(t, "-1,234", count(-1234))
}

func TestThePageNamesTheDistinctionsItsFiguresDependOn(t *testing.T) {
	page := renderedReport(content.Report{GeneratedAt: time.Now(), WindowDays: 30, RetentionBounded: true})

	// The two sentences that make the numbers usable, asserted because a report whose figures cannot be read
	// is a report that will be misread: that a release and a refusal are counted apart, and that the state
	// figures have no window (which is the difference between "how many are refused" and "how many were
	// refused this month", the exact confusion these pages are notorious for).
	assert.True(t, strings.Contains(page, "A release is not a new refusal"),
		"the release/refusal distinction is stated where the figures are")
	assert.Contains(t, page, "no window",
		"and so is the fact that a refusal does not expire out of the in-force figures")
}
