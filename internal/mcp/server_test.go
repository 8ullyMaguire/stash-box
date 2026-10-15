package mcp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/mcp"
)

// THE MCP SERVER, tested without a database and without a network.
//
// The GraphQL side is an httptest stub that RECORDS what was asked, which is what makes the interesting
// assertions possible: not only "did the tool answer" but "did it ask the instance the question the caller
// asked, with the arguments the caller sent, and did it ask at all". Several of these tests assert that
// NOTHING was sent -- an argument this server validates away must never reach the instance, because a
// request that arrives with the wrong arguments answered differently is the failure the validation exists
// to prevent.

type captured struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
	APIKey    string         `json:"-"`
	Path      string         `json:"-"`
}

type stub struct {
	mu       sync.Mutex
	requests []captured
	respond  func(captured) (int, string)
	headers  []string
}

func (s *stub) handler(w http.ResponseWriter, r *http.Request) {
	var req captured
	_ = json.NewDecoder(r.Body).Decode(&req)
	req.APIKey = r.Header.Get("ApiKey")
	req.Path = r.URL.Path

	s.mu.Lock()
	s.requests = append(s.requests, req)
	respond := s.respond
	s.headers = append(s.headers, r.Header.Get("Content-Type"))
	s.mu.Unlock()

	status, body := http.StatusOK, `{"data":{}}`
	if respond != nil {
		status, body = respond(req)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

func (s *stub) seen() []captured {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]captured(nil), s.requests...)
}

func newStub(t *testing.T, respond func(captured) (int, string)) (*stub, *mcp.Client) {
	t.Helper()
	s := &stub{respond: respond}
	server := httptest.NewServer(http.HandlerFunc(s.handler))
	t.Cleanup(server.Close)
	return s, mcp.NewClient(server.URL+"/graphql", "walk-key")
}

// serve runs the transport over one input and decodes every response line.
func serve(t *testing.T, client *mcp.Client, input string) []map[string]any {
	t.Helper()
	var out strings.Builder
	err := mcp.Serve(context.Background(), strings.NewReader(input), &out, client)
	require.NoError(t, err)

	var responses []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var decoded map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &decoded), "every line the server writes must be JSON: %q", line)
		responses = append(responses, decoded)
	}
	return responses
}

func callTool(t *testing.T, client *mcp.Client, name string, args string) map[string]any {
	t.Helper()
	input := `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"` + name + `","arguments":` + args + `}}` + "\n"
	responses := serve(t, client, input)
	require.Len(t, responses, 1, "one request, one response")
	return responses[0]
}

// toolText is the single text block a tool result carries.
func toolText(t *testing.T, response map[string]any) string {
	t.Helper()
	result, ok := response["result"].(map[string]any)
	require.True(t, ok, "the response must carry a result: %v", response)
	content, ok := result["content"].([]any)
	require.True(t, ok, "a tool result carries content: %v", result)
	require.NotEmpty(t, content)
	first := content[0].(map[string]any)
	return first["text"].(string)
}

func isToolError(response map[string]any) bool {
	result, ok := response["result"].(map[string]any)
	if !ok {
		return false
	}
	isError, _ := result["isError"].(bool)
	return isError
}

// ---------------------------------------------------------------- the handshake

func TestTheHandshakeNamesTheRevisionItSpeaks(t *testing.T) {
	_, client := newStub(t, nil)

	// The client asks for a revision this server does not implement. The answer is OURS: the protocol
	// version is a statement about what this code speaks, and echoing the client's would be a claim the
	// implementation does not back.
	responses := serve(t, client, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`+"\n")
	require.Len(t, responses, 1)

	result := responses[0]["result"].(map[string]any)
	assert.Equal(t, mcp.ProtocolVersion, result["protocolVersion"])
	assert.Equal(t, "stash-box", result["serverInfo"].(map[string]any)["name"])
	assert.Contains(t, result["capabilities"], "tools")
}

// A NOTIFICATION IS A MESSAGE WITH NO ID, and answering one puts the client's next read off by a message.
func TestANotificationGetsNoAnswer(t *testing.T) {
	_, client := newStub(t, nil)

	responses := serve(t, client,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n"+
			`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`+"\n")

	require.Len(t, responses, 1, "only the request is answered")
	assert.Equal(t, float64(2), responses[0]["id"], "and the answer belongs to the request, not to the notification")
}

func TestOneBadLineDoesNotEndTheSession(t *testing.T) {
	_, client := newStub(t, nil)

	responses := serve(t, client,
		"this is not JSON\n"+
			`{"jsonrpc":"2.0","id":3,"method":"tools/list"}`+"\n")

	require.Len(t, responses, 2, "the parse error is answered AND the session continues")
	assert.Equal(t, float64(-32700), responses[0]["error"].(map[string]any)["code"])
	assert.NotNil(t, responses[1]["result"], "the next request is still served")
}

func TestAnUnknownMethodIsNamedBack(t *testing.T) {
	_, client := newStub(t, nil)

	responses := serve(t, client, `{"jsonrpc":"2.0","id":4,"method":"resources/list"}`+"\n")
	require.Len(t, responses, 1)

	errObj := responses[0]["error"].(map[string]any)
	assert.Equal(t, float64(-32601), errObj["code"])
	assert.Contains(t, errObj["message"].(string), "resources/list", "the message names what was asked for")
	assert.Contains(t, errObj["message"].(string), "tools/list", "and what is implemented")
}

// ---------------------------------------------------------------- what each tool publishes vs applies

// THE SCHEMA AND THE VALIDATOR ARE ONE LIST, and this test is what keeps them one: every argument a tool
// publishes must be an argument it accepts, and every argument it accepts must be published. A tool that
// advertised something it ignored would answer a different question than its caller asked, silently.
func TestEveryToolPublishesExactlyTheArgumentsItValidates(t *testing.T) {
	// The stub is built here rather than through a helper, because these assertions are about what reached
	// the INSTANCE: a helper that returns an empty list would make "nothing was sent" true for the wrong
	// reason (the first version of this test did exactly that).
	stub, client := newStub(t, func(captured) (int, string) {
		return http.StatusOK, `{"data":{}}`
	})

	responses := serve(t, client, `{"jsonrpc":"2.0","id":5,"method":"tools/list"}`+"\n")
	require.Len(t, responses, 1)

	tools := responses[0]["result"].(map[string]any)["tools"].([]any)
	require.Len(t, tools, 4, "the four tools of slice 13a")

	for _, raw := range tools {
		tool := raw.(map[string]any)
		name := tool["name"].(string)
		schema := tool["inputSchema"].(map[string]any)

		assert.Equal(t, false, schema["additionalProperties"], "%s must refuse arguments it does not know", name)
		properties := schema["properties"].(map[string]any)

		// Every published argument is accepted by the validator: sending each one (with a type its kind
		// allows) must not produce an "unknown argument" refusal.
		for argName := range properties {
			response := callTool(t, client, name, argFor(argName, properties[argName].(map[string]any)))
			assert.NotContains(t, toolText(t, response), "does not take the argument",
				"%s publishes %q, so it must accept it", name, argName)
		}

		// An argument it does NOT publish is refused, and the instance is never asked.
		before := len(stub.seen())
		require.Greater(t, before, 0, "the published-argument calls above must have reached the instance")

		response := callTool(t, client, name, `{"definitely_not_an_argument":"x"}`)
		assert.True(t, isToolError(response), "%s must refuse an unknown argument", name)
		assert.Contains(t, toolText(t, response), "definitely_not_an_argument")
		assert.Equal(t, before, len(stub.seen()),
			"%s must not ask the instance a question it already knows is malformed", name)
	}
}

// argFor builds a valid value for a published argument, from its published type.
func argFor(name string, property map[string]any) string {
	switch property["type"] {
	case "integer":
		return `{"` + name + `":5}`
	case "array":
		return `{"` + name + `":[{"hash":"abc","algorithm":"MD5"}]}`
	default:
		if name == "id" {
			return `{"` + name + `":"11111111-1111-1111-1111-111111111111"}`
		}
		return `{"` + name + `":"something"}`
	}
}

// ---------------------------------------------------------------- the translations

func TestSearchScenesAsksForTheTermAndTheModernDateField(t *testing.T) {
	stub, client := newStub(t, func(captured) (int, string) {
		return http.StatusOK, `{"data":{"searchScenes":{"count":1,"scenes":[{"id":"s1","title":"A"}]}}}`
	})

	response := callTool(t, client, "search_scenes", `{"term":"pizza","per_page":3}`)
	assert.False(t, isToolError(response))
	assert.Contains(t, toolText(t, response), `"title":"A"`)

	requests := stub.seen()
	require.Len(t, requests, 1)
	assert.Contains(t, requests[0].Query, "searchScenes(term: $term")
	assert.Contains(t, requests[0].Query, "release_date",
		"the selection must use the field the instance asks clients to use")
	assert.NotContains(t, requests[0].Query, "\n    date\n",
		"and must not teach a deprecated field name")
	assert.Equal(t, "pizza", requests[0].Variables["term"])
	assert.Equal(t, float64(3), requests[0].Variables["per_page"])
	_, hadPage := requests[0].Variables["page"]
	assert.False(t, hadPage, "an argument the caller did not send must not be invented")
}

func TestTheApiKeyTravelsInTheApiKeyHeader(t *testing.T) {
	stub, client := newStub(t, func(captured) (int, string) {
		return http.StatusOK, `{"data":{"findScene":null}}`
	})

	callTool(t, client, "get_scene", `{"id":"11111111-1111-1111-1111-111111111111"}`)

	requests := stub.seen()
	require.Len(t, requests, 1)
	assert.Equal(t, "walk-key", requests[0].APIKey,
		"a key that never reaches the instance is a server that silently reads nothing")
	assert.Equal(t, "application/json", stub.headers[0])
}

// THE DOUBLE LIST IS THE INSTANCE'S SHAPE: one inner list is one FILE, and the hashes inside it are
// alternatives for that file. A tool that flattened them would ask "which of these files are these" when
// the caller asked "which file is this".
func TestFingerprintsAreSubmittedAsOneFile(t *testing.T) {
	stub, client := newStub(t, func(captured) (int, string) {
		return http.StatusOK, `{"data":{"findScenesBySceneFingerprints":[[null]]}}`
	})

	callTool(t, client, "find_scenes_by_fingerprint",
		`{"fingerprints":[{"hash":"aaa","algorithm":"MD5"},{"hash":"bbb","algorithm":"OSHASH"}]}`)

	requests := stub.seen()
	require.Len(t, requests, 1)
	groups := requests[0].Variables["fingerprints"].([]any)
	require.Len(t, groups, 1, "one file, so one group")
	assert.Len(t, groups[0].([]any), 2, "and both of its hashes inside it")
}

// THE HASH IS HEXADECIMAL, and this is its own test because the failure it prevents is SILENT: the
// instance reads a hash in base 16, so a decimal rendering of the same number asks for a different
// fingerprint and comes back as an empty result -- which reads exactly like "this instance has never seen
// this file". The repo paid an afternoon for that; an MCP client must not be able to pay it again.
func TestAHashMustBeHexadecimal(t *testing.T) {
	stub, client := newStub(t, func(captured) (int, string) {
		return http.StatusOK, `{"data":{"findScenesBySceneFingerprints":[[null]]}}`
	})

	response := callTool(t, client, "find_scenes_by_fingerprint",
		`{"fingerprints":[{"hash":"this is not a hash","algorithm":"MD5"}]}`)
	assert.True(t, isToolError(response))
	assert.Contains(t, toolText(t, response), "base 16", "the refusal explains why the form matters")
	assert.Empty(t, stub.seen(), "nothing was asked of the instance")

	// The control, in the same test: a hexadecimal hash DOES reach the instance, so the refusal above is
	// about the form of the value and not about a tool that refuses everything.
	ok := callTool(t, client, "find_scenes_by_fingerprint",
		`{"fingerprints":[{"hash":"00ff01","algorithm":"MD5"}]}`)
	assert.False(t, isToolError(ok))
	assert.Len(t, stub.seen(), 1, "the hexadecimal call is served")
}

// THE PUBLISHED DESCRIPTION IS PART OF THE CONTRACT for a tool a MODEL calls: the only thing standing
// between a client and the decimal trap is that the description says so, so the description is asserted.
func TestTheFingerprintToolPublishesThatItsHashesAreHexadecimal(t *testing.T) {
	_, client := newStub(t, nil)
	responses := serve(t, client, `{"jsonrpc":"2.0","id":11,"method":"tools/list"}`+"\n")
	tools := responses[0]["result"].(map[string]any)["tools"].([]any)

	var found bool
	for _, raw := range tools {
		tool := raw.(map[string]any)
		if tool["name"] != "find_scenes_by_fingerprint" {
			continue
		}
		found = true
		assert.Contains(t, tool["description"].(string), "HEXADECIMAL")
		property := tool["inputSchema"].(map[string]any)["properties"].(map[string]any)["fingerprints"].(map[string]any)
		items := property["items"].(map[string]any)["properties"].(map[string]any)
		hash := items["hash"].(map[string]any)["description"].(string)
		assert.Contains(t, hash, "base 16", "the argument's own description names why the form matters")
	}
	require.True(t, found, "the tool must be published")
}

func TestAnAlgorithmMustBeSpelledTheInstancesWay(t *testing.T) {
	stub, client := newStub(t, nil)

	response := callTool(t, client, "find_scenes_by_fingerprint", `{"fingerprints":[{"hash":"aaa","algorithm":"md5"}]}`)
	assert.True(t, isToolError(response))
	assert.Contains(t, toolText(t, response), "MD5", "the refusal names the spelling the instance knows")
	assert.Empty(t, stub.seen(), "nothing was asked of the instance")
}

// ---------------------------------------------------------------- failures and absences

// THE INSTANCE'S OWN WORDS, and a TOOL error rather than a protocol error: "not authorized" reported as
// -32602 would tell the client its request was malformed, when in fact a decision was made about it.
func TestAnInstanceRefusalKeepsItsOwnWording(t *testing.T) {
	_, client := newStub(t, func(captured) (int, string) {
		return http.StatusOK, `{"errors":[{"message":"not authorized"}]}`
	})

	response := callTool(t, client, "search_scenes", `{"term":"pizza"}`)
	assert.Nil(t, response["error"], "a refusal is a tool error, not a protocol error")
	assert.True(t, isToolError(response))
	assert.Contains(t, toolText(t, response), "not authorized")
}

// AN EMPTY ANSWER IS NOT A FAILURE. `findScene: null` is the instance saying it holds no such record, and
// a client must be able to tell that from a refusal -- which is exactly what isError distinguishes.
func TestAnEmptyAnswerIsNotAnError(t *testing.T) {
	_, client := newStub(t, func(captured) (int, string) {
		return http.StatusOK, `{"data":{"findScene":null}}`
	})

	response := callTool(t, client, "get_scene", `{"id":"11111111-1111-1111-1111-111111111111"}`)
	assert.False(t, isToolError(response), "nothing was refused and nothing failed")
	assert.Contains(t, toolText(t, response), "null")
}

func TestARefusedKeyIsNamedAsSuch(t *testing.T) {
	_, client := newStub(t, func(captured) (int, string) {
		return http.StatusUnauthorized, "unauthorized"
	})

	response := callTool(t, client, "search_scenes", `{"term":"pizza"}`)
	assert.True(t, isToolError(response))
	assert.Contains(t, toolText(t, response), "401", "the status is the diagnosis")
}

func TestAnUnknownToolIsNamedBack(t *testing.T) {
	_, client := newStub(t, nil)

	response := callTool(t, client, "read_everything", `{}`)
	assert.True(t, isToolError(response))
	assert.Contains(t, toolText(t, response), "read_everything")
}

// AN UNKNOWN ARGUMENT IS REFUSED, AND THE INSTANCE IS NEVER ASKED -- the constraint phase 13's plan states
// for this slice, named as its own test so a mutation to the validation has somewhere specific to fail.
func TestAnUnknownArgumentIsRefusedAndNeverReachesTheInstance(t *testing.T) {
	stub, client := newStub(t, func(captured) (int, string) {
		return http.StatusOK, `{"data":{"searchScenes":{"count":0,"scenes":[]}}}`
	})

	// The typo a client actually makes: the field name snake_cased wrongly.
	response := callTool(t, client, "search_scenes", `{"term":"pizza","perpage":5}`)

	assert.True(t, isToolError(response), "an argument the tool does not take is an error")
	assert.Contains(t, toolText(t, response), "perpage", "and the message names it")
	assert.Empty(t, stub.seen(),
		"the instance must never be asked with a request this server already knows is not what was meant")

	// The control, in the same test: the correctly spelled call DOES reach the instance, so "nothing was
	// sent" above is about the typo and not about a client that cannot call anything at all.
	serve(t, client, `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"search_scenes","arguments":{"term":"pizza","per_page":5}}}`+"\n")
	assert.Len(t, stub.seen(), 1, "the corrected call is served")
}

// A missing required argument is named, and it is a tool error so the model can fix it and try again.
func TestAMissingRequiredArgumentIsNamed(t *testing.T) {
	stub, client := newStub(t, nil)

	response := callTool(t, client, "search_scenes", `{}`)
	assert.True(t, isToolError(response))
	assert.Contains(t, toolText(t, response), "term")
	assert.Empty(t, stub.seen())
}
