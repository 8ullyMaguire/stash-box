package edit

import (
	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/models"
)

// SPEC §7.24.1: "No XP for asserting one on an entity you just edited. Otherwise
// verified-unknown becomes the cheapest XP-per-minute farm in the system — the same shape
// §7.20 refused for vanguards, applied here for the same reason."
//
// This file exists so that rule is decided in ONE place, and that place is the funnel.
//
// recordTrustEvent is the single point every applied edit passes through: an immediate
// moderator accept, a vote that tips the tally, and the cron sweep closing an expired edit
// all reach ApplyEdit, and ApplyEdit calls recordTrustEvent. A rule written at any one of
// those three call sites is a rule the other two do not apply — and the cron sweep in
// particular runs unattended, so it would be the one that silently paid the farm.

// selfAffirming reports whether an author asserted a verified-unknown on an entity they had
// just edited themselves.
//
// The parameter order is (authorOfRecentEdit, assertedAboutEntity) and the arguments read in
// that order at the call site, which is why the tests name them rather than passing bare
// uuids: getting them backwards inverts the rule into "nobody may ever earn XP for an
// assertion", which fails closed and looks like a bug rather than like a farm.
//
// The zero UUID case is handled explicitly. `edit.UserID` is a uuid.NullUUID, so an edit with
// no author carries uuid.Nil, and two independently-missing ids compare equal — which would
// make every anonymous edit self-affirming. Those edits earn no XP at all (recordTrustEvent
// returns before reaching here), but the predicate must not be the thing that stops it: it is
// a reusable helper, and a helper whose safety depends on its caller is one refactor away
// from being the safety.
func selfAffirming(recentEditor, assertedAbout uuid.UUID) bool {
	if recentEditor == uuid.Nil || assertedAbout == uuid.Nil {
		return false
	}
	return recentEditor == assertedAbout
}

// editCarriesVerifiedUnknown reports whether an edit's data contains at least one
// verified-unknown assertion.
//
// The check is on the whole edit, not on any one field: an edit that fills in a title AND
// marks a date unanswerable is an ordinary curation act, and an implementation that read only
// the last field it happened to parse would let that edit through or block it depending on
// field order.
func editCarriesVerifiedUnknown(data *models.SceneEditData) bool {
	if data == nil || data.New == nil {
		return false
	}
	return len(data.New.VerifiedUnknowns) > 0
}

// assertionEarnsTrust is the whole §7.24.1 XP rule as one call, so there is exactly one
// decision rather than two predicates callers can combine incorrectly.
//
// `recentEditor` is who edited this entity before the assertion, and `assertedAbout` is the
// entity the assertion names. They come from different tables -- the edit's author, and
// scene_edits/performer_edits for the entity -- which is why both are passed in rather than
// one being looked up here: the rule is about a RELATIONSHIP between two facts, and a
// function that looked up either of them would need the transaction to see the other's
// uncommitted state.
func assertionEarnsTrust(recentEditor, assertedAbout uuid.UUID, data *models.SceneEditData) bool {
	if !editCarriesVerifiedUnknown(data) {
		return true // an ordinary edit: nothing about §7.24.1 applies
	}
	return !selfAffirming(recentEditor, assertedAbout)
}
