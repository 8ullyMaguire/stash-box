package invite

import (
	"errors"
	"time"

	"github.com/stashapp/stash-box/internal/queries"
)

// THE KEY'S OWN FACTS, IN ONE PLACE. Two callers ask this question and they must not answer it
// differently: the signup path, which REFUSES a signup on a key nobody could use, and the landing
// page, which must not record a scan of a key nobody could use (the plan's "a scan of an expired key
// is not counted"). A rule implemented twice cannot be killed by removing one implementation -- the
// lesson 11b paid for -- so the rule lives here and both callers spend it.
//
// These are the reasons a key that EXISTS cannot be used. Whether a key is REQUIRED at all is a
// different question with a different owner (`require_invite`), and neither of these is about it:
// expiry and exhaustion are equally true on an instance that requires invites and on one that does
// not.
var (
	ErrExpired = errors.New("invite key expired")
	ErrUsed    = errors.New("invite key used")
)

// Unusable returns nil when the key may be used, and the reason it may not otherwise.
//
// pendingUses is how many signups have already been PROMISED this key: a use is consumed at
// ACTIVATION, not at signup, so a key with one use and one pending activation has none left and the
// pending tokens are what "already promised" looks like.
func Unusable(key *queries.InviteKey, pendingUses int, now time.Time) error {
	if key == nil {
		// No key was presented at all. That is not this function's question -- nothing about a key
		// that does not exist can make it unusable -- and the caller that needs a key says so itself.
		return nil
	}

	if key.ExpireTime != nil && key.ExpireTime.Before(now) {
		return ErrExpired
	}

	if key.Uses != nil && pendingUses >= int(*key.Uses) {
		return ErrUsed
	}

	return nil
}
