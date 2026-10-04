//go:build integration

package api_test

import (
	"fmt"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/database/testutil"
)

// SPEC §7.24.1 -- verified-unknown markers.
//
// The rules are in the SCHEMA, not in a service, and these tests are here to prove the schema
// actually refuses the things the design says it refuses. A service that validates is a
// service a later code path can forget; a constraint is not.
//
// Rule 1: absence means MISSING, so every column is nullable and migrating an existing
//         database invents no assertions.
// Rule 2: reason_code is NOT NULL and FK'd to the registry, because "not publicly knowable"
//         and "exists but nobody has looked" are different facts and a free-text or absent
//         reason collapses them.
// Rule 3: one assertion per (entity, field), so a gap is suppressed ONCE.

func TestFieldVerificationMigrationApplied(t *testing.T) {
	var version int
	var dirty bool
	err := testutil.DB().QueryRow(t.Context(),
		`SELECT version, dirty FROM schema_migrations`).Scan(&version, &dirty)
	require.NoError(t, err)
	assert.False(t, dirty, "the schema must not be left mid-migration")
	assert.GreaterOrEqual(t, version, 96,
		"migration 96 must be applied; a lower version means this test is passing "+
			"against a schema that lacks the tables it goes on to use")
}

func TestTheThreeReasonsExistWithTheirDocumentedBehaviour(t *testing.T) {
	rows, err := testutil.DB().Query(t.Context(),
		`SELECT code, suppresses_gap, allows_self_affirming
		 FROM field_verification_reasons ORDER BY code`)
	require.NoError(t, err)
	defer rows.Close()

	got := map[string][2]bool{}
	for rows.Next() {
		var code string
		var suppresses, selfAffirming bool
		require.NoError(t, rows.Scan(&code, &suppresses, &selfAffirming))
		got[code] = [2]bool{suppresses, selfAffirming}
	}
	require.NoError(t, rows.Err())

	require.Len(t, got, 3, "the registry must hold exactly the three documented reasons, "+
		"and a test that reads its length cannot silently pass on a renamed code")

	assert.Equal(t, [2]bool{true, false}, got["not_publicly_knowable"])
	assert.Equal(t, [2]bool{true, false}, got["declined_by_owner"])
	// The load-bearing one: not_yet_looked does NOT suppress. If it did, an unanswerable
	// question would be closed by assertion, which is the farm §7.24.1 refuses.
	assert.Equal(t, [2]bool{false, false}, got["not_yet_looked"],
		"not_yet_looked must leave the gap in place, or asserting 'nobody looked' clears "+
			"the quest without anyone doing the work")

	for code, flags := range got {
		assert.False(t, flags[1], "reason %q allows a self-affirming assertion; §7.24.1 "+
			"refuses those because verified-unknown would be the cheapest XP farm available",
			code)
	}
}

func TestAnUnknownReasonCodeIsRefusedByTheSchema(t *testing.T) {
	user := createFieldVerificationUser(t)
	_, err := testutil.DB().Exec(t.Context(),
		`INSERT INTO field_verification_states
		   (id, entity_type, entity_id, field, reason_code, asserted_by, asserted_at)
		 VALUES ($1, 'performer', $2, 'height', 'because_i_said_so', $3, now())`,
		uuid.Must(uuid.NewV4()), uuid.Must(uuid.NewV4()), user)
	require.Error(t, err, "a reason code outside the registry was accepted, so the FK is "+
		"missing and an assertion can carry a reason nothing validates")

	// Assert on the CONSTRAINT, not on the prose of the error. pgx surfaces SQLSTATE 23503
	// and the constraint name; the human-readable "is not present in table
	// field_verification_reasons" sentence lives in the server's DETAIL field, which
	// `err.Error()` does NOT include:
	//
	//	Error()       = ERROR: insert or update on table "field_verification_states"
	//	                violates foreign key constraint
	//	                "field_verification_states_reason_code_fkey" (SQLSTATE 23503)
	//	Detail        = Key (reason_code)=(because_i_said_so) is not present in table
	//	                "field_verification_reasons".
	//
	// This assertion used to require the DETAIL sentence inside err.Error(), so it failed
	// while the FK was working perfectly -- a green constraint reported as broken. Naming
	// the constraint is also the stronger claim: it survives a wording change in any
	// PostgreSQL release.
	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr, "expected a PostgreSQL error, got %T", err)
	assert.Equal(t, "23503", pgErr.Code, "foreign_key_violation")
	assert.Equal(t, "field_verification_states_reason_code_fkey", pgErr.ConstraintName,
		"the violated constraint must be the reason_code FK -- a different one would "+
			"mean this insert failed for an unrelated reason and proved nothing about "+
			"the registry")
}

func TestAnAssertionWithNoReasonIsRefused(t *testing.T) {
	// Rule 2. NULL is the interesting case, because the column being nullable is what makes
	// this a constraint rather than a type error -- and a nullable reason_code is precisely
	// the "verified-unknown without a reason is a way to clear a gap" hole.
	user := createFieldVerificationUser(t)
	_, err := testutil.DB().Exec(t.Context(),
		`INSERT INTO field_verification_states
		   (id, entity_type, entity_id, field, reason_code, asserted_by, asserted_at)
		 VALUES ($1, 'performer', $2, 'height', NULL, $3, now())`,
		uuid.Must(uuid.NewV4()), uuid.Must(uuid.NewV4()), user)
	require.Error(t, err, "an assertion with a NULL reason was accepted")
	assert.Contains(t, err.Error(), "reason_code")
}

func TestAnAssertionWithNoAuthorIsRefused(t *testing.T) {
	// asserted_by is NOT NULL for the same reason as reason_code: an assertion nobody made
	// cannot be weighed against trust, and §7.24.1 deliberately routes these through the
	// edit machinery so they earn trust like any other edit.
	_, err := testutil.DB().Exec(t.Context(),
		`INSERT INTO field_verification_states
		   (id, entity_type, entity_id, field, reason_code, asserted_by, asserted_at)
		 VALUES ($1, 'performer', $2, 'height', 'not_publicly_knowable', NULL, now())`,
		uuid.Must(uuid.NewV4()), uuid.Must(uuid.NewV4()))
	require.Error(t, err, "an assertion with no author was accepted")
	assert.Contains(t, err.Error(), "asserted_by")
}

func TestOneAssertionPerEntityAndField(t *testing.T) {
	// Rule 3. Two curators asserting the same unknown must not produce two rows: the gap is
	// suppressed once, by one assertion, and a duplicate would let the suppression count
	// double-count and completion exceed what it should.
	user := createFieldVerificationUser(t)
	entity := uuid.Must(uuid.NewV4())
	id := uuid.Must(uuid.NewV4())

	_, err := testutil.DB().Exec(t.Context(),
		`INSERT INTO field_verification_states
		   (id, entity_type, entity_id, field, reason_code, asserted_by, asserted_at)
		 VALUES ($1, 'performer', $2, 'height', 'not_publicly_knowable', $3, now())`,
		id, entity, user)
	require.NoError(t, err, "the first assertion must be accepted")

	_, err = testutil.DB().Exec(t.Context(),
		`INSERT INTO field_verification_states
		   (id, entity_type, entity_id, field, reason_code, asserted_by, asserted_at)
		 VALUES ($1, 'performer', $2, 'height', 'not_yet_looked', $3, now())`,
		uuid.Must(uuid.NewV4()), entity, user)
	require.Error(t, err, "a second assertion for the same entity and field was accepted, so "+
		"the unique index is missing and one gap can be suppressed twice")
}

func TestTheSameFieldOnDifferentEntitiesIsAllowed(t *testing.T) {
	// The unique index is on (entity_type, entity_id, field) -- not on field alone, and not
	// without entity_type. Without entity_type in the key, asserting a performer is
	// height-unknown would block every other performer, which is the bug an over-broad index
	// causes rather than a missing one.
	user := createFieldVerificationUser(t)
	for range 2 {
		_, err := testutil.DB().Exec(t.Context(),
			`INSERT INTO field_verification_states
			   (id, entity_type, entity_id, field, reason_code, asserted_by, asserted_at)
			 VALUES ($1, 'performer', $2, 'height', 'not_publicly_knowable', $3, now())`,
			uuid.Must(uuid.NewV4()), uuid.Must(uuid.NewV4()), user)
		require.NoError(t, err, "two different entities must be able to assert the same field")
	}
}

func TestAnEntityWithNoAssertionsHasEveryFieldMissing(t *testing.T) {
	// Rule 1, observed rather than asserted: absence is the normal state and means MISSING.
	// If this returned a row for an unasserted entity, every scene in a fresh database would
	// count as fully catalogued.
	var count int
	err := testutil.DB().QueryRow(t.Context(),
		`SELECT COUNT(*) FROM field_verification_states
		 WHERE entity_type = 'performer' AND entity_id = $1`,
		uuid.Must(uuid.NewV4())).Scan(&count)
	require.NoError(t, err)
	assert.Zero(t, count, "a brand-new entity reported assertions; absence must mean missing")
}

func TestAnAssertionSurvivesWithoutACitationAndOneAcceptsIt(t *testing.T) {
	// citation_url is NULLABLE by design: "nobody has published this performer's height" is a
	// normal thing for a curator to record and needs no URL. The counterpart is §7.24.2's
	// expected totals, where an unsourced total silently deflates every completion score --
	// which is why THAT one is NOT NULL and this one is not.
	user := createFieldVerificationUser(t)
	entity := uuid.Must(uuid.NewV4())

	_, err := testutil.DB().Exec(t.Context(),
		`INSERT INTO field_verification_states
		   (id, entity_type, entity_id, field, reason_code, asserted_by, asserted_at)
		 VALUES ($1, 'performer', $2, 'height', 'not_publicly_knowable', $3, now())`,
		uuid.Must(uuid.NewV4()), entity, user)
	require.NoError(t, err, "an uncited assertion was rejected")

	var url *string
	err = testutil.DB().QueryRow(t.Context(),
		`SELECT citation_url FROM field_verification_states
		 WHERE entity_type = 'performer' AND entity_id = $1`, entity).Scan(&url)
	require.NoError(t, err)
	assert.Nil(t, url, "an uncited assertion stored a citation")

	// And a cited one is accepted, so the column is usable rather than decorative.
	entity2 := uuid.Must(uuid.NewV4())
	_, err = testutil.DB().Exec(t.Context(),
		`INSERT INTO field_verification_states
		   (id, entity_type, entity_id, field, reason_code, asserted_by, asserted_at, citation_url)
		 VALUES ($1, 'performer', $2, 'birth_date', 'not_publicly_knowable', $3, now(), $4)`,
		uuid.Must(uuid.NewV4()), entity2, user, "https://example.org/performer")
	require.NoError(t, err, "a cited assertion was rejected")
}

func TestAnAssertionIsRemovedWithItsEntity(t *testing.T) {
	// ON DELETE CASCADE, and the reason is in the migration: an assertion describes a field of
	// an entity that no longer exists, and keeping it would let a re-created entity of the
	// same name inherit a confidence nobody gave it.
	user := createFieldVerificationUser(t)
	performerID, editID := createFieldVerificationPerformer(t)

	_, err := testutil.DB().Exec(t.Context(),
		`INSERT INTO field_verification_states
		   (id, entity_type, entity_id, field, reason_code, asserted_by, asserted_at, edit_id)
		 VALUES ($1, 'performer', $2, 'height', 'not_publicly_knowable', $3, now(), $4)`,
		uuid.Must(uuid.NewV4()), performerID, user, editID)
	require.NoError(t, err)

	_, err = testutil.DB().Exec(t.Context(), `DELETE FROM performers WHERE id = $1`, performerID)
	require.NoError(t, err)

	var count int
	err = testutil.DB().QueryRow(t.Context(),
		`SELECT COUNT(*) FROM field_verification_states
		 WHERE entity_type = 'performer' AND entity_id = $1`, performerID).Scan(&count)
	require.NoError(t, err)
	assert.Zero(t, count, "the assertion outlived the entity it described")
}

// Fixtures insert directly rather than going through the API, because these tests are about
// what the SCHEMA accepts. A helper that created users through the registration path would
// let a change there decide whether these tests can run at all.
func createFieldVerificationUser(t *testing.T) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV4())
	marker := uuid.Must(uuid.NewV4()).String()
	_, err := testutil.DB().Exec(t.Context(),
		`INSERT INTO users (id, name, password_hash, email, api_key, last_api_call,
		                    created_at, updated_at)
		 VALUES ($1, $2, 'not-a-real-hash', $3, $4, NOW(), NOW(), NOW())`,
		id,
		fmt.Sprintf("fv_user_%s", marker),
		fmt.Sprintf("fv_%s@example.invalid", marker),
		fmt.Sprintf("fv_key_%s", marker))
	require.NoError(t, err)
	return id
}

func createFieldVerificationPerformer(t *testing.T) (performerID, editID uuid.UUID) {
	t.Helper()
	err := testutil.DB().QueryRow(t.Context(),
		`INSERT INTO performers (id, name, created_at, updated_at)
		 VALUES (gen_random_uuid(), $1, NOW(), NOW()) RETURNING id`,
		fmt.Sprintf("fv_fixture_%s", uuid.Must(uuid.NewV7()).String())).Scan(&performerID)
	require.NoError(t, err)

	err = testutil.DB().QueryRow(t.Context(),
		`INSERT INTO edits (id, operation, target_type, status, created_at, updated_at)
		 VALUES (gen_random_uuid(), 'MODIFY', 'PERFORMER', 'PENDING', NOW(), NOW())
		 RETURNING id`).Scan(&editID)
	require.NoError(t, err)
	return performerID, editID
}
