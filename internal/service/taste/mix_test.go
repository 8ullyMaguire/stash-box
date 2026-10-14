package taste

import (
	"fmt"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/models"
)

func feedFixture(titles ...string) []FeedScene {
	out := make([]FeedScene, 0, len(titles))
	for i, title := range titles {
		out = append(out, FeedScene{
			Scene:    &models.Scene{ID: uuid.FromStringOrNil(fmt.Sprintf("00000000-0000-0000-0000-%012d", i+1)), Title: strptr(title)},
			Affinity: float64(len(titles) - i),
		})
	}
	return out
}

func strptr(s string) *string { return &s }

func feedTitles(feed []FeedScene) []string {
	out := make([]string, 0, len(feed))
	for _, f := range feed {
		out = append(out, *f.Scene.Title)
	}
	return out
}

// serendipity 0 must be a SUBSET of the affinity order (section 9's acceptance criterion). A
// feed that claims to rank by taste and then shows something the taste does not explain is a
// ranking with a hole in it.
func TestSerendipityZeroIsTheAffinityOrderAndItsSubset(t *testing.T) {
	ranked := feedFixture("a", "b", "c", "d", "e")
	other := feedFixture("x", "y", "z")

	feed := mixFeed(ranked, other, 3, 0, 1)
	assert.Equal(t, []string{"a", "b", "c"}, feedTitles(feed), "no surprises at all")

	// The same call with a pool of exactly the page: still the affinity order.
	assert.Equal(t, []string{"a", "b", "c"}, feedTitles(mixFeed(ranked, nil, 3, 0, 1)))
}

// The degenerate-output test section 9 asks for: two contrasting settings of the parameter over
// the SAME SEED must not return near-identical pages. If serendipity did nothing, or if the seed
// and the parameter were the wrong way round, this is the assertion that fails.
func TestSerendipityChangesThePageAtTheSameSeed(t *testing.T) {
	ranked := feedFixture("a", "b", "c", "d", "e", "f", "g", "h", "i", "j")
	other := feedFixture("p", "q", "r", "s", "t", "u", "v", "w", "x", "y")

	off := mixFeed(ranked, other, 10, 0, 42)
	on := mixFeed(ranked, other, 10, 1, 42)
	require.Len(t, off, 10)
	require.Len(t, on, 10)

	offSet := map[string]struct{}{}
	for _, f := range off {
		offSet[*f.Scene.Title] = struct{}{}
	}
	shared := 0
	for _, f := range on {
		if _, ok := offSet[*f.Scene.Title]; ok {
			shared++
		}
	}
	assert.Equal(t, 0, shared,
		"serendipity 1 is every slot from outside the affinity order, so a page of 10 drawn from a "+
			"pool of 10 surprises shares nothing with the affinity page; got %v vs %v", feedTitles(off), feedTitles(on))

	// And the same seed twice must agree: reproducible is what makes the above attributable to
	// the parameter rather than to the shuffle.
	assert.Equal(t, feedTitles(on), feedTitles(mixFeed(ranked, other, 10, 1, 42)))

	// A different seed must NOT be required to differ, but with a pool this size it will; the
	// assertion that matters is that the seed is actually used.
	assert.NotEqual(t, feedTitles(on), feedTitles(mixFeed(ranked, other, 10, 1, 43)))
}

func TestSerendipitySetsTheStride(t *testing.T) {
	ranked := feedFixture("a", "b", "c", "d", "e", "f", "g", "h")
	other := feedFixture("p", "q", "r", "s")

	// 0.5 -> every second slot is a surprise, so slots 1,3,5,7 keep the affinity order.
	feed := mixFeed(ranked, other, 8, 0.5, 7)
	require.Len(t, feed, 8)
	for _, slot := range []int{0, 2, 4, 6} {
		assert.Equal(t, string(rune('a'+slot/2)), *feed[slot].Scene.Title,
			"slot %d should be an affinity pick", slot+1)
	}
	for _, slot := range []int{1, 3, 5, 7} {
		assert.Contains(t, []string{"p", "q", "r", "s"}, *feed[slot].Scene.Title,
			"slot %d should be a surprise", slot+1)
	}

	// 0.2 -> one slot in five (5, 10, ...). With a page of 8 only slot 5 is replaced.
	feed = mixFeed(ranked, other, 8, 0.2, 7)
	assert.Equal(t, []string{"a", "b", "c", "d"}, feedTitles(feed)[:4])
	assert.Contains(t, []string{"p", "q", "r", "s"}, *feed[4].Scene.Title, "slot 5 is the surprise")
}

// A pool with nothing to be surprised by is a short page, not a panic and not a duplicate.
func TestMixFeedDegradesWhenThereIsNothingToSurpriseWith(t *testing.T) {
	ranked := feedFixture("a", "b")
	assert.Equal(t, []string{"a", "b"}, feedTitles(mixFeed(ranked, nil, 5, 0.5, 1)))
	assert.Nil(t, mixFeed(ranked, nil, 0, 0.5, 1))

	// Serendipity 1 with no surprises falls back to the affinity order rather than returning
	// nothing: the parameter asks for a mix, and there is only one thing to mix.
	assert.Equal(t, []string{"a", "b"}, feedTitles(mixFeed(ranked, nil, 5, 1, 1)))
	// Out-of-range values are clamped, not trusted.
	assert.Equal(t, []string{"a", "b"}, feedTitles(mixFeed(ranked, nil, 5, 2, 1)))
}

func TestPoolSizeIsBounded(t *testing.T) {
	assert.Equal(t, 60+20*6, poolSize(20), "the default page")
	assert.Equal(t, 60+50*6, poolSize(50))
	assert.Equal(t, maxPool, poolSize(1000), "bounded, because the scorer walks the tag graph per candidate")
}
