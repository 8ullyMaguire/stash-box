//go:build integration

package federation_test

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/federation"
)

// F2: a foreign candidate is evidence and NEVER a vote.
//
// D2's definition of done, verbatim: "A foreign candidate provably cannot reach
// the local vote path, proven by a test that ATTEMPTS IT AND EXPECTS A
// REJECTION."
//
// This file is that test, and it exists because the definition of done was not
// met without it. store.go -- the F2 boundary, committed as d3934900 -- arrived
// with no test at all, and the two things that DO exist both fall short of
// "attempts it and expects a rejection":
//
//   - TestRegistryCannotWriteForeignEvidence asserts the registry exposes no
//     Suggest/Vote/Resolve method. That is a claim about NAMES, and a rename
//     would defeat it while the hole stayed open.
//   - The migration's own comments argue the case. A comment in a migration is a
//     claim about the schema, not the schema -- a rule this project has believed
//     five times already.
//
// So the crossing is attempted here, three ways, against a real database.

// TestMain brings up the test database. Nil populater: every fixture is inline,
// because identification_queries and federation_peers are each one INSERT and a
// shared populater for one package would be a second place to keep in sync.
func TestMain(m *testing.M) {
	testutil.TestWithDatabase(m, nil)
}

// TestAForeignCandidateCannotBecomeALocalVote is the F2 boundary, attempted.
//
// The strongest form of this test is a genuine ATTEMPT: build a remote id that
// exactly matches a real local performer, hand it to the store as a peer's
// answer, and show that nothing local was created or attached. A peer's id is
// opaque text, so this is not a contrived input -- a peer naming one of its own
// entities with a string that happens to collide with a local uuid is precisely
// the confusion F2 exists to prevent.
func TestAForeignCandidateCannotBecomeALocalVote(t *testing.T) {
	ctx := context.Background()
	db := testutil.DB()
	require.NotNil(t, db, "testutil.DB() is nil; TestWithDatabase did not run")

	queryID, peerID, localPerformerID := seedF2Fixtures(t)

	// The colliding id: a peer's idea of a performer, spelled exactly as this
	// instance spells its own.
	colliding := localPerformerID.String()

	// Attempt: record it as a peer's answer.
	stored, err := federation.NewStore(queries.New(db)).Record(ctx, queryID, peerID, "performer",
		[]federation.RemoteCandidate{{
			Name:           "Someone Else's Performer",
			PeerID:         colliding,
			SuggesterCount: 500, // a big number, to show it buys nothing
		}})
	require.NoError(t, err, "recording foreign evidence should succeed; it is evidence")
	require.Equal(t, 1, stored)

	// REJECTION 1: the evidence exists, as evidence.
	list, err := federation.NewStore(queries.New(db)).List(ctx, queryID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, colliding, list[0].RemoteEntityID,
		"the remote id is stored verbatim as the PEER's id")

	// REJECTION 2: it attached to nothing. No local performer was created, and
	// the local one is untouched.
	var performersNamed int
	require.NoError(t, db.QueryRow(ctx,
		`SELECT count(*) FROM performers WHERE name = $1`,
		"Someone Else's Performer").Scan(&performersNamed))
	assert.Zero(t, performersNamed,
		"a foreign candidate became a local performer. F2 says a peer's answer "+
			"is evidence; this is a vote.")

	// REJECTION 3: the evidence has no column pointing at a local entity, so
	// there is nowhere for it to attach even in principle. Checked against the
	// real table rather than asserted in a comment.
	var hasEntityID bool
	require.NoError(t, db.QueryRow(ctx,
		`SELECT EXISTS (
		     SELECT 1 FROM information_schema.columns
		     WHERE table_name = 'identification_foreign_candidates'
		       AND column_name IN ('entity_id', 'performer_id', 'studio_id', 'scene_id')
		 )`).Scan(&hasEntityID))
	assert.False(t, hasEntityID,
		"identification_foreign_candidates grew a column that can point at a local "+
			"entity. That column is the F2 hole, whatever the code does today.")

	// And the peer's 500 suggestions bought nothing locally: the row is the
	// peer's number about the peer's users, and it is not summed anywhere.
	assert.Equal(t, 500, list[0].RemoteVoteCount)
	var localVoteColumns int
	require.NoError(t, db.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.columns
		 WHERE table_name = 'identification_foreign_candidates'
		   AND column_name LIKE '%weight%'`).Scan(&localVoteColumns))
	assert.Zero(t, localVoteColumns,
		"foreign evidence grew a weight column, which is how it would start "+
			"counting toward a local score")
}

// TestNoLocalEntityIdOnEvidenceTypes is the belt to that braces.
//
// The rule is NOT "these types hold no uuid". StoredCandidate.PeerID is a
// uuid.UUID and is CORRECT: it is our own row id for the peer that answered, so
// an operator can see who claimed what and disable that peer. Attribution to a
// peer is required by F2.
//
// The rule is narrower and it is about the CANDIDATE. Nothing that identifies a
// local entity -- a performer, a scene, a studio -- may appear on a type that
// carries foreign evidence, because that is the edge along which an answer would
// become a vote. So the assertion is on field NAMES, which is the only way to
// express "this is about a peer, not about an entity" without also forbidding the
// peer attribution that F2 requires.
//
// A name check is weaker than a type check, and deliberately so: the previous
// draft asserted "no uuid.UUID anywhere" and correctly failed on PeerID, which
// proved the assertion was wrong rather than the code. Naming the rule is what
// makes it checkable.
func TestNoLocalEntityIdOnEvidenceTypes(t *testing.T) {
	// A field is a LOCAL entity reference when its name names an entity kind
	// WITHOUT the "remote" marker. RemoteEntityID is the PEER's id for an entity
	// and is exactly what F2 wants; LocalPerformerID would be the hole.
	//
	// Two earlier drafts of this assertion were wrong about the code rather than
	// finding anything: one banned every uuid.UUID (and correctly failed on
	// PeerID, which F2 requires), the next stripped "remote" and then banned the
	// residue, which is self-contradictory. The check has to be "entity kind,
	// unmarked" -- so the marker is stripped and the name is then required NOT to
	// have contained it in the first place.
	entityMarkers := []string{
		"entityid", "entity", "performer", "scene", "studio", "siteid", "tagid",
		"candidateid", "local",
	}

	for _, typ := range []reflect.Type{
		reflect.TypeFor[federation.StoredCandidate](),
		reflect.TypeFor[federation.RemoteCandidate](),
	} {
		t.Run(typ.Name(), func(t *testing.T) {
			for i := 0; i < typ.NumField(); i++ {
				name := strings.ToLower(typ.Field(i).Name)
				for _, marker := range entityMarkers {
					// "remote" in the name is the marker that makes an entity id
					// safe, so its presence exempts the field.
					if strings.Contains(name, "remote") {
						continue
					}
					assert.NotContains(t, name, marker,
						"%s.%s names a local entity. These types carry the PEER's "+
							"identity and the QUERY's id; a local entity id here is "+
							"the edge along which an answer becomes a vote.",
						typ.Name(), typ.Field(i).Name)
				}
			}
		})
	}
}

// TestRecordRefusesUnattributableEvidence covers the store's own guards, which
// are what stop a peer addressing a local query it never answered.
func TestRecordRefusesUnattributableEvidence(t *testing.T) {
	ctx := context.Background()
	db := testutil.DB()
	store := federation.NewStore(queries.New(db))

	queryID, peerID, _ := seedF2Fixtures(t)

	// THE ASSERTION IS ON THE ERROR TEXT, NOT MERELY ON error != nil, and that
	// distinction is the whole test. An earlier draft used assert.Error and two
	// mutations survived it: with the guard removed, the INSERT still fails -- on
	// the foreign key, because uuid.Nil is not a row in either table -- so the
	// error was real and the test was meaningless. Asserting that the error is
	// the GUARD's own message is what distinguishes "refused deliberately" from
	// "refused by the database by accident".
	cases := []struct {
		name    string
		queryID uuid.UUID
		peerID  uuid.UUID
		wantMsg string
		why     string
	}{
		{
			name: "no local query id", queryID: uuid.Nil, peerID: peerID,
			wantMsg: "foreign evidence needs a local query id",
			why: "a remote query id is not a key in this database. Accepting one " +
				"would let a peer address any local query, including a private or " +
				"abandoned one, by guessing a uuid",
		},
		{
			name: "no local peer id", queryID: queryID, peerID: uuid.Nil,
			wantMsg: "foreign evidence needs a local peer id",
			why: "the peer is identified by OUR row id, never by a value the peer " +
				"supplied. Mapping its self-declaration to a row here would mean " +
				"trusting a remote string to select which local row to write",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := store.Record(ctx, c.queryID, c.peerID, "performer",
				[]federation.RemoteCandidate{{Name: "x", PeerID: "y"}})
			require.Error(t, err, c.why)
			assert.Contains(t, err.Error(), c.wantMsg,
				"refused, but not by the guard. The error was %q, which means the "+
					"refusal came from the database rather than from the check "+
					"under test -- so the guard is not what is protecting this", err)
		})
	}

	t.Run("an answer with no remote id is skipped, not stored", func(t *testing.T) {
		// An empty remote id would make the UNIQUE constraint collide across
		// peers for one query and silently collapse distinct answers into a
		// single row. A peer that answers with no id has told us nothing.
		before, err := store.List(ctx, queryID)
		require.NoError(t, err)

		stored, err := store.Record(ctx, queryID, peerID, "performer",
			[]federation.RemoteCandidate{{Name: "nameless", PeerID: ""}})
		require.NoError(t, err)
		assert.Zero(t, stored)

		after, err := store.List(ctx, queryID)
		require.NoError(t, err)
		assert.Len(t, after, len(before), "an answer with no id was stored anyway")
	})
}

// seedF2Fixtures creates a local query, a peer, and a local performer, returning
// their ids.
//
// The local performer is the point: without a real local row there is nothing for
// a peer's id to collide with, and the test would pass vacuously -- the same trap
// the stash exporter fell into with its own positive control.
func seedF2Fixtures(t *testing.T) (queryID, peerID, performerID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	db := testutil.DB()

	queryID = uuid.Must(uuid.NewV7())
	peerID = uuid.Must(uuid.NewV7())
	performerID = uuid.Must(uuid.NewV7())
	now := time.Now()

	// status is CHECKed to open|solved|abandoned -- 'pending' is not a legal
	// value, so the obvious one is wrong. description is NOT NULL with no
	// default, so it has to be supplied even though this test never reads it.
	_, err := db.Exec(ctx,
		`INSERT INTO identification_queries (id, target_type, target_id, description, status, created_at)
		 VALUES ($1, 'performer', $2, $3, 'open', $4)`,
		queryID, performerID, "f2 fixture question", now)
	require.NoError(t, err)

	_, err = db.Exec(ctx,
		`INSERT INTO federation_peers (id, name, base_url, instance_id, trust_weight, enabled, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, 0.5, TRUE, $5, $5)`,
		peerID, "f2-peer", "http://93.184.216.34:9999/graphql", "f2-instance-"+peerID.String(), now)
	require.NoError(t, err)

	_, err = db.Exec(ctx,
		`INSERT INTO performers (id, name, created_at, updated_at) VALUES ($1, $2, $3, $3)`,
		performerID, "local-performer-"+performerID.String(), now)
	require.NoError(t, err)

	return queryID, peerID, performerID
}
