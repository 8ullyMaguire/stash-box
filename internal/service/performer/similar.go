package performer

import (
	"context"
	"fmt"

	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/errutil"
)

// Similarity tuning, and why the defaults are what they are.
//
// These are defaults a caller may override, not policy: a client rendering a
// sidebar wants a handful, a client building a discovery page wants more. The floor
// matters more than the ceiling though -- see defaultMinShared.

// defaultMinShared is the minimum number of shared scenes before a performer counts
// as similar at all.
//
// 2, not 1, because a single co-appearance is the most common case in the entire
// dataset: on any instance with crowded scenes, every performer has shared at least
// one scene with every other, so a floor of 1 makes the top result "whoever happened
// to share a four-hander" -- noise dressed as a recommendation. Two shared scenes is
// where co-appearance starts to mean something, and it is a floor rather than a
// filter because the query computes the ranking for every candidate and only then
// applies it.
const defaultMinShared = 2

// defaultSimilarLimit is how many performers a caller gets when they ask for none.
// A recommendation list is not a catalogue: past a dozen entries a client is showing
// a grid, and the ranking has stopped being the point.
const defaultSimilarLimit = 10

// maxSimilarLimit bounds what a caller may request.
//
// Without a ceiling, `limit: 100000` on a large instance is a full co-occurrence
// self-join, which is the most expensive query in this file by a wide margin. The
// cap is deliberately generous: a client that wants everything should page, and the
// Count method exists so it can know how much there is.
const maxSimilarLimit = 50

// SimilarPerformer is one recommendation, with the evidence that produced it.
//
// The evidence fields are the point. A recommendation that cannot say WHY it
// recommended something is one a curator can only conclude is broken: they see an
// unrelated performer and have no way to check the claim. Reporting the three
// observable facts instead of a bare score lets a client render something checkable
// — "you have appeared together 6 times, alongside 4 of the same performers" — and
// lets a developer see immediately whether a ranking regression is a scoring change
// or a data change.
type SimilarPerformer struct {
	// Performer is the recommendation itself.
	Performer models.Performer
	// ScenesShared is the raw evidence: scenes both performers appear in.
	ScenesShared int
	// TargetScenes is how many scenes the SUBJECT appears in. Exposed because
	// ScenesShared means different things for a performer in 4 scenes and one in 400,
	// and a client rendering "6 scenes together" without this overstates the pairing.
	TargetScenes int
	// CoPerformers is the count of distinct THIRD performers who appear alongside
	// both. This is what separates "the same scene twice" from "the same circle":
	// two people who keep appearing with the same third person are more alike than two
	// who share scenes only with strangers.
	CoPerformers int
	// Score is the ranking value, in no particular unit. It is
	// (shared / subject scenes) * (1 + min(coPerformers/5, 1)), so it is roughly
	// 0-2: 1.0 means "in every one of this performer's scenes we have met", and the
	// factor above 1 rewards a recurring circle.
	Score float64
}

// SimilarPerformerInput bounds a similarity search.
type SimilarPerformerInput struct {
	// PerformerID is the subject. Never appears in its own results.
	PerformerID uuid.UUID
	// MinShared is the shared-scene floor. Zero uses defaultMinShared.
	MinShared int
	// Limit is how many to return. Zero uses defaultSimilarLimit; values above
	// maxSimilarLimit are clamped rather than rejected, because a client asking for
	// more than the ceiling wants results, not an error.
	Limit int
}

// FindSimilar returns performers who appear alongside the subject, most alike first.
//
// CO-OCCURRENCE is the signal because it is the only strong one this schema has.
// There is no performer-to-tag table, and no performer-to-studio relationship at all:
// no studio_performers table and no performers.studio_id column, so a performer
// reaches a studio only indirectly, through the scenes they appear in. Co-appearance
// is the one thing that genuinely correlates, and building on the absence of the
// others would mean inventing a taxonomy this archive does not have.
//
// The subject's scene count is the DENOMINATOR on purpose. Using the shared count's
// own min or max would let a prolific performer dominate every result — someone in
// 400 scenes shares 20 with nearly everybody, which is noise — whereas a pair in 2
// scenes who shared both is a much stronger statement than a pair in 400 who shared
// 20.
func (s *Performer) FindSimilar(ctx context.Context, input SimilarPerformerInput) ([]SimilarPerformer, error) {
	minShared, limit := input.MinShared, input.Limit
	if minShared <= 0 {
		minShared = defaultMinShared
	}
	if limit <= 0 {
		limit = defaultSimilarLimit
	}
	if limit > maxSimilarLimit {
		// Clamped, not rejected. An over-limit request is a client that wants results,
		// and erroring teaches it to retry smaller for no benefit.
		limit = maxSimilarLimit
	}

	rows, err := s.queries.FindSimilarPerformers(ctx, queries.FindSimilarPerformersParams{
		PerformerID: input.PerformerID,
		MinShared:   minShared,
		Lim:         limit,
	})
	if err != nil {
		return nil, fmt.Errorf("finding performers similar to %s: %w", input.PerformerID, err)
	}
	if len(rows) == 0 {
		return []SimilarPerformer{}, nil
	}

	// Resolve the performers in one query rather than one per row. The ranking is
	// already computed; loading each performer individually would turn a single
	// indexed join into N round trips to render one list.
	ids := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.PerformerID)
	}
	performers, errs := s.LoadIds(ctx, ids)
	// loadutil.One reports ONE error per requested id rather than a single aggregate
	// error, so a single missing performer must not fail the whole list. "Not found" is
	// the expected case here -- the ranking selected a row that was merged or deleted
	// between the two queries -- so it is ignored rather than surfaced.
	for _, err := range errs {
		if err := errutil.IgnoreNotFound(err); err != nil {
			return nil, fmt.Errorf("loading similar performers: %w", err)
		}
	}
	fmt.Printf("PROBE rows=%d ids=%v loaded=%d errs=%v\n", len(rows), ids, len(performers), errs)
	byID := make(map[uuid.UUID]models.Performer, len(performers))
	for _, p := range performers {
		if p != nil {
			byID[p.ID] = *p
		}
	}

	// Preserve the QUERY's order. Iterating `rows` rather than `performers` is the
	// whole reason the ranking survives: LoadIds returns whatever order the database
	// felt like, so appending by index would silently discard the sort.
	out := make([]SimilarPerformer, 0, len(rows))
	for _, r := range rows {
		p, ok := byID[r.PerformerID]
		if !ok {
			// A performer the ranking selected but the loader did not return is a
			// soft-deleted row or a concurrent merge. Skipping it is right: a
			// recommendation list with a hole reads as a bug, and a link to a deleted
			// performer is worse than one fewer suggestion.
			continue
		}
		out = append(out, SimilarPerformer{
			Performer:    p,
			ScenesShared: r.ScenesShared,
			TargetScenes: r.TargetScenes,
			CoPerformers: r.CoPerformers,
			Score:        r.Score,
		})
	}
	return out, nil
}

// CountSimilar reports how many performers clear the floor for a subject.
//
// Separate from FindSimilar because the list is limited to a page while "12
// performers qualify and you can see 10" is information a client needs and cannot get
// from the list alone.
func (s *Performer) CountSimilar(ctx context.Context, performerID uuid.UUID, minShared int) (int, error) {
	if minShared <= 0 {
		minShared = defaultMinShared
	}
	n, err := s.queries.CountSimilarPerformers(ctx, queries.CountSimilarPerformersParams{
		PerformerID: performerID,
		MinShared:   minShared,
	})
	if err != nil {
		return 0, fmt.Errorf("counting performers similar to %s: %w", performerID, err)
	}
	return n, nil
}
