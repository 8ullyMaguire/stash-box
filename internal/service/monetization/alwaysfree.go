// §18.2's always-free list, and §18.1's premium features, both as data (plan phase 15, spec §18).
//
// WHY THIS IS CODE AND NOT A PAGE. A list of promises on a page is a paragraph, and the only thing that stops
// the next feature from putting a core path behind a subscription is a refusal with a name in it, in one
// place, that a caller cannot route around. So:
//
//   - every item carries §18.2's own words AND the surfaces that provide it on THIS instance, which is what
//     the leak test calls (`monetization_integration_test.go`) -- a surface that is deleted later is a failing
//     test rather than a stale sentence;
//   - `Gate(capability)` consults this list FIRST and returns ErrAlwaysFree, so a paywall on a core path fails
//     here rather than in production;
//   - the unit tests assert the list and the premium features are DISJOINT and that every built item names at
//     least one surface, so an edit cannot quietly move something from one list to the other.
//
// THE SURFACES ARE THIS INSTANCE'S, and that is deliberate: §18.2 says "full search", and what that means
// HERE is `searchScenes`, `searchPerformers` and `queryScenes`. A list of NexusXXX's nouns with no local
// referents could be satisfied by an instance that had deleted the search box.
package monetization

// Surface is one place a capability is provided, in the form the leak test uses.
type Surface struct {
	// Kind is "graphql" (a field of the served schema) or "route" (a path on the HTTP surface).
	Kind string
	// Ref is the field name or the path.
	Ref string
	// Note states what the surface is, when the reference alone does not.
	Note string
}

// Capability is one item of §18.2's list.
type Capability struct {
	// Name is the stable key the gate refuses by.
	Name string `json:"name"`
	// Words is §18.2's own wording, so a reader can put the two documents side by side.
	Words string `json:"words"`
	// Surfaces are where this instance provides it. At least one, always: an item with no surface is a claim
	// about a document, which is the thing this file exists to prevent.
	Surfaces []Surface `json:"surfaces"`
	// Built reports whether this instance can actually do it today.
	Built bool `json:"built"`
	// Because names what would build it when Built is false, and states the scope when the item is built but
	// narrower than NexusXXX's phrase.
	Because string `json:"because,omitempty"`
}

// alwaysFree is §18.2, in its own order: the seven things a paywall may never touch.
//
// EACH ITEM'S SURFACES ARE THE FIELDS AND ROUTES THE LEAK TEST CALLS. They are named with the role directive
// they carry (`@hasRole(role: READ)`), because that is the point: access on this instance is decided by the
// role system, which monetization has no entry in -- so a capability whose only access rule is a role is, by
// construction, one that no tier can take away.
var alwaysFree = []Capability{
	{
		Name:  "search",
		Words: "full search, browsing, and all discovery features",
		Surfaces: []Surface{
			{Kind: "graphql", Ref: "searchScenes", Note: "@hasRole(role: READ)"},
			{Kind: "graphql", Ref: "searchPerformers", Note: "@hasRole(role: READ)"},
			{Kind: "graphql", Ref: "queryScenes", Note: "@hasRole(role: READ) -- the query language the UI's search page uses"},
		},
		Built: true,
	},
	{
		Name:  "content",
		Words: "all content (premium never gates content, only UX features)",
		Surfaces: []Surface{
			{Kind: "graphql", Ref: "findScene", Note: "@hasRole(role: READ)"},
			{Kind: "graphql", Ref: "findScenes", Note: "@hasRole(role: READ)"},
		},
		Built: true,
		Because: "the TIER system of §10 still decides what a member may read, exactly as it did before there " +
			"was a monetization switch: a rating gate is not a paywall, and this list is about the latter",
	},
	{
		Name:  "tagging",
		Words: "tagging and community participation",
		Surfaces: []Surface{
			{Kind: "graphql", Ref: "sceneEdit", Note: "@hasRole(role: EDIT)"},
			{Kind: "graphql", Ref: "submitSceneDraft", Note: "@hasRole(role: EDIT)"},
		},
		Built: true,
	},
	{
		Name:  "collections",
		Words: "basic collections/favorites",
		Surfaces: []Surface{
			{Kind: "graphql", Ref: "lists", Note: "@hasRole(role: READ)"},
			{Kind: "graphql", Ref: "listCreate", Note: "@hasRole(role: READ)"},
		},
		Built: true,
	},
	{
		Name:  "api_access",
		Words: "API access (rate-limited)",
		Surfaces: []Surface{
			{Kind: "route", Ref: "/graphql", Note: "the API, at the free limit that slice 13d set"},
			{Kind: "route", Ref: "/feeds/saved-search/<token>", Note: "a feed reader's own token is its credential (slice 13b)"},
		},
		Built: true,
		Because: "rate-limited is the point of the item: the free limit is the SAME limit whether or not " +
			"monetization is on, and a premium tier is a second limit, never a reduction of this one",
	},
	{
		Name:  "data_export",
		Words: "full data export",
		Surfaces: []Surface{
			{Kind: "graphql", Ref: "exportScenes", Note: "@hasRole(role: READ) -- the Hydrus-shaped export of what this instance holds (phase 13c)"},
		},
		Built: true,
		Because: "the scope is narrower than the phrase and is stated rather than implied: what exists is the " +
			"export of this instance's scene records; an export of a member's own account rows is phase 13d " +
			"and is not built",
	},
	{
		Name:  "federation",
		Words: "federation participation",
		Surfaces: []Surface{
			{Kind: "graphql", Ref: "federation_peers", Note: "@hasRole(role: ADMIN) -- this instance's mesh is operator-configured"},
		},
		Built: true,
		Because: "on this instance there is no MEMBER-facing federation surface for a paywall to take: the peer " +
			"list is an operator surface, and what a member contributes (their edits) reaches peers through " +
			"the same tables everything else does. §12.3-§12.6 are phase 12",
	},
}

// AlwaysFree returns §18.2's list. A copy, so a caller cannot edit the package's own data.
func AlwaysFree() []Capability {
	out := make([]Capability, len(alwaysFree))
	copy(out, alwaysFree)
	return out
}

// alwaysFreeByName finds a capability, and reports whether it exists.
func alwaysFreeByName(name string) (Capability, bool) {
	for _, c := range alwaysFree {
		if c.Name == name {
			return c, true
		}
	}
	return Capability{}, false
}

// §18.1's premium UX features, by the name the gate is asked about. The API tier is the one with code behind
// it; the rest are named with what they would take, because a feature list that promises seven things and
// delivers one is worth stating honestly in the same breath as the one.
const (
	// FeaturePremiumAPITier is "higher API limits", the one §18.1 feature this tree can sell today.
	FeaturePremiumAPITier = "premium_api_tier"
	// FeatureAdFree is "ad-free", and it cannot exist before the ads stream's renderer does.
	FeatureAdFree = "ad_free"
	// FeaturePriorityDelivery is "priority CDN", which is phase 10's media plane.
	FeaturePriorityDelivery = "priority_delivery"
	// FeatureAdvancedSearch is "advanced search (regex, complex boolean, cross-instance)".
	FeatureAdvancedSearch = "advanced_search"
	// FeatureSyncedHistory is "unlimited server-synced history" -- §8.9, phase 7.
	FeatureSyncedHistory = "synced_history"
	// FeatureCustomThemes is "custom UI themes". The three free palettes (system, light, dark) are not this
	// feature and are not gated by it: they exist for everybody and stay that way.
	FeatureCustomThemes = "custom_themes"
	// FeatureSupportBadge is the acknowledgement a subscriber gets beside their name.
	FeatureSupportBadge = "support_badge"
)

// premiumFeature is one catalogue entry: what it sells and which stream it belongs to.
type premiumFeature struct {
	Name   string
	Sells  string
	Stream string
}

// premiumFeatures is §18.1's premium subscription, feature by feature. The stream of each is
// StreamPremiumSubscription except where a feature's own stream has to exist first (ad-free needs ads).
var premiumFeatures = []premiumFeature{
	{
		Name:   FeaturePremiumAPITier,
		Sells:  "a higher API limit for this member's keys: 1200 requests per minute and a burst of 240, against 600 and 120 for everybody else",
		Stream: StreamPremiumSubscription,
	},
	{
		Name:   FeatureAdFree,
		Sells:  "no ad slots on this member's pages",
		Stream: StreamAds,
	},
	{
		Name:   FeaturePriorityDelivery,
		Sells:  "priority delivery of a work's media, when this instance serves media at all",
		Stream: StreamPremiumSubscription,
	},
	{
		Name:   FeatureAdvancedSearch,
		Sells:  "regex and complex boolean search, and search across the instances this one peers with",
		Stream: StreamPremiumSubscription,
	},
	{
		Name:   FeatureSyncedHistory,
		Sells:  "unlimited reading history kept server-side, so it follows a member between devices",
		Stream: StreamPremiumSubscription,
	},
	{
		Name:   FeatureCustomThemes,
		Sells:  "custom UI themes beyond the three palettes every member has",
		Stream: StreamPremiumSubscription,
	},
	{
		Name:   FeatureSupportBadge,
		Sells:  "an acknowledgement beside the member's name",
		Stream: StreamPremiumSubscription,
	},
}

// premiumFeatureByName finds a feature, and reports whether it exists.
func premiumFeatureByName(name string) (premiumFeature, bool) {
	for _, f := range premiumFeatures {
		if f.Name == name {
			return f, true
		}
	}
	return premiumFeature{}, false
}

// PremiumFeatures returns the catalogue's names, for tests and for anything that has to enumerate what this
// version could sell.
func PremiumFeatures() []string {
	names := make([]string, 0, len(premiumFeatures))
	for _, f := range premiumFeatures {
		names = append(names, f.Name)
	}
	return names
}
