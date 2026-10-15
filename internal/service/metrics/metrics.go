// Package metrics serves the instance's own counters (spec §16.2, plan phase 16 slice 2).
//
// A METRIC IS A NUMBER PLUS A DEFINITION, and this package exists to keep the two inseparable. Two readers of
// a bare "scenes_total" who disagree about whether deleted scenes are counted are reading two different
// figures, and nothing in a response containing only the number would tell them so. So every figure is served
// with its own sentence, and the sentence lives HERE, beside the accessor that reads its column — adding a
// metric means wiring a column explicitly, and the unit test refuses a list whose length disagrees with the
// statement's.
//
// WHY NOTHING IS COUNTED AT A MUTATION SITE. A counter incremented by a handler is a second write path, and
// when it disagrees with the table it counts nothing detects it. Every figure here is instead DERIVED from
// rows the features already write, so a metric cannot be wrong about the record without the record being
// wrong. The cost is stated in the metrics themselves: activity is `users.last_api_call`, the only activity
// signal this instance keeps, and the metric that reads it says so.
package metrics

import (
	"context"
	"time"

	"github.com/stashapp/stash-box/internal/queries"
)

// WindowDays is the ONE window every windowed figure uses.
//
// Served as WindowDays alongside Since, because a response in which each metric carried its own window would
// be a table nobody could read across. Sixty days is not the number: what matters is that there is one, and
// that a reader can see which figures it applies to — the figures it does NOT apply to say so in their own
// definitions, since a reader scanning one metric needs that metric's sentence and not a section heading.
const WindowDays = 30

// UnitCount is the only unit served today, and it is served rather than assumed: a figure that is later a
// fraction or a duration must not be mistaken for a count by a client that had no way to tell.
const UnitCount = "count"

// Metric is one figure with the definition that decides what it counts.
type Metric struct {
	Key string
	// Unit is what the number IS, so a client can format it without knowing the key.
	Unit string
	// Definition is one sentence: what this counts, which rows it excludes, and — where a reader would
	// otherwise assume it — the window it is bound to.
	Definition string
	Value      int64
}

// Metrics is the whole response: the figures, and the frame they were taken in.
type Metrics struct {
	GeneratedAt time.Time
	Since       time.Time
	WindowDays  int
	Metrics     []Metric
}

// Service reads the counters.
type Service struct {
	queries *queries.Queries
}

// NewService creates a metrics service.
func NewService(queries *queries.Queries) *Service {
	return &Service{queries: queries}
}

// InstanceMetrics computes every figure in ONE statement, so all of them are a snapshot of the same instant.
//
// One statement rather than one query per metric, and the reason is the relations BETWEEN the figures: a walk
// or a page that fetched them one at a time would read a moving table, and a check like "no more members were
// active than exist" would hold by luck rather than by construction.
func (s *Service) InstanceMetrics(ctx context.Context) (Metrics, error) {
	now := time.Now().UTC()
	since := now.AddDate(0, 0, -WindowDays)

	row, err := s.queries.InstanceMetrics(ctx, since)
	if err != nil {
		return Metrics{}, err
	}

	return Metrics{
		GeneratedAt: now,
		Since:       since,
		WindowDays:  WindowDays,
		Metrics:     metricList(row),
	}, nil
}

// metricList is the ordered list, and it is a pure function of the row so that its shape can be tested without
// a database.
//
// EVERY FIGURE THE STATEMENT RETURNS APPEARS HERE EXACTLY ONCE, and the unit test enforces both halves of
// that: the length is compared against the row's own field count by reflection (so a figure added to the SQL
// and forgotten here fails), and the key sequence is asserted (so a metric dropped, duplicated or reordered
// fails too). The compiler would catch a field that does not exist; only this catches a field nobody serves.
//
// The DEFINITIONS are the parameter worth reading: each one names what it counts, what it excludes, and — for
// a windowed figure — that it is bound to the window. A figure whose sentence does not fit on one line is a
// figure whose meaning is not one thing.
func metricList(row queries.InstanceMetricsRow) []Metric {
	return []Metric{
		metric("members_total", row.MembersTotal,
			"Accounts, excluding the instance's own bots (user_roles.role = 'BOT') and the [deleted user] placeholder row."),
		metric("members_active_window", row.MembersActiveWindow,
			"Accounts inside the window whose last authenticated call (users.last_api_call) falls in it -- the only activity signal this instance keeps, so a member who reads without calling the API does not appear here."),
		metric("members_created_window", row.MembersCreatedWindow,
			"Accounts created inside the window."),
		metric("members_invited", row.MembersInvited,
			"Accounts that arrived through an invite (users.invited_by is set) -- the invite chain counted rather than asserted."),

		metric("scenes_total", row.ScenesTotal,
			"Catalogue scenes not marked deleted."),
		metric("scenes_created_window", row.ScenesCreatedWindow,
			"Scenes created inside the window."),
		metric("scenes_with_sources", row.ScenesWithSources,
			"Scenes holding at least one recorded source address (content_sources) -- the aggregation coverage this programme exists for."),
		metric("scenes_with_fingerprint", row.ScenesWithFingerprint,
			"Scenes carrying at least one fingerprint -- what the duplicate detection can actually compare."),
		metric("fingerprints_total", row.FingerprintsTotal,
			"Fingerprint rows, one per fingerprint and scene pair."),
		metric("tags_total", row.TagsTotal,
			"Tags in the catalogue -- the vocabulary every edit is made against."),
		metric("tag_implications_total", row.TagImplicationsTotal,
			"Tag implications -- the ontology's edges."),

		metric("sources_total", row.SourcesTotal,
			"Recorded source addresses, whatever their health."),
		metric("sources_checked_window", row.SourcesCheckedWindow,
			"Source addresses whose health was last checked inside the window (content_sources.last_checked)."),
		metric("sources_unknown", row.SourcesUnknown,
			"Source addresses no health check has ever looked at (status = 'unknown'); zero here means the sweep has seen everything this instance holds."),

		metric("edits_pending", row.EditsPending,
			"Edits awaiting a decision (edits.status = 'PENDING')."),
		metric("edits_decided_window", row.EditsDecidedWindow,
			"Edits that reached a decision inside the window (edits.closed_at), whoever decided them and whether they were accepted."),
		metric("identification_candidates_pending", row.IdentificationCandidatesPending,
			"Candidate identifications waiting for a moderator; the table IS the queue, so every row counts."),
		metric("invites_issued", row.InvitesIssued,
			"Invite keys generated by members, whether or not they were ever used."),
		metric("invites_unused", row.InvitesUnused,
			"Invite keys with no use recorded."),
	}
}

func metric(key string, value int64, definition string) Metric {
	return Metric{Key: key, Unit: UnitCount, Definition: definition, Value: value}
}
