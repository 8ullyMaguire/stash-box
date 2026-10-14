package models

import (
	"time"

	"github.com/gofrs/uuid"
)

// TagImplication is one row of the graph, by name: "tag implies implied".
//
// The graph itself is many-to-many and is never a field on Tag -- it is a separate table so
// that adding an implication does not rewrite a tag row, and so that one tag can imply many
// while being implied by many. See docs/spec/feature-tag-ontology.md.
type TagImplication struct {
	TagID        uuid.UUID `json:"tag_id"`
	TagName      string    `json:"tag_name"`
	ImpliedTagID uuid.UUID `json:"implied_tag_id"`
	ImpliedName  string    `json:"implied_name"`
	CreatedAt    time.Time `json:"created_at"`
}

// TagAtDepth is a tag with how far it is from the tag that was asked about.
//
// `depth` is what makes a flat list usable: without it a client cannot tell a direct
// implication from something three levels down without walking the graph itself. 1 is a
// direct implication.
type TagAtDepth struct {
	Tag   Tag `json:"tag"`
	Depth int `json:"depth"`
}

// OntologyVersion is one version of the tag ontology.
//
// Snapshot is the stored JSON document as text, because that is what the GraphQL type
// publishes: the document is a list of {"tag": ..., "implies": ...} pairs by NAME, so it is
// readable and diffable rather than a wall of ids.
type OntologyVersion struct {
	Version   int       `json:"version"`
	Snapshot  string    `json:"snapshot,omitempty"`
	Note      *string   `json:"note,omitempty"`
	CreatedAt time.Time `json:"created"`
}
