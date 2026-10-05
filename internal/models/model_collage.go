package models

import "github.com/gofrs/uuid"

// Collage types (SPEC §7.25.1, growth item 12).
//
// These live in `models` rather than binding directly to
// `internal/service/collage` because gqlgen generates `generated_exec.go` INTO
// this package: binding a GraphQL type to a service struct makes models import
// the service, and the service imports `queries`, which imports models. That is
// an import cycle, and gqlgen reports it as
// "package internal/models imports internal/service/collage ... import cycle not
// allowed" — an error that names four packages and none of the real cause.
//
// `ClusterSceneSubmission` follows the same rule: a view type the service
// produces gets a model struct here, and the resolver converts. Two conversions
// to keep is a fair price for not inverting the whole dependency graph.

// CollageSnapshot is one timestamped frame claim for a scene.
//
// Timestamp is milliseconds, matching how snapshots are stored and how a client
// seeks. The scene's own duration is an integer second count, so converting here
// would only add a rounding step between the claim and the seek.
type CollageSnapshot struct {
	ID        uuid.UUID
	SceneID   uuid.UUID
	Timestamp int64
	CreatedAt any
	CollageID *uuid.UUID
	CreatedBy *uuid.UUID
}

// CollageFrame is one selected frame of a generated collage.
//
// Fraction is carried alongside TimestampMS because a client holding a different
// duration for the scene should scrub by fraction and land in the right place
// regardless — see CollageFrame.Fraction.
type CollageFrame struct {
	ID          uuid.UUID
	TimestampMS int64
	SnapshotID  uuid.UUID
	// Fraction is the position through the scene, 0.0-1.0, recomputed against
	// CurrentDurationMS on every read rather than stored.
	//
	// A POINTER, and that is the whole point: 0.0 is a real position (the very
	// start of a scene), so "unknown" has to be nil. As a plain float64 an unknown
	// fraction and a frame at the opening were the same value, and a client
	// scrubbing by fraction would be told to go to the start with no way to tell it
	// was guessing.
	Fraction *float64
	// SceneID is carried on the frame so the snapshot back-reference can be
	// resolved without loading the collage first.
	SceneID uuid.UUID
	// CurrentDurationMS is the scene's duration NOW, which is what the fraction is
	// computed against. Nil when the scene has no recorded duration.
	CurrentDurationMS *int64
	// Snapshot is the claim this frame came from, loaded by the resolver.
	Snapshot *CollageSnapshot
}

// Collage is a generated selection of frames for a scene.
//
// SourceDurationMS and CurrentDurationMS both exist because they differ
// routinely, and a collage generated against a since-corrected duration has its
// frames bunched at the end. Recording both is what makes that diagnosable
// instead of merely looking wrong.
type Collage struct {
	ID         uuid.UUID
	SceneID    uuid.UUID
	FrameCount int
	// SourceDurationMS is what the sampler believed. CurrentDurationMS is what the
	// scene records now. Both nil when the scene had no duration.
	SourceDurationMS  *int64
	CurrentDurationMS *int64
	GeneratedAt       any
	// Frames is loaded by the resolver, not carried on the row.
	Frames []*CollageFrame
}

// Stale reports whether the collage was generated against a different duration
// than the scene records now.
//
// False when either duration is unknown: an unknown duration is not evidence of a
// mismatch, and reporting "stale" for every scene with no duration recorded would
// make the flag meaningless.
func (c *Collage) Stale() bool {
	if c.SourceDurationMS == nil || c.CurrentDurationMS == nil {
		return false
	}
	return *c.SourceDurationMS != *c.CurrentDurationMS
}

// UnderSnapshottedScene is a scene with fewer snapshots than a collage needs.
//
// This is a quest input rather than a discovery surface: it names scenes that are
// MISSING snapshots, which is a work item, not something to browse.
type UnderSnapshottedScene struct {
	SceneID       uuid.UUID
	Scene         *Scene
	SnapshotCount int
	Minimum       int
}
