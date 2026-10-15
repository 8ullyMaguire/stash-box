// Collaborative lists (plan phase 8 slice 8e): the named write set.
//
// THE DESIGN IN ONE SENTENCE: a list has exactly one OWNER, who may do anything to it, and a bounded
// NAMED set of COLLABORATORS, who may do one thing -- change its contents. The distinction is not
// stylistic. "Anybody who can see it may write it" makes every published list publicly writable, and
// "everybody may see it" is not a permission model, it is a moderation surface handed to strangers by
// a rename. So the write set is a list of names its owner typed, and nothing here derives from
// `published_at`.
//
// WHAT A COLLABORATOR MAY DO: add an entry, remove an entry, reorder entries. Every one of those is
// AUDITED with the actor, because a list written by two people cannot answer "who changed it" from its
// own state -- and an entry carries no author column for the trail to read.
//
// WHAT A COLLABORATOR MAY NOT DO, enumerated rather than implied, because each line is a decision and
// a reader should be able to disagree with a specific one: rename the list, re-describe it, delete it,
// publish it, unpublish it, make it follow (or stop following) a saved search, add another
// collaborator, or remove one -- including themselves, which they do not need to do because
// `RemoveCollaborator` lets a collaborator remove their OWN row. Those are the acts that change what
// the list IS or who may write it, and they stay with the owner. `requireWriter` refuses all of them
// through the same door the entry writes come in by: the owner checks in `Update`, `Delete`,
// `Publish`, `Unpublish`, `SetSourceSearch` and `ClearSourceSearch` are untouched by this slice, and
// that is deliberate rather than an oversight.
//
// THE OWNER IS NOT A ROW in list_collaborators. `lists.owner_id` already answers "may this person
// write this list", and a second copy is two answers to one question -- which is how one of them comes
// to be wrong. So every question about writing goes through requireWriter below, which is the only
// place the two halves are combined.
package list

import (
	"context"
	"errors"
	"fmt"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/stashapp/stash-box/internal/queries"
)

// MaxCollaborators bounds one list's write set.
//
// A bound rather than a limit anyone has asked for: the write set is a list a person maintains by hand,
// a list with fifty writers is a list nobody is answerable for, and an unbounded set on a public
// artefact is a shape somebody will find a use for that this feature did not intend. Twenty-five is
// generous for the feature's own description -- several people filling one list.
//
// Like maxItems, this is enforced by a count and the count is ADVISORY: two concurrent adds can land
// at 26. The bound exists to stop a write set growing without bound, not to be exact, and a lock to
// make it exact would serialise every add to gain one row.
const MaxCollaborators = 25

// ErrAlreadyCollaborator is returned when adding somebody who is already in the set.
var ErrAlreadyCollaborator = errors.New("that member is already a collaborator on this list")

// ErrNotCollaborator is returned when removing somebody who is not in the set.
//
// A refusal rather than a quiet success, matching ErrAlreadyPublished: a client that removes a name
// twice should not be told it removed something, because the second answer is not the same act as the
// first and a UI that displays "removed" twice is describing one change.
var ErrNotCollaborator = errors.New("that member is not a collaborator on this list")

// ErrOwnerIsNotACollaborator is returned when the owner is added to their own list's set.
//
// The owner is not a row here, so adding them would store a writer that already is one -- and then
// "remove collaborator" could remove the owner's own row, leaving a list whose owner is in the set on
// one read and not the next. Refused by name instead.
var ErrOwnerIsNotACollaborator = errors.New("the list's owner is not a collaborator on it")

// ErrUnknownUser is returned when a collaborator id does not resolve to a member.
//
// Checked before the insert rather than relying on the foreign key: 23503 is mapped to ErrNotFound
// across this package (a list or an entity that is gone), and reporting "list not found" for a
// mistyped USER id would send a caller looking at the wrong half of their request.
var ErrUnknownUser = errors.New("that user does not exist")

// requireWriter decides whether actorID may change this list's CONTENTS.
//
// THE ONLY PLACE the owner and the collaborator set are combined. Every entry write comes through here,
// and each of the owner-only methods checks `OwnerID != actorID` directly -- so a reader can see, by
// grepping for requireWriter, exactly which acts a collaborator gained.
//
// The refusal is ErrNotOwner, whose message ("not the owner of this list") is literally true for a
// caller who is neither the owner nor a collaborator, and which is the package's existing word for
// "this is not yours to change". A second sentinel here would be a distinction no caller can act on.
func (s *Service) requireWriter(ctx context.Context, list *queries.List, actorID uuid.UUID) error {
	if list.OwnerID == actorID {
		return nil
	}
	if actorID == uuid.Nil {
		return ErrNotOwner
	}
	isCollaborator, err := s.IsCollaborator(ctx, list.ID, actorID)
	if err != nil {
		return err
	}
	if !isCollaborator {
		return ErrNotOwner
	}
	return nil
}

// IsCollaborator reports whether a member is in a list's write set.
//
// The single read of list_collaborators that the POLICY uses. Its caller decides what the fact is worth
// -- canView takes it as an argument, requireWriter refuses on it -- which keeps the policy a pure
// function of a value rather than of a database.
func (s *Service) IsCollaborator(ctx context.Context, listID, userID uuid.UUID) (bool, error) {
	if userID == uuid.Nil {
		return false, nil
	}
	_, err := s.queries.FindListCollaborator(ctx, queries.FindListCollaboratorParams{
		ListID: listID,
		UserID: userID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// AddCollaborator puts a member in a list's write set. OWNER ONLY.
//
// Owner only, and that is the slice's other half: a collaborator may change the contents and may not
// change who else may. The alternative -- collaborators can invite -- is a list whose write set grows
// without its owner knowing, which is the feature this one is deliberately not.
//
// The order of the checks is the order of the questions: can the actor see this list at all (Get, which
// reports somebody else's draft as not-found), may they change who writes it, is the set even the kind
// of thing this list has, is the target a real member, is the target the owner, is there room, and is
// this a change. A duplicate is refused by name AND by the primary key: the check gives the useful
// message, and the constraint closes the race the check cannot.
func (s *Service) AddCollaborator(ctx context.Context, listID, userID, actorID uuid.UUID) error {
	current, err := s.Get(ctx, listID, actorID)
	if err != nil {
		return err
	}
	if current.OwnerID != actorID {
		return ErrNotOwner
	}
	// A derived list has no contents of its own to write, so it has no write set either. Refused by
	// name for the same reason its entries are: a collaborator added to a list where nothing can be
	// written is a permission that does nothing.
	if current.SourceSavedSearchID.Valid {
		return ErrDerivedList
	}
	if userID == current.OwnerID {
		return ErrOwnerIsNotACollaborator
	}
	if _, err := s.queries.FindUser(ctx, userID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrUnknownUser
		}
		return err
	}

	already, err := s.IsCollaborator(ctx, listID, userID)
	if err != nil {
		return err
	}
	if already {
		return ErrAlreadyCollaborator
	}

	count, err := s.queries.CountListCollaborators(ctx, listID)
	if err != nil {
		return err
	}
	if count >= MaxCollaborators {
		return fmt.Errorf("a list may have at most %d collaborators", MaxCollaborators)
	}

	return s.withTxn(func(tx *queries.Queries) error {
		if err := tx.AddListCollaborator(ctx, queries.AddListCollaboratorParams{
			ListID:  listID,
			UserID:  userID,
			AddedBy: uuid.NullUUID{UUID: actorID, Valid: true},
		}); err != nil {
			return mapWriteError(err)
		}
		return tx.RecordListAudit(ctx, queries.RecordListAuditParams{
			ListID:  listID,
			ActorID: uuid.NullUUID{UUID: actorID, Valid: true},
			Action:  ActionCollaboratorAdd,
		})
	})
}

// RemoveCollaborator takes a member out of a list's write set.
//
// TWO callers may do this, and the second is why this is not owner-only: the owner (who may remove
// anybody) and the collaborator THEMSELVES. Leaving a shared list should not require asking the person
// who invited you, and a member who cannot remove themselves would have to be removed, which is a
// request they may not want to make.
//
// Removing a collaborator does NOT remove what they added. Entries carry no author column, so nobody's
// contributions can be taken away with them -- which is also why the audit trail matters here: it is
// the only record that a particular entry came from the member who has just been removed.
func (s *Service) RemoveCollaborator(ctx context.Context, listID, userID, actorID uuid.UUID) error {
	current, err := s.Get(ctx, listID, actorID)
	if err != nil {
		return err
	}
	if current.OwnerID != actorID && userID != actorID {
		return ErrNotOwner
	}

	return s.withTxn(func(tx *queries.Queries) error {
		removed, err := tx.RemoveListCollaborator(ctx, queries.RemoveListCollaboratorParams{
			ListID: listID,
			UserID: userID,
		})
		if err != nil {
			return mapWriteError(err)
		}
		// execrows, so "nothing to remove" is a fact this can refuse instead of reporting a change
		// that did not happen. The count is checked INSIDE the transaction together with the audit
		// row, so the trail and the set cannot disagree about whether anybody left.
		if removed == 0 {
			return ErrNotCollaborator
		}
		return tx.RecordListAudit(ctx, queries.RecordListAuditParams{
			ListID:  listID,
			ActorID: uuid.NullUUID{UUID: actorID, Valid: true},
			Action:  ActionCollaboratorRemove,
		})
	})
}

// Collaborators returns a list's write set.
//
// Readable by the OWNER and by the collaborators themselves, and by nobody else -- not even a reader
// of the published list. Who works on a list together is an association between members, and a
// published list publishes its contents, not its authors' relationships. Reported as ErrNotFound
// rather than as an empty list: an empty list would say "nobody collaborates on this", which is a
// statement about the set rather than a refusal to describe it.
func (s *Service) Collaborators(ctx context.Context, listID, viewerID uuid.UUID) ([]queries.ListCollaborator, error) {
	current, err := s.Get(ctx, listID, viewerID)
	if err != nil {
		return nil, err
	}
	if err := s.requireWriter(ctx, current, viewerID); err != nil {
		return nil, ErrNotFound
	}
	return s.queries.FindListCollaborators(ctx, listID)
}

// CanEdit reports whether actorID may change this list's contents.
//
// The third answer, alongside canView and requireWriter, and the only one that returns a BOOL rather
// than a refusal: the GraphQL field it feeds (`List.canEdit`) is a question about the asker's own
// powers, and its honest answer for most askers is "no". Nothing here is a permission a caller did not
// already have -- a stranger learning they cannot write a published list learns what the layout of
// every published list already tells them.
//
// A list the caller cannot see is still ErrNotFound, because that is a different question (whether the
// list exists for them) and answering `false` there would confirm an id that the package spends the
// rest of its code refusing to confirm.
func (s *Service) CanEdit(ctx context.Context, listID, actorID uuid.UUID) (bool, error) {
	current, err := s.Get(ctx, listID, actorID)
	if err != nil {
		return false, err
	}
	if err := s.requireWriter(ctx, current, actorID); err != nil {
		if errors.Is(err, ErrNotOwner) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
