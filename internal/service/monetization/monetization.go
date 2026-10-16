// Package monetization holds this instance's monetization posture: the switch, the revenue streams this
// version can actually sell, and -- the part that matters most -- §18.2's list of what is ALWAYS FREE, as
// code that a gate refuses to gate (plan phase 15, spec §18).
//
// WHY THIS PACKAGE EXISTS AT ALL. NexusXXX §18.2 lists seven things a paywall may never touch: full search
// and discovery, all content, tagging and community participation, basic collections, API access, full data
// export and federation participation. In a document that list is a paragraph, and a paragraph does not stop
// the next feature from putting a core path behind a subscription -- the only thing that does is a refusal
// with a name in it, in one place, that a future caller cannot route around by accident. Hence
// internal/service/monetization/alwaysfree.go: the list is data, the gate consults it FIRST, and
// `Gate("search")` returns an error rather than a bool, so a caller who tries it gets told why not.
//
// WHAT THE SWITCH IS. `monetization_enabled` false means this instance charges for NOTHING -- and that is not
// a licence to gate things either: off means NO STREAM MAY BE ON, and a stream that is on while the switch is
// false is refused by name at startup (`Validate`, called where the process starts), because a configuration
// that states two contradictory things is one whose operator will believe the wrong half.
//
// WHAT A STREAM IS. §18.1's revenue streams, as a CLOSED set. A stream is only "on" when monetization is
// enabled AND the stream has an implementation on this tree, and everything is read through RequireStream so
// that "monetization is enabled" can never be a claim about code that does not exist. Four of the five are
// named and refused with the work each would take: that is the difference between a specified switch and a
// switch that lies.
package monetization

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/stashapp/stash-box/internal/config"
	"github.com/stashapp/stash-box/internal/ratelimit"
)

// The §18.1 revenue streams, by the name an operator writes in `monetization_streams`.
const (
	// StreamPremiumSubscription is the premium subscription: §18.1's UX features -- higher API limits
	// (implemented, the one stream with code behind it today), ad-free, priority delivery, advanced search,
	// server-synced history, custom themes and a support badge.
	StreamPremiumSubscription = "premium_subscription"
	// StreamCreatorSubs is creator subscriptions: verified performers and studios selling exclusive work
	// through the platform, first-class in discovery. Needs a payment path and phase 10's media plane.
	StreamCreatorSubs = "creator_subs"
	// StreamAds is ethical advertising: contextual only, based on the page's own tags, no tracking, no ad
	// network, at most two slots a page. Needs the renderer (and the bound enforced where slots are counted).
	StreamAds = "ads"
	// StreamDataLicensing is anonymised aggregate trend data licensed to studios and researchers. Needs the
	// aggregate export (phase 13d's family) and a licence registry.
	StreamDataLicensing = "data_licensing"
	// StreamCommercialAPITier is the commercial API tier for businesses. Deliberately NOT built as a second
	// mechanism: it is the premium tier with a commercial licence beside it, and two implementations of one
	// limit would be two answers to "how fast may this caller go".
	StreamCommercialAPITier = "commercial_api_tier"
)

// ErrMonetizationDisabled is returned when a stream or a premium feature is asked about while the switch is
// off. It names the key, because the reader who can act on it is the operator.
var ErrMonetizationDisabled = errors.New(
	"this instance charges for nothing (monetization_enabled is off)")

// ErrUnknownStream is a stream name this version does not know -- a typo, or a key written for a later
// version. The message carries KnownStreams() so the fix is in the error.
var ErrUnknownStream = errors.New("unknown monetization stream")

// ErrUnknownCapability is a name that is neither §18.2's (always free) nor §18.1's (for sale). It is an
// error rather than a silent allow: a gate that answers "not a paywall" for a name nobody recognises would
// pass for a name that was misspelled.
var ErrUnknownCapability = errors.New("unknown capability")

// ErrAlwaysFree is §18.2 as a rule rather than as a paragraph. Returned by Gate for every capability on the
// always-free list, naming the capability, so that a future paywall on a core path fails HERE, in one place
// with a message, rather than in production.
var ErrAlwaysFree = errors.New("always free")

// ErrStreamNotImplemented is returned for a stream §18.1 asks for and this tree cannot sell. The message
// carries the work it would take, so the operator who switched it on learns what is missing rather than
// watching a stream that does nothing.
var ErrStreamNotImplemented = errors.New("not implemented on this instance")

// ErrStreamOff is returned for a stream this tree CAN sell and this operator has not switched on. It exists
// because "implemented" and "on" are two different facts, and conflating them is not a theoretical mistake:
// the first version of RequireStream answered nil for any stream the code could sell, so a comped member was
// granted the premium API limit on an instance whose only configured stream was `ads`. The unit test for the
// entitlement found it. "May this be sold?" and "is this instance selling it?" have to be the same question,
// and the answer to the second is `monetization_streams`.
var ErrStreamOff = errors.New("not switched on by this instance")

// stream is one of §18.1's streams, with what it sells and whether this tree has any of it.
type stream struct {
	name string
	// sells is what the stream sells, one sentence, rendered on the published posture.
	sells string
	// implemented reports whether this tree has code behind the stream AT ALL.
	implemented bool
	// because explains an unimplemented stream: the work it would take, named.
	because string
}

// streams is the closed set, in §18.1's order. A stream with implemented false is one an operator can name
// and cannot turn on, and its refusal says what it would take.
var streams = []stream{
	{
		name:        StreamPremiumSubscription,
		sells:       "a premium subscription: a higher API limit, and later ad-free reading, priority delivery, advanced search, server-synced history, custom themes and a support badge",
		implemented: true,
	},
	{
		name:        StreamCreatorSubs,
		sells:       "creator subscriptions: verified performers and studios selling exclusive work here, first-class in discovery",
		implemented: false,
		because:     "it needs a payment path and phase 10's media plane (nothing can be sold before there is something to deliver)",
	},
	{
		name:        StreamAds,
		sells:       "ethical advertising: contextual only, read from the page's own tags, never tracking, no ad network, at most two slots a page",
		implemented: false,
		because:     "it needs the contextual renderer and the two-slot bound enforced where slots are counted",
	},
	{
		name:        StreamDataLicensing,
		sells:       "anonymised aggregate trend data licensed to studios and researchers, never individual data",
		implemented: false,
		because:     "it needs an aggregate export and a licence registry naming who may read it",
	},
	{
		name:        StreamCommercialAPITier,
		sells:       "a commercial API tier for businesses building on this instance, free for personal and open-source use",
		implemented: false,
		because:     "it is the premium tier with a commercial licence beside it, and is deliberately not a second implementation of one limit",
	},
}

// Enabled reports whether this instance may charge for anything.
func Enabled() bool {
	return config.GetMonetizationEnabled()
}

// KnownStreams returns §18.1's stream names in the order this file lists them. Used by the unknown-stream
// error and by the published posture, so both name the same set.
func KnownStreams() []string {
	names := make([]string, 0, len(streams))
	for _, s := range streams {
		names = append(names, s.name)
	}
	return names
}

// streamByName finds a stream, and reports whether it exists.
func streamByName(name string) (stream, bool) {
	for _, s := range streams {
		if s.name == name {
			return s, true
		}
	}
	return stream{}, false
}

// Implemented reports whether this tree has any code behind a stream. Unknown names are not implemented, and
// say so through RequireStream rather than here.
func Implemented(name string) bool {
	s, ok := streamByName(name)
	return ok && s.implemented
}

// StreamOn reports whether a stream is switched on: monetization enabled AND the stream named in
// `monetization_streams` AND the stream implemented. A stream that is named but unbuildable is NOT on, which
// is the whole reason RequireStream exists -- and the reason this returns false rather than deferring to the
// configuration alone.
func StreamOn(name string) bool {
	if !Enabled() {
		return false
	}
	if !Implemented(name) {
		return false
	}
	return contains(config.GetMonetizationStreams(), name)
}

// RequireStream is the ONE reader of `monetization_streams`. It returns nil for a stream this instance can
// and does sell, and an error naming the reason otherwise:
//
//   - an unknown name: ErrUnknownStream, whose message carries KnownStreams();
//   - the switch off: ErrMonetizationDisabled, naming the key;
//   - a stream with no implementation: ErrStreamNotImplemented, whose message carries the work it would take.
//
// A caller that reads config.GetMonetizationStreams() directly has to invent those three messages itself,
// which is how a switch starts meaning three different things in three places.
func RequireStream(name string) error {
	s, ok := streamByName(name)
	if !ok {
		return fmt.Errorf("%w: %q (this version knows: %s)",
			ErrUnknownStream, name, strings.Join(KnownStreams(), ", "))
	}
	if !Enabled() {
		return ErrMonetizationDisabled
	}
	if !s.implemented {
		return fmt.Errorf("%w: the stream %q would need %s", ErrStreamNotImplemented, name, s.because)
	}
	if !StreamOn(name) {
		return fmt.Errorf(
			"%w: the stream %q exists on this tree, and this instance is not selling it -- it is not in "+
				"monetization_streams", ErrStreamOff, name)
	}
	return nil
}

// Validate is the startup refusal: the configuration is read once, at the point the process starts, and a
// configuration that contradicts itself stops the process rather than being logged.
//
// The three refusals, in the order an operator meets them:
//
//  1. A stream named while the switch is off. The message lists the streams and names the key, so the fix is
//     to remove them or to turn monetization on -- and it is a REFUSAL rather than a warning because the
//     alternative is a process that runs in one of the two postures while its configuration says the other.
//  2. A stream this tree cannot sell, with the work it would take (RequireStream's message).
//  3. A premium limit that is not higher than the free one while the premium subscription is on. §18.1 sells
//     "higher API limits"; a premium tier at or below the free limit is a promise nothing honours, and it is
//     the kind of mistake a default makes silently (two keys that happen to be equal).
func Validate() error {
	configured := config.GetMonetizationStreams()
	if !Enabled() {
		if len(configured) > 0 {
			return fmt.Errorf(
				"monetization_enabled is off, so no stream may be on -- remove them, or turn the switch on: %s",
				strings.Join(configured, ", "))
		}
		return nil
	}

	for _, name := range configured {
		if err := RequireStream(name); err != nil {
			return err
		}
	}

	if StreamOn(StreamPremiumSubscription) {
		free, premium := config.GetRateLimit(), config.GetMonetizationPremiumRateLimit()
		if premium.RequestsPerMinute <= free.RequestsPerMinute || premium.Burst <= free.Burst {
			return fmt.Errorf(
				"the stream %q is on, but the premium limit (%d/minute, burst %d) is not above the free one "+
					"(%d/minute, burst %d): a \"higher API limit\" that is not higher is a promise nothing honours",
				StreamPremiumSubscription, premium.RequestsPerMinute, premium.Burst,
				free.RequestsPerMinute, free.Burst)
		}
	}
	return nil
}

// Gate asks whether a §18.1 feature may be gated (sold) on this instance.
//
// THE ORDER OF THE TWO QUESTIONS IS THE WHOLE POINT: §18.2's list is consulted FIRST, so a capability that is
// always free can never be reported as gatable, whatever the switch says. Everything else follows:
//
//   - a name on the always-free list: ErrAlwaysFree, naming the capability and the section;
//   - a name that is neither list: ErrUnknownCapability;
//   - a known premium feature: ErrMonetizationDisabled when the switch is off, ErrStreamNotImplemented when
//     its stream has no code behind it, nil when the feature may be sold.
//
// It returns an error rather than a bool for the same reason mediaplane.RequireDelivery does: an unchecked
// bool is invisible to the compiler, and the refusal has to carry its reason to be actionable.
func Gate(capability string) error {
	if free, ok := alwaysFreeByName(capability); ok {
		return fmt.Errorf(
			"%w: %q is on §18.2's always-free list (%s) and cannot be gated, whatever monetization_enabled says",
			ErrAlwaysFree, capability, free.Words)
	}

	feature, ok := premiumFeatureByName(capability)
	if !ok {
		return fmt.Errorf("%w: %q is neither on §18.2's always-free list nor one of §18.1's features",
			ErrUnknownCapability, capability)
	}
	return RequireStream(feature.Stream)
}

// Posture is everything the published page and the JSON endpoint state. ONE value renders both, so the page
// cannot disagree with the endpoint -- the rule slice 16a's transparency report established.
type Posture struct {
	// Enabled is the switch itself, and the page's first sentence is built from it.
	Enabled bool `json:"monetization_enabled"`
	// Streams are ALL of §18.1's streams with their state, so an operator reading the page learns what this
	// version could sell as well as what it is selling.
	Streams []StreamState `json:"streams"`
	// AlwaysFree is §18.2, in its own order, with the surfaces that provide each item HERE.
	AlwaysFree []Capability `json:"always_free"`
	// Premium is §18.1's UX features, with the state of each and why it is not available when it is not.
	Premium []Feature `json:"premium_features"`
	// FreeLimit and PremiumLimit are the two API rates a credential may be counted against.
	FreeLimit    Limit `json:"free_limit"`
	PremiumLimit Limit `json:"premium_limit"`
	// PremiumMembers is how many members this operator has comped. The ids are NOT published: the page states
	// that the mechanism exists and how many grants it holds, because who pays is their business.
	PremiumMembers int `json:"premium_members"`
}

// StreamState is one stream as the page states it.
type StreamState struct {
	Name        string `json:"name"`
	Sells       string `json:"sells"`
	On          bool   `json:"on"`
	Implemented bool   `json:"implemented"`
	Because     string `json:"because,omitempty"`
}

// Feature is one of §18.1's premium UX features.
type Feature struct {
	Name string `json:"name"`
	// Sells is what a member gets, in one sentence.
	Sells string `json:"sells"`
	// Stream is the revenue stream the feature belongs to.
	Stream string `json:"stream"`
	// Available reports whether the feature may actually be sold on this instance right now -- which is
	// Gate(feature) == nil, computed rather than restated.
	Available bool `json:"available"`
	// Because is the refusal when it is not available: the reason a reader can act on.
	Because string `json:"because,omitempty"`
}

// Limit is a rate limit as the page states it.
type Limit struct {
	RequestsPerMinute int `json:"requests_per_minute"`
	Burst             int `json:"burst"`
}

// PostureOf collects the posture. It reads the configuration and the two lists and computes each feature's
// availability through Gate, so the page is a rendering of the rules rather than a second statement of them.
func PostureOf() Posture {
	p := Posture{
		Enabled:        Enabled(),
		FreeLimit:      Limit{RequestsPerMinute: config.GetRateLimit().RequestsPerMinute, Burst: config.GetRateLimit().Burst},
		PremiumLimit:   Limit{RequestsPerMinute: config.GetMonetizationPremiumRateLimit().RequestsPerMinute, Burst: config.GetMonetizationPremiumRateLimit().Burst},
		PremiumMembers: len(config.GetMonetizationPremiumMembers()),
	}

	for _, s := range streams {
		p.Streams = append(p.Streams, StreamState{
			Name:        s.name,
			Sells:       s.sells,
			On:          StreamOn(s.name),
			Implemented: s.implemented,
			Because:     s.because,
		})
	}

	p.AlwaysFree = AlwaysFree()

	for _, f := range premiumFeatures {
		state := Feature{Name: f.Name, Sells: f.Sells, Stream: f.Stream, Available: Gate(f.Name) == nil}
		if !state.Available {
			state.Because = Gate(f.Name).Error()
		}
		p.Premium = append(p.Premium, state)
	}

	return p
}

// CredentialIsPremium reports whether a credential belongs to a member this operator has comped, and only
// while the premium subscription's API tier is actually on.
//
// THE STREAM CHECK IS NOT DEFENSIVE PADDING: with monetization off -- or the stream off -- NO credential is
// premium, which is what makes "with monetization off every feature is reachable" true by construction rather
// than by inspection. The gate is consulted rather than the config, so a hypothetical tree where the API tier
// had drifted onto the always-free list would grant nobody a tier instead of granting one quietly.
//
// A credential that does not authenticate is a FREE caller, never an error: a rate limiter that fails closed
// on an unparseable key would turn a typo into a 429, and the caller would have no way to tell why.
func CredentialIsPremium(credential string) bool {
	if err := Gate(FeaturePremiumAPITier); err != nil {
		return false
	}
	member, ok := memberOfCredential(credential)
	if !ok {
		return false
	}
	return contains(config.GetMonetizationPremiumMembers(), member)
}

// CredentialTier is the tier name a credential counts against, in the vocabulary the limiter counts in:
// ratelimit.TierPremium or ratelimit.TierFree. The names live THERE rather than here, because the limiter is
// what compares them -- a second pair of constants here would be two spellings of one tier, and the day they
// drifted the premium limit would simply stop applying with nothing to say so.
func CredentialTier(credential string) string {
	if CredentialIsPremium(credential) {
		return ratelimit.TierPremium
	}
	return ratelimit.TierFree
}

// PostureLine states this process's monetization posture in the startup log, both ways.
//
// The line names the key in BOTH variants, because the log is where an operator finds out which posture they
// are in -- and a posture a process never mentions is one nobody can tell was ignored. It is also the only
// place the always-free list is mentioned at runtime, so a reader who greps the log learns that the list is
// enforced in code rather than promised on a page.
func PostureLine() string {
	const free = "§18.2's always-free list is enforced in code (internal/service/monetization); " +
		"GET /monetization publishes the posture"

	if !Enabled() {
		configured := config.GetMonetizationStreams()
		if len(configured) > 0 {
			// Unreachable in a process that started through Validate, and phrased so that a reader who finds
			// it in a log knows the process did not start the ordinary way.
			return fmt.Sprintf(
				"monetization is OFF (monetization_enabled is false) but streams are configured: %s -- "+
					"this contradicts itself and startup refuses it; %s",
				strings.Join(configured, ", "), free)
		}
		return fmt.Sprintf(
			"monetization is OFF (monetization_enabled is false): this instance charges for nothing, and no "+
				"stream is on; %s", free)
	}

	on, off := []string{}, []string{}
	for _, s := range streams {
		if StreamOn(s.name) {
			on = append(on, s.name)
		} else if s.implemented {
			off = append(off, s.name)
		}
	}
	sort.Strings(on)
	sort.Strings(off)

	line := fmt.Sprintf("monetization is ON (monetization_enabled): streams on: %s", strings.Join(on, ", "))
	if len(off) > 0 {
		line += fmt.Sprintf("; available and off: %s", strings.Join(off, ", "))
	}
	if StreamOn(StreamPremiumSubscription) {
		premium := config.GetMonetizationPremiumRateLimit()
		line += fmt.Sprintf(
			"; the premium API tier is %d requests/minute, burst %d, against %d/minute burst %d for everybody "+
				"else, granted to %d comped member(s)",
			premium.RequestsPerMinute, premium.Burst,
			config.GetRateLimit().RequestsPerMinute, config.GetRateLimit().Burst,
			len(config.GetMonetizationPremiumMembers()))
	}
	return line + "; " + free
}

// contains is a small helper rather than a set: these lists are a handful of names, and a set built per call
// to save microseconds would be a data structure in exchange for nothing.
func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

// Limits returns the two limit configurations a credential may be counted against: the free one everybody
// gets, and the premium one an entitled credential gets (§18.1, "higher API limits").
//
// THE PREMIUM ONE IS DISABLED UNLESS THE STREAM IS ACTUALLY ON, and that is a correction rather than a
// convenience: the configured premium numbers exist in the file whether or not the operator is selling
// anything, and a limiter handed them would STATE a premium tier in its startup line on an instance where
// nobody can be premium. A posture that describes a limit no caller can reach is the kind of claim this
// programme keeps refusing, so the question "is there a second limit at all" is answered here, through the
// same RequireStream the gate uses, and the limiter's own rule (a disabled configuration means no limit)
// carries it from there.
//
// The wiring passes BOTH to ratelimit.NewWithTiers rather than unpacking them, so "a premium limit that is
// not above the free one" stays one decision in one place (Validate refuses it at startup) instead of
// becoming arithmetic a caller could get wrong.
func Limits() (free, premium ratelimit.Config) {
	free = config.GetRateLimit()
	if err := RequireStream(StreamPremiumSubscription); err != nil {
		return free, ratelimit.Config{}
	}
	return free, config.GetMonetizationPremiumRateLimit()
}
