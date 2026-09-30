package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// federationSchema reads the schema file the check is about.
//
// The file is read from disk rather than taken from a parsed in-memory schema on
// purpose: the claims being made here are claims about what a future edit would
// have to break, and an edit to the .graphql is the thing most likely to make
// them false. Reading the source is what makes the test a tripwire on the file a
// contributor actually edits.
func federationSchema() (string, error) {
	path := filepath.Join("..", "..", "graphql", "schema", "types", "federation.graphql")
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// typeBlock returns the body of one GraphQL type, or "" if it is absent.
//
// It strips COMMENTS before matching, which is the part that matters. Several of
// the forbidden names appear in this repo's own prose -- the schema documents that
// ForeignCandidate deliberately has no local id, and a naive substring scan reads
// that documentation as a violation and fails a correct schema. A test that
// cannot distinguish "the field is declared" from "the field is mentioned" is a
// test that gets deleted the first time somebody improves a comment.
//
// It also stops at the closing brace at nesting level zero, so a field of a
// different type that happens to share the name is not mistaken for this one.
func typeBlock(schema, name string) string {
	marker := "type " + name
	i := strings.Index(schema, marker)
	if i < 0 {
		return ""
	}
	// Skip a trailing block description, then take from the opening brace.
	start := strings.Index(schema[i:], "{")
	if start < 0 {
		return ""
	}
	start += i

	depth := 0
	for j := start; j < len(schema); j++ {
		switch schema[j] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return stripComments(schema[start : j+1])
			}
		}
	}
	return ""
}

// declaredFields returns the field names of a type body, snake_cased as the schema
// spells them.
//
// It only accepts "name: Type" lines, so a description line or a stray fragment
// cannot register as a field. Returns nil rather than empty when nothing parses,
// which is what lets the caller tell "no forbidden fields" from "did not read".
func declaredFields(block string) []string {
	var out []string
	for _, line := range strings.Split(block, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "\"\"\"") {
			continue
		}
		idx := strings.Index(trimmed, ":")
		if idx <= 0 {
			continue
		}
		name := strings.TrimSpace(trimmed[:idx])
		// A field name is a bare identifier; anything else is prose.
		for _, r := range name {
			if !(r == '_' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
				name = ""
				break
			}
		}
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}

// stripComments removes # line comments and """ block descriptions.
func stripComments(s string) string {
	var b strings.Builder
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if i > 0 {
			b.WriteString("\n")
		}
		if t := strings.TrimSpace(line); t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		b.WriteString(line)
	}
	// Block descriptions are indented multi-line strings; drop any line that is
	// inside one by tracking the triple quote.
	out := []string{}
	inBlock := false
	for _, line := range strings.Split(b.String(), "\n") {
		if strings.Count(line, `"""`)%2 == 1 {
			inBlock = !inBlock
			continue
		}
		if inBlock {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// FederationForeignCandidates is the read half of D2 step 6, and the whole of it
// exists because an operator needs to see WHY a federation run came back the way
// it did.
//
// The security property is what makes it safe, and it is a property of the SHAPE
// rather than of a code path: the type it returns has no field that could serve
// as a local identity. So the test below is mostly about the schema, because the
// schema is where that property has to hold for it to hold at all. A future
// field that a mutation could act on would be a first step toward resolving a
// remote string into a local performer, which is exactly the edge F2 exists to
// cut.

// TestForeignCandidateTypeCarriesNoLocalIdentity is the boundary test.
//
// This is the assertion worth having, and it is a schema assertion on purpose. A
// test that checked the resolver's output would pass just as happily if someone
// later added an `id` field to the type, because an unused field changes no
// output. The type is the thing that must not drift.
func TestForeignCandidateTypeCarriesNoLocalIdentity(t *testing.T) {
	schema, err := federationSchema()
	if err != nil {
		t.Fatalf("reading the federation schema: %v", err)
	}

	block := typeBlock(schema, "ForeignCandidate")
	if block == "" {
		t.Fatal("ForeignCandidate is not in the schema at all")
	}

	// Every one of these would be a way to address a local entity from a remote
	// string. F2 says the edge does not exist, and "we did not write the field"
	// is not the same claim as "the field is not there".
	// Matched as WHOLE FIELD NAMES, not as substrings. An earlier version of this
	// test listed "id:" and failed on `remote_id:`, which is a legitimate remote
	// identifier and the opposite of a local one -- a test that rejects a correct
	// schema gets deleted rather than fixed, and then the real check is gone.
	fields := declaredFields(block)
	forbidden := []struct {
		field string
		why   string
	}{
		{"id", "a bare id is a local identity by another name"},
		{"performer", "naming a local performer"},
		{"performer_id", "naming a local performer"},
		{"entity_id", "the vote edge F2 cuts"},
		{"local_id", "a local anything"},
		{"local_performer", "a local anything"},
		{"vote", "a vote here is exactly what foreign evidence may not become"},
		{"weight", "local weight would be remote weight summed in"},
		{"accept", "accepting would turn evidence into an entity"},
		{"create", "creating is the other half of accepting"},
		{"approve", "approval is acceptance with extra steps"},
		{"confidence", "a local score the remote value would be trusted into"},
	}

	for _, f := range forbidden {
		for _, name := range fields {
			if name == f.field {
				t.Errorf("ForeignCandidate exposes field %q (%s).\n"+
					"F2 makes foreign evidence evidence and never a local vote. A "+
					"field that can address a local entity is that edge, whether or "+
					"not any resolver currently uses it.", name, f.why)
			}
		}
	}

	// And the positive direction, so the check cannot pass by reading nothing.
	// A test that only asserts absence passes just as happily against a type that
	// was never parsed, which is exactly what happened the first time this ran.
	for _, want := range []string{"remote_id", "remote_name", "instance_id", "suggester_count", "fetched_at"} {
		found := false
		for _, name := range fields {
			if name == want {
				found = true
			}
		}
		if !found {
			t.Errorf("ForeignCandidate is missing expected field %q. The type was "+
				"renamed or the parser stopped seeing it, and the absence checks "+
				"above would have passed regardless.", want)
		}
	}
}

// TestForeignCandidatesQueryIsReadOnlyAndAdminOnly pins the two properties that
// make the surface acceptable: it reads, and it is not public.
func TestForeignCandidatesQueryIsReadOnlyAndAdminOnly(t *testing.T) {
	schema, err := federationSchema()
	if err != nil {
		t.Fatalf("reading the federation schema: %v", err)
	}

	if !strings.Contains(schema, "federation_foreign_candidates") {
		t.Error("the query is missing from the schema")
	}
	if !strings.Contains(schema, "extend type Query") ||
		!strings.Contains(schema, "federation_foreign_candidates(query_id: ID!): [ForeignCandidate!]! @hasRole(role: ADMIN)") {
		t.Error("federation_foreign_candidates is not declared as an ADMIN query under Query.\n" +
			"It lists what peers told this instance, which is instance-internal state, " +
			"and the peer ids and remote strings in it are not user data to be public.")
	}

	// The read-only half is the point. A candidate mutation anywhere in the
	// federation schema would be the same hole with a smaller door.
	for _, m := range []string{
		"federation_candidate_accept",
		"federation_candidate_create",
		"federation_candidate_vote",
		"federation_foreign_candidate_update",
		"federation_foreign_candidate_delete",
	} {
		if strings.Contains(schema, m) {
			t.Errorf("%s exists. F2 says a peer's answer cannot become a local\n"+
				"entity here, and an operator override is that same edge with a\n"+
				"smaller door. Evidence can be read, discarded, and nothing else.", m)
		}
	}
}
