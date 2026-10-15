// Package seasonal measures member contributions inside declared events.
//
// A seasonal event is a NAMED WINDOW WITH A THRESHOLD: "Winter Sprint, the first two weeks of
// December, ten contributions". This package reads them and answers two questions about each
// one -- is it running, and how far along is this member -- and both answers are DERIVED.
//
// "ACTIVE" IS WHAT TODAY FALLS BETWEEN, not a status column: there is no scheduler here, no
// row per member per event, and nothing that decays. That is the same shape the streak, the
// heatmap and the leaderboard have, and for the same reason -- a stored state disagrees with
// the log that justifies it and nothing reports the disagreement.
//
// WHAT THIS SLICE DELIBERATELY DOES NOT DO: award anything. There is no badge, no points and
// no multiplier on the level curve, and that is a decision rather than an omission. A
// multiplier would be a change to the LEVEL POLICY of slice 6b -- which counts contributions,
// holds a demoted member down for a recovery window, and has a threshold table an operator
// moves deliberately -- and changing what a contribution is worth, for two weeks, to whoever
// happened to be active, is not something to smuggle in behind a title and a date range. The
// MEASUREMENT ships; the reward is a policy decision with its own slice.
package seasonal

import (
	"context"
	"errors"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/stashapp/stash-box/internal/queries"
)

// Status is where an event sits relative to a moment.
type Status string

const (
	// StatusUpcoming: it has not started.
	StatusUpcoming Status = "upcoming"
	// StatusActive: it is running, including the instant it ends.
	StatusActive Status = "active"
	// StatusEnded: it is over.
	StatusEnded Status = "ended"
)

// Event is one declared window.
type Event struct {
	Slug        string
	Title       string
	Description string
	StartsAt    time.Time
	EndsAt      time.Time
	// Threshold is how many contributions inside the window count as reaching it.
	Threshold int
}

// Progress is an event and what one member has done about it.
type Progress struct {
	Event Event
	// Status is where the event sits relative to the DATABASE's clock at the moment it was
	// read, resolved here rather than by the caller so that no caller has to be trusted with
	// the same question.
	Status Status
	// Contributions is how many the member recorded BETWEEN the two instants.
	Contributions int
	// Reached is whether that is at least the threshold. It is a fact about the log, so an
	// event that has ENDED still reports the members who reached it -- nothing takes credit
	// away, which is the same principle as the streak's "a level is never handed back on a
	// timer".
	Reached bool
}

// StatusOf places an event relative to a moment.
//
// THE BOUNDARIES ARE THE WHOLE FUNCTION, and they are closed on both ends on purpose: an
// event that starts at noon is active AT noon, and one that ends at midnight is still active
// AT midnight. That matches the count's own convention -- a contribution made at the closing
// instant counts -- so a member who submits at the last possible moment sees it counted
// rather than reading "ended" beside a progress that includes it.
func StatusOf(ev Event, now time.Time) Status {
	if now.Before(ev.StartsAt) {
		return StatusUpcoming
	}
	if now.After(ev.EndsAt) {
		return StatusEnded
	}
	return StatusActive
}

// Service reads events and member progress.
type Service struct {
	queries *queries.Queries
}

// NewService builds a seasonal service.
func NewService(q *queries.Queries) *Service {
	return &Service{queries: q}
}

// For returns every declared event with the member's progress against it, newest event first.
//
// "Now" comes from the DATABASE, following the rule the streak, the heatmap and the
// leaderboard already follow: an event's window is a fact about the instance's clock, and a
// server whose clock ran a minute fast would show a member an event that the count beside it
// disagreed about.
//
// One query per event, which is correct here for the same reason the vouch's user lookup is:
// the number of events is bounded by what an operator has declared, not by the membership.
func (s *Service) For(ctx context.Context, userID uuid.UUID) ([]Progress, error) {
	now, err := s.queries.DatabaseNow(ctx)
	if err != nil {
		return nil, err
	}

	rows, err := s.queries.ListSeasonalEvents(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]Progress, 0, len(rows))
	for _, row := range rows {
		ev := eventOf(row)
		count, err := s.queries.CountUserContributionsBetween(ctx, queries.CountUserContributionsBetweenParams{
			UserID:   userID,
			StartsAt: row.StartsAt,
			EndsAt:   row.EndsAt,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, Progress{
			Event:         ev,
			Status:        StatusOf(ev, now),
			Contributions: int(count),
			Reached:       int(count) >= ev.Threshold,
		})
	}
	return out, nil
}

// Event returns one event by slug, for callers that already know which one they want.
func (s *Service) Event(ctx context.Context, slug string) (Event, error) {
	row, err := s.queries.GetSeasonalEventBySlug(ctx, slug)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Event{}, ErrNoSuchEvent
		}
		return Event{}, err
	}
	return eventOf(row), nil
}

// ErrNoSuchEvent: no event carries that slug.
var ErrNoSuchEvent = errors.New("no such seasonal event")

func eventOf(row queries.SeasonalEvent) Event {
	return Event{
		Slug:        row.Slug,
		Title:       row.Title,
		Description: row.Description,
		StartsAt:    row.StartsAt.Time,
		EndsAt:      row.EndsAt.Time,
		Threshold:   int(row.Threshold),
	}
}
