package similar

import (
	"context"
	"fmt"
	"sort"

	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/errutil"
	"github.com/stashapp/stash-box/internal/service/scene"
)

// Tuning, and why the defaults are what they are.
const (
	// defaultLimit is how many recommendations a caller gets when they ask for none. A
	// recommendation list is not a catalogue: past a dozen entries a client is rendering a
	// grid and the ranking has stopped being the point.
	defaultLimit = 10
	// maxLimit bounds what a caller may request. Clamped rather than rejected, like the
	// performer similarity limit: a client asking for more than the ceiling wants results,
	// not an error.
	maxLimit = 50
	// candidateSlack widens the candidate pool beyond the page. The SQL pre-rank counts
	// shared facts and the service scores them, so the two orders are not the same order:
	// a candidate that loses the pre-rank on four shared tags can still win on one shared
	// performer plus the studio. A pool of exactly `limit` rows would make the exact scorer
	// a no-op and the pre-rank the real ranking -- which is the thing the SQL comments say
	// it must not be.
	candidateSlack = 4
	// candidateFloor keeps small requests sane: `limit: 1` would otherwise consider four
	// candidates, and a client rendering a sidebar wants the best ONE, which means seeing
	// more than four scenes to find it.
	candidateFloor = 20
	// maxCandidates bounds the pool. The evidence queries take an id array and the tag
	// evidence walks the ontology for each, so an unbounded pool is an unbounded closure
	// walk for a page of ten.
	maxCandidates = 500
	// vectorWeight scales a cosine similarity into the ranking value. 1.0 means a candidate
	// whose embedding is identical to the subject's gains as much as a shared exact tag,
	// which is the ordering claim the vector half is allowed to make and no more.
	vectorWeight = 1.0
)

// Service answers what a scene is like, and why.
type Service struct {
	queries *queries.Queries
	scenes  *scene.Scene
	vectors VectorStore
}

// New returns a similarity service. A nil store is a no-op store: the default is that
// nothing is embedded, and the caller that forgets to configure one gets correct ordering
// from the structured evidence rather than a panic.
func New(q *queries.Queries, scenes *scene.Scene, vectors VectorStore) *Service {
	if vectors == nil {
		vectors = Noop{}
	}
	return &Service{queries: q, scenes: scenes, vectors: vectors}
}

// VectorStore reports the store in use, so a resolver or an operator can tell "the
// re-ranker changed nothing" from "there is no re-ranker".
func (s *Service) VectorStore() VectorStore { return s.vectors }

// SimilarSceneInput bounds a similarity search.
type SimilarSceneInput struct {
	// Subject is the scene to find neighbours of. Passed as the model rather than as an id
	// because the caller already has it -- this is a field on a scene -- and re-reading the
	// row would be a second read of a row the request already loaded.
	Subject models.Scene
	// Limit is how many to return. Zero uses defaultLimit; above maxLimit is clamped.
	Limit int
}

// SimilarScene is one recommendation, with the evidence that produced it.
//
// The evidence is the point, exactly as it is for similar performers: a recommendation that
// cannot say WHY is one a reader can only conclude is broken. Everything here except
// SemanticScore is a fact a reader can open and check.
type SimilarScene struct {
	// Scene is the recommendation.
	Scene *models.Scene
	// Score is the ranking value: the weighted evidence divided by what the subject
	// offers. Roughly 0-1, and above 1 for a rich candidate against a narrow subject (see
	// score in explain.go). Exposed for ordering and debugging, NOT for display --
	// rendering "87% match" from it invents a precision this heuristic does not have.
	// Render the evidence instead.
	Score float64
	// Reasons is what the two scenes have in common, strongest kind first.
	Reasons []Reason
	// SemanticScore is the embedding cosine, when the vector store knew both scenes. Nil
	// otherwise, including on every instance with the default no-op store.
	//
	// NOT a reason. It is a number, and the reasons are what a reader checks; it is
	// reported because it explains why two candidates with equal evidence are in the order
	// they are. It never contributes to Score.
	SemanticScore *float64
	// rankingValue is what the page is ORDERED by: Score plus the vector bonus below. It is
	// kept apart from Score because the two answer different questions -- "how alike are
	// these, on the evidence" and "where does this go on the page" -- and folding the second
	// into the first made the number a client was shown untraceable from the reasons beside
	// it, which the integration test caught by reading 1.307 where the evidence said 0.6.
	rankingValue float64
}

// FindSimilarScenes returns scenes related to the subject, best first.
//
// THE STRUCTURED EVIDENCE DECIDES THE SCORE AND THE VECTOR HALF ONLY REORDERS. The
// explanation half of section 8 lands first because it is the half that is honest on this
// instance: shared tags through the ontology, a shared performer, a shared studio, a similar
// duration -- all of them facts a reader can check, none of them needing a model. The vector
// half needs an embedding model that nothing here produces yet, so it is wired as a
// re-ranker over the candidates the structured half already found, and a candidate is only
// ever promoted by it, never demoted: demoting on a number that appears nowhere in the
// response is invisible behaviour, and invisible behaviour is what this spec keeps removing.
func (s *Service) FindSimilarScenes(ctx context.Context, input SimilarSceneInput) ([]SimilarScene, error) {
	limit := input.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}

	candidates, err := s.queries.FindSimilarSceneCandidates(ctx, queries.FindSimilarSceneCandidatesParams{
		SubjectID:      input.Subject.ID,
		CandidateLimit: candidatePoolSize(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("finding scenes similar to %s: %w", input.Subject.ID, err)
	}
	if len(candidates) == 0 {
		return []SimilarScene{}, nil
	}

	ids := make([]uuid.UUID, 0, len(candidates))
	for _, row := range candidates {
		ids = append(ids, row.SceneID)
	}

	tagRows, err := s.queries.FindSimilarSceneTagEvidence(ctx, queries.FindSimilarSceneTagEvidenceParams{
		SubjectID:    input.Subject.ID,
		CandidateIds: ids,
	})
	if err != nil {
		return nil, fmt.Errorf("reading shared tags for scenes similar to %s: %w", input.Subject.ID, err)
	}
	performerRows, err := s.queries.FindSimilarSceneSharedPerformers(ctx, queries.FindSimilarSceneSharedPerformersParams{
		SubjectID:    input.Subject.ID,
		CandidateIds: ids,
	})
	if err != nil {
		return nil, fmt.Errorf("reading shared performers for scenes similar to %s: %w", input.Subject.ID, err)
	}

	// Load the candidates once, in one query, rather than one per row: the ranking is
	// already computed and rendering a page should not be N round trips.
	loaded, errs := s.scenes.LoadIds(ctx, ids)
	for _, err := range errs {
		// "Not found" is the expected case -- the ranking selected a row that was merged or
		// deleted between the two queries -- so it is ignored rather than surfaced.
		if err := errutil.IgnoreNotFound(err); err != nil {
			return nil, fmt.Errorf("loading similar scenes: %w", err)
		}
	}
	byID := make(map[uuid.UUID]*models.Scene, len(loaded))
	for _, sc := range loaded {
		if sc != nil {
			byID[sc.ID] = sc
		}
	}

	evidenceByScene := buildEvidence(tagRows, performerRows)

	semantic, err := s.semanticScores(ctx, input.Subject, ids)
	if err != nil {
		return nil, fmt.Errorf("scoring semantic neighbours of %s: %w", input.Subject.ID, err)
	}

	// The subject's own counts come back on every candidate row; they are facts about the
	// subject and the same on all of them. Read from the first row rather than queried
	// again, so the page and its denominator agree by construction.
	subjectTags := candidates[0].SubjectTags
	subjectPerformers := candidates[0].SubjectPerformers
	subjectHasStudio := input.Subject.StudioID.Valid
	subjectHasDuration := input.Subject.Duration != nil

	studioName, err := s.studioName(ctx, input.Subject)
	if err != nil {
		return nil, err
	}

	// AN UNKNOWN DURATION IS NOT EVIDENCE OF A SIMILAR ONE, so the term is absent rather
	// than zero-valued when either side is missing: a scene whose runtime nobody recorded
	// must not earn the duration term against another such scene. Two zeros compared would
	// be a perfect match between two unknowns.
	subjectDuration := input.Subject.Duration

	out := make([]SimilarScene, 0, len(candidates))
	for _, row := range candidates {
		sc, ok := byID[row.SceneID]
		if !ok {
			// A candidate the ranking selected but the loader did not return is deleted or
			// merged. Skipping is right: a recommendation list with a hole reads as a bug,
			// and a link to a deleted scene is worse than one fewer suggestion.
			continue
		}

		ev := evidenceByScene[row.SceneID]
		ev.studioName = studioName
		// The studio axis is in the SQL predicate and in the denominator, and this is the
		// only place it becomes a REASON. It was missing here once: the candidate was
		// selected on the studio, the score counted the opportunity, and no reason was ever
		// emitted -- so the number came out lower than the reasons beside it could explain,
		// which is the exact failure the "evidence is the design" rule exists to prevent.
		ev.sameStudio = input.Subject.StudioID.Valid && sc.StudioID.Valid &&
			input.Subject.StudioID.UUID == sc.StudioID.UUID
		ev.durationDelta = durationDelta(subjectDuration, sc.Duration)
		if ev.isEmpty() {
			continue
		}

		reasons, scoreValue := score(ev, subjectTags, subjectPerformers, subjectHasStudio, subjectHasDuration)

		rec := SimilarScene{
			Scene:        sc,
			Score:        scoreValue,
			Reasons:      reasons,
			rankingValue: scoreValue,
		}
		if cosine, ok := semantic[sc.ID]; ok {
			value := cosine
			rec.SemanticScore = &value
			if cosine > 0 {
				// Only a positive similarity reorders. A negative cosine says the two point
				// apart, which is not evidence that the structured match is wrong -- it is a
				// different claim from a different system, and acting on it would let an
				// unexplained number push a chosen recommendation down.
				//
				// It moves the RANKING VALUE and never the score: the score is the sum of
				// things a reader can check, and a component that cannot be checked must not
				// be added to it.
				rec.rankingValue = scoreValue + vectorWeight*cosine
			}
		}
		out = append(out, rec)
	}

	// Deterministic: score, then the evidence-only order the query produced. Ties broken by
	// id so two identical reads return the same page.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].rankingValue != out[j].rankingValue {
			return out[i].rankingValue > out[j].rankingValue
		}
		return out[i].Scene.ID.String() < out[j].Scene.ID.String()
	})

	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// buildEvidence groups the two evidence queries into one structure per candidate.
func buildEvidence(tagRows []queries.FindSimilarSceneTagEvidenceRow, performerRows []queries.FindSimilarSceneSharedPerformersRow) map[uuid.UUID]evidence {
	out := make(map[uuid.UUID]evidence, len(tagRows)+len(performerRows))

	for _, row := range tagRows {
		ev := out[row.SceneID]
		if ev.tags == nil {
			ev.tags = make(map[uuid.UUID]tagEvidence)
		}
		candidate := tagEvidence{
			tagID:     row.TagID,
			tagName:   row.TagName,
			rootName:  row.RootName,
			direction: row.Direction,
			depth:     row.Depth,
		}
		// ONE REASON PER TAG, even though a tag can satisfy several of the subject's tags
		// (two subject tags both implying the same general one) or arrive in both
		// directions. The strongest explanation wins -- shallowest, then the exact/instance
		// direction -- because a reason list that repeats a tag name three times reads as
		// three separate facts, and the score would be paid three times for one.
		if existing, ok := ev.tags[row.TagID]; ok && !betterExplanation(candidate, existing) {
			out[row.SceneID] = ev
			continue
		}
		ev.tags[row.TagID] = candidate
		out[row.SceneID] = ev
	}

	for _, row := range performerRows {
		ev := out[row.SceneID]
		if ev.performers == nil {
			ev.performers = make(map[uuid.UUID]string)
		}
		ev.performers[row.PerformerID] = row.PerformerName
		out[row.SceneID] = ev
	}

	return out
}

// betterExplanation reports whether `a` explains a shared tag better than `b`.
//
// Shallowest first -- an exact match beats an instance beats a relative -- and then the
// 'down' direction, whose label ("a kind of X") names what a reader can check in both
// scenes rather than a mere family resemblance.
func betterExplanation(a, b tagEvidence) bool {
	if a.depth != b.depth {
		return a.depth < b.depth
	}
	if a.direction != b.direction {
		return a.direction == "down"
	}
	return a.rootName < b.rootName
}

// semanticScores asks the vector store for the subject-to-candidate similarities.
//
// A no-op store answers with nothing, which is the default and leaves the order exactly as
// the evidence put it.
func (s *Service) semanticScores(ctx context.Context, subject models.Scene, ids []uuid.UUID) (map[uuid.UUID]float64, error) {
	if s.vectors.Len() == 0 {
		// Asked before the call so the default path costs nothing: an in-memory store with
		// no vectors would return an empty map anyway, and a real store with none should
		// not be interrogated once per page.
		return nil, nil
	}
	return s.vectors.Scores(subject.ID, ids)
}

// studioName resolves the subject's studio for the studio reason's label.
//
// Only asked when the subject has a studio AND at least one candidate shares it: the label
// is for a reason that may never be emitted, and a page with no studio match should not pay
// for a lookup.
func (s *Service) studioName(ctx context.Context, subject models.Scene) (string, error) {
	if !subject.StudioID.Valid {
		return "", nil
	}
	row, err := s.queries.FindStudio(ctx, subject.StudioID.UUID)
	if err != nil {
		if errutil.IgnoreNotFound(err) == nil {
			return "", nil
		}
		return "", fmt.Errorf("resolving the studio of scene %s: %w", subject.ID, err)
	}
	return row.Name, nil
}

// durationDelta is the absolute runtime difference in seconds, or nil when either side is
// unknown.
func durationDelta(a, b *int) *int {
	if a == nil || b == nil {
		return nil
	}
	delta := *a - *b
	if delta < 0 {
		delta = -delta
	}
	return &delta
}

// candidatePoolSize is how many candidates the exact scorer gets to see.
func candidatePoolSize(limit int) int {
	pool := limit*candidateSlack + candidateFloor
	if pool > maxCandidates {
		return maxCandidates
	}
	return pool
}

// evidenceIsEmpty reports whether nothing at all was observed about a candidate.
//
// The SQL predicate already guarantees a candidate shares an axis, so an empty evidence set
// here means the predicate and the evidence queries disagree. Those rows are DROPPED rather
// than rendered: a recommendation whose reason list is empty is a scene appearing in a
// "related scenes" list for no stated reason, which is the one thing this feature exists to
// avoid. Dropping is also the safe direction -- it can only shorten a list.
func (e evidence) isEmpty() bool {
	return len(e.tags) == 0 && len(e.performers) == 0 && !e.sameStudio
}
