package edit

import (
	"fmt"

	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/pkg/utils"
)

// An operation that targets an existing entity needs its id. CREATE does not:
// a create edit has no target yet, so EditInput.ID is nil for it (see the
// generated EditInput, "Not required for create type").
//
// The per-operation processors dereference input.Edit.ID directly, so a
// mismatch between the edit's own operation and the operation supplied on an
// update reaches them as a nil dereference rather than a validation error
// (issue #729). Checking here covers every one of those processors with a
// single guard, next to the other cross-field checks.
//
// An operation left unset means "keep the edit's own": an update carries only
// the fields being changed, and callers that omit the operation update a create
// edit as a create edit. So the guard only fires on an operation that was
// actually supplied and disagrees.
func validateEditTargetID(edit *models.Edit, input *models.EditInput) error {
	if edit == nil || input == nil {
		return nil
	}

	if input.Operation == "" {
		return nil
	}

	if input.Operation == models.OperationEnumCreate {
		return nil
	}

	var existing models.OperationEnum
	if !utils.ResolveEnumString(edit.Operation, &existing) {
		// An unrecognised stored operation is not something to reinterpret as
		// a match; leave it to the processor that owns that operation.
		return nil
	}

	if input.ID == nil {
		return fmt.Errorf("%w: %s requires an id", ErrEditOperationMismatch, input.Operation)
	}

	if existing != input.Operation {
		return fmt.Errorf("%w: edit is a %s edit and cannot be updated as %s",
			ErrEditOperationMismatch, existing, input.Operation)
	}

	return nil
}
