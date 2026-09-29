// Package webhook delivers event notifications to user-supplied URLs
// (SPEC §7.11, phase 3 step 3).
//
// The security requirements here are not the interesting part of a webhook
// feature and they are the part most likely to be skipped under time pressure,
// which is exactly why they are in a separate file with their own tests. A webhook
// that works and is not SSRF-safe is a remote code execution primitive for anyone
// who can register an endpoint, and it is found by somebody exploiting it rather
// than by a failing test.
package webhook

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// ErrUnsafeTarget is returned for a URL that points somewhere a webhook must not
// reach.
//
// A distinct type so a caller can tell "your URL is not allowed" from "your URL
// could not be parsed" and from "the delivery failed", because the three mean
// different things to whoever registered the endpoint and only the first is worth
// retrying with a different URL.
type ErrUnsafeTarget struct {
	// Host is the offending host, kept for the error message.
	Host string
	// Reason is a short machine-ish phrase -- "private address", "not http" --
	// so a test can assert on the REASON and not on prose.
	Reason string
}

func (e *ErrUnsafeTarget) Error() string {
	return fmt.Sprintf("webhook target %q rejected: %s", e.Host, e.Reason)
}

// ValidateTarget checks that a webhook URL is safe to POST to.
//
// FOUR SEPARATE CHECKS, and each one closes a hole the previous one does not:
//
//  1. SCHEME. http or https only. `file://`, `gopher://`, `dict://` and the rest
//     are not "webhooks pointing somewhere private" -- they are protocol handlers
//     that read local files or speak arbitrary protocols, and a client that
//     follows them is the vulnerability rather than the target.
//  2. HOST IS PRESENT and not a bare local name. `http://localhost:8080` parses
//     fine and is the single most common SSRF target there is.
//  3. EVERY RESOLVED IP is public. This is the one that is easy to get wrong,
//     and the wrong version of it is checking only the LITERAL host -- see
//     ResolveAndCheck for why that is not enough.
//  4. NO USERINFO. `http://expected.example.com@127.0.0.1/` is a request to
//     127.0.0.1, and every naive host parser reads "expected.example.com" out of
//     it. This is a parser-confusion bug that lives in the URL grammar, not in
//     the network, so no amount of IP checking catches it.
//
// Ports are NOT restricted. A webhook to a non-standard port is a legitimate thing
// for someone to want, and the IP check is what actually matters; restricting ports
// would stop a real use case while blocking nothing an attacker wants.
func ValidateTarget(raw string, resolved []net.IP) error {
	u, err := url.Parse(raw)
	if err != nil {
		return &ErrUnsafeTarget{Host: raw, Reason: "not a valid URL"}
	}

	// 1. Scheme.
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	case "":
		return &ErrUnsafeTarget{Host: raw, Reason: "no scheme"}
	default:
		return &ErrUnsafeTarget{Host: raw, Reason: "scheme must be http or https"}
	}

	// 4. Userinfo, checked BEFORE the host is read, because the whole point is
	// that the host a naive reader sees is not the host that will be contacted.
	if u.User != nil {
		return &ErrUnsafeTarget{Host: u.Host, Reason: "userinfo is not allowed"}
	}

	host := u.Hostname()
	if host == "" {
		return &ErrUnsafeTarget{Host: u.Host, Reason: "no host"}
	}

	// 2. Bare local names. Checked as a NAME, before resolution, so
	// "localhost" is rejected even in an environment where DNS would happily
	// resolve it -- which is every environment, because /etc/hosts has it.
	if isLocalName(host) {
		return &ErrUnsafeTarget{Host: host, Reason: "local host name"}
	}

	// 3. Every resolved address, not the first one. A name with two A records
	// where one is public and one is not is the DNS-rebinding case, and checking
	// only the first is checking the one the attacker chose to put first.
	if len(resolved) == 0 {
		return &ErrUnsafeTarget{Host: host, Reason: "host did not resolve"}
	}
	for _, ip := range resolved {
		if !isPublicIP(ip) {
			return &ErrUnsafeTarget{Host: host, Reason: "resolves to a non-public address"}
		}
	}

	return nil
}

// localNames are hostnames that always mean "this machine" or "this network".
//
// A closed list, and the reason it is a list rather than a suffix check is that a
// suffix check (`strings.HasSuffix(host, ".local")`) misses `localhost`,
// `localhost.localdomain`, and every name in /etc/hosts that an operator has ever
// added for an internal service. A closed list is incomplete in the same way, but
// the incomplete case FAILS CLOSED -- an unrecognised name goes to DNS, and the IP
// check on the result is what catches it.
var localNames = map[string]bool{
	"localhost":                true,
	"localhost.localdomain":    true,
	"ip6-localhost":            true,
	"ip6-loopback":             true,
	"broadcasthost":            true,
	"metadata":                 true, // GCP's metadata service
	"metadata.google.internal": true,
	"instance-data":            true, // AWS's EC2 metadata
}

// isLocalName reports whether a hostname is one of the always-local names.
func isLocalName(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	if localNames[h] {
		return true
	}
	// A trailing dot is the same host in DNS and a common way to slip past a
	// string comparison, so it is trimmed before every comparison here.
	return false
}

// isPublicIP reports whether an address is safe to send a webhook to.
//
// THIS IS THE FUNCTION THAT MATTERS, and it is longer than the rest of the file
// because "is it private" has more answers than people expect. Each range below is
// a case where a webhook to it is either an SSRF into the host or a
// cloud-credential theft:
//
//   - loopback: the instance itself, including any admin-only listener.
//   - link-local unicast: 169.254.169.254 is the CLOUD METADATA endpoint, and it
//     hands out instance credentials to anything that asks. This single address is
//     why webhook SSRF is treated as a critical finding rather than a nuisance.
//   - link-local multicast and interface-local: same 169.254/16 block.
//   - private ranges: anything on the instance's own VPC or LAN.
//   - unspecified (0.0.0.0, ::): routes to localhost on most stacks.
//   - multicast: not a webhook destination under any reading.
//   - CGNAT 100.64/10: shared address space that is NOT covered by RFC1918 and is
//     routinely used for internal service meshes. Missing this one is a common
//     bypass because it looks public and is not.
//   - IPv4-mapped IPv6: ::ffff:127.0.0.1 IS loopback, and a naive check on the
//     16-byte form misses it entirely. net.IP.IsLoopback handles the conversion,
//     which is why the mapped form is worth calling out rather than trusting a
//     prefix comparison.
//
// The zero value is REJECTED, not accepted. A nil IP means resolution failed or
// was never done, and "I could not tell" must not mean "probably fine" in a
// function whose job is to decide whether to open a socket.
func isPublicIP(ip net.IP) bool {
	// len() rather than `ip == nil`. A nil net.IP and an EMPTY net.IP are both
	// "no address", and the second one is what a zero-valued struct field or a
	// failed parse actually produces -- `ip == nil` is false for it, every
	// Is* method below returns false, and the value falls through to `return
	// true`. That is the worst possible failure: a failed resolution reading as
	// a PUBLIC one.
	if len(ip) == 0 {
		return false
	}
	if ip.IsUnspecified() {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() {
		return false
	}
	// CGNAT / shared address space (RFC 6598). Not covered by IsPrivate, and
	// routinely internal in cloud deployments.
	if v4 := ip.To4(); v4 != nil {
		if v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
			return false
		}
	}
	return true
}
