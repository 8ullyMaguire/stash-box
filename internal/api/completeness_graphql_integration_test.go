//go:build integration

package api_test

import (
	"context"
	"testing"

	"github.com/99designs/gqlgen/client"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbtest "github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/queries"
)

// Completeness over GraphQL, end to end (SPEC §7.24.2, slice 9f).
//
// The service is proved elsewhere. What this file proves is that a READER can see it: the verdict's
// rules arrive over the wire, the status round-trips through the enum, and `lostSource` is present
// exactly when something is lost. A resolver that returns the right struct is still broken if the
// schema names the field differently or the enum is spelled differently on the two sides, and neither
// is visible to a service-level test.

// Field names match the selection set: gqlgen decodes by name and panics on a key it does not know.
type completenessClaimResponse struct {
	Kind         string
	Expected     int
	Observed     int
	Missing      int
	SourceUrl    string
	Status       *string
	Unverifiable bool
}

type completenessVerdictResponse struct {
	Expected     int
	Observed     int
	Percent      int
	Score        int
	HasClaim     bool
	Unverifiable bool
	LostSource   *completenessClaimResponse
	Claims       []completenessClaimResponse
}

type studioCompletenessResponse struct {
	StudioCompleteness completenessVerdictResponse
}

const studioCompletenessQuery = `query($id: ID!) {
	studioCompleteness(studioId: $id) {
		expected observed percent score hasClaim unverifiable
		lostSource { kind expected observed missing sourceUrl status unverifiable }
		claims { kind expected observed missing sourceUrl status unverifiable }
	}
}`

func (v completenessVerdictResponse) onlyClaim(t *testing.T) completenessClaimResponse {
	t.Helper()
	require.Len(t, v.Claims, 1, "the studio has exactly one asserted total in this test")
	return v.Claims[0]
}

// A claim fully readable, then its page dies: the numbers stay and the record stops being checkable.
func TestStudioCompletenessOverGraphQL(t *testing.T) {
	studioID := uuid.FromStringOrNil(seedStudioRow(t, "gql-completeness-"+uuid.Must(uuid.NewV4()).String()))
	userID := uuid.FromStringOrNil(seedUserRow(t, "gql-completeness-user-"+uuid.Must(uuid.NewV4()).String()))

	// One scene of four hundred claimed: 0.25%, which is 0 either way. The rule this case cannot show is
	// the subject of its own test below; what it shows is that a nearly-empty archive reads as nearly
	// empty rather than as a small complete number.
	sceneID := uuid.Must(uuid.NewV7())
	_, err := contentQ().CreateScene(t.Context(), queries.CreateSceneParams{ID: sceneID, Title: strPtr("completeness scene")})
	require.NoError(t, err)
	_, err = dbtest.DB().Exec(t.Context(), `UPDATE scenes SET studio_id = $1 WHERE id = $2`, studioID, sceneID)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := dbtest.DB().Exec(context.Background(), `DELETE FROM scenes WHERE id = $1`, sceneID)
		require.NoError(t, err)
	})

	const canonical = "https://example.invalid/GqlStudio/One"
	_, err = dbtest.DB().Exec(t.Context(), `
		INSERT INTO expected_totals
			(entity_type, entity_id, kind, total, source_entity_type, source_url, asserted_by, asserted_at)
		VALUES ('studio', $1, 'scenes', 400, 'url', $2, $3, now())`,
		studioID, canonical, userID)
	require.NoError(t, err)

	var sourceID uuid.UUID
	require.NoError(t, dbtest.DB().QueryRow(t.Context(), `
		INSERT INTO content_sources (entity_type, entity_id, url, url_normalized, status)
		VALUES ('scene', $1, $2, $2, 'live') RETURNING id`,
		uuid.Must(uuid.NewV4()), canonical).Scan(&sourceID))
	t.Cleanup(func() {
		_, err := dbtest.DB().Exec(context.Background(), `DELETE FROM content_sources WHERE id = $1`, sourceID)
		require.NoError(t, err)
	})

	var resp studioCompletenessResponse
	asRead(t).client.MustPost(studioCompletenessQuery, &resp, client.Var("id", studioID))

	verdict := resp.StudioCompleteness
	assert.Equal(t, 400, verdict.Expected)
	assert.Equal(t, 1, verdict.Observed)
	assert.True(t, verdict.HasClaim)
	assert.False(t, verdict.Unverifiable)
	assert.Equal(t, 0, verdict.Percent,
		"0.25% is 0. This case does NOT distinguish flooring from rounding -- see the note on "+
			"TestStudioCompletenessFloorsRatherThanRounds -- and says so rather than claiming a rule it "+
			"cannot observe")
	assert.Equal(t, 0, verdict.Score)
	assert.Nil(t, verdict.LostSource, "nothing is lost, so there is nothing to act on")

	claim := verdict.onlyClaim(t)
	assert.Equal(t, "scenes", claim.Kind)
	assert.Equal(t, 399, claim.Missing)
	assert.Equal(t, canonical, claim.SourceUrl)
	require.NotNil(t, claim.Status)
	assert.Equal(t, "LIVE", *claim.Status, "the column is lowercase, the enum is not")
	assert.False(t, claim.Unverifiable)

	// The page dies. The arithmetic must not move -- and now the API says the record is uncheckable.
	_, err = dbtest.DB().Exec(t.Context(), `UPDATE content_sources SET status = 'dead' WHERE id = $1`, sourceID)
	require.NoError(t, err)

	var after studioCompletenessResponse
	asRead(t).client.MustPost(studioCompletenessQuery, &after, client.Var("id", studioID))

	dead := after.StudioCompleteness
	assert.Equal(t, 400, dead.Expected, "the denominator STANDS when its source dies")
	assert.Equal(t, 1, dead.Observed)
	assert.True(t, dead.Unverifiable)
	require.NotNil(t, dead.LostSource, "'unverifiable' without 'which claim' is not actionable")
	assert.Equal(t, canonical, dead.LostSource.SourceUrl)
	assert.Equal(t, "scenes", dead.LostSource.Kind)
	require.NotNil(t, dead.LostSource.Status)
	assert.Equal(t, "DEAD", *dead.LostSource.Status)
}

// FLOORING, proved where it differs from rounding. The first case in this file cannot show it: 1 of 400
// floors to 0 AND rounds to 0, because the rounding term (400/2) is smaller than the earned term. 2 of 3
// is the smallest case where the two rules disagree -- 66 against 67 -- and a client that showed the
// rounded figure would publish a percentage the archive does not hold.
//
// The claim is deliberately one short of met, and its page dies: 66 stays 66, because the cap is a
// CEILING and an entity below it loses nothing when its source goes.
func TestStudioCompletenessFloorsRatherThanRounds(t *testing.T) {
	studioID := uuid.FromStringOrNil(seedStudioRow(t, "gql-floor-"+uuid.Must(uuid.NewV4()).String()))
	userID := uuid.FromStringOrNil(seedUserRow(t, "gql-floor-user-"+uuid.Must(uuid.NewV4()).String()))

	for i := 0; i < 2; i++ {
		sceneID := uuid.Must(uuid.NewV7())
		_, err := contentQ().CreateScene(t.Context(), queries.CreateSceneParams{ID: sceneID, Title: strPtr("floor scene")})
		require.NoError(t, err)
		_, err = dbtest.DB().Exec(t.Context(), `UPDATE scenes SET studio_id = $1 WHERE id = $2`, studioID, sceneID)
		require.NoError(t, err)
		id := sceneID
		t.Cleanup(func() {
			_, err := dbtest.DB().Exec(context.Background(), `DELETE FROM scenes WHERE id = $1`, id)
			require.NoError(t, err)
		})
	}

	_, err := dbtest.DB().Exec(t.Context(), `
		INSERT INTO expected_totals
			(entity_type, entity_id, kind, total, source_entity_type, source_url, asserted_by, asserted_at)
		VALUES ('studio', $1, 'scenes', 3, 'url', $2, $3, now())`,
		studioID, "https://example.invalid/flooring", userID)
	require.NoError(t, err)

	var sourceID uuid.UUID
	require.NoError(t, dbtest.DB().QueryRow(t.Context(), `
		INSERT INTO content_sources (entity_type, entity_id, url, url_normalized, status)
		VALUES ('scene', $1, $2, $2, 'live') RETURNING id`,
		uuid.Must(uuid.NewV4()), "https://example.invalid/flooring").Scan(&sourceID))
	t.Cleanup(func() {
		_, err := dbtest.DB().Exec(context.Background(), `DELETE FROM content_sources WHERE id = $1`, sourceID)
		require.NoError(t, err)
	})

	var resp studioCompletenessResponse
	asRead(t).client.MustPost(studioCompletenessQuery, &resp, client.Var("id", studioID))

	verdict := resp.StudioCompleteness
	assert.Equal(t, 3, verdict.Expected)
	assert.Equal(t, 2, verdict.Observed)
	assert.Equal(t, 66, verdict.Percent, "2 of 3 FLOORS to 66; the completion scorer's rounding would say 67")
	assert.Equal(t, 66, verdict.Score, "a live source adds no cap")

	_, err = dbtest.DB().Exec(t.Context(), `UPDATE content_sources SET status = 'dead' WHERE id = $1`, sourceID)
	require.NoError(t, err)

	var after studioCompletenessResponse
	asRead(t).client.MustPost(studioCompletenessQuery, &after, client.Var("id", studioID))

	assert.Equal(t, 66, after.StudioCompleteness.Score,
		"the cap is a CEILING: at 66% a lost source changes the record, not the number")
	assert.Equal(t, 66, after.StudioCompleteness.Percent)
	assert.True(t, after.StudioCompleteness.Unverifiable)
	require.NotNil(t, after.StudioCompleteness.LostSource)
}

// A claim whose page this instance does not track: status is NULL, not a status. The distinction is
// the whole reason the field is nullable -- an empty-string status would either become UNKNOWN (a lie:
// nothing has checked the URL) or fail the read, and every source page in the world that is not one of
// ours lands here.
func TestStudioCompletenessWithAnUntrackedSource(t *testing.T) {
	studioID := uuid.FromStringOrNil(seedStudioRow(t, "gql-untracked-"+uuid.Must(uuid.NewV4()).String()))
	userID := uuid.FromStringOrNil(seedUserRow(t, "gql-untracked-user-"+uuid.Must(uuid.NewV4()).String()))

	_, err := dbtest.DB().Exec(t.Context(), `
		INSERT INTO expected_totals
			(entity_type, entity_id, kind, total, source_entity_type, source_url, asserted_by, asserted_at)
		VALUES ('studio', $1, 'scenes', 7, 'url', $2, $3, now())`,
		studioID, "https://nowhere.invalid/untracked", userID)
	require.NoError(t, err)

	var resp studioCompletenessResponse
	asRead(t).client.MustPost(studioCompletenessQuery, &resp, client.Var("id", studioID))

	verdict := resp.StudioCompleteness
	require.Len(t, verdict.Claims, 1)
	assert.Equal(t, 7, verdict.Expected)
	assert.Nil(t, verdict.Claims[0].Status, "no record is not a status, and must not read as UNKNOWN")
	assert.False(t, verdict.Claims[0].Unverifiable)
	assert.False(t, verdict.Unverifiable, "an untracked page does not cap the studio")
	assert.Nil(t, verdict.LostSource)
}

// A studio nobody has claimed anything about: no ratio, and NOT a lost source. Every absence assertion
// here is preceded by a count, because "no claims" and "the query returned nothing" look identical.
func TestStudioCompletenessWithoutAClaim(t *testing.T) {
	studioID := uuid.FromStringOrNil(seedStudioRow(t, "gql-noclaim-"+uuid.Must(uuid.NewV4()).String()))

	var resp studioCompletenessResponse
	asRead(t).client.MustPost(studioCompletenessQuery, &resp, client.Var("id", studioID))

	verdict := resp.StudioCompleteness
	require.NotNil(t, verdict.Claims, "the field resolved (an empty list, not a missing field)")
	assert.Len(t, verdict.Claims, 0)
	assert.False(t, verdict.HasClaim)
	assert.Equal(t, 0, verdict.Expected)
	assert.Equal(t, 0, verdict.Percent)
	assert.Equal(t, 0, verdict.Score)
	assert.False(t, verdict.Unverifiable, "nothing claimed is not a lost source")
	assert.Nil(t, verdict.LostSource)
}
