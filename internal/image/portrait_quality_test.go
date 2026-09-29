package image

import (
	"fmt"
	"testing"

	"github.com/stashapp/stash-box/internal/models"
	"github.com/stretchr/testify/assert"
)

// Issue #1205: "Low quality Performer image prioritization".
//
//   The intent of #1089 was to prioritize a 2:3 image aspect ratio. The issue
//   with this change is that in some cases, very low resolution photos are
//   chosen over more suitable ones. Aspect ratio cannot be the sole method of
//   identifying a quality image.
//
//   For example, a 200x300 image (a perfect 2:3 ratio) gets prioritized over a
//   1200x2000 image. This is too extreme. There needs to be a reasonable cut
//   off where the aspect ratio calculation is secondary to the resolution of a
//   portrait image if there is a major difference between them.
//
// The bug is that OrderPortrait compared aspect-ratio distance FIRST and used
// height only as a tie-break, so resolution could never outrank a better ratio.
// A 200x300 thumbnail is exactly 2:3 and won against a 1200x2000 portrait.
//
// The first image in this list is the performer's display image, so the ordering
// is user-visible, not cosmetic.

// The reported pair: a tiny image at the ideal ratio must not beat a large one.
func TestOrderPortraitPrefersResolutionOverIdealRatio(t *testing.T) {
	images := []models.Image{
		{Width: 200, Height: 300},   // perfect 2:3, but tiny
		{Width: 1200, Height: 2000}, // exactly 2:3, large
	}

	OrderPortrait(images)

	assert.Equal(t, []string{"1200x2000", "200x300"}, formatDims(images),
		"a 200x300 thumbnail must not outrank a 1200x2000 portrait (#1205)")
}

// Within the resolution band, aspect ratio still decides -- that is the intent
// of #1089 and it must survive. Both images here are large enough that the
// floor does not separate them.
func TestOrderPortraitStillPrefersIdealRatioAtComparableResolution(t *testing.T) {
	images := []models.Image{
		{Width: 1066, Height: 1600}, // 2:3-ish, large
		{Width: 1600, Height: 1066}, // landscape, large
	}

	OrderPortrait(images)

	assert.Equal(t, []string{"1066x1600", "1600x1066"}, formatDims(images),
		"at comparable resolution the ideal 2:3 ratio must still win (#1205)")
}

// The tie-break: identical ratio AND both above the floor => larger height first.
// This is the pre-existing behaviour and it must be untouched.
func TestOrderPortraitTieBreaksOnHeightWhenBothLarge(t *testing.T) {
	images := []models.Image{
		{Width: 500, Height: 1000},
		{Width: 1000, Height: 2000},
		{Width: 250, Height: 500},
	}

	OrderPortrait(images)

	assert.Equal(t, []string{"1000x2000", "500x1000", "250x500"}, formatDims(images),
		"identical ratios above the floor must still sort by height descending")
}

// A large image with a poor ratio must still beat a small image with a good
// one, and a small image with a good one must beat a large image with a worse
// one. The floor is a band, not a blanket "biggest wins" rule: once two images
// are close enough in resolution, ratio decides again.
func TestOrderPortraitFloorIsABandNotABlanketRule(t *testing.T) {
	t.Run("small good-ratio loses to large worse-ratio", func(t *testing.T) {
		images := []models.Image{
			{Width: 200, Height: 300},   // 2:3, below floor
			{Width: 1200, Height: 1600}, // 3:4, worse ratio, above floor
		}
		OrderPortrait(images)
		assert.Equal(t, []string{"1200x1600", "200x300"}, formatDims(images),
			"resolution must outrank ratio when the gap is major (#1205)")
	})

	t.Run("small better-ratio wins against a comparable small image", func(t *testing.T) {
		// Both below the floor and close in resolution: the floor does not
		// separate them, so ratio decides as it did before.
		images := []models.Image{
			{Width: 640, Height: 480}, // 4:3, bad ratio
			{Width: 400, Height: 600}, // 2:3, ideal
		}
		OrderPortrait(images)
		assert.Equal(t, []string{"400x600", "640x480"}, formatDims(images),
			"comparable small images must still be ordered by ratio (#1205)")
	})
}

// Zero-dimension images must not panic and must still sort last, matching the
// behaviour the pre-existing tests pin.
func TestOrderPortraitZeroDimensionsSortLast(t *testing.T) {
	images := []models.Image{
		{Width: 0, Height: 1000},
		{Width: 400, Height: 600},
		{Width: 1200, Height: 2000},
	}

	assert.NotPanics(t, func() { OrderPortrait(images) })

	got := formatDims(images)
	assert.Equal(t, "0x1000", got[len(got)-1],
		"a zero-width image must sort last (#1205)")
}

func TestOrderPortraitReportedScenarioIsStable(t *testing.T) {
	// A realistic performer image set: a couple of large portraits, one ideal
	// ratio thumbnail, and some junk. The display image must be a large one.
	images := []models.Image{
		{Width: 200, Height: 300},   // ideal ratio, tiny
		{Width: 300, Height: 450},   // ideal ratio, still tiny
		{Width: 1200, Height: 2000}, // 2:3, large
		{Width: 1066, Height: 1600}, // ~2:3, large
		{Width: 800, Height: 1200},  // 2:3, medium
	}

	OrderPortrait(images)

	first := fmt.Sprintf("%dx%d", images[0].Width, images[0].Height)
	assert.Equal(t, "1200x2000", first,
		"the display image must not be a thumbnail (#1205)")
}
