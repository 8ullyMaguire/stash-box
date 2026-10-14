package savedsearch

import (
	"context"
	"encoding/json"

	"github.com/gofrs/uuid"
	"github.com/stashapp/stash-box/internal/auth"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/pkg/logger"
)

// SceneQuerier is the whole of the scene service this job needs -- run a query, count a query --
// and nothing else. An interface rather than the concrete service so the job can be exercised
// against a real database without starting a scheduler, and so it cannot quietly acquire a
// second dependency.
type SceneQuerier interface {
	Query(ctx context.Context, input models.SceneQueryInput) ([]models.Scene, error)
	QueryCount(ctx context.Context, input models.SceneQueryInput) (int, error)
}

// NotifyMatches tells each subscribed search's owner what it found since that search's cursor,
// and returns how many notifications it wrote.
//
// ONE NOTIFICATION PER SEARCH PER RUN, never one per match. A standing query over a busy
// instance would otherwise turn one busy hour into a mailbox: the payload carries the count, and
// the notification's own target is the NEWEST match, so opening it lands on something to look at
// rather than on a search to run again.
//
// THE CURSOR IS TAKEN BEFORE THE RUN and written afterwards, and it comes from the DATABASE's
// clock (SavedSearchNow) because `scenes.created_at` is stamped by that clock. A `timestamp`
// column has no zone, so a Go process in +02:00 would write a cursor two hours ahead of the rows
// it is compared with. Taking it before the run means a run that takes a minute cannot move the
// cursor past a scene that arrived while it was running.
//
// A search that has never run gets a cursor and NO notification. A search saved a year ago must
// not announce a year of matches the first time it is subscribed.
//
// A failure on ONE search is logged and the loop continues: one unparseable query (saved before
// a syntax tightening, say) must not stop every other reader being told.
func (s *SavedSearch) NotifyMatches(ctx context.Context, scenes SceneQuerier) (int, error) {
	savedSearches, err := s.queries.FindNotifyingSavedSearches(ctx)
	if err != nil {
		return 0, err
	}

	runAt, err := s.queries.SavedSearchNow(ctx)
	if err != nil {
		return 0, err
	}

	// The OWNER's identity goes into the context for each run, and it is not a formality. The
	// filter can depend on who is asking -- `favorites: PERFORMER` means the saved search
	// OWNER's favorites, not the server's -- and the query path reads the current user, so a run
	// with no user in the context is not merely imprecise, it fails. One lookup per distinct
	// owner per run, so ten searches by one person cost one query.
	owners := make(map[uuid.UUID]*auth.AuthUser)
	ownerOf := func(id uuid.UUID) (*auth.AuthUser, error) {
		if owner, ok := owners[id]; ok {
			return owner, nil
		}

		user, err := s.queries.FindUser(ctx, id)
		if err != nil {
			return nil, err
		}

		owner := &auth.AuthUser{ID: user.ID, Name: user.Name}
		owners[id] = owner
		return owner, nil
	}

	written := 0
	for _, savedSearch := range savedSearches {
		if savedSearch.LastNotifiedAt == nil {
			if err := s.MarkNotified(ctx, savedSearch.ID, runAt); err != nil {
				logger.Errorf("Error starting the cursor for saved search %s: %s", savedSearch.ID, err)
			}
			continue
		}

		owner, err := ownerOf(savedSearch.UserID)
		if err != nil {
			logger.Errorf("Error loading the owner of saved search %s: %s", savedSearch.ID, err)
			continue
		}
		runCtx := context.WithValue(ctx, auth.ContextUser, owner)

		query := savedSearch.Query
		input := models.SceneQueryInput{
			Query:        &query,
			CreatedSince: savedSearch.LastNotifiedAt,
			Page:         1,
			PerPage:      1,
			Sort:         models.SceneSortEnumCreatedAt,
			Direction:    models.SortDirectionEnumDesc,
		}

		// The count answers "did anything arrive" and the single scene answers "which one".
		// Deliberately the same filter: a count taken from a different one would let the
		// notification link to a scene the count does not include.
		count, err := scenes.QueryCount(runCtx, input)
		if err != nil {
			logger.Errorf("Error running saved search %s: %s", savedSearch.ID, err)
			continue
		}

		if count == 0 {
			if err := s.MarkNotified(ctx, savedSearch.ID, runAt); err != nil {
				logger.Errorf("Error advancing the cursor for saved search %s: %s", savedSearch.ID, err)
			}
			continue
		}

		matches, err := scenes.Query(runCtx, input)
		if err != nil {
			logger.Errorf("Error loading the newest match for saved search %s: %s", savedSearch.ID, err)
			continue
		}
		if len(matches) == 0 {
			// A count above zero with no row behind it is a contradiction. Logged rather than
			// written: a notification whose target does not exist is worse than a missing one.
			logger.Errorf("Saved search %s counted %d matches and returned none", savedSearch.ID, count)
			continue
		}

		payload, err := json.Marshal(map[string]any{
			"saved_search_id": savedSearch.ID.String(),
			"count":           count,
		})
		if err != nil {
			logger.Errorf("Error encoding the payload for saved search %s: %s", savedSearch.ID, err)
			continue
		}

		data := json.RawMessage(payload)
		if err := s.queries.CreateSavedSearchMatchNotification(ctx, queries.CreateSavedSearchMatchNotificationParams{
			UserID: savedSearch.UserID,
			ID:     matches[0].ID,
			Data:   &data,
		}); err != nil {
			logger.Errorf("Error notifying for saved search %s: %s", savedSearch.ID, err)
			continue
		}

		written++

		if err := s.MarkNotified(ctx, savedSearch.ID, runAt); err != nil {
			logger.Errorf("Error advancing the cursor for saved search %s: %s", savedSearch.ID, err)
		}
	}

	return written, nil
}
