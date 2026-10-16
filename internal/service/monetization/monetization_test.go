package monetization

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/stashapp/stash-box/internal/config"
	"github.com/stashapp/stash-box/internal/ratelimit"
	"github.com/stashapp/stash-box/internal/service/user"
)

// monetizationForTest sets the switch, the streams, the comp list and the premium limit for one test, and
// restores all of them afterwards.
//
// THE RESTORE IS NOT TIDINESS. config.C is a package global and this package's every decision reads it, so a
// test that left the switch on would let the refusal tests pass for the wrong reason -- and one that left a
// comp list behind would let the entitlement tests pass for somebody else's state.
func monetizationForTest(t *testing.T, enabled bool, streams, members []string, premium ratelimit.Config) {
	t.Helper()
	previousEnabled, previousStreams := config.C.MonetizationEnabled, config.C.MonetizationStreams
	previousMembers := config.C.MonetizationPremiumMembers
	previousPerMinute, previousBurst := config.C.MonetizationPremiumRequestsPerMinute, config.C.MonetizationPremiumBurst
	t.Cleanup(func() {
		config.C.MonetizationEnabled = previousEnabled
		config.C.MonetizationStreams = previousStreams
		config.C.MonetizationPremiumMembers = previousMembers
		config.C.MonetizationPremiumRequestsPerMinute = previousPerMinute
		config.C.MonetizationPremiumBurst = previousBurst
	})

	config.C.MonetizationEnabled = enabled
	config.C.MonetizationStreams = streams
	config.C.MonetizationPremiumMembers = members
	config.C.MonetizationPremiumRequestsPerMinute = premium.RequestsPerMinute
	config.C.MonetizationPremiumBurst = premium.Burst
}

// premiumLimit returns a premium limit strictly above the free one this test process is configured with, so a
// test about something else does not fail the "must be higher" rule by accident.
func premiumLimit() ratelimit.Config {
	free := config.GetRateLimit()
	return ratelimit.Config{RequestsPerMinute: free.RequestsPerMinute * 2, Burst: free.Burst * 2}
}

// THE SHIPPED STATE IS ASSERTED IN internal/config/monetization_test.go, where the shipped default is
// reachable: the config type is unexported, so the test that reads it lives with it -- and "this instance
// charges for nothing unless an operator says otherwise" is a property of the default, not of a comment.

// A STREAM CANNOT BE ON WHILE THE SWITCH IS OFF, in three places that have to agree: the postures this package
// reports, the refusal a caller gets, and the startup validation. The disagreement this prevents is the one
// that costs an operator an afternoon: a file that says "ads are on" and a process that charges nothing.
func TestAStreamCannotBeOnWhileTheSwitchIsOff(t *testing.T) {
	monetizationForTest(t, false, []string{StreamAds}, nil, premiumLimit())

	if StreamOn(StreamAds) {
		t.Error("StreamOn is true with monetization_enabled off: the configuration is being read as if it " +
			"were the posture")
	}
	if err := RequireStream(StreamAds); !errors.Is(err, ErrMonetizationDisabled) {
		t.Errorf("RequireStream = %v, want ErrMonetizationDisabled", err)
	}
	err := Validate()
	if err == nil {
		t.Fatal("Validate accepted a stream with the switch off")
	}
	if !strings.Contains(err.Error(), "monetization_enabled") || !strings.Contains(err.Error(), StreamAds) {
		t.Errorf("the refusal must name the key AND the stream, got: %s", err)
	}
}

// THE LIMITER IS HANDED NO SECOND LIMIT UNLESS THE STREAM IS ON. `monetization.Limits` is where the wiring
// asks "is there a premium tier at all", and answering with the configured numbers while the stream is off
// would make the startup line state a premium limit no caller can reach -- a posture about a tier with no
// members, which is the kind of claim this programme keeps refusing.
func TestThereIsNoSecondLimitUnlessTheStreamIsOn(t *testing.T) {
	monetizationForTest(t, false, nil, nil, premiumLimit())
	if _, premium := Limits(); !premium.Disabled() {
		t.Error("with monetization off the limiter is still handed a premium limit")
	}

	monetizationForTest(t, true, nil, nil, premiumLimit())
	if _, premium := Limits(); !premium.Disabled() {
		t.Error("with no stream configured the limiter is still handed a premium limit")
	}

	monetizationForTest(t, true, []string{StreamAds}, nil, premiumLimit())
	if _, premium := Limits(); !premium.Disabled() {
		t.Error("with only an unimplemented stream configured the limiter is handed a premium limit")
	}

	monetizationForTest(t, true, []string{StreamPremiumSubscription}, nil, premiumLimit())
	free, premium := Limits()
	if premium.Disabled() {
		t.Fatal("with the stream on the limiter is handed no premium limit: the tier would do nothing")
	}
	if free.RequestsPerMinute != config.GetRateLimit().RequestsPerMinute {
		t.Errorf("the free limit moved with the tier: %d", free.RequestsPerMinute)
	}
}

// An unknown name is refused WITH THE LIST, so a typo is a fixable message rather than a stream that silently
// does nothing.
func TestAnUnknownStreamIsRefusedWithTheKnownList(t *testing.T) {
	monetizationForTest(t, true, nil, nil, premiumLimit())

	err := RequireStream("premium_api_teir")
	if !errors.Is(err, ErrUnknownStream) {
		t.Fatalf("RequireStream = %v, want ErrUnknownStream", err)
	}
	for _, known := range KnownStreams() {
		if !strings.Contains(err.Error(), known) {
			t.Errorf("the refusal does not name the known stream %q: %s", known, err)
		}
	}
}

// A stream this tree cannot sell names the work it would take, and the refusal is the SAME whether it arrives
// through RequireStream or through Validate -- one message, two callers.
func TestAnUnimplementedStreamIsRefusedWithTheWorkItWouldTake(t *testing.T) {
	monetizationForTest(t, true, nil, nil, premiumLimit())

	err := RequireStream(StreamAds)
	if !errors.Is(err, ErrStreamNotImplemented) {
		t.Fatalf("RequireStream(ads) = %v, want ErrStreamNotImplemented", err)
	}
	if !strings.Contains(err.Error(), "renderer") {
		t.Errorf("the refusal has to say what building it would take, got: %s", err)
	}

	monetizationForTest(t, true, []string{StreamAds}, nil, premiumLimit())
	if err := Validate(); !errors.Is(err, ErrStreamNotImplemented) {
		t.Errorf("Validate accepted a stream with no implementation: %v", err)
	}
}

// THE GATE REFUSES §18.2, CAPABILITY BY CAPABILITY. This is the rule the whole package exists for: a caller
// that asks to sell search gets an error naming the item and the section, rather than a working paywall.
func TestTheGateRefusesEveryAlwaysFreeCapability(t *testing.T) {
	monetizationForTest(t, true, []string{StreamPremiumSubscription}, nil, premiumLimit())

	capabilities := AlwaysFree()
	if len(capabilities) != 7 {
		t.Fatalf("the always-free list has %d items, want §18.2's seven", len(capabilities))
	}
	for _, c := range capabilities {
		err := Gate(c.Name)
		if !errors.Is(err, ErrAlwaysFree) {
			t.Errorf("Gate(%q) = %v, want ErrAlwaysFree", c.Name, err)
			continue
		}
		if !strings.Contains(err.Error(), "§18.2") && !strings.Contains(err.Error(), "always-free") {
			t.Errorf("Gate(%q)'s refusal must name where the rule comes from, got: %s", c.Name, err)
		}
	}
}

// The gate also refuses a capability that is neither list -- a misspelling must not read as "not a paywall".
func TestTheGateRefusesANameOnNeitherList(t *testing.T) {
	monetizationForTest(t, true, []string{StreamPremiumSubscription}, nil, premiumLimit())

	if err := Gate("serch"); !errors.Is(err, ErrUnknownCapability) {
		t.Errorf("Gate(serch) = %v, want ErrUnknownCapability", err)
	}
}

// A premium feature whose stream has no implementation is refused with that reason, and the switch being off is
// refused as such.
func TestTheGateStatesWhyAPremiumFeatureIsUnavailable(t *testing.T) {
	monetizationForTest(t, true, nil, nil, premiumLimit())
	if err := Gate(FeatureAdFree); !errors.Is(err, ErrStreamNotImplemented) {
		t.Errorf("Gate(ad_free) = %v, want ErrStreamNotImplemented (its stream has no renderer)", err)
	}

	monetizationForTest(t, false, nil, nil, premiumLimit())
	if err := Gate(FeaturePremiumAPITier); !errors.Is(err, ErrMonetizationDisabled) {
		t.Errorf("Gate(premium_api_tier) with the switch off = %v, want ErrMonetizationDisabled", err)
	}

	monetizationForTest(t, true, []string{StreamPremiumSubscription}, nil, premiumLimit())
	if err := Gate(FeaturePremiumAPITier); err != nil {
		t.Errorf("Gate(premium_api_tier) on an instance selling it = %v, want nil", err)
	}
}

// THE TWO LISTS ARE DISJOINT, asserted rather than assumed: a name on both would be a feature this package both
// promises free and sells, and which one won would depend on the order of two loops.
func TestTheAlwaysFreeListAndThePremiumFeaturesAreDisjoint(t *testing.T) {
	free := map[string]bool{}
	for _, c := range AlwaysFree() {
		free[c.Name] = true
	}
	for _, name := range PremiumFeatures() {
		if free[name] {
			t.Errorf("%q is both always free and for sale", name)
		}
	}
}

// Every capability names at least one surface, and every surface is one of the two kinds the leak test knows
// how to call. A capability with no surface is a claim about a document, which is the thing this file exists to
// prevent.
func TestEveryAlwaysFreeCapabilityNamesCallableSurfaces(t *testing.T) {
	for _, c := range AlwaysFree() {
		if len(c.Surfaces) == 0 {
			t.Errorf("%q lists no surface: the page would promise something nothing provides", c.Name)
			continue
		}
		if c.Words == "" {
			t.Errorf("%q does not carry §18.2's own words", c.Name)
		}
		for _, s := range c.Surfaces {
			if s.Kind != "graphql" && s.Kind != "route" {
				t.Errorf("%q has a surface of kind %q, which the leak test cannot call", c.Name, s.Kind)
			}
			if s.Ref == "" {
				t.Errorf("%q has an empty surface reference", c.Name)
			}
		}
		if !c.Built && c.Because == "" {
			t.Errorf("%q is not built and does not say why", c.Name)
		}
	}
}

// A "higher API limit" that is not higher is refused at startup, because that is a promise nothing honours --
// and it is the mistake a default makes silently, by being equal.
func TestAPremiumLimitThatIsNotHigherIsRefused(t *testing.T) {
	free := config.GetRateLimit()

	monetizationForTest(t, true, []string{StreamPremiumSubscription}, nil, free)
	if err := Validate(); err == nil {
		t.Fatal("Validate accepted a premium limit equal to the free one")
	}

	monetizationForTest(t, true, []string{StreamPremiumSubscription}, nil,
		ratelimit.Config{RequestsPerMinute: free.RequestsPerMinute + 1, Burst: free.Burst})
	if err := Validate(); err == nil {
		t.Fatal("Validate accepted a premium burst equal to the free one")
	}

	monetizationForTest(t, true, []string{StreamPremiumSubscription}, nil, premiumLimit())
	if err := Validate(); err != nil {
		t.Errorf("Validate refused a genuinely higher limit: %v", err)
	}
}

// The posture line names the key in BOTH states: the log is where an operator finds out which posture they are
// in, and a line that only mentions the switch when it is on is a line nobody reads when it matters.
func TestThePostureLineNamesTheKeyInBothStates(t *testing.T) {
	monetizationForTest(t, false, nil, nil, premiumLimit())
	off := PostureLine()
	if !strings.Contains(off, "monetization_enabled") || !strings.Contains(off, "OFF") {
		t.Errorf("the off posture line does not state the key and the posture: %s", off)
	}
	if !strings.Contains(off, "always-free") {
		t.Errorf("the off posture line does not say the always-free list is enforced: %s", off)
	}

	monetizationForTest(t, true, []string{StreamPremiumSubscription}, []string{"member-1"}, premiumLimit())
	on := PostureLine()
	if !strings.Contains(on, "ON") || !strings.Contains(on, StreamPremiumSubscription) {
		t.Errorf("the on posture line does not name the posture and the stream: %s", on)
	}
	if !strings.Contains(on, "1200") || !strings.Contains(on, "comped") {
		t.Errorf("the on posture line does not state the premium limit and the comps: %s", on)
	}
}

// The posture is BUILT from the two lists and from Gate, so the page cannot state something the code does not:
// every feature's availability in the posture is exactly Gate's answer.
func TestThePostureIsBuiltFromTheListsAndTheGate(t *testing.T) {
	monetizationForTest(t, true, []string{StreamPremiumSubscription}, []string{"a", "b"}, premiumLimit())

	p := PostureOf()
	if !p.Enabled {
		t.Error("the posture reports the switch off while it is on")
	}
	if len(p.Streams) != len(KnownStreams()) {
		t.Errorf("the posture lists %d streams, want all %d", len(p.Streams), len(KnownStreams()))
	}
	if len(p.AlwaysFree) != len(AlwaysFree()) {
		t.Errorf("the posture lists %d always-free items, want %d", len(p.AlwaysFree), len(AlwaysFree()))
	}
	if p.PremiumMembers != 2 {
		t.Errorf("the posture reports %d comped members, want 2", p.PremiumMembers)
	}
	for _, f := range p.Premium {
		if want := Gate(f.Name) == nil; f.Available != want {
			t.Errorf("%q: the posture says available=%v while the gate says %v", f.Name, f.Available, want)
		}
	}
	if p.FreeLimit.RequestsPerMinute != config.GetRateLimit().RequestsPerMinute {
		t.Errorf("the posture's free limit is %d, want the configured %d",
			p.FreeLimit.RequestsPerMinute, config.GetRateLimit().RequestsPerMinute)
	}
}

// AN ANONYMOUS CREDENTIAL IS NOTHING, NOT AN ERROR, and neither is a feed token or a garbage string: the
// entitlement answers "free", which is what the limiter counts against when nothing claims a caller.
func TestACredentialThatIsNotAnApiKeyIsFree(t *testing.T) {
	monetizationForTest(t, true, []string{StreamPremiumSubscription}, []string{"member-1"}, premiumLimit())

	for _, credential := range []string{"", "address:203.0.113.7", "feed:some-feed-token", "ApiKey:not-a-jwt"} {
		if CredentialIsPremium(credential) {
			t.Errorf("%q was treated as premium", credential)
		}
		if tier := CredentialTier(credential); tier != ratelimit.TierFree {
			t.Errorf("CredentialTier(%q) = %q, want the free tier", credential, tier)
		}
	}
}

// A COMPED MEMBER'S KEY IS PREMIUM, and only while the stream is on: the entitlement is derived, never stored,
// so the switch is the whole of the grant's existence.
func TestACompedMembersKeyIsPremiumWhileTheStreamIsOn(t *testing.T) {
	key := apiKeyFor(t, "11111111-1111-1111-1111-111111111111")
	credential := "ApiKey:" + key

	monetizationForTest(t, true, []string{StreamPremiumSubscription},
		[]string{"11111111-1111-1111-1111-111111111111"}, premiumLimit())
	if !CredentialIsPremium(credential) {
		t.Fatal("a comped member's key is not premium with the stream on")
	}
	if tier := CredentialTier(credential); tier != ratelimit.TierPremium {
		t.Errorf("CredentialTier = %q, want %q", tier, ratelimit.TierPremium)
	}

	// The same key, on an instance whose switch is off: nobody is premium, which is what makes "with
	// monetization off every feature is reachable" true by construction.
	monetizationForTest(t, false, nil, []string{"11111111-1111-1111-1111-111111111111"}, premiumLimit())
	if CredentialIsPremium(credential) {
		t.Error("a comped member is premium on an instance that charges for nothing")
	}

	// And with the stream configured but monetization on and the stream NOT named: still free.
	monetizationForTest(t, true, []string{StreamAds},
		[]string{"11111111-1111-1111-1111-111111111111"}, premiumLimit())
	if CredentialIsPremium(credential) {
		t.Error("a comped member is premium while the premium subscription stream is off")
	}

	// A member who is not on the list, with everything on: free.
	monetizationForTest(t, true, []string{StreamPremiumSubscription},
		[]string{"some-other-member"}, premiumLimit())
	if CredentialIsPremium(credential) {
		t.Error("a member the operator did not comp is premium")
	}
}

// apiKeyFor mints the key this instance issues for a member. It builds the same claims
// internal/service/user.apikey.go builds, because the entitlement has to read the real thing: a test that
// invented its own format would prove nothing about the parser the limiter uses.
func apiKeyFor(t *testing.T, member string) string {
	t.Helper()
	previous := config.C.JWTSignKey
	config.C.JWTSignKey = "monetization-test-signing-key"
	t.Cleanup(func() { config.C.JWTSignKey = previous })

	claims := &user.APIKeyClaims{
		UserID: member,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:  user.APIKeySubject,
			IssuedAt: jwt.NewNumericDate(time.Now()),
		},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(config.GetJWTSignKey())
	if err != nil {
		t.Fatalf("signing a test key: %v", err)
	}
	return signed
}
