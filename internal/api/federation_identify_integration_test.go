//go:build integration

package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	// google/uuid, because that is the type federation.Question carries on the wire -- the same split
	// the repo has everywhere it puts an id into a payload a different implementation reads.
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/api"
	"github.com/stashapp/stash-box/internal/config"
	dbtest "github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/service/federation"
)

// The answering half over HTTP (phase 12, slice 12b), through the same handler the route registers.
//
// The route itself is registered in server.go and is exercised live by scripts/walk-12b-live.py; what
// THIS covers is the handler's decisions, which are the part with rules in them: the protocol version
// header, the F1 check on the way in, the refusal for an identification question, the validation of a
// peer's own claim, and the answer carrying the document's digest.

const wireInstance = "wire-under-test"

// postQuestion sends one question to a server wrapping the real handler and returns the status and the
// decoded body.
func postQuestion(t *testing.T, srv *httptest.Server, version string, body any) (int, map[string]any) {
	t.Helper()

	payload, err := json.Marshal(body)
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodPost, srv.URL, bytes.NewReader(payload))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if version != "" {
		req.Header.Set("X-Stash-Federation-Version", version)
	}

	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	var decoded map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&decoded))
	return resp.StatusCode, decoded
}

// ontologyOnlyQuestion is the shape this slice exists for: a peer that names no entity and only says
// which ontology it speaks. The claim has to be WELL-FORMED (it is validated on the way in), and it has
// to be non-empty -- an empty claim means "this peer has not built the half", which Validate reads as a
// question with nothing in it.
func ontologyOnlyQuestion(t *testing.T) federation.Question {
	t.Helper()
	return federation.Question{
		QueryID: uuid.Must(uuid.NewRandom()),
		Ontology: federation.OntologyClaim{
			CoreVersions: []int{federation.CoreOntologyV1},
			LocalVersion: 1,
			Digest:       strings.Repeat("ab", 32),
			NamedTags:    1,
			Implications: 1,
		},
	}
}

func TestFederationIdentifyIntegration(t *testing.T) {
	factory := dbtest.Factory()
	srv := httptest.NewServer(api.FederationIdentify(*factory))
	defer srv.Close()

	// The instance's name comes from config, and it is restored afterwards: a test that leaves a global
	// set makes the NEXT test's 503 arm unreachable, which is the kind of order dependence this suite
	// has paid for before.
	previous := config.C.InstanceID
	config.C.InstanceID = wireInstance
	t.Cleanup(func() { config.C.InstanceID = previous })

	version := strconv.Itoa(federation.ProtocolVersion)

	t.Run("an ontology-only question is answered with the claim", func(t *testing.T) {
		status, body := postQuestion(t, srv, version, ontologyOnlyQuestion(t))
		require.Equal(t, http.StatusOK, status, "body: %v", body)

		assert.Equal(t, wireInstance, body["PeerInstanceID"])

		claim, ok := body["Ontology"].(map[string]any)
		require.True(t, ok, "the answer must carry the ontology claim: %v", body)

		// THE EXCHANGE, ASSERTED AGAINST AN INDEPENDENT READING: the digest on the wire must be the
		// digest of the document this instance holds, computed here from the service rather than read
		// back out of the answer. A test that compared the answer with itself would pass on any number.
		local, err := federation.LocalDeclaration(context.Background(), factory.TagOntology())
		require.NoError(t, err)
		assert.Equal(t, local.Digest, claim["Digest"])
		assert.Equal(t, float64(local.Implications), claim["Implications"])
		assert.Equal(t, float64(local.NamedTags), claim["NamedTags"])
		assert.Equal(t, float64(local.LocalVersion), claim["LocalVersion"])

		versions, ok := claim["CoreVersions"].([]any)
		require.True(t, ok)
		assert.Equal(t, []any{float64(federation.CoreOntologyV1), float64(federation.CoreOntologyV2)}, versions)

		// F2 on this path: this instance invents no evidence.
		assert.Nil(t, body["Candidates"])
	})

	t.Run("the protocol version header is required and named", func(t *testing.T) {
		status, body := postQuestion(t, srv, "", ontologyOnlyQuestion(t))
		assert.Equal(t, http.StatusBadRequest, status)
		assert.Contains(t, body["error"], "none sent")

		status, body = postQuestion(t, srv, "99", ontologyOnlyQuestion(t))
		assert.Equal(t, http.StatusBadRequest, status)
		assert.Contains(t, body["error"], "99",
			"the refusal names the version the peer sent, so the operator can see how far behind it is")
	})

	t.Run("an identification question is refused by name", func(t *testing.T) {
		q := ontologyOnlyQuestion(t)
		q.TargetType = "performer"
		q.Description = "who is this"

		status, body := postQuestion(t, srv, version, q)
		assert.Equal(t, http.StatusUnprocessableEntity, status)
		assert.Contains(t, body["error"], "identification",
			"an empty candidate list would read as 'this peer has nothing', which is worse than a refusal")
	})

	t.Run("a peer claim that is declared but malformed is refused", func(t *testing.T) {
		q := ontologyOnlyQuestion(t)
		q.Ontology = federation.OntologyClaim{CoreVersions: []int{1}, Digest: strings.Repeat("zz", 32)}

		status, body := postQuestion(t, srv, version, q)
		assert.Equal(t, http.StatusUnprocessableEntity, status)
		assert.Contains(t, body["error"], "claim")
	})

	t.Run("an F1 violation is refused on the way in", func(t *testing.T) {
		q := ontologyOnlyQuestion(t)
		q.TargetType = "performer"
		q.Description = "the file at /etc/passwd"

		status, body := postQuestion(t, srv, version, q)
		assert.Equal(t, http.StatusUnprocessableEntity, status)
		assert.Contains(t, body["error"], "node boundary")
	})

	t.Run("without an instance_id the endpoint refuses, naming the key", func(t *testing.T) {
		config.C.InstanceID = ""
		defer func() { config.C.InstanceID = wireInstance }()

		status, body := postQuestion(t, srv, version, ontologyOnlyQuestion(t))
		assert.Equal(t, http.StatusServiceUnavailable, status,
			"503 and not 422: nothing about the peer's request is wrong")
		assert.Contains(t, body["error"], "instance_id")
	})
}
