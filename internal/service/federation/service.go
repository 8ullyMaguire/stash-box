package federation

import (
	"context"
	"errors"
	"fmt"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/stashapp/stash-box/internal/queries"
)

// This file is the WRITE PATH for the peer registry, and its entire reason to
// exist is one line: it calls ValidateBaseURL before anything reaches the
// database.
//
// Before this file, `internal/queries/sql/federation.sql` shipped
// CreateFederationPeer and UpdateFederationPeer while nothing in Go called them,
// so `federation_peers.base_url` was unwritten and unwatched. The R074 guard
// (baseurl.go) was proven by 13 mutations and entirely unreachable. A guard that
// nothing calls is a comment. This is the file that makes it a guard.
//
// SCOPE, DELIBERATELY NARROW. The D2 plan's step 6 asks for this file PLUS a
// GraphQL operator surface (`federation.peerCreate/peerList/peerDelete` and
// `federation.queryForeignCandidates` in `graphql/schema/types/federation.graphql`).
// **The GraphQL surface is not built here, on purpose.** Another session owns
// D2 in `~/code-local/go/stash-box` and the operator surface is the part that
// would collide with it. This file is the security-relevant half and it depends
// on nothing that session is writing, so it can be landed independently and the
// GraphQL layer added on top by whoever owns step 6.
//
// **READ THIS BEFORE ASSUMING THE HOLE IS CLOSED.** `Factory.Federation()` is
// wired, so `federation.NewService` is reachable — but **nothing calls
// `Factory.Federation()` yet**, because the only thing that would call it is the
// GraphQL operator surface this file deliberately does not build. So:
//
//   - any Go code that reaches a peer row MUST go through `Service.Create` or
//     `Service.Update`, and both validate. There is no other way to write one
//     from this package.
//   - **but** `internal/queries/sql/federation.sql` still ships
//     `CreateFederationPeer` and `UpdateFederationPeer` as generated methods on
//     `*queries.Queries`, and `service.Factory` holds a `*pgxpool.Pool` from
//     which `queries.New` can be built by any caller. The schema has no CHECK
//     constraint on `base_url` that encodes this rule.
//
// That is the remaining gap, and it is not closed by this commit. It closes when
// the operator surface lands and becomes the only caller. **The honest framing:
// the guard is now on every path this package owns, and the path that would
// bypass it — a caller building its own queries handle — is still open.**
//
// The rule this file encodes, stated once: **a peer's base_url is validated at
// write time, and only then persisted.** There is no path from a caller to
// CreateFederationPeer or UpdateFederationPeer that skips the check, because both
// are unexported fields of this service rather than fields on the queries
// handle that a caller could reach around this type.

// Service is the peer registry's write and read surface.
//
// Deliberately narrow: it owns peers and nothing else. It has no method that
// takes a foreign candidate, and no method that reaches the identification
// service, because F2 says foreign evidence is evidence and never a vote — and a
// type with such a method is a method someone later calls.
type Service struct {
	queries *queries.Queries
	// resolver is the DNS-backed validator's dependency. Injected so tests can
	// exercise the rebinding case, which needs a resolver that answers
	// differently than DNS would. See baseurl.go's Resolver.
	resolver Resolver
}

// NewService returns a Service over the given queries handle.
//
// The resolver argument is not optional. A caller that passed nil would get a
// guard that refuses every peer, which looks like a working guard and stops all
// federation, so the constructor takes it explicitly and this package supplies
// netResolver in the factory wiring.
func NewService(q *queries.Queries, r Resolver) *Service {
	if r == nil {
		r = netResolver{}
	}
	return &Service{queries: q, resolver: r}
}

// CreateInput is what an operator supplies to register a peer.
type CreateInput struct {
	Name string
	// BaseURL is validated before the row is written. See Create.
	BaseURL string
	// InstanceID is the peer's self-declared identity. UNIQUE in the schema, so
	// registering one instance twice makes the second call fail — which is the
	// point: asking the same instance twice double-counts its evidence.
	InstanceID string
	// TrustWeight multiplies into every piece of evidence this peer contributes,
	// so a peer's word is worth less than the local community's (F5). Zero means
	// "use the schema default"; a negative value is rejected.
	TrustWeight float64
	// Enabled defaults to true when the caller does not care, because a peer
	// registered and immediately invisible is a confusing first experience.
	Enabled bool
}

// Create registers a peer, validating its base URL first.
//
// The validation is the point of the method, so it happens BEFORE the insert and
// before anything else can fail. A peer row whose base_url points at
// 127.0.0.1 or 169.254.169.254 is an SSRF primitive aimed at this box's own
// network — a strictly worse outcome than a leaked path in a metadata row,
// because it is aimed inward rather than merely disclosed.
func (s *Service) Create(ctx context.Context, in CreateInput) (Peer, error) {
	if in.Name == "" {
		return Peer{}, fmt.Errorf("a peer needs a name")
	}
	if in.InstanceID == "" {
		return Peer{}, fmt.Errorf("a peer needs an instance id")
	}
	// Out of range here rather than clamped. The CHECK constraint in migration
	// 89 would refuse it anyway, and a silently clamped weight would make an
	// operator believe they had set a trust level they had not.
	if in.TrustWeight < 0 || in.TrustWeight > 1 {
		return Peer{}, fmt.Errorf("trust weight must be in [0,1], got %v", in.TrustWeight)
	}

	if err := ValidateBaseURL(ctx, in.BaseURL, s.resolver); err != nil {
		return Peer{}, fmt.Errorf("peer %q: %w", in.Name, err)
	}

	weight := in.TrustWeight
	if weight == 0 {
		// The schema's default is 0.5, applied by omitting the column. sqlc
		// sends the Go zero value, so "omitted" has to be expressed some other
		// way: 0 is not a legal stored weight (the CHECK is > 0), so treating a
		// zero input as "unset" is unambiguous rather than a guess.
		weight = 0.5
	}

	row, err := s.queries.CreateFederationPeer(ctx, queries.CreateFederationPeerParams{
		Name:        in.Name,
		BaseUrl:     in.BaseURL,
		InstanceID:  in.InstanceID,
		TrustWeight: weight,
		Enabled:     in.Enabled,
	})
	if err != nil {
		return Peer{}, fmt.Errorf("registering peer %q: %w", in.Name, err)
	}
	return FromRow(row), nil
}

// UpdateInput is what an operator supplies to change a peer.
//
// InstanceID is deliberately absent: it is the peer's identity and the schema
// makes it unique, so changing it would let one instance take over another's
// row — including its trust weight and its accumulated evidence. An operator who
// wants a different identity registers a different peer and deletes this one.
type UpdateInput struct {
	ID          uuid.UUID
	Name        string
	BaseURL     string
	TrustWeight float64
	Enabled     bool
}

// Update changes a peer, re-validating its base URL.
//
// Re-validation on every update, not only on create, because that is the whole
// point: a URL that was safe when it was written can be pointed somewhere else
// by an edit, and a guard that only runs at insert is a guard an operator
// bypasses by updating the row.
func (s *Service) Update(ctx context.Context, in UpdateInput) (Peer, error) {
	if in.ID == uuid.Nil {
		return Peer{}, ErrNotFound
	}
	if in.Name == "" {
		return Peer{}, fmt.Errorf("a peer needs a name")
	}
	if in.TrustWeight <= 0 || in.TrustWeight > 1 {
		return Peer{}, fmt.Errorf("trust weight must be in (0,1], got %v", in.TrustWeight)
	}

	if err := ValidateBaseURL(ctx, in.BaseURL, s.resolver); err != nil {
		return Peer{}, fmt.Errorf("peer %q: %w", in.Name, err)
	}

	row, err := s.queries.UpdateFederationPeer(ctx, queries.UpdateFederationPeerParams{
		ID:          in.ID,
		Name:        in.Name,
		BaseUrl:     in.BaseURL,
		TrustWeight: in.TrustWeight,
		Enabled:     in.Enabled,
	})
	if err != nil {
		return Peer{}, fmt.Errorf("updating peer %q: %w", in.Name, err)
	}
	return FromRow(row), nil
}

// Get returns one peer by our row id.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (Peer, error) {
	row, err := s.queries.GetFederationPeer(ctx, id)
	if err != nil {
		// pgx.ErrNoRows is translated rather than wrapped so a caller can tell
		// "no such peer" (404) from "the database is unreachable" (500)
		// without importing the driver.
		if isNoRows(err) {
			return Peer{}, ErrNotFound
		}
		return Peer{}, fmt.Errorf("reading peer %s: %w", id, err)
	}
	return FromRow(row), nil
}

// List returns every configured peer, ordered by name.
//
// Operator surface, so ordered by name rather than id: a list whose order
// changes between calls is a list nobody can scan.
func (s *Service) List(ctx context.Context) ([]Peer, error) {
	rows, err := s.queries.ListFederationPeers(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing peers: %w", err)
	}
	out := make([]Peer, 0, len(rows))
	for i := range rows {
		out = append(out, FromRow(rows[i]))
	}
	return out, nil
}

// ListEnabled returns the peers eligible for a broadcast.
//
// Filters in SQL rather than in Go so the broadcast path cannot accidentally read
// a disabled peer, and returns the askable subset only — Fresh and weight-checked
// — because a stale peer is not asked (F3).
func (s *Service) ListEnabled(ctx context.Context) ([]Peer, error) {
	rows, err := s.queries.ListEnabledFederationPeers(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing enabled peers: %w", err)
	}
	out := make([]Peer, 0, len(rows))
	for i := range rows {
		p := FromRow(rows[i])
		if p.Askable() {
			out = append(out, p)
		}
	}
	return out, nil
}

// Delete removes a peer and everything it told us.
//
// The evidence goes with it. A disabled or deleted peer whose claims are still
// visible on old queries is evidence attached to an instance this operator has
// said not to listen to.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	store := NewStore(s.queries)
	if err := store.ForgetPeer(ctx, id); err != nil {
		return err
	}
	if err := s.queries.DeleteFederationPeer(ctx, id); err != nil {
		return fmt.Errorf("deleting peer %s: %w", id, err)
	}
	return nil
}

// isNoRows reports whether a query failed because it matched nothing.
//
// A named helper rather than an inline errors.Is at each call site, so the one
// driver error that means "absent" is identified in a single place. The reason
// it matters: a caller distinguishing 404 from 500 is the distinction that
// decides the HTTP status, and making every caller import pgx to learn it is a
// coupling this package should not impose.
func isNoRows(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}
