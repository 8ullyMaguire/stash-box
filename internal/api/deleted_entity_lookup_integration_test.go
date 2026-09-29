//go:build integration

package api_test

import (
	"testing"

	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Issue #1007: the Stash tagger linked images to a deleted studio, and
// findTag has the same problem.
//
// The queries that back the public find-by-id lookups were:
//
//	-- name: FindStudio :one
//	SELECT * FROM studios WHERE id = $1;
//
//	-- name: FindTag :one
//	SELECT * FROM tags WHERE id = $1;
//
//	-- name: FindPerformer :one
//	SELECT * FROM performers WHERE id = $1;
//
// No deleted filter, and no redirect hop. Their FindByName siblings all filter
// correctly (`AND deleted = false`), so the inconsistency was only visible in
// the id path. Anything resolving an entity by id -- and that includes the
// Stash tagger, which is looking up the studio it was configured with -- got
// whatever row happened to hold the id, including a soft-deleted one.
//
// The fix points each service FindByID at the redirect-aware query that already
// existed and was only used by the drafts path.

// The reported case: two studios share a name, one is deleted. Resolving by
// name must not return the deleted one, and resolving the deleted one by id
// must return nothing.
func TestFindStudioDoesNotReturnDeletedStudio(t *testing.T) {
	admin := asAdmin(t)

	name := admin.generateStudioName()

	live, err := admin.createTestStudio(&models.StudioCreateInput{Name: name})
	require.NoError(t, err)

	// A second studio with the same name, then destroy it. This is the state
	// the reporter hit: the name is ambiguous, and a deleted row still holds it.
	duplicate, err := admin.createTestStudio(&models.StudioCreateInput{Name: name})
	require.NoError(t, err)
	duplicateID := duplicate.UUID()

	// The destroy EDIT, not StudioDestroy. This distinction is the whole test:
	// studio.destroy calls DeleteStudio (`DELETE FROM studios WHERE id = $1`) --
	// a HARD delete, so the row is gone and findStudio returns nothing with or
	// without the fix. Only the edit processor calls SoftDeleteStudio, which
	// leaves `deleted = true` on the row. Using StudioDestroy here made this
	// test pass on the pre-fix code, which is exactly the kind of green that
	// proves nothing.
	destroyEdit, err := admin.createTestStudioEdit(models.OperationEnumDestroy,
		&models.StudioEditDetailsInput{},
		&models.EditInput{Operation: models.OperationEnumDestroy, ID: &duplicateID},
	)
	require.NoError(t, err)
	_, err = admin.resolver.Mutation().ApproveEdit(admin.ctx, models.ApproveEditInput{ID: destroyEdit.ID})
	require.NoError(t, err)

	// By id: the deleted studio must not resolve.
	byID, err := admin.resolver.Query().FindStudio(admin.ctx, &duplicateID, nil)
	require.NoError(t, err)
	assert.Nil(t, byID,
		"findStudio must not return a soft-deleted studio (#1007)")

	// By name: the live studio must win, not the deleted one.
	byName, err := admin.resolver.Query().FindStudio(admin.ctx, nil, &name)
	require.NoError(t, err)
	require.NotNil(t, byName, "the live studio must still resolve by name")
	assert.Equal(t, live.UUID(), byName.ID,
		"findStudio by name must not return a soft-deleted studio (#1007)")
}

// The redirect half, which is the same defect seen from the other side: a merged
// source id must resolve to the survivor, so a client holding a stale id keeps
// working instead of silently reading a deleted row.
func TestFindStudioResolvesMergedSourceToSurvivor(t *testing.T) {
	admin := asAdmin(t)

	source, err := admin.createTestStudio(&models.StudioCreateInput{
		Name: admin.generateStudioName(),
	})
	require.NoError(t, err)

	target, err := admin.createTestStudio(&models.StudioCreateInput{
		Name: admin.generateStudioName(),
	})
	require.NoError(t, err)
	targetID := target.UUID()
	sourceID := source.UUID()

	// Merge source into target, as the edit system does. MergeSourceIds lives on
	// EditInput, not on the details input.
	newName := admin.generateStudioName()
	edit, err := admin.createTestStudioEdit(models.OperationEnumMerge,
		&models.StudioEditDetailsInput{Name: &newName},
		&models.EditInput{
			Operation:      models.OperationEnumMerge,
			ID:             &targetID,
			MergeSourceIds: []uuid.UUID{sourceID},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, edit)

	_, err = admin.resolver.Mutation().ApproveEdit(admin.ctx, models.ApproveEditInput{ID: edit.ID})
	require.NoError(t, err)

	// The source id must now resolve to the target.
	resolved, err := admin.resolver.Query().FindStudio(admin.ctx, &sourceID, nil)
	require.NoError(t, err)
	require.NotNil(t, resolved,
		"a merged studio id must resolve to its survivor (#1007)")
	assert.Equal(t, targetID, resolved.ID,
		"a merged studio id must resolve to its survivor (#1007)")
}

// Tags, from the comment on the issue. Same query defect, same fix -- but the
// reported scenario does not transfer, and the difference is worth recording.
//
// Studios and performers are unique on (name, disambiguation) WHERE NOT deleted,
// so a deleted studio and a live studio can share a name and the name lookup is
// genuinely ambiguous. Tags are unique on (name) WHERE NOT deleted alone:
//
//	index_active_tags_on_name ON public.tags USING btree (name) WHERE (NOT deleted)
//
// so a second active tag with the same name cannot be created at all -- the
// insert is rejected by the database. There is no ambiguous-name case to build
// here. What remains is the id case, which is the same defect: findTag used
// `WHERE id = $1` with no deleted filter, so it returned soft-deleted rows.
func TestFindTagDoesNotReturnDeletedTag(t *testing.T) {
	admin := asAdmin(t)

	tag, err := admin.createTestTag(&models.TagCreateInput{Name: admin.generateTagName()})
	require.NoError(t, err)
	tagID := tag.UUID()

	// The destroy EDIT, not tag.destroy: tag.destroy calls DeleteTag
	// (`DELETE FROM tags WHERE id = $1`), a HARD delete, so the row disappears
	// and findTag returns nothing whether or not the fix is present. Only the
	// edit processor calls SoftDeleteTag, which leaves `deleted = true` behind.
	destroyEdit, err := admin.createTestTagEdit(models.OperationEnumDestroy,
		&models.TagEditDetailsInput{},
		&models.EditInput{Operation: models.OperationEnumDestroy, ID: &tagID},
	)
	require.NoError(t, err)
	_, err = admin.resolver.Mutation().ApproveEdit(admin.ctx, models.ApproveEditInput{ID: destroyEdit.ID})
	require.NoError(t, err)

	byID, err := admin.resolver.Query().FindTag(admin.ctx, &tagID, nil)
	require.NoError(t, err)
	assert.Nil(t, byID,
		"findTag must not return a soft-deleted tag (#1007)")
}

// Performers: same defect, third entity. Included so a future change that
// regresses one entity but not the others is caught.
func TestFindPerformerDoesNotReturnDeletedPerformer(t *testing.T) {
	admin := asAdmin(t)

	name := admin.generatePerformerName()

	live, err := admin.createTestPerformer(&models.PerformerCreateInput{Name: name})
	require.NoError(t, err)

	duplicate, err := admin.createTestPerformer(&models.PerformerCreateInput{Name: name})
	require.NoError(t, err)

	// The destroy EDIT, not performer.destroy: performer.destroy calls
	// DeletePerformer, a HARD delete, so the row is gone and findPerformer
	// returns nothing with or without the fix. Only the edit processor calls
	// SoftDeletePerformer, leaving `deleted = true` on the row -- which is
	// exactly the row FindPerformer used to hand back.
	duplicateID := duplicate.UUID()
	destroyEdit, err := admin.createTestPerformerEdit(models.OperationEnumDestroy,
		&models.PerformerEditDetailsInput{},
		&models.EditInput{Operation: models.OperationEnumDestroy, ID: &duplicateID},
		nil)
	require.NoError(t, err)
	_, err = admin.resolver.Mutation().ApproveEdit(admin.ctx, models.ApproveEditInput{ID: destroyEdit.ID})
	require.NoError(t, err)

	byID, err := admin.resolver.Query().FindPerformer(admin.ctx, duplicate.UUID())
	require.NoError(t, err)
	assert.Nil(t, byID,
		"findPerformer must not return a soft-deleted performer (#1007)")

	// findPerformer is id-only -- there is no name variant on the performer
	// resolver, unlike studio and tag -- so the id case above is the whole of
	// the reported surface for this entity. Assert the live performer is still
	// reachable, so "returns nothing" cannot be satisfied by breaking lookups.
	liveByID, err := admin.resolver.Query().FindPerformer(admin.ctx, live.UUID())
	require.NoError(t, err)
	require.NotNil(t, liveByID,
		"a live performer must still resolve (#1007 regression guard)")
}

// The guard that gives the three tests above their meaning: a live entity must
// still resolve through the new query path. Without this, "returns nothing"
// would be satisfiable by breaking every lookup.
func TestFindByIDStillReturnsLiveEntities(t *testing.T) {
	admin := asAdmin(t)

	studio, err := admin.createTestStudio(&models.StudioCreateInput{
		Name: admin.generateStudioName(),
	})
	require.NoError(t, err)
	studioID := studio.UUID()
	resolvedStudio, err := admin.resolver.Query().FindStudio(admin.ctx, &studioID, nil)
	require.NoError(t, err)
	require.NotNil(t, resolvedStudio, "a live studio must still resolve (#1007 regression guard)")
	assert.Equal(t, studio.UUID(), resolvedStudio.ID)

	performer, err := admin.createTestPerformer(&models.PerformerCreateInput{
		Name: admin.generatePerformerName(),
	})
	require.NoError(t, err)
	resolvedPerformer, err := admin.resolver.Query().FindPerformer(admin.ctx, performer.UUID())
	require.NoError(t, err)
	require.NotNil(t, resolvedPerformer, "a live performer must still resolve (#1007 regression guard)")

	tag, err := admin.createTestTag(&models.TagCreateInput{Name: admin.generateTagName()})
	require.NoError(t, err)
	tagID := tag.UUID()
	resolvedTag, err := admin.resolver.Query().FindTag(admin.ctx, &tagID, nil)
	require.NoError(t, err)
	require.NotNil(t, resolvedTag, "a live tag must still resolve (#1007 regression guard)")
}
