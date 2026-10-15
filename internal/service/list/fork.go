package list

import (
	"context"
	"errors"
	"fmt"

	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/queries"
)

// Forking a list (plan phase 8 slice 8c).
//
// A FORK IS A COPY THAT REMEMBERS WHERE IT CAME FROM AND TRACKS NOTHING. It exists to be edited: the
// moment it is created it is independent, so later changes to the source do not reach it and changes
// to it do not reach the source. The alternative -- a list that follows somebody else's -- is the
// auto-updating list (slice 8d), and conflating the two would mean a member's own list changing under
// them because a stranger edited theirs.

// ErrForkWouldExceedCap: the source has more entries than one list may hold, so a copy of it would be
// over the bound before it was even edited.
var ErrForkWouldExceedCap = errors.New("that list holds more entries than a fork may")

// CanFork decides whether a list of this size may be copied, without reading anything.
//
// The cap is enforced on the way IN elsewhere (AddItem counts before it inserts), and a bulk copy is
// the one path that would walk straight past that check: it inserts every row in a single statement,
// so there is no per-item moment at which a count could refuse. Written as a rule rather than as an
// inline `if`, so the bound has one statement of it.
func CanFork(sourceItems int) error {
	if sourceItems > maxItems {
		return ErrForkWouldExceedCap
	}
	return nil
}

// Fork copies a list into one owned by actorID.
//
// VISIBILITY IS THE SOURCE'S OWN RULE, REUSED RATHER THAN RESTATED: `Get` already decides that a
// published list is readable by anyone and a draft only by its owner, and it reports somebody else's
// draft as ErrNotFound rather than "not yours" so ids cannot be enumerated. A fork that answered
// differently would be a second implementation of a privacy rule, and the copy would be a way to read
// a list that cannot be read.
//
// A FORK OF A PUBLISHED LIST STARTS UNPUBLISHED -- the source owner's publication is their decision
// and not something a copy inherits -- and it inherits the source's description when the caller gives
// none, because a fork is meant to be "that list, mine" and an empty description is a blank the
// forker has to fill in again.
func (s *Service) Fork(ctx context.Context, sourceID, actorID uuid.UUID, name, description string) (*queries.List, error) {
	source, err := s.Get(ctx, sourceID, actorID)
	if err != nil {
		return nil, err
	}

	// The count is the SOURCE's total, which for a derived source is the search's total rather than a
	// page of it -- so the cap refuses a search too large to fork, before anything is written.
	count, err := s.CountItems(ctx, sourceID)
	if err != nil {
		return nil, err
	}
	if err := CanFork(int(count)); err != nil {
		return nil, err
	}

	// A FORK OF A DERIVED LIST TAKES ITS CURRENT ENTRIES AND TRACKS NOTHING. There are no rows to copy,
	// so the entries are computed first and written as this fork's own -- which is what makes the fork
	// independent, exactly as an ordinary fork is. Read BEFORE the transaction opens: the entries come
	// from another service's query, and holding a write transaction across it is how two locks get
	// taken in two orders.
	var derived []queries.ListItem
	if source.SourceSavedSearchID.Valid {
		derived, err = s.derivedItems(ctx, *source)
		if err != nil {
			return nil, err
		}
	}

	clean, err := normaliseName(name)
	if err != nil {
		return nil, err
	}
	if description == "" && source.Description != nil {
		description = *source.Description
	}
	if len(description) > maxDescriptionLength {
		return nil, ErrInvalidName
	}

	var ownerName string
	if u, err := s.queries.FindUser(ctx, actorID); err == nil {
		ownerName = u.Name
	}

	id := uuid.Must(uuid.NewV4())
	var created *queries.List
	err = s.withTxn(func(tx *queries.Queries) error {
		row, err := tx.CreateForkedList(ctx, queries.CreateForkedListParams{
			ID:          id,
			OwnerID:     actorID,
			Name:        clean,
			Description: nullableString(description),
			OwnerName:   ownerName,
			// The parent, named in the statement that creates the row: a list cannot exist with a
			// provenance that arrives later.
			ForkedFromID: uuid.NullUUID{UUID: sourceID, Valid: true},
		})
		if err != nil {
			return mapWriteError(err)
		}

		// A derived source has no rows to copy, so its computed entries are written one by one -- still
		// inside the transaction, so a fork cannot half-exist either way.
		if derived != nil {
			for i := range derived {
				if _, err := tx.AddListItem(ctx, queries.AddListItemParams{
					ListID:     id,
					EntityType: derived[i].EntityType,
					EntityID:   derived[i].EntityID,
					Position:   derived[i].Position,
				}); err != nil {
					return fmt.Errorf("copying the list's entries: %w", err)
				}
			}
			created = &row
			return nil
		}

		// One statement for a stored source, so a fork never half-exists. The count is checked rather
		// than the result: rows-affected would tell us the copy happened, not that it is within the
		// bound.
		copied, err := tx.CopyListItems(ctx, queries.CopyListItemsParams{
			DestinationID: id,
			SourceID:      sourceID,
		})
		if err != nil {
			return fmt.Errorf("copying the list's entries: %w", err)
		}
		if int(copied) > maxItems {
			return ErrForkWouldExceedCap
		}

		created = &row
		return nil
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}
