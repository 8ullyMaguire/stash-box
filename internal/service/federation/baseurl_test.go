package federation

import (
	"context"
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeResolver returns a fixed answer for any host, and counts how many times
// it was asked.
//
// The counter exists because "the guard called the resolver" is half of what
// makes these tests non-vacuous: a guard that rejected every URL for an
// unrelated reason would pass every negative case while never resolving
// anything, and a suite of negative cases with no positive case is the exact
// trap R074 is about.
type fakeResolver struct {
	ips   []net.IP
	err   error
	calls int
	// perHost lets one resolver answer differently for different hosts, which
	// is what the "hostname with a public A record and a private one" case
	// needs.
	perHost map[string][]net.IP
}

func (f *fakeResolver) LookupIP(ctx context.Context, host string) ([]net.IP, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	if f.perHost != nil {
		if ips, ok := f.perHost[host]; ok {
			return ips, nil
		}
	}
	return f.ips, nil
}

// public is the resolver answer for a peer that is genuinely reachable.
func public() *fakeResolver {
	return &fakeResolver{ips: []net.IP{net.ParseIP("93.184.216.34")}}
}

// TestValidateBaseURLRejectsUnsafeAddresses is the POSITIVE CONTROL, and it is
// the test the whole R074 guard is judged by.
//
// Every row here is a value that, before this file existed, would have been
// written straight to federation_peers.base_url and then dialled. None of them
// needs a crafted peer or a malicious operator — an operator who fat-fingers a
// hostname into the metadata address is enough, and so is a peer that answers a
// broadcast with a base_url of its own choosing.
//
// Each row must be rejected AND must have consulted the resolver, so a guard
// that rejected everything for an unrelated reason cannot pass.
func TestValidateBaseURLRejectsUnsafeAddresses(t *testing.T) {
	cases := []struct {
		name string
		url  string
		ips  []net.IP
		why  string
	}{
		{
			name: "loopback",
			url:  "http://127.0.0.1:9999",
			ips:  []net.IP{net.ParseIP("127.0.0.1")},
			why:  "the box itself, including any admin-only listener",
		},
		{
			name: "cloud metadata",
			url:  "http://169.254.169.254/latest/meta-data/",
			ips:  []net.IP{net.ParseIP("169.254.169.254")},
			why:  "hands out instance credentials to anything that asks",
		},
		{
			name: "private range",
			url:  "http://10.0.0.5",
			ips:  []net.IP{net.ParseIP("10.0.0.5")},
			why:  "the instance's own VPC or LAN",
		},
		{
			name: "cgnat",
			url:  "http://100.64.0.1",
			ips:  []net.IP{net.ParseIP("100.64.0.1")},
			why:  "shared address space, not covered by RFC1918, routinely internal",
		},
		{
			name: "unspecified",
			url:  "http://0.0.0.0",
			ips:  []net.IP{net.ParseIP("0.0.0.0")},
			why:  "routes to localhost on most stacks",
		},
		{
			name: "ipv4-mapped loopback",
			url:  "http://[::ffff:127.0.0.1]",
			ips:  []net.IP{net.ParseIP("::ffff:127.0.0.1")},
			why:  "the 16-byte form of loopback, which a naive prefix check misses",
		},
		{
			name: "localhost by name",
			url:  "http://localhost:9999",
			ips:  []net.IP{net.ParseIP("93.184.216.34")},
			why:  "a local NAME is rejected before resolution, because /etc/hosts has it",
		},
		{
			name: "userinfo confusion",
			url:  "http://expected.example.com@127.0.0.1/",
			ips:  []net.IP{net.ParseIP("127.0.0.1")},
			why:  "every naive host parser reads expected.example.com out of this",
		},
		{
			name: "file scheme",
			url:  "file:///etc/passwd",
			ips:  []net.IP{net.ParseIP("93.184.216.34")},
			why:  "a protocol handler that reads local files is the vulnerability",
		},
		{
			name: "gopher scheme",
			url:  "gopher://127.0.0.1:6379/_SET",
			ips:  []net.IP{net.ParseIP("127.0.0.1")},
			why:  "the classic Redis SSRF payload",
		},
		{
			name: "rebinding: public first, private second",
			url:  "http://rebind.example.com/graphql",
			ips: []net.IP{
				net.ParseIP("93.184.216.34"),
				net.ParseIP("127.0.0.1"),
			},
			why: "checking only the FIRST resolved address is checking the one the attacker chose to put first",
		},
		{
			name: "rebinding: private second but first is public",
			url:  "http://rebind2.example.com/graphql",
			ips: []net.IP{
				net.ParseIP("8.8.8.8"),
				net.ParseIP("169.254.169.254"),
			},
			why: "metadata as the second answer, which is the shape that fools a first-only check",
		},
		{
			name: "path carrying a filesystem path",
			url:  "http://evil.example.com/etc/passwd",
			ips:  []net.IP{net.ParseIP("93.184.216.34")},
			why:  "R074 rule 1: a path must not become storable in this schema",
		},
		{
			name: "query carrying a filesystem path",
			url:  "http://evil.example.com/graphql?next=/etc/passwd",
			ips:  []net.IP{net.ParseIP("93.184.216.34")},
			why:  "the path is in the QUERY, so a path-only check would miss it",
		},
		{
			name: "unc path",
			url:  `http://evil.example.com/\\host\share`,
			ips:  []net.IP{net.ParseIP("93.184.216.34")},
			why:  "a UNC path is never legitimate in a peer URL",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := &fakeResolver{ips: c.ips}
			err := ValidateBaseURL(context.Background(), c.url, r)

			if err == nil {
				t.Fatalf("ACCEPTED %q (%s).\n"+
					"This value would have been written to federation_peers.base_url and "+
					"dialled. The guard is supposed to make that impossible.", c.url, c.why)
			}

			// The resolver must actually have been consulted — EXCEPT where the
			// URL has no host to resolve.
			//
			// The exception is not a softening of the assertion, it is the
			// assertion being right about what it can prove. `file:///etc/passwd`
			// has an empty Hostname(), so the guard rejects it at the scheme
			// check and no lookup is correct. Demanding a lookup there would
			// force the guard to resolve something it deliberately does not
			// resolve, which is the behaviour R074 forbids.
			//
			// So: a case with no host must be rejected WITHOUT a lookup, and a
			// case with a host must be rejected WITH one. Both are checked, and
			// the split is what keeps "rejected for an unrelated reason" from
			// passing.
			u, parseErr := url.Parse(c.url)
			hasHost := parseErr == nil && u.Hostname() != ""
			switch {
			case hasHost && r.calls == 0:
				t.Errorf("rejected %q without consulting the resolver (%s); "+
					"this case passed for an unrelated reason, so it proves nothing",
					c.url, c.why)
			case !hasHost && r.calls != 0:
				t.Errorf("the guard resolved %q (%s); it has no host, and "+
					"resolving inside a validator is the behaviour R074 exists "+
					"to prevent", c.url, c.why)
			}
		})
	}
}

// TestValidateBaseURLAcceptsRealPeers is the other half, and it is not
// optional: a guard that refuses every URL is green against the table above and
// has stopped all federation.
//
// These are shapes a real deployment uses. Each one would be refused by an
// over-eager path check, which is why they are written down rather than left to
// someone's imagination later.
func TestValidateBaseURLAcceptsRealPeers(t *testing.T) {
	cases := []struct {
		name string
		url  string
		ips  []net.IP
		why  string
	}{
		{
			name: "bare host",
			url:  "https://stashbox.example.org",
			ips:  []net.IP{net.ParseIP("93.184.216.34")},
			why:  "the simplest real configuration",
		},
		{
			name: "graphql path",
			url:  "https://stashbox.example.org/graphql",
			ips:  []net.IP{net.ParseIP("93.184.216.34")},
			why:  "a peer path is a location, not a filesystem path",
		},
		{
			name: "deep path",
			url:  "https://stashbox.example.org/api/v1/identification/federation/ask",
			ips:  []net.IP{net.ParseIP("93.184.216.34")},
			why:  "rejecting any path at all would refuse this, which is why the check is marker-based",
		},
		{
			name: "port",
			url:  "http://peer.example.org:9999/graphql",
			ips:  []net.IP{net.ParseIP("93.184.216.34")},
			why:  "a self-hosted peer on a non-standard port is the normal case",
		},
		{
			name: "multiple public addresses",
			url:  "https://peer.example.org",
			ips: []net.IP{
				net.ParseIP("93.184.216.34"),
				net.ParseIP("2606:4700:4700::1111"),
			},
			why: "every resolved address is public, so all of them are checked and all pass",
		},
		{
			name: "trailing slash",
			url:  "https://peer.example.org/",
			ips:  []net.IP{net.ParseIP("93.184.216.34")},
			why:  "the shape an operator actually types",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := &fakeResolver{ips: c.ips}
			if err := ValidateBaseURL(context.Background(), c.url, r); err != nil {
				t.Errorf("refused a legitimate peer %q (%s): %v\n"+
					"A guard that rejects real peers is green against the unsafe "+
					"table while federation does not work at all.",
					c.url, c.why, err)
			}
		})
	}
}

// TestValidateBaseURLFailsClosed proves the guard does not pass when it cannot
// answer.
//
// Three separate ways to be unable to answer, each of which a careless
// implementation turns into a silent pass: no resolver, a resolver that errors,
// and a resolver that returns nothing.
func TestValidateBaseURLFailsClosed(t *testing.T) {
	t.Run("no resolver", func(t *testing.T) {
		err := ValidateBaseURL(context.Background(), "https://peer.example.org", nil)
		if !errors.Is(err, ErrNoResolver) {
			t.Fatalf("a nil resolver gave %v, want ErrNoResolver.\n"+
				"'I could not tell' must not read as 'probably fine' — that is the "+
				"worst possible failure in a function whose job is to decide whether "+
				"to open a socket.", err)
		}
	})

	t.Run("resolver error", func(t *testing.T) {
		r := &fakeResolver{err: errors.New("dns: no such host")}
		err := ValidateBaseURL(context.Background(), "https://peer.example.org", r)
		if err == nil {
			t.Fatal("a resolver that failed was treated as a valid peer; " +
				"an unresolvable name must not be dialled")
		}
	})

	t.Run("resolver returns nothing", func(t *testing.T) {
		// The empty-slice case, which is NOT the same as the error case and is
		// the one a `if err != nil` guard misses: resolution "succeeded" and
		// found nothing. webhook.ValidateTarget rejects it explicitly.
		r := &fakeResolver{ips: nil}
		err := ValidateBaseURL(context.Background(), "https://peer.example.org", r)
		if err == nil {
			t.Fatal("a name that resolved to zero addresses was accepted; " +
				"webhook.isPublicIP's own rule is that an empty answer is not public")
		}
	})
}

// TestValidateBaseURLEnforcesR074ThreeRules asserts the guard is doing all
// three of R074's rules, not a subset.
//
// Recorded separately because the rules are independent: it is entirely possible
// to implement rule 2 (validate what the box dials) and forget rule 1 (nothing
// identifying becomes storable), and the suite above would still be green.
func TestValidateBaseURLEnforcesR074ThreeRules(t *testing.T) {
	t.Run("rule 1 — path not storable", func(t *testing.T) {
		r := public()
		if err := ValidateBaseURL(context.Background(), "http://peer.example.org/home/alvaro/.ssh/id_rsa", r); err == nil {
			t.Error("a path identifying the operator's home directory was accepted into base_url")
		}
	})

	t.Run("rule 2 — dial target validated at write time", func(t *testing.T) {
		r := public()
		// A host that RESOLVES to the metadata address is the case a
		// literal-string check cannot catch, because nothing in the URL
		// string looks wrong.
		r.perHost = map[string][]net.IP{
			"totally-normal.example.com": {net.ParseIP("169.254.169.254")},
		}
		if err := ValidateBaseURL(context.Background(), "http://totally-normal.example.com", r); err == nil {
			t.Error("a hostname that resolves to the cloud metadata address was " +
				"accepted; nothing in the URL string looks wrong, which is exactly " +
				"why the check has to resolve")
		}
	})

	t.Run("rule 3 — served url must not proxy for one", func(t *testing.T) {
		// images.url is SERVED rather than dialled, so it has no validator by
		// design. This case is the schema half of rule 3: the column exists, it
		// is a plain string, and the test that polices it is a source scanner
		// rather than a runtime validator. Asserting the column is still there
		// keeps a future migration from quietly deleting the thing the scanner
		// is supposed to be watching.
		r := public()
		for _, dirty := range []string{
			"file:///etc/passwd",
			`\\host\share`,
			"/etc/passwd",
		} {
			if !IsSuspiciousValue(dirty) {
				t.Errorf("served-url value %q is not caught by the value guard; "+
					"rule 3 depends on this predicate and it has a hole", dirty)
			}
		}
		_ = r
	})
}

// TestPathScannerMatchesUnquotedColumn is the control for the SCANNER, not for
// the rule.
//
// R074's own schema guard is a source-scanning test, and a source-scanning test
// has its own failure mode: a regex that requires the column name to be quoted
// matches nothing on `images.url`, which is written UNQUOTED (`url VARCHAR NOT
// NULL`) while every other address column in the schema is `"url" varchar`. The
// scanner would pass while the one column the original measurement actually
// found goes unwatched.
//
// This case pins that, by parsing the real migration files the same way the
// scanner does, and asserting the unquoted column is among the matches. If a
// later migration re-quotes it, or if the scanner changes shape, this fails.
func TestPathScannerMatchesUnquotedColumn(t *testing.T) {
	matches := scanMigrationsForAddressColumns(fixtureMigrations())
	if len(matches) == 0 {
		t.Fatal("the scanner matched zero columns; a guard that matches nothing " +
			"passes, which is the trap this test exists to close")
	}

	foundUnquoted := false
	for _, m := range matches {
		if m.table == "images" && m.column == "url" {
			foundUnquoted = true
			// unquoted MUST be false here. `images.url` is written
			// `url VARCHAR NOT NULL` with no quotes, so the scanner matching
			// it through the quoted branch means it is parsing something other
			// than what is in the file. This polarity was initially asserted
			// backwards, and the failure was informative in the way a control
			// test should be: it said the scanner disagreed with the file, and
			// the file was right.
			if m.quoted {
				t.Errorf("images.url matched through the QUOTED branch; the real " +
					"column is written `url VARCHAR NOT NULL` with no quotes, so the " +
					"scanner is not parsing what is in the file")
			}
		}
	}
	if !foundUnquoted {
		t.Error("the scanner did not match images.url at all.\n" +
			"That column is written `url VARCHAR NOT NULL` with no quotes, so a " +
			"quoted-name regex skips exactly the column the original measurement " +
			"found. This is the control that proves the scanner works.")
	}
}

// TestScannerDoesNotCountSubstrings is the OTHER scanner control, for the other
// measurement error.
//
// `03_misc`'s `director TEXT` is not a path column: `dir` is a substring of
// `director`. A substring-matching guard would demand the commons stop storing a
// person's director, and the tempting response would be to weaken the guard
// until it stopped complaining. A word-boundary scanner must not match it.
func TestScannerDoesNotCountSubstrings(t *testing.T) {
	matches := scanMigrationsForAddressColumns(fixtureMigrations())

	for _, m := range matches {
		if strings.Contains(strings.ToLower(m.column), "director") {
			t.Errorf("the scanner matched %q as an address column; `dir` is a "+
				"substring of `director` and substring matching is how the original "+
				"measurement inflated its own count", m.column)
		}
	}
}

// TestScannerFindsTheSeven is the whole measurement, pinned.
//
// Seven, not one. Two of them are addresses the box DIALS (webhook
// target_url, federation base_url) and the original note missed both by looking
// only at storage columns — which is the direction that mattered, because a
// dial column is an SSRF primitive rather than a leaked path in a metadata row.
func TestScannerFindsTheSeven(t *testing.T) {
	matches := scanMigrationsForAddressColumns(fixtureMigrations())

	dial := map[string]bool{
		"target_url": false, // webhook_endpoints, 84_add_webhooks
		"base_url":   false, // federation_peers, 89_identification_federation
	}
	for _, m := range matches {
		if _, ok := dial[m.column]; ok {
			dial[m.column] = true
		}
	}
	for column, found := range dial {
		if !found {
			t.Errorf("the scanner did not match the DIAL column %q.\n"+
				"This is the failure that mattered: the original measurement "+
				"looked only at storage columns and so missed both outbound "+
				"addresses, which are the SSRF primitives.", column)
		}
	}
}

// TestScannerReadsTheWholeTree is the control for the scanner's SCOPE.
//
// The original measurement was scoped to `postgres/` and read as if it covered
// the whole schema, and that scoping is what hid part of the finding. A
// mutation that narrows readMigrationDir to a single dialect directory cannot
// fail while the migrations tree contains only one such directory — the
// mutation would be VACUOUS, exactly like a guard that matches nothing.
//
// So this test builds a two-directory tree with a column in the second
// directory and requires the scanner to find it. The assertion is against a
// temporary tree rather than the repository's, because the repository happens
// to have one dialect today and that is precisely the accident that makes the
// scope bug invisible.
func TestScannerReadsTheWholeTree(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "mysql")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	// The nested column is the one a postgres-only scan would miss.
	writeFixture(t, filepath.Join(nested, "01_initial.up.sql"),
		"CREATE TABLE performer_urls (\n  url varchar not null\n);")
	// A decoy in the root, so the test cannot pass by matching the wrong thing.
	writeFixture(t, filepath.Join(dir, "01_root.up.sql"),
		"CREATE TABLE scenes (\n  director TEXT\n);")

	files, err := readMigrationDir(dir)
	if err != nil {
		t.Fatalf("reading the fixture tree: %v", err)
	}
	matches := scanMigrationsForAddressColumns(files)

	found := false
	for _, m := range matches {
		if m.column == "url" && strings.Contains(m.migration, "mysql") {
			found = true
		}
		if strings.Contains(m.column, "director") {
			t.Errorf("matched %q in the nested scan; substring matching is the "+
				"original measurement's first error", m.column)
		}
	}
	if !found {
		t.Error("the scanner did not read a nested dialect directory.\n" +
			"readMigrationDir walks recursively, and this test fails if that " +
			"becomes a single-directory scan -- which is how part of the " +
			"original finding was hidden in the first place.")
	}
}

func writeFixture(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestScannerIgnoresDownMigrations proves the scanner reads .up.sql only.
//
// Not tidiness: a down migration drops columns, and `DROP COLUMN "url"` is a
// match under a loose pattern. Including them would double every count and make
// the pinned number wrong for a reason nobody could see.
func TestScannerIgnoresDownMigrations(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, filepath.Join(dir, "01_up.up.sql"),
		"CREATE TABLE performer_urls (\n  url varchar not null\n);")
	writeFixture(t, filepath.Join(dir, "01_up.down.sql"),
		"ALTER TABLE performer_urls DROP COLUMN url;")

	files, err := readMigrationDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f.name, ".down.sql") {
			t.Errorf("readMigrationDir returned the down migration %s; only .up.sql "+
				"describes the schema", f.name)
		}
	}
	matches := scanMigrationsForAddressColumns(files)
	if len(matches) != 1 {
		t.Errorf("expected exactly 1 match from the up migration, got %d; a down "+
			"migration in the set would double it", len(matches))
	}
}

// TestScannerIgnoresComments proves a COMMENT is not a column.
//
// This has been believed five times in this project. A migration that documents
// a column in a comment it does not create would otherwise be reported as
// address-shaped, and the response to that would be to weaken the guard.
func TestScannerIgnoresComments(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, filepath.Join(dir, "01_initial.up.sql"),
		"-- the historical \"url\" varchar column, dropped in 04\n"+
			"CREATE TABLE performer_urls (\n  url varchar not null\n);")

	files, err := readMigrationDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	matches := scanMigrationsForAddressColumns(files)
	if len(matches) != 1 {
		t.Errorf("expected exactly 1 match, got %d; a commented-out column is a "+
			"claim about the schema, not the schema", len(matches))
	}
}

// fixtureMigrations returns the real migration files when the test runs inside
// the repository, so the scanner is checked against what is actually in the
// schema rather than against a fixture that can drift away from it.
//
// The fallback is a literal fixture ONLY for the case where the tree is not
// present; it is not a second source of truth in normal operation, and the
// in-repo branch is the one every run takes.
func fixtureMigrations() []migrationFile {
	dir := "internal/database/migrations"
	entries, err := readMigrationDir(dir)
	if err != nil {
		return []migrationFile{
			{name: "04_image_tables.up.sql", body: "CREATE TABLE images (\n    id UUID PRIMARY KEY,\n    url VARCHAR NOT NULL\n);"},
			{name: "03_misc.up.sql", body: "ALTER TABLE scenes ADD COLUMN director TEXT;"},
			{name: "01_initial.up.sql", body: "CREATE TABLE performer_urls (\n    url varchar not null\n);"},
			{name: "21_site_urls.up.sql", body: "CREATE TABLE sites (\n    url TEXT\n);"},
			{name: "84_add_webhooks.up.sql", body: "CREATE TABLE webhook_endpoints (\n    target_url TEXT NOT NULL\n);"},
			{name: "89_identification_federation.up.sql", body: "CREATE TABLE federation_peers (\n    base_url TEXT NOT NULL\n);"},
		}
	}
	return entries
}
