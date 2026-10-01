package sdbimport

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// SyncMode decides which side wins a field that differs.
type SyncMode string

const (
	// ModeLatest keeps whichever value was written most recently. It is the
	// default because it needs no configuration and is right for the common
	// case: a curator fixes a typo here, upstream fixes the same field later,
	// and the last correction is the one you want.
	//
	// It is also the only mode that CANNOT be decided from the data alone -- see
	// provenance() for why, and for what it costs.
	ModeLatest SyncMode = "latest-wins"

	// ModeUpstream overwrites local unconditionally. Cheapest and most
	// predictable, and the only mode with no provenance requirement.
	ModeUpstream SyncMode = "upstream-wins"

	// ModeLocal preserves local edits and fills only fields that are empty. Also
	// needs no provenance: "is this field empty?" is answerable from the row.
	ModeLocal SyncMode = "local-wins"
)

// ParseMode validates a mode name. An unrecognised mode is an error rather than a
// default: silently running `upstream-wins` because someone typo'd
// `upstream-winss` would overwrite curated data on 110,000 records.
func ParseMode(s string) (SyncMode, error) {
	switch SyncMode(strings.TrimSpace(s)) {
	case ModeLatest:
		return ModeLatest, nil
	case ModeUpstream:
		return ModeUpstream, nil
	case ModeLocal:
		return ModeLocal, nil
	case "":
		return ModeLatest, nil
	}
	return "", fmt.Errorf("unknown sync mode %q: want %s, %s or %s", s, ModeLatest, ModeUpstream, ModeLocal)
}

// FieldChange is one field that differs between upstream and local.
//
// Old is what the destination holds now, New is what the sync would write. Both
// are kept in the report so a dry run can be read without a second query, which
// matters when the report covers tens of thousands of fields.
type FieldChange struct {
	EntityID string
	Field    string
	Old      string
	New      string
}

// SyncReport is what a run did, or would have done under -dry-run.
type SyncReport struct {
	Scanned   int
	Created   int
	Unchanged int
	Updated   int
	Skipped   int
	Failed    int

	// StoppedAt is the `updated` value of the first record older than the
	// watermark, i.e. the new watermark. Zero when the walk ran out of records.
	StoppedAt time.Time

	// ReScanned counts records re-examined because they fell inside the safety
	// margin. Non-zero is normal and cheap; a persistently large number means the
	// margin is too small for the source's update rate.
	ReScanned int

	Changes []FieldChange
}

// summary is the one-line form used in logs.
func (r *SyncReport) summary() string {
	return fmt.Sprintf("scanned %d  created %d  updated %d  unchanged %d  skipped %d  failed %d",
		r.Scanned, r.Created, r.Updated, r.Unchanged, r.Skipped, r.Failed)
}

// shouldStop reports whether the walk has passed the watermark.
//
// The margin is the whole reason this function is not a bare `t.Before(wm)`.
// Recency ordering on the source is OBSERVED, not documented: I verified three
// pages came back monotonically decreasing and that is not a specification. If
// two records share an `updated` value and their relative order is unstable, a
// strict comparison drops whichever one landed on the wrong side of the
// boundary, and that row is then never revisited.
//
// Re-reading an hour of upstream changes costs one extra page or two. Silently
// skipping a boundary row costs a record that is wrong forever, because the next
// run's watermark has already moved past it.
func shouldStop(updated, watermark time.Time, margin time.Duration) bool {
	if watermark.IsZero() {
		return false
	}
	return updated.Before(watermark.Add(-margin))
}

// listEq compares two string lists as SETS.
//
// Order is not meaningful for aliases or URLs -- the same two URLs in a different
// order is the same performer -- so an ordered comparison would report a change
// on every single run and rewrite records that never actually changed.
func listEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	as := append([]string(nil), a...)
	bs := append([]string(nil), b...)
	sort.Strings(as)
	sort.Strings(bs)
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}

// normPtr renders an optional field for comparison and for the report.
//
// A nil and a pointer-to-empty are different states in the database but the same
// statement to a human, and treating them as different would report a change on
// every field the source has never set.
func normPtr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
