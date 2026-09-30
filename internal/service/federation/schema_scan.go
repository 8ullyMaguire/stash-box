package federation

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// This file is R074's SCHEMA half: the guard that watches the address-shaped
// columns in the migrations, as opposed to baseurl.go's guard on the values
// that get written into one of them.
//
// Why it exists as a source scan rather than a database query. The thing worth
// protecting is the SCHEMA — a column that can hold a path is a column that can
// be given one, and the interesting property is about what the migration files
// SAY, not about what any particular running database happens to contain.
// Querying a live database would answer "what is true of today's schema" and
// stay silent about a migration added tomorrow, which is when the hole appears.
//
// And this is exactly the shape of guard that the stash exporter got wrong, so
// the failure mode is known rather than hypothetical: a source scanner that
// matches ZERO things passes. Every function here is therefore written to be
// testable against the real files, and the tests in baseurl_test.go include
// controls for the SCANNER, not only for the rule it polices.

// addressColumn is one address-shaped column found in a migration.
type addressColumn struct {
	migration string
	table     string
	column    string
	// quoted records whether the column name appeared in double quotes in the
	// source. It is carried rather than discarded because the difference is
	// load-bearing: `images.url` is written UNQUOTED and every other address
	// column is quoted, so a scanner that silently normalised both would look
	// identical in its results while being unable to see the one column the
	// original measurement actually found.
	quoted bool
	// line is 1-based, for an error message a human can act on.
	line int
}

// migrationFile is one migration's name and body.
type migrationFile struct {
	name string
	body string
}

// addressWords is the closed set of address-shaped column names.
//
// Closed rather than a wildcard because a guard that matches everything is a
// guard that stops being read. Kept as a single list because the quoted and
// unquoted alternatives have to agree on it, and two copies of a word list is a
// way for them to drift.
var addressWords = []string{
	"path", "paths",
	"url", "urls",
	"host", "hostname", "hosts",
	"dir", "directory",
	"file", "filepath", "filename",
	"uri",
	"address", "addr",
	"endpoint",
	"target_url", "base_url",
}

// addressAlternation renders addressWords as a regex alternation.
//
// Built from the list rather than written out, because a hand-written
// alternation is a place for the two quoting forms below to disagree about
// which words exist — and a disagreement there is invisible until a new column
// name is added to one and not the other.
func addressAlternation() string {
	return strings.Join(addressWords, "|")
}

// columnPattern matches a column declaration whose NAME is one of the
// address-shaped words and whose TYPE is text-ish.
//
// THREE THINGS IT DELIBERATELY DOES, and two of them were measurement errors
// before they were design:
//
//   - The unquoted alternative is inside \b word boundaries. A plain substring
//     search counts `03_misc`'s `director TEXT` as a path column, because `dir`
//     is a substring of `director`; that inflated the original count from six to
//     seven in the harmless direction, and a guard built the same way would
//     demand the commons stop storing a person's director.
//   - The unquoted form is matched as its OWN alternative rather than by
//     making the quotes optional. `images.url` is written `url VARCHAR NOT
//     NULL` with no quotes while every other column is `"url" varchar`, so a
//     regex that requires the quotes misses precisely the column that matters.
//     (RE2 has no backreferences, so "optional quotes" cannot also mean
//     "balanced quotes" — hence two alternatives and two capture groups.)
//   - The type list includes inet, cidr and citext, not just text and varchar:
//     an address column can be declared as an inet and is then just as
//     address-shaped.
var columnPattern = regexp.MustCompile(
	`(?i)(?:"(` + addressAlternation() + `)"|\b(` + addressAlternation() + `)\b)\s+(?:text|varchar|character\s+varying|inet|cidr|citext)\b`)

// tablePattern captures the table a column belongs to.
//
// Table tracking is done by scanning backwards for the nearest CREATE TABLE,
// which is a heuristic and is documented as one here rather than left to be
// discovered: a hand-maintained table/column list would rot silently, and the
// scanner's job is precisely to notice columns nobody listed. A wrong table
// attribution shows up as a wrong error message, never as a wrong PASS, because
// the column name and its presence are what the rule decides on.
var tablePattern = regexp.MustCompile(`(?i)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?("?[\w]+"?)`)

// scanMigrationsForAddressColumns finds every address-shaped column across a
// set of migration files.
//
// Returns one entry per match, with the migration, table, column and line, so a
// failure names the file and the line a human has to open.
func scanMigrationsForAddressColumns(files []migrationFile) []addressColumn {
	var out []addressColumn
	for _, f := range files {
		lines := strings.Split(f.body, "\n")
		table := ""
		for i, line := range lines {
			// A COMMENT is a claim about the schema, not the schema. This has
			// been believed five times in this project, and a migration that
			// documents a column it does not create is exactly how a scanner
			// starts reporting things that are not there.
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "--") {
				continue
			}
			if m := tablePattern.FindStringSubmatch(line); m != nil {
				table = strings.Trim(m[1], `"`)
			}
			m := columnPattern.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			// Group 1 is the quoted name, group 2 the unquoted one. Exactly one
			// is set, and which one is the `quoted` field — carried rather than
			// discarded, because the difference is what the unquoted-column
			// control test asserts on.
			column := m[1]
			quoted := true
			if column == "" {
				column = m[2]
				quoted = false
			}
			out = append(out, addressColumn{
				migration: f.name,
				table:     table,
				column:    strings.ToLower(column),
				quoted:    quoted,
				line:      i + 1,
			})
		}
	}
	return out
}

// readMigrationDir reads every .sql file under dir, recursively.
//
// The whole tree and not `postgres/` alone: the original measurement was scoped
// to postgres/ and read as if it covered the whole schema, and that scoping is
// what hid part of the finding. A guard that watches one directory is a guard
// that watches one directory.
func readMigrationDir(dir string) ([]migrationFile, error) {
	var out []migrationFile
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".sql") {
			return nil
		}
		// down migrations are the inverse of the schema and would double every
		// match, so only .up.sql is read.
		if !strings.HasSuffix(path, ".up.sql") {
			return nil
		}
		b, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		out = append(out, migrationFile{name: path, body: string(b)})
		return nil
	})
	return out, err
}