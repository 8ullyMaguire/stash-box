//go:build integration

package api_test

import (
	"testing"

	"github.com/stashapp/stash-box/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Issue #778: "Commas in Aliases split the alias on call to ScrapeSinglePerformer".
//
// The report says aliases come back from a scrape as one comma-joined string
// ("abc, abc, def"), so a client splitting on the comma turns one alias into
// two, and the comma is what was keeping them distinct in the first place.
//
// That shape does not exist in this codebase. The scrape mutations
// (scrapeSinglePerformer and friends) are not part of the GraphQL schema at
// all -- there is no ScrapeSinglePerformer resolver, no ScrapedPerformer type,
// and nothing anywhere that joins aliases into a single string. The only
// "scrape" occurrence in the Go tree is an unrelated comment about Stash's
// fingerprint submission.
//
// aliases is `[String!]!` -- a real JSON list -- so a comma inside an element
// cannot be confused with a separator. These tests pin that: an alias
// containing a comma is stored intact and comes back as exactly one element.
//
// The underlying data hazard the reporter was reaching for IS real: a comma is
// a legal character in a name, and a downstream consumer that re-serialises
// aliases as a comma-joined string (which Stash's own importer does for its
// CSV-style fields) would corrupt it. That is a client-side concern; the
// server's job is to not lose the comma in the first place, and it does not.

func TestPerformerAliasWithCommaIsStoredIntact(t *testing.T) {
	pt := createPerformerTestRunner(t)

	// Two aliases that are distinct ONLY because of the comma between them.
	commaAlias := "Smith, Jane"
	plainAlias := "Smith Jane"

	p, err := pt.createTestPerformer(&models.PerformerCreateInput{
		Name:    pt.generatePerformerName(),
		Aliases: []string{commaAlias, plainAlias},
	})
	require.NoError(t, err)

	found, err := pt.client.findPerformer(p.UUID())
	require.NoError(t, err, "Error finding performer")

	// The comma must survive, and the two aliases must stay two entries.
	assert.Contains(t, found.Aliases, commaAlias,
		"alias containing a comma must round-trip unchanged (#778)")
	assert.Contains(t, found.Aliases, plainAlias,
		"alias without a comma must round-trip unchanged")
	assert.Len(t, found.Aliases, 2,
		"a comma inside an alias must not create an extra alias")
}

// A comma must not be treated as a delimiter on the way in either: submitting
// "a, b" as one alias has to stay one alias, distinct from "a" and "b".
func TestPerformerAliasWithCommaIsNotSplitOnWrite(t *testing.T) {
	pt := createPerformerTestRunner(t)

	commaAlias := "Doe, Jane"
	p, err := pt.createTestPerformer(&models.PerformerCreateInput{
		Name:    pt.generatePerformerName(),
		Aliases: []string{commaAlias},
	})
	require.NoError(t, err)

	found, err := pt.client.findPerformer(p.UUID())
	require.NoError(t, err)

	require.Len(t, found.Aliases, 1,
		"one alias with a comma must remain one alias, not two")
	assert.Equal(t, commaAlias, found.Aliases[0],
		"the comma and surrounding text must be preserved verbatim")
}
