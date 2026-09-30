package federation

import (
	"fmt"
	"net"
	"strings"

	"github.com/google/uuid"
)

// F1 has two halves and the second is the one that gets skipped.
//
// The first half is structural: no field is a `[]byte`, an `any`, or a struct
// with a URL in it, and wire_test.go reflects over the field kinds to prove it.
// That half is cheap and it is not sufficient, because every field in Question
// is a `string` — and a string happily holds "/etc/passwd",
// "http://169.254.169.254/latest/meta-data/", or "\\\\host\\share".
//
// So the second half is a VALUE check on the way out. It is applied to every
// string field of a Question before it leaves the process, because the boundary
// is the only place where "this is a name" and "this is a path" can still be
// told apart by the sender. On the receiving side they cannot: a peer that
// answers "just trust this name, it's /etc/passwd" has produced a legal Answer.

// urlSchemes are the schemes that make a value an ACTION rather than a name.
//
// Checked as scheme + "://" rather than as a bare "://" or a bare "http",
// because both of those fire on ordinary text: a performer description
// legitimately mentions "the http:// era of Adult Swim", and a studio name
// legitimately contains "//". The negative control in ask_test.go fails if this
// is loosened back to a substring match, and that is the point of having one.
//
// `file` and `gopher` are the two that matter most -- the first reads a local
// file, the second is the classic Redis SSRF payload -- but the set is closed
// rather than "any scheme" because a peer that receives "gopher://" and knows
// how to speak it has already lost, and a value with a scheme in it is never a
// name.
var urlSchemes = []string{
	"file", "gopher", "ftp", "dict", "ldap", "tftp", "jar", "netdoc",
	"http", "https", "ssh", "smb", "data", "php", "expect", "sftp",
}

// internalHostnames are names that always mean "an address inside this
// infrastructure" and are never a performer's name.
//
// Separate from the IP check because a hostname has nothing to parse — DNS
// would resolve it, and resolving it is exactly what must not happen inside a
// validation function. The positive control caught this gap: a description
// naming "metadata.google.internal" passed every other check, because it has no
// scheme, no slash, no "..", and is not a literal IP.
//
// GCP and AWS spell theirs differently, so the list is not just one name. It is
// a closed list because an unrecognised internal name is not the attack — the
// attack is a specific set of endpoints that hand out credentials.
var internalHostnames = []string{
	"metadata.google.internal",
	"metadata.goog",
	"instance-data",
	"instance-data.ec2.internal",
	"169.254.169.254", // also covered by the IP check; harmless to repeat
	"metadata",
}

// IsSuspiciousValue reports whether a value looks like a path, a URL, or an
// internal address rather than a name or a description.
//
// WHAT THIS IS NOT. It is not a security boundary on its own, and it would be a
// mistake to describe it as one. Someone who wants to smuggle a path through can
// spell it "etc passwd" or base64 it, and this will not catch that. What it does
// is make the common cases fail loudly at the boundary, and — the part that
// actually matters — give the positive control in the test suite something to
// assert on. Without it, "the payload cannot carry content" is a claim about
// field types that stops at the first person who puts a string in a string field.
//
// The false-positive cost is real and is why this is not applied to
// CandidateNames. A performer named "AC/DC" or a studio called "Blue Öyster
// Cult" must still cross the boundary, so a name is checked for the path and
// URL markers only, and a description additionally for internal hostnames.
func IsSuspiciousValue(s string) bool {
	lowered := strings.ToLower(s)
	for _, marker := range pathMarkers {
		if strings.Contains(lowered, marker) {
			return true
		}
	}
	for _, scheme := range urlSchemes {
		if strings.Contains(lowered, scheme+"://") {
			return true
		}
	}
	if IsSuspiciousAddress(lowered) {
		return true
	}
	for _, hostname := range internalHostnames {
		if strings.Contains(lowered, hostname) {
			return true
		}
	}
	return false
}

// pathMarkers are substrings that make a value look like a filesystem path.
//
// Separated from urlSchemes so the two failure modes can be tested apart: a
// backslash is a UNC path and never legitimate in a name, while a scheme is
// only suspicious when followed by "://".
var pathMarkers = []string{
	"..",    // traversal, in either separator style
	"\\",    // UNC path, and a Windows separator inside a POSIX path
	"/etc/", // the specific file every scanner's positive control uses
	"/proc/",
	"/var/run",
	"/home/", // a home directory is a path and leaks the username
	"/root/",
}

// IsSuspiciousAddress reports whether a value contains a literal private,
// loopback, link-local or otherwise internal IP address.
//
// Added because the positive control caught the gap: a bare "169.254.169.254"
// as a candidate name contains no scheme, no slash and no ".." marker, so it
// passed every other check. It is an address, not a name, and an address in a
// question is a callback for the peer to make.
//
// Checked by PARSING rather than by substring, because a substring check on the
// digits would reject a performer described as "born in 1984" or a studio called
// "Studio 54" — the false-positive cost of the cheap version is higher than the
// cost of a few extra lines here.
//
// 169.254.169.254 is the one that matters: it is the cloud instance metadata
// endpoint on every major provider, it hands out instance credentials to
// anything that asks, and it is reachable from the box itself.
func IsSuspiciousAddress(s string) bool {
	// A cheap pre-filter. Every candidate IP has a dot, and text that mentions
	// no dot at all cannot contain one.
	if !strings.Contains(s, ".") {
		return false
	}
	for _, field := range strings.FieldsFunc(s, func(r rune) bool {
		return !(r == '.' || r == ':' || (r >= '0' && r <= '9'))
	}) {
		ip := net.ParseIP(field)
		if ip == nil {
			continue
		}
		if isInternalIP(ip) {
			return true
		}
	}
	return false
}

// isInternalIP mirrors the range list in webhook.isPublicIP.
//
// DUPLICATED DELIBERATELY, and this is the one place a reviewer should push
// back. The two lists answer the same question — "is this address somewhere the
// box must not reach?" — and having two answers is how one of them falls behind.
//
// The alternative is federation importing internal/webhook for a five-line
// predicate, which couples the peer registry to the webhook feature's release
// cycle and makes a package that has nothing to do with notifications a
// dependency of the federation. Extracting a shared internal/netguard package is
// the correct fix, and it is deferred rather than done here because it touches
// webhook's tested behaviour and the webhook side of the split is owned by
// another profile's contract (HANDOFF-SPLIT.md D5 records the same decision).
// Until that happens, this comment is the warning: if you change one list, change
// both, and the test TestInternalIPMatchesWebhookList exists to make the drift
// fail.
func isInternalIP(ip net.IP) bool {
	if len(ip) == 0 {
		// "Could not tell" must never mean "probably fine".
		return true
	}
	if ip.IsUnspecified() || ip.IsLoopback() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return true
	}
	// CGNAT / shared address space (RFC 6598). Not covered by IsPrivate, and
	// routinely internal in cloud deployments.
	if v4 := ip.To4(); v4 != nil {
		if v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
			return true
		}
	}
	return false
}

// Validate checks a question before it is sent.
//
// Called by Broadcast, not by the peer: the guard has to run on the SENDING
// side, because the sender is the only party that still knows which field a
// value arrived in and whether the operator typed it.
//
// Three checks, and each rejects rather than repairs. Silently dropping a
// suspicious candidate would produce a question that looks clean and returns
// answers to a different question; refusing to send it produces an error the
// caller can log.
func (q Question) Validate() error {
	if q.QueryID == uuid.Nil {
		return fmt.Errorf("a question needs a query id")
	}
	if strings.TrimSpace(q.TargetType) == "" {
		return fmt.Errorf("a question needs a target type")
	}
	if len(q.Description) > MaxDescriptionLen {
		return fmt.Errorf("description is %d characters, the maximum is %d",
			len(q.Description), MaxDescriptionLen)
	}
	if len(q.CandidateNames) > MaxCandidateNames {
		return fmt.Errorf("question carries %d candidate names, the maximum is %d",
			len(q.CandidateNames), MaxCandidateNames)
	}
	// The description is free text, so it is where a value that is not a name
	// is most likely to be pasted in by a user following a link. It is checked
	// for every marker.
	if IsSuspiciousValue(q.Description) {
		return fmt.Errorf("the description contains what looks like a path, URL or " +
			"internal address; nothing identifying crosses a node boundary")
	}
	// Names are checked for path and URL markers only — no internal hostnames —
	// because "Blue Öyster Cult" and "AC/DC" are real names and must pass.
	for _, name := range q.CandidateNames {
		if IsSuspiciousPath(name) || IsSuspiciousAddress(name) {
			return fmt.Errorf("candidate name %q contains what looks like a path, URL or "+
				"address; names cross the boundary, paths and addresses do not", name)
		}
	}
	return nil
}

// IsSuspiciousPath is the narrower check used for names: path and URL markers,
// without the internal-hostname entries.
//
// Split out rather than parameterised because the two callers have genuinely
// different tolerances, and a single function with a boolean argument would let
// a caller pass the permissive flag on the description path by accident.
func IsSuspiciousPath(s string) bool {
	lowered := strings.ToLower(s)
	for _, marker := range pathMarkers {
		if strings.Contains(lowered, marker) {
			return true
		}
	}
	// The scheme list, NOT a bare "://" check. This function used to contain its
	// own `"://"` substring test, and that was worse than duplication: a bare
	// "://" also fires on "AC//DC" and on "the http:// era", so it rejected
	// legitimate names, AND it made the shared urlSchemes list unreachable from
	// every caller — deleting "file" from that list then changed nothing
	// observable, which is exactly the kind of dead guard this project's own
	// notes keep warning about. Two copies of one rule, one of them stricter,
	// is the worst shape: the stricter one wins and the other looks load-bearing.
	//
	// The negative control in ask_test.go ("AC/DC", "Studio 54") is what keeps
	// this honest, and TestUrlSchemesIsReachable asserts the two stay coupled.
	for _, scheme := range urlSchemes {
		if strings.Contains(lowered, scheme+"://") {
			return true
		}
	}
	return false
}

// InternalAddresses returns whether each address is internal, exposed so the
// drift test can compare this list against webhook's.
//
// Exported for the test and nothing else. A public predicate is not a security
// boundary.
func InternalAddresses(ips []net.IP) []bool {
	out := make([]bool, len(ips))
	for i, ip := range ips {
		out[i] = isInternalIP(ip)
	}
	return out
}
