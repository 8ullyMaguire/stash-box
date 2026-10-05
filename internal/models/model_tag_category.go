package models

import (
	"time"

	"github.com/gofrs/uuid"
)

type TagCategory struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Group       string    `json:"group"`
	Description *string   `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`

	// ParentID is a NULLABLE UUID, matching a nullable GraphQL parent: null means this
	// category is top-level, or that its parent was deleted (ON DELETE SET NULL promotes
	// the child rather than cascading, so reorganising vocabulary cannot destroy it).
	ParentID uuid.NullUUID `json:"parent_id"`
}
