package edit

import (
	"errors"
	"testing"

	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/models"
)

func editWithOperation(op models.OperationEnum) *models.Edit {
	return &models.Edit{Operation: op.String()}
}

func inputFor(op models.OperationEnum, id *uuid.UUID) *models.EditInput {
	return &models.EditInput{Operation: op, ID: id}
}

var someID = uuid.Must(uuid.NewV7())

// Issue #729: a sceneEditUpdate naming MODIFY against a CREATE edit panicked
// with a nil pointer dereference, because the processors dereference
// input.Edit.ID directly and a create edit has no id.
func TestValidateEditTargetIDRefusesAnOperationThatDisagreesWithTheEdit(t *testing.T) {
	tests := []struct {
		name    string
		edit    models.OperationEnum
		input   models.OperationEnum
		id      *uuid.UUID
		wantErr bool
		errIs   error
	}{
		{"create edit updated as modify", models.OperationEnumCreate, models.OperationEnumModify, nil, true, ErrEditOperationMismatch},
		{"create edit updated as destroy", models.OperationEnumCreate, models.OperationEnumDestroy, nil, true, ErrEditOperationMismatch},
		{"create edit updated as merge", models.OperationEnumCreate, models.OperationEnumMerge, nil, true, ErrEditOperationMismatch},
		{"modify edit updated as create", models.OperationEnumModify, models.OperationEnumCreate, &someID, false, nil},
		{"modify edit updated as modify", models.OperationEnumModify, models.OperationEnumModify, &someID, false, nil},
		{"modify edit updated as destroy", models.OperationEnumModify, models.OperationEnumDestroy, &someID, true, ErrEditOperationMismatch},
		{"create edit updated as create", models.OperationEnumCreate, models.OperationEnumCreate, nil, false, nil},
		{"non-create operation with no id", models.OperationEnumModify, models.OperationEnumModify, nil, true, ErrEditOperationMismatch},
		{"operation omitted keeps the edit's own", models.OperationEnumCreate, "", &someID, false, nil},
		{"operation omitted on a modify edit", models.OperationEnumModify, "", &someID, false, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateEditTargetID(editWithOperation(tt.edit), inputFor(tt.input, tt.id))

			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected a refusal, got nil")
				}
				if !errors.Is(err, tt.errIs) {
					t.Fatalf("got error %v, want it to wrap %v", err, tt.errIs)
				}
				return
			}

			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}

// The guard must not fire on the nil inputs the update paths can hand it, or
// it would break every other update.
func TestValidateEditTargetIDToleratesNilInput(t *testing.T) {
	edit := editWithOperation(models.OperationEnumModify)

	if err := validateEditTargetID(edit, nil); err != nil {
		t.Fatalf("nil input should be tolerated, got %v", err)
	}
	if err := validateEditTargetID(nil, inputFor(models.OperationEnumModify, &someID)); err != nil {
		t.Fatalf("nil edit should be tolerated, got %v", err)
	}
}

// A stored operation this build does not recognise is not something to
// reinterpret as a match; it must not be reported as a mismatch either.
func TestValidateEditTargetIDLeavesAnUnknownStoredOperationAlone(t *testing.T) {
	edit := &models.Edit{Operation: "SOMETHING_ELSE"}

	if err := validateEditTargetID(edit, inputFor(models.OperationEnumModify, &someID)); err != nil {
		t.Fatalf("unknown stored operation should not be refused here, got %v", err)
	}
}
