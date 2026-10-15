package invite

import (
	"context"

	"github.com/gofrs/uuid"
	"github.com/stashapp/stash-box/internal/queries"
)

// EventKind is one thing a member's invitation can have done.
//
// THREE KINDS, AND THE POINT IS THAT THEY ARE DIFFERENT FACTS: a scan is interest, a signup is an
// address, an activation is a person. A member deciding whether their invitation is working needs
// to tell them apart -- "four people looked, one gave an address, nobody joined" and "one person
// looked and joined" are the same total and completely different news.
type EventKind string

const (
	// EventScan is somebody opening the key's landing page. NOBODY IN PARTICULAR: it is an
	// unauthenticated GET, so the row records a key, a kind and a time -- never an address, an agent
	// or an account.
	EventScan EventKind = "scan"
	// EventSignup is an address registered with the key: somebody's intent, not yet a person.
	EventSignup EventKind = "signup"
	// EventActivation is the account the key produced. This is the one that is a person.
	EventActivation EventKind = "activation"
)

// AllEventKinds is the closed set, in the order a member's own journey through an invitation goes.
func AllEventKinds() []EventKind {
	return []EventKind{EventScan, EventSignup, EventActivation}
}

// EventCounts is what a member sees.
//
// It is DERIVED EVERY TIME from the event rows -- there is no counter column anywhere -- so it
// cannot disagree with the events, and an event that was never recorded cannot be counted.
type EventCounts struct {
	Scans       int
	Signups     int
	Activations int
}

// KindCount is one row of a grouped count, in the shape this package reasons about rather than in
// the shape sqlc generated (the queries for a member and for one key return different row types
// with the same two columns).
type KindCount struct {
	Kind  string
	Count int
}

// CountsFromRows maps grouped rows onto the three counters.
//
// PURE, and it is where the case that matters lives: A KIND WITH NO EVENTS IS ABSENT FROM THE
// DATABASE'S ANSWER, not a zero. Reading the rows into a struct by assignment would leave a kind
// that never happened indistinguishable from a kind this code does not know, and a member whose
// invitations have never been scanned would be told nothing rather than "0". Every kind in
// AllEventKinds starts at zero and is filled in by what was found, and an unknown kind is IGNORED
// rather than invented -- the same rule the trust rollup applies to an unknown event kind.
func CountsFromRows(rows []KindCount) EventCounts {
	var out EventCounts
	for _, row := range rows {
		switch EventKind(row.Kind) {
		case EventScan:
			out.Scans = row.Count
		case EventSignup:
			out.Signups = row.Count
		case EventActivation:
			out.Activations = row.Count
		}
	}
	return out
}

// RecordEvent appends one event, inside the caller's transaction.
//
// It takes the transaction rather than opening one, because the event has to be committed WITH the
// thing it describes: a signup row that survives a signup that failed, or an activation that is
// missing because the member's own write rolled back, would both be counters that lie. A package
// function rather than a method for the same reason `invite.Referral` is reached through a closure:
// the `user` package is the caller, and it already imports this one.
//
// `userID` is the account a signup or an activation became, and is nil for a scan -- which is not a
// missing value but the fact that a scan is nobody.
func RecordEvent(ctx context.Context, tx *queries.Queries, inviteKeyID uuid.UUID, generatedBy uuid.UUID, kind EventKind, userID *uuid.UUID) error {
	arg := queries.RecordInviteKeyEventParams{
		InviteKeyID: uuid.NullUUID{UUID: inviteKeyID, Valid: true},
		GeneratedBy: generatedBy,
		Kind:        string(kind),
	}
	if userID != nil {
		arg.UserID = uuid.NullUUID{UUID: *userID, Valid: true}
	}
	return tx.RecordInviteKeyEvent(ctx, arg)
}

// Counts reads a member's own counters. Scans, signups and activations across EVERY key they have
// ever minted, including the keys that no longer exist -- which is what the denormalised
// `generated_by` on each event is for.
func (s *Invite) Counts(ctx context.Context, userID uuid.UUID) (EventCounts, error) {
	rows, err := s.queries.CountInviteKeyEventsForUser(ctx, userID)
	if err != nil {
		return EventCounts{}, err
	}
	return CountsFromRows(kindCounts(rows, func(r queries.CountInviteKeyEventsForUserRow) (string, int) {
		return r.Kind, r.Count
	})), nil
}

// CountsForKey reads one key's counters. A key that has been spent is gone, and its events are not:
// the rows survive with a nil `invite_key_id`, so this returns nothing for a deleted key while the
// member's own counters still hold what it did. That asymmetry is deliberate and it is the reason
// the member's view is the one the API serves.
func (s *Invite) CountsForKey(ctx context.Context, keyID uuid.UUID) (EventCounts, error) {
	rows, err := s.queries.CountInviteKeyEventsForKey(ctx, uuid.NullUUID{UUID: keyID, Valid: true})
	if err != nil {
		return EventCounts{}, err
	}
	return CountsFromRows(kindCounts(rows, func(r queries.CountInviteKeyEventsForKeyRow) (string, int) {
		return r.Kind, r.Count
	})), nil
}

// kindCounts projects sqlc's per-query row types onto the one shape CountsFromRows reasons about.
func kindCounts[T any](rows []T, project func(T) (string, int)) []KindCount {
	out := make([]KindCount, 0, len(rows))
	for _, row := range rows {
		kind, count := project(row)
		out = append(out, KindCount{Kind: kind, Count: count})
	}
	return out
}
