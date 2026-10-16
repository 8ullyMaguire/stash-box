package edit

import (
	"encoding/json"

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
	return assertionEarnsTrustFor(recentEditor, assertedAbout, editCarriesVerifiedUnknown(data))
}

// assertionEarnsTrustFor is the ruling itself, parameterised by the ONE fact it needs: whether the
// edit carries an assertion at all.
//
// It exists as a separate function because the funnel (recordTrustEvent) holds an edit whose data is
// raw jsonb rather than a *SceneEditData, and an entity type that is not necessarily a scene. Two
// callers with two copies of "if it carries one, and the editor is the same person, then no" is how
// the rule drifts; so both entry points below reduce to this one line.
func assertionEarnsTrustFor(recentEditor, assertedAbout uuid.UUID, carriesUnknown bool) bool {
	if !carriesUnknown {
		return true // an ordinary edit: nothing about §7.24.1 applies
	}
	return !selfAffirming(recentEditor, assertedAbout)
}

// assertionEarnsTrustRaw is the funnel's entry point: an edit's data as stored, for any entity type.
//
// It reads the assertion out of the raw jsonb rather than unmarshalling the whole edit, so it works
// for a target type whose edit struct this package does not import -- and so that a future entity
// type that adopts the field is covered by this rule the day its struct gains the key, rather than
// the day somebody remembers to extend a type switch.
func assertionEarnsTrustRaw(recentEditor, assertedAbout uuid.UUID, raw []byte) bool {
	return assertionEarnsTrustFor(recentEditor, assertedAbout, editDataCarriesVerifiedUnknown(raw))
}

// editDataCarriesVerifiedUnknown reports whether raw edit data (the jsonb `data` column) carries at
// least one verified-unknown assertion, whatever the entity type.
//
// A malformed or empty payload reports FALSE -- it carries no assertion, so the rule does not apply
// -- rather than erroring. An edit whose data failed to unmarshal must not stop earning trust:
// assertionsEarnsTrustFor's own test has this case for the typed path, and this is the same case for
// the raw one.
func editDataCarriesVerifiedUnknown(raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	var probe struct {
		New *struct {
			VerifiedUnknowns []models.VerifiedUnknownInput `json:"verified_unknowns"`
		} `json:"new_data"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return false
	}
	return probe.New != nil && len(probe.New.VerifiedUnknowns) > 0
}
