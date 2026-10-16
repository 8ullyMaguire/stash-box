package config

import (
	"testing"

	"github.com/stashapp/stash-box/internal/ratelimit"
)

// monetization is a posture and not a trade-off, so its SHIPPED state is the assertion that matters: `var C =
// &config{...}` is the value an instance runs with when no config file names the keys, which makes "this
// instance charges for nothing unless an operator says otherwise" a property of the default rather than of a
// comment (plan phase 15, spec §18).
//
// This is the test for the shape that cost this repository an afternoon once already: a rung that wrote
// `commons.enabled = false` while no code read the key, so "the capability ships disabled" was a claim about a
// configuration file.
func TestMonetizationIsOffInTheShippedDefault(t *testing.T) {
	if C.MonetizationEnabled {
		t.Fatal("monetization_enabled defaults to true: an instance nobody told to charge would charge")
	}
	if len(C.MonetizationStreams) != 0 {
		t.Fatalf("monetization_streams defaults to %v: a stream that is on by default is a stream nobody chose",
			C.MonetizationStreams)
	}
	if len(C.MonetizationPremiumMembers) != 0 {
		t.Fatalf("monetization_premium_members defaults to %d entries: nobody is premium by default",
			len(C.MonetizationPremiumMembers))
	}
}

// The two limits ship SET and the premium one ships HIGHER, because the pair is only meaningful that way: a
// premium limit that shipped equal to the free one would be refused by monetization.Validate the moment an
// operator switched the stream on, and the operator would have to guess which number to change.
func TestThePremiumLimitShipsHigherThanTheFreeOne(t *testing.T) {
	free, premium := GetRateLimit(), GetMonetizationPremiumRateLimit()
	if premium.RequestsPerMinute <= free.RequestsPerMinute {
		t.Errorf("the premium rate ships at %d against a free %d: a \"higher limit\" that is not higher is a "+
			"promise nothing honours", premium.RequestsPerMinute, free.RequestsPerMinute)
	}
	if premium.Burst <= free.Burst {
		t.Errorf("the premium burst ships at %d against a free %d", premium.Burst, free.Burst)
	}
}

// The accessors read their OWN fields. Not a tautology in this file: the neighbouring conn_max_lifetime helper
// spent a while reading max_idle_conns, and a switch that read a different bool would be a switch that does not
// control what its name says -- which, for this key, is the difference between charging and not charging.
func TestTheMonetizationAccessorsReadTheirOwnFields(t *testing.T) {
	previousEnabled, previousStreams := C.MonetizationEnabled, C.MonetizationStreams
	previousMembers := C.MonetizationPremiumMembers
	previousPerMinute, previousBurst := C.MonetizationPremiumRequestsPerMinute, C.MonetizationPremiumBurst
	previousFree, previousFreeBurst := C.RateLimitRequestsPerMinute, C.RateLimitBurst
	t.Cleanup(func() {
		C.MonetizationEnabled, C.MonetizationStreams = previousEnabled, previousStreams
		C.MonetizationPremiumMembers = previousMembers
		C.MonetizationPremiumRequestsPerMinute, C.MonetizationPremiumBurst = previousPerMinute, previousBurst
		C.RateLimitRequestsPerMinute, C.RateLimitBurst = previousFree, previousFreeBurst
	})

	C.MonetizationEnabled = true
	C.MonetizationStreams = []string{"premium_subscription"}
	C.MonetizationPremiumMembers = []string{"member-1"}
	C.MonetizationPremiumRequestsPerMinute, C.MonetizationPremiumBurst = 4321, 432

	if !GetMonetizationEnabled() {
		t.Error("GetMonetizationEnabled() = false with the field set")
	}
	if len(GetMonetizationStreams()) != 1 || GetMonetizationStreams()[0] != "premium_subscription" {
		t.Errorf("GetMonetizationStreams() = %v", GetMonetizationStreams())
	}
	if len(GetMonetizationPremiumMembers()) != 1 {
		t.Errorf("GetMonetizationPremiumMembers() = %v", GetMonetizationPremiumMembers())
	}
	if got := GetMonetizationPremiumRateLimit(); got != (ratelimit.Config{RequestsPerMinute: 4321, Burst: 432}) {
		t.Errorf("GetMonetizationPremiumRateLimit() = %+v", got)
	}

	// And the free accessor does not read the premium keys: two limits that moved together would be one
	// limit with two names.
	C.RateLimitRequestsPerMinute, C.RateLimitBurst = 600, 120
	if got := GetRateLimit(); got != (ratelimit.Config{RequestsPerMinute: 600, Burst: 120}) {
		t.Errorf("GetRateLimit() = %+v while the premium keys are set to 4321/432", got)
	}
	C.MonetizationEnabled = false
	if GetMonetizationEnabled() {
		t.Error("GetMonetizationEnabled() = true with the field clear")
	}
}
