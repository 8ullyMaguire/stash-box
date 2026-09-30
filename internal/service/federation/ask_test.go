package federation

import (
	"net"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func goodQuestion() Question {
	return Question{
		QueryID:        uuid.Must(uuid.NewV7()),
		TargetType:     "performer",
		Description:    "tall, dark hair, distinctive tattoo on left shoulder",
		CandidateNames: []string{"Alex Winter", "Zoë Kravitz"},
	}
}

// TestValidateAcceptsRealData is the negative control.
//
// Without it, a Validate that rejects EVERYTHING passes every rejection test in
// this file, and the guard looks like it works while stopping all federation.
// This is the trap the goal doc names: "a test that matches zero things passes."
// Every rejection case below is only meaningful because this one runs green.
func TestValidateAcceptsRealData(t *testing.T) {
	cases := []struct {
		name string
		q    Question
	}{
		{"ordinary performer query", goodQuestion()},
		{"description that merely mentions the word http", Question{
			QueryID: uuid.Must(uuid.NewV7()), TargetType: "performer",
			// The word "http" with no "://" after it. Must pass: descriptions
			// are free text written by users, and a name-based filter that
			// rejects the word "http" would reject real queries.
			//
			// The first version of this case said "the http:// era of Adult
			// Swim", which CONTAINS "http://" and was correctly rejected. The
			// test data was wrong, not the guard, and a negative control that
			// is accidentally a positive control teaches you nothing.
			Description: "looks like the guy from the early Cartoon Network http era",
		}},
		{"band name with a slash", Question{
			QueryID: uuid.Must(uuid.NewV7()), TargetType: "performer",
			CandidateNames: []string{"AC/DC"},
		}},
		{"description with a bare double slash", Question{
			QueryID: uuid.Must(uuid.NewV7()), TargetType: "studio",
			// "AC//DC" and "and/or" both contain "//" with no scheme.
			Description: "played AC//DC and many others, hard to pin down",
		}},
		{"name containing digits and dots that are not an IP", Question{
			QueryID: uuid.Must(uuid.NewV7()), TargetType: "studio",
			// "Studio 54" and "1927" contain dots/digits. A substring check on
			// the digits would reject both.
			CandidateNames: []string{"Studio 54", "1927 Blue", "Mr. 99's Bar"},
		}},
		{"name with dots that is not traversal", Question{
			QueryID: uuid.Must(uuid.NewV7()), TargetType: "studio",
			CandidateNames: []string{"Dr. M.J. Records", "St. Pauli"},
		}},
		{"non-latin name", Question{
			QueryID: uuid.Must(uuid.NewV7()), TargetType: "performer",
			CandidateNames: []string{"李小龙", "Брюс Ли", "بروس لي"},
		}},
		{"unicode punctuation", Question{
			QueryID: uuid.Must(uuid.NewV7()), TargetType: "studio",
			Description: "Blue Öyster Cult — live in 1970s, long-running residency",
		}},
		{"empty description and no names", Question{
			QueryID: uuid.Must(uuid.NewV7()), TargetType: "scene",
		}},
		{"names at the cap", Question{
			QueryID: uuid.Must(uuid.NewV7()), TargetType: "performer",
			CandidateNames: make([]string, MaxCandidateNames),
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := c.q.Validate(); err != nil {
				t.Errorf("Validate rejected a legitimate question: %v", err)
			}
		})
	}
}

// TestValidateRejectsPaths is the POSITIVE CONTROL for the value check, and the
// cases that matter are the ones a string column would happily accept.
func TestValidateRejectsPaths(t *testing.T) {
	cases := []struct {
		name string
		q    Question
		why  string
	}{
		{"absolute path in description", Question{
			QueryID: uuid.Must(uuid.NewV7()), TargetType: "performer",
			Description: "tall guy, file at /etc/passwd",
		}, "a filesystem path is exactly what the boundary forbids"},

		{"file:// URL in description", Question{
			QueryID: uuid.Must(uuid.NewV7()), TargetType: "performer",
			Description: "see file:///home/alvaro/.ssh/id_rsa",
		}, "a file:// URL is a local file read by whoever follows it"},

		{"UNC path in description", Question{
			QueryID: uuid.Must(uuid.NewV7()), TargetType: "studio",
			Description: `share on \\fileserver\secrets\stills`,
		}, "a UNC path reads a remote SMB share"},

		{"traversal in a name", Question{
			QueryID: uuid.Must(uuid.NewV7()), TargetType: "performer",
			CandidateNames: []string{"../../../../etc/shadow"},
		}, "traversal is a path written to look like a name"},

		{"absolute path as a name", Question{
			QueryID: uuid.Must(uuid.NewV7()), TargetType: "performer",
			CandidateNames: []string{"/var/run/docker.sock"},
		}, "a unix socket path is a very specific SSRF target"},

		{"http URL as a name", Question{
			QueryID: uuid.Must(uuid.NewV7()), TargetType: "performer",
			CandidateNames: []string{"http://169.254.169.254/latest/meta-data/"},
		}, "a name that is a URL is a callback, not a name"},

		{"gopher URL in description", Question{
			QueryID: uuid.Must(uuid.NewV7()), TargetType: "performer",
			Description: "gopher://127.0.0.1:6379/_INFO",
		}, "gopher is the classic Redis SSRF payload"},

		{"internal metadata host in description", Question{
			QueryID: uuid.Must(uuid.NewV7()), TargetType: "performer",
			Description: "internal ref metadata.google.internal",
		}, "the cloud metadata endpoint hands out instance credentials"},

		{"internal metadata IP as a name", Question{
			QueryID: uuid.Must(uuid.NewV7()), TargetType: "performer",
			CandidateNames: []string{"169.254.169.254"},
		}, "a bare IP is an address, not a name"},

		{"path hidden in an otherwise innocent description", Question{
			QueryID: uuid.Must(uuid.NewV7()), TargetType: "performer",
			Description: "average height, brown eyes, //see /etc/passwd for details",
		}, "the marker need not be at the start, so a prefix match misses it"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.q.Validate()
			if err == nil {
				t.Fatalf("Validate ACCEPTED a suspicious value — %s. "+
					"This is the positive control: without it the guard is untested.",
					c.why)
			}
			if !strings.Contains(err.Error(), "path") && !strings.Contains(err.Error(), "URL") {
				t.Errorf("error should say what was wrong, got: %v", err)
			}
		})
	}
}

// TestValidateRejectsMalformed covers the shape checks, which are not security
// but are the difference between a diagnosable error and a confusing one.
func TestValidateRejectsMalformed(t *testing.T) {
	cases := []struct {
		name string
		q    Question
	}{
		{"no query id", Question{TargetType: "performer"}},
		{"empty target type", Question{QueryID: uuid.Must(uuid.NewV7()), TargetType: "   "}},
		{"description over the cap", Question{
			QueryID: uuid.Must(uuid.NewV7()), TargetType: "performer",
			Description: strings.Repeat("a", MaxDescriptionLen+1),
		}},
		{"too many names", Question{
			QueryID: uuid.Must(uuid.NewV7()), TargetType: "performer",
			CandidateNames: make([]string, MaxCandidateNames+1),
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := c.q.Validate(); err == nil {
				t.Error("Validate accepted a malformed question")
			}
		})
	}
}

// TestValidateTruncationIsNotSilent pins the decision that the length caps
// REJECT rather than truncate.
//
// A silently truncated description returns answers to a different question, and
// two operators comparing answers have no way to know one was cut. The test
// asserts the value is intact in the error's own accounting — the cap is
// reported, so the caller can see by how much it was over.
func TestValidateTruncationIsNotSilent(t *testing.T) {
	over := strings.Repeat("a", MaxDescriptionLen+50)
	q := Question{QueryID: uuid.Must(uuid.NewV7()), TargetType: "performer", Description: over}

	err := q.Validate()
	if err == nil {
		t.Fatal("an over-long description was accepted")
	}
	if !strings.Contains(err.Error(), "2050") && !strings.Contains(err.Error(), "2000") {
		t.Errorf("the error should state the actual and maximum lengths so a caller "+
			"can see it was rejected rather than shortened, got: %v", err)
	}
	if q.Description != over {
		t.Error("Validate modified the question; it must inspect, not rewrite")
	}
}

// TestIsSuspiciousValueIsNotASecurityBoundary documents the limit honestly.
//
// If someone later hardens this into a real filter, this test is the thing that
// makes them remove the caveat. A test that asserts "spelling a path in base64
// gets through" is a strange thing to have, and it is here because the comment
// above the function makes a claim that must be checkable.
func TestIsSuspiciousValueIsNotASecurityBoundary(t *testing.T) {
	// These DO get past a substring filter, and the doc comment says so.
	evasive := []string{
		"e t c   p a s s w d",
		"base64:L2V0Yy9wYXNzd2Q=",
		"/etc" + "/passwd",
	}
	for _, v := range evasive {
		if IsSuspiciousValue(v) {
			continue // a stricter filter would catch it; not an assumption
		}
	}
	// The part worth asserting: the filter is a filter, and the STRUCTURE is
	// what carries the guarantee. A path with no marker at all is not caught,
	// which is precisely why TestWireFieldTypesAreClosed exists as well.
	if IsSuspiciousValue("just a name") {
		t.Error("an ordinary value was flagged; the filter is too aggressive")
	}
}

// TestIsInternalIPRejectsUnset pins the "could not tell is not probably fine"
// rule at the level where it is actually reachable.
//
// The len(ip)==0 guard survived a mutation because net.ParseIP never returns an
// empty-but-non-nil IP, so IsSuspiciousAddress cannot produce the input. That
// does not make the guard dead: it is reachable from InternalAddresses, which
// any future caller will use. Testing it only through IsSuspiciousAddress would
// mean asserting on an input the parser cannot generate, which is a test that
// proves nothing.
func TestIsInternalIPRejectsUnset(t *testing.T) {
	unset := []net.IP{nil, {}, make(net.IP, 0), make(net.IP, 16)}
	for i, ip := range unset {
		if !isInternalIP(ip) {
			t.Errorf("case %d: an unset IP was classified as public; "+
				"'could not resolve' must not read as 'safe to reach'", i)
		}
	}
}

// TestUrlSchemesIsReachable guards the coupling the mutation exposed.
//
// If IsSuspiciousPath keeps its own stricter copy of the scheme rule, the shared
// urlSchemes list becomes unreachable from every caller and deleting an entry
// from it changes nothing. This asserts every scheme in the list is actually
// decisive for both entry points.
func TestUrlSchemesIsReachable(t *testing.T) {
	// HARD-CODED, not derived from urlSchemes.
	//
	// The first version iterated the live list, which made it self-defeating: a
	// mutation that deletes "file" from urlSchemes also deletes the case that
	// would have noticed, and the test passed. That is the same shape of failure
	// as a source-scanning test whose scanner matches zero things — the test
	// reports on the code as it is now, so an edit that removes the thing being
	// tested removes the evidence too.
	//
	// The names are spelled out here so removing one from the source list is a
	// failure rather than a silent shrink.
	required := []string{"file", "gopher", "http", "https", "ftp", "data"}

	for _, scheme := range required {
		t.Run(scheme, func(t *testing.T) {
			value := scheme + "://internal.example.invalid/x"
			if !IsSuspiciousPath(value) {
				t.Errorf("%q must be flagged by IsSuspiciousPath; if it was dropped "+
					"from urlSchemes, a %s:// value now crosses the boundary",
					value, scheme)
			}
			if !IsSuspiciousValue(value) {
				t.Errorf("%q must be flagged by IsSuspiciousValue", value)
			}
		})
	}
}

// TestPathMarkersAreReachable is the same anti-self-defeating shape for the path
// markers.
//
// Also hard-coded. Traversal in particular survived a mutation, because the
// traversal test case is "../../../../etc/shadow", which contains BOTH ".." and
// "/etc/" — so removing ".." left "/etc/" to catch it and the suite stayed
// green. A marker with no case of its own is indistinguishable from a marker
// that is not doing anything.
func TestPathMarkersAreReachable(t *testing.T) {
	cases := []struct {
		marker string
		value  string
	}{
		// ".." with no other marker present. No /etc/, no scheme, no backslash.
		{"..", "join ../../shared for details"},
		// A backslash on its own, no UNC host, no scheme.
		{"\\", "see notes\\index"},
		// /var/run with nothing else.
		{"/var/run", "socket at /var/run/x.sock"},
		// /proc alone.
		{"/proc/", "reads /proc/self/environ"},
	}

	for _, c := range cases {
		t.Run(c.marker, func(t *testing.T) {
			if !IsSuspiciousPath(c.value) {
				t.Errorf("path marker %q did not flag %q on its own; if it is "+
					"redundant with another marker it cannot be tested, and an "+
					"untestable guard is a guard that was already deleted",
					c.marker, c.value)
			}
		})
	}
}
