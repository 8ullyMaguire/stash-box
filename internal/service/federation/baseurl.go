package federation

import (
	"context"
	"errors"
	"net"
	"net/url"

	"github.com/stashapp/stash-box/internal/webhook"
)

// This file is R074's RECEIVING half: the guard that stops a path, a hostname or
// an IP address from being ACQUIRED by this repo, rather than the guard that
// stops one from being sent out. The two are different problems and only one of
// them exists here.
//
// Why it exists. ALIGNMENT.md §3 says nothing identifying crosses a node
// boundary. The `stash` side enforces the SENDING half, in its exporter's path
// guard, with a positive control. That guard is real and it is useless against
// this hole: an exporter can be perfect and the commons can still acquire a
// path, a hostname or an IP from a peer, because nothing on this side of the
// wire inspects what arrives. A one-sided guard is half a guard.
//
// Why base_url specifically. Of the seven address-shaped columns in the schema,
// five are inbound references a human typed into a form, and two are addresses
// the box DIALS: webhook_endpoints.target_url (84_add_webhooks) and
// federation_peers.base_url (89_identification_federation). The dial columns
// are the worse of the two, because a path in one is not a leaked path in a
// metadata row — it is an SSRF primitive aimed at the box's own network. Of the
// two dial columns, target_url already has the correct guard
// (webhook.ValidateTargetURL) and base_url had NONE.
//
// D5: reuse webhook.ValidateTargetURL. Do not write a second
// resolve-then-check-every-address implementation. A second one is a second
// thing to keep in sync and a second thing to get subtly wrong, and the
// subtlety is the whole difficulty here — see the rebinding note on
// ValidateBaseURL below.

// Resolver resolves a hostname to the addresses it points at.
//
// Injected, and it is an interface rather than a concrete type because the
// rebinding case cannot be tested at all without it: proving that the guard
// checks EVERY resolved address rather than the first needs a resolver that
// returns two different addresses for one name, and net.Resolver cannot be
// talked into that without a live DNS server. A test with only the default
// resolver can assert the ordinary cases and nothing about the case that
// matters.
type Resolver interface {
	LookupIP(ctx context.Context, host string) ([]net.IP, error)
}

// netResolver adapts net.DefaultResolver to Resolver.
//
// The default resolver rather than a fresh net.Resolver because that is what
// the webhook validator uses, and matching it means the two guards see the same
// view of DNS. A federation guard that resolved differently from the webhook
// guard would be a third thing to keep in sync, which is what this file exists
// to avoid.
type netResolver struct{}

func (netResolver) LookupIP(ctx context.Context, host string) ([]net.IP, error) {
	return net.DefaultResolver.LookupIP(ctx, "ip", host)
}

// ErrNoResolver is returned when a caller needs DNS-backed validation and none
// was supplied.
//
// Its own value rather than a nil-pointer panic or a silent pass, because the
// failure mode of getting this wrong is a guard that does not run.
var ErrNoResolver = errors.New("federation: no resolver supplied for base_url validation")

// ValidateBaseURL checks that a peer's base URL is an address this box may
// dial, and that it is an address at all.
//
// Four checks, and the order matters because each one is a prerequisite for the
// next to be able to run:
//
//  1. PARSE. A string that is not a URL cannot be a peer, and every later check
//     needs a parsed URL.
//  2. SCHEME, HOST, USERINFO, LOCAL NAME. These are exactly webhook's checks 1,
//     2 and 4, delegated rather than repeated — D5. Reimplementing them here
//     would be a second place for `http://a@127.0.0.1/` to be half-handled.
//  3. EVERY RESOLVED ADDRESS IS PUBLIC. The check that is easy to get wrong, and
//     the one the whole file exists for.
//  4. NO PATH, NO IDENTIFYING SUFFIX. F1 on the receiving side: a peer's URL is
//     a location, not evidence about a person, so it must not be able to carry
//     a filesystem path or a local filename.
//
// The last check is the genuinely new rule, and it is why this is not just a
// call to webhook.ValidateTargetURL. base_url is not metadata an operator typed
// — it is a value that arrives from a peer and is stored. R074's rule 1 says a
// path must not become STORABLE in this repo's schema, and a peer row is a place
// it can become storable. A peer that answers with a base_url of
// `http://evil.example/etc/passwd` gets that string written to
// federation_peers.base_url verbatim, and the resulting row is exactly what
// ALIGNMENT.md §3 forbids.
func ValidateBaseURL(ctx context.Context, raw string, r Resolver) error {
	u, err := url.Parse(raw)
	if err != nil {
		return &webhook.ErrUnsafeTarget{Host: raw, Reason: "not a valid URL"}
	}

	// A base_url is a LOCATION, so it must have a host before anything can be
	// resolved. Checked here as well as inside ValidateTarget because
	// ValidateTarget's own host check is downstream of its scheme check, and a
	// peer URL with a scheme but no host ("http:///graphql") is a parse
	// success and a nonsense value.
	if u.Hostname() == "" {
		return &webhook.ErrUnsafeTarget{Host: raw, Reason: "no host"}
	}

	// Resolution is delegated, so the guard needs a resolver. Missing resolver is
	// an error rather than a skip: a guard that silently passes when it cannot
	// resolve is the "I could not tell, so probably fine" failure that
	// webhook.isPublicIP's own comment calls the worst possible failure.
	if r == nil {
		return ErrNoResolver
	}

	ips, err := r.LookupIP(ctx, u.Hostname())
	if err != nil {
		// DELIBERATELY NOT CHECKED HERE. A resolver error leaves `ips` empty,
		// and webhook.ValidateTarget rejects an empty slice itself
		// (`len(resolved) == 0` -> "host did not resolve"). This branch was
		// written, then mutated to `ips = nil`, and the suite stayed green:
		// the line was DEAD, because the delegated validator already covers
		// it. A surviving mutation means the guarded line is redundant, not
		// that a test is missing — so the duplicate check was deleted rather
		// than a test written to pin it. Re-adding it would be a second
		// implementation of a rule D5 says to have exactly one of, and it
		// would drift from webhook's the moment either changes.
		_ = err
	}

	// Delegated to webhook's rule set, with the resolved addresses supplied. All
	// four of webhook's checks then run against this URL: scheme, userinfo, bare
	// local name, and every-resolved-IP-is-public. D5, obeyed rather than
	// reimplemented.
	if err := webhook.ValidateTarget(raw, ips); err != nil {
		return err
	}

	// The R074-specific rule, applied last so it only ever sees a URL that has
	// already passed the address checks. Ordering it last means the operator's
	// error message names the address problem when the value has one, which is
	// the problem they need to fix first.
	if err := checkNoPathComponent(raw); err != nil {
		return err
	}
	return nil
}

// checkNoPathComponent rejects a base_url whose PATH or QUERY carries something
// that is not a location.
//
// This is the rule that is not in webhook.ValidateTarget, and the reason is
// semantic rather than accidental: a webhook target_url legitimately has a
// deep path (many webhook receivers live at /hooks/abc123/deep/path), and a
// federation base_url is the same shape. So "reject any path" would refuse a
// legitimate peer, and the false-positive cost is higher than the value.
//
// What is rejected instead is a path that carries an IDENTIFYING MARKER, which
// is exactly what ALIGNMENT.md §3 forbids and exactly what the sender-side
// guard on the stash side looks for. The markers come from
// IsSuspiciousValue, the same predicate ask.go already uses on the sending side
// — one definition of "this looks like a path" for both halves of the boundary,
// rather than two that drift.
func checkNoPathComponent(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return &webhook.ErrUnsafeTarget{Host: raw, Reason: "not a valid URL"}
	}
	// Path and query together, not path alone: a peer URL of
	// `http://host/?next=/etc/passwd` stores the path in the QUERY, and checking
	// the path only would let it through. The fragment is included because it is
	// sent to the peer as part of the request line too.
	for _, part := range []string{u.Path, u.RawQuery, u.Fragment} {
		if part == "" {
			continue
		}
		if IsSuspiciousValue(part) {
			return &webhook.ErrUnsafeTarget{
				Host:   u.Hostname(),
				Reason: "path carries a filesystem path or URL",
			}
		}
	}
	return nil
}