//go:build integration

package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/config"
	"github.com/stashapp/stash-box/internal/database/testutil"
)

// THE LANDING PAGE (SPEC §11, plan phase 11 slice 11c): GET /invite/<code>.
//
// Two claims are enforced here and they pull in opposite directions, which is why they are in one
// file:
//
//   - It COUNTS A VISIT. A scan is a row, because a member needs to be able to tell "somebody looked"
//     from "somebody joined".
//   - It COUNTS NOTHING ELSE. No address, no agent, no account: the row this test reads is asserted to
//     carry a NULL user and a NULL-by-absence visitor, because a page that recorded who opened it
//     would be a page that records strangers.
//
// And the third claim, which is not about the counters at all: THE PAGE CANNOT BE ASKED WHETHER A KEY
// IS GOOD. A live key, an expired key and a code that was never a key get the same document and the
// same headers -- the only difference is the echo of what the visitor typed, which they already have.

// inviteRouter mounts the route the way rootRoutes does, so the test exercises the MOUNT and not
// only the handler: the paths inside a chi sub-router are relative, and a test that served the
// sub-router directly at the full path would 404 on a route that works in production (measured).
//
// The mount is registered BEFORE a catch-all here for the same reason it is in routes_root.go: the
// SPA is served for every other path, so an invitation registered after it would never be reached.
func inviteRouter(t *testing.T) chi.Router {
	t.Helper()
	r := chi.NewRouter()
	r.Mount("/invite", inviteRoutes{fac: *testutil.Factory()}.Routes())
	r.HandleFunc("/*", func(w http.ResponseWriter, _ *http.Request) {
		t.Error("the SPA catch-all answered: the invitation route was swallowed by it")
		http.Error(w, "app", http.StatusTeapot)
	})
	return r
}

func getInvitePage(t *testing.T, code string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/invite/"+code, nil)
	inviteRouter(t).ServeHTTP(rec, req)
	return rec
}

// insertInviteKey makes a key the way the service does, with the facts the test needs to vary.
func insertInviteKey(t *testing.T, member uuid.UUID, uses *int, expire *time.Time) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV4())
	_, err := testutil.DB().Exec(t.Context(),
		`insert into invite_keys (id, generated_by, generated_at, uses, expire_time)
		 values ($1, $2, now(), $3, $4)`, id, member, uses, expire)
	require.NoError(t, err)
	return id
}

// anInvitingMember is any existing account to hang the key on. The counters are read as a DELTA
// around what this test does, so the member's own history does not matter.
func anInvitingMember(t *testing.T) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, testutil.DB().QueryRow(t.Context(), `select id from users limit 1`).Scan(&id))
	return id
}

type scanRow struct {
	Kind        string
	UserID      *uuid.UUID
	InviteKeyID *uuid.UUID
}

// eventsFor reads the journal for a member, which is the ONLY place these facts live: there is no
// counter column to disagree with.
func eventsFor(t *testing.T, member uuid.UUID) []scanRow {
	t.Helper()
	rows, err := testutil.DB().Query(t.Context(),
		`select kind, user_id, invite_key_id from invite_key_events where generated_by = $1 order by id`, member)
	require.NoError(t, err)
	defer rows.Close()

	var out []scanRow
	for rows.Next() {
		var r scanRow
		require.NoError(t, rows.Scan(&r.Kind, &r.UserID, &r.InviteKeyID))
		out = append(out, r)
	}
	require.NoError(t, rows.Err())
	return out
}

func scansOf(events []scanRow) int {
	n := 0
	for _, e := range events {
		if e.Kind == "scan" {
			n++
		}
	}
	return n
}

// A VISIT IS COUNTED, AND A VISIT RECORDS NOBODY.
func TestTheLandingPageCountsAVisitAndRecordsNobodyInParticular(t *testing.T) {
	member := anInvitingMember(t)
	uses := 5
	key := insertInviteKey(t, member, &uses, nil)

	before := eventsFor(t, member)

	res := getInvitePage(t, key.String())
	require.Equal(t, http.StatusOK, res.Code,
		"the page is served to an unauthenticated GET: a shareable link cannot need an account")

	// WHAT THE PAGE SAYS, asserted positively before anything is asserted about what it omits (an
	// absence assertion needs its subject counted, or it passes on an empty document).
	body := res.Body.String()
	assert.Contains(t, body, config.GetTitle(), "the page names the instance")
	assert.Contains(t, body, key.String(), "and it echoes the key the visitor was given")
	assert.Equal(t, 1, strings.Count(body, key.String()),
		"exactly once, where it was typed: the page does not repeat the key as a link anywhere")
	assert.Contains(t, res.Header().Get("X-Robots-Tag"), "noindex",
		"a key in a search index is a key that is no longer private")
	assert.Contains(t, res.Header().Get("Referrer-Policy"), "same-origin",
		"so the key does not travel to the next site in a Referer header")

	events := eventsFor(t, member)
	require.Len(t, events, len(before)+1, "one visit is one row")
	scan := events[len(events)-1]
	assert.Equal(t, "scan", scan.Kind)
	assert.Nil(t, scan.UserID, "A SCAN RECORDS NOBODY: there is no account to name")
	require.NotNil(t, scan.InviteKeyID)
	assert.Equal(t, key, *scan.InviteKeyID, "and it names the key it was a visit to")
	assert.Equal(t, 0, len(events)-len(before)-scansOf(events[len(before):]),
		"and the visit moved NOTHING else: no signup, no activation, because nobody did anything yet")

	// A second visit is a second scan: the counter counts VISITS. (Stated because the alternative --
	// counting visitors -- is the reading that would make the number mean something else.)
	getInvitePage(t, key.String())
	assert.Equal(t, 2, scansOf(eventsFor(t, member)[len(before):]),
		"two visits are two scans; this counter counts visits, not visitors")
}

// THE PLAN'S OWN SENTENCE: a scan of an expired key is not counted. And the page it gets is the page a
// live key gets, which is the anti-enumeration answer to the obvious follow-up question.
func TestAnExpiredKeysScanIsNotCountedAndItsPageIsALiveKeysPage(t *testing.T) {
	member := anInvitingMember(t)
	uses := 5
	yesterday := time.Now().Add(-24 * time.Hour)

	live := insertInviteKey(t, member, &uses, nil)
	expired := insertInviteKey(t, member, &uses, &yesterday)

	before := eventsFor(t, member)

	liveRes := getInvitePage(t, live.String())
	require.Equal(t, http.StatusOK, liveRes.Code)
	liveEvents := eventsFor(t, member)
	require.Equal(t, len(before)+1, len(liveEvents), "the live key's visit was counted")

	expiredRes := getInvitePage(t, expired.String())
	require.Equal(t, http.StatusOK, expiredRes.Code,
		"an expired key is not an error page: the visitor is not told the key is dead either")
	assert.Equal(t, len(liveEvents), len(eventsFor(t, member)),
		"AND THE EXPIRED KEY'S VISIT IS NOT COUNTED -- the member must not be told of people who "+
			"could never have joined through a key that was already dead")

	// The only difference between the two documents is the code the visitor typed.
	assert.Equal(t, normalizeKey(liveRes.Body.String(), live),
		normalizeKey(expiredRes.Body.String(), expired),
		"the same document for a live key and an expired one: the page cannot be asked whether a key is good")
	assert.Equal(t, liveRes.Header().Clone(), expiredRes.Header().Clone(),
		"and the same headers, since a header that varied would be just as askable")
}

// A code that was never a key gets that same document too -- and counts nothing, not even a visit that
// nobody made. This is the case that makes the page safe to serve to the internet.
func TestACodeThatIsNotAKeyGetsTheSamePageAndCountsNothing(t *testing.T) {
	member := anInvitingMember(t)
	uses := 5
	live := insertInviteKey(t, member, &uses, nil)

	before := len(eventsFor(t, member))
	liveRes := getInvitePage(t, live.String())
	require.Equal(t, http.StatusOK, liveRes.Code)
	require.Equal(t, before+1, len(eventsFor(t, member)), "the subject: a real key's visit counts")

	for _, code := range []string{"not-a-uuid", uuid.Must(uuid.NewV4()).String()} {
		res := getInvitePage(t, code)
		assert.Equal(t, http.StatusOK, res.Code, "code %q: served, not refused", code)
		assert.Equal(t, normalizeKey(liveRes.Body.String(), live),
			strings.ReplaceAll(res.Body.String(), code, "<CODE>"),
			"code %q: the same document as a live key's", code)
	}
	assert.Equal(t, before+1, len(eventsFor(t, member)),
		"and neither invented code recorded anything: there was no key to record a visit to")
}

// normalizeKey removes the one thing that legitimately differs between two visits, the echoed code, so
// the comparison is about the DOCUMENT rather than about the string somebody typed.
func normalizeKey(body string, key uuid.UUID) string {
	return strings.ReplaceAll(body, key.String(), "<CODE>")
}
