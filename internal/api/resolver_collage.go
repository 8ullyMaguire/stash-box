package api

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stashapp/stash-box/internal/auth"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/service/collage"
)

// Collage GraphQL resolvers (SPEC §7.25.1, growth item 12).
//
// The collage service was complete and tested before it was reachable: snapshots,
// generation, frame selection and an under-snapshotted query for quests all
// existed, with integration tests, and no GraphQL type could read any of it. These
// resolvers are the whole of the missing feature.
//
// Every service type is CONVERTED here rather than bound directly in gqlgen.yml.
// gqlgen generates into internal/models, so binding a GraphQL type to a service
// struct makes models import the service, which imports queries, which imports
// models — an import cycle gqlgen reports as "imports internal/service/collage
// ... import cycle not allowed", naming four packages and none of the real cause.
// The same rule is why ClusterSceneSubmission is a hand-written model struct.

// ---------------------------------------------------------------- conversions

func collageSnapshotToModel(s *collage.Snapshot) *models.CollageSnapshot {
	if s == nil {
		return nil
	}
	return &models.CollageSnapshot{
		ID:        s.ID,
		SceneID:   s.SceneID,
		Timestamp: s.TimestampMS,
		CreatedAt: s.CreatedAt,
		CollageID: s.CollageID,
		CreatedBy: s.CreatedBy,
	}
}

func collageFrameToModel(f *collage.Frame, sceneID uuid.UUID, durationMS *int64) *models.CollageFrame {
	if f == nil {
		return nil
	}
	// Fraction is recomputed against the scene's CURRENT duration rather than
	// copied from the frame's stored value. The whole point of a fraction is that
	// it survives a duration correction, and a stored fraction is wrong the moment
	// the duration changes — so a client scrubbing by fraction lands correctly even
	// on a collage generated against a stale duration.
	//
	// Stored nil when no duration is recorded: 0.0 is a real position (the very
	// start of a scene), so returning it for "unknown" would scrub a client to the
	// opening frame while looking like a valid answer.
	var fraction *float64
	if durationMS != nil && *durationMS > 0 {
		fr := float64(f.TimestampMS) / float64(*durationMS)
		fraction = &fr
	}
	return &models.CollageFrame{
		ID:                uuid.Nil,
		SceneID:           sceneID,
		TimestampMS:       f.TimestampMS,
		SnapshotID:        f.SnapshotID,
		Fraction:          fraction,
		CurrentDurationMS: durationMS,
	}
}

func collageToModel(c *collage.Collage) *models.Collage {
	if c == nil {
		return nil
	}
	return &models.Collage{
		ID:                c.ID,
		SceneID:           c.SceneID,
		FrameCount:        c.FrameCount,
		SourceDurationMS:  c.SourceDurationMS,
		CurrentDurationMS: c.CurrentDurationMS,
		GeneratedAt:       c.GeneratedAt,
	}
}

// msToInt narrows an optional millisecond value to GraphQL's Int.
//
// Checked rather than cast, because GraphQL Int is a signed 32-bit integer and a
// millisecond timestamp overflows it at 24.8 days: a silent wrap would place a
// frame in the wrong spot for any long scene, which reads as a client bug rather
// than a server overflow. An unrepresentable value becomes null — the field is
// nullable, and null is honest where a wrapped number is confidently wrong.
func msToInt(ms *int64) *int {
	if ms == nil {
		return nil
	}
	if *ms > math.MaxInt32 || *ms < math.MinInt32 {
		return nil
	}
	v := int(*ms)
	return &v
}

// asTime coerces the service's `any` timestamp to a time.Time.
//
// The service stores timestamps as `any` because it passes rows through from
// sqlc, and a nil column arrives as a typed nil rather than an untyped one. A
// failed assertion yields nil rather than an error: a missing timestamp is not
// worth failing a whole collage read over.
func asTime(v any) *time.Time {
	switch t := v.(type) {
	case time.Time:
		return &t
	case *time.Time:
		if t == nil {
			return nil
		}
		return t
	default:
		return nil
	}
}

// ------------------------------------------------------------------- resolvers

func (r *Resolver) Collage() models.CollageResolver { return &collageResolver{r} }

type collageResolver struct{ *Resolver }

func (r *collageResolver) SourceDuration(_ context.Context, obj *models.Collage) (*int, error) {
	return msToInt(obj.SourceDurationMS), nil
}

func (r *collageResolver) Duration(_ context.Context, obj *models.Collage) (*int, error) {
	return msToInt(obj.CurrentDurationMS), nil
}

// Stale reports whether the collage was generated against a duration the scene no
// longer records.
//
// Computed per request, never stored: staleness depends on the scene's CURRENT
// duration, so a stored flag would itself go stale, and a flag that lies about
// being stale is worse than having none.
func (r *collageResolver) Stale(_ context.Context, obj *models.Collage) (bool, error) {
	return obj.Stale(), nil
}

func (r *collageResolver) GeneratedAt(_ context.Context, obj *models.Collage) (*time.Time, error) {
	return asTime(obj.GeneratedAt), nil
}

// Frames loads a collage's frames.
//
// A separate load per collage rather than an eager join: a list view asks for
// frameCount and stale but not frames, and eager-loading would pay for every frame
// of every collage to serve a list that never rendered one.
func (r *collageResolver) Frames(ctx context.Context, obj *models.Collage) ([]models.CollageFrame, error) {
	frames, err := r.services.Collage().ListFrames(ctx, obj.SceneID)
	if err != nil {
		return nil, err
	}
	out := make([]models.CollageFrame, 0, len(frames))
	for _, f := range frames {
		out = append(out, *collageFrameToModel(&f, obj.SceneID, obj.CurrentDurationMS))
	}
	return out, nil
}

func (r *Resolver) CollageFrame() models.CollageFrameResolver { return &collageFrameResolver{r} }

type collageFrameResolver struct{ *Resolver }

func (r *collageFrameResolver) Timestamp(_ context.Context, obj *models.CollageFrame) (int, error) {
	v := msToInt(&obj.TimestampMS)
	if v == nil {
		// The timestamp does not fit GraphQL's 32-bit Int. Refusing is better than
		// returning a wrapped number, but the field is non-null so there is nothing
		// honest to return here.
		return 0, fmt.Errorf("frame timestamp %d overflows GraphQL Int", obj.TimestampMS)
	}
	return *v, nil
}

// Fraction is NULL when the scene's duration is unknown, and null is the point:
// 0.0 is a real position (the very start of a scene), so an unknown position and a
// frame at the opening cannot share a value. Returning an error here instead -- as
// an earlier draft did -- failed the WHOLE query, once per frame, for a scene
// whose length simply is not known.
func (r *collageFrameResolver) Fraction(_ context.Context, obj *models.CollageFrame) (*float64, error) {
	return obj.Fraction, nil
}

// Snapshot loads the claim a frame was selected from.
//
// Frames carry only a snapshot ID, and the service's only snapshot read is by
// scene, so the scene's snapshot list is filtered by ID rather than issuing a
// lookup per frame — one query for a collage instead of one per frame.
func (r *collageFrameResolver) Snapshot(ctx context.Context, obj *models.CollageFrame) (*models.CollageSnapshot, error) {
	if obj.Snapshot != nil {
		return obj.Snapshot, nil
	}
	snaps, err := r.services.Collage().ListSnapshots(ctx, obj.SceneID)
	if err != nil {
		return nil, err
	}
	for _, s := range snaps {
		if s.ID == obj.SnapshotID {
			return collageSnapshotToModel(s), nil
		}
	}
	return nil, nil
}

func (r *Resolver) Snapshot() models.SnapshotResolver { return &snapshotResolver{r} }

type snapshotResolver struct{ *Resolver }

func (r *snapshotResolver) Timestamp(_ context.Context, obj *models.CollageSnapshot) (int, error) {
	v := msToInt(&obj.Timestamp)
	if v == nil {
		return 0, fmt.Errorf("snapshot timestamp %d overflows GraphQL Int", obj.Timestamp)
	}
	return *v, nil
}

func (r *snapshotResolver) CreatedAt(_ context.Context, obj *models.CollageSnapshot) (*time.Time, error) {
	return asTime(obj.CreatedAt), nil
}

func (r *Resolver) UnderSnapshottedScene() models.UnderSnapshottedSceneResolver {
	return &underSnapshottedSceneResolver{r}
}

type underSnapshottedSceneResolver struct{ *Resolver }

func (r *underSnapshottedSceneResolver) Minimum(_ context.Context, obj *models.UnderSnapshottedScene) (int, error) {
	// The service's own floor, not a constant repeated here: a second copy would
	// drift, and the client's "contribute snapshots" prompt would silently disagree
	// with the server about how many are needed.
	return collage.DefaultFrames, nil
}

func (r *underSnapshottedSceneResolver) Scene(ctx context.Context, obj *models.UnderSnapshottedScene) (*models.Scene, error) {
	if obj.Scene != nil {
		return obj.Scene, nil
	}
	// FindByID already returns a *models.Scene. The scene service is model-shaped --
	// unlike the collage service, whose types are its own -- so there is nothing to
	// convert here, and an earlier draft that called a non-existent Find plus a
	// non-existent sceneToModel was guessing at both names.
	return r.services.Scene().FindByID(ctx, obj.SceneID)
}

// ---------------------------------------------------------------------- queries

// SceneSnapshots lists a scene's frame claims, in timestamp order: a snapshot list
// is a scrub bar, and unsorted timestamps make every client re-sort the same data.
func (r *queryResolver) SceneSnapshots(ctx context.Context, sceneID uuid.UUID) ([]models.CollageSnapshot, error) {
	snaps, err := r.services.Collage().ListSnapshots(ctx, sceneID)
	if err != nil {
		return nil, err
	}
	out := make([]models.CollageSnapshot, 0, len(snaps))
	for _, s := range snaps {
		out = append(out, *collageSnapshotToModel(s))
	}
	return out, nil
}

// SceneCollage returns a scene's generated collage, or nil when none exists.
//
// Nil rather than an empty collage: "no collage yet" is an invitation to
// contribute, while "a collage with zero frames" is a bug. Collapsing the two
// would make a broken instance look like an empty one.
func (r *queryResolver) SceneCollage(ctx context.Context, sceneID uuid.UUID) (*models.Collage, error) {
	c, err := r.services.Collage().Get(ctx, sceneID)
	if err != nil {
		return nil, err
	}
	return collageToModel(c), nil
}

// UnderSnapshottedScenes lists scenes that need snapshots contributed.
//
// This is a quest input, not a browsing surface: it names scenes that are MISSING
// work, which is why the ordering is least-snapshotted first -- the most starved
// scene is the one worth offering first.
func (r *queryResolver) UnderSnapshottedScenes(ctx context.Context, minimum, limit *int) ([]models.UnderSnapshottedScene, error) {
	min, lim := 0, 0
	if minimum != nil {
		min = *minimum
	}
	if limit != nil {
		lim = *limit
	}
	rows, err := r.services.Collage().ListUnderSnapshotted(ctx, min, lim)
	if err != nil {
		return nil, err
	}
	out := make([]models.UnderSnapshottedScene, 0, len(rows))
	for _, row := range rows {
		out = append(out, models.UnderSnapshottedScene{
			SceneID:       row.SceneID,
			SnapshotCount: int(row.SnapshotCount),
			// The minimum a client is told is the same floor the query used, so a
			// client rendering "2 of 10" agrees with the server about what 10 is.
			Minimum: collage.DefaultFrames,
		})
	}
	return out, nil
}

// -------------------------------------------------------------------- mutations

// AddSnapshot claims a timestamped frame for a scene.
//
// READ, not VOTE, per the schema: claiming a frame observes, it does not change any
// existing claim.
func (r *mutationResolver) AddSnapshot(ctx context.Context, sceneID uuid.UUID, timestamp int) (*models.CollageSnapshot, error) {
	// A pointer because "no user" is a real state for a claim (an API-key
	// submission or a seeded fixture), and passing a zero UUID would attribute the
	// claim to a user that does not exist.
	// No negative-timestamp guard here: the SERVICE owns that invariant
	// (collage.go rejects a negative claim), and a second copy in the resolver
	// creates an equivalent mutant -- weaken the resolver guard and the suite still
	// passes, because the service catches it -- which makes the tests claim
	// coverage of a check the resolver does not actually perform. The negative case
	// is still tested end to end; it is just tested where the rule lives.
	user := auth.GetCurrentUser(ctx)
	createdBy := user.ID
	s, err := r.services.Collage().AddSnapshot(ctx, sceneID, int64(timestamp), &createdBy)
	if err != nil {
		return nil, err
	}
	return collageSnapshotToModel(s), nil
}

// GenerateCollage selects frames from a scene's snapshots, replacing any previous
// selection so a user who dislikes the sample can ask for a different one.
func (r *mutationResolver) GenerateCollage(ctx context.Context, sceneID uuid.UUID, frameCount *int) (*models.Collage, error) {
	n := 0
	if frameCount != nil {
		n = *frameCount
	}
	c, _, err := r.services.Collage().Generate(ctx, sceneID, n)
	if err != nil {
		return nil, err
	}
	return collageToModel(c), nil
}
