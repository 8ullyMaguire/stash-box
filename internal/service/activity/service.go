// Package activity assembles the chronological activity feed (discovery SPEC section 10,
// aggregator phase 7 slice 7).
//
// CHRONOLOGICAL BY CONTRACT. The draft is explicit -- "no algorithmic reordering" -- so there is
// no score, no decay term, no diversity adjustment and no permission for one to be added later:
// the order is the timestamp, ties broken deterministically. A feed that reorders itself is not a
// feed a reader can follow, and the next person to open this file should find that decision
// written down rather than inferred from the absence of a ranking.
//
// THREE BOUNDED READS, MERGED HERE. Each kind of activity is one indexed query with its own limit
// (see internal/queries/sql/follow.sql for why they are not one UNION), and the merge is this
// package's whole job: filter, sort, cut to the page, and load the scenes.
package activity

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/scene"
	"github.com/stashapp/stash-box/internal/service/trust"
)

// Kind is what happened. The values match the `kind` column the arms produce.
type Kind string

const (
	// KindFingerprint is a submission: this user says they have this scene. The draft's
	// "favorites-equivalent", and the strongest thing a user states about a scene.
	KindFingerprint Kind = "FINGERPRINT"

	// KindListAdd is a scene filed into a list. The list's NAME is reported only when the list
	// is published -- see the query and the test that holds it.
	KindListAdd Kind = "LIST_ADD"

	// KindReview is a published review.
	KindReview Kind = "REVIEW"
)

// Tuning.
const (
	// defaultLimit is a feed page. Twenty rather than fifty: a feed is read, not scanned.
	defaultLimit = 20
	// maxLimit bounds what a caller may request; clamped rather than rejected, like the taste
	// feed's.
	maxLimit = 50
)

// HideUnpublishedList reports whether a list addition's list should be NAMED in the feed.
//
// The activity is "someone filed this scene", which the visibility grant covers; WHICH list they
// filed it into is theirs until they publish it. A private list's name reaching a follower's feed
// would be the visibility column protecting one thing while this row leaked another, so the rule
// is a named function with a test rather than a CASE in the query.
func HideUnpublishedList(published bool) bool { return !published }

// Item is one row of the feed, with its scene already loaded.
//
// The SCENE IS A LOADED MODEL and not an id the GraphQL layer resolves later, because the page is
// bounded (at most 50 rows) and one batched load is cheaper and simpler than 50 dataloader
// resolutions. A row whose scene is gone is dropped: the feed is about scenes a reader can open,
// and a row pointing at nothing is not a recommendation, it is a broken link.
type Item struct {
	Kind      Kind
	CreatedAt time.Time
	ActorID   uuid.UUID
	ActorName string
	Scene     *models.Scene
	// ListID and ListName are set only for a list addition into a PUBLISHED list.
	ListID   uuid.NullUUID
	ListName *string
	// Rating is set only for a review, and only when the review carried one: an unrated review
	// is normal (a review of a studio's ethics is not a 1-to-5 quantity).
	Rating *int
}

// FeedInput is one page of the feed.
type FeedInput struct {
	// ViewerID is whose feed this is, and also the user whose own activity is excluded: the
	// page is "what people you follow have been doing", not "here is what you did".
	ViewerID uuid.UUID
	// Limit is how many rows. Zero uses defaultLimit; above maxLimit is clamped.
	Limit int
	// Before is a KEYSET cursor: only activity strictly older than this instant. Nil means from
	// the top. Not an offset: a feed paged by OFFSET shows a reader the same row twice when
	// something is added between pages, which on a feed is the normal case.
	Before *time.Time
}

// Service is the feed service.
type Service struct {
	queries *queries.Queries
	scenes  *scene.Scene
}

// New creates the feed service.
func New(queries *queries.Queries, scenes *scene.Scene) *Service {
	return &Service{queries: queries, scenes: scenes}
}

// mergeItems applies the two rules that are NOT the queries': whose rows are dropped, and the
// order they are cut in.
//
// Pulled out of Feed so both are testable without a database, and so the ordering rules sit in one
// readable place rather than inside the middle of an ORM call sequence:
//
//   - The viewer's own activity is not "someone you follow did something".
//   - THE ORDER IS THE TIMESTAMP, descending, with a deterministic tiebreak. Two rows at the same
//     instant are ordered by kind then scene id, which is arbitrary and STABLE: a reader paging a
//     feed must not see a row move between pages because the sort was free to choose.
//
// NO RANKING, EVER. The draft says "no algorithmic reordering"; a score, a decay term or a
// diversity adjustment added here would be a change to what this page IS (see the package doc).
func mergeItems(items []Item, viewerID uuid.UUID, limit int) []Item {
	kept := items[:0]
	for _, item := range items {
		if item.ActorID == viewerID {
			continue
		}
		kept = append(kept, item)
	}
	items = kept

	sort.SliceStable(items, func(i, j int) bool {
		if !items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].CreatedAt.After(items[j].CreatedAt)
		}
		if items[i].Kind != items[j].Kind {
			return items[i].Kind < items[j].Kind
		}
		return items[i].Scene.ID.String() < items[j].Scene.ID.String()
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items
}

// dropAboveTier removes items whose scene is above the viewer's content-tier ceiling
// (phase 5, migration 120).
//
// FILTERED IN GO, unlike every other scene list in this repository, and the reason is the shape of
// this feed: it is assembled from three arms (fingerprints, list additions, reviews) that share no
// statement, each asked for at most `limit` rows, and the merge takes the newest `limit` overall.
// Pushing the predicate into each arm would add the same join to three statements whose visibility
// rules differ, and the cost of being wrong there is showing a reader what they opted out of.
// The cost here is a page that can come back SHORT when some of its items are above the ceiling --
// which is the same trade this function already makes when it drops the viewer's own items.
//
// The batch lookup is the entity_effective_tier view: a known, small set of ids, which is exactly
// what a view over every entity is good for.
func (s *Service) dropAboveTier(ctx context.Context, viewerID uuid.UUID, items []Item) ([]Item, error) {
	if len(items) == 0 {
		return items, nil
	}

	ceiling, err := s.queries.UserVisibleTier(ctx, viewerID)
	if err != nil {
		return nil, fmt.Errorf("reading the viewer's content-tier ceiling: %w", err)
	}
	if int(ceiling) >= trust.MaxContentTier {
		// Opted into everything: nothing to exclude, and the lookup is skipped rather than
		// run to discover an empty answer.
		return items, nil
	}

	ids := make([]uuid.UUID, 0, len(items))
	for _, item := range items {
		if item.Scene != nil {
			ids = append(ids, item.Scene.ID)
		}
	}

	tiers, err := s.queries.SceneEffectiveTiers(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("reading the candidate scenes' tiers: %w", err)
	}

	byScene := make(map[uuid.UUID]int16, len(tiers))
	for _, row := range tiers {
		byScene[row.EntityID] = row.Tier
	}
	return filterAboveCeiling(items, byScene, ceiling), nil
}

// filterAboveCeiling keeps the items whose scene is within the viewer's ceiling.
//
// PURE, and separated from the lookup above it, for the reason the content-access gate gives for
// being a pure function: the mistake that matters is a comparison, and a comparison shows up in a
// table rather than in a database. The wrapper touches two tables; this touches none.
//
// An item with NO scene is kept, and that is deliberate: this function's subject is a tier, and
// "has no scene" is not evidence about one -- dropping it here would be this function refusing
// something for a reason it is not about. A scene the map does not mention has no tags, so its
// tier is 0 and it is inside every ceiling.
func filterAboveCeiling(items []Item, tierByScene map[uuid.UUID]int16, ceiling int16) []Item {
	kept := items[:0]
	for _, item := range items {
		if item.Scene != nil && tierByScene[item.Scene.ID] > ceiling {
			continue
		}
		kept = append(kept, item)
	}
	return kept
}

// Feed returns one page, newest first.
func (s *Service) Feed(ctx context.Context, input FeedInput) ([]Item, error) {
	limit := input.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}

	// The actors who granted THIS viewer at least 'followers'. The public broadcast is applied
	// per arm in SQL, because it is a property of the actor rather than of this relationship.
	actorIDs, err := s.queries.FindVisibleActivityActors(ctx, input.ViewerID)
	if err != nil {
		return nil, fmt.Errorf("reading the visible actors: %w", err)
	}

	before := pgtype.Timestamptz{}
	if input.Before != nil {
		before = pgtype.Timestamptz{Time: *input.Before, Valid: true}
	}

	// Each arm is asked for AT MOST `limit` rows, and that is exactly enough: the merge takes
	// the newest `limit` overall, so no single arm can contribute more than that many. Asking
	// for more would make every page read rows it then throws away.
	if len(actorIDs) == 0 {
		// Nobody's activity reaches this viewer. Still ONE of the three reads has to run for
		// the public broadcast to be considered, and the arms do that with an empty array
		// rather than by skipping: a viewer who follows nobody must still see activity its
		// authors published to everyone.
		actorIDs = []uuid.UUID{}
	}

	items := make([]Item, 0, limit*3)

	fingerprints, err := s.queries.FindFingerprintActivity(ctx, queries.FindFingerprintActivityParams{
		ActorIds: actorIDs,
		Before:   before,
		RowLimit: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("reading fingerprint activity: %w", err)
	}
	for _, row := range fingerprints {
		items = append(items, Item{
			Kind: KindFingerprint,
			// A plain `timestamp` column, unlike the other two arms: sqlc maps TIMESTAMPTZ to
			// pgtype.Timestamptz and TIMESTAMP to time.Time, so the two arms genuinely differ
			// and the difference is the column, not a mistake here.
			CreatedAt: row.CreatedAt,
			ActorID:   row.ActorID,
			ActorName: row.ActorName,
			Scene:     &models.Scene{ID: row.SceneID},
		})
	}

	listAdds, err := s.queries.FindListActivity(ctx, queries.FindListActivityParams{
		ActorIds: actorIDs,
		Before:   before,
		RowLimit: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("reading list activity: %w", err)
	}
	for _, row := range listAdds {
		item := Item{
			Kind:      KindListAdd,
			CreatedAt: row.CreatedAt.Time,
			ActorID:   row.ActorID,
			ActorName: row.ActorName,
			// entity_id is nullable in the schema (a list may hold any entity), and this arm
			// filters it to non-null, so the UUID is the value by construction.
			Scene: &models.Scene{ID: row.SceneID.UUID},
		}
		if !HideUnpublishedList(row.ListPublished) {
			item.ListID = uuid.NullUUID{UUID: row.ListID, Valid: true}
			item.ListName = &row.ListName
		}
		items = append(items, item)
	}

	reviews, err := s.queries.FindReviewActivity(ctx, queries.FindReviewActivityParams{
		ActorIds: actorIDs,
		Before:   before,
		RowLimit: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("reading review activity: %w", err)
	}
	for _, row := range reviews {
		items = append(items, Item{
			Kind:      KindReview,
			CreatedAt: row.CreatedAt.Time,
			ActorID:   row.ActorID,
			ActorName: row.ActorName,
			Scene:     &models.Scene{ID: row.SceneID},
			Rating:    row.Rating,
		})
	}

	items = mergeItems(items, input.ViewerID, limit)
	if len(items) == 0 {
		return []Item{}, nil
	}

	// One load for the whole page, then the rows whose scene is missing are dropped. A scene
	// the loader does not return is one that no longer exists (or is not visible), and a feed
	// row about it would be a link to a 404.
	ids := make([]uuid.UUID, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.Scene.ID)
	}
	loaded, errs := s.scenes.LoadIds(ctx, ids)
	byID := make(map[uuid.UUID]*models.Scene, len(loaded))
	for i, sc := range loaded {
		if errs != nil && i < len(errs) && errs[i] != nil {
			continue
		}
		if sc != nil {
			byID[sc.ID] = sc
		}
	}

	out := make([]Item, 0, len(items))
	for _, item := range items {
		sc, ok := byID[item.Scene.ID]
		if !ok {
			continue
		}
		item.Scene = sc
		out = append(out, item)
	}

	// Content tiers (phase 5, migration 120), last: an item whose scene the loader dropped is
	// already gone, so the tier lookup runs over the page that survived rather than over the
	// page as fetched.
	return s.dropAboveTier(ctx, input.ViewerID, out)
}
