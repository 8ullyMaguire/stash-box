package models

import (
	"time"

	"github.com/gofrs/uuid"
	"github.com/stashapp/stash-box/internal/models/assign"
	"github.com/stashapp/stash-box/internal/models/validator"
)

type Tag struct {
	// ContentTier is how far this tag raises its entities out of a viewer's
	// default view (0-3, migration 120). A tier can only REMOVE visibility.
	ContentTier int `json:"content_tier"`

	// RequiresOptIn refuses this tag's entities to a viewer who has not opted
	// into content viewing at all, whatever the tier.
	RequiresOptIn bool      `json:"requires_opt_in"`
	ID            uuid.UUID `json:"id"`
	Name          string    `json:"name"`
	Description   *string   `json:"description,omitempty"`
	Deleted       bool      `json:"deleted"`
	CategoryID    uuid.NullUUID
	Created       time.Time `json:"created"`
	Updated       time.Time `json:"updated"`
}

func (Tag) IsSceneDraftTag() {}
func (t *Tag) IsEditTarget() {}

func (t Tag) IsDeleted() bool {
	return t.Deleted
}

func (t *Tag) CopyFromTagEdit(input TagEdit, existing *TagEdit) {
	assign.String(&t.Name, input.Name)
	assign.StringPtr(&t.Description, input.Description, existing.Description)
	assign.NullUUID(&t.CategoryID, input.CategoryID, existing.CategoryID)
}

func (t *Tag) ValidateModifyEdit(edit TagEditData) error {
	if err := validator.String("name", edit.Old.Name, t.Name); err != nil {
		return err
	}
	if err := validator.StringPtr("description", edit.Old.Description, t.Description); err != nil {
		return err
	}
	return validator.UUID("CategoryID", edit.Old.CategoryID, t.CategoryID)
}
