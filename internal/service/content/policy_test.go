package content

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/config"
)

// withFetching sets the content-plane switch for one test and restores it.
//
// The restore is not tidiness: `config.C` is a package global, so a test that left fetching on would
// silently enable it for every test that ran after it in the same binary -- and the tests that assert
// the refusal are exactly the ones that would then pass for the wrong reason.
func withFetching(t *testing.T, enabled bool) {
	t.Helper()
	previous := config.C.ContentFetchSources
	config.C.ContentFetchSources = enabled
	t.Cleanup(func() { config.C.ContentFetchSources = previous })
}

// poisonedChecker fails the test if it is consulted at all.
//
// "The call returned the right error" and "no request was made" are two claims, and only the second is
// what the switch promises. A checker that records the failure is how the second one is asserted.
type poisonedChecker struct{ t *testing.T }

func (c poisonedChecker) Check(context.Context, string) error {
	c.t.Error("the checker was called: an instance with content_fetch_sources off must make no request")
	return nil
}

type poisonedArchiver struct{ t *testing.T }

func (a poisonedArchiver) Archive(context.Context, string) (string, error) {
	a.t.Error("the archiver was called: an instance with content_fetch_sources off must make no request")
	return "", nil
}

func TestFetchingIsOffUnlessTheOperatorTurnsItOn(t *testing.T) {
	withFetching(t, false)
	assert.False(t, FetchingEnabled())
	require.ErrorIs(t, RequireFetching(), ErrFetchingDisabled)

	withFetching(t, true)
	assert.True(t, FetchingEnabled())
	require.NoError(t, RequireFetching(), "an operator who turned it on must not be refused")
}

// The refusal is read by an operator and by a member whose URL was refused, so it has to name the key
// that changes it. A message that only said "refused" would leave an operator reading the source.
func TestTheRefusalNamesTheSwitch(t *testing.T) {
	withFetching(t, false)
	err := RequireFetching()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "content_fetch_sources",
		"the refusal must name the key an operator sets, not just the fact of the refusal")
}

// Every entry point that can issue or trigger an outbound request refuses when the switch is off.
//
// THE SERVICE IS A ZERO VALUE ON PURPOSE. `&Service{}` has nil queries, so the guard has to run BEFORE
// the first database call: a call that got past it would panic on the nil query handle here instead of
// quietly reading a database. The poisoned checker and archiver cover the other half of the claim, and
// between them the two failures (a request made, or work done that could lead to one) are both loud.
//
// The table is a slice and not a map so that a failure names the same order every run.
func TestEveryEntryPointThatFetchesRefusesWhenTheSwitchIsOff(t *testing.T) {
	withFetching(t, false)

	svc := &Service{}
	ctx := context.Background()
	now := time.Now()

	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"Sweep", func() error {
			_, err := svc.Sweep(ctx, now, 10, poisonedChecker{t})
			return err
		}},
		{"SweepResurrections", func() error {
			_, err := svc.SweepResurrections(ctx, now, 10, poisonedChecker{t})
			return err
		}},
		{"CheckSource", func() error {
			_, err := svc.CheckSource(ctx, uuid.Nil, now, poisonedChecker{t})
			return err
		}},
		{"ArchiveSources", func() error {
			_, err := svc.ArchiveSources(ctx, 10, poisonedArchiver{t})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.ErrorIs(t, tc.call(), ErrFetchingDisabled)
		})
	}
}

// The switch is one rule with one message, and this is the assertion that keeps it that way: the same
// value, not merely a value whose text matches.
func TestTheRefusalIsTheSameErrorEverywhere(t *testing.T) {
	withFetching(t, false)
	svc := &Service{}
	err := func() error {
		_, err := svc.Sweep(context.Background(), time.Now(), 1, poisonedChecker{t})
		return err
	}()
	require.Error(t, err)
	assert.Same(t, ErrFetchingDisabled, err,
		"the refusal must be one exported error, so a caller can match it without reading its text")
	assert.True(t, strings.Contains(err.Error(), "content_fetch_sources"))
}
