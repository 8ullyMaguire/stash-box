package content

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stashapp/stash-box/internal/service/hostrules"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The boundary table for migration 108's machine. Every row is a case the migration's comment names,
// and the ones that matter most are the boundaries: one failure short of intermittent, one hour short
// of dead, and the terminal statuses answering 200.
func TestNextStatus(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	firstSeen := now.Add(-30 * 24 * time.Hour)
	fresh := now.Add(-1 * time.Hour)
	old := now.Add(-8 * 24 * time.Hour)

	tests := []struct {
		name     string
		current  string
		failures int
		lastOK   *time.Time
		first    time.Time
		ok       bool
		want     string
	}{
		{"a first success makes it live", StatusUnknown, 0, nil, firstSeen, true, StatusLive},
		{"a success from intermittent is live again", StatusIntermittent, 5, &old, firstSeen, true, StatusLive},
		{"a success from dead IS the resurrection", StatusDead, 40, &old, firstSeen, true, StatusLive},

		// A source first seen an hour ago: one failure is not enough for intermittent, and its age is
		// an hour, so it stays unknown. (The never-ok fallback is `first_seen` -- a row that has never
		// answered and is older than the dead threshold IS dead, because "unreachable" is the only
		// thing anyone has ever observed about it.)
		{"one failure leaves a young unknown source unknown", StatusUnknown, 0, nil, fresh, false, StatusUnknown},
		{"two failures are still short of intermittent", StatusLive, 1, &fresh, firstSeen, false, StatusLive},
		{"three consecutive failures make it intermittent", StatusLive, 2, &fresh, firstSeen, false, StatusIntermittent},
		{"an intermittent source stays intermittent", StatusIntermittent, 9, &fresh, firstSeen, false, StatusIntermittent},

		{"six days and twenty-three hours is not dead", StatusIntermittent, 20, ptr(now.Add(-(7*24*time.Hour - time.Hour))), firstSeen, false, StatusIntermittent},
		{"exactly seven days is dead", StatusIntermittent, 20, ptr(now.Add(-7 * 24 * time.Hour)), firstSeen, false, StatusDead},
		{"never ok, first seen eight days ago, is dead", StatusUnknown, 4, nil, old, false, StatusDead},
		{"never ok, first seen an hour ago, is not", StatusUnknown, 4, nil, fresh, false, StatusIntermittent},

		{"dead stays dead on a failure", StatusDead, 40, &old, firstSeen, false, StatusDead},
		{"dead stays dead even when its last success is recent", StatusDead, 3, &fresh, firstSeen, false, StatusDead},

		{"a dmca source is not resurrected by answering", StatusDMCA, 0, &old, firstSeen, true, StatusDMCA},
		{"and not by failing either", StatusDMCA, 0, &old, firstSeen, false, StatusDMCA},
		{"an archived source is terminal too", StatusArchived, 0, &old, firstSeen, true, StatusArchived},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, NextStatus(tt.current, tt.failures, tt.lastOK, tt.first, now, tt.ok))
		})
	}
}

func ptr(t time.Time) *time.Time { return &t }

// The checker's three outcomes: alive, refused (which is NOT death), and answered badly.
func TestHTTPCheckerSeparatesARefusalFromADeath(t *testing.T) {
	code := http.StatusOK
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The checker now asks robots.txt first (internal/service/hostrules), and this server states
		// no terms: the 404 is the answer that allows the check to proceed. It is answered SEPARATELY
		// so the assertion below stays what it was -- a health check of a PAGE is a HEAD, and a sweep
		// that started downloading pages would still fail this test.
		if r.URL.Path == "/robots.txt" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		assert.Equal(t, http.MethodHead, r.Method, "a health check must not download the page")
		w.WriteHeader(code)
	}))
	defer server.Close()

	// No pacing for this test: it checks the same host eleven times, and the assertion is about the
	// status mapping rather than about the pace (which has its own tests in hostrules).
	checker := NewHTTPChecker()
	checker.Rules = hostrules.New(0)

	for _, alive := range []int{http.StatusOK, http.StatusMovedPermanently, http.StatusNoContent} {
		code = alive
		require.NoError(t, checker.Check(context.Background(), server.URL), "status %d is alive", alive)
	}

	for _, refused := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests} {
		code = refused
		err := checker.Check(context.Background(), server.URL)
		assert.ErrorIs(t, err, ErrCheckInconclusive,
			"status %d is a refusal, not evidence of death: a bot wall answers 403 to us and 200 to a reader", refused)
	}

	for _, gone := range []int{http.StatusNotFound, http.StatusGone, http.StatusInternalServerError} {
		code = gone
		err := checker.Check(context.Background(), server.URL)
		require.Error(t, err)
		assert.NotErrorIs(t, err, ErrCheckInconclusive, "status %d is a failure, not a refusal", gone)
	}
}

// A hung server must not hang the sweep.
func TestHTTPCheckerTimesOutRatherThanHanging(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer func() { close(release); server.Close() }()

	checker := HTTPChecker{Client: &http.Client{Timeout: 50 * time.Millisecond}, Rules: hostrules.New(0)}
	err := checker.Check(context.Background(), server.URL)
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrCheckInconclusive, "a timeout is a failure to answer, not a refusal")
	assert.True(t, errors.Is(err, context.DeadlineExceeded) || err != nil)
}
