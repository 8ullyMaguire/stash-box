package federation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The exchange (phase 12, slice 12f). The client's own behaviour is covered in client_test.go; what is
// under test here is the ORCHESTRATION -- which peers get asked, what a row says when one does not
// answer, and whether the two sides of the negotiation are actually compared.

// answeringPeer is a peer that returns one ontology answer. It echoes the instance id it was built with,
// because Broadcast treats a mismatched echo as an impostor (and it is right to).
func answeringPeer(t *testing.T, instanceID string, crossing []OntologyFieldCrossing, versions []int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Answer{
			PeerInstanceID: instanceID,
			Ontology:       OntologyClaim{CoreVersions: versions, LocalVersion: 1, Digest: string(make([]byte, 0))},
			Crossing:       crossing,
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// ontologyQuestion is the shape the exchange actually sends: the question IS the declaration, so it has
// to carry one. Worth stating because a question without a claim is refused by Broadcast ("a question
// needs a target type"), and the first version of this test built exactly that -- the F1 rule catching a
// test written by someone who had just read the rule.
// fieldNamed finds a decision BY NAME. The field order belongs to the ontology document, and an
// index-based assertion would pass or fail on a decision nobody made (an earlier version of these tests
// asserted `OurDecision[1]` and was reading `implies`).
func fieldNamed(t *testing.T, fields []OntologyFieldCrossing, name string) OntologyFieldCrossing {
	t.Helper()
	for _, c := range fields {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("%q is not among the %d decisions: %+v", name, len(fields), fields)
	return OntologyFieldCrossing{}
}

func ontologyQuestion() Question {
	return Question{
		QueryID: uuid.Must(uuid.NewV7()),
		Ontology: OntologyClaim{
			CoreVersions: []int{CoreOntologyV1, CoreOntologyV2},
			LocalVersion: 1,
			Digest:       strings.Repeat("ab", 32),
			NamedTags:    3,
			Implications: 2,
		},
	}
}

func TestExchangeRefusesToAskAQuestionThatDeclaresNothing(t *testing.T) {
	// The guard, and its control: the peer would answer, so the only thing stopping the question is the
	// guard. Without counting the peer's requests, "no rows were built" would also pass if the client
	// simply failed to connect.
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode(Answer{PeerInstanceID: "peer-1"})
	}))
	t.Cleanup(srv.Close)

	rows, err := ExchangeOntologies(context.Background(), testClient(t, srv.Client()),
		[]Peer{freshPeer("peer-1", "reachable", srv.URL)},
		Question{QueryID: uuid.Must(uuid.NewV7())}, []int{CoreOntologyV1}, time.Now())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "own claim")
	assert.Nil(t, rows)
	assert.Zero(t, calls, "a question that declares nothing must not reach the network")
}

func TestExchangeAsksEveryAskablePeerAndKeepsARowForTheRest(t *testing.T) {
	// The peer that answers speaks core 1 only, and we speak both -- so the crossing it sends is the one
	// the RULE produces for that pair: the v2-only field may travel from it to us because WE implement
	// core 2, which is the receiving side the rule is about.
	//
	// The fixture is built from the rule rather than hand-written, and that is the honest way to do it
	// here: a hand-written "correct answer" would have been my belief about the rule, and the first
	// version of this test asserted exactly the opposite (the v1-only refusal the ANSWERING half produces
	// when the ASKER is the v1 side). Two directions, two different questions.
	theirs := crossingsFrom(Compatibility([]int{CoreOntologyV1}, []int{CoreOntologyV1, CoreOntologyV2}))
	require.Equal(t, 3, len(theirs), "the whole document is decided, field by field")
	srv := answeringPeer(t, "peer-1", theirs, []int{CoreOntologyV1})

	// Enabled is true on the first three: `freshPeer` leaves it false, and a peer the operator has not
	// switched on must not be contacted at all -- which is the fourth case below.
	answered := freshPeer("peer-1", "always-up", srv.URL)
	answered.Enabled = true
	stale := freshPeer("peer-2", "gone-quiet", "https://peer-2.example")
	stale.Enabled = true
	longAgo := time.Now().Add(-365 * 24 * time.Hour)
	stale.LastSeenAt = &longAgo
	disabled := freshPeer("peer-3", "switched-off", "https://peer-3.example")
	disabled.Enabled = false
	anonymous := freshPeer("", "no-id", "https://peer-4.example")
	anonymous.Enabled = true

	rows, err := ExchangeOntologies(context.Background(), testClient(t, srv.Client()),
		[]Peer{answered, stale, disabled, anonymous}, ontologyQuestion(),
		[]int{CoreOntologyV1, CoreOntologyV2}, time.Now())
	require.NoError(t, err)

	// One row per peer, IN THE ORDER GIVEN: a report that reordered itself would make two runs of the
	// same exchange look different for no reason.
	require.Len(t, rows, 4)
	assert.Equal(t, "always-up", rows[0].Peer.Name)
	assert.Equal(t, "gone-quiet", rows[1].Peer.Name)

	// The one that answered.
	assert.True(t, rows[0].Asked)
	assert.True(t, rows[0].Answered, "reason: %s", rows[0].Reason)
	assert.Empty(t, rows[0].Reason, "an answer needs no excuse")
	assert.Equal(t, theirs, rows[0].Theirs())

	// Our side of the negotiation, computed HERE from our versions and its claim -- the same rule the
	// peer ran on itself. It agrees, which is the control for the Disagrees case below.
	require.Len(t, rows[0].OurDecision, len(ontologyFields))
	category := rows[0].OurDecision[len(rows[0].OurDecision)-1]
	assert.Equal(t, "category", category.Name)
	assert.False(t, category.Allowed, "we implement core 2, the peer does not, and BOTH are required")
	assert.Contains(t, category.Reason, "core 2", "the reason names the version the field belongs to")
	// The DECISION fields, not the sentences: both sides word the reason from their own end, so comparing
	// the prose is how a correct peer gets reported as disagreeing (which is what this assertion did
	// before it was narrowed).
	assert.True(t, sameCrossing(theirs, rows[0].OurDecision), "the same two claims, and the same decision")
	assert.False(t, rows[0].Disagrees, "both sides ran the same rule over the same two claims")

	// The three that were not asked, each with its own reason, and NONE of them silently absent: "no
	// peers" and "every peer is unreachable" must not be the same list.
	for i, want := range []string{"gone-quiet", "switched-off", "no-id"} {
		row := rows[i+1]
		assert.False(t, row.Asked, want)
		assert.False(t, row.Answered, want)
		assert.NotEmpty(t, row.Reason, "%s: a peer that was not asked still needs a reason", want)
	}
	assert.Contains(t, rows[1].Reason, "last seen", "a stale peer's reason carries WHEN it was last seen")
	assert.Contains(t, rows[2].Reason, "disabled in the registry",
		"a switched-off peer is not contacted, and the reason says the operator's switch did it")
	assert.Contains(t, rows[3].Reason, "no instance id")

	// The summary names the silent ones, so an operator reading one line learns which peer is missing
	// rather than only how many.
	line := Summary(rows)
	assert.Contains(t, line, "asked 1 peer(s), 1 answered")
	assert.Contains(t, line, "gone-quiet")
	assert.Equal(t, 1, AnsweredCount(rows))

	// The empty registry is not an error and says so in words.
	assert.Equal(t, "no peers are registered, so nothing was asked", Summary(nil))
}

func TestExchangeNoticesWhenAPeerComputesTheNegotiationDifferently(t *testing.T) {
	// A peer that REFUSES a field its own answer says it may send. The difference is the finding -- the
	// whole point of computing our side rather than trusting its answer, and of having this case at all:
	// without it, Disagrees could be constant false and nothing would say so.
	//
	// The field to flip is a CORE 1 one, because that is a field this pair may legitimately exchange:
	// flipping `category` produced no disagreement at all, since both sides refuse it for a version-skewed
	// pair -- which is how the first version of this test came to assert a `Disagrees` that was false for
	// a reason it had not understood.
	lying := crossingsFrom(Compatibility([]int{CoreOntologyV1, CoreOntologyV2}, []int{CoreOntologyV1}))
	require.True(t, fieldNamed(t, lying, "tag").Allowed, "the core-1 field is exchangeable between these two")
	for i := range lying {
		if lying[i].Name == "tag" {
			lying[i].Allowed = false
			lying[i].Reason = "not sending that"
		}
	}
	srv := answeringPeer(t, "peer-1", lying, []int{CoreOntologyV1})

	agrees := freshPeer("peer-1", "agrees-with-itself", srv.URL)
	agrees.Enabled = true
	rows, err := ExchangeOntologies(context.Background(), testClient(t, srv.Client()),
		[]Peer{agrees}, ontologyQuestion(),
		[]int{CoreOntologyV1, CoreOntologyV2}, time.Now())
	require.NoError(t, err)

	require.Len(t, rows, 1)
	assert.True(t, rows[0].Answered)
	assert.True(t, rows[0].Disagrees, "it refused a core-1 field our own computation of the same pair allows")

	// Both halves of the row, by name: our computation allows the field, the peer refused it, and the row
	// keeps the peer's own words -- because "which side said what" is the whole content of a disagreement.
	tag := fieldNamed(t, rows[0].OurDecision, "tag")
	assert.True(t, tag.Allowed, "our own computation says the core-1 field may be exchanged")
	assert.Equal(t, "not sending that", fieldNamed(t, rows[0].Theirs(), "tag").Reason)

	// The control: category is the field BOTH sides refuse for a version-skewed pair, and a comparison
	// that flagged every field would have called it a disagreement too.
	category := fieldNamed(t, rows[0].OurDecision, "category")
	assert.False(t, category.Allowed)
	assert.Equal(t, category, fieldNamed(t, rows[0].Theirs(), "category"))
}

func TestExchangeSurvivesAPeerThatDoesNotAnswer(t *testing.T) {
	// A peer that accepts the connection and hangs. It must come back as a row with a reason, not as a
	// missing row and not as a hung call: an exchange that blocks on one silent peer is an exchange an
	// operator will disable.
	silent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(3 * time.Second)
	}))
	t.Cleanup(silent.Close)

	client := testClient(t, silent.Client())
	client.perPeerTimeout = 250 * time.Millisecond

	start := time.Now()
	hangs := freshPeer("peer-1", "hangs", silent.URL)
	hangs.Enabled = true
	rows, err := ExchangeOntologies(context.Background(), client,
		[]Peer{hangs}, ontologyQuestion(),
		[]int{CoreOntologyV1}, time.Now())
	require.NoError(t, err, "one peer failing is not the exchange failing")
	assert.Less(t, time.Since(start), 2*time.Second, "the per-peer deadline must bound the whole exchange")

	require.Len(t, rows, 1)
	assert.True(t, rows[0].Asked)
	assert.False(t, rows[0].Answered)
	assert.NotEmpty(t, rows[0].Reason)
	assert.Contains(t, Summary(rows), "hangs")
}
