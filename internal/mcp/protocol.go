// Package mcp is a READ-ONLY MCP server over this instance's own GraphQL API (plan phase 13 slice 13a).
//
// WHAT IT IS, in one sentence: a translation layer. Every tool maps to a query that already exists and
// already carries `@hasRole(role: READ)`, so this package adds no read path and no authority -- an API key
// that cannot read a scene through GraphQL cannot read it through a tool either, and there is no second
// authorization implementation to drift.
//
// WHY A SEPARATE BINARY ON STDIO. An endpoint inside the instance would be a second serving surface on a
// server whose posture is one public GraphQL endpoint plus the SPA: it would need routing, its own auth
// story, and it would meet the content-plane scanner from the wrong side. A stdio server is launched by the
// CLIENT, talks to the instance over HTTP with the key the operator already has, and leaves the instance
// with exactly one thing listening.
//
// WHAT AN MCP CLIENT SEES: `initialize`, `tools/list`, `tools/call` (and `ping`, and the two notifications
// it must ignore without answering). A notification -- a message with no id -- gets NO response, which is
// what the framing requires and what a client waits on.
package mcp

import (
	"encoding/json"
	"io"
)

// ProtocolVersion is the revision this server speaks. It is a constant rather than a flag because a
// client sends its own version and this server answers with the one it implements; a version string that
// could be configured would be a claim about a protocol this code does not speak.
const ProtocolVersion = "2024-11-05"

// JSON-RPC 2.0 error codes. Only the ones this server can produce.
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

// Request is one JSON-RPC message. `ID` is a RawMessage because the protocol allows a number OR a string,
// and re-marshalling it as a string would answer a question the client did not ask -- a client that sent
// `1` and gets `"1"` back cannot match the response to its request.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// IsNotification reports whether this message is a notification: no `id` field at all. An id that is
// present but null is also a notification per JSON-RPC, and both are handled here.
func (r Request) IsNotification() bool {
	return len(r.ID) == 0 || string(r.ID) == "null"
}

type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// CallToolResult is what `tools/call` returns: a list of content blocks.
//
// `IsError` rather than a JSON-RPC error, and the distinction is the whole reason this type exists: a tool
// that failed AT ITS JOB (no such scene, not authorized, a bad argument) is a RESULT the model can read and
// correct, while a protocol error is a failure of the transport. Reporting "you are not authorized" as
// -32602 tells the client the request was malformed; reporting it as `isError` tells the model what
// happened, which is the only one of the two it can act on.
type CallToolResult struct {
	Content []Content `json:"content"`
	IsError bool      `json:"isError,omitempty"`
}

type Content struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// textResult renders a tool's payload as the single text block MCP expects.
func textResult(payload []byte) CallToolResult {
	return CallToolResult{Content: []Content{{Type: "text", Text: string(payload)}}}
}

// errorResult is a tool failure the model can read. The message is carried through verbatim when it came
// from the instance -- a rewritten message would be this server's opinion about an authorization decision
// it did not make.
func errorResult(message string) CallToolResult {
	return CallToolResult{
		Content: []Content{{Type: "text", Text: message}},
		IsError: true,
	}
}

// writeMessage emits one line of JSON. The stdio transport is newline-delimited, so a message that is not
// followed by a newline leaves the client waiting for the rest of it.
func writeMessage(w io.Writer, msg any) error {
	encoded, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if _, err := w.Write(append(encoded, '\n')); err != nil {
		return err
	}
	return nil
}
