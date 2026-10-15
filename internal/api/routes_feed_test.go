package api

import (
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/stashapp/stash-box/internal/models"
)

// The pure half of the feed: how a RECORD becomes an item's text.
//
// It is here rather than in the integration file because none of it needs a database, and because the
// cases that matter are the awkward ones -- a record with no title, no date, no duration, a feed where
// the studio could not be read -- which are tedious to build for real and trivial to state here.

func str(value string) *string { return &value }
func num(value int) *int       { return &value }

func TestAScenesDescriptionIsWhatAReaderScansFor(t *testing.T) {
	scene := models.Scene{Title: str("A Record"), Date: str("2026-10-15"), Duration: num(1812)}

	description := describeScene(scene, []string{"One", "Two"}, "A Studio")

	assert.Equal(t, "With One, Two · A Studio · 2026-10-15 · 30:12", description)
}

func TestADescriptionNeverInventsWhatTheRecordDoesNotSay(t *testing.T) {
	// A record with nothing but an id: the description is EMPTY rather than a row of separators, because
	// " ·  · " reads as a record whose fields are broken rather than as one that has none.
	assert.Equal(t, "", describeScene(models.Scene{}, nil, ""))
	assert.Equal(t, "", describeScene(models.Scene{Duration: num(0)}, nil, ""),
		"a zero duration is not a duration")
	assert.Equal(t, "2026-10-15", describeScene(models.Scene{Date: str("2026-10-15")}, nil, ""))
	assert.Equal(t, "With One", describeScene(models.Scene{}, []string{"One"}, ""))
}

func TestDurationReadsTheWayAPersonReadsIt(t *testing.T) {
	assert.Equal(t, "0:45", formatDuration(45))
	assert.Equal(t, "30:12", formatDuration(1812))
	assert.Equal(t, "1:02:03", formatDuration(3723))
	assert.Equal(t, "10:00:00", formatDuration(36000))
}

func TestATitleFallsBackToSomethingClickable(t *testing.T) {
	assert.Equal(t, "A Title", sceneTitle(models.Scene{Title: str("A Title")}))
	assert.Equal(t, "ABC-123", sceneTitle(models.Scene{Code: str("ABC-123")}),
		"the studio's own code for the work is a name a reader recognises")

	// A blank title is NOT a title: the item would read as an unnamed row in a list.
	assert.Equal(t, "ABC-123", sceneTitle(models.Scene{Title: str("   "), Code: str("ABC-123")}))

	// And with neither, the record's id -- truncated, because it is an identifier rather than a name.
	id := uuid.Must(uuid.FromString("8f14e45f-ceea-467a-9c1f-1a2b3c4d5e6f"))
	title := sceneTitle(models.Scene{ID: id})
	assert.Equal(t, "Scene 8f14e45f", title)
}
