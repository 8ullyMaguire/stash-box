package api

import (
	"context"
	"fmt"

	"github.com/stashapp/stash-box/internal/auth"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/service/edit"
)

// Confirming an identification (plan phase 8 slice 8b).
//
// THE ORCHESTRATION LIVES HERE AND NOT IN THE EDIT SERVICE. The edit service cannot call the scene
// service, because the scene service is what CREATES edits -- the dependency runs that way, and
// reaching back is a cycle. So the resolver holds the two halves together: it asks the edit service
// what the comment is and whether it can be confirmed, asks the scene service to propose the change,
// and then asks the edit service to record what the proposal became.
//
// THE FAILURE MODE THIS SHAPE PREVENTS, and the plan's own words for the phase: a comment that
// silently writes the link. Nothing in this function touches a scene's performers directly -- the
// only write is a submitted edit, and applying it is a separate decision a moderator still has to
// make.
func (r *mutationResolver) ConfirmPerformerComment(ctx context.Context, input models.ConfirmPerformerCommentInput) (*models.Edit, error) {
	user := auth.GetCurrentUser(ctx)
	if user == nil {
		return nil, auth.ErrUnauthorized
	}

	// The decision comes first, so a suggestion the database would refuse to mark never gets an edit
	// created for it.
	identification, err := r.services.Edit().IdentificationFor(ctx, input.CommentID)
	if err != nil {
		return nil, err
	}
	if identification.TargetType != "SCENE" {
		// Slice 8b routes scene identifications. A performer or studio identification is a different
		// proposal with a different input shape, and it is owed rather than guessed at.
		return nil, edit.ErrIdentificationTargetNotAScene
	}

	// The scene's current performers, carried into the update as the caller has to state them: the
	// performer being identified is ADDED to what is there rather than replacing it, which is what
	// an appearance list means.
	existing, err := r.services.Scene().GetPerformers(ctx, identification.TargetID)
	if err != nil {
		return nil, err
	}
	performers := make([]models.PerformerAppearanceInput, 0, len(existing)+1)
	alreadyThere := false
	for _, appearance := range existing {
		if appearance.Performer == nil {
			continue
		}
		performers = append(performers, models.PerformerAppearanceInput{
			PerformerID: appearance.Performer.ID,
			As:          appearance.As,
		})
		if appearance.Performer.ID == identification.Comment.PerformerID.UUID {
			alreadyThere = true
		}
	}
	if !alreadyThere {
		performers = append(performers, models.PerformerAppearanceInput{
			PerformerID: identification.Comment.PerformerID.UUID,
		})
	}

	// THE SCENE'S DIRECT WRITE PATH IS NOT USED HERE, and finding that out was the most useful
	// mistake of this slice. Scene().Update calls tx.UpdateScene and updatePerformers directly: it
	// is how an approved edit is APPLIED, and calling it would have written the relationship -- the
	// exact failure the phase's verification names, "a comment that silently writes the link". The
	// compile error that surfaced it (Update returns a *Scene, this function returns an *Edit) was
	// the difference between a proposal and a fait accompli.
	//
	// CreateSceneEdit is the path every other scene change takes: it diffs the proposed appearances
	// against the scene's own and stores an edit for a moderator to decide on.
	note := fmt.Sprintf("Identification from edit comment %s", identification.Comment.ID)
	proposed, err := r.services.Edit().CreateSceneEdit(ctx, models.SceneEditInput{
		Edit: &models.EditInput{
			ID:        &identification.TargetID,
			Operation: models.OperationEnumModify,
			// The comment names the comment, so the proposal carries its own justification and a
			// moderator reading the edit knows why it exists.
			Comment: &note,
		},
		Details: &models.SceneEditDetailsInput{Performers: performers},
	})
	if err != nil {
		return nil, err
	}

	// Record what the suggestion became. If this fails because somebody else confirmed it first, the
	// edit above stands and is returned to nobody -- see the note in the plan: an extra edit is
	// reviewable and refusable, which is the direction to fail in.
	if _, err := r.services.Edit().RecordConfirmation(ctx, identification.Comment.ID, proposed.ID, input.Reason); err != nil {
		return nil, err
	}

	return proposed, nil
}
