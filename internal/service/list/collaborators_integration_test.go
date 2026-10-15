//go:build integration

package list_test

import (
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/list"
)

// The write set against a real database (plan phase 8 slice 8e).
//
// The pure tests in list_test.go prove the POLICY -- canView's arms, including the zero viewer with
// the collaborator fact set. What can only be proved here is the WIRING: that Get actually asks the
// question, that a collaborator's write lands in the database with the collaborator as its actor, and
// that the seven owner-only acts still refuse. Every one of those is a claim about code that talks to
// a database, and a pure test of requireWriter would prove the function and nothing about whether
// anything calls it.
//
// TestMain and the helpers (q, runInTxn, newService, createUser, storedItemCount) live in
// source_integration_test.go: one package, one TestMain, one database.

// collabFixture is the shape most of these tests need: an owner, a member, a stranger and a private
// list owned by the first.
type collabFixture struct {
	owner    uuid.UUID
	member   uuid.UUID
	stranger uuid.UUID
	svc      *list.Service
	listID   uuid.UUID
	name     string
}

func newCollabFixture(t *testing.T) collabFixture {
	t.Helper()
	owner := createUser(t)
	member := createUser(t)
	stranger := createUser(t)

	svc := newService(nil)
	name := "shared-" + uuid.Must(uuid.NewV4()).String()[:8]
	created, err := svc.Create(t.Context(), owner, name, "")
	require.NoError(t, err)

	return collabFixture{owner: owner, member: member, stranger: stranger, svc: svc, listID: created.ID, name: name}
}

// item adds an entry as the owner, so a later test's write is the only variable.
func (f collabFixture) item(t *testing.T) uuid.UUID {
	t.Helper()
	entity := uuid.Must(uuid.NewV4())
	_, err := f.svc.AddItem(t.Context(), f.listID, entity, f.owner, list.EntityScene, 0)
	require.NoError(t, err, "the owner's own add must work, or every test below measures the wrong failure")
	return entity
}

// THE SLICE'S HEADLINE, in two halves that must both hold: a collaborator can OPEN a draft they do not
// own, and can CHANGE it. The first half is canView's new arm; the second is requireWriter's. A test
// that only added an item would pass if Get were still owner-only (the write would fail with
// not-found) -- so the read is asserted first and on its own.
func TestACollaboratorMayReadAndWriteADraftTheyDoNotOwn(t *testing.T) {
	f := newCollabFixture(t)

	// Before: the member is a stranger to this list.
	_, err := f.svc.Get(t.Context(), f.listID, f.member)
	assert.ErrorIs(t, err, list.ErrNotFound, "a draft is invisible to a non-member")

	require.NoError(t, f.svc.AddCollaborator(t.Context(), f.listID, f.member, f.owner))

	row, err := f.svc.Get(t.Context(), f.listID, f.member)
	require.NoError(t, err, "a collaborator may READ the draft they may write")
	assert.Equal(t, f.owner, row.OwnerID, "reading it does not make them its owner")

	// The write half. All three entry writes, because they are three separate service methods and a
	// slice that wired only one of them would leave the others refusing for a reason no test named.
	entity := uuid.Must(uuid.NewV4())
	added, err := f.svc.AddItem(t.Context(), f.listID, entity, f.member, list.EntityScene, 0)
	require.NoError(t, err, "a collaborator may ADD an entry")

	require.NoError(t, f.svc.ReorderItem(t.Context(), added.ID, f.member, 4), "a collaborator may REORDER an entry")
	require.NoError(t, f.svc.RemoveItem(t.Context(), added.ID, f.member), "a collaborator may REMOVE an entry")

	assert.Equal(t, 0, storedItemCount(t, f.listID), "the removal took effect, not just returned nil")
}

// The other half of the design, enumerated: the seven acts that change what the list IS or who may
// write it stay with the owner. Each is asserted separately so a failure names the act that leaked
// rather than "a collaborator could do something they should not".
func TestACollaboratorMayNotDoTheOwnerOnlyActs(t *testing.T) {
	f := newCollabFixture(t)
	require.NoError(t, f.svc.AddCollaborator(t.Context(), f.listID, f.member, f.owner))
	f.item(t)

	_, err := f.svc.Update(t.Context(), f.listID, f.member, "renamed by a collaborator", "")
	assert.ErrorIs(t, err, list.ErrNotOwner, "RENAME is the owner's")

	_, err = f.svc.Publish(t.Context(), f.listID, f.member)
	assert.ErrorIs(t, err, list.ErrNotOwner, "PUBLISH is the owner's -- and it is the audited trust act")

	err = f.svc.SetSourceSearch(t.Context(), f.listID, f.member, uuid.Must(uuid.NewV4()))
	assert.ErrorIs(t, err, list.ErrNotOwner, "choosing what the list FOLLOWS is the owner's")

	err = f.svc.ClearSourceSearch(t.Context(), f.listID, f.member)
	assert.ErrorIs(t, err, list.ErrNotOwner, "stop following is the owner's")

	err = f.svc.AddCollaborator(t.Context(), f.listID, f.stranger, f.member)
	assert.ErrorIs(t, err, list.ErrNotOwner, "growing the WRITE SET is the owner's, and that is half the feature")

	err = f.svc.RemoveCollaborator(t.Context(), f.listID, f.owner, f.member)
	assert.ErrorIs(t, err, list.ErrNotOwner, "a collaborator may not remove another member")

	err = f.svc.Delete(t.Context(), f.listID, f.member)
	assert.ErrorIs(t, err, list.ErrNotOwner, "DELETE is the owner's")

	// The list is still there, unchanged, and still named what it was -- the point of refusing rather
	// than partially applying.
	row, err := f.svc.Get(t.Context(), f.listID, f.owner)
	require.NoError(t, err)
	assert.Equal(t, f.name, row.Name, "none of the seven attempts renamed it")
	assert.False(t, row.PublishedAt.Valid, "none of the seven attempts published it as a side effect")
	assert.Equal(t, 1, storedItemCount(t, f.listID), "and none of them changed the entries")
}

// A third member -- not the owner, not a collaborator -- is refused by the SAME error a missing list
// gets. The point is not that they are refused but that the two answers are indistinguishable: a
// distinct error would make a uuid range probeable.
func TestAStrangerCannotReadOrWriteADraftThatHasCollaborators(t *testing.T) {
	f := newCollabFixture(t)
	require.NoError(t, f.svc.AddCollaborator(t.Context(), f.listID, f.member, f.owner))
	f.item(t)

	_, err := f.svc.Get(t.Context(), f.listID, f.stranger)
	assert.ErrorIs(t, err, list.ErrNotFound)

	_, err = f.svc.AddItem(t.Context(), f.listID, uuid.Must(uuid.NewV4()), f.stranger, list.EntityScene, 0)
	assert.ErrorIs(t, err, list.ErrNotFound, "the refusal is not-found, not not-owner")

	err = f.svc.RemoveCollaborator(t.Context(), f.listID, f.member, f.stranger)
	assert.ErrorIs(t, err, list.ErrNotFound, "a stranger cannot even touch the write set")

	assert.Equal(t, 1, storedItemCount(t, f.listID), "no write reached the table")
}

// REVOCATION TAKES BOTH HALVES AWAY, and leaves the entries. The second assertion is the one worth
// having: entries carry no author column, so removing a member cannot remove what they contributed --
// and the audit trail is the only record of who added what.
func TestRemovingACollaboratorRevokesBothHalvesAndKeepsTheirEntries(t *testing.T) {
	f := newCollabFixture(t)
	require.NoError(t, f.svc.AddCollaborator(t.Context(), f.listID, f.member, f.owner))

	entity := uuid.Must(uuid.NewV4())
	_, err := f.svc.AddItem(t.Context(), f.listID, entity, f.member, list.EntityScene, 0)
	require.NoError(t, err)

	require.NoError(t, f.svc.RemoveCollaborator(t.Context(), f.listID, f.member, f.owner))

	_, err = f.svc.Get(t.Context(), f.listID, f.member)
	assert.ErrorIs(t, err, list.ErrNotFound, "the read half is gone")

	_, err = f.svc.AddItem(t.Context(), f.listID, uuid.Must(uuid.NewV4()), f.member, list.EntityScene, 0)
	assert.ErrorIs(t, err, list.ErrNotFound, "and the write half with it")

	assert.Equal(t, 1, storedItemCount(t, f.listID),
		"the entry the removed member added is still in the list: entries carry no author")

	audit, err := f.svc.Audit(t.Context(), f.listID, f.owner)
	require.NoError(t, err)
	assert.Contains(t, auditActions(audit), list.ActionCollaboratorRemove,
		"the removal itself is recorded, which is what makes it possible to say who could write and when")
}

// A member may leave. Requiring the owner's cooperation to stop working on somebody else's list would
// mean the way out is a request, which is exactly the wrong shape for a feature whose premise is that
// the members chose to be there.
func TestACollaboratorMayRemoveThemselves(t *testing.T) {
	f := newCollabFixture(t)
	require.NoError(t, f.svc.AddCollaborator(t.Context(), f.listID, f.member, f.owner))

	require.NoError(t, f.svc.RemoveCollaborator(t.Context(), f.listID, f.member, f.member),
		"a member may leave without asking")

	_, err := f.svc.Get(t.Context(), f.listID, f.member)
	assert.ErrorIs(t, err, list.ErrNotFound)
}

// The owner is not a row in the set, and adding them is refused by name rather than stored: a second
// row answering "may this person write" could then be removed, leaving a list whose owner is in the
// set on one read and not the next.
func TestTheOwnerIsNeverARowInTheWriteSet(t *testing.T) {
	f := newCollabFixture(t)

	err := f.svc.AddCollaborator(t.Context(), f.listID, f.owner, f.owner)
	assert.ErrorIs(t, err, list.ErrOwnerIsNotACollaborator)

	assert.Equal(t, int64(0), collaboratorCount(t, f.listID),
		"and nothing was written: the refusal is before the insert")

	// The owner's own powers do not depend on the set, which is the property the refusal protects.
	_, err = f.svc.Update(t.Context(), f.listID, f.owner, "renamed by its owner", "")
	require.NoError(t, err)
}

func TestAddingSomebodyTwiceIsRefusedByItsOwnError(t *testing.T) {
	f := newCollabFixture(t)
	require.NoError(t, f.svc.AddCollaborator(t.Context(), f.listID, f.member, f.owner))

	err := f.svc.AddCollaborator(t.Context(), f.listID, f.member, f.owner)
	assert.ErrorIs(t, err, list.ErrAlreadyCollaborator)
	assert.Equal(t, int64(1), collaboratorCount(t, f.listID), "the second attempt wrote nothing extra")
}

func TestAddingSomebodyWhoDoesNotExistIsRefusedAsAnUnknownUser(t *testing.T) {
	f := newCollabFixture(t)

	err := f.svc.AddCollaborator(t.Context(), f.listID, uuid.Must(uuid.NewV4()), f.owner)
	assert.ErrorIs(t, err, list.ErrUnknownUser,
		"NOT ErrNotFound: the list exists, and reporting it as missing would send the caller to the wrong half of their request")
}

func TestRemovingSomebodyWhoIsNotACollaboratorIsRefusedRatherThanSucceedingQuietly(t *testing.T) {
	f := newCollabFixture(t)

	err := f.svc.RemoveCollaborator(t.Context(), f.listID, f.member, f.owner)
	assert.ErrorIs(t, err, list.ErrNotCollaborator, "\"removed\" twice describes one change")
}

// The bound is inclusive at 25: the twenty-fifth member is added and the twenty-sixth is refused, and
// the message names the number so an operator reading a log knows which limit they met.
func TestTheWriteSetIsBoundedAndTheBoundIsInclusive(t *testing.T) {
	f := newCollabFixture(t)

	members := make([]uuid.UUID, 0, list.MaxCollaborators)
	for i := 0; i < list.MaxCollaborators; i++ {
		m := createUser(t)
		require.NoError(t, f.svc.AddCollaborator(t.Context(), f.listID, m, f.owner),
			"member %d of %d is within the bound", i+1, list.MaxCollaborators)
		members = append(members, m)
	}

	// The LAST one is still a collaborator -- an exclusive bound would have refused it, and that
	// off-by-one is the only thing this test can be about.
	_, err := f.svc.AddItem(t.Context(), f.listID, uuid.Must(uuid.NewV4()), members[len(members)-1], list.EntityScene, 0)
	require.NoError(t, err,
		"the last member accepted is a real collaborator, not a row that failed to mean anything")

	err = f.svc.AddCollaborator(t.Context(), f.listID, createUser(t), f.owner)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "25", "the refusal names the bound")
	assert.Equal(t, int64(list.MaxCollaborators), collaboratorCount(t, f.listID))
}

// A derived list's contents are computed, so there is nothing for a collaborator to write. Refused by
// name, like its entry writes: a permission that does nothing is worse than a refusal, because the
// owner would see the member in a set that changes no outcome.
func TestADerivedListRefusesCollaboration(t *testing.T) {
	owner := createUser(t)
	member := createUser(t)
	searchID := createSearch(t, owner, "a search nobody runs here")
	svc := newService(nil)
	listID := derivedList(t, svc, owner, searchID)

	err := svc.AddCollaborator(t.Context(), listID, member, owner)
	assert.ErrorIs(t, err, list.ErrDerivedList)
	assert.Equal(t, int64(0), collaboratorCount(t, listID))
}

// THE AUDIT CLAIM: the entry writes name the ACTOR, which is the only record of who put something in a
// list, because an entry carries no author of its own. Asserted with TWO actors so the test cannot
// pass by naming whichever single id is in the row: the member's add and the owner's add must be
// distinguishable in the trail.
func TestTheEntryWritesRecordTheActorAndNotTheOwner(t *testing.T) {
	f := newCollabFixture(t)
	require.NoError(t, f.svc.AddCollaborator(t.Context(), f.listID, f.member, f.owner))

	byMember, err := f.svc.AddItem(t.Context(), f.listID, uuid.Must(uuid.NewV4()), f.member, list.EntityScene, 0)
	require.NoError(t, err)
	_, err = f.svc.AddItem(t.Context(), f.listID, uuid.Must(uuid.NewV4()), f.owner, list.EntityScene, 0)
	require.NoError(t, err)
	require.NoError(t, f.svc.ReorderItem(t.Context(), byMember.ID, f.member, 9))
	require.NoError(t, f.svc.RemoveItem(t.Context(), byMember.ID, f.member))

	audit, err := f.svc.Audit(t.Context(), f.listID, f.owner)
	require.NoError(t, err)

	// One row per recorded act, in order. The COUNT is asserted first: the three entry verbs plus the
	// membership change are four acts by two different people, and a set-membership check alone would
	// pass with a single row.
	require.Len(t, audit, 5, "collaborator_add, add, add, reorder and remove are five records")

	// Newest first (the query orders by created_at DESC), so the expected order is the reverse.
	want := []struct {
		action string
		actor  uuid.UUID
	}{
		{list.ActionRemoveItem, f.member},
		{list.ActionReorderItem, f.member},
		{list.ActionAddItem, f.owner},
		{list.ActionAddItem, f.member},
		{list.ActionCollaboratorAdd, f.owner},
	}
	for i, w := range want {
		assert.Equal(t, w.action, audit[i].Action, "record %d", i)
		require.True(t, audit[i].ActorID.Valid, "record %d must carry an actor", i)
		assert.Equal(t, w.actor, audit[i].ActorID.UUID,
			"record %d is attributed to the person who did it, not to the owner", i)
	}
}

// The write set is readable by the people it is about, and by nobody else -- including a reader of the
// published list, who may read the CONTENTS. Asserted together with canEdit, because the two are the
// same question asked two ways and a client uses both to decide whether to offer the controls.
func TestTheWriteSetIsHiddenFromReadersOfAPublishedList(t *testing.T) {
	f := newCollabFixture(t)
	require.NoError(t, f.svc.AddCollaborator(t.Context(), f.listID, f.member, f.owner))
	_, err := f.svc.Publish(t.Context(), f.listID, f.owner)
	require.NoError(t, err)

	// The stranger can now READ the list -- it is published -- and not the set.
	_, err = f.svc.Items(t.Context(), f.listID, f.stranger)
	require.NoError(t, err, "publishing made the contents readable")
	_, err = f.svc.Collaborators(t.Context(), f.listID, f.stranger)
	assert.ErrorIs(t, err, list.ErrNotFound,
		"who works on a list together is not part of what publishing it publishes")

	for _, viewer := range []uuid.UUID{f.owner, f.member} {
		rows, err := f.svc.Collaborators(t.Context(), f.listID, viewer)
		require.NoError(t, err, "the owner and the members may read the set")
		require.Len(t, rows, 1)
		assert.Equal(t, f.member, rows[0].UserID)
	}

	canEdit, err := f.svc.CanEdit(t.Context(), f.listID, f.member)
	require.NoError(t, err)
	assert.True(t, canEdit, "a member is told they may write")

	canEdit, err = f.svc.CanEdit(t.Context(), f.listID, f.stranger)
	require.NoError(t, err)
	assert.False(t, canEdit, "a reader is told they may not -- false, not an error")

	canEdit, err = f.svc.CanEdit(t.Context(), f.listID, f.owner)
	require.NoError(t, err)
	assert.True(t, canEdit)
}

// collaboratorCount counts the ROWS, which is the only way to tell "refused before writing" from
// "wrote and hid it" -- a service-level assertion cannot distinguish the two.
func collaboratorCount(t *testing.T, listID uuid.UUID) int64 {
	t.Helper()
	var n int64
	require.NoError(t, testutil.DB().QueryRow(t.Context(),
		`SELECT count(*) FROM list_collaborators WHERE list_id = $1`, listID).Scan(&n))
	return n
}

func auditActions(audit []queries.ListAudit) []string {
	out := make([]string, 0, len(audit))
	for i := range audit {
		out = append(out, audit[i].Action)
	}
	return out
}
