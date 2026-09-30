package federation

import (
	"context"
	"net"
	"strings"
	"testing"
)

// DialGuard is R074 rule 2's second half: the check on the way OUT.
//
// The write-time guard cannot cover this, and the reason is the whole reason
// this function exists. A hostname that resolved to a public address when the
// peer row was written can resolve to 127.0.0.1 by the time we dial, because a
// DNS record's TTL has nothing to do with when an operator registered the peer.
// An attacker who controls a name with a short TTL walks straight past a guard
// that ran only at insert.
//
// So these tests use a resolver that returns a PUBLIC address first and a PRIVATE
// one second. That ordering is the attack: a name that looks fine at
// registration and points inward afterwards.

// rebindingResolver answers with a fixed address list, EXCEPT for a host that
// is already a literal IP address -- which it answers with itself.
//
// That exception is not a convenience, it is the point. ValidateBaseURL resolves
// u.Hostname() and judges whatever comes back, so a fake that returned one fixed
// public address for every host made http://127.0.0.1 resolve to a public address
// and the guard allow it. Real DNS does not behave that way: a literal address is
// not looked up, it IS the answer. A resolver fake that ignores the host is a
// fixture that quietly disables the rule under test, and it did exactly that here
// -- three subtests passed a loopback, link-local and private-range URL.
type rebindingResolver struct {
	ips   []net.IP
	calls int
}

func (r *rebindingResolver) LookupIP(ctx context.Context, host string) ([]net.IP, error) {
	r.calls++

	// A literal address resolves to itself, which is what makes the
	// literal-IP cases meaningful: the guard must reject them on the ADDRESS,
	// not on anything about the name.
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}

	return r.ips, nil
}

// TestDialGuardClosesTheRebindingWindow is the positive control for rule 2.
//
// Without a test that a dial-time check happens, "we validate on write" reads as
// coverage and is not: the write-time check cannot see this case, because by
// definition the row was written when the name still resolved outward.
func TestDialGuardClosesTheRebindingWindow(t *testing.T) {
	// The attack: a name that was public at write time and is private now.
	ips := []net.IP{net.ParseIP("93.184.216.34"), net.ParseIP("127.0.0.1")}
	res := &rebindingResolver{ips: ips}

	err := dialGuard(context.Background(), Peer{
		InstanceID: "rebind",
		BaseURL:    "https://peer.example.org",
	}, res)

	if err == nil {
		t.Fatal("DialGuard accepted a name that resolves to 127.0.0.1.\n" +
			"This is the rebinding hole: the row was written when the name pointed " +
			"outward, and every address it resolves to NOW must be checked, not the " +
			"first one.")
	}

	// The resolver must have been consulted. Asserting only on the error cannot
	// distinguish a refusal from some unrelated failure, which is the same
	// mistake that made a fail-closed test pass for the wrong reason.
	if res.calls == 0 {
		t.Error("DialGuard refused without resolving anything, so the check that " +
			"matters never ran")
	}
}

// TestDialGuardRefusesTheObviousUnsafeHosts covers the cases that need no DNS at
// all, because a literal address is judged from the URL itself.
func TestDialGuardRefusesTheObviousUnsafeHosts(t *testing.T) {
	cases := []struct {
		name string
		url  string
		why  string
	}{
		{"loopback", "http://127.0.0.1:9999", "the box itself"},
		{"link-local metadata", "http://169.254.169.254/latest/meta-data/", "cloud credentials"},
		{"private range", "http://10.0.0.5", "the instance's own LAN"},
		{"file scheme", "file:///etc/passwd", "a handler that reads local files"},
		{"path in url", "http://peer.example.org/etc/passwd", "R074 rule 1"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Answers a name with a public address, so a refusal of a LITERAL
			// address can only come from the address itself.
			res := &rebindingResolver{ips: []net.IP{net.ParseIP("93.184.216.34")}}

			err := dialGuard(context.Background(), Peer{
				InstanceID: "evil",
				BaseURL:    c.url,
			}, res)

			if err == nil {
				t.Errorf("DialGuard accepted %q (%s)", c.url, c.why)
			}
		})
	}
}

// TestDialGuardAllowsAPublicPeer is the other direction.
//
// A guard that refuses everything is not a guard, it is an outage, and it would
// pass every test above. This is the control that stops the check being too
// strict: a peer at a public address, asked over https, is allowed.
func TestDialGuardAllowsAPublicPeer(t *testing.T) {
	res := &rebindingResolver{ips: []net.IP{net.ParseIP("93.184.216.34")}}

	err := dialGuard(context.Background(), Peer{
		InstanceID: "good",
		BaseURL:    "https://peer.example.org",
	}, res)

	if err != nil {
		t.Errorf("DialGuard refused a legitimate public peer: %v\n"+
			"A guard that refuses everything passes every refusal test above and "+
			"breaks federation entirely", err)
	}
	if res.calls == 0 {
		t.Error("the allowed case never resolved, so it was not actually checked " +
			"against anything")
	}
}

// TestDialGuardRefusesAPeerWithNoURL covers the degenerate input.
func TestDialGuardRefusesAPeerWithNoURL(t *testing.T) {
	// The nil resolver is safe here on purpose: an empty URL is refused from the
	// URL itself, before any resolution, so this cannot pass because resolution
	// happened to fail. That is also why the missing-URL guard is checked BEFORE
	// the resolver is consulted -- an ordering change would turn this into a
	// test of ErrNoResolver.
	err := DialGuard(context.Background(), Peer{InstanceID: "no-url"})
	if err == nil {
		t.Fatal("DialGuard accepted a peer with no base url")
	}
	if !strings.Contains(err.Error(), "no base url") {
		t.Errorf("the error did not name the missing url: %v", err)
	}
}
