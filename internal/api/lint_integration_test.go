//go:build integration

package api_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbtest "github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/lint"
)

// The data-lint detectors against real rows (SPEC §7.24.4).
//
// The unit tests pin the decisions that need no database -- which registry row is refused, what a
// candidate carries, whether the entity filter reaches the query. What they CANNOT reach is whether a
// detector's SQL selects the entities a curator would agree are wrong, and that is the entire value of
// a lint: a detector that fires on the wrong rows sends curators to fix records that are fine, and a
// detector that misses the right ones reports an archive in better shape than it is.
//
// So every case below builds two rows that differ in exactly one property -- one that must fire and
// one that must not -- and asserts BOTH. The negative half is the half that catches a join that is
// one condition too loose, and it is the half usually left out.
//
// Every fixture carries a unique suffix, because these tests share one database with every other
// integration test in this package and a detector is a question about the WHOLE table: a row another
// test left behind is a row this detector will find.

func lintService() *lint.Service { return lint.NewService(cq()) }

func nowDatePlus(days int) string { return time.Now().AddDate(0, 0, days).Format("2006-01-02") }

func lintSuffix() string { return uuid.Must(uuid.NewV7()).String() }

// lintSite returns a site row's id, creating one. A URL row requires a site_id, and the detector joins
// nothing to sites -- but the fixture cannot skip a NOT NULL column.
func lintSite(t *testing.T) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	_, err := cq().CreateSite(t.Context(), queries.CreateSiteParams{
		ID: id, Name: "lint-fixture-" + id.String(), ValidTypes: []string{},
	})
	require.NoError(t, err, "creating the site a url row requires")
	return id
}

func lintPerformer(t *testing.T, name string, birthdate *string) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	_, err := cq().CreatePerformer(t.Context(), queries.CreatePerformerParams{
		ID: id, Name: name, Birthdate: birthdate,
	})
	require.NoError(t, err, "creating performer %s", name)
	return id
}

func lintPerformerURL(t *testing.T, performerID, siteID uuid.UUID, url string) {
	t.Helper()
	_, err := cq().CreatePerformerURLs(t.Context(), []queries.CreatePerformerURLsParams{
		{PerformerID: performerID, Url: url, SiteID: siteID},
	})
	require.NoError(t, err, "recording a url on a performer")
}

func lintAlias(t *testing.T, performerID uuid.UUID, alias string) {
	t.Helper()
	_, err := cq().CreatePerformerAliases(t.Context(), []queries.CreatePerformerAliasesParams{
		{PerformerID: performerID, Alias: alias},
	})
	require.NoError(t, err, "recording an alias")
}

func lintScene(t *testing.T, title string, date *string) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	_, err := cq().CreateScene(t.Context(), queries.CreateSceneParams{ID: id, Title: &title, Date: date})
	require.NoError(t, err, "creating scene %s", title)
	return id
}

func lintSceneURL(t *testing.T, sceneID, siteID uuid.UUID, url string) {
	t.Helper()
	_, err := cq().CreateSceneURLs(t.Context(), []queries.CreateSceneURLsParams{
		{SceneID: sceneID, Url: url, SiteID: siteID},
	})
	require.NoError(t, err, "recording a url on a scene")
}

func lintScenePerformer(t *testing.T, sceneID, performerID uuid.UUID) {
	t.Helper()
	_, err := cq().CreateScenePerformers(t.Context(), []queries.CreateScenePerformersParams{
		{SceneID: sceneID, PerformerID: performerID},
	})
	require.NoError(t, err, "linking a performer to a scene")
}

func lintImage(t *testing.T, performerID uuid.UUID, width, height int) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	url := "https://lint.test/" + id.String()
	// A UNIQUE checksum per fixture, and the reason is worth keeping: CreateImage is
	// `ON CONFLICT (checksum) DO UPDATE ... RETURNING id`, so images that share the default empty
	// checksum silently collapse into one row and every later fixture gets back the FIRST id -- the
	// insert reports success, the id points at an image somebody else linked, and the failure arrives
	// as a foreign-key violation on the NEXT call. (It cost a debugging round here.)
	sum := "lintfixture" + id.String()
	_, err := cq().CreateImage(t.Context(), queries.CreateImageParams{
		ID: id, Url: &url, Width: width, Height: height, Checksum: sum,
	})
	require.NoError(t, err, "creating an image")
	_, err = cq().CreatePerformerImages(t.Context(), []queries.CreatePerformerImagesParams{
		{PerformerID: performerID, ImageID: id},
	})
	require.NoError(t, err, "linking an image to a performer")
	return id
}

// candidatesOf flattens a report set into "slug -> entity ids", which is the shape every assertion here
// wants: which entities did this detector accuse.
func candidatesOf(reports []lint.Report) map[string]map[uuid.UUID]string {
	out := map[string]map[uuid.UUID]string{}
	for _, r := range reports {
		out[r.Slug] = map[uuid.UUID]string{}
		for _, c := range r.Candidates {
			out[r.Slug][c.EntityID] = c.Detail
		}
	}
	return out
}

// THE IMPORT VALIDATION PASS, which §7.24.4 calls the more valuable half. Two performers share a URL
// and one does not; the pass is asked about all three and must name exactly the sharing pair. The
// detail is asserted too, because "which URL" is what a curator acts on.
func TestDuplicatePerformerURLsFiresOnTheSharingPairOnly(t *testing.T) {
	site := lintSite(t)
	shared := "https://lint.test/perf/" + lintSuffix()

	first := lintPerformer(t, "Lint Dup One "+lintSuffix(), nil)
	second := lintPerformer(t, "Lint Dup Two "+lintSuffix(), nil)
	lonely := lintPerformer(t, "Lint Dup Lonely "+lintSuffix(), nil)
	lintPerformerURL(t, first, site, shared)
	lintPerformerURL(t, second, site, shared)
	lintPerformerURL(t, lonely, site, "https://lint.test/perf/unique-"+lintSuffix())

	reports, err := lintService().ValidateEntities(context.Background(), []lint.EntityRef{
		{EntityType: "performer", EntityID: first},
		{EntityType: "performer", EntityID: second},
		{EntityType: "performer", EntityID: lonely},
	})
	require.NoError(t, err)
	got := candidatesOf(reports)["duplicate_performer_url"]

	assert.Contains(t, got, first)
	assert.Contains(t, got, second)
	assert.NotContains(t, got, lonely, "a performer with a unique URL was accused")
	assert.Contains(t, got[first], shared, "the detail must name the colliding URL")
	assert.Contains(t, got[first], "2 performers", "the detail must say how many share it")
}

// A SOFT-DELETED performer is not a curator's task, and this is the negative half that matters: a merge
// in this schema leaves a deleted row behind, so without the filter every merged-away performer would
// reappear as a duplicate-URL finding.
func TestDuplicatePerformerURLsIgnoresDeletedPerformers(t *testing.T) {
	site := lintSite(t)
	shared := "https://lint.test/perf/" + lintSuffix()

	live := lintPerformer(t, "Lint Live "+lintSuffix(), nil)
	dead := lintPerformer(t, "Lint Deleted "+lintSuffix(), nil)
	lintPerformerURL(t, live, site, shared)
	lintPerformerURL(t, dead, site, shared)
	// SoftDeletePerformer, and NOT DeletePerformer: the latter is a hard `DELETE FROM performers`,
	// and using it here is what made the first version of this test vacuous. The row vanished, so the
	// "deleted rows are not lint" rule was never exercised -- and the mutation that removes the filter
	// from the detector SURVIVED, because there was nothing left for it to report. (An absence
	// assertion whose subject is absent proves nothing; hence the count below.)
	_, err := cq().SoftDeletePerformer(t.Context(), dead)
	require.NoError(t, err)

	// THE SUBJECT IS COUNTED BEFORE THE ASSERTION. `deleted = true` on a row that still exists is the
	// state this rule is about, and asserting the state rather than assuming it is the whole point.
	var stillThere bool
	require.NoError(t, dbtest.DB().QueryRow(t.Context(),
		"SELECT deleted FROM performers WHERE id = $1", dead).Scan(&stillThere))
	require.True(t, stillThere, "the fixture did not leave a soft-deleted row behind, so this test proves nothing")

	reports, err := lintService().ValidateEntities(context.Background(), []lint.EntityRef{
		{EntityType: "performer", EntityID: live},
		{EntityType: "performer", EntityID: dead},
	})
	require.NoError(t, err)
	got := candidatesOf(reports)["duplicate_performer_url"]
	assert.NotContains(t, got, live, "the only other holder of the URL is deleted, so this is not a duplicate")
	assert.NotContains(t, got, dead, "a soft-deleted performer is not a curator's task")
}

// THE NEGATIVE THE SPEC CALLS OUT: a name colliding with a name is NOT lint (disambiguation is how this
// schema separates two performers who share one), while an ALIAS claiming another performer's name is.
// One fixture, two rows, exactly one of which may fire.
func TestAliasCollisionFiresOnTheAliasAndNotOnTheSharedName(t *testing.T) {
	shared := "Lint Shared Name " + lintSuffix()

	named := lintPerformer(t, shared, nil)
	alsoNamed := lintPerformer(t, shared, nil) // same name, different person: legitimate
	aliasing := lintPerformer(t, "Lint Alias Holder "+lintSuffix(), nil)
	lintAlias(t, aliasing, shared) // its alias claims the first performer's identity

	reports, err := lintService().ValidateEntities(context.Background(), []lint.EntityRef{
		{EntityType: "performer", EntityID: named},
		{EntityType: "performer", EntityID: alsoNamed},
		{EntityType: "performer", EntityID: aliasing},
	})
	require.NoError(t, err)
	got := candidatesOf(reports)["performer_alias_collision"]

	assert.Contains(t, got, aliasing, "an alias claiming another performer's name is the finding")
	assert.NotContains(t, got, named, "a name is not an alias, so it cannot collide as one")
	assert.NotContains(t, got, alsoNamed, "two performers sharing a name is a legitimate state")
	assert.Contains(t, got[aliasing], "is also the name of", "the detail must say what it collides with")
}

// AN ALIAS COLLIDING WITH AN ALIAS is the second half of the same rule, and it is here because a
// mutation found the gap: deleting the alias arm of the "other performer's identities" union left
// every test above green, since the fixtures only ever collided an alias with a NAME. Two performers
// both claiming "Barbie" as an alias is the same defect as one claiming another's name -- two records
// asserting one identity -- and without this fixture nothing proved the detector sees it.
func TestAliasCollisionFiresWhenTwoPerformersShareAnAlias(t *testing.T) {
	shared := "Lint Shared Alias " + lintSuffix()

	first := lintPerformer(t, "Lint Aliasist One "+lintSuffix(), nil)
	second := lintPerformer(t, "Lint Aliasist Two "+lintSuffix(), nil)
	lintAlias(t, first, shared)
	lintAlias(t, second, shared)

	reports, err := lintService().ValidateEntities(context.Background(), []lint.EntityRef{
		{EntityType: "performer", EntityID: first},
		{EntityType: "performer", EntityID: second},
	})
	require.NoError(t, err)
	got := candidatesOf(reports)["performer_alias_collision"]

	assert.Contains(t, got, first)
	assert.Contains(t, got, second)
	assert.Contains(t, got[first], "is also the alias of", "the detail must say it collided with an alias")
}

// The future arm has a slack window, and both sides of it are asserted: a date inside the window is a
// scheduled release and must NOT be reported; a date outside it is a typo and must be.
func TestImpossibleSceneDatesRespectTheSlackWindow(t *testing.T) {
	soon := nowDatePlus(10)
	far := nowDatePlus(400)

	scheduled := lintScene(t, "Lint Scheduled "+lintSuffix(), &soon)
	typo := lintScene(t, "Lint Typo "+lintSuffix(), &far)

	reports, err := lintService().ValidateEntities(context.Background(), []lint.EntityRef{
		{EntityType: "scene", EntityID: scheduled},
		{EntityType: "scene", EntityID: typo},
	})
	require.NoError(t, err)
	got := candidatesOf(reports)["impossible_scene_date"]

	assert.NotContains(t, got, scheduled, "a scene dated next week is a scheduled release")
	assert.Contains(t, got, typo)
	assert.Contains(t, got[typo], "in the future")
}

// The birth-date arm: a scene dated before one of its performers was born. The scene has TWO
// performers, only one of whom makes it impossible -- so the finding is about the scene, and the detail
// names the performer that makes it so.
func TestImpossibleSceneDatesFireOnASceneDatedBeforeAPerformerWasBorn(t *testing.T) {
	birth := "2000-01-01"
	tooEarly := "1990-06-01"
	born := lintPerformer(t, "Lint Born "+lintSuffix(), &birth)
	older := lintPerformer(t, "Lint Older "+lintSuffix(), nil)

	impossible := lintScene(t, "Lint Before Birth "+lintSuffix(), &tooEarly)
	lintScenePerformer(t, impossible, born)
	lintScenePerformer(t, impossible, older)

	fine := lintScene(t, "Lint After Birth "+lintSuffix(), &tooEarly)
	lintScenePerformer(t, fine, older) // nobody in this scene was born after it

	reports, err := lintService().ValidateEntities(context.Background(), []lint.EntityRef{
		{EntityType: "scene", EntityID: impossible},
		{EntityType: "scene", EntityID: fine},
	})
	require.NoError(t, err)
	got := candidatesOf(reports)["impossible_scene_date"]

	assert.Contains(t, got, impossible)
	assert.NotContains(t, got, fine)
	assert.Contains(t, got[impossible], "before ", "the detail must say the date precedes something")
}

// A picture too small to use is a finding; one big enough is not.
//
// AND AN IMAGE WITH NO MEASURED SIZE CANNOT EXIST, which this test establishes rather than assumes.
// The detector's comment originally claimed such a row was possible ("an image row can exist before
// its bytes were decoded"); the fixture that tried to build one was refused by a NOT NULL constraint,
// so the claim is false and the guard that "handled" it was code for a state the schema forbids. The
// assertion below is the constraint, kept here because a detector written against an imaginary state
// is how a rule stops matching the data.
func TestLowResolutionImagesFireOnlyOnSmallPictures(t *testing.T) {
	small := lintPerformer(t, "Lint Small Pic "+lintSuffix(), nil)
	big := lintPerformer(t, "Lint Big Pic "+lintSuffix(), nil)

	lintImage(t, small, 120, 90)
	imageID := lintImage(t, big, 1200, 900)

	_, err := dbtest.DB().Exec(t.Context(), "UPDATE images SET width = NULL, height = NULL WHERE id = $1", imageID)
	require.Error(t, err, "images.width is NOT NULL: an unmeasured size is not a state a row can be in")
	assert.Contains(t, err.Error(), "not-null", "the refusal must be the constraint, not something else")

	reports, err := lintService().ValidateEntities(context.Background(), []lint.EntityRef{
		{EntityType: "performer", EntityID: small},
		{EntityType: "performer", EntityID: big},
	})
	require.NoError(t, err)
	got := candidatesOf(reports)["low_resolution_performer_image"]

	assert.Contains(t, got, small)
	assert.NotContains(t, got, big, "a 1200x900 picture is not too small")
	assert.Contains(t, got[small], "120x90")
}

// THE PROPERTY THE PACKAGE PROMISES: a finding disappears when the RECORD is fixed, not when the board
// is refreshed or claimed. The fixture shares a URL, observes the finding, changes the URL, and observes
// it gone -- which is the whole difference between this and a stored quest.
func TestAFinishedFindingLeavesTheBoard(t *testing.T) {
	site := lintSite(t)
	shared := "https://lint.test/scene/" + lintSuffix()
	date := "2015-01-01"

	one := lintScene(t, "Lint Fixed One "+lintSuffix(), &date)
	two := lintScene(t, "Lint Fixed Two "+lintSuffix(), &date)
	lintSceneURL(t, one, site, shared)
	lintSceneURL(t, two, site, shared)

	refs := []lint.EntityRef{
		{EntityType: "scene", EntityID: one},
		{EntityType: "scene", EntityID: two},
	}

	reports, err := lintService().ValidateEntities(context.Background(), refs)
	require.NoError(t, err)
	require.Contains(t, candidatesOf(reports)["duplicate_scene_url"], one, "the fixture must start broken")

	// Somebody fixes one scene's URL -- the archive changes, nothing in the lint does.
	_, err = dbtest.DB().Exec(t.Context(),
		"UPDATE scene_urls SET url = $1 WHERE scene_id = $2 AND url = $3",
		"https://lint.test/scene/fixed-"+lintSuffix(), one, shared)
	require.NoError(t, err)

	reports, err = lintService().ValidateEntities(context.Background(), refs)
	require.NoError(t, err)
	got := candidatesOf(reports)["duplicate_scene_url"]
	assert.NotContains(t, got, one, "the fixed scene is still on the board")
	assert.NotContains(t, got, two, "its partner has no duplicate any more")
}

// THE REGISTRY CONTRACT, against the real table: seeding is idempotent, and an ENABLED row naming no
// implementation stops the instance -- by name, with the slugs the build does have.
func TestEnsureBuiltinsIsIdempotentAndValidateRefusesAnUnknownEnabledDetector(t *testing.T) {
	svc := lintService()

	inserted, err := svc.EnsureBuiltins(context.Background())
	require.NoError(t, err)
	t.Logf("seeded %d of %d detectors", inserted, len(lint.Builtins()))

	// A second start inserts nothing, and does not fail -- the property that lets an operator disable a
	// detector without it coming back on the next deploy.
	again, err := svc.EnsureBuiltins(context.Background())
	require.NoError(t, err)
	assert.Zero(t, again, "a second seed re-inserted rows an operator may have edited")

	require.NoError(t, svc.Validate(context.Background()),
		"every enabled seeded row has an implementation by construction")

	// A row a future release would own. Enabled and unimplemented is a broken instance.
	bogus := "lint_fixture_unknown_" + fmt.Sprintf("%d", time.Now().UnixNano())
	_, err = cq().InsertBuiltinLintQuestDefinition(context.Background(), queries.InsertBuiltinLintQuestDefinitionParams{
		Slug: bogus, Description: "a detector this build does not implement", BountyPoints: 1,
	})
	require.NoError(t, err)
	defer func() {
		_, _ = dbtest.DB().Exec(context.Background(), "DELETE FROM lint_quest_definitions WHERE slug = $1", bogus)
	}()

	err = svc.Validate(context.Background())
	require.Error(t, err)
	assert.ErrorIs(t, err, lint.ErrUnknownDetector)
	assert.Contains(t, err.Error(), bogus)

	// And once it is disabled, the instance starts: a retired detector's row is history.
	_, err = dbtest.DB().Exec(context.Background(), "UPDATE lint_quest_definitions SET enabled = FALSE WHERE slug = $1", bogus)
	require.NoError(t, err)
	require.NoError(t, svc.Validate(context.Background()))
}
