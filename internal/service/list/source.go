// Derived lists: a list whose entries are a saved search's current results (plan phase 8 slice 8d).
//
// THE WHOLE DESIGN IS ONE SENTENCE: membership is COMPUTED on every read, never stored. A list
// following "unwatched anal" shows what that search says NOW, so a scene that matches appears without
// anything being written and without a sweep job to keep two things in step. A snapshot refreshed on a
// schedule would have a window in which the list and its search disagree, and that window is the
// feature's entire value.
//
// TWO PROPERTIES THIS FILE EXISTS TO KEEP, AND EACH HAS A TEST THAT CAN FAIL:
//
//  1. THE SEARCH RUNS AS ITS OWNER, NEVER AS THE VIEWER. The filter can depend on who is asking
//     (`favorites:` means the asker's favourites), so a published derived list read as the viewer would
//     show every reader their OWN results while claiming to be one list. The run context is built here,
//     from the search's owner, and a test asserts the identity the runner was handed.
//  2. A DERIVED LIST'S ENTRIES ARE NOT ITS OWN TO EDIT. Adding, removing or reordering an entry would
//     change nothing -- the next read recomputes membership -- so each is refused by name rather than
//     silently discarded. "It looked like it worked and it was gone on reload" is the failure this
//     prevents.
package list

import (
	"context"
	"errors"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/stashapp/stash-box/internal/auth"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/query"
)

// SearchRunner is the whole of the scene service a derived list needs -- run a saved search's text as
// somebody, and count it -- and nothing else. An interface for the same two reasons
// savedsearch.SceneQuerier is one: the read path can be tested without a live search engine, and the
// service cannot quietly acquire a second dependency.
//
// The count is part of it because `itemCount` must be the SEARCH's total, not the length of the page
// this reader is being shown: a client rendering "100 of 4,312" needs both numbers, and deriving the
// second from the first would cap the displayed total at the page ceiling.
type SearchRunner interface {
	Query(ctx context.Context, input models.SceneQueryInput) ([]models.Scene, error)
	QueryCount(ctx context.Context, input models.SceneQueryInput) (int, error)
}

var (
	// ErrListHasItems refuses making a list derived when it already holds entries of its own. The
	// alternative -- discarding them -- is data loss the caller did not ask for, and the entries were
	// curated by hand.
	ErrListHasItems = errors.New("this list has entries of its own")

	// ErrDerivedList refuses an entry edit on a list whose membership is computed.
	ErrDerivedList = errors.New("this list's entries come from a saved search")

	// ErrNoSuchSearch is another member's saved search, or an id that does not exist. One error for
	// both, for the reason the read path gives one answer: reporting the difference is an oracle.
	ErrNoSuchSearch = errors.New("no saved search with that id")

	// ErrNoSearchRunner is an instance constructed without a search runner -- a wiring mistake, and one
	// that must be loud rather than an empty list.
	ErrNoSearchRunner = errors.New("this instance cannot run saved searches")
)

// derivedPositionStep matches the spacing the stored path uses when it reorders entries, so a derived
// entry and a stored one land in the same kind of gap.
const derivedPositionStep = 10

// derivedEntryType is the entity type a derived entry carries. A saved search is a SCENE query, so
// every entry a list derives is a scene -- a narrower set than a stored list may hold, and one the
// schema's own enum already names.
var derivedEntryType = string(models.ListEntityTypeEnumScene)

// derivedItemID mints the id a derived entry is presented with.
//
// DERIVED ENTRIES HAVE NO ROWS, so they have no ids, and a client still needs a stable key: a list
// rendered with fresh uuids on every read is a list whose rows are all new to the framework and whose
// selection is lost on every refresh. UUIDv5 over (list, entity) is deterministic, distinct per list,
// and DISTINCT FROM EVERY STORED ITEM's id in practice -- stored ids are v4, and the versions occupy
// disjoint bit patterns in the same field.
//
// The id is a PRESENTATION key and not a handle: RemoveItem resolves an item id to a row, and there is
// no row, so a client that passes one back gets "no such entry" -- and the refusal above means it would
// be refused earlier anyway.
func derivedItemID(listID, entityID uuid.UUID) uuid.UUID {
	return uuid.NewV5(uuid.NamespaceURL, listID.String()+":"+entityID.String())
}

// derivedSearchInput is the query a derived list runs.
//
// The defaults are SPELLED OUT for the reason the saved-search resolver spells out its own: an input
// built in Go does not get gqlgen's schema defaults, and the zero values are not "unset" -- `Page: 0`
// is a negative offset and an empty sort is no sort at all. `created_at` descending is the honest order
// for a list: newest addition first, and stable across reads in a way a relevance or date order is not.
func derivedSearchInput(queryText string) models.SceneQueryInput {
	return models.SceneQueryInput{
		Query:     &queryText,
		Page:      1,
		PerPage:   query.MaxPerPage,
		Sort:      models.SceneSortEnumCreatedAt,
		Direction: models.SortDirectionEnumDesc,
	}
}

// derivedRunContext puts the SEARCH OWNER on the context.
//
// This is the identity rule the whole slice turns on, and it is why the runner takes a context rather
// than a user id: the query path reads the current user, so the only way to run somebody's search is to
// BE them for the length of the call. The viewer's id is deliberately not consulted -- a published
// derived list is its owner's view, published.
func derivedRunContext(ctx context.Context, ownerID uuid.UUID, ownerName string) context.Context {
	return context.WithValue(ctx, auth.ContextUser, &auth.AuthUser{ID: ownerID, Name: ownerName})
}

// sourceSearchOf reads the saved search a list derives from, with its owner's identity.
func (s *Service) sourceSearchOf(ctx context.Context, listID uuid.UUID) (*queries.FindListSourceSearchRow, error) {
	row, err := s.queries.FindListSourceSearch(ctx, listID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// The list names a search the join cannot find. The foreign key makes that unreachable,
			// so it is reported as the missing list it behaves like rather than as a driver error.
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &row, nil
}

// derivedItems computes a derived list's current entries.
//
// A SINGLE PAGE, bounded by the query layer's own ceiling rather than by `maxItems`. The stored cap
// bounds what a list may HOLD; nothing is held here, so the only bound that matters is how much work
// one read may do -- and fetching 1,000 entries would mean ten sequential queries on a page a stranger
// can load. `itemCount` still reports the search's full total, so a client shows "100 of N" rather than
// believing the list is short.
func (s *Service) derivedItems(ctx context.Context, list queries.List) ([]queries.ListItem, error) {
	if s.search == nil {
		return nil, ErrNoSearchRunner
	}

	src, err := s.sourceSearchOf(ctx, list.ID)
	if err != nil {
		return nil, err
	}

	scenes, err := s.search.Query(derivedRunContext(ctx, src.UserID, src.OwnerName), derivedSearchInput(src.Query))
	if err != nil {
		return nil, err
	}

	out := make([]queries.ListItem, 0, len(scenes))
	for i := range scenes {
		out = append(out, queries.ListItem{
			ID:         derivedItemID(list.ID, scenes[i].ID),
			ListID:     list.ID,
			EntityType: &derivedEntryType,
			EntityID:   uuid.NullUUID{UUID: scenes[i].ID, Valid: true},
			Position:   (i + 1) * derivedPositionStep,
			// The scene's own creation time, so an entry does not claim to have been added to the
			// list today every time it is read -- a derived list has no addition event to report.
			CreatedAt: pgtype.Timestamptz{Time: scenes[i].CreatedAt, Valid: true},
		})
	}
	return out, nil
}

// derivedCount is how many entries the search matches in total.
func (s *Service) derivedCount(ctx context.Context, list queries.List) (int64, error) {
	if s.search == nil {
		return 0, ErrNoSearchRunner
	}
	src, err := s.sourceSearchOf(ctx, list.ID)
	if err != nil {
		return 0, err
	}
	count, err := s.search.QueryCount(derivedRunContext(ctx, src.UserID, src.OwnerName), derivedSearchInput(src.Query))
	if err != nil {
		return 0, err
	}
	return int64(count), nil
}

// SourceSearchID returns the saved search a list follows, or nil for an ordinary list.
//
// Applies no visibility rule, like OwnerOf: it is read for a list that has already resolved, and the
// caller decides who may see the search itself -- which is the OWNER, and the resolver enforces that by
// looking the search up through the owner-scoped read.
func (s *Service) SourceSearchID(ctx context.Context, listID uuid.UUID) (*uuid.UUID, error) {
	row, err := s.queries.FindList(ctx, listID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if !row.SourceSavedSearchID.Valid {
		return nil, nil
	}
	id := row.SourceSavedSearchID.UUID
	return &id, nil
}

// SetSourceSearch makes a list follow a saved search.
//
// THE SEARCH MUST BE THE CALLER'S OWN, and it is checked through the owner-scoped read rather than a
// lookup by id. Attaching a stranger's standing question to your own list would let a member PUBLISH
// somebody else's criteria as their results -- and the criteria are not public: a derived list exposes
// its source to its owner and to nobody else.
//
// A LIST THAT HOLDS ENTRIES OF ITS OWN IS REFUSED (ErrListHasItems), because becoming derived would
// discard them. The caller empties the list first, which is a decision they make rather than one this
// call makes for them.
func (s *Service) SetSourceSearch(ctx context.Context, listID, actorID, searchID uuid.UUID) error {
	current, err := s.Get(ctx, listID, actorID)
	if err != nil {
		return err
	}
	if current.OwnerID != actorID {
		return ErrNotOwner
	}

	if _, err := s.queries.FindSavedSearchForUser(ctx, queries.FindSavedSearchForUserParams{
		ID:     searchID,
		UserID: actorID,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoSuchSearch
		}
		return err
	}

	count, err := s.queries.CountListItems(ctx, listID)
	if err != nil {
		return err
	}
	if count > 0 {
		return ErrListHasItems
	}

	rows, err := s.queries.SetListSourceSearch(ctx, queries.SetListSourceSearchParams{
		ID:                  listID,
		OwnerID:             actorID,
		SourceSavedSearchID: uuid.NullUUID{UUID: searchID, Valid: true},
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		// Unreachable after the checks above, and kept because a write that cannot say whether it
		// wrote is how a permission check becomes decorative.
		return ErrNotOwner
	}
	return nil
}

// ClearSourceSearch turns a derived list back into an ordinary one.
//
// THE ENTRIES ARE NOT COPIED INTO IT. The list becomes empty, which is what "stop following this
// search" means: the alternative -- silently materialising the current results as stored entries --
// would turn a derived list into a snapshot nobody asked for, and the owner could no longer tell which
// of their lists track anything.
//
// This is also why deleting a saved search that a list follows is RESTRICTed at the database rather
// than resolved here: a source going away on its own must not quietly produce this same empty list.
func (s *Service) ClearSourceSearch(ctx context.Context, listID, actorID uuid.UUID) error {
	current, err := s.Get(ctx, listID, actorID)
	if err != nil {
		return err
	}
	if current.OwnerID != actorID {
		return ErrNotOwner
	}

	rows, err := s.queries.SetListSourceSearch(ctx, queries.SetListSourceSearchParams{
		ID:      listID,
		OwnerID: actorID,
		// The invalid NullUUID IS the NULL the column takes: one write path, so the ownership rule
		// cannot be implemented twice.
		SourceSavedSearchID: uuid.NullUUID{},
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotOwner
	}
	return nil
}
