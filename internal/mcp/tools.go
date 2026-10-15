package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// THE FOUR TOOLS, and the rule that shapes every line of this file: the schema a tool PUBLISHES and the
// validation it APPLIES are the same list of arguments. A hand-written JSON schema next to a hand-written
// validator is two descriptions of one rule, and the failure mode is a tool that advertises an argument it
// ignores -- which answers a different question than the caller asked, silently.
//
// Each tool is a TRANSLATION: arguments in, one GraphQL query and its variables out, the instance's answer
// back as text. Nothing here decides who may read what -- the query carries `@hasRole(role: READ)` and the
// instance enforces it with the key it was given.

// sceneFields is the selection used everywhere a Scene is returned, kept in one place so `get_scene` and
// `search_scenes` cannot come back with different shapes for the same record (a model that learned the
// fields from one tool would break on the other).
//
// `date` is deliberately NOT requested: the instance deprecates it in favour of `release_date`, and asking
// for a deprecated field would teach every client the old name.
const sceneFields = `
    id
    title
    details
    release_date
    production_date
    code
    director
    duration
    urls { url site { id name } }
    studio { id name }
    performers { as performer { id name } }
    fingerprints { hash algorithm duration }
    deleted
    created
    updated`

const performerFields = `
    id
    name
    disambiguation
    aliases
    gender
    country
    birth_date
    death_date
    urls { url site { id name } }`

// argKind is a small closed set of argument types: the tools' arguments are strings, integers and one
// list-of-objects shape, and a wider type system would be a validator to maintain for no reader's benefit.
type argKind string

const (
	kindString argKind = "string"
	kindInt    argKind = "integer"
	kindHashes argKind = "hashes"
)

type argSpec struct {
	name     string
	kind     argKind
	desc     string
	required bool
}

type tool struct {
	name        string
	description string
	args        []argSpec
	build       func(args map[string]json.RawMessage) (query string, variables map[string]any, err error)
}

// inputSchema is the published schema, DERIVED from the same argSpec list the validator walks.
//
// `additionalProperties: false` is not decoration: without it a client that sends `perpage` instead of
// `per_page` gets an answer to a question it did not ask, and nothing anywhere says so.
func (t tool) inputSchema() map[string]any {
	properties := map[string]any{}
	required := []string{}
	for _, a := range t.args {
		property := map[string]any{"description": a.desc}
		switch a.kind {
		case kindInt:
			property["type"] = "integer"
		case kindHashes:
			// The shape the instance's FingerprintQueryInput takes, spelled out so a client can build it
			// without reading this repo.
			property["type"] = "array"
			property["items"] = map[string]any{
				"type": "object",
				"properties": map[string]any{
					"hash": map[string]any{"type": "string", "description": "the hash, HEXADECIMAL and not " +
						"decimal: the instance reads it in base 16, so a decimal rendering names a different " +
						"fingerprint and comes back empty"},
					"algorithm": map[string]any{"type": "string", "enum": []string{"MD5", "OSHASH", "PHASH"}},
				},
				"required":             []string{"hash", "algorithm"},
				"additionalProperties": false,
			}
		default:
			property["type"] = "string"
		}
		properties[a.name] = property
		if a.required {
			required = append(required, a.name)
		}
	}

	schema := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// validate applies exactly what inputSchema advertises: known names, required present, the declared type.
//
// An UNKNOWN argument is an error rather than something to ignore -- see inputSchema.
func (t tool) validate(args map[string]json.RawMessage) error {
	known := make([]string, 0, len(t.args))
	for _, a := range t.args {
		known = append(known, a.name)
	}

	// Iterate in sorted order so a client sending two bad arguments is told about the same one every time:
	// a message that varies between runs is a message nobody can test.
	sent := make([]string, 0, len(args))
	for name := range args {
		sent = append(sent, name)
	}
	sort.Strings(sent)

	for _, name := range sent {
		if !t.has(name) {
			return fmt.Errorf("%s does not take the argument %q; it takes: %s",
				t.name, name, strings.Join(known, ", "))
		}
	}

	for _, a := range t.args {
		raw, present := args[a.name]
		if !present {
			if a.required {
				return fmt.Errorf("%s requires %q", t.name, a.name)
			}
			continue
		}
		if err := a.check(raw); err != nil {
			return fmt.Errorf("%s: %w", t.name, err)
		}
	}
	return nil
}

func (t tool) has(name string) bool {
	for _, a := range t.args {
		if a.name == name {
			return true
		}
	}
	return false
}

func (a argSpec) check(raw json.RawMessage) error {
	switch a.kind {
	case kindString:
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return fmt.Errorf("%q must be a string", a.name)
		}
		if strings.TrimSpace(s) == "" {
			return fmt.Errorf("%q must not be empty", a.name)
		}
	case kindInt:
		var n int
		if err := json.Unmarshal(raw, &n); err != nil {
			return fmt.Errorf("%q must be an integer", a.name)
		}
	case kindHashes:
		var entries []struct {
			Hash      string `json:"hash"`
			Algorithm string `json:"algorithm"`
		}
		if err := json.Unmarshal(raw, &entries); err != nil {
			return fmt.Errorf("%q must be a list of {hash, algorithm} objects", a.name)
		}
		if len(entries) == 0 {
			return fmt.Errorf("%q must name at least one hash", a.name)
		}
		for i, e := range entries {
			if strings.TrimSpace(e.Hash) == "" {
				return fmt.Errorf("%q entry %d has no hash", a.name, i)
			}
			// HEXADECIMAL, and this is not a formality: the instance reads the hash in base 16, so a
			// decimal rendering of the same number names a DIFFERENT fingerprint and comes back empty --
			// which reads exactly like "this instance has never seen this file". The repo paid an
			// afternoon for that (see the comment in internal/api/content_tier_integration_test.go), and
			// this refusal is what stops an MCP client paying it again.
			if !isHex(e.Hash) {
				return fmt.Errorf("%q entry %d (%q) is not hexadecimal: the instance reads a hash in base 16, "+
					"so a decimal rendering asks for a different fingerprint", a.name, i, e.Hash)
			}
			if !knownAlgorithm(e.Algorithm) {
				// Named here rather than left to the instance: the algorithm set is this API's own
				// vocabulary, and a client that sent "md5" gets told the spelling instead of a refusal
				// from three layers down.
				return fmt.Errorf("%q entry %d has algorithm %q; the instance knows MD5, OSHASH and PHASH",
					a.name, i, e.Algorithm)
			}
		}
	}
	return nil
}

// isHex reports whether a hash is something the instance's scalar can read: bare hexadecimal, which is
// what it both accepts and emits (its own output is `%016x`).
func isHex(s string) bool {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X")
	if s == "" {
		return false
	}
	_, err := strconv.ParseUint(s, 16, 64)
	return err == nil
}

func knownAlgorithm(name string) bool {
	switch name {
	case "MD5", "OSHASH", "PHASH":
		return true
	}
	return false
}

// stringArg reads a validated string argument.
func stringArg(args map[string]json.RawMessage, name string) string {
	var s string
	if raw, ok := args[name]; ok {
		_ = json.Unmarshal(raw, &s)
	}
	return s
}

// optionalIntArg reads an integer argument that may be absent. The second return distinguishes "absent"
// from "zero", because a client that sent `per_page: 0` asked for nothing rather than for the default.
func optionalIntArg(args map[string]json.RawMessage, name string) (int, bool) {
	raw, ok := args[name]
	if !ok {
		return 0, false
	}
	var n int
	_ = json.Unmarshal(raw, &n)
	return n, true
}

func Tools() []tool {
	return []tool{
		{
			name: "search_scenes",
			description: "Search this instance's scenes by text. Returns the matching records with their " +
				"studio, performers, addresses and fingerprints -- everything this instance holds about them.",
			args: []argSpec{
				{name: "term", kind: kindString, desc: "what to search for", required: true},
				{name: "page", kind: kindInt, desc: "1-based page of results (default 1)"},
				{name: "per_page", kind: kindInt, desc: "results per page (default 25, the instance caps it)"},
			},
			build: func(args map[string]json.RawMessage) (string, map[string]any, error) {
				vars := map[string]any{"term": stringArg(args, "term")}
				if n, ok := optionalIntArg(args, "page"); ok {
					vars["page"] = n
				}
				if n, ok := optionalIntArg(args, "per_page"); ok {
					vars["per_page"] = n
				}
				query := "query($term: String!, $page: Int, $per_page: Int) {" +
					"\n  searchScenes(term: $term, page: $page, per_page: $per_page) {" +
					"\n    count" +
					"\n    scenes {" + sceneFields + "\n    }" +
					"\n  }" +
					"\n}"
				return query, vars, nil
			},
		},
		{
			name: "search_performers",
			description: "Search this instance's performers by name or alias, returning the records with " +
				"their disambiguation, dates, country and addresses.",
			args: []argSpec{
				{name: "term", kind: kindString, desc: "what to search for", required: true},
				{name: "page", kind: kindInt, desc: "1-based page of results (default 1)"},
				{name: "per_page", kind: kindInt, desc: "results per page (default 25, the instance caps it)"},
			},
			build: func(args map[string]json.RawMessage) (string, map[string]any, error) {
				vars := map[string]any{"term": stringArg(args, "term")}
				if n, ok := optionalIntArg(args, "page"); ok {
					vars["page"] = n
				}
				if n, ok := optionalIntArg(args, "per_page"); ok {
					vars["per_page"] = n
				}
				query := "query($term: String!, $page: Int, $per_page: Int) {" +
					"\n  searchPerformers(term: $term, page: $page, per_page: $per_page) {" +
					"\n    count" +
					"\n    performers {" + performerFields + "\n    }" +
					"\n  }" +
					"\n}"
				return query, vars, nil
			},
		},
		{
			name: "get_scene",
			description: "Read one scene by its id: the record this instance holds, with its addresses, " +
				"studio, performers and fingerprints. Answers null when the id names nothing.",
			args: []argSpec{
				{name: "id", kind: kindString, desc: "the scene's id (a UUID)", required: true},
			},
			build: func(args map[string]json.RawMessage) (string, map[string]any, error) {
				vars := map[string]any{"id": stringArg(args, "id")}
				query := "query($id: ID!) {" +
					"\n  findScene(id: $id) {" + sceneFields + "\n  }" +
					"\n}"
				return query, vars, nil
			},
		},
		{
			name: "find_scenes_by_fingerprint",
			description: "Identify ONE file from the hashes it was fingerprinted with, by asking which " +
				"scene this instance already holds those hashes for. Returns one entry per submitted hash " +
				"group: the scene it matches, or null when this instance has never seen it. Hashes are " +
				"HEXADECIMAL (MD5, OSHASH and PHASH as this instance stores them); a decimal rendering of " +
				"the same number asks for a different fingerprint and comes back empty.",
			args: []argSpec{
				{name: "fingerprints", kind: kindHashes, required: true,
					desc: "the hashes of ONE file, hexadecimal, any of them matching is enough"},
			},
			build: func(args map[string]json.RawMessage) (string, map[string]any, error) {
				var entries []map[string]string
				if err := json.Unmarshal(args["fingerprints"], &entries); err != nil {
					return "", nil, err
				}
				// The API takes a LIST OF LISTS: one inner list is one file, and the hashes inside it are
				// alternatives for that file. This tool identifies one file per call, so the inner list is
				// the whole argument -- spelled out here because a client that sent its hashes as several
				// files would get "which of these is the file" answered as "which files are these".
				vars := map[string]any{"fingerprints": [][]map[string]string{entries}}
				query := "query($fingerprints: [[FingerprintQueryInput!]!]!) {" +
					"\n  findScenesBySceneFingerprints(fingerprints: $fingerprints) {" +
					"\n    id" +
					"\n    title" +
					"\n    urls { url site { id name } }" +
					"\n    fingerprints { hash algorithm }" +
					"\n  }" +
					"\n}"
				return query, vars, nil
			},
		},
	}
}

// call runs one tool: validate, translate, ask the instance, hand the answer back as text.
func call(ctx context.Context, client *Client, name string, args map[string]json.RawMessage) CallToolResult {
	for _, t := range Tools() {
		if t.name != name {
			continue
		}

		if err := t.validate(args); err != nil {
			// A tool error, not a protocol error: the model asked for something this tool cannot do and
			// the message names the argument, so the next attempt can be right.
			return errorResult(err.Error())
		}

		query, variables, err := t.build(args)
		if err != nil {
			return errorResult(fmt.Sprintf("%s could not build its query: %v", name, err))
		}

		var data map[string]json.RawMessage
		if err := client.Do(ctx, query, variables, &data); err != nil {
			// The instance's own wording when the instance refused: "not authorized" from here would be
			// this server guessing at a decision it did not make.
			return errorResult(err.Error())
		}

		// The answer is re-marshalled rather than passed through, so what a client sees is exactly the
		// requested selection and not whatever else the transport happened to carry.
		pretty, err := json.Marshal(data)
		if err != nil {
			return errorResult(fmt.Sprintf("%s: the instance's answer could not be re-encoded: %v", name, err))
		}
		return textResult(pretty)
	}

	return errorResult(fmt.Sprintf("unknown tool %q", name))
}

// toolList is the `tools/list` payload.
func toolList() []map[string]any {
	out := make([]map[string]any, 0, len(Tools()))
	for _, t := range Tools() {
		out = append(out, map[string]any{
			"name":        t.name,
			"description": t.description,
			"inputSchema": t.inputSchema(),
		})
	}
	return out
}
