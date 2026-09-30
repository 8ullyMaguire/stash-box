//go:build integration

package api_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The peer registry's operator surface (D2 step 6).
//
// WHAT THIS FILE IS FOR. The R074 receiving guard -- ValidateBaseURL, called from
// federation.Service.Create and Update -- was proven by 21 mutations and called by
// nothing. Factory.Federation() existed but had no caller, because the GraphQL
// operator surface did not exist. These tests go through the real GraphQL
// handler, so the guard is on the path an operator's request actually takes, and
// the @hasRole directive is enforced by the same machinery that enforces it
// everywhere else.
//
// THE DECISIVE ASSERTION IS THE LAST ONE. An admin cannot register a peer
// pointing at 169.254.169.254. That is R074 stated as a user-visible outcome
// rather than as a unit test on a validator, and it is the test that fails if
// someone deletes the ValidateBaseURL call from Create and leaves every unit
// test green.
//
// A NOTE ON HOSTNAMES. ValidateBaseURL fails CLOSED when a host does not resolve,
// and the harness builds the real service factory, so these tests use LITERAL
// PUBLIC IPs rather than example.org. A literal needs no DNS, which makes the
// tests hermetic -- they assert the same thing on a laptop with no network as on
// a machine with one. Using a name here would have made the security cases pass
// or fail depending on the resolver, which is the worst property a security test
// can have.

func TestFederationPeerSurface(t *testing.T) {
	s := asAdmin(t)
	c := s.client

	t.Run("create, list, update, delete", func(t *testing.T) {
		created, err := c.createFederationPeer(federationPeerCreateInput{
			Name:       "peer-one",
			BaseURL:    "http://93.184.216.34:9999/graphql",
			InstanceID: "instance-one",
		})
		require.NoError(t, err, "creating a peer at a public address should succeed")
		require.NotNil(t, created)

		assert.Equal(t, "peer-one", created.Name)
		assert.Equal(t, "http://93.184.216.34:9999/graphql", created.BaseURL)
		assert.Equal(t, "instance-one", created.InstanceID)
		assert.True(t, created.Enabled, "a peer defaults to enabled")

		// The omitted trust weight must become 0.5, not 0. GraphQL cannot
		// distinguish an omitted Float from an explicit 0, and 0 violates the
		// schema CHECK (trust_weight > 0), so getting this wrong makes
		// peerCreate fail for every caller that omits the field.
		assert.Equal(t, 0.5, created.TrustWeight,
			"an omitted trust_weight must default to 0.5; 0 is illegal")

		// It appears in the list.
		peers, err := c.federationPeers()
		require.NoError(t, err)
		found := false
		for _, p := range peers {
			if p.ID == created.ID {
				found = true
				assert.Equal(t, created.BaseURL, p.BaseURL)
				assert.Nil(t, p.LastSeenAt,
					"a never-contacted peer has no lastSeenAt; it must be null, "+
						"not the zero time")
			}
		}
		assert.True(t, found, "the created peer did not appear in federationPeers")

		// Update re-validates and changes the row.
		updated, err := c.updateFederationPeer(federationPeerUpdateInput{
			ID:          created.ID,
			Name:        "peer-one-renamed",
			BaseURL:     "http://93.184.216.35:9999/graphql",
			TrustWeight: 0.75,
			Enabled:     false,
		})
		require.NoError(t, err)
		assert.Equal(t, "peer-one-renamed", updated.Name)
		assert.Equal(t, 0.75, updated.TrustWeight)
		assert.False(t, updated.Enabled)

		// Delete is idempotent from the operator's point of view: the state the
		// operator asked for is the state they have.
		ok, err := c.deleteFederationPeer(created.ID)
		require.NoError(t, err)
		assert.True(t, ok)

		// Deleting it AGAIN must also succeed, which is the assertion that
		// exercises the ErrNotFound branch. Service.Delete did not return
		// ErrNotFound at all before this -- the delete query is an sqlc :exec,
		// which discards the row count, so a missing peer and a removed one were
		// indistinguishable and this branch was dead code.
		ok, err = c.deleteFederationPeer(created.ID)
		require.NoError(t, err,
			"deleting an already-deleted peer errored; the state the operator "+
				"asked for is the state they have")
		assert.True(t, ok)

		peers, err = c.federationPeers()
		require.NoError(t, err)
		for _, p := range peers {
			assert.NotEqual(t, created.ID, p.ID, "the deleted peer is still listed")
		}
	})

	t.Run("an out-of-range trust weight is refused", func(t *testing.T) {
		// 0 violates the CHECK. The service rejects it before the insert rather
		// than letting Postgres refuse it, so the operator gets a message about
		// the field rather than a constraint violation.
		_, err := c.createFederationPeer(federationPeerCreateInput{
			Name:        "zero-weight",
			BaseURL:     "http://93.184.216.40:9999/graphql",
			InstanceID:  "instance-zero",
			TrustWeight: f64(0),
		})
		assert.Error(t, err, "a trust weight of 0 must be refused")
	})

	t.Run("the same instance id cannot be registered twice", func(t *testing.T) {
		// instance_id is UNIQUE, and that is load-bearing: asking one instance
		// twice double-counts its evidence, so a peer could buy double weight by
		// being entered twice.
		_, err := c.createFederationPeer(federationPeerCreateInput{
			Name:       "dup-a",
			BaseURL:    "http://93.184.216.41:9999/graphql",
			InstanceID: "instance-dup",
		})
		require.NoError(t, err)

		_, err = c.createFederationPeer(federationPeerCreateInput{
			Name:       "dup-b",
			BaseURL:    "http://93.184.216.42:9999/graphql",
			InstanceID: "instance-dup",
		})
		assert.Error(t, err, "a duplicate instance_id must be refused")
	})
}

// TestAdminCannotRegisterAnUnsafePeer is the R074 rule as an outcome.
//
// Every case is a host this box must never be made to dial on an operator's
// behalf. The first four are refused from the URL itself and need no DNS; the
// rebinding case is the one that requires resolution, so it is left to the
// federation package's own tests -- see TestValidateBaseURL there, which covers
// it with an injected resolver. Repeating it here would need DNS in a security
// test, which is the property worth avoiding.
func TestAdminCannotRegisterAnUnsafePeer(t *testing.T) {
	s := asAdmin(t)
	c := s.client

	cases := []struct {
		name string
		url  string
		why  string
	}{
		{"loopback", "http://127.0.0.1:9999/graphql", "the box itself"},
		{"link-local metadata", "http://169.254.169.254/latest/meta-data/", "cloud instance credentials"},
		{"private range", "http://10.0.0.5/graphql", "the instance's own LAN"},
		{"file scheme", "file:///etc/passwd", "a protocol handler that reads local files"},
		{"path traversal in url", "http://93.184.216.34/etc/passwd", "R074 rule 1: a local-file reference in the path"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := c.createFederationPeer(federationPeerCreateInput{
				Name:       "evil-" + tc.name,
				BaseURL:    tc.url,
				InstanceID: "instance-evil-" + tc.name,
			})
			require.Error(t, err,
				"the R074 guard let %q through (%s). An operator -- or anyone who "+
					"reached an admin session -- could make this box issue requests "+
					"to an address it must never dial.", tc.url, tc.why)
		})
	}

	// Nothing was written. The guard runs BEFORE the insert, so a refusal must
	// leave no row behind -- otherwise a rejected URL would still be a peer this
	// instance knows about.
	peers, err := c.federationPeers()
	require.NoError(t, err)
	for _, p := range peers {
		assert.NotContains(t, p.BaseURL, "169.254.169.254",
			"a refused peer was written anyway")
		assert.NotContains(t, p.BaseURL, "127.0.0.1",
			"a refused peer was written anyway")
	}
}

// TestUpdatingToAnUnsafeUrlIsRefused covers the second half of R074 rule 2.
//
// A guard that ran only on insert would be bypassed by editing the row, which is
// the cheapest possible way around it.
func TestUpdatingToAnUnsafeUrlIsRefused(t *testing.T) {
	s := asAdmin(t)
	c := s.client

	created, err := c.createFederationPeer(federationPeerCreateInput{
		Name:       "will-be-repointed",
		BaseURL:    "http://93.184.216.50:9999/graphql",
		InstanceID: "instance-repoint",
	})
	require.NoError(t, err)

	_, err = c.updateFederationPeer(federationPeerUpdateInput{
		ID:          created.ID,
		Name:        created.Name,
		BaseURL:     "http://169.254.169.254/latest/meta-data/",
		TrustWeight: 0.5,
		Enabled:     true,
	})
	require.Error(t, err,
		"an update repointed a peer at the metadata endpoint. The guard ran on "+
			"create and not on update, which is exactly the bypass it was meant "+
			"to prevent.")

	// The row is unchanged, not half-updated.
	peers, err := c.federationPeers()
	require.NoError(t, err)
	for _, p := range peers {
		if p.ID == created.ID {
			assert.Equal(t, "http://93.184.216.50:9999/graphql", p.BaseURL,
				"a refused update still changed the stored URL")
		}
	}
}

// TestPeerSurfaceRequiresAdmin pins the role requirement.
//
// The peer list is the set of hosts this box will dial, which is exactly what an
// attacker wants to know, so it must not be readable by an ordinary user.
func TestPeerSurfaceRequiresAdmin(t *testing.T) {
	read := asRead(t)

	_, err := read.client.federationPeers()
	assert.Error(t, err, "a READ-role user could list the peer registry")

	_, err = read.client.createFederationPeer(federationPeerCreateInput{
		Name:       "nope",
		BaseURL:    "http://93.184.216.60:9999/graphql",
		InstanceID: "instance-nope",
	})
	assert.Error(t, err, "a READ-role user could create a peer")
}

func f64(f float64) *float64 { return &f }
