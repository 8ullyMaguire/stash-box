package image

import (
	"fmt"
	"testing"

	"github.com/stashapp/stash-box/internal/models"
	"github.com/stretchr/testify/assert"
)

func formatDims(images []models.Image) []string {
	if images == nil {
		return nil
	}
	dims := make([]string, len(images))
	for i, img := range images {
		dims[i] = fmt.Sprintf("%dx%d", img.Width, img.Height)
	}
	return dims
}

func TestOrderLandscape(t *testing.T) {
	tests := []struct {
		name     string
		images   []models.Image
		expected []models.Image
	}{
		{
			name: "Sorts by widest to most narrow aspect ratio",
			images: []models.Image{
				{Width: 1080, Height: 1920}, // 9:16 (0.5625)
				{Width: 640, Height: 480},   // 4:3 (1.333)
				{Width: 400, Height: 600},   // 2:3 (0.666)
				{Width: 422, Height: 600},   // 0.703
				{Width: 1920, Height: 1080}, // 16:9 (1.777)
				{Width: 600, Height: 400},   // 3:2 (1.5)
			},
			expected: []models.Image{
				{Width: 1920, Height: 1080}, // 16:9 (1.777)
				{Width: 600, Height: 400},   // 3:2 (1.5)
				{Width: 640, Height: 480},   // 4:3 (1.333)
				{Width: 422, Height: 600},   // 0.703
				{Width: 400, Height: 600},   // 2:3 (0.666)
				{Width: 1080, Height: 1920}, // 9:16 (0.5625)
			},
		},
		{
			name: "Fallback to width descending when aspect ratio is identical",
			images: []models.Image{
				{Width: 500, Height: 1000},  // Aspect: 0.5, Width: 500
				{Width: 250, Height: 500},   // Aspect: 0.5, Width: 250
				{Width: 1000, Height: 2000}, // Aspect: 0.5, Width: 1000
			},
			expected: []models.Image{
				{Width: 1000, Height: 2000},
				{Width: 500, Height: 1000},
				{Width: 250, Height: 500},
			},
		},
		{
			name: "Zero width images sort last (aspect 0.0)",
			images: []models.Image{
				{Width: 1920, Height: 1080}, // aspect 1.778
				{Width: 0, Height: 1000},    // aspect 0.0
				{Width: 640, Height: 480},   // aspect 1.333
			},
			expected: []models.Image{
				{Width: 1920, Height: 1080},
				{Width: 640, Height: 480},
				{Width: 0, Height: 1000},
			},
		},
		{
			name: "Zero height images do not panic",
			images: []models.Image{
				{Width: 1920, Height: 1080},
				{Width: 1000, Height: 0},
				{Width: 0, Height: 0},
			},
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Copy the input slice so we don't mutate the test case definition
			input := make([]models.Image, len(tt.images))
			copy(input, tt.images)

			OrderLandscape(input)

			if tt.expected != nil {
				assert.Equal(t, formatDims(tt.expected), formatDims(input))
			}
		})
	}
}

func TestOrderPortrait(t *testing.T) {
	tests := []struct {
		name     string
		images   []models.Image
		expected []models.Image
	}{
		{
			// Updated for #1205. This case previously led with 400x600 because
			// it is exactly the ideal 2:3 -- and it is only 240k pixels, while
			// 1080x1920 is 2.07M. Ordering by ratio alone made the thumbnail the
			// display image, which is the reported bug. The two large images
			// (2.07M each) are one resolution bucket, so the ratio comparison
			// still orders them relative to each other; the four small ones
			// (240k-307k) are all in bucket 0 and likewise still sort by ratio.
			name: "Resolution separates the tiers, ratio orders within a tier",
			images: []models.Image{
				{Width: 640, Height: 480},   // 4:3 (1.333), 307k, bucket 0
				{Width: 1920, Height: 1080}, // 16:9 (1.777), 2.07M, bucket 2
				{Width: 1080, Height: 1920}, // 9:16 (0.5625), 2.07M, bucket 2
				{Width: 400, Height: 600},   // 2:3 ideal, 240k, bucket 0
				{Width: 600, Height: 400},   // 3:2 (1.5), 240k, bucket 0
				{Width: 422, Height: 600},   // 0.703, 253k, bucket 0
			},
			expected: []models.Image{
				{Width: 1080, Height: 1920}, // bucket 2, then by ratio
				{Width: 1920, Height: 1080}, // bucket 2
				{Width: 400, Height: 600},   // bucket 0, ideal ratio
				{Width: 422, Height: 600},   // bucket 0
				{Width: 640, Height: 480},   // bucket 0
				{Width: 600, Height: 400},   // bucket 0
			},
		},
		{
			name: "Fallback to height descending when aspect ratio is identical",
			images: []models.Image{
				{Width: 500, Height: 1000},  // Aspect: 0.5, Height: 1000
				{Width: 250, Height: 500},   // Aspect: 0.5, Height: 500
				{Width: 1000, Height: 2000}, // Aspect: 0.5, Height: 2000
			},
			expected: []models.Image{
				{Width: 1000, Height: 2000},
				{Width: 500, Height: 1000},
				{Width: 250, Height: 500},
			},
		},
		{
			// Updated for #1205: 1080x1920 (2.07M) now precedes 400x600 (240k).
			// The property this case exists to pin -- the zero-width image sorts
			// last -- is unchanged.
			name: "Zero width images sort last (aspect 0.0, max distance from ideal)",
			images: []models.Image{
				{Width: 400, Height: 600},   // ideal 2:3, diff 0, bucket 0
				{Width: 0, Height: 1000},    // aspect 0.0, diff 0.667, bucket 0
				{Width: 1080, Height: 1920}, // 9:16 (0.5625), diff 0.104, bucket 2
			},
			expected: []models.Image{
				{Width: 1080, Height: 1920},
				{Width: 400, Height: 600},
				{Width: 0, Height: 1000},
			},
		},
		{
			name: "Zero height images do not panic",
			images: []models.Image{
				{Width: 400, Height: 600},
				{Width: 1000, Height: 0},
				{Width: 0, Height: 0},
			},
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Copy the input slice so we don't mutate the test case definition
			input := make([]models.Image, len(tt.images))
			copy(input, tt.images)

			OrderPortrait(input)

			if tt.expected != nil {
				assert.Equal(t, formatDims(tt.expected), formatDims(input))
			}
		})
	}
}
