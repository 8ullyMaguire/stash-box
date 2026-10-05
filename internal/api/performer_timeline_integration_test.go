//go:build integration

package api_test

import (
	"testing"

	"github.com/99designs/gqlgen/client"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/models"
)

// Performer timeline over GraphQL (growth item 21).
//
// The interesting property is NOT that the buckets are right. It is that the timeline's
// total agrees with the performer's own scene count -- because the buckets cannot, on
// their own, account for an appearance whose scene has no date, and a dated-only timeline
// under-reports silently.
//
// The fixtures deliberately include an undated scene. Without it, every assertion below
// passes on a timeline that drops undated appearances entirely, which is the exact bug
// this design exists to prevent.

type timelineEntryRow struct {
	Year       int
	SceneCount int
}

type timelineRow struct {
	Entries      []timelineEntryRow
	UndatedCount int
	SceneCount   int
}

// `scene_count` on Performer is SNAKE_CASE while `sceneCount` on PerformerTimeline is
// camelCase. Both are valid GraphQL and both are correct here -- the schema uses
// snake_case for the pre-existing performer fields and camelCase for the new ones. Kept as
// found rather than "fixed": renaming either would break existing clients, and the
// inconsistency is a schema-wide convention issue, not this field's.
type timelineResponse struct {
	FindPerformer *struct {
		ID   string
		Name string
		// EXPLICIT TAG: the response key is `scene_count` and gqlgen's decoder matches on
		// the Go field name by default, so without this it reports "invalid keys".
		SceneCount int `json:"scene_count"`
		Timeline   timelineRow
	}
}

// makeTimelinePerformer creates a performer with appearances dated across several years
// plus one undated scene, and returns its id.
//
// Co-occurrence matters: buckets are keyed by year, so a performer needs appearances in
// DIFFERENT years to prove the grouping, and two in the SAME year to prove it groups at
// all.
func makeTimelinePerformer(t *testing.T, name string) uuid.UUID {
	t.Helper()
	r := asAdmin(t)

	var created struct {
		PerformerCreate struct {
			ID string
		}
	}
	r.client.MustPost(
		`mutation($input: PerformerCreateInput!) { performerCreate(input: $input) { id } }`,
		&created,
		client.Var("input", map[string]any{"name": name}))
	performerID, err := uuid.FromString(created.PerformerCreate.ID)
	require.NoError(t, err, "performerCreate must return a parseable id")
	require.NotEqual(t, uuid.Nil, performerID)

	// 2019 x2, 2021 x1, 2023 x3, and one undated.
	dates := []string{
		"2019-01-01", "2019-06-01",
		"2021-03-01",
		"2023-01-01", "2023-02-01", "2023-03-01",
		"",
	}
	for _, d := range dates {
		title := name + " scene " + d
		_, err := r.client.createScene(models.SceneCreateInput{
			Title:        &title,
			Date:         d,
			Fingerprints: []models.FingerprintEditInput{},
			Performers: []models.PerformerAppearanceInput{
				{PerformerID: performerID, As: nil},
			},
		})
		require.NoError(t, err, "creating timeline scene dated %q", d)
	}
	return performerID
}

// makeTimelinePerformerWithNamedScene is makeTimelinePerformer plus a handle on ONE
// specific scene -- the 2021 one, so deleting it takes a dated bucket from 1 to 0 rather
// than merely reducing the total. That makes the bucket-level effect observable, not just
// the total.
func makeTimelinePerformerWithNamedScene(t *testing.T, name string) (uuid.UUID, uuid.UUID) {
	t.Helper()
	r := asAdmin(t)

	var created struct {
		PerformerCreate struct {
			ID string
		}
	}
	r.client.MustPost(
		`mutation($input: PerformerCreateInput!) { performerCreate(input: $input) { id } }`,
		&created,
		client.Var("input", map[string]any{"name": name}))
	performerID, err := uuid.FromString(created.PerformerCreate.ID)
	require.NoError(t, err)

	var named uuid.UUID
	for _, d := range []string{"2019-01-01", "2019-06-01", "2021-03-01", "2023-01-01", "", ""} {
		title := name + " named scene " + d
		scene, err := r.client.createScene(models.SceneCreateInput{
			Title:        &title,
			Date:         d,
			Fingerprints: []models.FingerprintEditInput{},
			Performers:   []models.PerformerAppearanceInput{{PerformerID: performerID}},
		})
		require.NoError(t, err)
		if d == "2021-03-01" {
			parsed, err := uuid.FromString(scene.ID)
			require.NoError(t, err)
			named = parsed
		}
	}
	require.NotEqual(t, uuid.Nil, named, "the fixture must name one scene to delete")
	return performerID, named
}

func askTimeline(t *testing.T, id uuid.UUID) timelineRow {
	t.Helper()
	var resp timelineResponse
	asRead(t).client.MustPost(
		`query($id: ID!) { findPerformer(id: $id) { id name scene_count timeline {
			entries { year sceneCount }
			undatedCount
			sceneCount
		} } }`,
		&resp,
		client.Var("id", id.String()))
	require.NotNil(t, resp.FindPerformer, "performer must resolve")
	return resp.FindPerformer.Timeline
}

// Buckets group by year, count correctly within a year, and come back oldest-first.
func TestPerformerTimelineBucketsByYear(t *testing.T) {
	id := makeTimelinePerformer(t, "Timeline Buckets")

	got := askTimeline(t, id)

	assert.Equal(t, []timelineEntryRow{
		{Year: 2019, SceneCount: 2},
		{Year: 2021, SceneCount: 1},
		{Year: 2023, SceneCount: 3},
	}, got.Entries,
		"years must group by year, count correctly within each, and be ordered oldest first")
}

// The property that makes the feature honest: the timeline's total EQUALS the performer's
// own scene count.
//
// Asserted against the profile's `sceneCount` rather than against the fixture I wrote,
// because the point is that two independent paths agree. If undated appearances were
// dropped from the buckets and not counted separately, the timeline would read as a
// 6-scene career for a 7-scene performer -- and both numbers on the same page would look
// fine independently.
func TestPerformerTimelineTotalAgreesWithSceneCount(t *testing.T) {
	id := makeTimelinePerformer(t, "Timeline Total")

	var resp timelineResponse
	asRead(t).client.MustPost(
		`query($id: ID!) { findPerformer(id: $id) { scene_count timeline { entries { year sceneCount } undatedCount sceneCount } } }`,
		&resp,
		client.Var("id", id))
	require.NotNil(t, resp.FindPerformer)

	got := resp.FindPerformer
	require.Equal(t, 7, got.SceneCount, "the fixture must have 7 appearances")
	assert.Equal(t, 1, got.Timeline.UndatedCount,
		"the undated scene must be counted separately, or the timeline silently under-reports")
	assert.Equal(t, got.SceneCount, got.Timeline.SceneCount,
		"the timeline total and the profile's scene count must be the SAME number -- a "+
			"disagreement means undated appearances fell out of both")
}

// scenes.date is TEXT, and the archive's placeholder for an unknown date is '--', not
// NULL or ”. So the undated count has to come from a failed YEAR PARSE, not from a NULL
// check -- counting NULLs alone would report 0 undated for a performer whose every date is
// a placeholder, which reads as "fully dated" and is the opposite of the truth.
func TestPerformerTimelineTreatsPlaceholderDateAsUndated(t *testing.T) {
	r := asAdmin(t)

	var created struct {
		PerformerCreate struct {
			ID string
		}
	}
	r.client.MustPost(
		`mutation($input: PerformerCreateInput!) { performerCreate(input: $input) { id } }`,
		&created,
		client.Var("input", map[string]any{"name": "Placeholder Dates"}))
	id, err := uuid.FromString(created.PerformerCreate.ID)
	require.NoError(t, err)

	// One properly dated, one with the '--' placeholder, one empty.
	for _, d := range []string{"2020-05-05", "--", ""} {
		title := "Placeholder scene " + d
		_, err = r.client.createScene(models.SceneCreateInput{
			Title:        &title,
			Date:         d,
			Fingerprints: []models.FingerprintEditInput{},
			Performers:   []models.PerformerAppearanceInput{{PerformerID: id}},
		})
		require.NoError(t, err, "creating scene dated %q", d)
	}

	got := askTimeline(t, id)
	assert.Equal(t, []timelineEntryRow{{Year: 2020, SceneCount: 1}}, got.Entries,
		"only the real date produces a bucket")
	assert.Equal(t, 2, got.UndatedCount,
		"'--' and '' must both count as undated -- they are non-NULL, so a date IS NULL "+
			"check would count neither and report a fully-dated performer")
	assert.Equal(t, 3, got.SceneCount)
}

// A performer with no dated appearances is a real case, not an error.
//
// Worth its own test because the alternative failure is subtle: an undated-only performer
// yields zero buckets, and a client that renders the timeline as a bar chart has nothing
// to draw. That must be an empty list and a non-zero undatedCount, never null and never
// an error.
func TestPerformerTimelineWithOnlyUndatedAppearances(t *testing.T) {
	r := asAdmin(t)

	var created struct {
		PerformerCreate struct {
			ID string
		}
	}
	r.client.MustPost(
		`mutation($input: PerformerCreateInput!) { performerCreate(input: $input) { id } }`,
		&created,
		client.Var("input", map[string]any{"name": "Undated Only"}))
	id, err := uuid.FromString(created.PerformerCreate.ID)
	require.NoError(t, err)

	title := "Undated Only Scene"
	_, err = r.client.createScene(models.SceneCreateInput{
		Title:        &title,
		Date:         "",
		Fingerprints: []models.FingerprintEditInput{},
		Performers:   []models.PerformerAppearanceInput{{PerformerID: id}},
	})
	require.NoError(t, err)

	got := askTimeline(t, id)
	assert.Empty(t, got.Entries, "no dated scene means no buckets")
	assert.NotNil(t, got.Entries, "must be an empty list, not null -- a client should not "+
		"have to special-case 'no history' apart from 'no such performer'")
	assert.Equal(t, 1, got.UndatedCount)
	assert.Equal(t, 1, got.SceneCount, "the undated appearance still counts toward the total")
}

// A performer with NO appearances at all must not error.
func TestPerformerTimelineWithNoAppearancesIsEmptyNotNull(t *testing.T) {
	r := asAdmin(t)

	var created struct {
		PerformerCreate struct {
			ID string
		}
	}
	r.client.MustPost(
		`mutation($input: PerformerCreateInput!) { performerCreate(input: $input) { id } }`,
		&created,
		client.Var("input", map[string]any{"name": "No Scenes"}))
	id, err := uuid.FromString(created.PerformerCreate.ID)
	require.NoError(t, err)

	got := askTimeline(t, id)
	assert.NotNil(t, got.Entries)
	assert.Empty(t, got.Entries)
	assert.Zero(t, got.UndatedCount)
	assert.Zero(t, got.SceneCount)
}

// Deleted scenes must not appear in the timeline.
//
// A soft-deleted scene is excluded everywhere else via `deleted = false`, and a timeline
// that keeps counting deleted rows would report a longer career than the profile shows.
//
// THE FIXTURE HERE MATTERS AND MY FIRST VERSION GOT IT WRONG. I deleted
// `findPerformer.scenes[0]` -- and that field ALREADY excludes soft-deleted scenes, so the
// listed id was always live and the count fell whether or not the timeline's own
// `deleted = false` filter existed. I proved this by mutation: with the filter REMOVED
// from the generated query, the test still passed. A test that cannot fail is worse than
// no test, because it reports coverage.
//
// So the scene to delete is named explicitly by the fixture, and the assertion is an
// EXACT total rather than a decrease. Now the only thing that can hide the deleted row is
// the filter under test.
func TestPerformerTimelineExcludesDeletedScenes(t *testing.T) {
	id, doomedID := makeTimelinePerformerWithNamedScene(t, "Timeline Deleted")

	// SIX scenes: 2019x2, 2021x1, 2023x1, and two undated. After deleting the 2021 one,
	// five remain and the 2021 bucket disappears entirely -- so the entry count goes 3 -> 2,
	// not 3 -> 3. Asserting 3 would have been wrong in the direction that hides the bug.
	before := askTimeline(t, id)
	require.Equal(t, 6, before.SceneCount)

	// SOFT delete, via the query and not via sceneDestroy.
	//
	// sceneDestroy calls DeleteScene, which is `DELETE FROM scenes WHERE id = $1` with
	// ON DELETE CASCADE -- a HARD delete. The scene_performers rows disappear with the
	// scene, so the join yields nothing either way and the `deleted = false` filter is
	// never exercised. I found this by mutating the filter away, watching the test still
	// pass, and then counting the raw join rows: 5 with the filter, 5 without.
	//
	// A SoftDeleteScene query exists (`UPDATE scenes SET deleted = true ... RETURNING *`)
	// and no GraphQL mutation calls it -- sceneDestroy is the hard delete. So the flag is
	// set with the same statement, through testutil.DB() as the other migration-adjacent
	// tests in this package do. This is the only way to produce a row that still joins but
	// is flagged, which is the state the filter exists to exclude.
	_, err := testutil.DB().Exec(t.Context(),
		`UPDATE scenes SET deleted = true WHERE id = $1`, doomedID)
	require.NoError(t, err, "the fixture must flag the scene, not remove it")

	// Precondition: the row still joins. Without this the test could pass for the same
	// reason it passed before -- an absent row rather than a filtered one.
	var stillJoins bool
	require.NoError(t, testutil.DB().QueryRow(t.Context(), `
		SELECT EXISTS (SELECT 1 FROM scene_performers SP JOIN scenes S ON S.id = SP.scene_id
		                JOIN performers P ON P.id = SP.performer_id
		                 WHERE P.name = 'Timeline Deleted' AND S.id = $1 AND S.deleted)`,
		doomedID).Scan(&stillJoins))
	require.True(t, stillJoins,
		"the soft-deleted scene must still be joinable, or this test proves nothing")

	after := askTimeline(t, id)
	assert.Equal(t, 5, after.SceneCount,
		"the soft-deleted scene must leave the timeline. If `deleted = false` were "+
			"missing from the query this would read 6")
	assert.Len(t, after.Entries, 2,
		"2021 had exactly one appearance, so deleting it empties that bucket entirely -- "+
			"this is why the fixture deletes a scene whose year has no sibling")
}
