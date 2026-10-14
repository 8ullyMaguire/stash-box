package edit

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/stashapp/stash-box/internal/converter"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/pkg/utils"
)

// ErrContentItemMergeUnsupported is what a merge of content items is refused with.
//
// A merge needs two rows that are the SAME thing, and that is an identity question: two
// performers with the same name, two studios that turned out to be one. A content item's
// kind is a media type, and two text items are two text items however alike they look. So
// this kind refuses MERGE by NAME rather than falling through to a generic "unsupported
// operation", because the moderator reading the failed edit's comment should learn why.
var ErrContentItemMergeUnsupported = errors.New(
	"content items cannot be merged: their kind is a media type, not an identity")

// ContentItemEditProcessor handles proposed changes to a content item.
//
// The shape every type here has: Edit() validates and stores an old/new diff on the edit,
// CreateJoin links the edit to its target, and apply() writes the change when the vote passes.
type ContentItemEditProcessor struct {
	mutator
}

func ContentItem(ctx context.Context, queries *queries.Queries, edit *models.Edit) *ContentItemEditProcessor {
	return &ContentItemEditProcessor{
		mutator{
			context: ctx,
			queries: queries,
			edit:    edit,
		},
	}
}

func (m *ContentItemEditProcessor) Edit(input models.ContentItemEditInput, inputArgs utils.ArgumentsQuery) error {
	// Details are required for create and modify and NOT for destroy: a destroy names the
	// item and carries no fields, so a check here would refuse the one operation that needs
	// nothing but a target.
	switch input.Edit.Operation {
	case models.OperationEnumCreate:
		return m.createEdit(input, inputArgs)
	case models.OperationEnumModify:
		return m.modifyEdit(input, inputArgs)
	case models.OperationEnumDestroy:
		return m.destroyEdit(input)
	case models.OperationEnumMerge:
		return ErrContentItemMergeUnsupported
	}

	return fmt.Errorf("unsupported operation %q", input.Edit.Operation.String())
}

func (m *ContentItemEditProcessor) createEdit(input models.ContentItemEditInput, inputArgs utils.ArgumentsQuery) error {
	if input.Details == nil {
		// A create with no details would store an empty diff, which applies as a no-op and
		// reads to a voter as an edit that does nothing.
		return errors.New("content item edits need details")
	}
	data, err := input.Details.ContentItemEditFromCreateChecked(inputArgs)
	if err != nil {
		return err
	}
	if data.New == nil || data.New.Kind == nil || data.New.Title == nil {
		// Refused at submit rather than at apply: the row cannot be written without these
		// two, and an edit that cannot apply is one a voter should never be asked about.
		return errors.New("a content item needs a kind and a title")
	}
	return m.edit.SetData(data)
}

func (m *ContentItemEditProcessor) modifyEdit(input models.ContentItemEditInput, inputArgs utils.ArgumentsQuery) error {
	if input.Details == nil {
		return errors.New("content item edits need details")
	}
	if input.Edit.ID == nil {
		return errors.New("a modify needs the id of the item it changes")
	}

	// GetContentItem filters soft-deleted rows, so a deleted item cannot be modified -- the
	// apply would fail anyway, and this refuses it while the submitter can still change it.
	row, err := m.queries.GetContentItem(m.context, *input.Edit.ID)
	if err != nil {
		return fmt.Errorf("%w: content item %s: %w", ErrEntityNotFound, input.Edit.ID.String(), err)
	}
	item := converter.ContentItemToModel(row)

	data, err := input.Details.ContentItemEditFromDiffChecked(item, inputArgs.Field("details"))
	if err != nil {
		return err
	}
	if reflect.DeepEqual(data.Old, data.New) {
		return ErrNoChanges
	}
	return m.edit.SetData(data)
}

func (m *ContentItemEditProcessor) destroyEdit(input models.ContentItemEditInput) error {
	if input.Edit.ID == nil {
		return errors.New("a destroy needs the id of the item it removes")
	}
	_, err := m.queries.GetContentItem(m.context, *input.Edit.ID)
	if err != nil {
		return fmt.Errorf("%w: content item %s: %w", ErrEntityNotFound, input.Edit.ID.String(), err)
	}
	return nil
}

// CreateJoin links the edit to the item it names.
//
// Nothing for a create: the item does not exist until the edit applies, and the apply writes
// the join itself. The statement is idempotent, so a re-submitted edit that already has a
// join row is not an error.
func (m *ContentItemEditProcessor) CreateJoin(input models.ContentItemEditInput) error {
	if input.Edit.ID == nil {
		return nil
	}
	return m.queries.CreateContentItemEdit(m.context, queries.CreateContentItemEditParams{
		EditID:        m.edit.ID,
		ContentItemID: *input.Edit.ID,
	})
}

func (m *ContentItemEditProcessor) apply() error {
	operation := m.operation()

	data, err := m.edit.GetContentItemData()
	if err != nil {
		return err
	}

	switch operation {
	case models.OperationEnumCreate:
		if data.New == nil || data.New.Kind == nil || data.New.Title == nil {
			return errors.New("a content item needs a kind and a title")
		}

		id, err := uuid.NewV7()
		if err != nil {
			return err
		}
		row, err := m.queries.CreateContentItem(m.context, queries.CreateContentItemParams{
			ID:          id,
			Kind:        string(*data.New.Kind),
			Title:       *data.New.Title,
			Details:     data.New.Details,
			ReleaseDate: releaseDateOrNull(data.New.ReleaseDate),
		})
		if err != nil {
			return err
		}
		// The join, so the edit that created the item can be found from the item afterwards.
		return m.queries.CreateContentItemEdit(m.context, queries.CreateContentItemEditParams{
			EditID:        m.edit.ID,
			ContentItemID: row.ID,
		})

	case models.OperationEnumModify:
		row, err := m.targetItem()
		if err != nil {
			return err
		}
		item := converter.ContentItemToModel(row)
		if data.New == nil {
			return errors.New("a modify with nothing to change")
		}
		if err := item.CopyFromContentItemEdit(*data.New); err != nil {
			return err
		}
		_, err = m.queries.UpdateContentItem(m.context, queries.UpdateContentItemParams{
			ID:          item.ID,
			Kind:        string(item.Kind),
			Title:       item.Title,
			Details:     item.Details,
			ReleaseDate: releaseDateOrNull(item.ReleaseDate),
		})
		return err

	case models.OperationEnumDestroy:
		row, err := m.targetItem()
		if err != nil {
			return err
		}
		return m.queries.SoftDeleteContentItem(m.context, row.ID)
	}

	return fmt.Errorf("unsupported operation %q", operation.String())
}

// targetItem resolves the item an edit is about, through the join the apply can rely on.
func (m *ContentItemEditProcessor) targetItem() (queries.ContentItem, error) {
	res, err := m.queries.GetEditTargetID(m.context, m.edit.ID)
	if err != nil {
		return queries.ContentItem{}, err
	}
	if res.ID.IsNil() {
		// The CASE has no arm for this target type, or the join row is missing. Named
		// rather than allowed to fall through to a not-found from the next statement, which
		// would report the item as deleted instead of the edit as unlinked.
		return queries.ContentItem{}, fmt.Errorf("%w: the edit has no content item linked", ErrEntityNotFound)
	}
	row, err := m.queries.GetContentItem(m.context, res.ID)
	if err != nil {
		return queries.ContentItem{}, fmt.Errorf("%w: content item %s: %w", ErrEntityNotFound, res.ID.String(), err)
	}
	return row, nil
}

// releaseDateOrNull converts an edit's *string date to the column's type.
//
// The empty string means "no date" -- that is how a client clears one, since null means
// "leave it alone" -- and anything unparseable was already refused at submit time, so a
// parse failure here is a bug rather than user input and lands as NULL rather than as a
// fabricated date.
func releaseDateOrNull(date *string) pgtype.Date {
	if date == nil || *date == "" {
		return pgtype.Date{}
	}
	parsed, err := time.Parse("2006-01-02", *date)
	if err != nil {
		return pgtype.Date{}
	}
	return pgtype.Date{Time: parsed, Valid: true}
}
