package elo

import (
	"github.com/gofrs/uuid"
)

// EntityType is what a rating is for.
//
// A closed set in Go, free text in the database, for the same reason
// trust.KindEnum is: the set grows with each roadmap phase (SPEC §9 ranks
// performers, scenes, studios, sites, tags, lists and instances), but the service
// should not accept arbitrary strings that silently do nothing. The database
// column stays unconstrained because migration 77 stores entity_type as a plain
// varchar to avoid seven nullable FK columns, and an unrated or merged entity
// must not be a foreign-key violation.
type EntityType string

const (
	// EntityPerformer is a ranked performer. The only type wired up in this
	// phase.
	EntityPerformer EntityType = "performer"
	// EntityScene is a ranked scene. SPEC §9.
	EntityScene EntityType = "scene"
	// EntityStudio is a ranked studio. SPEC §9.
	EntityStudio EntityType = "studio"
	// EntitySite is a ranked site. Phase 3, with the directory.
	EntitySite EntityType = "site"
	// EntityTag is a ranked tag. SPEC §9.
	EntityTag EntityType = "tag"
	// EntityList is a ranked list. SPEC §9.
	EntityList EntityType = "list"
	// EntityInstance is a ranked instance, which is what makes taste-based
	// peering (SPEC §2) computable. Phase 4, when instances have identity here.
	EntityInstance EntityType = "instance"
)

// AllEntityTypes is every type the service will accept.
//
// Kept as a slice rather than derived from a map, so a type added to the
// constants without being added here is a visible omission rather than a silent
// one -- the same reasoning as trust.AllKinds.
var AllEntityTypes = []EntityType{
	EntityPerformer,
	EntityScene,
	EntityStudio,
	EntitySite,
	EntityTag,
	EntityList,
	EntityInstance,
}

// Valid reports whether an entity type is one this version knows.
//
// A vote naming an unknown type is refused rather than recorded, which is the
// OPPOSITE of how trust.ErrUnknownKind behaves and deliberately so. A trust
// event is an audit-trail entry that a later version may still be able to count,
// so discarding it loses history. An Elo vote is not: it has to move a rating,
// and a vote that cannot move one is a vote the system accepted and then ignored,
// which is worse than refusing it. The caller gets an error instead.
func (e EntityType) Valid() bool {
	for _, known := range AllEntityTypes {
		if e == known {
			return true
		}
	}
	return false
}

// Entity identifies one thing being rated.
type Entity struct {
	Type EntityType
	ID   uuid.UUID
}

// Matchup is a proposed pair to vote on, before the user has chosen.
//
// Both sides are returned in a fixed order by the caller, and the order is part
// of the data rather than an accident: position bias -- a tendency to pick
// whichever performer is shown first -- is the easiest way to game a pairwise
// vote, and it is only measurable if the order the user saw is recorded. That is
// what WinnerID/LoserID in elo_votes mean by "winner": which slot was picked,
// not which performer is better. See VoteInput.PickedSide.
type Matchup struct {
	EntityType EntityType
	// Left and Right are the two candidates, in the order they will be shown.
	Left  Entity
	Right Entity
}

// PickedSide is which slot a user chose.
type PickedSide int

const (
	// PickedLeft means the user chose the performer shown on the left.
	PickedLeft PickedSide = 0
	// PickedRight means the user chose the performer shown on the right.
	PickedRight PickedSide = 1
)

// Valid reports whether the side is one of the two.
func (p PickedSide) Valid() bool {
	return p == PickedLeft || p == PickedRight
}

// VoteInput is a recorded matchup vote.
//
// The caller passes the two candidates IN DISPLAY ORDER plus which one the user
// picked, and the service works out the storage order itself. Taking a
// pre-resolved winner/loser pair instead would push the ordering decision to
// every call site, and a call site that got it backwards would record the vote
// correctly and display it wrongly -- a bug with no test failure anywhere.
type VoteInput struct {
	UserID     uuid.UUID
	Matchup    Matchup
	PickedSide PickedSide
	// ElapsedDaysA and ElapsedDaysB are how long each side has been unrated. They
	// differ, because a performer nobody has voted on for a year is not in the
	// same position as one rated last week, and the whole point of Glicko-2 over
	// Elo is that a gap in play increases uncertainty.
	ElapsedDaysA float64
	ElapsedDaysB float64
}

// ErrUnknownEntityType is returned when a vote names a type this version does not
// know. See EntityType.Valid for why this refuses rather than records.
var ErrUnknownEntityType = errUnknownEntityType{}

type errUnknownEntityType struct{}

func (errUnknownEntityType) Error() string {
	return "unknown elo entity type"
}

// ErrSameEntity is returned when a matchup names the same entity twice.
//
// The database CHECK constraint catches this too, but catching it here returns a
// useful error instead of a constraint-violation string, and -- more importantly
// -- the check happens before either rating is loaded, so a nonsense matchup
// cannot move anything on its way to being rejected.
var ErrSameEntity = errSameEntity{}

type errSameEntity struct{}

func (errSameEntity) Error() string {
	return "a matchup must be between two different entities"
}

// ErrInvalidSide is returned when PickedSide is neither 0 nor 1.
var ErrInvalidSide = errInvalidSide{}

type errInvalidSide struct{}

func (errInvalidSide) Error() string {
	return "picked side must be left (0) or right (1)"
}
