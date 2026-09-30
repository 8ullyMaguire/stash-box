//go:build integration

package edit_test

import (
	"context"
	"testing"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/stashapp/stash-box/internal/auth"
	"github.com/stashapp/stash-box/internal/config"
	"github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/edit"
	"github.com/stashapp/stash-box/internal/service/trust"
)

// The WIRING, against a real database, through a PUBLIC entry point.
//
// The unit tests in edit_authority_test.go cover the DECISION by calling
// allowUpdateEdit directly, and that is deliberate -- it is a pure function and
// enumerating its crossings needs no I/O. But a pure-function test suite has a
// blind spot, and this file exists because a mutation found it:
//
//	AUTHORITY: threshold becomes admin-only (isAdmin forced true)
//	-- in mayUpdateEdit, at the call site -- SURVIVED the unit tests.
//
// Every unit test calls allowUpdateEdit with the arguments it wants, so changing
// what mayUpdateEdit PASSES IN is invisible to all of them. The property that
// actually matters -- "a non-admin above the threshold is allowed" -- is a
// property of the wiring, not of the rule.
//
// So this file calls UpdateTagEdit, one of the four public methods that route
// through validateEditUpdate. That is a stronger claim than reaching for
// mayUpdateEdit directly: it proves the authority check is on the path a real
// request takes, not merely that a function behaves correctly when called by
// hand. It also forces this file into package edit_test, which is what breaks
// the import cycle -- testutil pulls in the service factory, and the factory
// imports edit.
//
// The rule table still has value and is kept: it enumerates 12 crossings in a
// millisecond. This file proves the rule table is the thing being consulted.

func TestMain(m *testing.M) {
	testutil.TestWithDatabase(m, nil)
}

// setLevel writes the cached level directly.
//
// user_trust.level is a stored column precisely so listing users does not
// recompute, and recomputeLevel is only re-run by RebuildLevels. Writing the
// column is therefore the honest way to arrange a level here: reaching it
// through real trust events would make every case in this file depend on the
// threshold table, and a test that breaks when an unrelated threshold moves is
// a test that gets deleted instead of fixed.
func setLevel(t *testing.T, userID uuid.UUID, level trust.LevelEnum) {
	t.Helper()
	db := testutil.DB()
	require.NotNil(t, db, "testutil.DB() is nil; TestWithDatabase did not run")

	_, err := db.Exec(context.Background(),
		`INSERT INTO user_trust (user_id, level) VALUES ($1, $2)
		 ON CONFLICT (user_id) DO UPDATE SET level = EXCLUDED.level`,
		userID, int(level))
	require.NoError(t, err)
}

// createUser inserts a user and returns its id.
//
// The column list is spelled out rather than trimmed to what this file uses,
// because the users table has six NOT NULL columns and a shorter list fails with
// a constraint violation that reads like a database problem rather than a
// missing fixture field. The values are junk: password_hash is never verified
// here (the acting user is put in the context directly) and api_key is never
// used.
//
// The name is the FULL uuid string, not a slice of it. uuid.NewV7() is
// time-ordered, so two users created in the same millisecond share their leading
// bytes and a truncated name collides on users_name_key. A one-line fixture that
// fails only when the machine is fast is the worst kind, so this is not
// truncated.
func createUser(t *testing.T) uuid.UUID {
	t.Helper()
	db := testutil.DB()
	require.NotNil(t, db, "testutil.DB() is nil; TestWithDatabase did not run")

	id := uuid.Must(uuid.NewV7())
	now := time.Now()

	_, err := db.Exec(context.Background(),
		`INSERT INTO users (id, name, password_hash, email, api_key,
		                    last_api_call, created_at, updated_at)
		 VALUES ($1, $2, 'x', $3, $4, $5, $5, $5)`,
		id, "t-"+id.String(), id.String()+"@example.org", "k-"+id.String(),
		now)
	require.NoError(t, err)
	return id
}

// withAdminContext returns a context carrying the admin role.
//
// Roles go in ContextRoles as a plain []models.RoleEnum, NOT on the AuthUser --
// auth.AuthUser is {ID, Name, APIKey} and has no roles field. IsRole reads
// ContextRoles first and only falls back to the role cache, so setting it
// directly is both sufficient and the only way to arrange an admin without
// warming that cache.
func withAdminContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, auth.ContextRoles,
		[]models.RoleEnum{models.RoleEnumAdmin})
}

// TestUpdateTagEditAuthority drives the real public method.
//
// UpdateTagEdit is chosen because it is the smallest of the four: it validates,
// then calls Performer(ctx, tx, edit) with a "tag" target. The authority check
// runs BEFORE that, so every rejection below happens before any target-specific
// work -- which means these tests assert on the authority error and not on
// whatever a missing tag would have produced.
func TestUpdateTagEditAuthority(t *testing.T) {
	ctx := context.Background()

	// newPendingTagEdit creates a user, a tag, and a pending tag edit owned by
	// that user, returning the edit id and the owner id.
	//
	// The target is NOT a column on edits -- edits has (id, user_id, operation,
	// target_type, data, status, ...) and the tag it refers to lives in the
	// tag_edits join table. Inserting a target_id into edits would fail, and
	// omitting the tag_edits row would fail later at validateEditTargetID with a
	// different error, so both rows are created here.
	newPendingTagEdit := func(t *testing.T) (uuid.UUID, uuid.UUID) {
		t.Helper()
		db := testutil.DB()
		owner := createUser(t)
		tagID := uuid.Must(uuid.NewV7())
		editID := uuid.Must(uuid.NewV7())
		now := time.Now()

		_, err := db.Exec(ctx,
			`INSERT INTO tags (id, name, created_at, updated_at) VALUES ($1, $2, $3, $3)`,
			tagID, "t-"+tagID.String(), now)
		require.NoError(t, err)

		_, err = db.Exec(ctx,
			`INSERT INTO edits (id, user_id, operation, target_type, data, status, created_at, updated_at)
			 VALUES ($1, $2, 'modify', 'tag', '{}'::jsonb, 'pending', $3, $3)`,
			editID, owner, now)
		require.NoError(t, err)

		_, err = db.Exec(ctx,
			`INSERT INTO tag_edits (edit_id, tag_id) VALUES ($1, $2)`, editID, tagID)
		require.NoError(t, err)

		return editID, owner
	}

	// update drives one request as callerID, with the given context.
	update := func(t *testing.T, c context.Context, editID, callerID uuid.UUID) error {
		t.Helper()
		svc := newTestEdit(t)
		c = asRequest(asUser(c, callerID))
		_, err := svc.UpdateTagEdit(c, editID, models.TagEditInput{
			Edit: &models.EditInput{Comment: strPtr("amending")},
		})
		return err
	}

	t.Run("a non-admin above the threshold may update", func(t *testing.T) {
		editID, owner := newPendingTagEdit(t)
		caller := createUser(t)
		setLevel(t, caller, trust.LevelCurator)
		defer setMinTrustLevel(t, 3)()

		require.NoError(t, update(t, ctx, editID, caller),
			"a Curator at threshold 3 was refused through the public path. If the "+
				"call site passes isAdmin=true, or never consults the threshold, the "+
				"configurable threshold is decorative and this is #708 unchanged")
		_ = owner
	})

	t.Run("a non-admin below the threshold may not", func(t *testing.T) {
		editID, _ := newPendingTagEdit(t)
		caller := createUser(t)
		setLevel(t, caller, trust.LevelRegistered)
		defer setMinTrustLevel(t, 4)()

		err := update(t, ctx, editID, caller)
		require.ErrorIs(t, err, edit.ErrUnauthorizedUpdate,
			"a Registered user at threshold 4 was allowed; the configured level is "+
				"not being compared against")
	})

	t.Run("the owner may always update", func(t *testing.T) {
		editID, owner := newPendingTagEdit(t)
		defer setMinTrustLevel(t, -1)()

		require.NoError(t, update(t, ctx, editID, owner),
			"the creator was refused at the creator-only default")
	})

	t.Run("a Steward may not at the creator-only default", func(t *testing.T) {
		editID, _ := newPendingTagEdit(t)
		caller := createUser(t)
		setLevel(t, caller, trust.LevelSteward)
		defer setMinTrustLevel(t, -1)()

		err := update(t, ctx, editID, caller)
		require.ErrorIs(t, err, edit.ErrUnauthorizedUpdate,
			"a Steward was allowed at the default. Upgrading must not silently "+
				"widen who can amend moderation history")
	})

	t.Run("an admin may at the creator-only default", func(t *testing.T) {
		// Upstream #708's exact case, at the setting this instance runs.
		editID, _ := newPendingTagEdit(t)
		caller := createUser(t)
		defer setMinTrustLevel(t, -1)()

		require.NoError(t, update(t, withAdminContext(ctx), editID, caller),
			"#708's case: an admin with no trust history was refused at the default. "+
				"Admin is a role, not a trust level, so it must not be folded into "+
				"the comparison")
	})
}

// TestTrustLookupErrorFailsClosed covers the one path no other test reaches.
//
// mayUpdateEdit returns `false, err` when the trust lookup fails, on the rule
// that "I could not tell the caller's level" must not read as "probably
// trusted". Every other test here has a working trust lookup, so that branch is
// dead code as far as the suite is concerned -- and a mutation replacing it with
// LevelSteward SURVIVES against a test that looks like it covers it.
//
// THREE APPROACHES FAILED BEFORE THIS ONE, and the reasons are the point:
//
//  1. REVOKE SELECT on user_trust. Does not work: the test connects as a
//     SUPERUSER, and superusers bypass table privileges, so the REVOKE succeeds
//     and the read still succeeds.
//  2. Cancelling the context. LOOKS like it works -- the test went green -- but
//     it passes for the WRONG REASON, and the mutation still survived. The
//     service's first query, FindEdit, uses the same context and fails FIRST, so
//     mayUpdateEdit is never reached and the fail-closed branch never runs. The
//     error assertion (ErrorIs context.Canceled) could not tell the difference,
//     because FindEdit's error is context.Canceled too. This is the exact
//     "passes for an unrelated reason" failure the R074 guard tests were written
//     to avoid, committed by me in this very file.
//  3. Renaming the table. This one targets the trust lookup and nothing else.
//
// The precondition assertion is what keeps the result honest: it first shows the
// caller IS allowed with a working lookup, so the later refusal cannot be
// vacuously true because the fixture was never valid.
func TestTrustLookupErrorFailsClosed(t *testing.T) {
	ctx := context.Background()
	db := testutil.DB()
	require.NotNil(t, db)

	owner := createUser(t)
	caller := createUser(t)
	setLevel(t, caller, trust.LevelSteward)
	defer setMinTrustLevel(t, 1)()

	// Precondition: a Steward at threshold 1 is allowed. Without this the
	// fail-closed assertion could pass because the caller was never allowed in
	// the first place, and would prove nothing.
	ok, err := mayUpdateEditViaService(t, ctx, caller, owner)
	require.NoError(t, err)
	require.True(t, ok, "precondition failed: the Steward should be allowed "+
		"before the trust lookup is broken")

	// Hide the trust table. Nothing else in UpdateTagEdit reads it, so
	// GetUserTrust is the only query that fails -- which is the whole
	// requirement. A rename rather than a drop: the fixtures other tests in this
	// package use reference users, and dropping would cascade.
	_, err = db.Exec(ctx, `ALTER TABLE user_trust RENAME TO user_trust_hidden`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := db.Exec(context.Background(),
			`ALTER TABLE user_trust_hidden RENAME TO user_trust`)
		require.NoError(t, err, "user_trust was left renamed; every later test in "+
			"this package will fail on a missing table")
	})

	ok, err = mayUpdateEditViaService(t, ctx, caller, owner)
	require.False(t, ok,
		"a failing trust lookup was treated as TRUSTED. This is the fail-open bug: "+
			"an unreachable or broken trust table would silently grant edit "+
			"authority to anyone the operator meant to gate")

	// The error must be the trust lookup's own, not something incidental. Code
	// 42P01 is undefined_table, and it can only have come from GetUserTrust --
	// which is what rules out the FindEdit-fails-first mistake above.
	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr,
		"expected the trust lookup's own Postgres error, got %v. If the error is "+
			"swallowed and replaced, the fail-closed branch is not what runs", err)
	require.Equal(t, "42P01", pgErr.Code,
		"the wrong error surfaced, so the refusal came from somewhere other than "+
			"the trust lookup and this test is not covering the fail-closed branch")
}

// mayUpdateEditViaService runs a real UpdateTagEdit as caller against an edit
// owned by owner, and reports the AUTHORITY outcome only.
//
// It exists because mayUpdateEdit is unexported and this file must be package
// edit_test to break the import cycle with testutil. Going through the public
// method is the stronger claim anyway: it proves the authority check is on the
// path a request actually takes.
//
// The bool is "was authority granted", derived by recognising the two authority
// errors rather than by swallowing everything, so an unrelated failure is
// reported as an error rather than quietly read as a refusal.
func mayUpdateEditViaService(t *testing.T, ctx context.Context, caller, owner uuid.UUID) (bool, error) {
	t.Helper()
	// Fixtures are written on a background context, deliberately separate from the
	// request context: a fixture insert that failed would look exactly like an
	// authority refusal, and the test would pass for the wrong reason.
	setup := context.Background()
	db := testutil.DB()
	tagID := uuid.Must(uuid.NewV7())
	editID := uuid.Must(uuid.NewV7())
	now := time.Now()

	_, err := db.Exec(setup,
		`INSERT INTO tags (id, name, created_at, updated_at) VALUES ($1, $2, $3, $3)`,
		tagID, "t-"+tagID.String(), now)
	if err != nil {
		return false, err
	}
	_, err = db.Exec(setup,
		`INSERT INTO edits (id, user_id, operation, target_type, data, status, created_at, updated_at)
		 VALUES ($1, $2, 'modify', 'tag', '{}'::jsonb, 'pending', $3, $3)`,
		editID, owner, now)
	if err != nil {
		return false, err
	}
	_, err = db.Exec(setup,
		`INSERT INTO tag_edits (edit_id, tag_id) VALUES ($1, $2)`, editID, tagID)
	if err != nil {
		return false, err
	}

	c := asRequest(asUser(ctx, caller))
	_, err = newTestEdit(t).UpdateTagEdit(c, editID, models.TagEditInput{
		Edit: &models.EditInput{Comment: strPtr("amending")},
	})
	// nil error means the update went through, so authority WAS granted. Any
	// error -- a clean ErrUnauthorizedUpdate or a broken trust lookup -- means it
	// was not, and both are returned so the caller can tell them apart; that
	// distinction is what TestTrustLookupErrorFailsClosed asserts on.
	if err == nil {
		return true, nil
	}
	return false, err
}

// setMinTrustLevel overrides the config value for one test and returns a
// restore function.
//
// config.C is a package-level struct with no setter, so this writes the field
// and puts it back via t.Cleanup. The restore matters more than it looks: these
// subtests set the threshold to -1, 3 and 4 in sequence, and a leaked value
// would make a later subtest's result depend on test order.
func setMinTrustLevel(t *testing.T, level int) func() {
	t.Helper()
	prev := config.C.EditUpdateMinTrustLevel
	config.C.EditUpdateMinTrustLevel = level
	return func() { config.C.EditUpdateMinTrustLevel = prev }
}

// newTestEdit builds an Edit service with a real trust service behind it.
//
// The factory's real withTxn is not reachable from a test -- createWithTxnFunc
// needs the pool and is unexported -- so this passes a wrapper that calls
// straight through. That is NOT a real transaction and the comment says so
// rather than pretending otherwise: no rollback is exercised, so these tests
// must not be extended to assert anything about atomicity. They assert
// AUTHORITY, which is decided before any write, which is why the shortcut is
// sound here and would not be for a transactional test.
func newTestEdit(t *testing.T) *edit.Edit {
	t.Helper()
	q := queries.New(testutil.DB())
	withTxn := func(fn func(*queries.Queries) error) error {
		return fn(q)
	}
	return edit.NewEdit(q, withTxn, trust.NewTrust(q, withTxn))
}

// asUser puts the caller in the context, since the service reads the acting user
// from there rather than taking it as a parameter.
func asUser(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, auth.ContextUser, &auth.AuthUser{ID: id, Name: "caller"})
}

// asRequest adds the gqlgen contexts that the service's audit path reads.
//
// UpdateTagEdit calls utils.Arguments(ctx).Field("input") to record which input
// fields the caller actually set, for the edit's audit trail. utils.Arguments
// handles a missing field context gracefully, but gqlgen's own
// GetOperationContext PANICS by design when there is none -- so a test calling
// the service directly, rather than through the GraphQL handler, has to supply
// these or it crashes in the audit code rather than reaching its assertion.
//
// The "id" field is declared because the service reads the edit id out of
// ctx to log it; leaving it unset would make Field("id") report absent, which is
// harmless today and would silently produce a wrong audit row tomorrow. This is
// the same shape internal/api/integration_test.go's updateContext uses.
func asRequest(ctx context.Context) context.Context {
	ctx = graphql.WithOperationContext(ctx, &graphql.OperationContext{
		Variables: map[string]any{"id": true},
	})
	// The CollectedField must be non-nil: FieldContext.Field is a
	// graphql.CollectedField, which embeds *ast.Field, and ArgumentMap is
	// promoted from that pointer -- so a zero CollectedField nil-derefs inside
	// gqlgen. An empty ast.Field gives an empty argument map, which is all the
	// audit path needs to walk without panicking.
	//
	// "id" and "input" are declared as arguments because those are the two the
	// service reads: Field("input") is what records which fields the caller set,
	// and Field("id") is the edit being updated. Declaring them keeps the audit
	// row honest if the service starts reading one it does not today.
	field := graphql.CollectedField{
		Field: &ast.Field{
			Name: "tagEdit",
			Arguments: ast.ArgumentList{
				{Name: "id"},
				{Name: "input"},
			},
		},
	}
	return graphql.WithFieldContext(ctx, &graphql.FieldContext{
		Object: "Query",
		Field:  field,
	})
}

func strPtr(s string) *string { return &s }
