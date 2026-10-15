package content

import (
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/queries"
)

// The rules that need no database: what a key normalizes to, who may decide, and how a row reads for the
// API.
//
// WHY THESE ARE UNIT TESTS AND THE OTHERS ARE NOT. The identity rule is a pure comparison and the key
// normalization is a pure function, so testing them through a database would test the fixture as much as
// the rule -- and would put the rule behind a build tag, where it does not run on every commit. What a
// database IS needed for (that a grant releases, that the appeal survives the release, that a second
// decision is refused) is covered by the wire tests in internal/api/appeal_integration_test.go, which is
// also where the audit trail is asserted.

func appealRow(blockedBy, filedBy uuid.UUID) queries.ContentSourceBlockAppeal {
	row := queries.ContentSourceBlockAppeal{Kind: "url", Value: "https://example.invalid/x"}
	if blockedBy != uuid.Nil {
		row.BlockedByID = uuid.NullUUID{UUID: blockedBy, Valid: true}
	}
	if filedBy != uuid.Nil {
		row.FiledByID = uuid.NullUUID{UUID: filedBy, Valid: true}
	}
	return row
}

// THE RULE. Each case names WHO is trying to decide and what should happen, because a table of booleans
// would not say which arm refused.
func TestTheDeciderIsNeitherTheOperatorWhoBlockedNorTheMemberWhoFiled(t *testing.T) {
	blocker := uuid.Must(uuid.NewV4())
	filer := uuid.Must(uuid.NewV4())
	outsider := uuid.Must(uuid.NewV4())

	cases := []struct {
		name        string
		blockedBy   uuid.UUID
		filedBy     uuid.UUID
		actor       uuid.UUID
		wantErr     error
		refusalSays string
	}{
		{
			name:        "the operator who recorded the block",
			blockedBy:   blocker,
			filedBy:     filer,
			actor:       blocker,
			wantErr:     ErrAppealSelfDecision,
			refusalSays: "you recorded the block",
		},
		{
			name:        "the member who filed the appeal",
			blockedBy:   blocker,
			filedBy:     filer,
			actor:       filer,
			wantErr:     ErrAppealSelfDecision,
			refusalSays: "you filed this appeal",
		},
		{
			name:      "a moderator who did neither",
			blockedBy: blocker,
			filedBy:   filer,
			actor:     outsider,
		},
		{
			name:      "a moderator deciding an appeal against a block whose operator is gone",
			blockedBy: uuid.Nil,
			filedBy:   filer,
			actor:     outsider,
		},
		{
			name:      "a moderator deciding an appeal whose author is gone",
			blockedBy: blocker,
			filedBy:   uuid.Nil,
			actor:     outsider,
		},
		{
			name:        "nobody at all",
			blockedBy:   blocker,
			filedBy:     filer,
			actor:       uuid.Nil,
			wantErr:     ErrAppealSelfDecision,
			refusalSays: "needs a decider",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := appealDeciderAllowed(appealRow(tc.blockedBy, tc.filedBy), tc.actor)
			if tc.wantErr == nil {
				assert.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, tc.wantErr)
			assert.Contains(t, err.Error(), tc.refusalSays,
				"the refusal names which arm fired: the remedy differs between them")
		})
	}
}

// THE KEY. An appeal is filed against what a member typed, and it has to land on the same key the block
// table holds -- which is the pipeline's own normalization, not a second one written here.
func TestAnAppealKeyIsTheKeyThePipelineItselfComputes(t *testing.T) {
	cases := []struct {
		name  string
		kind  string
		value string
		want  string
	}{
		{
			name:  "a URL the way a member submits it",
			kind:  "url",
			value: "https://Example.INVALID/watch/ABC",
			want:  "https://example.invalid/watch/ABC",
		},
		{
			name:  "the same URL with a tracking parameter",
			kind:  "url",
			value: "https://example.invalid/watch/ABC?utm_source=twitter",
			want:  "https://example.invalid/watch/ABC",
		},
		{
			name:  "a URL with surrounding whitespace, because people paste",
			kind:  "url",
			value: "  https://example.invalid/watch/ABC  ",
			want:  "https://example.invalid/watch/ABC",
		},
		{
			name:  "a bare host",
			kind:  "host",
			value: "Example.INVALID",
			want:  "example.invalid",
		},
		{
			name:  "a host written as a URL",
			kind:  "host",
			value: "https://example.invalid/some/page",
			want:  "example.invalid",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key, err := appealKey(tc.kind, tc.value)
			require.NoError(t, err)
			assert.Equal(t, tc.want, key)
		})
	}
}

// And what cannot be keyed is refused rather than stored: an appeal against a string no block can hold
// would sit in the queue forever answering a question about nothing.
func TestAnAppealRefusesAKeyItCannotNormalize(t *testing.T) {
	cases := []struct {
		name  string
		kind  string
		value string
	}{
		{name: "an unknown kind", kind: "works", value: "https://example.invalid/x"},
		{name: "an empty kind", kind: "", value: "https://example.invalid/x"},
		{name: "a URL with no scheme", kind: "url", value: "//example.invalid/x"},
		{name: "a host that is not a host", kind: "host", value: "   "},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := appealKey(tc.kind, tc.value)
			require.ErrorIs(t, err, ErrAppealUnkeyable)
		})
	}
}

// HOW A ROW READS. The API's nulls are reconstructed here rather than in the resolver, and this is the
// test that says which zeros mean "absent" -- a client's `if (outcome)` test is the thing at stake: an
// undecided appeal must not report an empty outcome that reads as a decision.
func TestAPendingAppealReadsAsPending(t *testing.T) {
	blockID := uuid.Must(uuid.NewV4())
	filedBy := uuid.Must(uuid.NewV4())
	blockedBy := uuid.Must(uuid.NewV4())
	filedAt := time.Now().UTC().Truncate(time.Microsecond)

	appeal := AppealToModel(queries.ContentSourceBlockAppeal{
		ID:          blockID,
		BlockID:     uuid.NullUUID{UUID: blockID, Valid: true},
		Kind:        "url",
		Value:       "https://example.invalid/x",
		Statement:   "this is my own work",
		FiledByID:   uuid.NullUUID{UUID: filedBy, Valid: true},
		BlockedByID: uuid.NullUUID{UUID: blockedBy, Valid: true},
		FiledAt:     pgtype.Timestamptz{Time: filedAt, Valid: true},
	})

	assert.Equal(t, blockID.String(), uuid.UUID(appeal.ID).String())
	assert.Equal(t, blockID.String(), uuid.UUID(appeal.BlockID).String())
	assert.Equal(t, filedBy.String(), uuid.UUID(appeal.FiledByID).String())
	assert.Equal(t, blockedBy.String(), uuid.UUID(appeal.BlockedByID).String())
	assert.Equal(t, filedAt, appeal.FiledAt)
	// NIL, not empty: the wire's null is a first-class claim about a pending appeal, and `assert.Empty`
	// passes for an empty string too -- which is what the deployed instance actually sent before the model
	// carried pointers (found by the live walk, not by this test, which is why it now distinguishes).
	assert.Nil(t, appeal.Outcome, "pending: no outcome at all, which serialises as null")
	assert.Nil(t, appeal.DecisionNote)
	assert.Nil(t, appeal.DecidedAt, "and no decision time -- not the zero time, which a client would render")
}

// A decided appeal carries all four decision values, which is what the table's CHECK constraints guarantee
// -- the model cannot be asked to represent a half-decided row.
func TestADecidedAppealReadsAsDecided(t *testing.T) {
	id := uuid.Must(uuid.NewV4())
	decidedBy := uuid.Must(uuid.NewV4())
	decidedAt := time.Now().UTC().Truncate(time.Microsecond)
	note := "the notice named the wrong uploader"

	outcome := AppealGranted
	appeal := AppealToModel(queries.ContentSourceBlockAppeal{
		ID:           id,
		Kind:         "host",
		Value:        "example.invalid",
		Statement:    "please review",
		DecidedByID:  uuid.NullUUID{UUID: decidedBy, Valid: true},
		Outcome:      &outcome,
		DecisionNote: &note,
		DecidedAt:    pgtype.Timestamptz{Time: decidedAt, Valid: true},
	})

	require.NotNil(t, appeal.Outcome)
	assert.Equal(t, AppealGranted, *appeal.Outcome)
	require.NotNil(t, appeal.DecisionNote)
	assert.Equal(t, note, *appeal.DecisionNote)
	assert.Equal(t, decidedBy.String(), uuid.UUID(appeal.DecidedByID).String())
	require.NotNil(t, appeal.DecidedAt)
	assert.Equal(t, decidedAt, *appeal.DecidedAt)
	assert.Equal(t, modelsIDNilString(), uuid.UUID(appeal.BlockID).String(),
		"a released block leaves no reference, and that nil is the model's honest statement about it")
}

func modelsIDNilString() string { return uuid.Nil.String() }

// The unique-violation check reads the SQLSTATE, not the message: the code is the contract Postgres offers
// and the prose changes between versions.
func TestOnlyAUniqueViolationIsReportedAsOne(t *testing.T) {
	assert.True(t, isUniqueViolation(&pgconn.PgError{Code: "23505"}))
	assert.False(t, isUniqueViolation(&pgconn.PgError{Code: "23503"}), "a foreign-key violation is not this")
	assert.False(t, isUniqueViolation(assert.AnError), "and neither is an arbitrary error")
}
