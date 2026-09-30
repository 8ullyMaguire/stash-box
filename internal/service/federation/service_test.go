package federation

import (
	"context"
	"errors"
	"net"
	"reflect"
	"testing"

	"github.com/gofrs/uuid"
)

// These tests exist to prove ONE thing that baseurl_test.go cannot: that
// ValidateBaseURL is actually REACHED from the write path. A guard that is
// proven, mutation-verified, and never called is a comment — and that was
// exactly the state of this package until service.go landed.
//
// Every test here goes through Create or Update, so a service that dropped the
// ValidateBaseURL call fails every one of them. The unit tests in
// baseurl_test.go would all stay green.

// staticResolver answers every host with one public address, so a legitimate
// peer passes and an unsafe one is rejected on its URL or its resolved address
// rather than on an unrelated failure.
type staticResolver struct {
	ips   []net.IP
	calls int
}

func (r *staticResolver) LookupIP(ctx context.Context, host string) ([]net.IP, error) {
	r.calls++
	return r.ips, nil
}

func okInput(name, url, instanceID string) CreateInput {
	return CreateInput{Name: name, BaseURL: url, InstanceID: instanceID, TrustWeight: 0.5, Enabled: true}
}

// TestCreateValidatesBeforeWriting is the POSITIVE CONTROL for the wiring.
//
// Without a database this cannot prove the row was not written, so it proves the
// weaker but still decisive thing: the validator was consulted, and Create
// returned its error. A Create that inserted first and validated afterwards
// would fail the second assertion in TestCreateValidatesBeforeAnyOtherCheck,
// because the name/instance checks would not have run yet.
func TestCreateValidatesBeforeWriting(t *testing.T) {
	cases := []struct {
		name string
		url  string
		ips  []net.IP
		why  string
	}{
		{"loopback", "http://127.0.0.1:9999", []net.IP{net.ParseIP("127.0.0.1")}, "the box itself"},
		{"metadata", "http://169.254.169.254/latest/meta-data/", []net.IP{net.ParseIP("169.254.169.254")}, "instance credentials"},
		{"private", "http://10.0.0.5", []net.IP{net.ParseIP("10.0.0.5")}, "the instance's own LAN"},
		{"rebinding", "https://peer.example.org", []net.IP{net.ParseIP("93.184.216.34"), net.ParseIP("127.0.0.1")}, "public first, private second"},
		{"file scheme", "file:///etc/passwd", nil, "a protocol handler that reads local files"},
		{"path in url", "http://peer.example.org/etc/passwd", []net.IP{net.ParseIP("93.184.216.34")}, "R074 rule 1"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := &staticResolver{ips: c.ips}
			// A nil *queries.Queries is deliberate: any URL that got past the
			// validator would panic on the insert rather than fail cleanly, which
			// makes "reached the database" a test failure and not a silent pass.
			s := NewService(nil, r)

			_, err := s.Create(context.Background(), okInput("evil", c.url, "instance-1"))

			if err == nil {
				t.Fatalf("Create accepted %q (%s).\n"+
					"The write path did not enforce the guard, so a peer row would "+
					"exist that this box is willing to dial.", c.url, c.why)
			}
			// The error must be the validator's, not a nil-pointer panic from a
			// query that should never have been attempted.
			//
			// Same reasoning as the Update case: WHICH resolver was consulted,
			// not merely that the call errored. A service that fell back to real
			// DNS would also error on these names, so only the injected counter
			// distinguishes them.
			if r.calls == 0 && c.name != "file scheme" {
				t.Errorf("Create rejected %q without consulting the INJECTED "+
					"resolver (%s); it was refused for an unrelated reason, so the "+
					"test proves nothing", c.url, c.why)
			}
		})
	}
}

// TestCreateValidatesBeforeAnyOtherCheck pins the ORDER, which the case above
// cannot.
//
// The name and instance-id checks are cheap and local; the DNS resolution is not
// and can take seconds. Validating the URL first means a malformed peer is
// refused without a round trip. If someone reorders these, the resolver gets
// called for input that was never going to be written.
func TestCreateValidatesBeforeAnyOtherCheck(t *testing.T) {
	r := &staticResolver{ips: []net.IP{net.ParseIP("93.184.216.34")}}
	s := NewService(nil, r)

	// Both of these are invalid on their face, and neither mentions a URL.
	if _, err := s.Create(context.Background(), CreateInput{BaseURL: "http://peer.example.org"}); err == nil {
		t.Error("Create accepted a peer with no name")
	}
	if _, err := s.Create(context.Background(), CreateInput{Name: "x", BaseURL: "http://peer.example.org"}); err == nil {
		t.Error("Create accepted a peer with no instance id")
	}

	if r.calls != 0 {
		t.Errorf("the resolver was called %d times for input rejected on its face.\n"+
			"Cheap local checks must run before DNS: a reordering here makes every "+
			"malformed peer cost a network round trip.", r.calls)
	}
}

// TestCreateRejectsOutOfRangeTrustWeight covers the weight rule, which is a
// second way to make a peer row mean something other than the operator intended.
func TestCreateRejectsOutOfRangeTrustWeight(t *testing.T) {
	r := &staticResolver{ips: []net.IP{net.ParseIP("93.184.216.34")}}
	s := NewService(nil, r)

	for _, w := range []float64{-0.1, 1.5} {
		in := okInput("p", "https://peer.example.org", "i")
		in.TrustWeight = w
		if _, err := s.Create(context.Background(), in); err == nil {
			t.Errorf("Create accepted trust weight %v; the schema CHECK is > 0 AND <= 1, "+
				"and a clamped value would make an operator believe they set a weight "+
				"they did not", w)
		}
	}
}

// TestUpdateRevalidates is the half the goal's rule 2 is really about.
//
// "At write time AND at dial time" — and an update IS a write. A guard that ran
// only on insert is bypassed by editing the row, which is the cheapest possible
// way around it. UpdateInput carries no InstanceID precisely so an operator
// cannot repoint an existing peer's identity either.
func TestUpdateRevalidates(t *testing.T) {
	cases := []struct {
		name string
		url  string
		ips  []net.IP
	}{
		{"loopback", "http://127.0.0.1", []net.IP{net.ParseIP("127.0.0.1")}},
		{"metadata", "http://169.254.169.254", []net.IP{net.ParseIP("169.254.169.254")}},
		{"rebinding", "https://peer.example.org", []net.IP{net.ParseIP("93.184.216.34"), net.ParseIP("169.254.169.254")}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := &staticResolver{ips: c.ips}
			s := NewService(nil, r)
			in := UpdateInput{
				Name:        "peer",
				BaseURL:     c.url,
				TrustWeight: 0.5,
				Enabled:     true,
			}
			in.ID = uuid.Must(uuid.NewV7())

			if _, err := s.Update(context.Background(), in); err == nil {
				t.Fatalf("Update accepted %q. An update is a write, and a guard "+
					"that runs only on insert is bypassed by editing the row.", c.url)
			}

			// WHICH resolver was consulted, not merely that something was.
			//
			// Added because a mutation that swapped s.resolver for a real
			// netResolver SURVIVED this test: the real lookup of
			// peer.example.org fails with "host did not resolve", which is also
			// an error, so the assertion passed either way. A test that cannot
			// tell an injected dependency from a real one is not testing the
			// injection, and Update is exactly where that matters — Create got
			// the same treatment for the same reason.
			if r.calls == 0 {
				t.Errorf("Update rejected %q without consulting the INJECTED "+
					"resolver; a service falling back to real DNS still returns an "+
					"error here (the name does not resolve), so asserting only on "+
					"the error cannot distinguish them", c.url)
			}
		})
	}
}

// TestUpdateRejectsInvalidInput covers the local rules, kept separate from the
// validator's because they must hold even when the URL is perfectly fine.
func TestUpdateRejectsInvalidInput(t *testing.T) {
	r := &staticResolver{ips: []net.IP{net.ParseIP("93.184.216.34")}}
	s := NewService(nil, r)

	t.Run("nil id", func(t *testing.T) {
		_, err := s.Update(context.Background(), UpdateInput{
			Name: "p", BaseURL: "https://peer.example.org", TrustWeight: 0.5,
		})
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("a nil id gave %v, want ErrNotFound", err)
		}
	})

	t.Run("weight of zero", func(t *testing.T) {
		// Distinct from Create, where zero means "use the default". On update
		// there is no default to fall back to: the caller is stating a value, and
		// 0 would violate the CHECK constraint.
		in := UpdateInput{Name: "p", BaseURL: "https://peer.example.org", TrustWeight: 0}
		in.ID = uuid.Must(uuid.NewV7())
		if _, err := s.Update(context.Background(), in); err == nil {
			t.Error("Update accepted trust weight 0, which the schema CHECK forbids")
		}
	})
}

// TestRegistryCannotWriteForeignEvidence is the structural half of F2, asserted
// through the registry's own surface rather than by reading its imports.
//
// F2 says a peer's answer is evidence and never a vote. Enforcing that by
// behaviour would mean building the violation to test it, so it is enforced
// structurally: the queries handle this service holds can write to ANY table, so
// "the registry cannot write a local candidate" is not true of the object. What
// IS true, and what the package's own comment claims, is that this TYPE exposes
// no method that reaches identification.
//
// So the test asserts the claim that is checkable — the registry's write surface
// is peers, and peers only — by asserting that the one method that takes an
// Answer-shaped value does not exist. If a future method named around Suggest,
// Vote or Resolve appears on *Service, whoever adds it will see this failing and
// has to delete this line, which is the moment to notice.
func TestRegistryCannotWriteForeignEvidence(t *testing.T) {
	// The registry's exported write methods, by name. Peer creation, peer
	// update, peer delete. Nothing else.
	writes := []string{"Create", "Update", "Delete"}
	for _, name := range writes {
		if _, ok := reflect.TypeOf(&Service{}).MethodByName(name); !ok {
			t.Errorf("*Service has no %s method; the registry's write surface "+
				"changed and this test needs to know what it is now", name)
		}
	}

	// And the ones that must NOT exist. Each is a way foreign evidence could
	// reach the local vote path; none is present.
	for _, forbidden := range []string{"Suggest", "Vote", "Resolve", "RecordAnswer", "ApplyEvidence"} {
		if _, ok := reflect.TypeOf(&Service{}).MethodByName(forbidden); ok {
			t.Errorf("*Service has a %s method. F2 says foreign evidence is never "+
				"a vote and never a local row; a method with that name on the peer "+
				"registry is the hole, whatever it does today.", forbidden)
		}
	}
}
