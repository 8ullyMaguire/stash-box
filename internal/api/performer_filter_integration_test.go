//go:build integration

package api_test

import (
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Issue #829: "Some Performer Filter Criterions Do Not Work".
//
// The reporter listed eleven filters that were accepted by the GraphQL schema
// and then dropped by the query builder, so a query setting them returned every
// performer instead of the filtered set. That is a silent wrong answer, not an
// error: the only way to notice was to notice the result was too large.
//
// Each test below creates a performer that MATCHES and one that DOES NOT, then
// asserts the filter returns exactly the matching one. Returning the
// non-matching performer is the bug; returning both is also the bug.

// ptr helpers: the create input models these fields as pointers.
func pEnum[T ~string](v T) *T      { return &v }
func pInt[T ~int | ~int32](v T) *T { return &v }
func pStr(v string) *string        { return &v }

func (s *testRunner) queryPerformerIDs(t *testing.T, input models.PerformerQueryInput) []uuid.UUID {
	t.Helper()

	// The integration suite shares one database, so performers created by other
	// tests are still in the table. Without an explicit page size this uses the
	// 25-row default and a fixture created moments earlier can fall off the
	// end -- which is how TestPerformerFilterEyeColorIsNull started failing
	// once #1007 added a few more performers. A fixed page size still cannot fix
	// a genuinely unbounded result set, but it makes the filter assertions
	// depend on the filter rather than on how much other data happens to exist.
	if input.PerPage == 0 {
		input.PerPage = 10000
	}

	res, err := s.resolver.Query().QueryPerformers(s.ctx, input)
	require.NoError(t, err)
	if res == nil {
		return nil
	}

	q, err := s.resolver.QueryPerformersResultType().Performers(s.ctx, res)
	require.NoError(t, err)

	ids := make([]uuid.UUID, 0, len(q))
	for i := range q {
		ids = append(ids, q[i].ID)
	}
	return ids
}

func TestPerformerFilterBreastType(t *testing.T) {
	pt := createPerformerTestRunner(t)

	match, err := pt.createTestPerformer(&models.PerformerCreateInput{
		Name:       pt.generatePerformerName(),
		BreastType: pEnum(models.BreastTypeEnumNatural),
	})
	require.NoError(t, err)

	other, err := pt.createTestPerformer(&models.PerformerCreateInput{
		Name:       pt.generatePerformerName(),
		BreastType: pEnum(models.BreastTypeEnumFake),
	})
	require.NoError(t, err)

	ids := pt.queryPerformerIDs(t, models.PerformerQueryInput{
		BreastType: &models.BreastTypeCriterionInput{
			Value:    pEnum(models.BreastTypeEnumNatural),
			Modifier: models.CriterionModifierEquals,
		},
	})

	assert.Contains(t, ids, match.UUID(), "matching performer should be returned")
	assert.NotContains(t, ids, other.UUID(),
		"performer with a different breast_type must be filtered out (#829)")
}

func TestPerformerFilterEyeColorAndHairColor(t *testing.T) {
	pt := createPerformerTestRunner(t)

	match, err := pt.createTestPerformer(&models.PerformerCreateInput{
		Name:      pt.generatePerformerName(),
		EyeColor:  pEnum(models.EyeColorEnumBlue),
		HairColor: pEnum(models.HairColorEnumAuburn),
	})
	require.NoError(t, err)

	other, err := pt.createTestPerformer(&models.PerformerCreateInput{
		Name:      pt.generatePerformerName(),
		EyeColor:  pEnum(models.EyeColorEnumGreen),
		HairColor: pEnum(models.HairColorEnumBlack),
	})
	require.NoError(t, err)

	ids := pt.queryPerformerIDs(t, models.PerformerQueryInput{
		EyeColor: &models.EyeColorCriterionInput{
			Value:    pEnum(models.EyeColorEnumBlue),
			Modifier: models.CriterionModifierEquals,
		},
		HairColor: &models.HairColorCriterionInput{
			Value:    pEnum(models.HairColorEnumAuburn),
			Modifier: models.CriterionModifierEquals,
		},
	})

	assert.Contains(t, ids, match.UUID())
	assert.NotContains(t, ids, other.UUID(),
		"eye_color/hair_color filters must actually filter (#829)")
}

func TestPerformerFilterMeasurements(t *testing.T) {
	pt := createPerformerTestRunner(t)

	match, err := pt.createTestPerformer(&models.PerformerCreateInput{
		Name:      pt.generatePerformerName(),
		Height:    pInt(170),
		CupSize:   pStr("DD"),
		BandSize:  pInt(80),
		WaistSize: pInt(60),
		HipSize:   pInt(90),
	})
	require.NoError(t, err)

	other, err := pt.createTestPerformer(&models.PerformerCreateInput{
		Name:      pt.generatePerformerName(),
		Height:    pInt(150),
		CupSize:   pStr("A"),
		BandSize:  pInt(60),
		WaistSize: pInt(50),
		HipSize:   pInt(70),
	})
	require.NoError(t, err)

	ids := pt.queryPerformerIDs(t, models.PerformerQueryInput{
		Height: &models.IntCriterionInput{
			Value:    170,
			Modifier: models.CriterionModifierEquals,
		},
		CupSize: &models.StringCriterionInput{
			Value:    "DD",
			Modifier: models.CriterionModifierEquals,
		},
	})

	assert.Contains(t, ids, match.UUID())
	assert.NotContains(t, ids, other.UUID(),
		"height/cup_size filters must actually filter (#829)")
}

func TestPerformerFilterCareerYears(t *testing.T) {
	pt := createPerformerTestRunner(t)

	match, err := pt.createTestPerformer(&models.PerformerCreateInput{
		Name:            pt.generatePerformerName(),
		CareerStartYear: pInt(2010),
		CareerEndYear:   pInt(2020),
	})
	require.NoError(t, err)

	other, err := pt.createTestPerformer(&models.PerformerCreateInput{
		Name:            pt.generatePerformerName(),
		CareerStartYear: pInt(1990),
		CareerEndYear:   pInt(2000),
	})
	require.NoError(t, err)

	ids := pt.queryPerformerIDs(t, models.PerformerQueryInput{
		CareerStartYear: &models.IntCriterionInput{
			Value:    2010,
			Modifier: models.CriterionModifierEquals,
		},
	})

	assert.Contains(t, ids, match.UUID())
	assert.NotContains(t, ids, other.UUID(),
		"career_start_year filter must actually filter (#829)")
}

// A performer with no recorded eye color is reachable by asking for exactly
// that. The column is nullable, so IS NULL is a real question, not a no-op.
func TestPerformerFilterEyeColorIsNull(t *testing.T) {
	pt := createPerformerTestRunner(t)

	unset, err := pt.createTestPerformer(&models.PerformerCreateInput{
		Name: pt.generatePerformerName(),
	})
	require.NoError(t, err)

	set, err := pt.createTestPerformer(&models.PerformerCreateInput{
		Name:     pt.generatePerformerName(),
		EyeColor: pEnum(models.EyeColorEnumBlue),
	})
	require.NoError(t, err)

	ids := pt.queryPerformerIDs(t, models.PerformerQueryInput{
		EyeColor: &models.EyeColorCriterionInput{
			Modifier: models.CriterionModifierIsNull,
		},
	})

	assert.Contains(t, ids, unset.UUID(), "performer with no eye_color should match IS NULL")
	assert.NotContains(t, ids, set.UUID(), "performer with an eye_color must not match IS NULL")
}

func TestPerformerFilterTattooLocation(t *testing.T) {
	pt := createPerformerTestRunner(t)

	match, err := pt.createTestPerformer(&models.PerformerCreateInput{
		Name:    pt.generatePerformerName(),
		Tattoos: []models.BodyModificationInput{{Location: "Left shoulder"}},
	})
	require.NoError(t, err)

	other, err := pt.createTestPerformer(&models.PerformerCreateInput{
		Name:    pt.generatePerformerName(),
		Tattoos: []models.BodyModificationInput{{Location: "Right ankle"}},
	})
	require.NoError(t, err)

	ids := pt.queryPerformerIDs(t, models.PerformerQueryInput{
		Tattoos: &models.BodyModificationCriterionInput{
			Location: pStr("Left shoulder"),
			Modifier: models.CriterionModifierEquals,
		},
	})

	assert.Contains(t, ids, match.UUID())
	assert.NotContains(t, ids, other.UUID(),
		"tattoo location filter must actually filter (#829)")
}

// Both location and description have to hold on the SAME modification. A
// performer with a left-shoulder tattoo and a separate wing tattoo must not
// match a query asking for both.
func TestPerformerFilterTattooLocationAndDescriptionMatchSameEntry(t *testing.T) {
	pt := createPerformerTestRunner(t)

	split, err := pt.createTestPerformer(&models.PerformerCreateInput{
		Name: pt.generatePerformerName(),
		Tattoos: []models.BodyModificationInput{
			{Location: "Left shoulder"},
			{Location: "Wing", Description: pStr("Falcon")},
		},
	})
	require.NoError(t, err)

	combined, err := pt.createTestPerformer(&models.PerformerCreateInput{
		Name: pt.generatePerformerName(),
		Tattoos: []models.BodyModificationInput{
			{Location: "Left shoulder", Description: pStr("Falcon")},
		},
	})
	require.NoError(t, err)

	ids := pt.queryPerformerIDs(t, models.PerformerQueryInput{
		Tattoos: &models.BodyModificationCriterionInput{
			Location:    pStr("Left shoulder"),
			Description: pStr("Falcon"),
			Modifier:    models.CriterionModifierEquals,
		},
	})

	assert.Contains(t, ids, combined.UUID())
	assert.NotContains(t, ids, split.UUID(),
		"location and description must match the same modification, not two different ones")
}
