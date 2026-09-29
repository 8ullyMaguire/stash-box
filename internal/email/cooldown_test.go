package email

import (
	"errors"
	"testing"
	"time"

	"github.com/stashapp/stash-box/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Issue #1277: "Unclear error message for email cooldown".
//
//   Apparently Stash-Box has a five minute cooldown period before it will send
//   another email to the same address. However it's not clear to use that's
//   what's happening because the error message says "pending-email-change". This
//   sounds more like the user requested to change their email address and the
//   error won't go away until that process is resolved one way or another.
//
//   This became an issue recently when a user complained of being blocked from
//   resetting their password on a brand-new account. I believe what happened was
//   they created the account with a mis-typed password, couldn't log in
//   afterward, and tried to trigger a password reset within 5 minutes of
//   receiving the registration email.
//
// The string was a bare errors.New in validateEmailCooldown, and every email
// flow in the package funnels through it: new-user confirmation, password reset,
// confirm-old-email and confirm-new-email. So a user blocked by the cooldown on
// any of them was told an email change was pending.
//
// Two separate defects, both fixed here:
//
//  1. the wording, which named a process the user may never have started;
//  2. the absence of any information about how long to wait.

// withCooldown sets the email cooldown for one test and restores it after.
func withCooldown(t *testing.T, d time.Duration) {
	t.Helper()

	prev := config.GetEmailCooldown()
	t.Cleanup(func() { config.SetEmailCooldownForTest(prev) })
	config.SetEmailCooldownForTest(d)
}

// The reported message must not come back.
func TestCooldownDoesNotClaimAPendingEmailChange(t *testing.T) {
	withCooldown(t, 5*time.Minute)

	mgr := NewManager()
	const addr = "user@example.com"

	// Prime the cooldown the way a real send does.
	mgr.lastEmailed[addr] = time.Now()

	err := mgr.validateEmailCooldown(addr)
	require.Error(t, err)

	assert.NotContains(t, err.Error(), "pending-email-change",
		"the cooldown must not claim an email change is pending (#1277)")
	assert.NotContains(t, err.Error(), "email change",
		"the cooldown must not mention an email change at all (#1277)")
}

// The replacement says what actually happened and how long to wait.
func TestCooldownMessageNamesTheWait(t *testing.T) {
	withCooldown(t, 5*time.Minute)

	mgr := NewManager()
	const addr = "user@example.com"

	// Sent 1 minute ago, so ~4 minutes of cooldown remain.
	mgr.lastEmailed[addr] = time.Now().Add(-1 * time.Minute)

	err := mgr.validateEmailCooldown(addr)
	require.Error(t, err)

	assert.Contains(t, err.Error(), "cooldown",
		"the message must say a cooldown is active (#1277)")
	assert.Contains(t, err.Error(), "minute",
		"the message must say how long to wait (#1277)")
}

// A sub-minute remainder must not render as "0 minutes", which reads as a bug and
// tells the user nothing.
func TestCooldownMessageHandlesSubMinuteRemainder(t *testing.T) {
	withCooldown(t, 5*time.Minute)

	mgr := NewManager()
	const addr = "user@example.com"
	mgr.lastEmailed[addr] = time.Now().Add(-5*time.Minute + 10*time.Second)

	err := mgr.validateEmailCooldown(addr)
	require.Error(t, err)

	assert.NotContains(t, err.Error(), "0 minute",
		"a sub-minute remainder must not be reported as 0 minutes (#1277)")
}

// The cooldown is per address, not global: a second user must not be blocked by
// the first one's email.
func TestCooldownIsPerAddress(t *testing.T) {
	withCooldown(t, 5*time.Minute)

	mgr := NewManager()
	mgr.lastEmailed["first@example.com"] = time.Now()

	assert.Error(t, mgr.validateEmailCooldown("first@example.com"),
		"the address that was emailed must be blocked")
	assert.NoError(t, mgr.validateEmailCooldown("second@example.com"),
		"an unrelated address must not be blocked (#1277)")
}

// After the cooldown expires the address must be usable again. This is the
// behaviour the old "pending-email-change" wording obscured: the user was told to
// wait for a process to resolve rather than being told to wait a few minutes.
func TestCooldownExpires(t *testing.T) {
	withCooldown(t, 5*time.Minute)

	mgr := NewManager()
	const addr = "user@example.com"
	mgr.lastEmailed[addr] = time.Now().Add(-6 * time.Minute)

	assert.NoError(t, mgr.validateEmailCooldown(addr),
		"the cooldown must expire (#1277)")
}

// The error must be matchable with errors.Is, so a caller can distinguish a
// cooldown from a real send failure without string matching.
func TestCooldownIsMatchableWithErrorsIs(t *testing.T) {
	withCooldown(t, 5*time.Minute)

	mgr := NewManager()
	const addr = "user@example.com"
	mgr.lastEmailed[addr] = time.Now()

	err := mgr.validateEmailCooldown(addr)
	require.Error(t, err)

	assert.True(t, errors.Is(err, ErrEmailCooldown),
		"callers must be able to match the cooldown with errors.Is (#1277)")

	var cooldown *CooldownError
	assert.True(t, errors.As(err, &cooldown),
		"the error must carry the remaining wait as a typed value (#1277)")
	if cooldown != nil {
		assert.Positive(t, cooldown.RetryAfter,
			"RetryAfter must be positive while the cooldown is active")
	}
}

// A different failure must NOT match the cooldown, or every send error would be
// reported to users as "wait a few minutes".
func TestSendFailureIsNotACooldown(t *testing.T) {
	withCooldown(t, 5*time.Minute)

	// No email settings configured: a real failure, not a rate limit.
	err := NewManager().Send("someone@example.com", "s", "t", "h")
	require.Error(t, err)
	assert.False(t, errors.Is(err, ErrEmailCooldown),
		"a configuration failure must not be reported as a cooldown")
}
