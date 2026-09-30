package federation

import (
	"context"
	"fmt"
)

// DialGuard is the R074 dial-time check, as a function the client calls.
//
// WHY THIS EXISTS AS A SEPARATE PIECE. R074 rule 2 wants the base URL validated
// on the way OUT as well as on the way in, and the way out is
// Client.askOne — which lives in client.go, an UNTRACKED file in the shared tree
// owned by another session. Writing the check there means editing a file that is
// not committed anywhere, so the work would be lost the moment that session
// rewrote it, and a second dialer built here to avoid touching theirs would be
// exactly the duplication D5 exists to prevent.
//
// So the rule lives here, in the same package as the write-time guard, and the
// client calls it. The two share ValidateBaseURL, so there is still exactly ONE
// implementation of the address rules; what is factored out is only the moment of
// the check.
//
// THE REBINDING WINDOW IS THE WHOLE POINT. The write-time guard cannot cover
// this: a hostname that resolved to a public address when the peer row was
// written can resolve to 127.0.0.1 by the time we dial, because a DNS record's
// TTL has nothing to do with when an operator registered the peer. An attacker
// with a short TTL walks straight past a guard that ran only at insert. Checking
// on the way out closes that window; it does not replace the write-time check,
// which is what stops the row existing at all.
//
// A nil peer is refused rather than skipped. A caller with no peer has made a
// programming error, and silently continuing would dial whatever zero value
// happens to be.
func DialGuard(ctx context.Context, p Peer) error {
	// A nil Resolver means the package's real one, which is what production wants.
	// It is passed as nil rather than constructed here so the one place that knows
	// how a live resolver is built stays inside baseurl.go, beside the write-time
	// guard that also needs it.
	return dialGuard(ctx, p, nil)
}

// dialGuard is the rule, with the resolver injected.
//
// It exists so the tests can drive THIS function rather than a reimplementation of
// it. An earlier draft had the tests call ValidateBaseURL directly through a local
// helper, and two mutations removing the call from DialGuard SURVIVED: the tests
// were exercising a copy, so the function the client actually calls was untested.
// A test of a copy proves the copy.
func dialGuard(ctx context.Context, p Peer, r Resolver) error {
	if p.BaseURL == "" {
		return fmt.Errorf("peer %s has no base url to dial", p.InstanceID)
	}

	if err := ValidateBaseURL(ctx, p.BaseURL, r); err != nil {
		return fmt.Errorf("peer %s refused: %w", p.InstanceID, err)
	}
	return nil
}
