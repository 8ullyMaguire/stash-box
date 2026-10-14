// Package savedsearch stores search-language queries a reader wants to keep, re-run, and
// optionally be told about.
//
// THE STORED TEXT IS CANONICAL. The filter is derived by parsing that text on every run
// (docs/spec/feature-discovery.md section 3.2), so there is one source of truth, the row stays
// readable and editable by the person who owns it, and a saved search cannot answer differently
// from the statement it stores -- it goes through the same parse -> resolve -> builder path a
// live query does.
//
// The cost of that choice, paid at resolution and stated there: a tag merge or rename is not
// rewritten into the stored text. A merge is transparent because resolution follows the
// redirect; a rename leaves the old name unresolvable and the run reports it as a diagnostic
// beside an empty result rather than narrowing in silence.
package savedsearch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stashapp/stash-box/internal/converter"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/errutil"
	"github.com/stashapp/stash-box/internal/service/search"
)

type SavedSearch struct {
	queries *queries.Queries
	withTxn queries.WithTxnFunc
}

func NewSavedSearch(queries *queries.Queries, withTxn queries.WithTxnFunc) *SavedSearch {
	return &SavedSearch{
		queries: queries,
		withTxn: withTxn,
	}
}

// WithTxn executes a function within a transaction
func (s *SavedSearch) WithTxn(fn func(*queries.Queries) error) error {
	return s.withTxn(fn)
}

// ErrNotFound is returned by the mutations when the row is not this user's.
var ErrNotFound = errors.New("no saved search with that id")

// validate refuses what could never work, at save time instead of on every run.
//
// A SYNTAX error is refused: an unparseable statement fails every time it runs, and the reader
// would learn that from a search that quietly returns nothing. A term the filter cannot apply
// YET is accepted -- it parses, its run reports it in `diagnostics`, and the reader may be
// waiting for it to mean something. Refusing it would make the feature's first honest report
// impossible to save.
func validate(name, query string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("a saved search needs a name")
	}
	if strings.TrimSpace(query) == "" {
		return errors.New("a saved search needs a query")
	}
	if _, err := search.Parse(query); err != nil {
		return fmt.Errorf("that is not valid search language: %w", err)
	}
	return nil
}

func (s *SavedSearch) FindByUser(ctx context.Context, userID uuid.UUID) ([]models.SavedSearch, error) {
	rows, err := s.queries.FindSavedSearchesByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	return converter.SavedSearchesToModels(rows), nil
}

// FindForUser returns nil when the row belongs to somebody else. Not an error, and not a
// different message: another person's id and an id that does not exist have to look the same,
// or the endpoint answers "this exists but is not yours".
func (s *SavedSearch) FindForUser(ctx context.Context, userID uuid.UUID, id uuid.UUID) (*models.SavedSearch, error) {
	row, err := s.queries.FindSavedSearchForUser(ctx, queries.FindSavedSearchForUserParams{
		ID:     id,
		UserID: userID,
	})
	if err != nil {
		if err := errutil.IgnoreNotFound(err); err != nil {
			return nil, err
		}
		return nil, nil
	}

	model := converter.SavedSearchToModel(row)
	return &model, nil
}

func (s *SavedSearch) Create(ctx context.Context, userID uuid.UUID, input models.SavedSearchInput) (*models.SavedSearch, error) {
	if err := validate(input.Name, input.Query); err != nil {
		return nil, err
	}

	// The name is unique per owner, and the message matters: the reader typed a name, not a
	// constraint. The unique index stays as the real guarantee -- two concurrent creates still
	// cannot both win, and that failure reaches the caller as an error either way.
	if existing, err := s.queries.FindSavedSearchByUserAndName(ctx, queries.FindSavedSearchByUserAndNameParams{
		UserID: userID,
		Name:   input.Name,
	}); err == nil && existing.ID != uuid.Nil {
		return nil, fmt.Errorf("you already keep a search called %q", input.Name)
	}

	notify := false
	if input.Notify != nil {
		notify = *input.Notify
	}

	row, err := s.queries.CreateSavedSearch(ctx, queries.CreateSavedSearchParams{
		UserID: userID,
		Name:   input.Name,
		Query:  input.Query,
		Notify: notify,
	})
	if err != nil {
		return nil, err
	}

	model := converter.SavedSearchToModel(row)
	return &model, nil
}

func (s *SavedSearch) Update(ctx context.Context, userID uuid.UUID, input models.SavedSearchUpdateInput) (*models.SavedSearch, error) {
	if err := validate(input.Name, input.Query); err != nil {
		return nil, err
	}

	notify := false
	if input.Notify != nil {
		notify = *input.Notify
	}

	row, err := s.queries.UpdateSavedSearch(ctx, queries.UpdateSavedSearchParams{
		ID:     input.ID,
		UserID: userID,
		Name:   input.Name,
		Query:  input.Query,
		Notify: notify,
	})
	if err != nil {
		if err := errutil.IgnoreNotFound(err); err != nil {
			return nil, err
		}
		return nil, ErrNotFound
	}

	model := converter.SavedSearchToModel(row)
	return &model, nil
}

// Delete removes the search and the notifications it produced. The notifications go first and
// in the same call: one that outlives its search is a row whose `data` names something that no
// longer exists, and the notification list has to render every unread row it returns.
func (s *SavedSearch) Delete(ctx context.Context, userID uuid.UUID, id uuid.UUID) (bool, error) {
	found := false
	err := s.withTxn(func(tx *queries.Queries) error {
		if _, err := tx.FindSavedSearchForUser(ctx, queries.FindSavedSearchForUserParams{
			ID:     id,
			UserID: userID,
		}); err != nil {
			if err := errutil.IgnoreNotFound(err); err != nil {
				return err
			}
			return nil
		}
		found = true

		if err := tx.DeleteSavedSearchMatchNotifications(ctx, id.String()); err != nil {
			return err
		}

		if _, err := tx.DeleteSavedSearch(ctx, queries.DeleteSavedSearchParams{
			ID:     id,
			UserID: userID,
		}); err != nil {
			return err
		}

		return nil
	})

	return found, err
}

// FindNotifying returns every subscribed search, oldest-updated first. It is the one read here
// that is NOT scoped to a user, because the job runs for everybody; it returns the raw rows and
// the job never hands them to a request.
func (s *SavedSearch) FindNotifying(ctx context.Context) ([]queries.SavedSearch, error) {
	return s.queries.FindNotifyingSavedSearches(ctx)
}

// MarkNotified moves the notification cursor. The forward-only check is in the caller's hands by
// design: the job passes the instant it took `now` from, so a run that takes two minutes cannot
// move the cursor past a scene that arrived while it was running.
func (s *SavedSearch) MarkNotified(ctx context.Context, id uuid.UUID, at time.Time) error {
	return s.queries.SetSavedSearchNotifiedAt(ctx, queries.SetSavedSearchNotifiedAtParams{
		ID:             id,
		LastNotifiedAt: &at,
	})
}
