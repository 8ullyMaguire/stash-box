package image

import (
	"math"
	"sort"

	"github.com/stashapp/stash-box/internal/models"
)

// Sorts by "most" to "least" landscape, i.e. largest to smallest aspect ratio; ties broken by largest --> smallest width.
func OrderLandscape(p []models.Image) {
	sort.Slice(p, func(a, b int) bool {
		if p[a].Height == 0 || p[b].Height == 0 {
			return false
		}
		aspectA := float64(p[a].Width) / float64(p[a].Height)
		aspectB := float64(p[b].Width) / float64(p[b].Height)
		if aspectA > aspectB {
			return true
		} else if aspectA < aspectB {
			return false
		}
		return p[a].Width > p[b].Width
	})
}

// portraitResolutionFloor is the pixel area below which an image is treated as
// too small to be chosen on aspect ratio alone.
//
// #1089 prioritized the 2:3 portrait ratio, and that is still the primary
// criterion -- but ratio was the ONLY criterion, with height as a tie-break, so
// resolution could never outrank a better ratio. A 200x300 thumbnail is exactly
// 2:3 and beat a 1200x2000 portrait (#1205).
//
// The floor is a band, not a rule that resolution always wins. Two images within
// one step of this floor of each other are still ordered by ratio, so #1089's
// intent is preserved for the normal case where all the candidate images are a
// similar size. Only a MAJOR difference in resolution is allowed to override the
// ratio, which is what the report asks for.
//
// 800*1000 = 800_000 pixels, roughly a 900x900 image: comfortably above the
// thumbnails and phone-wallpaper crops that caused the report, and below the
// 1200x2000+ portraits it is protecting.
const portraitResolutionFloor = 800_000

// resolutionBucket returns the image's area divided by the floor.
//
// Comparing buckets rather than raw areas is what makes this a band: two images
// in the same bucket are treated as the same resolution tier and fall through to
// the aspect-ratio comparison, while a large gap in buckets dominates it.
func resolutionBucket(img models.Image) int {
	return int(img.Width) * int(img.Height) / portraitResolutionFloor
}

// Sorts by distance from ideal aspect ratio of 2:3, with resolution as the
// primary discriminator when the candidates differ substantially in size; ties
// broken by largest --> smallest height.
func OrderPortrait(p []models.Image) {
	sort.Slice(p, func(a, b int) bool {
		if p[a].Height == 0 || p[b].Height == 0 {
			return false
		}
		// A major resolution difference outranks the ratio (#1205). Without
		// this, a perfect-ratio thumbnail sorts above a high-resolution
		// portrait, and since the first entry is the performer's display
		// image, the UI showed the low-quality one.
		if bucketA, bucketB := resolutionBucket(p[a]), resolutionBucket(p[b]); bucketA != bucketB {
			return bucketA > bucketB
		}
		aspectA := float64(p[a].Width) / float64(p[a].Height)
		aspectB := float64(p[b].Width) / float64(p[b].Height)
		aspectIdeal := 2.0 / 3.0
		diffA := math.Abs(aspectA - aspectIdeal)
		diffB := math.Abs(aspectB - aspectIdeal)
		if diffA < diffB {
			return true
		} else if diffA > diffB {
			return false
		}
		return p[a].Height > p[b].Height
	})
}
