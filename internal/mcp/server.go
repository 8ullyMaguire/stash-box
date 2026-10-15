package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
)

// Version is this server's own version, set at build time the way the instance sets its own (see the
// Makefile). It rides in `serverInfo`, so an operator can tell which build a client is talking to.
var Version = "dev"

// maxMessage bounds one line of input. A tool result can be large (a page of scenes with their addresses),
// and a client that streams a huge line should be cut off with an error rather than allowed to exhaust the
// process's memory.
const maxMessage = 8 << 20

// Serve runs the stdio transport until the input ends.
//
// Three behaviours are load-bearing rather than incidental:
//
//   - A NOTIFICATION GETS NO RESPONSE. `notifications/initialized` is a message with no id, and a client
//     that receives an answer to it is a client whose next read is off by one message.
//   - ONE BAD LINE DOES NOT END THE SESSION. A parse error is answered with -32700 and the loop continues:
//     the transport is a stream and a single malformed line is not a reason to drop everything after it.
//     (A read error IS the end, and it is returned.)
//   - ERRORS ARE ANSWERS. Every failure this server can produce is a JSON-RPC error object with the
//     request's own id, so a client always gets exactly one response per request.
func Serve(ctx context.Context, in io.Reader, out io.Writer, client *Client) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), maxMessage)

	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			// A blank line is not a message. Answering it would put a response on the wire that belongs to
			// no request.
			continue
		}

		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			if err := writeMessage(out, Response{
				JSONRPC: "2.0",
				Error:   &RPCError{Code: codeParseError, Message: "this line is not a JSON-RPC message: " + err.Error()},
			}); err != nil {
				return err
			}
			continue
		}

		if req.IsNotification() {
			// Deliberately not answered. Nothing here needs the notification's content: no state is
			// established by `initialized`, and pretending otherwise would be inventing a handshake this
			// server does not require.
			continue
		}

		if err := writeMessage(out, handle(ctx, client, req)); err != nil {
			return err
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("reading the transport: %w", err)
	}
	return nil
}

func handle(ctx context.Context, client *Client, req Request) Response {
	respond := func(result any) Response {
		return Response{JSONRPC: "2.0", ID: req.ID, Result: result}
	}
	fail := func(code int, format string, args ...any) Response {
		return Response{JSONRPC: "2.0", ID: req.ID, Error: &RPCError{Code: code, Message: fmt.Sprintf(format, args...)}}
	}

	switch req.Method {
	case "initialize":
		// The client's requested version is read and NOT echoed: this server answers with the revision it
		// implements, and a client that cannot speak it says so.
		return respond(map[string]any{
			"protocolVersion": ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "stash-box", "version": Version},
		})

	case "ping":
		return respond(map[string]any{})

	case "tools/list":
		return respond(map[string]any{"tools": toolList()})

	case "tools/call":
		var params struct {
			Name      string                     `json:"name"`
			Arguments map[string]json.RawMessage `json:"arguments"`
		}
		if len(req.Params) > 0 {
			if err := json.Unmarshal(req.Params, &params); err != nil {
				return fail(codeInvalidParams, "tools/call needs {name, arguments}: %v", err)
			}
		}
		if params.Name == "" {
			return fail(codeInvalidParams, "tools/call needs the name of a tool")
		}
		// An absent `arguments` is an empty set rather than an error: a tool with no required arguments
		// may legitimately be called without any, and the validation below says which ones it needs.
		if params.Arguments == nil {
			params.Arguments = map[string]json.RawMessage{}
		}
		return respond(call(ctx, client, params.Name, params.Arguments))

	default:
		// Named, so a client can tell a typo from an unimplemented capability.
		return fail(codeMethodNotFound, "this server does not implement %q; it implements initialize, ping, tools/list and tools/call", req.Method)
	}
}
