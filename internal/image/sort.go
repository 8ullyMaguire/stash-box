package image

import (
	"math"
	"sort"

	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/models"
)

// Unranked is the tuple component for an image carrying no type from a group:
// such images sort last within that dimension
const Unranked = math.MaxInt

// RankTuple orders an image against the instance's vocabulary, one component
// per group in group priority order. Tuples compare lexicographically.
type RankTuple []int

// at reads a component, treating a missing one as Unranked, so an image with no
// assignments can be absent from the ranks map entirely.
func (t RankTuple) at(i int) int {
	if i < len(t) {
		return t[i]
	}
	return Unranked
}

func (t RankTuple) before(other RankTuple) bool {
	for i := range max(len(t), len(other)) {
		if a, b := t.at(i), other.at(i); a != b {
			return a < b
		}
	}
	return false
}

// OrderByType sorts images by their rank tuple, breaking ties with the
// entity's existing comparator
//
// The sort must stay stable: equally-ranked images would otherwise come back in
// an arbitrary order, which surfaces as the primary image changing when nobody
// edited anything
func OrderByType(images []models.Image, ranks map[uuid.UUID]RankTuple, tiebreak func([]models.Image)) {
	tiebreak(images)

	sort.SliceStable(images, func(a, b int) bool {
		return ranks[images[a].ID].before(ranks[images[b].ID])
	})
}

// NewestFirst wraps a tiebreak so that images equally ranked come back most
// recent first, undated last
//
// Dates are partial ISO 8601 and compare as strings: "2019" < "2019-06" <
// "2019-06-15", so a bare year sorts as the start of its year. Nothing needs
// parsing and there is no timezone to be wrong about
func NewestFirst(dates map[uuid.UUID]*string, tiebreak func([]models.Image)) func([]models.Image) {
	return func(images []models.Image) {
		tiebreak(images)

		sort.SliceStable(images, func(a, b int) bool {
			left, right := dates[images[a].ID], dates[images[b].ID]
			if left == nil || right == nil {
				return left != nil && right == nil
			}
			return *left > *right
		})
	}
}

// OrganizedFirst prefers moderator-reviewed images, breaking ties with the
// given comparator first: within one rank, an organized image beats any
// unorganized one, however the inner tiebreak would have ordered them.
func OrganizedFirst(tiebreak func([]models.Image)) func([]models.Image) {
	return func(images []models.Image) {
		tiebreak(images)

		sort.SliceStable(images, func(a, b int) bool {
			return images[a].Organized && !images[b].Organized
		})
	}
}

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
