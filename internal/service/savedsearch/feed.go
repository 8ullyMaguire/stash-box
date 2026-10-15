package savedsearch

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/gofrs/uuid"
	"github.com/stashapp/stash-box/internal/auth"
	"github.com/stashapp/stash-box/internal/converter"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/errutil"
)

// FEEDS: a saved search handed to a program that cannot log in (plan slice 13b).
//
// A feed reader sends no headers, so the credential has to be IN the URL -- and a URL is not private the
// way a header is. It is written into the reader's configuration file, into whatever proxy or gateway
// logs the request line, and into the `Referer` of every link a reader clicks out of the feed. So the
// token is designed for the place it has to live:
//
//   - ONE TOKEN PER SAVED SEARCH, never the member's `users.api_key`. An API key carries the member's
//     whole role authority and is a header credential; a leaked feed URL must cost one search.
//   - ONLY THE SHA-256 IS STORED. A dump of `saved_searches` therefore does not hand out feeds, and
//     "that URL is shown once" is a FACT about the schema rather than a policy it contradicts.
//   - ENABLING AGAIN ROTATES. A member who pasted their feed somewhere has exactly one remedy, and it is
//     the same gesture as starting: enable. Disabling clears the hash and the URL is dead.
//
// The hash is the LOOKUP KEY rather than something compared after a fetch, so there is no timing
// question to get wrong: a token that does not hash to a stored row finds no row.

const (
	// 32 bytes of crypto/rand. This string is the only thing between a stranger with a scanner and a
	// member's standing question, and a standing question names people.
	feedTokenBytes = 32

	// The base64url encoding of 32 bytes, unpadded. The SHAPE is checked before the database is
	// consulted: a scanner trying `/feeds/saved-search/<word>` costs one comparison rather than a query,
	// and a token of this shape can never be a legitimate one we failed to find.
	FeedTokenLength = 43

	// Where the feed lives. One string, used by the resolver that hands the URL to its owner and by the
	// router that serves it -- two call sites building a path is how a shared link stops working.
	feedPath = "/feeds/saved-search/"
)

// ErrNoFeed is returned when a member asks to disable a feed on a search that has none.
var ErrNoFeed = errors.New("that search has no feed")

// feedTokenHash is what gets stored: the SHA-256 of the token, as hex.
func feedTokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// FeedTokenShape reports whether a string could be one of this instance's tokens.
//
// Deliberately NOT a security check -- the hash lookup is the check. It is here so that the common case
// of somebody requesting a path that was never a token does not become a database query, and so that a
// token which cannot be ours is refused by the same 404 as one that merely is not (the handler gives
// every miss the same answer, because a different one would say which searches exist).
func FeedTokenShape(token string) bool {
	if len(token) != FeedTokenLength {
		return false
	}
	for _, r := range token {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// FeedURL is the address a member pastes into a feed reader.
func FeedURL(base, token string) string {
	return strings.TrimSuffix(base, "/") + feedPath + token
}

// newFeedToken mints one token. The ONLY caller stores its hash and returns the token to its owner: it
// is not recoverable afterwards, by construction.
func newFeedToken() (string, error) {
	raw := make([]byte, feedTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// EnableFeed mints a token for one of this member's searches and stores its hash, returning the token
// ONCE. Enabling a feed that already exists ROTATES it: the old URL stops working, which is the point.
func (s *SavedSearch) EnableFeed(ctx context.Context, userID, id uuid.UUID) (string, error) {
	token, err := newFeedToken()
	if err != nil {
		return "", err
	}

	hash := feedTokenHash(token)
	affected, err := s.queries.SetSavedSearchFeedToken(ctx, queries.SetSavedSearchFeedTokenParams{
		ID:            id,
		UserID:        userID,
		FeedTokenHash: &hash,
	})
	if err != nil {
		return "", err
	}
	// Scoped to the owner in the WHERE clause, so zero rows means "not yours or not there", and both are
	// ErrNotFound: the caller must not be able to tell them apart.
	if affected == 0 {
		return "", ErrNotFound
	}

	return token, nil
}

// DisableFeed clears the token, and the URL that named it stops resolving. Idempotent on a search that
// already has no feed: the member asked for there to be no feed, and there is no feed.
func (s *SavedSearch) DisableFeed(ctx context.Context, userID, id uuid.UUID) (bool, error) {
	_, err := s.queries.FindSavedSearchForUser(ctx, queries.FindSavedSearchForUserParams{
		ID:     id,
		UserID: userID,
	})
	if err != nil {
		if err := errutil.IgnoreNotFound(err); err != nil {
			return false, err
		}
		return false, ErrNotFound
	}

	if _, err := s.queries.ClearSavedSearchFeedToken(ctx, queries.ClearSavedSearchFeedTokenParams{
		ID:     id,
		UserID: userID,
	}); err != nil {
		return false, err
	}

	return true, nil
}

// FeedSearch is a saved search reached through its feed token, with the OWNER it has to be run as.
//
// The owner is not decoration. The stored text is evaluated by the same query path a signed-in request
// takes, and that path reads the current user: `favorites:` means the OWNER's favourites and the tier
// ceilings are keyed on the reader. Running a feed's search with nobody in the context would not be
// merely imprecise, it would fail -- and running it as the wrong person would answer with somebody
// else's view.
type FeedSearch struct {
	Search models.SavedSearch
	Owner  *auth.AuthUser
}

// FindByFeedToken resolves a token to the search it was minted for. nil, nil means "no such feed" --
// which the handler answers with the same 404 as a token that was never minted and a search whose feed
// was disabled, because telling those apart tells a stranger which searches exist.
func (s *SavedSearch) FindByFeedToken(ctx context.Context, token string) (*FeedSearch, error) {
	if !FeedTokenShape(token) {
		return nil, nil
	}

	hash := feedTokenHash(token)
	row, err := s.queries.FindSavedSearchByFeedToken(ctx, &hash)
	if err != nil {
		if err := errutil.IgnoreNotFound(err); err != nil {
			return nil, err
		}
		return nil, nil
	}

	// The row is flat (one query, one struct), so the model is built through the converter every other
	// read of this table uses -- a second hand-written copy of the field mapping is a second place for
	// `last_notified_at` to be dropped.
	search := converter.SavedSearchToModel(queries.SavedSearch{
		ID:             row.ID,
		UserID:         row.UserID,
		Name:           row.Name,
		Query:          row.Query,
		Notify:         row.Notify,
		LastNotifiedAt: row.LastNotifiedAt,
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
		FeedTokenHash:  row.FeedTokenHash,
	})
	return &FeedSearch{
		Search: search,
		Owner:  &auth.AuthUser{ID: row.UserID, Name: row.OwnerName},
	}, nil
}
