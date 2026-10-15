package content

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/stashapp/stash-box/internal/config"
)

// The transparency report: what this instance refused, what was contested, and how it answered — counted
// from its own records (phase 16's slice 16a; the report phase 14's published policy promises).
//
// WHY EVERY NUMBER HERE IS A COUNT OF A DECISION. `docs/goal-check.py` states the rule this phase is
// verified by — "Never trust a count written in a document. Recompute it." — and a transparency report is
// exactly the artifact that rule is about. A published figure a reader cannot check is a claim about the
// instance's character; these figures are SELECTs over `content_source_block`,
// `content_source_block_appeal`, `content_sources` and `mod_audit`, and there is nowhere in this repository
// a hand-written number could live undetected (the integration test recomputes each one with its OWN SQL and
// requires agreement, and a mutation that makes any of them constant is killed by it).
//
// TWO KINDS OF NUMBER, KEPT APART ON PURPOSE. The single most common way a transparency report misleads is
// by mixing a state with a period's activity, so they are separate STRUCTS rather than separate sentences:
//
//   - InForce is STATE, with no window. What is refused right now does not expire: a block recorded two
//     years ago is still in force, so putting it inside "last 30 days" would understate it. Read from the
//     content tables, which have no retention.
//   - Activity is a PERIOD, read from `mod_audit`, which DOES have a retention
//     (`mod_audit_retention_days` → DeleteExpiredModAudits). So the window is the retention itself: an
//     activity count can never reach further back than the rows that exist, and a report that used a wider
//     window than the retention would silently under-report every figure in it. `RetentionBounded` says
//     which of the two situations the reader is looking at, and the served page says it in words.
type Report struct {
	// GeneratedAt is when the numbers were computed, so a reader knows the report's age.
	GeneratedAt time.Time `json:"generated_at"`
	// Since is the start of the activity window: the oldest instant an activity count can see.
	Since time.Time `json:"since"`
	// WindowDays is the width of that window, and RetentionBounded is whether that width is a LIMIT the
	// instance imposes (its audit retention) rather than the full history.
	WindowDays         int  `json:"window_days"`
	RetentionBounded   bool `json:"retention_bounded"`
	RecordsKeptForever bool `json:"records_kept_forever"`

	InForce  ReportInForce  `json:"in_force"`
	Activity ReportActivity `json:"activity"`
}

// ReportInForce is what is true at this moment, with no window (see Report's comment).
type ReportInForce struct {
	// Blocks is how many addresses or hosts are refused right now.
	Blocks int64 `json:"blocks_in_force"`
	// RecordsOutOfService is how many catalogue records were taken out of service. These are two different
	// facts — one refusal can take many records out of service, and a refusal against an address this
	// instance never recorded takes none — which is why a report shows both rather than one "takedowns".
	RecordsOutOfService int64 `json:"records_out_of_service"`

	AppealsFiled   int64 `json:"appeals_filed"`
	AppealsPending int64 `json:"appeals_pending"`
	AppealsDecided int64 `json:"appeals_decided"`
	// AppealsGranted and AppealsDenied are what an appellant actually wants to know: of the contests that
	// were heard, how many succeeded. They are read from the appeal rows and not from the audit rows,
	// because a granted appeal against a block somebody had already released changes nothing and counting
	// it as a reversal would overstate this instance's reversals.
	AppealsGranted int64 `json:"appeals_granted"`
	AppealsDenied  int64 `json:"appeals_denied"`
}

// ReportActivity is the window's decisions (see Report's comment).
type ReportActivity struct {
	BlocksRecorded int64 `json:"blocks_recorded"`
	AppealsFiled   int64 `json:"appeals_filed"`
	AppealsDecided int64 `json:"appeals_decided"`
	// Refusals and Releases are read from `mod_audit` and separated by the DECISION the row records, not by
	// its action: a release reuses the takedown's action with `data.released = true` (migration 134's
	// "one value per decision"), so counting actions would report every release as a new refusal.
	Refusals int64 `json:"refusals"`
	Releases int64 `json:"releases"`
}

// unboundedRetentionWindowDays is the activity window when the instance keeps its moderation records
// indefinitely. A retention of 0 or less disables `DeleteExpiredModAudits` (the same `> 0` guard
// `internal/service/edit` uses), so there is no window to derive from the configuration — the report covers
// a decade, which is what "everything this instance holds" means for an instance that has existed for
// months, and `RecordsKeptForever` tells the reader which claim is being made rather than leaving a number
// to imply it.
const unboundedRetentionWindowDays = 3650

// TransparencyReport computes the report (spec §16, plan's slice 16a).
//
// The window is derived from the AUDIT RETENTION rather than from a second config key: two numbers naming
// the same period is one number that will disagree with itself, and the retention is the one that decides
// what any activity count can possibly see.
func (s *Service) TransparencyReport(ctx context.Context) (Report, error) {
	now := time.Now().UTC()
	windowDays, bounded, keptForever := reportWindow(config.GetModAuditRetentionDays())
	since := now.AddDate(0, 0, -windowDays)

	row, err := s.queries.ContentModerationReport(ctx, pgtype.Timestamptz{Time: since, Valid: true})
	if err != nil {
		return Report{}, err
	}

	return Report{
		GeneratedAt:        now,
		Since:              since,
		WindowDays:         windowDays,
		RetentionBounded:   bounded,
		RecordsKeptForever: keptForever,
		InForce: ReportInForce{
			Blocks:              row.BlocksInForce,
			RecordsOutOfService: row.RecordsOutOfService,
			AppealsFiled:        row.AppealsFiled,
			AppealsPending:      row.AppealsPending,
			AppealsDecided:      row.AppealsDecided,
			AppealsGranted:      row.AppealsGranted,
			AppealsDenied:       row.AppealsDenied,
		},
		Activity: ReportActivity{
			BlocksRecorded: row.BlocksRecordedInWindow,
			AppealsFiled:   row.AppealsFiledInWindow,
			AppealsDecided: row.AppealsDecidedInWindow,
			Refusals:       row.RefusalsInWindow,
			Releases:       row.ReleasesInWindow,
		},
	}, nil
}

// reportWindow turns the audit retention into the activity window.
//
// Three outcomes and they are all real states of a deployed instance: a positive retention bounds the window
// (the usual case, 30 days by default); a retention of 0 or less means the rows are never deleted, so the
// window is nominal and the report says the records are kept forever; and a NEGATIVE retention is treated as
// "off" like the `> 0` guards elsewhere in this repository, rather than as a window that ends in the future.
func reportWindow(retentionDays int) (days int, bounded bool, keptForever bool) {
	if retentionDays > 0 {
		return retentionDays, true, false
	}
	return unboundedRetentionWindowDays, false, true
}
