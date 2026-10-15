//go:build integration

package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/auth"
	dbtest "github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/service/savedsearch"
)

// A SAVED SEARCH AS A FEED: GET /feeds/saved-search/<token> (plan slice 13b).
//
// The claims here are the ones a feed can get wrong while looking perfect in a reader, and each of them
// is about the URL being a CREDENTIAL that lives in somebody else's config file:
//
//  1. A token that was never minted, a token of the wrong shape and a token whose feed was disabled get
//     the SAME 404 -- body and all. A different answer for any of them turns this endpoint into a
//     scanner for which searches exist on a private instance.
//  2. Only the HASH is stored. Asserted against the database rather than the code: a row that held the
//     token itself would make "we can't show you that URL again" a lie.
//  3. The feed carries the search's OWN records -- compared against the same service call the search page
//     makes, so a feed that grew its own query path shows up as a disagreement.
//  4. Disabling makes the URL dead, and enabling again ROTATES: the old URL stays dead.
//  5. The response headers are part of the design (noindex, private, no-referrer), because the URL is the
//     credential and an indexed, shared-cached or referrer-leaked feed URL is a published one.

// feedRouter mounts the route the way rootRoutes does, so the test exercises the MOUNT rather than only
// the handler: the paths inside a chi sub-router are relative, and a test that served the sub-router at
// the full path would 404 on a route that works in production (a trap the invitation page's test records).
//
// The catch-all is registered for the same reason it is in routes_root.go: the SPA answers every other
// path, so a feed route registered after it would never be reached -- and this test would then be reading
// the application's index.html instead of failing.
func feedRouter(t *testing.T) chi.Router {
	t.Helper()
	r := chi.NewRouter()
	r.Mount("/feeds/saved-search", feedRoutes{fac: *dbtest.Factory()}.Routes())
	r.HandleFunc("/*", func(w http.ResponseWriter, _ *http.Request) {
		t.Error("the SPA catch-all answered: the feed route was swallowed by it")
		http.Error(w, "app", http.StatusTeapot)
	})
	return r
}

// getFeed performs the request a feed reader would: a bare GET, no headers, no session.
func getFeed(t *testing.T, token string, base ...string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/feeds/saved-search/"+token, nil)
	if len(base) > 0 {
		req = req.WithContext(context.WithValue(req.Context(), BaseURLCtxKey, base[0]))
	}
	feedRouter(t).ServeHTTP(rec, req)
	return rec
}

// feedDocument is the subset of the document these tests read back. It is parsed with encoding/xml -- the
// same rules a reader applies -- rather than searched for with a substring match, because a document that
// only LOOKS right is the failure mode of hand-built XML.
type feedDocument struct {
	XMLName xml.Name `xml:"rss"`
	Version string   `xml:"version,attr"`
	Channel struct {
		Title       string `xml:"title"`
		Link        string `xml:"link"`
		Description string `xml:"description"`
		Generator   string `xml:"generator"`
		Items       []struct {
			Title   string `xml:"title"`
			Link    string `xml:"link"`
			PubDate string `xml:"pubDate"`
			GUID    struct {
				IsPermaLink string `xml:"isPermaLink,attr"`
				Value       string `xml:",chardata"`
			} `xml:"guid"`
			Description string `xml:"description"`
		} `xml:"item"`
	} `xml:"channel"`
}

func parseFeed(t *testing.T, body string) feedDocument {
	t.Helper()
	var doc feedDocument
	require.NoError(t, xml.Unmarshal([]byte(body), &doc), "the feed must be well-formed XML: %s", body)
	return doc
}

// insertSavedSearch writes a search the way the service does, minus the GraphQL layer: this test is about
// the transport and the token, and a search created through a mutation would add a client to the path
// without adding a claim.
func insertSavedSearch(t *testing.T, userID uuid.UUID, name, query string) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV4())
	_, err := dbtest.DB().Exec(t.Context(),
		`insert into saved_searches (id, user_id, name, query) values ($1, $2, $3, $4)`,
		id, userID, name, query)
	require.NoError(t, err)
	return id
}

// insertSceneWithTag makes one record matching a bare-word query (`tag`). The tag search table is kept by
// a TRIGGER on scene_tags (migration 103), so an insert here is visible to the query language the same way
// an application insert is.
func insertSceneWithTag(t *testing.T, title string) (uuid.UUID, string) {
	t.Helper()

	// One tag name per call, and unique: the package shares a database, and a bare word matches by name.
	tagName := "walkfeed" + strings.ReplaceAll(uuid.Must(uuid.NewV4()).String()[:8], "-", "")
	tagID := uuid.Must(uuid.NewV4())
	_, err := dbtest.DB().Exec(t.Context(),
		`insert into tags (id, name, created_at, updated_at) values ($1, $2, now(), now())`,
		tagID, tagName)
	require.NoError(t, err)

	sceneID := uuid.Must(uuid.NewV4())
	_, err = dbtest.DB().Exec(t.Context(),
		`insert into scenes (id, title, created_at, updated_at) values ($1, $2, now(), now())`,
		sceneID, title)
	require.NoError(t, err)

	_, err = dbtest.DB().Exec(t.Context(),
		`insert into scene_tags (scene_id, tag_id) values ($1, $2)`, sceneID, tagID)
	require.NoError(t, err)

	// The tag rows this test wrote are cleaned up with the scenes they belong to (cascade on scene_tags),
	// and the tag itself is left behind deliberately: a tag is a shared vocabulary, and a test that
	// deleted one could race the search tables other tests read.
	return sceneID, tagName
}

func anyUser(t *testing.T) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, dbtest.DB().QueryRow(t.Context(), `select id from users limit 1`).Scan(&id))
	return id
}

// ---------------------------------------------------------------- the 404, and it is one 404

func TestEveryFeedThatDoesNotResolveIsTheSame404(t *testing.T) {
	owner := anyUser(t)

	// A token this instance never minted, in the shape a real one has.
	unknown := strings.Repeat("a", savedsearch.FeedTokenLength)
	// A path that is not a token at all.
	malformed := "not-a-token"

	minted := insertSavedSearch(t, owner, "feed-miss-"+uuid.Must(uuid.NewV4()).String()[:8], "anything")
	token, err := dbtest.Factory().SavedSearch().EnableFeed(t.Context(), owner, minted)
	require.NoError(t, err)
	if _, err := dbtest.Factory().SavedSearch().DisableFeed(t.Context(), owner, minted); err != nil {
		t.Fatalf("could not disable the feed this test made: %v", err)
	}

	responses := map[string]*httptest.ResponseRecorder{
		"never minted": getFeed(t, unknown),
		"not a token":  getFeed(t, malformed),
		"disabled":     getFeed(t, token),
	}

	first := ""
	for name, rec := range responses {
		assert.Equal(t, http.StatusNotFound, rec.Code, "%s must be 404", name)
		if first == "" {
			first = rec.Body.String()
			continue
		}
		// THE BODY TOO, not only the status: a body that differed would say which case it was.
		assert.Equal(t, first, rec.Body.String(), "%s must be indistinguishable from the others", name)
	}
}

// ---------------------------------------------------------------- the token is stored as a hash

func TestAFeedStoresOnlyTheHashOfItsToken(t *testing.T) {
	owner := anyUser(t)
	id := insertSavedSearch(t, owner, "feed-hash-"+uuid.Must(uuid.NewV4()).String()[:8], "anything")

	token, err := dbtest.Factory().SavedSearch().EnableFeed(t.Context(), owner, id)
	require.NoError(t, err)
	require.Len(t, token, savedsearch.FeedTokenLength)

	sum := sha256.Sum256([]byte(token))
	want := hex.EncodeToString(sum[:])

	var stored *string
	require.NoError(t, dbtest.DB().QueryRow(t.Context(),
		`select feed_token_hash from saved_searches where id = $1`, id).Scan(&stored))

	require.NotNil(t, stored, "enabling a feed must store something")
	assert.Equal(t, want, *stored, "the stored value is the SHA-256 of the token")
	assert.NotEqual(t, token, *stored, "and the token itself is nowhere in the table")

	// The owner can still tell that the feed EXISTS -- that is the whole of `feed_enabled`.
	searches, err := dbtest.Factory().SavedSearch().FindByUser(t.Context(), owner)
	require.NoError(t, err)
	var found bool
	for _, search := range searches {
		if search.ID == id {
			found = true
			assert.True(t, search.FeedEnabled, "a search with a token reports a feed")
		}
	}
	assert.True(t, found, "the search is in its owner's list")
}

// ---------------------------------------------------------------- the records, and the owner's view

func TestAFeedCarriesTheSameRecordsTheSearchDoes(t *testing.T) {
	owner := anyUser(t)
	sceneID, tagName := insertSceneWithTag(t, "Feed Record")
	id := insertSavedSearch(t, owner, "feed-items-"+uuid.Must(uuid.NewV4()).String()[:8], tagName)

	token, err := dbtest.Factory().SavedSearch().EnableFeed(t.Context(), owner, id)
	require.NoError(t, err)

	rec := getFeed(t, token, "https://example.test")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	doc := parseFeed(t, rec.Body.String())
	require.Len(t, doc.Channel.Items, 1, "the search matches the record this test made")

	item := doc.Channel.Items[0]
	assert.Equal(t, "Feed Record", item.Title)
	assert.Equal(t, "https://example.test/scenes/"+sceneID.String(), item.Link,
		"the item's link comes from the address the request arrived at, and points at the record")
	assert.Equal(t, sceneID.String(), item.GUID.Value, "the guid is the record's id")
	assert.Equal(t, "false", item.GUID.IsPermaLink, "and it is not the link, stated explicitly")
	assert.NotEmpty(t, item.PubDate)

	// THE FEED CANNOT ANSWER DIFFERENTLY FROM THE SEARCH. The reference is the same service call the
	// search page's own `results` field makes, run as the same owner -- so a feed that grew its own query
	// path, dropped a paging default or ran as nobody shows up here as a disagreement rather than as a
	// feed that looks plausible.
	// The same filter the feed builds, through the same service the search page uses, as the same owner.
	query := tagName
	input := models.SceneQueryInput{
		Query:     &query,
		Page:      1,
		PerPage:   feedItemLimit,
		Sort:      models.SceneSortEnumDate,
		Direction: models.SortDirectionEnumDesc,
	}
	runCtx := context.WithValue(t.Context(), auth.ContextUser, &auth.AuthUser{ID: owner})
	scenes, err := dbtest.Factory().Scene().Query(runCtx, input)
	require.NoError(t, err)
	require.Len(t, scenes, 1)
	assert.Equal(t, sceneID, scenes[0].ID, "the search's own answer is the record the feed carried")
}

// ---------------------------------------------------------------- an empty feed says why

func TestAFeedThatMatchesNothingIsStillAFeed(t *testing.T) {
	owner := anyUser(t)

	// TWO KINDS OF EMPTY, and the first version of this test only had the first one -- a mutation that
	// removed the plain case's message survived it, because a term nobody can RESOLVE takes the other
	// branch. Both are real and a reader sees them differently:
	//
	//  1. A term that cannot be applied. The query language says so in `diagnostics`, and the feed has to
	//     carry the reason, because the alternative is a page that looks like it simply found nothing.
	//  2. A term that resolves perfectly and matches no record today. There is nothing to explain; what
	//     matters is that the feed still says it is a feed with nothing in it.
	unresolvable := insertSavedSearch(t, owner, "feed-empty1-"+uuid.Must(uuid.NewV4()).String()[:8], "nothingcarriesthis")

	// A tag that exists and carries no records: resolution succeeds, the result set is empty.
	_, emptyTag := insertSceneWithTag(t, "A record for the tag, deleted below")
	_, err := dbtest.DB().Exec(t.Context(), `delete from scene_tags where tag_id = (select id from tags where name = $1)`, emptyTag)
	require.NoError(t, err)
	resolvable := insertSavedSearch(t, owner, "feed-empty2-"+uuid.Must(uuid.NewV4()).String()[:8], emptyTag)

	for _, tc := range []struct {
		name     string
		searchID uuid.UUID
		query    string
		expect   string
	}{
		{"a term that cannot be applied", unresolvable, "nothingcarriesthis", "no matches:"},
		{"a term that matches nothing today", resolvable, emptyTag, "no matches right now"},
	} {
		token, err := dbtest.Factory().SavedSearch().EnableFeed(t.Context(), owner, tc.searchID)
		require.NoError(t, err)

		rec := getFeed(t, token)
		// NOT a 404: the token resolved, so the feed exists. It is empty because the CATALOGUE is.
		require.Equal(t, http.StatusOK, rec.Code, tc.name)
		doc := parseFeed(t, rec.Body.String())
		assert.Empty(t, doc.Channel.Items, tc.name)
		assert.Contains(t, doc.Channel.Description, tc.query,
			"the channel carries the question, not just an answer")
		assert.Contains(t, doc.Channel.Description, tc.expect,
			"%s: an empty feed must say why it is empty, or it is indistinguishable from a broken one", tc.name)
	}
}

// ---------------------------------------------------------------- the headers, because the URL is a bearer

func TestAFeedIsNotIndexedNotSharedAndLeaksNoReferrer(t *testing.T) {
	owner := anyUser(t)
	id := insertSavedSearch(t, owner, "feed-headers-"+uuid.Must(uuid.NewV4()).String()[:8], "anything")
	token, err := dbtest.Factory().SavedSearch().EnableFeed(t.Context(), owner, id)
	require.NoError(t, err)

	rec := getFeed(t, token)
	require.Equal(t, http.StatusOK, rec.Code)

	assert.Equal(t, "application/rss+xml; charset=utf-8", rec.Header().Get("Content-Type"))
	assert.Equal(t, "noindex, nofollow", rec.Header().Get("X-Robots-Tag"))
	assert.Contains(t, rec.Header().Get("Cache-Control"), "private",
		"a SHARED cache holding a bearer URL would serve one member's search to the next caller")
	assert.NotContains(t, rec.Header().Get("Cache-Control"), "public")
	assert.Equal(t, "no-referrer", rec.Header().Get("Referrer-Policy"),
		"a reader clicking an item would otherwise send the token to wherever the item points")
}

// ---------------------------------------------------------------- disabling and rotating

func TestDisablingAFeedKillsItsURLAndEnablingAgainRotatesIt(t *testing.T) {
	owner := anyUser(t)
	sceneID, tagName := insertSceneWithTag(t, "Rotation Record")
	id := insertSavedSearch(t, owner, "feed-rotate-"+uuid.Must(uuid.NewV4()).String()[:8], tagName)

	first, err := dbtest.Factory().SavedSearch().EnableFeed(t.Context(), owner, id)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, getFeed(t, first).Code, "the first URL works")

	// Rotating: the gesture a member has after pasting their feed somewhere public.
	second, err := dbtest.Factory().SavedSearch().EnableFeed(t.Context(), owner, id)
	require.NoError(t, err)
	require.NotEqual(t, first, second, "enabling again mints a NEW token")

	assert.Equal(t, http.StatusNotFound, getFeed(t, first).Code, "the OLD url is dead")
	rec := getFeed(t, second)
	require.Equal(t, http.StatusOK, rec.Code, "and the new one works")
	require.Len(t, parseFeed(t, rec.Body.String()).Channel.Items, 1)
	assert.Equal(t, sceneID.String(), parseFeed(t, rec.Body.String()).Channel.Items[0].GUID.Value)

	// And disabling ends it.
	disabled, err := dbtest.Factory().SavedSearch().DisableFeed(t.Context(), owner, id)
	require.NoError(t, err)
	assert.True(t, disabled)
	assert.Equal(t, http.StatusNotFound, getFeed(t, second).Code, "the URL that worked a moment ago is dead")

	var stored *string
	require.NoError(t, dbtest.DB().QueryRow(t.Context(),
		`select feed_token_hash from saved_searches where id = $1`, id).Scan(&stored))
	assert.Nil(t, stored, "the hash is cleared, so the URL can never resolve again")
}

// ---------------------------------------------------------------- it is the OWNER's search

func TestAMemberCannotMintAFeedForSomebodyElsesSearch(t *testing.T) {
	owner := anyUser(t)
	id := insertSavedSearch(t, owner, "feed-owner-"+uuid.Must(uuid.NewV4()).String()[:8], "anything")

	other := uuid.Must(uuid.NewV4())
	_, err := dbtest.Factory().SavedSearch().EnableFeed(t.Context(), other, id)
	require.Error(t, err, "a token is minted for a search, and a search belongs to somebody")
	assert.ErrorIs(t, err, savedsearch.ErrNotFound,
		"and the refusal is the same one an id that does not exist gets, so it says nothing about whether the search exists")

	_, err = dbtest.Factory().SavedSearch().DisableFeed(t.Context(), other, id)
	assert.ErrorIs(t, err, savedsearch.ErrNotFound)

	var stored *string
	require.NoError(t, dbtest.DB().QueryRow(t.Context(),
		`select feed_token_hash from saved_searches where id = $1`, id).Scan(&stored))
	assert.Nil(t, stored, "the failed attempts left no feed behind")
}

// ---------------------------------------------------------------- and one thing about the shape

func TestAFeedTokenIsNeverAcceptedByAQueryThatWouldLeakIt(t *testing.T) {
	// The token is a bearer for ONE search and nothing else. This asserts the two properties that make
	// that true rather than merely likely: it is 43 URL-safe characters (so it cannot be confused with an
	// id or a path), and a string that is not of that shape never reaches the database at all.
	assert.False(t, savedsearch.FeedTokenShape(""))
	assert.False(t, savedsearch.FeedTokenShape("short"))
	assert.False(t, savedsearch.FeedTokenShape(strings.Repeat("a", savedsearch.FeedTokenLength+1)))
	assert.False(t, savedsearch.FeedTokenShape(strings.Repeat("a", 42)+"/"), "not URL-safe")
	assert.True(t, savedsearch.FeedTokenShape(strings.Repeat("a", savedsearch.FeedTokenLength)))

	found, err := dbtest.Factory().SavedSearch().FindByFeedToken(t.Context(), "not-a-token")
	require.NoError(t, err, "a malformed token is not an error")
	assert.Nil(t, found, "it is simply not a feed")
}
