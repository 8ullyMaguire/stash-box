package federation

import (
	"fmt"
	"net/url"
	"strings"
)

// R074 rule 3: images.url is the one address-shaped column this box SERVES.
//
// Why it is treated separately from the other four stored-URL columns, and why it
// is not ValidateBaseURL:
//
//   - The other four (performer_urls, studio_urls, scene_urls, sites) hold
//     OPERATOR-TYPED INBOUND REFERENCES. A human is transcribing a URL they found
//     somewhere, so a value that looks odd is usually a typo rather than an
//     attack, and refusing it breaks their data. The scanner reports them.
//
//   - images.url is handed to a client. A client that renders it is given whatever
//     is in the column, and one that fetches it makes the instance's browser or
//     server retrieve it. A stored file:// or an internal path there is not a
//     metadata leak, it is an active primitive aimed at whoever views the image.
//
//   - And it is NOT the peer rule. ValidateBaseURL resolves in DNS because a peer
//     is a host this box dials. An image URL is a host a CLIENT fetches: this box
//     never connects to it, so a hostname that does not resolve from here may
//     resolve perfectly from a user's machine. Refusing those would reject
//     legitimate rows -- and would be the guard being wrong in the direction that
//     looks safe.
//
// So the rule is about SHAPE, not reachability: the scheme, and whether the value
// is a local-file reference.

// allowedImageSchemes are the two schemes an image URL may use.
//
// data: is deliberately absent. A data: URL carries its own bytes, so it is not a
// fetch of anything internal, but it is unbounded, attacker-controlled content in
// a column a renderer will interpret -- and the size limit that would normally
// bound it lives on the upload path, not here.
var allowedImageSchemes = map[string]bool{
	"http":  true,
	"https": true,
}

// ValidateImageURL rejects an image URL that is not a plain http(s) reference.
//
// The local-file check is pathMarkers ONLY, and that narrowness is deliberate.
//
// It is tempting to reuse IsSuspiciousValue here -- it is the existing predicate
// for "does this value look like it carries a local reference" and sharing one
// list is better than two. It is also WRONG for this column, and the test caught
// it on the first run: IsSuspiciousValue is the F1 question-text predicate, so
// besides paths it rejects ANY value containing "http://" and any internal
// hostname. An image url is a url; using it made ValidateImageURL refuse
// https://cdn.example.com/pic.jpg and every other ordinary image on the column.
//
// So the marker list is shared -- one list, one place to update -- while the
// PREDICATE is not. pathMarkers is the filesystem half, and only that half
// applies to a value whose whole purpose is to be a url.
func ValidateImageURL(raw string) error {
	// NO EMPTY OR RELATIVE CHECK, and their absence is measured rather than
	// assumed. An earlier draft had three guards here and two mutations survived:
	// deleting the empty check and deleting the relative check left the suite
	// green, because url.Parse gives both an empty scheme and the scheme check
	// below refuses it. "/etc/passwd" and "//host/x" also carry path or host
	// markers and are refused downstream.
	//
	// So those two branches were DEAD -- redundant with the scheme allowlist and
	// the marker scan -- and the rule this project keeps relearning applies: a
	// surviving mutation means the guarded line is dead or redundant, so DELETE
	// it rather than write a test to pin it. Three guards for one job is three
	// places to update and two of them prove nothing.
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("not a valid url: %w", err)
	}

	scheme := strings.ToLower(u.Scheme)
	if !allowedImageSchemes[scheme] {
		// Naming the schemes matters: an operator who typed "ftp://" needs to know
		// what would have worked, not just that something was wrong.
		return fmt.Errorf("scheme %q is not allowed; use http or https", u.Scheme)
	}

	if u.Hostname() == "" {
		return fmt.Errorf("no host")
	}

	// The filesystem markers, over the WHOLE raw value rather than just the path.
	// The path is where they normally appear, but a query or fragment can carry
	// one too ("http://host/i?src=/etc/passwd"), and a client that hands the value
	// to a fetcher may use either.
	if hasPathMarker(raw) {
		return fmt.Errorf("looks like a local file reference")
	}

	return nil
}

// hasPathMarker reports whether a value contains a filesystem path marker.
//
// Case-folded, because the markers are lower-case and a value may not be:
// "HTTP://host/ETC/PASSWD" is the same reference as the lower-case form, and a
// check that only matched one case would be trivially bypassed by the other.
func hasPathMarker(s string) bool {
	lowered := strings.ToLower(s)
	for _, marker := range pathMarkers {
		if strings.Contains(lowered, marker) {
			return true
		}
	}
	return false
}
