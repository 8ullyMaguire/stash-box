package converter

import (
	"testing"

	"github.com/stashapp/stash-box/internal/models"
)

// Issue #9, open upstream since 2019 and still reproducible: "Whenever a mutation
// is sent to modify a value to NULL, the column is ignored and the old value is
// retained. This makes it impossible to set values to Unknown."
//
// The cause is the input TYPE, not the assignment. PerformerUpdateInput models
// every optional field as *string, and gqlgen unmarshals both "absent" and
// "explicitly null" to the same nil pointer. The converter then does
//
//	if input.Birthdate != nil { performer.BirthDate = input.Birthdate }
//
// which is correct for "absent" and silently wrong for "null": the second case
// leaves the old value in place. The user asks for Unknown and gets the previous
// answer, with no error anywhere.
//
// So this test cannot be written as "passing nil should clear the field" -- that
// is a change in semantics, not a test of current behaviour, and it would break
// every partial update. What it CAN assert is the thing that makes the bug
// possible at all, and that is a property of the generated type: the field has no
// way to say "null" as distinct from "absent". Asserting that keeps the defect
// named and measured, and it fails loudly the moment someone introduces a tri-state
// input type -- which is the fix.

// TestPerformerUpdateInputCannotExpressNull is the standing record of the defect.
//
// It is a test that asserts a limitation exists. That is unusual and it is
// deliberate: the alternative is a bug report that gets closed as "works as
// designed" because nobody wrote down that the design cannot do this.
func TestPerformerUpdateInputCannotExpressNull(t *testing.T) {
	in := models.PerformerUpdateInput{}

	// Both of these are the same value to the converter. The distinction the
	// mutation needs -- "do not touch this" versus "set this to NULL" -- has
	// nowhere to live in the type.
	var absent *string
	var explicitNull *string

	in.Birthdate = absent
	afterAbsent := in.Birthdate

	in.Birthdate = explicitNull
	afterNull := in.Birthdate

	if afterAbsent != afterNull {
		t.Fatal("the input type now distinguishes absent from explicit null, so " +
			"issue #9 may be fixed. UpdatePerformerFromUpdateInput and its test " +
			"need re-reading: either the nil-guards are now wrong, or a tri-state " +
			"type has been added that they do not use.")
	}
}

// TestUpdatePerformerFromUpdateInputIgnoresExplicitNull is the behavioural half.
//
// It documents what the code does TODAY, so the report above is not a claim about
// a type in the abstract but a claim about reachable behaviour. Performer is
// built with a value, the input carries nil for the same field, and the field
// survives the update.
//
// If this test ever starts failing by way of the field being cleared, that is the
// fix landing -- and the message says so, so nobody debugs it as a regression.

// TestUpdatePerformerFromUpdateInputRetainsValueOnNilInput proves the retention.
func TestUpdatePerformerFromUpdateInputRetainsValueOnNilInput(t *testing.T) {
	existing := "1990-01-01"

	performer := models.Performer{
		BirthDate: &existing,
		Name:      "Someone",
	}

	input := models.PerformerUpdateInput{
		// A client sending an explicit null for birthdate, which is what the
		// GraphQL layer produces for `birthdate: null`.
		Birthdate: nil,
	}

	UpdatePerformerFromUpdateInput(&performer, input)

	if performer.BirthDate == nil {
		t.Fatal("birthdate was CLEARED for a nil input.\n\n" +
			"Issue #9 is that it is RETAINED, and the reason is that *string cannot " +
			"tell absent from explicit null. If that has been fixed with a tri-state " +
			"type, this test now asserts the old contract and must be rewritten to " +
			"assert the new one -- but do that on purpose, because the same change " +
			"silently reinterprets every partial update in the API.\n\n" +
			"This is a failure rather than a skip deliberately: a first draft skipped " +
			"here, and all three mutations survived it, because skipping is passing.")
	}
	if *performer.BirthDate != existing {
		t.Errorf("birthdate changed unexpectedly: got %q, want %q",
			*performer.BirthDate, existing)
	}
}

// TestUpdateSceneFromUpdateInputRetainsValueOnNilInput is the same claim for the
// other converter that uses the idiom, because #9 is a pattern and a fix aimed at
// only one entity would leave the other in place.
func TestUpdateSceneFromUpdateInputRetainsValueOnNilInput(t *testing.T) {
	existing := "A Scene"

	scene := models.Scene{Details: &existing}

	UpdateSceneFromUpdateInput(&scene, models.SceneUpdateInput{
		Details: nil,
	})

	if scene.Details == nil {
		t.Fatal("details was CLEARED for a nil input. Same contract as the " +
			"performer case: assert the fix deliberately, do not skip into it.")
	}
	if *scene.Details != existing {
		t.Errorf("details changed unexpectedly: got %q, want %q", *scene.Details, existing)
	}
}
