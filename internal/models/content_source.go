package models

import (
	"time"

	"github.com/gofrs/uuid"
)

// ContentSource is where a piece of content lives, and whether it is still alive.
//
// This is the aggregator programme's central read model (docs/spec/
// feature-content-aggregator.md): until now every URL in this database recorded where
// a member's OWN file came from, and there was no record of content this instance
// knows about and nobody here owns.
//
// It is deliberately not the generated queries.SceneSource row. The row is shaped by
// the view in migration 108; this is shaped by what a client needs, and the two
// diverge the moment the view changes.
type ContentSource struct {
	// The content_sources row id, and NULLABLE on purpose.
	//
	// Scene.sources reads the scene_sources view, which LEFT JOINs membership against
	// health so a URL the scene claims but whose health row is missing still renders
	// (as 'unknown') instead of vanishing from the page. In that case there is no
	// source row and therefore no id. A client must treat a nil id as "a URL with no
	// health record yet", which is exactly what it is.
	ID *ID `json:"id"`

	// The URL itself. Always present: it is the membership half, which the edit path
	// owns and which no health state can take away.
	URL string `json:"url"`

	// The site this URL belongs to, when the directory knows it. Left as an id here;
	// the Site object is resolved through the dataloader, so a page of scenes does not
	// fetch the same site once per source.
	SiteID uuid.UUID `json:"site_id"`

	// The platform slug, free-form. Null until something sets it; never guessed from
	// the URL, because a guess here becomes a filter a client offers.
	Platform *string `json:"platform"`

	// One of unknown, live, intermittent, dead, dmca, archived. A string rather than a
	// GraphQL enum for now, deliberately: the transition rules (3 consecutive failures
	// to intermittent, 7 days unreachable to dead) are Phase 9's health daemon, and an
	// enum published before the rules exist would freeze a vocabulary nobody has
	// agreed to. Phase 9 turns it into a real enum.
	Status string `json:"status"`

	// Whether this is the source a reader should be sent to. At most one per scene, by
	// a partial unique index rather than by convention.
	Canonical bool `json:"canonical"`

	// When the URL was first seen by this instance. Null for a URL with no health row.
	FirstSeen *time.Time `json:"first_seen"`

	// When the health sweep last looked, and when it last succeeded. These are separate
	// because "checked and failed" and "never checked" are different answers, and
	// because a failure does not change when the source was last good -- that is the
	// only evidence a resurrection queue has to work from.
	LastChecked *time.Time `json:"last_checked"`
	LastOK      *time.Time `json:"last_ok"`

	// Consecutive failures. Reset by a success; the caller owns the threshold.
	CheckFailures int `json:"check_failures"`

	// The archived copy, when one exists. Null otherwise: an archive that was never
	// made is not a state, it is an absence.
	ArchivedURL *string `json:"archived_url"`
}

// ContentSourceBlock is something this instance refuses to point at: a URL a notice named, or a
// whole host (migration 134).
//
// It is the CURRENT policy and not a history -- releasing a block deletes the row, and the
// history is `mod_audit`. That is why there is no "released" flag here: a list of blocks that
// included released ones would make every reader filter for the live policy, and one reader
// would eventually forget to.
//
// Reason is required and is the complainant's own words where there are any (the notice's
// reference, the report's id). It is the field an instance answers a question with, which is
// why it is not optional.
type ContentSourceBlock struct {
	ID ID `json:"id"`

	// "url" or "host". A string rather than an enum for the same reason ContentSource.Status is:
	// the values are a database CHECK constraint today, and a GraphQL enum published before the
	// vocabulary settled is a schema change waiting to happen.
	Kind string `json:"kind"`

	// The key: a normalized URL (pkg/urlkey.Normalize) or a host (pkg/urlkey.SiteHost). Whatever
	// the kind, it is the same spelling the pipeline computes, so a block either matches what the
	// pipeline sees or matches nothing.
	Value string `json:"value"`

	// Why, in the words of whoever asked for it.
	Reason string `json:"reason"`

	// Who blocked it. Nil for a row whose author was deleted, and for one carried over from a
	// denylist an operator migrated by hand.
	CreatedByID ID `json:"created_by_id"`

	CreatedAt time.Time `json:"created_at"`
}
