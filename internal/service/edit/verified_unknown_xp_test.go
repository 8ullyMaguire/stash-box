package edit

import (
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/stashapp/stash-box/internal/models"
)

// SPEC §7.24.1: "No XP for asserting one on an entity you just edited. Otherwise
// verified-unknown becomes the cheapest XP-per-minute farm in the system — the same shape
// §7.20 refused for vanguards, applied here for the same reason."
//
// The rule is DECIDED HERE, not in the trust service and not in the caller, because
// recordTrustEvent is the single funnel every applied edit passes through: an immediate
// moderator accept, a vote that tips the tally, and the cron sweep closing an expired edit
// all reach it. A rule expressed at any one of those call sites is a rule the other two skip.
//
// Why it has to be checked here at all, and not simply "not be an edit": an assertion IS an
// edit, on purpose. §7.24.1 makes it attach to the existing consensus machinery so a curator
// with trust earns auto-approval, because "not publicly knowable" is a low-risk, high-value
// claim. The XP rule is the narrow exception inside that deliberate design.

func TestAVerifiedUnknownAssertionEarnsNoTrustOnTheEntityTheAuthorJustEdited(t *testing.T) {
	user := uuid.Must(uuid.NewV4())
	entity := uuid.Must(uuid.NewV4())

	assert.False(t,
		selfAffirming(user, entity),
		"a curator asserting a field unknown on the entity they just edited is the cheapest "+
			"XP-per-minute farm available, so it must earn nothing")
}

func TestAnAssertionAboutSomebodyElsesEntityIsNotSelfAffirming(t *testing.T) {
	// The normal case, and the whole reason §7.24.1 routes assertions through consensus: a
	// curator asserting a field unanswerable on an entity they did NOT touch earns trust,
	// because that is the behaviour the feature is for.
	//
	// The first version of this test asserted the OPPOSITE -- that different users are
	// "self-affirming" -- which is the predicate read as if it meant "earn nothing". It
	// failed, correctly, because taken with the sibling test it says both that equal ids and
	// unequal ids are self-affirming, i.e. that the predicate is always true.
	recentEditor := uuid.Must(uuid.NewV4())
	otherAuthor := uuid.Must(uuid.NewV4())

	assert.False(t,
		selfAffirming(recentEditor, otherAuthor),
		"an assertion about an entity the author did not touch is not self-affirming, so it "+
			"earns XP -- this is the case the feature exists for")

	untouched := uuid.Must(uuid.NewV4())
	assert.False(t,
		selfAffirming(uuid.Must(uuid.NewV4()), untouched),
		"an assertion about a wholly unrelated entity is not self-affirming")
}

func TestSelfAffirmingIsNeverTrueForTheZeroUUIDs(t *testing.T) {
	// A missing id must not accidentally equal itself. `edit.UserID` is a uuid.NullUUID, so
	// an edit with no author carries the zero UUID -- and two independently-missing ids
	// would compare equal, making every anonymous edit look self-affirming. Anonymous edits
	// earn no XP anyway (recordTrustEvent returns early), but the predicate must not be the
	// thing that stops it: one refactor later it could be reused where it matters.
	zero := uuid.Nil
	assert.False(t, selfAffirming(zero, zero),
		"the zero UUID compared equal to itself, so a missing author looks self-affirming")
}

func TestAVerifiedUnknownEditIsRecognisedInBothDataShapes(t *testing.T) {
	// The edit's data is jsonb, and the assertion rides inside it. A rule that only reads one
	// shape is a rule that silently stops applying when the second shape is the one a caller
	// happens to build. Note the entity id is NOT in this data -- it lives in scene_edits --
	// which is why assertionEarnsTrust takes it as a parameter.
	withNested := models.SceneEditData{
		New: &models.SceneEdit{
			VerifiedUnknowns: []models.VerifiedUnknownInput{
				{Field: "date", ReasonCode: "not_publicly_knowable"},
			},
		},
	}
	assert.True(t, editCarriesVerifiedUnknown(&withNested),
		"a scene edit carrying verified_unknowns was not recognised")

	without := models.SceneEditData{New: &models.SceneEdit{}}
	assert.False(t, editCarriesVerifiedUnknown(&without),
		"an ordinary edit was reported as carrying a verified-unknown")
}

func TestAnEditCarryingBothAValueAndAVerifiedUnknownIsStillAVerifiedUnknown(t *testing.T) {
	// Not mutually exclusive in the data, and the XP rule must not depend on them being so.
	// An edit that fills in one field and marks another unknown is a normal curation act,
	// and the XP rule reads the whole edit rather than the last field it happens to parse.
	title := "a title"
	data := models.SceneEditData{
		New: &models.SceneEdit{
			Title: &title,
			VerifiedUnknowns: []models.VerifiedUnknownInput{
				{Field: "date", ReasonCode: "not_yet_looked"},
			},
		},
	}
	assert.True(t, editCarriesVerifiedUnknown(&data),
		"an edit that both sets a value and marks a field unknown was not recognised")
}

func TestAssertionEarnsTrustIsTheWholeRuleInOneCall(t *testing.T) {
	self, other := uuid.Must(uuid.NewV4()), uuid.Must(uuid.NewV4())
	unknownEdit := &models.SceneEditData{
		New: &models.SceneEdit{
			VerifiedUnknowns: []models.VerifiedUnknownInput{
				{Field: "date", ReasonCode: "not_publicly_knowable"},
			},
		},
	}
	ordinaryEdit := &models.SceneEditData{New: &models.SceneEdit{}}

	assert.False(t, assertionEarnsTrust(self, self, unknownEdit),
		"a self-affirming assertion earned XP, which is the farm §7.24.1 names")
	assert.True(t, assertionEarnsTrust(other, self, unknownEdit),
		"an assertion about someone else's entity earned nothing, so nobody can ever earn XP")
	assert.True(t, assertionEarnsTrust(self, self, ordinaryEdit),
		"an ordinary self-edit earned nothing, which would make every edit worthless")
	assert.True(t, assertionEarnsTrust(uuid.Nil, uuid.Nil, unknownEdit),
		"an anonymous assertion was treated as self-affirming")
}

func TestAnEditWithNoNewDataIsNotAVerifiedUnknown(t *testing.T) {
	// Found by the mutation gate: deleting the `data.New == nil` guard makes this panic, and
	// no test reached it -- every other test passes a populated New. An edit with no
	// new_data is not hypothetical: a CREATE-shaped edit, or any edit whose data failed to
	// unmarshal into the expected shape, arrives with New nil, and the XP path runs for
	// EVERY applied edit. A nil dereference there takes down curation rather than one edit.
	assert.NotPanics(t, func() {
		assert.False(t, editCarriesVerifiedUnknown(&models.SceneEditData{}),
			"an edit with no new_data reported a verified-unknown")
		assert.True(t,
			assertionEarnsTrust(uuid.Must(uuid.NewV4()), uuid.Must(uuid.NewV4()),
				&models.SceneEditData{}),
			"an edit with no new_data was treated as self-affirming, so an ordinary edit "+
				"that failed to unmarshal would stop earning XP")
	})
}

func TestANilEditDataIsNotAVerifiedUnknown(t *testing.T) {
	assert.NotPanics(t, func() {
		assert.False(t, editCarriesVerifiedUnknown(nil))
		assert.True(t, assertionEarnsTrust(uuid.Must(uuid.NewV4()), uuid.Must(uuid.NewV4()), nil))
	})
}
