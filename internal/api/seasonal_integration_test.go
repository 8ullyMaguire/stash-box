//go:build integration

package api_test

import (
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/models"
)

// Seasonal events through the real handler (plan phase 6 slice 6g).
//
// Two things only this level can prove. The enum's WIRE SPELLING: a status is matched by a client on
// the literal string, and the mapping from the service's lower-case word to the schema's upper-case
// enum is hand-written Go -- a cast would compile and then report a value that does not exist, which
// is the failure mode the content-source status column is the standing example of.
//
// And that the progress is the CALLER's: two signed-in members asking the same question about the same
// event get their own numbers.

type seasonalEventOutput struct {
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Threshold   int    `json:"threshold"`
	Status      string `json:"status"`
	StartsAt    string `json:"startsAt"`
	EndsAt      string `json:"endsAt"`
	Description string `json:"description"`
}

type seasonalProgressOutput struct {
	Event         seasonalEventOutput `json:"event"`
	Contributions int                 `json:"contributions"`
	Reached       bool                `json:"reached"`
}

func (c *graphqlClient) seasonalEvents() ([]seasonalProgressOutput, error) {
	q := `
	query Seasonal {
		seasonalEvents {
			event {
				slug
				title
				description
				threshold
				status
				startsAt
				endsAt
			}
			contributions
			reached
		}
	}`
	var resp struct {
		SeasonalEvents []seasonalProgressOutput `json:"seasonalEvents"`
	}
	if err := c.Post(q, &resp); err != nil {
		return nil, err
	}
	return resp.SeasonalEvents, nil
}

// declareEvent writes an event the way an operator does.
func declareEvent(t *testing.T, slug string, starts, ends time.Time, threshold int) {
	t.Helper()
	_, err := testutil.DB().Exec(t.Context(),
		`INSERT INTO seasonal_events (id, slug, title, description, starts_at, ends_at, threshold)
		 VALUES ($1, $2, $3, 'A window with a threshold.', $4, $5, $6)`,
		uuid.Must(uuid.NewV7()), slug, "Test Event", starts, ends, threshold)
	require.NoError(t, err, "declaring an event")
}

func TestSeasonalEventsReportTheCallersOwnProgress(t *testing.T) {
	read := []models.RoleEnum{models.RoleEnumRead}
	busy, err := asAdmin(t).createTestUser(nil, read)
	require.NoError(t, err)
	idle, err := asAdmin(t).createTestUser(nil, read)
	require.NoError(t, err)

	// A window around now, so the event is ACTIVE and the status is a real mapping rather than a
	// constant: a status hard-coded to one value would pass an assertion written against a future
	// window as easily as against a running one.
	now := time.Now().UTC()
	slug := "wire-" + uuid.Must(uuid.NewV7()).String()[:8]
	declareEvent(t, slug, now.AddDate(0, 0, -1), now.AddDate(0, 0, 1), 2)

	recordApprovedEdits(t, busy.ID, 3) // three contributions today, inside the window

	busyRows, err := createTestRunner(t, busy, read).client.seasonalEvents()
	require.NoError(t, err)
	mine := findEvent(busyRows, slug)
	require.NotNil(t, mine, "the declared event must be in the list")

	assert.Equal(t, "Test Event", mine.Event.Title)
	assert.Equal(t, "A window with a threshold.", mine.Event.Description)
	assert.Equal(t, 2, mine.Event.Threshold)
	assert.Equal(t, "ACTIVE", mine.Event.Status, "the status is the enum's spelling, not the service's word")
	assert.NotEmpty(t, mine.Event.StartsAt)
	assert.NotEmpty(t, mine.Event.EndsAt)

	// THE ENUM IS NOT A CONSTANT, AND "ONE OF THREE" IS NOT ENOUGH TO SAY SO: a mapping that always
	// answered ACTIVE would satisfy a membership check on a running event, which is what the first
	// version of this test asserted. So a CLOSED event is declared too, and its status is asserted by
	// name -- which is the only assertion a constant fails.
	closed := "wire-closed-" + uuid.Must(uuid.NewV7()).String()[:8]
	declareEvent(t, closed, now.AddDate(0, 0, -20), now.AddDate(0, 0, -10), 1)

	rows, err := createTestRunner(t, busy, read).client.seasonalEvents()
	require.NoError(t, err)
	mine = findEvent(rows, slug)
	require.NotNil(t, mine)
	assert.Equal(t, "ACTIVE", findEvent2(t, rows, slug).Event.Status)
	closedRow := findEvent2(t, rows, closed)
	assert.Equal(t, "ENDED", closedRow.Event.Status, "a window in the past is ENDED, not ACTIVE")

	assert.Equal(t, 3, mine.Contributions, "the caller's own contributions, inside the window")
	assert.True(t, mine.Reached, "three reaches a threshold of two")

	// AND NOBODY ELSE'S. The same event, the same instant, a different member.
	idleRows, err := createTestRunner(t, idle, read).client.seasonalEvents()
	require.NoError(t, err)
	theirs := findEvent(idleRows, slug)
	require.NotNil(t, theirs)
	assert.Equal(t, 0, theirs.Contributions, "a member who did nothing is not shown somebody else's work")
	assert.False(t, theirs.Reached)
}

// A CALLER WITH NO ROLES IS REFUSED: the board's rule, for the board's reason.
func TestSeasonalEventsNeedASignedInMember(t *testing.T) {
	_, err := asNone(t).client.seasonalEvents()
	require.Error(t, err, "an account with no roles asks for its own progress and gets nothing")
}

// findEvent2 is findEvent with a fatal rather than a nil, for the second and later lookups in a test.
func findEvent2(t *testing.T, rows []seasonalProgressOutput, slug string) *seasonalProgressOutput {
	t.Helper()
	row := findEvent(rows, slug)
	require.NotNil(t, row, "fixture: %s must be in the list", slug)
	return row
}

func findEvent(rows []seasonalProgressOutput, slug string) *seasonalProgressOutput {
	for i, row := range rows {
		if row.Event.Slug == slug {
			return &rows[i]
		}
	}
	return nil
}
