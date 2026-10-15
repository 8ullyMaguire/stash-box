package edit

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/stashapp/stash-box/internal/auth"
	"github.com/stashapp/stash-box/internal/converter"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/queries"
)

// Confirming an identification (plan phase 8 slice 8b).
//
// The phase's own verification is the shape of this file: a confirmed `performer_id` comment CREATES
// AN EDIT, and applying that edit links the performer. The failure mode it exists to prevent is a
// comment that silently writes the link, which is why nothing here touches a scene.

var (
	// ErrNotAnIdentification: the comment is not a suggestion, so there is nothing to confirm.
	ErrNotAnIdentification = errors.New("only a performer_id comment can be confirmed")
	// ErrCommentAlreadyConfirmed: this suggestion has already become an edit.
	ErrCommentAlreadyConfirmed = errors.New("this identification has already been confirmed")
	// ErrIdentificationNeedsPerformer: a performer_id comment that names nobody has nothing to offer.
	ErrIdentificationNeedsPerformer = errors.New("a performer_id comment must name a performer")
	// ErrIdentificationTargetNotAScene: slice 8b routes scene identifications; other targets are owed.
	ErrIdentificationTargetNotAScene = errors.New("only a scene identification can be confirmed yet")
)

// CanConfirm decides whether a comment may become an edit, WITHOUT reading anything.
//
// It is the schema's two CHECK constraints stated in Go -- the kind and the structured field, and
// that a confirmed comment is one that has not been confirmed before -- so the answer is the same
// whether a caller asks before or after the database has had its say. The CHECKs remain the
// guarantee; this is the message.
func CanConfirm(kind string, performerID uuid.NullUUID, confirmedAt *time.Time) error {
	if kind != string(CommentKindPerformerID) {
		return ErrNotAnIdentification
	}
	if !performerID.Valid {
		return ErrIdentificationNeedsPerformer
	}
	if confirmedAt != nil {
		return ErrCommentAlreadyConfirmed
	}
	return nil
}

// Identification is a confirmable comment and what it points at.
type Identification struct {
	Comment *models.EditComment
	// TargetID is the id of the scene (or other target) the comment's edit concerns.
	TargetID uuid.UUID
	// TargetType is that target's type, which decides whether this slice can route it.
	TargetType string
}

// IdentificationFor reads a comment and decides whether it can be confirmed, returning everything the
// caller needs to create the edit.
//
// The decision is made BEFORE any write, so a caller never creates an edit for a suggestion the
// database would refuse to mark.
func (s *Edit) IdentificationFor(ctx context.Context, commentID uuid.UUID) (*Identification, error) {
	row, err := s.queries.FindEditComment(ctx, commentID)
	if err != nil {
		return nil, err
	}
	if err := CanConfirm(row.Kind, row.PerformerID, row.ConfirmedAt); err != nil {
		return nil, err
	}

	target, err := s.queries.GetEditTargetID(ctx, row.EditID)
	if err != nil {
		return nil, err
	}
	return &Identification{
		Comment:    converter.EditCommentToModelPtr(row),
		TargetID:   target.ID,
		TargetType: target.TargetType,
	}, nil
}

// RecordConfirmation writes the outcome: which edit the suggestion became, who confirmed it, and when.
//
// THE UPDATE IS CONDITIONAL ON THE COMMENT STILL BEING UNCONFIRMED, which is the whole of the
// idempotence rule and lives in the query rather than in a read-then-write here: two moderators
// confirming at once cannot both win, because the second update matches no row and reports the same
// error the sequential case does.
//
// A FAILURE HERE LEAVES THE EDIT STANDING, and that is the direction to fail in. The alternative --
// recording the confirmation first -- is impossible by construction (the schema refuses a
// confirmation with no edit to point at) and would be wrong anyway: an extra edit is visible to
// moderators and can be refused, while a comment recorded as confirmed with no edit behind it is a
// suggestion nobody will ever look at again.
func (s *Edit) RecordConfirmation(ctx context.Context, commentID, editID uuid.UUID, reason *string) (*models.EditComment, error) {
	currentUser := auth.GetCurrentUser(ctx)

	var out *models.EditComment
	err := s.withTxn(func(tx *queries.Queries) error {
		row, err := tx.ConfirmEditComment(ctx, queries.ConfirmEditCommentParams{
			ID:              commentID,
			ConfirmedEditID: uuid.NullUUID{UUID: editID, Valid: true},
			ConfirmedByID:   uuid.NullUUID{UUID: currentUser.ID, Valid: true},
		})
		if err != nil {
			// No row: the condition matched nothing, which means somebody else confirmed it first.
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrCommentAlreadyConfirmed
			}
			return err
		}

		auditID, err := uuid.NewV7()
		if err != nil {
			return err
		}
		// The audit row's data is NOT NULL, so the payload is a real one rather than an empty
		// object: the two ids are what a moderator reading the log needs to follow the action,
		// because the target is the comment and the EDIT it became is not the target.
		payload, err := json.Marshal(map[string]any{
			"comment_id": commentID,
			"edit_id":    editID,
		})
		if err != nil {
			return err
		}
		_, err = tx.CreateModAudit(ctx, queries.CreateModAuditParams{
			ID:         auditID,
			Action:     queries.ModAuditActionPERFORMERCOMMENTCONFIRM,
			UserID:     uuid.NullUUID{UUID: currentUser.ID, Valid: true},
			TargetID:   commentID,
			TargetType: "EDIT_COMMENT",
			Data:       payload,
			Reason:     reason,
		})
		if err != nil {
			return err
		}

		out = converter.EditCommentToModelPtr(row)
		return nil
	})

	return out, err
}
