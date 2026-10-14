//go:build integration

package api_test

import (
	"context"
	"testing"

	"github.com/99designs/gqlgen/client"

	"github.com/stashapp/stash-box/internal/queries"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/api"
	dbtest "github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/service"
)

// The content-item read surface, resolved (aggregator phase 3, slice 3c).
//
// These call the RESOLVER METHODS through the exported accessors -- the same objects
// the GraphQL server wires up -- rather than the queries underneath them. The point is
// the wiring: a resolver that compiles is not a resolver that is reachable, and the
// three joins this slice adds are exactly the ones a compilation cannot check:
//
//   1. the enum -> column mapping, in both directions
//   2. `content_item` as a GraphQL EntityType reaching the completion VOCABULARY.
//      `completionEntityType` is a plain cast, so a member the vocabulary does not know
//      does not fail -- it scores zero, which looks like "nothing is incomplete"
//   3. `ContentItem.completion` being a real resolver instead of an unpopulated
//      generated field. NOT covered here, and deliberately named rather than implied:
//      calling the accessor proves the method exists, not that the schema calls it.
//      That is held by gqlgen.yml and covered by the round-trip test below, which is
//      the only test that can see a null where a score belongs.
//
// The resolver methods take unexported types, so the accessors are what a test outside
// the package can use -- and that is the same surface models.ResolverRoot demands.

func TestAContentItemRoundTripsThroughTheResolvers(t *testing.T) {
	factory := service.NewFactory(dbtest.DB(), nil)
	resolvers := api.NewResolver(*factory)
	ctx := t.Context()

	item := newContentItem(t, "image", "Resolver round trip")

	got, err := resolvers.Query().ContentItem(ctx, item.ID)
	require.NoError(t, err)
	require.NotNil(t, got, "an existing item must resolve")
	assert.Equal(t, "Resolver round trip", got.Title)
	assert.Equal(t, models.ContentItemKindImage, got.Kind,
		"the column spelling must reach the client as the enum member that spells it")

	// A missing item is null and not an error: following a stale link is normal.
	missing, err := resolvers.Query().ContentItem(ctx, newUUID(t))
	require.NoError(t, err)
	assert.Nil(t, missing)

	// The kind filter filters. Sampled positively first -- every member carries the
	// kind asked for -- so the absence check below has a subject that exists.
	limit := 50
	images, err := resolvers.Query().QueryContentItems(ctx, ptrKind(models.ContentItemKindImage), &limit)
	require.NoError(t, err)
	require.NotEmpty(t, images, "the fixture makes at least one image item, so this page cannot be empty")
	for _, candidate := range images {
		assert.Equal(t, models.ContentItemKindImage, candidate.Kind)
	}

	// The discriminating half of the same claim: this image item is the NEWEST item, so
	// an unfiltered page contains it, and a comics page must not.
	comics, err := resolvers.Query().QueryContentItems(ctx, ptrKind(models.ContentItemKindComic), &limit)
	require.NoError(t, err)
	for _, candidate := range comics {
		assert.Equal(t, models.ContentItemKindComic, candidate.Kind,
			"a kind filter that returns other kinds has not filtered")
		assert.NotEqual(t, item.ID, candidate.ID,
			"the image fixture came back from a comics query, which means the filter is not applied")
	}

	// An absent filter is not a filter of nothing.
	all, err := resolvers.Query().QueryContentItems(ctx, nil, &limit)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(all), len(images))

	// A page size above the service ceiling is refused, not silently clamped: a
	// truncated list that looks complete is the failure the ceiling exists to prevent.
	tooBig := 500
	_, err = resolvers.Query().QueryContentItems(ctx, nil, &tooBig)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds the maximum")

	// The completion of an item: the join between slice 3c's resolvers and slice 3b's
	// vocabulary. A newly created item has neither tags nor media, so both must be
	// named as missing -- and if the vocabulary did not know the type, this would
	// return an error rather than a score.
	completion, err := resolvers.ContentItem().Completion(ctx, got)
	require.NoError(t, err)
	require.NotNil(t, completion)
	assert.Equal(t, models.EntityTypeContentItem, completion.EntityType)
	assert.Contains(t, completion.Missing, "tags")
	assert.Contains(t, completion.Missing, "urls")
	assert.Less(t, completion.Score, 100)

	// The enum member must reach the vocabulary through the resolver. `completionEntityType`
	// is a cast with no default, so a member the vocabulary has never heard of answers
	// zero here -- indistinguishable from "nothing is incomplete" unless a fixture exists.
	count, err := resolvers.Query().CountIncompleteEntities(ctx, models.EntityTypeContentItem, 100)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, count, 1,
		"the item created above is incomplete, so at least one incomplete content item exists; a zero means the enum member did not reach the vocabulary")
}

func ptrKind(kind models.ContentItemKind) *models.ContentItemKind { return &kind }

// ctxForFixtures is the request context the fixtures are written with. The harness's own
// context lives on the runner, and it is built for reading: reusing it here would tie a
// write to a request that has already been served.
func ctxForFixtures() context.Context { return context.Background() }

// The same surface over the wire, through the GraphQL handler the server builds.
//
// This is the test that can see a NULL where a score belongs. gqlgen binds a schema
// field to a struct field by default; for the types this repository generates from the
// schema (unlike its hand-written models) that means `completion` would be generated
// into ContentItem, the field resolver in internal/api would never run, and every
// client would read `"completion": null` while every Go test still passed. Only an
// executed query is evidence that gqlgen.yml's `resolver: true` is in effect.
func TestContentItemCompletionIsResolvedOverTheWire(t *testing.T) {
	runner := asRead(t)
	item := newContentItem(t, "text", "Over the wire")

	var resp struct {
		ContentItem *struct {
			Title       string  `json:"title"`
			Kind        string  `json:"kind"`
			ReleaseDate *string `json:"releaseDate"`
			Chapters    []struct {
				Number int     `json:"number"`
				Title  *string `json:"title"`
			} `json:"chapters"`
			Media []struct {
				URL   string `json:"url"`
				Kind  string `json:"kind"`
				Width *int   `json:"width"`
			} `json:"media"`
			Completion *struct {
				EntityType string   `json:"entityType"`
				Score      int      `json:"score"`
				Missing    []string `json:"missing"`
			} `json:"completion"`
		} `json:"contentItem"`
	}

	runner.client.MustPost(
		`query($id: ID!) { contentItem(id: $id) { title kind releaseDate completion { entityType score missing } chapters { number title } media { url kind width } } }`,
		&resp, client.Var("id", item.ID.String()))

	require.NotNil(t, resp.ContentItem, "an existing item must resolve by id")
	assert.Equal(t, "Over the wire", resp.ContentItem.Title)
	assert.Equal(t, "text", resp.ContentItem.Kind)
	assert.Nil(t, resp.ContentItem.ReleaseDate, "an item with no release date must be null, not a zero date")

	require.NotNil(t, resp.ContentItem.Completion,
		"completion must be RESOLVED: a null here means gqlgen bound the field to the generated struct")
	assert.Equal(t, "content_item", resp.ContentItem.Completion.EntityType)
	assert.Contains(t, resp.ContentItem.Completion.Missing, "tags")
	assert.Contains(t, resp.ContentItem.Completion.Missing, "urls")
	assert.Less(t, resp.ContentItem.Completion.Score, 100)

	// The list query with a kind argument, and the count with the new enum member.
	// chapters and media are generated FIELDS unless gqlgen.yml declares them external, and
	// an unpopulated generated list is `[]` -- which reads exactly like "this item has no
	// chapters". Fixtures go on the same item so an empty list cannot pass.
	chapterQ := contentItemQ()
	_, err := chapterQ.CreateContentItemChapter(ctxForFixtures(), queries.CreateContentItemChapterParams{
		ID: newUUID(t), ContentItemID: item.ID, Number: 1, Title: ptrString("Over the wire chapter"),
	})
	require.NoError(t, err)
	_, err = chapterQ.CreateContentItemMedia(ctxForFixtures(), queries.CreateContentItemMediaParams{
		ID: newUUID(t), ContentItemID: item.ID, Url: "https://example.invalid/wire.jpg", Kind: "original",
	})
	require.NoError(t, err)

	runner.client.MustPost(
		`query($id: ID!) { contentItem(id: $id) { chapters { number title } media { url kind } } }`,
		&resp, client.Var("id", item.ID.String()))
	require.Len(t, resp.ContentItem.Chapters, 1,
		"a chapter was created for this item, so an empty list means the field is bound to the generated struct instead of resolved")
	assert.Equal(t, "Over the wire chapter", *resp.ContentItem.Chapters[0].Title)
	require.Len(t, resp.ContentItem.Media, 1,
		"a media row was created for this item, so an empty list means the field is bound to the generated struct instead of resolved")
	assert.Equal(t, "https://example.invalid/wire.jpg", resp.ContentItem.Media[0].URL)
	assert.Equal(t, "original", resp.ContentItem.Media[0].Kind)

	var listResp struct {
		QueryContentItems []struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
		} `json:"queryContentItems"`
	}
	// Created here, newest first: the other-kind item would be in an UNFILTERED page,
	// which is what gives the absence check below teeth. Asserting only that every
	// member happened to carry the asked-for kind is vacuous whenever the unfiltered
	// page is homogeneous -- a mutation probe proved exactly that by removing the
	// filter and surviving.
	other := newContentItem(t, "image", "Over the wire (another kind)")

	runner.client.MustPost(`query { queryContentItems(kind: text, limit: 50) { id kind } }`, &listResp)
	require.NotEmpty(t, listResp.QueryContentItems)
	found, foundOther := false, false
	for _, candidate := range listResp.QueryContentItems {
		assert.Equal(t, "text", candidate.Kind, "the kind argument must filter")
		if candidate.ID == item.ID.String() {
			found = true
		}
		if candidate.ID == other.ID.String() {
			foundOther = true
		}
	}
	assert.True(t, found, "the fixture must appear in a page of its own kind")
	assert.False(t, foundOther,
		"an item of another kind was just created and would be in an unfiltered page, so its presence means the kind argument was parsed and not applied")

	var countResp struct {
		CountIncompleteEntities int `json:"countIncompleteEntities"`
	}
	runner.client.MustPost(`query { countIncompleteEntities(entityType: content_item, below: 100) }`, &countResp)
	assert.GreaterOrEqual(t, countResp.CountIncompleteEntities, 1,
		"a zero means the content_item enum member did not reach the completion vocabulary")
}
