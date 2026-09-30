package federation

import (
	"context"
	"fmt"
	"time"

	"github.com/gofrs/uuid"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/stashapp/stash-box/internal/queries"
)

// Store writes a peer's answers and reads them back.
//
// F2 IS ENFORCED HERE, STRUCTURALLY. This type holds only a
// *queries.Queries, which exposes every query in the codebase — so "the store
// cannot write to identification_candidates" is not true of the object, only of
// this file. What IS true, and what the tests assert, is:
//
//   - the only INSERT this file performs is CreateForeignCandidate, and that
//     query targets identification_foreign_candidates;
//   - there is no method here that calls identification.Service.Suggest,
//     Vote or Resolve, because this package does not import that package;
//   - the evidence carries the LOCAL query id and the LOCAL peer row id, and
//     nothing derived from a remote value decides where it lands.
//
// The design note worth keeping: this file deliberately has no method that
// takes a local candidate id. Every such method is a place where a remote id
// could later be passed by mistake, and the way to remove that class of bug is
// to make the signature unable to express it.

// StoredCandidate is one row of identification_foreign_candidates, as this
// package sees it.
//
// A distinct type rather than the sqlc row type, so the fields the rest of the
// federation can read are named explicitly. The sqlc row also carries query_id
// and peer_id, which nothing outside a store call needs, and exposing them
// invites a caller to treat a foreign row as if it were a local one.
type StoredCandidate struct {
	// RemoteEntityID is the PEER's id. Never resolvable locally, and there is
	// deliberately no local id on this type at all.
	RemoteEntityID string
	// RemoteEntityName is the peer's name for it.
	RemoteEntityName string
	// RemoteVoteCount is how many of the PEER's users suggested it. An
	// observation about another instance's users, never a vote here and never
	// summed into any local score.
	RemoteVoteCount int
	// PeerID is our own row id for the peer that said this, so an operator can
	// see who claimed it and disable that peer.
	PeerID uuid.UUID
	// FetchedAt is when we last heard it. Evidence expires (F3): a peer's
	// answer is about the moment it was given, not forever.
	FetchedAt time.Time
}

// Store persists foreign evidence.
type Store struct {
	queries *queries.Queries
}

// NewStore returns a Store over the given queries handle.
func NewStore(q *queries.Queries) *Store {
	return &Store{queries: q}
}

// Record stores one peer's answer to one of our queries.
//
// The peer is identified by OUR row id, never by a value the peer supplied.
// Answer.PeerInstanceID is the peer's self-declaration; mapping it to a local
// row is the registry's job (peer.go), and doing it here would mean trusting a
// remote string to select which local row to write.
func (s *Store) Record(ctx context.Context, queryID, peerID uuid.UUID, entityType string, answers []RemoteCandidate) (int, error) {
	if queryID == uuid.Nil {
		return 0, fmt.Errorf("foreign evidence needs a local query id")
	}
	if peerID == uuid.Nil {
		return 0, fmt.Errorf("foreign evidence needs a local peer id")
	}
	if entityType == "" {
		return 0, fmt.Errorf("foreign evidence needs an entity type")
	}

	stored := 0
	for _, c := range answers {
		// An empty remote id would make the UNIQUE constraint collide across
		// peers for the same query and silently collapse distinct answers into
		// one row. Rejecting is better: a peer that answers with no id has
		// told us nothing we can store.
		if c.PeerID == "" {
			continue
		}
		if c.Name == "" {
			continue
		}
		_, err := s.queries.CreateForeignCandidate(ctx, queries.CreateForeignCandidateParams{
			QueryID:          queryID,
			PeerID:           peerID,
			EntityType:       entityType,
			RemoteEntityID:   c.PeerID,
			RemoteEntityName: c.Name,
			RemoteVoteCount:  c.SuggesterCount,
		})
		if err != nil {
			return stored, fmt.Errorf("recording foreign candidate %q from peer %s: %w",
				c.PeerID, peerID, err)
		}
		stored++
	}
	return stored, nil
}

// List returns the evidence recorded for one query.
func (s *Store) List(ctx context.Context, queryID uuid.UUID) ([]StoredCandidate, error) {
	rows, err := s.queries.ListForeignCandidatesByQuery(ctx, queryID)
	if err != nil {
		return nil, fmt.Errorf("listing foreign candidates for query %s: %w", queryID, err)
	}
	out := make([]StoredCandidate, 0, len(rows))
	for i := range rows {
		r := rows[i]
		out = append(out, StoredCandidate{
			RemoteEntityID:   r.RemoteEntityID,
			RemoteEntityName: r.RemoteEntityName,
			RemoteVoteCount:  int(r.RemoteVoteCount),
			PeerID:           r.PeerID,
			FetchedAt:        fetchedAt(r.FetchedAt),
		})
	}
	return out, nil
}

// fetchedAt unwraps a nullable timestamptz.
//
// A pgtype.Timestamptz with Valid=false is the zero time, not a usable
// timestamp, and the column is NOT NULL so in practice it is always valid —
// unwrapping anyway keeps the conversion in one place rather than assuming a
// schema constraint the compiler cannot see.
func fetchedAt(ts pgtype.Timestamptz) time.Time {
	if !ts.Valid {
		return time.Time{}
	}
	return ts.Time
}

// ForgetQuery drops every piece of evidence for a query.
//
// Needed on the path where a query is resolved or abandoned: F3 says a peer's
// answer is scoped to one query, and a resolved query whose foreign evidence
// outlives it is evidence attached to a question nobody is asking any more.
func (s *Store) ForgetQuery(ctx context.Context, queryID uuid.UUID) error {
	if err := s.queries.DeleteForeignCandidatesByQuery(ctx, queryID); err != nil {
		return fmt.Errorf("forgetting foreign candidates for query %s: %w", queryID, err)
	}
	return nil
}

// ForgetPeer drops every piece of evidence from one peer.
//
// The operator action: disabling a peer should also remove what it told us, so
// its claims stop being visible on queries it is no longer trusted for.
func (s *Store) ForgetPeer(ctx context.Context, peerID uuid.UUID) error {
	if err := s.queries.DeleteForeignCandidatesByPeer(ctx, peerID); err != nil {
		return fmt.Errorf("forgetting foreign candidates from peer %s: %w", peerID, err)
	}
	return nil
}

// Count returns how many peers answered a query with evidence.
//
// A count of DISTINCT peers rather than a count of rows, because the number an
// operator wants is "how many instances agreed", and counting rows would make a
// peer that volunteers twenty candidates look like twenty corroborating sources.
func (s *Store) Count(ctx context.Context, queryID uuid.UUID) (int, error) {
	rows, err := s.queries.ListForeignCandidatesByQuery(ctx, queryID)
	if err != nil {
		return 0, fmt.Errorf("counting foreign candidates for query %s: %w", queryID, err)
	}
	peers := make(map[uuid.UUID]struct{}, len(rows))
	for i := range rows {
		peers[rows[i].PeerID] = struct{}{}
	}
	return len(peers), nil
}
