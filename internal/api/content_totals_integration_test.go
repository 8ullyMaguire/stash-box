//go:build integration

package api_test

import (
	"context"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbtest "github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/completeness"
)

// The expected-total read path with its evidence attached (slice 9f).
//
// Phase 3a built the table and the queries; nothing read them, so this is the first code that
// answers "what does the source claim, what do we hold, and is the claim still checkable".
func seedTotal(t *testing.T, studioID, userID uuid.UUID, total int, sourceURL string) {
	t.Helper()

	_, err := dbtest.DB().Exec(t.Context(), `
		INSERT INTO expected_totals
			(entity_type, entity_id, kind, total, source_entity_type, source_url, asserted_by, asserted_at)
		VALUES ('studio', $1, 'scenes', $2, 'url', $3, $4, now())`,
		studioID, total, sourceURL, userID)
	require.NoError(t, err, "the denominator")
}

// The claim's page, as a health record. `url_normalized` is what the lookup is keyed by, so the
// tests below feed a source_url that only the canonicaliser can connect to it.
// The page is tracked as a URL of a SCENE, because that is what content_sources can hold: its
// entity_type CHECK allows 'scene' and 'content_item' and never 'studio'. Which entity owns the row
// is not the question this read answers -- the page's health is.
func seedSourcePage(t *testing.T, raw, normalized, status string) uuid.UUID {
	t.Helper()

	var id uuid.UUID
	require.NoError(t, dbtest.DB().QueryRow(t.Context(), `
		INSERT INTO content_sources (entity_type, entity_id, url, url_normalized, status)
		VALUES ('scene', $1, $2, $3, $4) RETURNING id`,
		uuid.Must(uuid.NewV4()), raw, normalized, status).Scan(&id))
	t.Cleanup(func() {
		_, err := dbtest.DB().Exec(context.Background(), `DELETE FROM content_sources WHERE id = $1`, id)
		require.NoError(t, err)
	})
	return id
}

func TestStudioTotalsCarryTheirSourcesHealth(t *testing.T) {
	studioID := uuid.FromStringOrNil(seedStudioRow(t, "totals-"+uuid.Must(uuid.NewV4()).String()))
	userID := uuid.FromStringOrNil(seedUserRow(t, "totals-assert-"+uuid.Must(uuid.NewV4()).String()))

	// One scene, so the observed count is a number that could not be confused with the claim.
	sceneID := uuid.Must(uuid.NewV7())
	_, err := contentQ().CreateScene(t.Context(), queries.CreateSceneParams{ID: sceneID, Title: strPtr("totals scene")})
	require.NoError(t, err)
	_, err = dbtest.DB().Exec(t.Context(), `UPDATE scenes SET studio_id = $1 WHERE id = $2`, studioID, sceneID)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := dbtest.DB().Exec(context.Background(), `DELETE FROM scenes WHERE id = $1`, sceneID)
		require.NoError(t, err)
	})

	// The curator's string is NOT canonical: uppercase host, a tracking parameter, a trailing
	// slash. Only the ingest path's canonicaliser connects it to the health record.
	const canonical = "https://example.invalid/Studio/One"
	seedTotal(t, studioID, userID, 400, "HTTPS://EXAMPLE.invalid/Studio/One/?utm_source=news")
	sourceID := seedSourcePage(t, canonical, canonical, "live")

	svc := completeness.NewService(queries.New(dbtest.DB()))
	totals, err := svc.StudioTotals(t.Context(), studioID)
	require.NoError(t, err)
	require.Len(t, totals, 1)

	total := totals[0]
	assert.Equal(t, "scenes", total.Kind)
	assert.Equal(t, int64(400), total.Expected)
	assert.Equal(t, int64(1), total.Observed)
	assert.Equal(t, int64(399), total.Missing)
	assert.Equal(t, "HTTPS://EXAMPLE.invalid/Studio/One/?utm_source=news", total.SourceURL,
		"the claim's own string is reported as it was written, not rewritten")
	require.NotNil(t, total.Source, "the source page resolves through the canonicaliser")
	assert.Equal(t, sourceID, uuid.UUID(*total.Source.ID), "the row the claim's page resolves to")
	assert.Equal(t, "live", total.SourceStatus())
	assert.False(t, total.Unverifiable())

	// The page dies. The numbers MUST NOT move: the claim is about the world, and the world did
	// not change when the page that recorded it went away. Dropping the denominator instead would
	// score this studio's 1 of 400 at 100%, which is the failure migration 97 exists to prevent.
	_, err = dbtest.DB().Exec(t.Context(), `UPDATE content_sources SET status = 'dead' WHERE id = $1`, sourceID)
	require.NoError(t, err)

	after, err := svc.StudioTotals(t.Context(), studioID)
	require.NoError(t, err)
	require.Len(t, after, 1)
	assert.Equal(t, int64(400), after[0].Expected, "the denominator STANDS when its source dies")
	assert.Equal(t, int64(399), after[0].Missing)
	assert.True(t, after[0].Unverifiable(), "but the claim is no longer checkable, and says so")
}

// A source page this instance does not track is the ORDINARY case, not a lost one: most
// denominators come from pages nothing aggregates, and reporting them as unverifiable would be
// crying wolf about every total on the instance.
func TestStudioTotalsTreatAnUntrackedSourceAsOrdinary(t *testing.T) {
	studioID := uuid.FromStringOrNil(seedStudioRow(t, "totals-untracked-"+uuid.Must(uuid.NewV4()).String()))
	userID := uuid.FromStringOrNil(seedUserRow(t, "totals-untracked-user-"+uuid.Must(uuid.NewV4()).String()))
	seedTotal(t, studioID, userID, 12, "https://nowhere.invalid/studio/list")

	svc := completeness.NewService(queries.New(dbtest.DB()))
	totals, err := svc.StudioTotals(t.Context(), studioID)
	require.NoError(t, err)
	require.Len(t, totals, 1)

	assert.Nil(t, totals[0].Source, "nothing tracks this page")
	assert.Equal(t, "", totals[0].SourceStatus())
	assert.False(t, totals[0].Unverifiable(),
		"no record is not evidence of loss, and treating it as such would flag every total")
}

// Over-count is real and intended: an instance holding MORE than the source claims gets a gap of
// zero and the surplus is the caller's to report. A negative "missing" would read as an error.
func TestStudioTotalsFloorTheGapAtZero(t *testing.T) {
	studioID := uuid.FromStringOrNil(seedStudioRow(t, "totals-overcount-"+uuid.Must(uuid.NewV4()).String()))
	userID := uuid.FromStringOrNil(seedUserRow(t, "totals-overcount-user-"+uuid.Must(uuid.NewV4()).String()))
	seedTotal(t, studioID, userID, 1, "https://nowhere.invalid/overcount")

	for i := 0; i < 2; i++ {
		sceneID := uuid.Must(uuid.NewV7())
		_, err := contentQ().CreateScene(t.Context(), queries.CreateSceneParams{ID: sceneID, Title: strPtr("overcount scene")})
		require.NoError(t, err)
		_, err = dbtest.DB().Exec(t.Context(), `UPDATE scenes SET studio_id = $1 WHERE id = $2`, studioID, sceneID)
		require.NoError(t, err)
		id := sceneID
		t.Cleanup(func() {
			_, err := dbtest.DB().Exec(context.Background(), `DELETE FROM scenes WHERE id = $1`, id)
			require.NoError(t, err)
		})
	}

	svc := completeness.NewService(queries.New(dbtest.DB()))
	totals, err := svc.StudioTotals(t.Context(), studioID)
	require.NoError(t, err)
	require.Len(t, totals, 1)
	assert.Equal(t, int64(1), totals[0].Expected)
	assert.Equal(t, int64(2), totals[0].Observed)
	assert.Equal(t, int64(0), totals[0].Missing, "the gap floors at zero rather than going negative")
}
