// Package collage implements snapshot collages from SPEC §8.
//
// The design in one paragraph: a snapshot is a TIMESTAMP into a video, not a
// stored image, because Stash Box is a metadata server that does not host media.
// A collage is a DERIVED selection of 12-24 evenly spaced snapshots over a scene's
// duration, regenerated rather than mutated. The snapshots are the source of
// truth; the collage row is a cache of a selection over them.
//
// What this package does NOT do, deliberately: it does not fetch video, decode
// frames, or store images. Everything a client needs to render a collage is a
// scene id and a list of millisecond offsets, which is why SPEC §8 can promise
// that collages are replicated across the mesh "even when full content is not" --
// 24 timestamps replicate in 192 bytes.
package collage

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/stashapp/stash-box/internal/queries"
)

// Frame count bounds from SPEC §8, and the same bounds the database CHECK
// constraint enforces. Duplicated deliberately: the constraint protects the table
// from a hand-written INSERT, this protects the sampler from producing a value it
// would then fail to store, and a test asserts the two agree.
const (
	MinFrames = 12
	MaxFrames = 24
	// DefaultFrames is what Generate uses when the caller does not choose. 16 sits
	// mid-range: enough that adjacent frames are distinguishable when a user is
	// trying to identify a scene, few enough that a collage is legible on a phone.
	DefaultFrames = 16
)

// ErrSceneNotFound is returned when a scene does not exist or is deleted.
//
// A distinct error rather than "no snapshots", because the two produce completely
// different client behaviour: a missing scene is an error and a scene with no
// snapshots is an empty state.
var ErrSceneNotFound = errors.New("scene not found")

// ErrDuplicateSnapshot is returned when a snapshot already exists at that instant.
var ErrDuplicateSnapshot = errors.New("a snapshot already exists at that timestamp")

// ErrNotEnoughSnapshots is returned when a scene has too few snapshots to collage.
//
// A distinct error, not an empty collage. SPEC §8 promises 12-24 frames; returning
// fewer would be a collage that silently violates its own contract and renders as
// a broken strip, and the honest answer is that the scene needs more snapshots.
var ErrNotEnoughSnapshots = errors.New("not enough snapshots to generate a collage")

// ErrNoDuration is returned when a scene has no duration to sample across.
//
// Distinct because the fix is specific and the UI can say it: a scene with no
// duration needs one before a collage can be generated, and a user who has just
// been shown an empty collage learns nothing.
var ErrNoDuration = errors.New("scene has no duration, so snapshots cannot be spaced")

// Strategy names a sampling method, stored on the collage row.
type Strategy string

const (
	// StrategyUniform spaces frames evenly across the scene's duration. The
	// default, and the one SPEC §8 describes ("evenly spaced frames").
	StrategyUniform Strategy = "uniform"
	// StrategyHead is a placeholder for a future strategy that favours the
	// opening, which is where a scene's title card and its most distinctive
	// establishing shot usually are. Named now so the column is not a bare string
	// and so a second strategy is an addition rather than a reinterpretation of the
	// first -- see UniformFrames for why that matters.
	StrategyHead Strategy = "head"
)

// Snapshot is one timestamped frame claim.
type Snapshot struct {
	ID          uuid.UUID
	SceneID     uuid.UUID
	TimestampMS int64
	// CollageID is nil when the snapshot is not part of a generated collage.
	CollageID *uuid.UUID
	CreatedBy *uuid.UUID
	CreatedAt any
}

// Collage is a generated selection of snapshots.
type Collage struct {
	ID         uuid.UUID
	SceneID    uuid.UUID
	FrameCount int
	Strategy   Strategy
	// SourceDurationMS is the duration the sampler believed when it ran, and
	// CurrentDurationMS is what the scene records now. They differ routinely, and
	// a collage generated against a since-corrected duration has its frames
	// bunched at the end. Recording both is what makes that diagnosable instead of
	// merely looking wrong.
	SourceDurationMS  *int64
	CurrentDurationMS *int64
	GeneratedAt       any
}

// Service manages snapshots and collages.
type Service struct {
	queries *queries.Queries
	withTxn queries.WithTxnFunc
}

// NewCollage creates a new collage service.
func NewService(queries *queries.Queries, withTxn queries.WithTxnFunc) *Service {
	return &Service{queries: queries, withTxn: withTxn}
}

// WithTxn executes a function within a transaction.
func (c *Service) WithTxn(fn func(*queries.Queries) error) error {
	return c.withTxn(fn)
}

// AddSnapshot records a snapshot at a timestamp.
//
// Refuses a duplicate instant rather than relying on the unique constraint to
// fail: the constraint produces a 23505 that names an index, and "a snapshot
// already exists at 12:34" is a message a user can act on.
func (c *Service) AddSnapshot(ctx context.Context, sceneID uuid.UUID, timestampMS int64, createdBy *uuid.UUID) (*Snapshot, error) {
	if timestampMS < 0 {
		return nil, fmt.Errorf("a snapshot timestamp cannot be negative (got %d)", timestampMS)
	}

	id, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	row, err := c.queries.CreateSceneSnapshot(ctx, queries.CreateSceneSnapshotParams{
		ID:          id,
		SceneID:     sceneID,
		TimestampMs: timestampMS,
		CreatedBy:   nullUUID(createdBy),
	})
	if err != nil {
		// 23505 is unique_violation. Checked by the SQLSTATE rather than by a pgx
		// error type because the constraint is what enforces this, and a test that
		// asserts on the pgx type would break if pgx changed how it classifies
		// errors -- while the SQLSTATE is the contract the database actually offers.
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("%w at %dms", ErrDuplicateSnapshot, timestampMS)
		}
		return nil, err
	}
	return snapshotFromRow(row), nil
}

// DeleteSnapshot removes a snapshot.
//
// A snapshot inside a collage is NOT refused. SPEC §8's collage is a selection, so
// removing a frame leaves a collage with one fewer than its frame_count, which
// ListFrames reports honestly rather than padding. Refusing would mean a user
// could not correct a bad frame, which is the one thing they most need to do.
func (c *Service) DeleteSnapshot(ctx context.Context, id uuid.UUID) error {
	return c.queries.DeleteSceneSnapshot(ctx, id)
}

// ListSnapshots returns every snapshot for a scene, in playback order.
func (c *Service) ListSnapshots(ctx context.Context, sceneID uuid.UUID) ([]*Snapshot, error) {
	rows, err := c.queries.ListSceneSnapshots(ctx, sceneID)
	if err != nil {
		return nil, err
	}
	out := make([]*Snapshot, 0, len(rows))
	for _, row := range rows {
		out = append(out, snapshotFromRow(row))
	}
	return out, nil
}

// Frame is one entry in a rendered collage.
type Frame struct {
	// TimestampMS is where to seek. The client resolves this against the video it
	// already has; nothing is fetched from this server.
	TimestampMS int64
	SnapshotID  uuid.UUID
	// Fraction is the position through the scene, 0.0-1.0.
	//
	// Sent alongside the timestamp because a client that has a DIFFERENT duration
	// for the scene (a corrected one, or its own copy) should scrub by fraction and
	// land in the right place regardless. A collage generated against a stale
	// duration is exactly the case this makes survivable.
	Fraction float64
}

// Generate produces (or regenerates) a collage for a scene.
//
// Regeneration REPLACES rather than mutates, and the frames are sampled from the
// scene's whole snapshot pool rather than from the previous collage. That is what
// makes a re-roll able to choose different frames: §8's collages are generated
// artefacts, and a user who dislikes a sample should be able to ask again.
//
// The whole operation is one transaction. A collage with a half-assigned frame set
// renders as a broken strip, and a broken strip is worse than no collage because
// the user cannot tell it is incomplete.
func (c *Service) Generate(ctx context.Context, sceneID uuid.UUID, frameCount int) (*Collage, []Frame, error) {
	if frameCount == 0 {
		frameCount = DefaultFrames
	}
	if frameCount < MinFrames || frameCount > MaxFrames {
		return nil, nil, fmt.Errorf("frame count %d is outside SPEC §8's range of %d-%d",
			frameCount, MinFrames, MaxFrames)
	}

	// FindSceneDuration returns *int: a scene can have a NULL duration, which is
	// the ErrNoDuration case rather than a zero. Collapsing the two here would make
	// "no duration recorded" and "duration of zero" indistinguishable, and they are
	// different states with different fixes.
	rawDuration, err := c.queries.FindSceneDuration(ctx, sceneID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, ErrSceneNotFound
		}
		return nil, nil, err
	}
	if rawDuration == nil || *rawDuration <= 0 {
		return nil, nil, ErrNoDuration
	}
	// SECONDS to MILLISECONDS. The scene's `duration` column is in seconds and
	// every timestamp in this package is in milliseconds, and both are int64, so
	// nothing catches a mismatch here.
	duration := int64(*rawDuration) * 1000

	var result *Collage
	var frames []Frame
	err = c.withTxn(func(tx *queries.Queries) error {
		pool, err := tx.ListUnassignedSnapshots(ctx, sceneID)
		if err != nil {
			return err
		}

		// Free the previous collage's frames BEFORE sampling, so a regeneration can
		// choose from the whole pool rather than only from the frames the last
		// generation left behind. The snapshots themselves are untouched -- only
		// their assignment moves.
		if err := tx.DeleteCollageForScene(ctx, sceneID); err != nil {
			return err
		}
		if err := tx.DeleteCollage(ctx, sceneID); err != nil {
			return err
		}

		candidates := make([]int64, 0, len(pool))
		for _, row := range pool {
			candidates = append(candidates, row.TimestampMs)
		}
		// The pool is a SET of instants and the sampling rule is about POSITIONS in
		// the scene, not about which snapshots happen to exist. So the rule picks
		// target positions and then finds the nearest available snapshot to each.
		chosen, err := SelectFrames(candidates, duration, frameCount, StrategyUniform)
		if err != nil {
			return err
		}
		if len(chosen) < MinFrames {
			return fmt.Errorf("%w: %d available, need %d", ErrNotEnoughSnapshots,
				len(chosen), MinFrames)
		}

		collageID, err := uuid.NewV7()
		if err != nil {
			return err
		}
		row, err := tx.CreateCollage(ctx, queries.CreateCollageParams{
			ID:                collageID,
			SceneID:           sceneID,
			FrameCount:        len(chosen),
			Strategy:          string(StrategyUniform),
			SourceDurationMs:  pgInt8(duration),
			CurrentDurationMs: pgInt8(duration),
		})
		if err != nil {
			return err
		}

		// Map each chosen timestamp back to its snapshot id.
		byTimestamp := make(map[int64]uuid.UUID, len(pool))
		for _, p := range pool {
			byTimestamp[p.TimestampMs] = p.ID
		}
		ids := make([]uuid.UUID, 0, len(chosen))
		frames = make([]Frame, 0, len(chosen))
		for _, ts := range chosen {
			id, ok := byTimestamp[ts]
			if !ok {
				// Unreachable: SelectFrames only ever returns values that came from
				// candidates. Returned as an error rather than skipped, because
				// skipping would silently produce a short collage.
				return fmt.Errorf("internal: selected timestamp %d is not a snapshot", ts)
			}
			ids = append(ids, id)
			frames = append(frames, Frame{
				TimestampMS: ts,
				SnapshotID:  id,
				Fraction:    float64(ts) / float64(duration),
			})
		}

		claimed, err := tx.AssignSnapshotsToCollage(ctx, queries.AssignSnapshotsToCollageParams{
			CollageID:   uuid.NullUUID{UUID: collageID, Valid: true},
			SnapshotIds: ids,
		})
		if err != nil {
			return err
		}
		if int(claimed) != len(ids) {
			// The claim is optimistic (WHERE collage_id IS NULL), so a concurrent
			// generation that took a frame first makes this short. Rolling back is
			// right: a partial collage is worse than none.
			return fmt.Errorf("%w: %d of %d frames were claimed concurrently",
				ErrNotEnoughSnapshots, claimed, len(ids))
		}

		result = collageFromRow(row)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return result, frames, nil
}

// Get returns the scene's current collage, or nil when none has been generated.
//
// Nil rather than an error: a scene without a collage is the normal state for most
// scenes, and every caller has to render an empty state anyway.
func (c *Service) Get(ctx context.Context, sceneID uuid.UUID) (*Collage, error) {
	row, err := c.queries.GetCollageForScene(ctx, sceneID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return collageFromRow(row), nil
}

// ListFrames returns a collage's frames in playback order.
func (c *Service) ListFrames(ctx context.Context, sceneID uuid.UUID) ([]Frame, error) {
	collage, err := c.Get(ctx, sceneID)
	if err != nil || collage == nil {
		return nil, err
	}
	rows, err := c.queries.ListCollageSnapshots(ctx, sceneID)
	if err != nil {
		return nil, err
	}

	// CurrentDurationMS is already milliseconds -- it is stored in milliseconds --
	// so unlike Generate this needs no conversion. The asymmetry is the trap, and
	// it is why the conversion is done once, at the point the value enters the
	// package, rather than at each use.
	duration := int64(0)
	if collage.CurrentDurationMS != nil {
		duration = *collage.CurrentDurationMS
	}

	frames := make([]Frame, 0, len(rows))
	for _, row := range rows {
		f := Frame{TimestampMS: row.TimestampMs, SnapshotID: row.ID}
		if duration > 0 {
			f.Fraction = float64(row.TimestampMs) / float64(duration)
		}
		frames = append(frames, f)
	}
	return frames, nil
}

// UnderSnapscened is a scene whose visual index is too thin to identify from.
type UnderSnapshotted struct {
	SceneID       uuid.UUID
	SnapshotCount int64
}

// ListUnderSnapshotted finds scenes with too few snapshots to build a collage from.
//
// This is the input to SPEC §7's and §8's curation quests -- "this scene has only
// 2 snapshots" -- and to §3's preservation work. It exists as a query rather than
// as a UI-side scan because the answer is a property of the whole archive, and a
// client asking for "the 50 most under-snapshotted scenes" should not have to page
// the entire table to find them.
func (c *Service) ListUnderSnapshotted(ctx context.Context, minimum int, limit int) ([]UnderSnapshotted, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := c.queries.ListScenesWithInsufficientSnapshots(ctx,
		queries.ListScenesWithInsufficientSnapshotsParams{
			MinSnapshots: minimum,
			LimitCount:   int32(limit),
		})
	if err != nil {
		return nil, err
	}
	out := make([]UnderSnapshotted, 0, len(rows))
	for _, row := range rows {
		out = append(out, UnderSnapshotted{
			SceneID:       row.ID,
			SnapshotCount: row.SnapshotCount,
		})
	}
	return out, nil
}

// SelectFrames picks target positions across a duration and snaps each to the
// nearest available candidate.
//
// PURE, and that is the point. The rule is the whole of what makes a collage good
// or bad, it is the part most likely to be subtly wrong, and inline in Generate it
// could only be tested by inserting snapshots into a database for every case.
//
// Why target-positions-then-nearest rather than "take N evenly spaced from those
// available": the two produce very different collages when snapshots are sparse.
// Target-then-nearest keeps the frames spread across the whole scene, so a scene
// with 13 snapshots in its first minute and none afterwards still yields a collage
// covering its entire duration -- with duplicates avoided by never returning the
// same candidate twice. Evenly-spacing-the-available instead would pack every
// frame into the first minute and render a collage that shows one part of the
// scene twelve times.
//
// The cost of the rule: when candidates are very sparse, two targets can snap to
// the same candidate. Duplicates are dropped rather than emitted, so the collage
// may come back with FEWER frames than asked for. Generate treats that as
// ErrNotEnoughSnapshots rather than padding, because a 9-frame collage presented as
// a 12-frame one is a lie about the scene's identifiability.
func SelectFrames(candidates []int64, durationMS int64, frameCount int, strategy Strategy) ([]int64, error) {
	if durationMS <= 0 {
		return nil, ErrNoDuration
	}
	if frameCount < 1 {
		return nil, fmt.Errorf("frame count must be positive (got %d)", frameCount)
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("%w: no snapshots exist for this scene", ErrNotEnoughSnapshots)
	}

	sorted := make([]int64, len(candidates))
	copy(sorted, candidates)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	targets := targetPositions(durationMS, frameCount, strategy)

	chosen := make([]int64, 0, len(targets))
	used := make(map[int64]bool, len(targets))
	for _, target := range targets {
		nearest := nearestUnused(sorted, target, used)
		if nearest < 0 {
			// Every candidate is taken. Stop rather than emit a duplicate: a
			// collage with the same frame twice is one frame short of the count the
			// caller asked for and looks like a rendering bug.
			continue
		}
		used[nearest] = true
		chosen = append(chosen, nearest)
	}

	// Sort the result. The SELECTION is independent of the order targets are
	// visited in, but the OUTPUT is not, and the output is what gets rendered.
	//
	// Found by a diagnostic while checking a mutation: with a sparse pool the
	// frames came back as 19500, 58500, 71500, 65000, 52000, 45500, 39000... -- a
	// jumble. The cause is that once the frames nearest a later target are taken,
	// that target reaches DOWN to an earlier candidate, so the running order stops
	// matching the target order.
	//
	// My sparse test asserted only that the frames were DISTINCT, so it passed. A
	// collage renders in slice order, so a jumbled one plays frames backwards and
	// jumps about, which is exactly the "looks broken" failure the sort prevents.
	sort.Slice(chosen, func(i, j int) bool { return chosen[i] < chosen[j] })
	return chosen, nil
}

// targetPositions returns the ideal instants for a collage, in order.
func targetPositions(durationMS int64, frameCount int, strategy Strategy) []int64 {
	positions := make([]int64, 0, frameCount)
	switch strategy {
	case StrategyHead:
		// The first half of the positions fall in the opening 20% of the scene, the
		// rest spread over the remainder. A placeholder for the strategy §8 hints at
		// ("optional silent previews", "hover-scrubbable") and which the title card
		// makes worth having: the opening of a scene is where it is most
		// identifiable and most often where a user's memory of it lives.
		//
		// Implemented rather than left as a rejected value, because a Strategy column
		// with one usable value is a schema that will be reinterpretted the first
		// time someone needs a second one.
		headCount := (frameCount + 1) / 2
		for i := range headCount {
			positions = append(positions, int64(float64(durationMS)*0.20*float64(i)/float64(max(1, headCount-1))))
		}
		for i := range frameCount - headCount {
			positions = append(positions,
				int64(float64(durationMS)*0.20+
					float64(durationMS)*0.80*float64(i)/float64(max(1, frameCount-headCount-1))))
		}
	default:
		// Uniform: the midpoint of each of frameCount equal slices.
		//
		// Midpoints, not slice boundaries, because a boundary lands on the cut
		// between two shots -- a transition frame is the least identifiable frame in
		// a scene, and a collage built from them identifies worse than one built from
		// the middle of each shot.
		for i := range frameCount {
			slice := float64(durationMS) / float64(frameCount)
			positions = append(positions, int64(slice*(float64(i)+0.5)))
		}
	}
	return positions
}

// nearestUnused finds the candidate closest to target that has not been taken.
func nearestUnused(sorted []int64, target int64, used map[int64]bool) int64 {
	best := int64(-1)
	bestDist := int64(-1)
	for _, c := range sorted {
		if used[c] {
			continue
		}
		d := c - target
		if d < 0 {
			d = -d
		}
		if bestDist < 0 || d < bestDist {
			best = c
			bestDist = d
		}
	}
	return best
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
