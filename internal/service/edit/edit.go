package edit

import (
	"context"
	"errors"

	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/converter"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/pkg/utils"
)

var ErrNoChanges = errors.New("edit contains no changes")
var ErrMergeIDMissing = errors.New("merge target ID is required")
var ErrMergeTargetIsSource = errors.New("merge target cannot be used as source")
var ErrNoMergeSources = errors.New("no merge sources found")

// ErrPerformerAlreadyExists is returned when a create edit proposes a
// performer whose name and disambiguation already match an existing one.
//
// The database enforces this with a partial unique index
// (index_active_performers_on_name, migration 06), so without this check the
// contributor only finds out when the edit is *applied* -- after it has been
// voted on and closed -- and gets a raw
//
//	pq: duplicate key value violates unique constraint "index_active_performers_on_name"
//
// which names a database object rather than the entity that already exists
// (#950). Failing at apply time is also the worst possible moment: the voters
// have spent their vote on an edit that can never land.
var ErrPerformerAlreadyExists = errors.New("a performer with this name and disambiguation already exists")

// InputSpecifiedFunc is function that returns true if the qualified field name
// was specified in the input. Used to distinguish between nil/empty fields and
// unspecified fields
type InputSpecifiedFunc func(qualifiedField string) bool

type mutator struct {
	context context.Context
	edit    *models.Edit
	queries *queries.Queries
}

func (m *mutator) operation() models.OperationEnum {
	var operation models.OperationEnum
	utils.ResolveEnumString(m.edit.Operation, &operation)
	return operation
}

func (m *mutator) CreateEdit() (*models.Edit, error) {
	created, err := m.queries.CreateEdit(m.context, converter.EditToCreateParams(*m.edit))
	if err != nil {
		return nil, err
	}

	converted := converter.EditToModelPtr(created)
	m.edit = converted
	return converted, nil
}

func (m *mutator) UpdateEdit() (*models.Edit, error) {
	m.edit.UpdateCount++
	updatedEdit, err := m.queries.UpdateEdit(m.context, converter.EditToUpdateParams(*m.edit))
	if err != nil {
		return nil, err
	}

	if err := m.queries.ResetVotes(m.context, m.edit.ID); err != nil {
		return nil, err
	}
	return converter.EditToModelPtr(updatedEdit), nil
}

func (m *mutator) CreateComment(userID uuid.UUID, comment *string) error {
	if comment != nil && len(*comment) > 0 {
		text, err := linkCommentEntities(m.context, m.queries, *comment)
		if err != nil {
			return err
		}
		commentID, _ := uuid.NewV7()
		comment := models.NewEditComment(commentID, userID, m.edit, text)
		_, err = m.queries.CreateEditComment(m.context, converter.EditCommentToCreateParams(*comment))
		return err
	}

	return nil
}

type editApplyer interface {
	apply() error
}

func urlCompare(subject []models.URL, against []models.URL) (added []models.URL, missing []models.URL) {
	added = make([]models.URL, 0, len(subject))
	missing = make([]models.URL, 0, len(against))
	for _, s := range subject {
		newMod := true
		for _, a := range against {
			if s.URL == a.URL && s.SiteID == a.SiteID {
				newMod = false
			}
		}

		for _, a := range added {
			if s.URL == a.URL && s.SiteID == a.SiteID {
				newMod = false
			}
		}

		if newMod {
			added = append(added, s)
		}
	}

	for _, s := range against {
		removedMod := true
		for _, a := range subject {
			if s.URL == a.URL && s.SiteID == a.SiteID {
				removedMod = false
			}
		}

		for _, a := range missing {
			if s.URL == a.URL && s.SiteID == a.SiteID {
				removedMod = false
			}
		}

		if removedMod {
			missing = append(missing, s)
		}
	}
	return
}
