package validator

import (
	"fmt"
	"reflect"

	"github.com/gofrs/uuid"
)

type StringEnum interface {
	IsValid() bool
	String() string
}

type ErrEditPrerequisiteFailed struct {
	field    string
	expected any
	actual   any
}

func (e *ErrEditPrerequisiteFailed) Error() string {
	expected := "_blank_"
	if e.expected != "" {
		expected = fmt.Sprintf("**%v**", e.expected)
	}
	actual := "_blank_"
	if e.actual != "" {
		actual = fmt.Sprintf("**%v**", e.actual)
	}
	return fmt.Sprintf("Expected %s to be %s, but was %s.", e.field, expected, actual)
}

func newError(field string, expected any, actual any) error {
	return &ErrEditPrerequisiteFailed{field, expected, actual}
}

// String validates string fields
func String(field string, old *string, current string) error {
	if old != nil && *old != current {
		return newError(field, *old, current)
	}
	return nil
}

// StringPtr validates string pointer fields
func StringPtr(field string, old *string, current *string) error {
	if old != nil && current != nil {
		if *old != *current {
			return newError(field, *old, *current)
		}
	}
	return nil
}

// IntPtr validates int pointer fields
func IntPtr(field string, old *int, current *int) error {
	if old != nil && current != nil {
		if *old != *current {
			return newError(field, *old, current)
		}
	}
	return nil
}

// UUID validates UUID fields
func UUID(field string, old *uuid.UUID, current uuid.NullUUID) error {
	if old != nil && (!current.Valid || (*old != current.UUID)) {
		currentUUID := ""
		if current.Valid {
			currentUUID = current.UUID.String()
		}
		return newError(field, old.String(), currentUUID)
	}
	return nil
}

// EnumPtr validates enum pointer fields using generics
func EnumPtr[T StringEnum](field string, old *string, current *T) error {
	if old != nil && current != nil {
		currentVal := reflect.ValueOf(current)
		if !currentVal.IsNil() {
			currentEnum := currentVal.Elem().Interface().(T)
			if currentEnum.IsValid() && *old != currentEnum.String() {
				return newError(field, *old, currentEnum.String())
			}
		}
	}
	return nil
}

// ErrFieldTooLong is returned when a submitted value exceeds the length of the
// database column it will be written to.
type ErrFieldTooLong struct {
	Field    string
	Length   int
	Max      int
}

func (e *ErrFieldTooLong) Error() string {
	return fmt.Sprintf("%s is %d characters; the maximum is %d", e.Field, e.Length, e.Max)
}

// MaxStringLength is the length of the varchar(255) columns that hold these
// fields (see internal/database/migrations/postgres/01_initial.up.sql).
//
// It is a single constant rather than a per-field table because every
// constrained column in the schema is declared varchar(255); if a future
// migration narrows one of them, this constant is the single place to revisit
// and the tests will fail loudly rather than the change passing silently.
const MaxStringLength = 255

// MaxLength validates that a submitted string field fits its column.
//
// A nil value is accepted: it means the edit does not propose a value for the
// field, which is the same wire representation as an explicit deletion. Neither
// is a length violation, and conflating them here would reject ordinary edits
// (see issue #802 on why null and absent must stay distinct).
func MaxLength(field string, value *string) error {
	if value == nil {
		return nil
	}
	// Count runes, not bytes: the columns are sized in characters, so a
	// 255-character name of multi-byte text must be accepted.
	if n := len([]rune(*value)); n > MaxStringLength {
		return &ErrFieldTooLong{Field: field, Length: n, Max: MaxStringLength}
	}
	return nil
}
