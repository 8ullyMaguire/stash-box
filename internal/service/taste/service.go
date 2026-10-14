// Package taste answers "what would this person like", from the signals they have already left.
//
// AFFINITY IS DERIVED, NOT STORED. It is a function of a user's fingerprints, list items and
// reviews -- three small per-user tables, each indexed by user -- so a stored copy would be a
// second answer able to drift from them, and every write path would have to remember to update
// it. The read-time cost is one scan of those tables for the viewer, and one scoring query over
// a bounded page of candidates.
//
// THE FEED IS THE QUERY LANGUAGE PLUS AN AFFINITY RANKING PLUS A MIX. The filter half is the
// existing scene query, reused rather than reimplemented: `For You` narrows the archive the same
// way every other scene list does (same language, same visibility rules), and this package only
// changes what the narrowed list is ordered by.
package taste

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"sort"

	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/scene"
)

// Tuning, and why the defaults are what they are.
const (
	// defaultLimit is a feed page. Twenty rather than ten: a feed is scrolled, not read.
	defaultLimit = 20
	// maxLimit bounds what a caller may request, clamped rather than rejected.
	maxLimit = 50

	// defaultSerendipity is the share of a page that comes from outside the affinity order.
	// A feed that is entirely the viewer's own taste is a mirror: it can only ever show more
	// of what they already chose, so some of the page has to be a guess. 0.2 is one slot in
	// five, and it is a default rather than a policy -- the caller sets it.
	defaultSerendipity = 0.2

	// propagationFactor is the fraction of a direct affinity that reaches the PARENT tag:
	// liking `cowgirl_position` raises `woman_on_top` by this much per hop. The number is
	// passed to SQL so it lives in one place, next to the rule it implements.
	//
	// It is below 1 because the parent claim is weaker than the child's -- and the acceptance
	// test for section 9 is exactly this: a scene tagged the parent must score MORE than
	// nothing and LESS than one tagged the child.
	propagationFactor = 0.4

	// poolSlack and poolFloor widen the candidate set the scoring query sees. The filtered
	// pool is ordered by the archive's own order (recency), and affinity is not that order, so
	// a pool of exactly `limit` would make the ranking a no-op. Same shape as the similarity
	// candidate pool, for the same reason.
	poolSlack = 6
	poolFloor = 60
	// maxPool bounds it: the scoring query carries the candidate ids and walks the tag graph
	// for each, so an unbounded pool is an unbounded closure walk for a page of twenty.
	maxPool = 600

	// maxContributions is how many tags a row explains itself with. A scene with forty tags
	// would otherwise list every one it has, and a contribution list is a sentence, not an
	// inventory.
	maxContributions = 5
)

// Service ranks scenes by a viewer's taste.
type Service struct {
	queries *queries.Queries
	scenes  *scene.Scene
}

// New returns a taste service.
func New(q *queries.Queries, scenes *scene.Scene) *Service {
	return &Service{queries: q, scenes: scenes}
}

// FeedInput is a page of the viewer's feed.
type FeedInput struct {
	// UserID is whose taste this is.
	UserID uuid.UUID
	// Filter is the query-language half: the same input `queryScenes` takes, because For You
	// is a narrowing of the archive and not a second way to search it.
	Filter models.SceneQueryInput
	// Limit is how many to return. Zero uses defaultLimit; above maxLimit is clamped.
	Limit int
	// Serendipity is the share of the page drawn from outside the affinity order, 0..1.
	// Zero is a pure affinity ranking; one is a shuffle of the pool. Negative uses the
	// default, because "less than none" is a caller meaning "unset".
	Serendipity float64
	// Seed makes the serendipity part reproducible. Two calls with the same seed and the same
	// pool return the same page, which is what lets a test say "the parameter changed the
	// output" rather than "the output is random".
	Seed int64
}

// Contribution is one tag's share of a scene's affinity.
type Contribution struct {
	TagID uuid.UUID
	// TagName is the tag itself, so a client can render "because you like X" without a second
	// request. A contribution is evidence, and evidence names something.
	TagName string
	// Weight is this tag's total contribution, which may be negative: the viewer's taste has
	// a sign, and hiding the negative half would make the feed look more confident than it is.
	Weight float64
}

// FeedScene is one recommendation with the taste that produced it.
type FeedScene struct {
	Scene *models.Scene
	// Affinity is the total of the contributions, in no particular unit. Negative is normal:
	// a viewer who has voted a scene's tags down gets a feed that reflects it.
	Affinity float64
	// Contributions are the tags that produced it, largest magnitude first.
	Contributions []Contribution
}

// Feed ranks the filtered archive by the viewer's taste.
//
// THE FILTER RUNS FIRST AND THE RANKING SECOND, which is the only order that can work: affinity
// is a property of a scene's tags, so the tags have to be known, and the filter is what bounds
// the set whose tags are looked at. A ranking first would score the whole archive to show a page.
func (s *Service) Feed(ctx context.Context, input FeedInput) ([]FeedScene, error) {
	limit := input.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	serendipity := input.Serendipity
	if serendipity < 0 {
		serendipity = defaultSerendipity
	}
	if serendipity > 1 {
		serendipity = 1
	}

	// The pool: the filtered archive, most recently ADDED first, bounded.
	//
	// The order is CREATED_AT and not the default (DATE, the release date), and the full test
	// suite is what found out why: with the release-date default, a pool of 600 on an archive
	// with more than 600 dated scenes is filled by scenes that HAVE a release date, and a
	// scene with none is never scored -- so the feed stops recommending anything recently
	// added. Recency of ARRIVAL is the neutral order for a discovery feed: it is the archive's
	// own order, it is total (every row has a created_at), and it does not depend on a field
	// that is nullable in practice.
	filter := input.Filter
	filter.Sort = models.SceneSortEnumCreatedAt
	filter.Direction = models.SortDirectionEnumDesc
	pool := poolSize(limit)
	filter.PerPage = pool
	filter.Page = 1

	candidates, err := s.scenes.Query(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("building the feed pool: %w", err)
	}
	if len(candidates) == 0 {
		return []FeedScene{}, nil
	}

	ids := make([]uuid.UUID, 0, len(candidates))
	byID := make(map[uuid.UUID]*models.Scene, len(candidates))
	for i := range candidates {
		ids = append(ids, candidates[i].ID)
		byID[candidates[i].ID] = &candidates[i]
	}

	// Scenes the viewer has already engaged with are not recommendations. Removed from the
	// POOL rather than from the page, so a page is still full: dropping them after paging
	// would return a short page for a viewer who has been busy.
	engaged, err := s.queries.FindViewerEngagedSceneIDs(ctx, queries.FindViewerEngagedSceneIDsParams{
		UserID:   input.UserID,
		SceneIds: ids,
	})
	if err != nil {
		return nil, fmt.Errorf("reading the viewer's engaged scenes: %w", err)
	}
	engagedSet := make(map[uuid.UUID]struct{}, len(engaged))
	for _, id := range engaged {
		engagedSet[id] = struct{}{}
	}

	scored, err := s.queries.ScoreScenesByAffinity(ctx, queries.ScoreScenesByAffinityParams{
		UserID:            input.UserID,
		SceneIds:          ids,
		PropagationFactor: propagationFactor,
	})
	if err != nil {
		return nil, fmt.Errorf("scoring the feed pool by affinity: %w", err)
	}

	affinity := make(map[uuid.UUID]float64, len(scored))
	contributions := make(map[uuid.UUID][]Contribution, len(scored))
	for _, row := range scored {
		affinity[row.SceneID] += row.Weight
		contributions[row.SceneID] = append(contributions[row.SceneID], Contribution{
			TagID:   row.TagID,
			TagName: row.TagName,
			Weight:  row.Weight,
		})
	}

	ranked := make([]FeedScene, 0, len(candidates))
	for _, sc := range candidates {
		if _, skip := engagedSet[sc.ID]; skip {
			continue
		}
		sc := sc
		contribs := contributions[sc.ID]
		// Largest magnitude first: the tag that explains the score leads the sentence.
		sort.SliceStable(contribs, func(i, j int) bool {
			if math.Abs(contribs[i].Weight) != math.Abs(contribs[j].Weight) {
				return math.Abs(contribs[i].Weight) > math.Abs(contribs[j].Weight)
			}
			return contribs[i].TagName < contribs[j].TagName
		})
		if len(contribs) > maxContributions {
			contribs = contribs[:maxContributions]
		}
		ranked = append(ranked, FeedScene{
			Scene:         byID[sc.ID],
			Affinity:      affinity[sc.ID],
			Contributions: contribs,
		})
	}

	// NOTHING KNOWN ABOUT THIS PAGE. Either the viewer has left no signals, or none of them
	// touch anything in the pool. There is no ranking to apply -- every candidate is equally
	// (un)likely -- so the honest answer is the pool in its own order (recency, which is the
	// order the archive already has) rather than the id-ordered tie the comparator below would
	// otherwise produce. A brand-new account that lands on uuid order looks broken; one that
	// lands on what arrived today looks like an archive.
	if len(affinity) == 0 {
		if len(ranked) > limit {
			ranked = ranked[:limit]
		}
		return ranked, nil
	}

	// The affinity order, ties broken by id so two reads of the same data agree.
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].Affinity != ranked[j].Affinity {
			return ranked[i].Affinity > ranked[j].Affinity
		}
		return ranked[i].Scene.ID.String() < ranked[j].Scene.ID.String()
	})

	// `other` is the pool in the RECENCY order the query returned, minus what the affinity
	// order already offers on this page. A surprise is then never a duplicate of a pick, and
	// it is always something the affinity order would not have shown.
	offered := make(map[uuid.UUID]struct{}, limit)
	for i := 0; i < len(ranked) && i < limit; i++ {
		offered[ranked[i].Scene.ID] = struct{}{}
	}
	other := make([]FeedScene, 0, len(ranked))
	for _, sc := range candidates {
		if _, skip := engagedSet[sc.ID]; skip {
			continue
		}
		if _, taken := offered[sc.ID]; taken {
			continue
		}
		other = append(other, FeedScene{
			Scene:         byID[sc.ID],
			Affinity:      affinity[sc.ID],
			Contributions: contributions[sc.ID],
		})
	}

	return mixFeed(ranked, other, limit, serendipity, input.Seed), nil
}

// mixFeed is the serendipity parameter, isolated and pure so it can be tested without a database.
//
// IT IS A SLOT RULE, NOT A SAMPLE. `serendipity` sets a stride: one slot in `stride` comes from
// outside the affinity order, where `stride = round(1/serendipity)`. So 0 is a pure affinity
// page (and therefore a SUBSET of it), 0.5 is every second slot, and 1 is every slot. A random
// sample of size `serendipity * limit` would have been the obvious alternative and it is worse:
// the surprises would arrive in a clump at the end of the page, or in a random scatter that
// differs every call, and neither is what "some of this page is a guess" should look like.
//
// The surprises come from `other` in a seeded shuffle, so the same seed and pool give the same
// page: a caller can pin the seed to make a feed reproducible, and a test can change the
// parameter and attribute the difference to the parameter.
func mixFeed(ranked, other []FeedScene, limit int, serendipity float64, seed int64) []FeedScene {
	if limit <= 0 {
		return nil
	}
	if serendipity < 0 {
		serendipity = 0
	}
	if serendipity > 1 {
		serendipity = 1
	}

	shuffled := make([]FeedScene, len(other))
	copy(shuffled, other)
	if len(shuffled) > 1 {
		rng := rand.New(rand.NewSource(seed))
		rng.Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})
	}

	stride := 0 // 0 means "never take a surprise", which is serendipity 0.
	if serendipity > 0 {
		stride = int(math.Round(1 / serendipity))
		if stride < 1 {
			stride = 1
		}
	}

	out := make([]FeedScene, 0, limit)
	ri, oi := 0, 0
	for len(out) < limit {
		if stride > 0 && (len(out)+1)%stride == 0 && oi < len(shuffled) {
			out = append(out, shuffled[oi])
			oi++
			continue
		}
		if ri < len(ranked) {
			out = append(out, ranked[ri])
			ri++
			continue
		}
		if oi < len(shuffled) {
			out = append(out, shuffled[oi])
			oi++
			continue
		}
		// Both sources exhausted: a short page is the honest answer, and a filter that
		// narrowed the archive to three scenes is why.
		break
	}
	return out
}

// poolSize is how many candidates the affinity scorer sees.
func poolSize(limit int) int {
	pool := limit*poolSlack + poolFloor
	if pool > maxPool {
		return maxPool
	}
	return pool
}
