package elo

import (
	"strings"

	"github.com/gofrs/uuid"
	"github.com/stashapp/stash-box/internal/queries"
)

// The taste vector is the per-user preference profile of SPEC §2: "computed from
// Elo votes, reviews, tags, curation history, opt-in viewing signals, search
// behavior, and instance gravity".
//
// This file covers the Elo-vote term of that, which is the only term with data
// behind it in this phase. The others are listed below with the reason they are
// not here yet, because "taste is computed from six things" is a claim that will
// quietly become false-looking if five of them are silently missing.

// TasteKey prefixes. A taste key is "<prefix>:<entity uuid>".
//
// PER-ENTITY, not per-kind, and this is the load-bearing design decision rather
// than a detail. SPEC §4 requires ranking results by "personal taste", and a
// vector that only says "this user likes performers, +3" cannot produce a
// recommendation for a *specific* performer, which is the thing §4 asks for. So
// the entity id is the key and the kind is the prefix, which makes the vector
// sparse and grows with votes -- hence JSONB, and hence the vote_count stored
// beside it, because a recommender has to be able to tell three votes from three
// hundred.
//
// The cost of per-entity keys is that two entities of different kinds are not
// directly comparable, so cross-kind ranking needs a normalisation step. That is
// the point at which the missing terms (reviews, tags) earn their place: they are
// what give a performer and a studio a common scale. The prefix split is the seam
// where that gets added, and keeping the kinds in separate key spaces is what
// makes it possible to add it without a data migration.
const (
	// TasteKeyPerformer is the prefix for performer preferences. Wired up now.
	TasteKeyPerformer = "performer"
	// TasteKeyScene is the prefix for scene preferences. SPEC §9 ranks scenes, so
	// the rating side is already live for any type a caller votes on; the
	// preference side arrives with the matchup UI.
	TasteKeyScene = "scene"
	// TasteKeyStudio is the prefix for studio preferences.
	TasteKeyStudio = "studio"
	// TasteKeySite is the prefix for site preferences. Phase 3, with the
	// directory.
	TasteKeySite = "site"
	// TasteKeyTag is the prefix for tag preferences. Notable because a tag
	// preference is a *content* signal rather than a name signal: liking a
	// performer's tags is what the recommendation engine in §4 wants, and tags are
	// also the only taste term that generalises to entities nobody has voted on.
	TasteKeyTag = "tag"
	// TasteKeyList is the prefix for list preferences.
	TasteKeyList = "list"
	// TasteKeyInstance is the prefix for instance preferences -- the input to
	// taste-based peering in §2, where instances peer on taste similarity.
	TasteKeyInstance = "instance"
)

// prefixFor maps an entity type to its taste key prefix.
//
// A separate function rather than string(entityType) because the prefixes are the
// STORED keys: a future rename of EntityScene must not silently orphan every
// user's scene preferences. Routing through one function means the rename has to
// be handled here, in one place, where the storage consequences are visible.
func prefixFor(t EntityType) string {
	switch t {
	case EntityPerformer:
		return TasteKeyPerformer
	case EntityScene:
		return TasteKeyScene
	case EntityStudio:
		return TasteKeyStudio
	case EntitySite:
		return TasteKeySite
	case EntityTag:
		return TasteKeyTag
	case EntityList:
		return TasteKeyList
	case EntityInstance:
		return TasteKeyInstance
	default:
		// Reached only if a type is added to AllEntityTypes without a prefix. A
		// missing prefix is a bug that would write unreachable keys, so it falls
		// back to the raw type -- visible garbage in the data beats a panic in the
		// vote path, and Vote's Valid() check means the default is unreachable
		// today anyway.
		return string(t)
	}
}

// tasteKey builds the key for one entity.
func tasteKey(t EntityType, id uuid.UUID) string {
	return prefixFor(t) + ":" + id.String()
}

// splitTasteKey is the inverse of tasteKey: it returns the prefix and the entity
// id behind a stored key.
//
// Exists because a recommender has to go the other way. "Rank this performer by
// the user's taste" starts from an entity id and needs the key; "what does this
// user like" starts from the keys and needs the ids. Without this, the second
// direction is a string split written at every call site, and the separator ends
// up defined twice.
//
// A key with no separator, or with a prefix that does not parse as a UUID, is
// rejected rather than partially returned. A recommender handed a half-parsed key
// would rank against the wrong entity, which is worse than ranking against
// nothing.
func splitTasteKey(key string) (prefix string, id uuid.UUID, ok bool) {
	before, after, found := strings.Cut(key, ":")
	if !found || before == "" || after == "" {
		return "", uuid.Nil, false
	}
	parsed, err := uuid.FromString(after)
	if err != nil {
		return "", uuid.Nil, false
	}
	return before, parsed, true
}

// winnerType and loserType read the stored types off a vote row.
//
// elo_votes has winner_type/loser_type rather than a single entity_type because
// the matchup is between two entities of the same type today, but the schema
// allows the two to differ and does not constrain them. A vote between a
// performer and a studio is nonsense for rating and refused upstream, yet the
// column pair exists so that widening the semantics later is a code change and
// not a migration.
//
// Reading them per-row rather than assuming the vote's type is the service's
// matchup type means a mixed-kind row in the log still produces a well-formed
// vector, with each side keyed under its own prefix.
func winnerType(v queries.EloVote) EntityType { return EntityType(v.WinnerType) }
func loserType(v queries.EloVote) EntityType  { return EntityType(v.LoserType) }
