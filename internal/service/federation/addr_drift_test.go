package federation

import (
	"net"
	"testing"

	"github.com/stashapp/stash-box/internal/webhook"
)

// TestInternalIPMatchesWebhookList is the drift test for the ONE thing
// federation and webhook both need and neither owns.
//
// ask.go duplicates webhook's range list because federation importing webhook
// would couple the peer registry to the notification feature's release cycle,
// and inlining the list is the alternative. The duplication is the documented
// compromise; this test is what stops it silently rotting. If someone extends
// webhook's list and forgets this one, an address the webhook service refuses to
// POST to would be accepted inside a broadcast question, and the failure would
// be an SSRF nobody connects to this file.
//
// The list is deliberately NOT the same set the query tests use. This one exists
// to compare two implementations against each other, so it covers every range
// either of them cares about: the ordinary private ranges, the cloud metadata
// address, CGNAT, the unspecified address, multicast, and the IPv4-mapped IPv6
// form that a naive prefix comparison misses.
func TestInternalIPMatchesWebhookList(t *testing.T) {
	cases := []struct {
		addr string
		why  string
	}{
		{"127.0.0.1", "loopback v4"},
		{"127.10.20.30", "loopback v4, not just .0.1"},
		{"::1", "loopback v6"},
		{"0.0.0.0", "unspecified: routes to localhost on most stacks"},
		{"::", "unspecified v6"},
		{"10.0.0.1", "RFC1918"},
		{"172.16.0.1", "RFC1918"},
		{"192.168.1.1", "RFC1918"},
		{"169.254.169.254", "CLOUD INSTANCE METADATA — the one that matters most"},
		{"169.254.1.1", "link-local, not only the metadata address"},
		{"fe80::1", "link-local v6"},
		{"100.64.0.1", "CGNAT RFC 6598 — not covered by IsPrivate"},
		{"100.127.255.255", "CGNAT upper bound"},
		{"224.0.0.1", "multicast"},
		{"::ffff:127.0.0.1", "IPv4-mapped v6 that IS loopback"},
		{"255.255.255.255", "limited broadcast"},
	}

	for _, c := range cases {
		t.Run(c.addr, func(t *testing.T) {
			ip := net.ParseIP(c.addr)
			if ip == nil {
				t.Fatalf("test data is not an address: %q", c.addr)
			}

			// The two implementations, asked the same question. webhook's
			// predicate is unexported, so the comparison goes through the
			// exported entry point: ValidateTarget rejects an unsafe target, so
			// "webhook calls this unsafe" is `ValidateTarget(...) != nil` with
			// a URL built around the address.
			fed := InternalAddresses([]net.IP{ip})[0]

			// Rebuilding a URL around the IP is what makes this a fair
			// comparison: ValidateTarget checks the scheme and userinfo first,
			// and both are fine here, so the only thing that can reject it is
			// the resolved-address check.
			url := "http://" + c.addr + "/"
			webhookCallsUnsafe := webhook.ValidateTarget(url, []net.IP{ip}) != nil

			if fed != webhookCallsUnsafe {
				t.Errorf("DRIFT: federation and webhook disagree about %s (%s).\n"+
					"  federation.isInternalIP says internal=%v\n"+
					"  webhook.ValidateTarget     says unsafe  =%v\n"+
					"Both answer 'is this an address the box must not reach?' and must "+
					"give the same answer. Update both lists, or extract a shared "+
					"internal/netguard package.",
					c.addr, c.why, fed, webhookCallsUnsafe)
			}
		})
	}
}

// TestFederationAcceptsPublicAddresses is the other half: the two lists must not
// BOTH be wrong in the strict direction, which would look like agreement while
// breaking every real peer address.
func TestFederationAcceptsPublicAddresses(t *testing.T) {
	public := []string{
		"8.8.8.8",
		"1.1.1.1",
		"93.184.216.34",
		"2606:4700:4700::1111",
	}
	for _, addr := range public {
		t.Run(addr, func(t *testing.T) {
			ip := net.ParseIP(addr)
			if InternalAddresses([]net.IP{ip})[0] {
				t.Errorf("%s is a public address but was classified internal; a guard "+
					"that refuses every peer address looks like a working guard while "+
					"stopping all federation", addr)
			}
		})
	}
}
