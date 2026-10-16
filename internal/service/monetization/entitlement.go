// How a credential becomes a tier: the entitlement, read from the credential itself (plan phase 15, slice a).
//
// THE CREDENTIAL IS THE ONLY THING THE RATE LIMITER HAS, and slice 13d's decision is why: the unit counted is
// what the caller presents -- the API key for the API surface, the feed's token for a feed URL, and the
// connecting address only when nothing is presented. That makes the entitlement question exactly one
// question: does the member this credential belongs to appear on the operator's comp list?
//
// AN API KEY IS A SIGNED JWT carrying its member's id (`internal/service/user/apikey.go`), so the answer needs
// no database read and no cache: parse the key, read the member id, look it up in a list the operator wrote in
// their configuration. TWO CONSEQUENCES, both deliberate:
//
//   - `user.GetUserIDFromAPIKey` is REUSED rather than reimplemented. A second JWT parse here would be a
//     second answer to "is this key valid", and the two would disagree the day the signing key changed.
//   - A credential that does not parse is a FREE caller, never an error. A limiter that failed closed on an
//     unparseable key would turn a typo into a 429, and the caller would have no way to tell why -- so an
//     anonymous address and a feed token are both simply members of no tier.
//
// WHAT THIS DELIBERATELY DOES NOT DO (slice b's job): read a stored grant. §18.3's grants (TL4 permanent,
// top-10 monthly, seasonal winners) are rows with a source and an end, and they arrive with the reward pass
// that writes them, together with the entitlement cache that a per-request read would need.
package monetization

import (
	"strings"

	"github.com/stashapp/stash-box/internal/service/user"
)

// memberOfCredential reads the member a credential belongs to, and reports whether it names one at all.
//
// The prefix is stripped rather than matched: `ratelimit.Credential` prefixes the header it found
// (`ApiKey:`, `X-Api-Key:`, `Authorization:`) and `ratelimit.FeedToken` prefixes a feed token with `feed:`,
// and none of those prefixes is part of the credential itself. What is left is handed to the API key parser,
// which decides -- an address, a feed token and a garbage string all simply fail to parse, which is the
// answer this needs.
func memberOfCredential(credential string) (string, bool) {
	value := credential
	if _, after, found := strings.Cut(credential, ":"); found {
		value = after
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}

	member, err := user.GetUserIDFromAPIKey(value)
	if err != nil || member == "" {
		return "", false
	}
	return member, true
}
