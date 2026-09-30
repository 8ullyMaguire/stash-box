package api

import (
	"context"
	"errors"
	"time"

	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/service/federation"
)

// The operator surface for the peer registry (D2 step 6).
//
// THIS FILE IS WHAT MAKES THE R074 GUARD REACHABLE. Until something calls
// Factory.Federation(), the guard is on every path the federation package owns
// and on no path a request can take -- service.go's own header says so. These
// resolvers are that something.
//
// Note what is NOT here: a query for foreign candidates, and any mutation that
// turns a peer's answer into a local record. F2 makes foreign evidence evidence
// and never a vote, and an operator override would be the same hole with a
// smaller door. `federation.queryForeignCandidates` is still owed by step 6; it is
// read-only and does not depend on the write path, so it can land separately.

// FederationPeers returns every configured peer.
func (r *queryResolver) FederationPeers(ctx context.Context) ([]models.FederationPeer, error) {
	peers, err := r.services.Federation().List(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]models.FederationPeer, 0, len(peers))
	for _, p := range peers {
		out = append(out, *peerToModel(p))
	}
	return out, nil
}

// FederationPeerCreate registers a peer.
//
// The base URL is validated inside federation.Service.Create, BEFORE the insert.
// This resolver deliberately does not pre-validate: a second check here would be
// a second implementation of the same rule, and the two would drift.
func (r *mutationResolver) FederationPeerCreate(ctx context.Context, input models.FederationPeerCreateInput) (*models.FederationPeer, error) {
	// The schema makes trustWeight a Float, and GraphQL cannot distinguish an
	// OMITTED Float from an explicit 0 -- both arrive as nil on a plain Float but
	// this one arrives as a *float64 holding 0. That distinction is the whole
	// reason the input type uses a pointer.
	//
	// It matters because 0 is ILLEGAL: the schema CHECK is trust_weight > 0, and
	// the service rejects it. An omitted weight must therefore become 0.5, and an
	// explicit 0 must be passed through so the caller is told it is illegal --
	// silently upgrading 0 to 0.5 would mean an operator who typed 0 got a peer
	// they did not ask for and no error explaining why.
	weight := 0.5
	if input.TrustWeight != nil {
		weight = *input.TrustWeight
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}

	peer, err := r.services.Federation().Create(ctx, federation.CreateInput{
		Name:        input.Name,
		BaseURL:     input.BaseURL,
		InstanceID:  input.InstanceID,
		TrustWeight: weight,
		Enabled:     enabled,
	})
	if err != nil {
		return nil, err
	}
	return peerToModel(peer), nil
}

// FederationPeerUpdate updates a peer.
//
// There is no instance_id on the input, on purpose. It is the peer's identity
// and unique in the schema, so letting it change would let one instance take
// over another's row -- trust weight and accumulated evidence included.
func (r *mutationResolver) FederationPeerUpdate(ctx context.Context, input models.FederationPeerUpdateInput) (*models.FederationPeer, error) {
	peer, err := r.services.Federation().Update(ctx, federation.UpdateInput{
		ID:          input.ID,
		Name:        input.Name,
		BaseURL:     input.BaseURL,
		TrustWeight: input.TrustWeight,
		Enabled:     input.Enabled,
	})
	if err != nil {
		return nil, err
	}
	return peerToModel(peer), nil
}

// FederationPeerDelete removes a peer.
func (r *mutationResolver) FederationPeerDelete(ctx context.Context, id uuid.UUID) (bool, error) {
	// A malformed id never reaches here: gqlgen's ID scalar rejects it during
	// unmarshalling, before the resolver is called. An earlier draft took a
	// string and parsed it here, which put a client-side bug in a position where
	// it would read as "no such peer" and the caller would retry forever.
	if err := r.services.Federation().Delete(ctx, id); err != nil {
		if errors.Is(err, federation.ErrNotFound) {
			// Already gone. Deleting is idempotent from the operator's point of
			// view: the state they asked for is the state they have.
			return true, nil
		}
		return false, err
	}
	return true, nil
}

// peerToModel converts a registry peer to its GraphQL shape.
//
// The field types are the schema's, not the registry's: ID is a uuid.UUID (the
// generated ID scalar unmarshals from the wire form) and LastSeenAt is a *string
// because DateTime is declared as a string scalar in this schema. That is why
// the formatting is explicit here rather than a straight struct copy -- a copy
// would compile only if the two types happened to line up, and they do not.
//
// A nil LastSeenAt is "never contacted" and stays nil, which GraphQL renders as
// null. Collapsing it to the zero time would render 0001-01-01 and read as a
// real, very old contact.
func peerToModel(p federation.Peer) *models.FederationPeer {
	m := &models.FederationPeer{
		ID:          p.ID,
		Name:        p.Name,
		BaseURL:     p.BaseURL,
		InstanceID:  p.InstanceID,
		TrustWeight: p.TrustWeight,
		Enabled:     p.Enabled,
	}

	if p.LastSeenAt != nil {
		s := p.LastSeenAt.UTC().Format(time.RFC3339)
		m.LastSeenAt = &s
	}

	return m
}
