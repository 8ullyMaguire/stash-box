package federation

import (
	"strings"
	"testing"
)

// R074 rule 3: images.url is the one address-shaped column this box SERVES.
//
// The rule is about SHAPE, not reachability, and these tests are written to keep
// that distinction honest. A guard for a client-fetched URL that resolved DNS
// would be wrong in the direction that LOOKS safe: it would refuse
// "http://cdn.internal.corp/pic.jpg" on a server with no route to that host, and
// the operator's legitimate image would be gone. So TestValidateImageURLAllowsA
// NonResolvingPublicHost exists to pin that down.

// TestValidateImageURLRefusesLocalFileReferences is the positive control.
//
// These are the values that make images.url dangerous rather than merely ugly: the
// box or a client acting for it ends up reading a local file, and the column is
// one a renderer is handed directly.
func TestValidateImageURLRefusesLocalFileReferences(t *testing.T) {
	cases := []struct {
		name string
		url  string
		why  string
	}{
		{"file scheme", "file:///etc/passwd", "a local file, read by whoever renders the image"},
		{"unc scheme", "\\\\host\\share\\pic.jpg", "a UNC path reaches a file share"},
		{"absolute unix path", "/etc/passwd", "not a URL at all, and it parses as one"},
		{"protocol relative", "//evil.example/pic.jpg", "no scheme, so no scheme check fires"},
		{"windows drive", `C:\Windows\win.ini`, "a local file on a Windows client"},
		{"traversal in path", "http://cdn.example.com/../../etc/shadow", "the path walks out of the host"},
		{"marker in query", "http://cdn.example.com/i?src=/etc/passwd", "a fetcher may use the query"},
		{"data scheme", "data:text/html;base64,PHNjcmlwdD4=", "unbounded attacker content in a rendered column"},
		{"javascript scheme", "javascript:alert(1)", "script, in a column a renderer interprets"},
		{"ftp scheme", "ftp://cdn.example.com/pic.jpg", "not a scheme a client will fetch an image over"},

		// The four below each killed a mutation that the table above did not
		// cover. They are here because "the guard has a branch" and "the branch
		// is tested" are different claims, and the first four cases made me
		// believe the second.
		{"uppercase marker", "http://cdn.example.com/ETC/PASSWD",
			"the marker check was case-sensitive, so this walked past it"},
		{"uppercase traversal", "http://cdn.example.com/A/B/../SECRET",
			"traversal is case-insensitive too"},
		{"no host", "http:///pic.jpg",
			"a scheme with no host parses cleanly and is not a URL"},
		{"scheme only", "https://",
			"same, with nothing after the scheme"},
		{"empty", "", "no url at all"},
		{"whitespace", "   ", "a blank value is not a url"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := ValidateImageURL(c.url); err == nil {
				t.Errorf("ValidateImageURL accepted %q (%s).\n"+
					"images.url is served to clients, so this value is an active "+
					"primitive rather than a metadata leak.", c.url, c.why)
			}
		})
	}
}

// TestValidateImageURLAllowsOrdinaryImages is the other direction, and it is not
// optional.
//
// A guard that refuses everything passes every test above and breaks image
// uploads entirely. These are the shapes a real instance stores every day.
func TestValidateImageURLAllowsOrdinaryImages(t *testing.T) {
	allowed := []string{
		"http://cdn.example.com/pic.jpg",
		"https://cdn.example.com/pic.jpg",
		"https://images.example.com/a/b/c.png?width=200",
		"https://cdn.example.com/pic.jpg#fragment",
		"https://user:pass@cdn.example.com/pic.jpg",
		"HTTPS://CDN.EXAMPLE.COM/PIC.JPG", // scheme and host are case-insensitive
		"https://cdn.example.com:8443/pic.jpg",
		// A name that does not resolve from the server. This is the case a
		// DNS-resolving guard would wrongly refuse: the box never fetches this,
		// the client's machine does.
		"http://cdn.internal.corp/pic.jpg",
		"http://192.0.2.10/pic.jpg", // TEST-NET-1, unroutable on purpose
	}

	for _, u := range allowed {
		t.Run(u, func(t *testing.T) {
			if err := ValidateImageURL(u); err != nil {
				t.Errorf("ValidateImageURL refused an ordinary image url %q: %v\n"+
					"This is the failure mode that matters for this column: it "+
					"destroys existing user data over a value that is not an attack.",
					u, err)
			}
		})
	}
}

// TestValidateImageURLRejectsEmpty pins the degenerate case.
func TestValidateImageURLRejectsEmpty(t *testing.T) {
	if err := ValidateImageURL(""); err == nil {
		t.Error("an empty image url was accepted")
	}
}

// TestValidateImageURLNamesTheSchemeInItsError is a small thing that matters to
// the only person who will read it.
//
// An operator who typed ftp:// needs to know what would have worked. "invalid
// url" sends them to the documentation; "scheme \"ftp\" is not allowed; use http
// or https" tells them the answer.
func TestValidateImageURLNamesTheSchemeInItsError(t *testing.T) {
	err := ValidateImageURL("ftp://cdn.example.com/pic.jpg")
	if err == nil {
		t.Fatal("expected ftp to be refused")
	}
	if !strings.Contains(err.Error(), "http") {
		t.Errorf("the error does not say which schemes are allowed: %v", err)
	}
}
