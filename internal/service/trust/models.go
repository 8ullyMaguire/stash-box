package trust

import (
	"github.com/gofrs/uuid"
)

// KindEnum identifies what a trust event was for.
//
// Free text in the database (migration 76) and a closed set here, deliberately:
// the set grows with each roadmap phase, but the Go side should not have to
// accept arbitrary strings that silently do nothing. An unrecognised kind is
// recorded in trust_events for the audit trail and ignored by the rollup.
type KindEnum string

const (
	// KindEditApproved is a contributor's edit that was applied.
	KindEditApproved KindEnum = "edit_approved"
	// KindEditRejected is an edit that was closed without being applied.
	KindEditRejected KindEnum = "edit_rejected"
	// KindIdentificationSolved is a solved identification-board query.
	KindIdentificationSolved KindEnum = "identification_solved"
	// KindQuestCompleted is a finished curation quest.
	KindQuestCompleted KindEnum = "quest_completed"
	// KindReplicaHosted is a replica kept alive for mesh preservation.
	KindReplicaHosted KindEnum = "replica_hosted"
)

// AllKinds is every kind the rollup knows how to count.
//
// Kept as a slice rather than derived, so a kind added here without a matching
// case in the SQL CASE expressions is a visible omission rather than a silent
// one.
var AllKinds = []KindEnum{
	KindEditApproved,
	KindEditRejected,
	KindIdentificationSolved,
	KindQuestCompleted,
	KindReplicaHosted,
}

// PointsPerKind is how much each contribution is worth toward a level.
//
// This is a product decision and the numbers are argued about: they are the
// difference between "contribute a lot" and "curate well". They live in one
// exported map, with one place to change them, because a threshold curve
// scattered across a switch statement is a curve nobody can reason about.
//
// Weights are deliberately unequal. An applied edit is the highest-value
// signal available -- it is verified, it changed the archive, and it cost the
// contributor real time. Merely having an edit rejected is barely a signal at
// all, so it is worth almost nothing; a high rejection rate is caught by the
// level never rising, not by punishing the contributor.
var PointsPerKind = map[KindEnum]int{
	KindEditApproved:         10,
	KindEditRejected:         1,
	KindIdentificationSolved: 15,
	KindQuestCompleted:       20,
	KindReplicaHosted:        25,
}

// LevelEnum is a trust tier (SPEC §6).
//
// Distinct from models.RoleEnum, which is an authorization primitive checked by
// the auth middleware. Trust is reputation; a role is a permission. They are
// kept apart because "may not moderate" and "has not earned trust yet" are
// different statements, and merging them makes the first unrepresentable once
// data exists.
type LevelEnum int

const (
	// LevelPublic is the anonymous/default level: browse directory, rankings,
	// reviews, basic metadata and snapshot collages.
	LevelPublic LevelEnum = 0
	// LevelRegistered can vote in Elo matchups, submit edits, write reviews,
	// flag duplicates and post on the identification board.
	LevelRegistered LevelEnum = 1
	// LevelContributor has trusted edits auto-approve, sees expanded collages
	// and storyboards, and earns XP and badges.
	LevelContributor LevelEnum = 2
	// LevelCurator can merge duplicates, approve edits, manage tags, claim
	// curation quests and access full snapshot sets.
	LevelCurator LevelEnum = 3
	// LevelArchivist can opt in to content viewing, streaming, download and
	// upload, and can host replicas for the mesh.
	LevelArchivist LevelEnum = 4
	// LevelSteward has governance, API keys, awards voting, instance gravity
	// tuning and preservation policy input.
	LevelSteward LevelEnum = 5
)

// LevelContentViewing is the level at which a user MAY opt in to content
// viewing (SPEC §6).
//
// A constant rather than a config value because it is a product boundary, not a
// tuning knob: it is the line between "can see the archive" and "can see the
// media", and an operator loosening it should be a deliberate code change.
const LevelContentViewing = LevelArchivist

// threshold is the minimum points required to reach a level.
type threshold struct {
	level  LevelEnum
	points int
}

// thresholds is the level curve, in ascending order.
//
// The shape is the decision, and it is deliberately steep at the top:
//
//	L0 -> 1     0 points    every registered user
//	L1 -> 2     10 points   roughly one applied edit
//	L2 -> 3     50 points   about five applied edits
//	L3 -> 4     200 points  a consistently good contributor
//	L4 -> 5     750 points  a curator the community relies on
//	L5 -> 6     2500 points deliberately hard
//
// Level 4 (Archivist) is the point at which a user can opt in to viewing
// content, so it must be hard to reach by volume alone -- otherwise a
// spambot that mass-subjects acceptable edits unlocks media access. That is why
// L4 costs 3x L3 rather than 2x, and why L5 costs over 3x L4.
//
// Exported through LevelForPoints so there is exactly one implementation of
// "which level is this many points".
var thresholds = []threshold{
	{level: LevelPublic, points: 0},
	{level: LevelRegistered, points: 10},
	{level: LevelContributor, points: 50},
	{level: LevelCurator, points: 200},
	{level: LevelArchivist, points: 750},
	{level: LevelSteward, points: 2500},
}

// Totals is the rollup a level is derived from.
//
// Mirrors the user_trust columns rather than embedding the generated struct, so
// the level calculation has no database dependency and is testable without one.
type Totals struct {
	ApprovedEdits        int
	RejectedEdits        int
	IdentificationSolves int
	QuestsCompleted      int
	ReplicasHosted       int
}

// Points is the weighted total of everything the user has contributed.
//
// Rejected edits count at a small positive weight rather than a negative one.
// Subtracting them would let a new user who posts one bad edit drop below zero
// and lose access they had already earned, and it would make the score depend
// on the order events were applied. A low rejection rate is already expressed
// by approved_edits staying low, which is the signal that actually matters.
func (t Totals) Points() int {
	return t.ApprovedEdits*PointsPerKind[KindEditApproved] +
		t.RejectedEdits*PointsPerKind[KindEditRejected] +
		t.IdentificationSolves*PointsPerKind[KindIdentificationSolved] +
		t.QuestsCompleted*PointsPerKind[KindQuestCompleted] +
		t.ReplicasHosted*PointsPerKind[KindReplicaHosted]
}

// LevelForPoints maps a point total to a level.
//
// The single implementation of the curve. Returns LevelPublic for a total below
// the first threshold, and never exceeds the highest threshold: a user with an
// absurd total is a Steward, not a level 9000 that no switch statement handles.
func LevelForPoints(points int) LevelEnum {
	level := LevelPublic
	for _, t := range thresholds {
		if points < t.points {
			// thresholds is ascending, so the first unmet threshold ends the walk.
			break
		}
		level = t.level
	}
	return level
}

// LevelForTotals maps a rollup to a level.
func LevelForTotals(t Totals) LevelEnum {
	return LevelForPoints(t.Points())
}

// Event is one recorded contribution, as accepted by the service.
type Event struct {
	UserID uuid.UUID
	Kind   KindEnum
	// Delta is signed: +1 earns trust, -1 takes it away. The kind determines
	// which rollup column moves; the delta determines the direction.
	Delta int
	// EntityType and EntityID are optional. They exist so a reversal can find
	// the event it reverses, and they are part of the dedup key.
	EntityType string
	EntityID   *uuid.UUID
}
