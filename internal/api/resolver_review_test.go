package api

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/service/review"
)

// toReviewStatus's fallback for an unrecognised status, tested DIRECTLY.
//
// It is unreachable through normal writes: the reviews CHECK constraint allows exactly
// ('published', 'flagged', 'removed'), which is precisely the set the switch handles. I
// tried to cover it by mutating the fallback to PUBLISHED and re-running the whole
// integration suite, and it PASSED -- because no review can hold a status the switch
// misses. So an integration test cannot cover this, and claiming one did would be false.
//
// It is kept deliberately: it is the guard against a fourth status being added to the
// migration and forgotten here, where the failure mode is publishing a taken-down
// review. Hence a direct test rather than a hopeful integration assertion.
func TestToReviewStatusIsRestrictiveForUnknownValues(t *testing.T) {
	assert.Equal(t, models.ReviewStatusEnumPublished, toReviewStatus(review.StatusPublished))
	assert.Equal(t, models.ReviewStatusEnumFlagged, toReviewStatus(review.StatusFlagged))
	assert.Equal(t, models.ReviewStatusEnumRemoved, toReviewStatus(review.StatusRemoved))

	// Anything else must be restrictive. This is the load-bearing line: an unknown
	// status defaulting to PUBLISHED would expose a review a moderator took down.
	for _, unknown := range []string{"", "pending", "PUBLISHED", "spammed", "deleted"} {
		assert.Equal(t, models.ReviewStatusEnumRemoved, toReviewStatus(unknown),
			"an unrecognised status %q must read as REMOVED, never PUBLISHED", unknown)
	}
}
