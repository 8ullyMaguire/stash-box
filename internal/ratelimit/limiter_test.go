package ratelimit

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func at(base time.Time, seconds int) time.Time {
	return base.Add(time.Duration(seconds) * time.Second)
}

// THE BURST IS THE NUMBER A CALLER FEELS FIRST, and the sustained rate is the one that decides how the limit
// behaves over a minute. Both are asserted, because a limiter tested only on its burst passes with any refill
// rate at all -- including one that never refills.
func TestTheBurstIsSpentAndThenRefillsAtTheStatedRate(t *testing.T) {
	limiter := New(Config{RequestsPerMinute: 60, Burst: 3})
	start := time.Now()

	for i := range 3 {
		assert.True(t, limiter.Allow("k", start).Allowed, "request %d is inside the burst", i+1)
	}

	refused := limiter.Allow("k", start)
	require.False(t, refused.Allowed, "the fourth request has nothing left to spend")
	assert.Equal(t, 60, refused.Limit, "and the refusal states the limit the caller is over")
	assert.Greater(t, refused.RetryAfter, time.Duration(0))

	// 60/minute is one per second, so one second later there is exactly one token.
	assert.True(t, limiter.Allow("k", at(start, 1)).Allowed, "one second buys one token at 60/minute")
	assert.False(t, limiter.Allow("k", at(start, 1)).Allowed, "and only one")

	// A whole minute is the whole allowance, capped at the burst rather than at 60: the bucket can hold three.
	for range 3 {
		assert.True(t, limiter.Allow("k", at(start, 60)).Allowed, "a minute is the full allowance again")
	}
	assert.False(t, limiter.Allow("k", at(start, 60)).Allowed, "capped at the burst, not at the minute's rate")
}

func TestRetryAfterIsNeverZeroAndNeverShorterThanTheWait(t *testing.T) {
	limiter := New(Config{RequestsPerMinute: 600, Burst: 1})
	start := time.Now()
	require.True(t, limiter.Allow("k", start).Allowed)

	refused := limiter.Allow("k", start)
	require.False(t, refused.Allowed)
	// A header that says 0 invites an immediate retry that is refused again, which reads as a broken limit.
	assert.GreaterOrEqual(t, refused.RetryAfter, time.Second)
	assert.LessOrEqual(t, refused.RetryAfter, 1200*time.Millisecond,
		"and it is the wait for the next token, not a fixed penalty")
}

func TestCredentialsDoNotShareABucket(t *testing.T) {
	limiter := New(Config{RequestsPerMinute: 60, Burst: 1})
	start := time.Now()

	require.True(t, limiter.Allow("one", start).Allowed)
	require.False(t, limiter.Allow("one", start).Allowed, "the first credential is out")
	require.True(t, limiter.Allow("two", start).Allowed,
		"and the second is untouched: the limit is per credential, which is what the plan asks for -- per "+
			"address would punish a shared one and per account would punish a member's second client")
}

// A CLOCK THAT JUMPS BACKWARDS MUST NOT MINT TOKENS. This is not hypothetical: it is what a machine does when
// its time is corrected, and a limiter that refills on negative elapsed time is a limiter that can be spent
// twice.
func TestAClockGoingBackwardsAddsNoTokens(t *testing.T) {
	limiter := New(Config{RequestsPerMinute: 60, Burst: 2})
	start := time.Now()

	require.True(t, limiter.Allow("k", start).Allowed)
	require.True(t, limiter.Allow("k", at(start, 0)).Allowed)
	require.False(t, limiter.Allow("k", at(start, -3600)).Allowed, "an hour ago is not an hour of credit")
	require.False(t, limiter.Allow("k", at(start, 0)).Allowed, "and the bucket is still empty")
}

// DISABLED IS A CONFIGURATION, NOT A MISSING MIDDLEWARE: zero requests per minute or a zero burst means no
// limit at all, and the limiter is nil so that the wiring has one shape either way.
func TestZeroMeansNoLimit(t *testing.T) {
	assert.Nil(t, New(Config{}))
	assert.Nil(t, New(Config{RequestsPerMinute: 0, Burst: 100}))
	assert.Nil(t, New(Config{RequestsPerMinute: 100, Burst: 0}))

	var limiter *Limiter
	assert.True(t, limiter.Allow("k", time.Now()).Allowed, "a nil limiter allows everything")
	assert.Equal(t, 0, limiter.Buckets())
	assert.Contains(t, PostureLine(limiter), "NONE")
}

// The map is a caller-controlled resource: a client that presents a new credential for every request must not
// be able to make this process grow without bound. Two passes, asserted one at a time, because they are two
// different rules and a test that only counted buckets would pass with either one missing.
func TestTheMapIsBounded(t *testing.T) {
	limiter := New(Config{RequestsPerMinute: 60, Burst: 5})
	limiter.maxBuckets = 10
	start := time.Now()

	// A flood of one-shot credentials, which is what a scanner looks like.
	for i := range 500 {
		limiter.Allow("scanner-"+strconv.Itoa(i), at(start, i))
	}

	assert.LessOrEqual(t, limiter.Buckets(), limiter.maxBuckets,
		"the map is bounded by the cap, not by how many credentials a caller can invent")
	assert.Positive(t, limiter.sweeps+limiter.evictions, "and something had to give for it")
}

// THE FIRST PASS GOES FIRST, and it is the one that costs the limit nothing: a credential that has not been
// seen since it refilled carries no information, so it is dropped before a caller who is still spending.
func TestCredentialsThatRefilledAreForgottenBeforeOnesStillSpending(t *testing.T) {
	limiter := New(Config{RequestsPerMinute: 60, Burst: 5})
	limiter.maxBuckets = 4
	start := time.Now()

	// Four credentials, each spending its whole burst at its own instant, so "least recently used" has one
	// answer rather than a tie.
	for i, key := range []string{"a", "b", "c", "d"} {
		for range 5 {
			require.True(t, limiter.Allow(key, at(start, i)).Allowed)
		}
	}
	require.Equal(t, 4, limiter.Buckets())
	for _, key := range []string{"a", "b", "c", "d"} {
		require.True(t, limiter.Tracked(key))
	}

	// Nothing has refilled yet (four seconds of one-per-second is short of the burst), so the cap is reached
	// with nowhere to sweep: the least recently used goes instead, and that is `a`.
	limiter.Allow("e", at(start, 4))
	assert.LessOrEqual(t, limiter.Buckets(), limiter.maxBuckets)
	assert.Equal(t, 1, limiter.evictions, "one forgotten, one added")
	assert.False(t, limiter.Tracked("a"), "the least recently used is the one that went")
	assert.True(t, limiter.Tracked("d"), "and the most recent is still counted")
	assert.Zero(t, limiter.sweeps, "this was the second pass: nothing had refilled to sweep")

	// Five seconds after ITS last request, a bucket is full again -- and that is what the first pass exists
	// for. The map empties itself without forgetting anybody who is mid-spend, and the eviction count does not
	// move, which is what tells the two passes apart.
	before := limiter.evictions
	limiter.Allow("f", at(start, 20))
	assert.Greater(t, limiter.sweeps, 0)
	assert.Equal(t, before, limiter.evictions, "the first pass did the work; nothing was evicted")
	assert.True(t, limiter.Tracked("f"))
}

func TestPostureLineStatesTheLimitTheProcessIsRunning(t *testing.T) {
	line := PostureLine(New(Config{RequestsPerMinute: 600, Burst: 120}))
	assert.Contains(t, line, "600")
	assert.Contains(t, line, "120")
}

// --- the HTTP half ------------------------------------------------------------------------------------

func handlerThatServes(status int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
	})
}

// THE REFUSAL HAS TO BE THE SAME KIND OF ERROR AS EVERY OTHER ERROR THIS API RETURNS. It is the defect the
// plan names: a 429 that returns HTML or a bare string is a client bug reported as a server mystery, because
// a GraphQL client is already parsing {"errors":[{"message": ...}]} and has no second path for this.
func TestARefusalIsTheSameErrorShapeAsEveryOtherError(t *testing.T) {
	limiter := New(Config{RequestsPerMinute: 60, Burst: 1})
	server := httptest.NewServer(Middleware(limiter, Credential)(handlerThatServes(http.StatusOK)))
	defer server.Close()

	first, err := http.Get(server.URL + "/graphql")
	require.NoError(t, err)
	defer first.Body.Close()
	assert.Equal(t, http.StatusOK, first.StatusCode)
	assert.Equal(t, "60", first.Header.Get("X-RateLimit-Limit"),
		"the limit is stated on an ALLOWED response too: a client that only learns it when refused is a "+
			"client that will be refused again")
	assert.Equal(t, "0", first.Header.Get("X-RateLimit-Remaining"))

	second, err := http.Get(server.URL + "/graphql")
	require.NoError(t, err)
	defer second.Body.Close()

	require.Equal(t, http.StatusTooManyRequests, second.StatusCode)
	assert.Contains(t, second.Header.Get("Content-Type"), "application/json")
	require.NotEmpty(t, second.Header.Get("Retry-After"), "the way out of a limit is a time to wait")
	wait, err := strconv.Atoi(second.Header.Get("Retry-After"))
	require.NoError(t, err)
	assert.Greater(t, wait, 0)

	var body struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	require.NoError(t, json.NewDecoder(second.Body).Decode(&body), "the body is JSON, not HTML and not a string")
	require.Len(t, body.Errors, 1)
	assert.Contains(t, body.Errors[0].Message, "60", "the message names the limit")
	assert.Contains(t, body.Errors[0].Message, "Retry in", "and says what to do about it")
}

// THE CONTROL, without which the test above proves only that the middleware can refuse: the same requests,
// the same client, with no limit configured, are served. A passing refusal test on a middleware that refuses
// everything is not evidence of a working limiter.
func TestWithNoLimitConfiguredNothingIsRefused(t *testing.T) {
	server := httptest.NewServer(Middleware(New(Config{}), Credential)(handlerThatServes(http.StatusOK)))
	defer server.Close()

	for i := range 25 {
		response, err := http.Get(server.URL + "/graphql")
		require.NoError(t, err)
		response.Body.Close()
		require.Equal(t, http.StatusOK, response.StatusCode, "request %d", i+1)
		assert.Empty(t, response.Header.Get("X-RateLimit-Limit"), "and nothing claims a limit exists")
	}
}

// THE CREDENTIAL FUNCTION IS WHAT THE MIDDLEWARE COUNTS BY, so the sharing rule is asserted through the wire
// and not only on the function: two requests carrying two keys are two callers, and neither spends the
// other's budget.
func TestTwoCredentialsAreTwoBucketsThroughTheWire(t *testing.T) {
	server := httptest.NewServer(Middleware(New(Config{RequestsPerMinute: 60, Burst: 1}),
		Credential)(handlerThatServes(http.StatusOK)))
	defer server.Close()

	call := func(key string) int {
		request, err := http.NewRequest(http.MethodGet, server.URL+"/graphql", nil)
		require.NoError(t, err)
		request.Header.Set("ApiKey", key)

		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		defer response.Body.Close()
		return response.StatusCode
	}

	for _, key := range []string{"first", "second"} {
		assert.Equal(t, http.StatusOK, call(key), "%s has its own burst", key)
		assert.Equal(t, http.StatusTooManyRequests, call(key), "%s has now spent it", key)
	}
}

// The credential function decides who shares a bucket, so it is asserted rather than assumed: two requests
// carrying two keys are two callers, and an anonymous caller is counted by address -- the least bad answer for
// somebody who presents nothing, and NOT the unit the plan forbids for members.
func TestTheCredentialIsWhatTheCallerPresents(t *testing.T) {
	assert.Equal(t, "ApiKey:abc", Credential(requestWith(http.MethodGet, "/graphql", map[string]string{"ApiKey": "abc"}, "10.0.0.1:1234")))
	assert.Equal(t, "Authorization:xyz", Credential(requestWith(http.MethodGet, "/graphql", map[string]string{"Authorization": "Bearer xyz"}, "10.0.0.1:1234")))
	assert.Equal(t, "address:10.0.0.1", Credential(requestWith(http.MethodGet, "/graphql", nil, "10.0.0.1:1234")))

	feed := FeedToken("/feeds/saved-search/")
	assert.Equal(t, "feed:a-token", feed(requestWith(http.MethodGet, "/feeds/saved-search/a-token", nil, "10.0.0.1:1234")))
	assert.Equal(t, "address:10.0.0.1", feed(requestWith(http.MethodGet, "/feeds/saved-search/", nil, "10.0.0.1:1234")),
		"a request with no token is counted by address, so a scan of random paths shares one bucket")
	assert.Equal(t, "address:10.0.0.1", feed(requestWith(http.MethodGet, "/feeds/saved-search/a/b", nil, "10.0.0.1:1234")),
		"and so is a path that is not a token at all")
}

func requestWith(method, path string, headers map[string]string, remoteAddr string) *http.Request {
	request := httptest.NewRequest(method, path, nil)
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	request.RemoteAddr = remoteAddr
	return request
}

// The Middleware is used from the server's request path, where many goroutines share one limiter. `-race` is
// where this earns its keep: the mutex is the only thing between two requests and a corrupted map.
func TestTheLimiterIsSafeUnderConcurrency(t *testing.T) {
	limiter := New(Config{RequestsPerMinute: 6000, Burst: 100})
	var wg sync.WaitGroup

	for i := range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				limiter.AllowNow("key-" + strconv.Itoa(i%4))
			}
		}()
	}
	wg.Wait()

	assert.Positive(t, limiter.Buckets())
	assert.LessOrEqual(t, limiter.Buckets(), 4)
}

// The middleware runs before the GraphQL handler, so its own refusals must not be reachable by anything the
// handler does: a request that is allowed passes through untouched.
func TestAnAllowedRequestReachesTheHandlerUnchanged(t *testing.T) {
	var seen string
	server := httptest.NewServer(Middleware(New(Config{RequestsPerMinute: 60, Burst: 5}),
		Credential)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.Path + "?" + r.URL.RawQuery
		w.WriteHeader(http.StatusTeapot)
	})))
	defer server.Close()

	response, err := http.Get(server.URL + "/graphql?query=%7Bme%7D")
	require.NoError(t, err)
	defer response.Body.Close()

	assert.Equal(t, http.StatusTeapot, response.StatusCode, "the handler's own answer is what the caller gets")
	assert.Equal(t, "/graphql?query=%7Bme%7D", seen)
	assert.True(t, strings.Contains(response.Header.Get("X-RateLimit-Remaining"), "4") ||
		response.Header.Get("X-RateLimit-Remaining") == "4")
}
