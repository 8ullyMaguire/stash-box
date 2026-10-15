// Package ratelimit counts requests per credential and refuses the ones over the limit (plan slice 13d).
//
// THE UNIT IS WHAT THE CALLER PRESENTS, not where it connects from. The plan states the reason and it is worth
// repeating where the code is: a limit keyed on the address punishes everyone behind a shared one -- a house, a
// university, a VPN -- and a limit keyed on the ACCOUNT punishes a member for using two clients, which is
// ordinary use of an API. What is counted is therefore the credential: the API key for the GraphQL surface,
// the feed's own token for a feed URL, and -- only when a caller presents NOTHING, so there is no identity to
// share -- the connecting address, which is the least bad answer available to an anonymous caller.
//
// IT FORGETS ON PURPOSE. A bucket is memory, not a row: the alternative is a write per read on a read surface,
// and a limit whose history has to survive a restart is a limit an operator cannot lower in a hurry. A restart
// therefore resets every bucket, which is honest -- the state it describes is "how fast is this caller going
// RIGHT NOW", and that question has no yesterday.
package ratelimit

import (
	"sync"
	"time"
)

// Config is the instance's limit. ZERO MEANS OFF, and there is deliberately no separate boolean: a switch
// beside a number is two places to state one thing, and the number already says whether there is a limit.
type Config struct {
	// RequestsPerMinute is the sustained rate for one credential.
	RequestsPerMinute int
	// Burst is how many requests a credential may make at once, before the sustained rate applies. A UI
	// opening a page issues several requests in a moment, so a limit with no burst is a limit that breaks
	// ordinary use while barely slowing a loop.
	Burst int
}

// Disabled reports whether this configuration asks for no limiting at all.
func (c Config) Disabled() bool {
	return c.RequestsPerMinute <= 0 || c.Burst <= 0
}

// Decision is one verdict, and it carries everything the response needs rather than only the answer: a caller
// that has to recompute the boundary is a second implementation of the same rule.
type Decision struct {
	Allowed    bool
	Limit      int
	Remaining  int
	RetryAfter time.Duration
}

// Limiter is a token bucket per credential.
//
// The map is guarded by one mutex rather than sharded: this is a few instructions in the request path of an
// instance that answers GraphQL queries against a Postgres catalogue, so a shard table would be a second
// implementation of a data structure in exchange for a contention problem this instance does not have.
type Limiter struct {
	config Config
	rate   float64 // tokens per second
	now    func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket

	// maxBuckets bounds the memory a caller who presents a new credential for every request can make this
	// process hold. Reaching it sweeps the buckets that are full again (a bucket nobody has touched since it
	// refilled carries no information), which is the only kind that is safe to drop: a bucket that is still
	// spending is a caller who is still going.
	maxBuckets int
	sweeps     int
	evictions  int
}

type bucket struct {
	tokens float64
	last   time.Time
}

// DefaultMaxBuckets is the point at which the limiter sweeps. It is far above the number of credentials a real
// instance has and far below the point where the map is a memory problem.
const DefaultMaxBuckets = 10000

// New builds a limiter. A disabled configuration returns nil, and every method tolerates a nil receiver, so
// the wiring has one shape whether or not a limit is set.
func New(config Config) *Limiter {
	if config.Disabled() {
		return nil
	}
	return &Limiter{
		config:     config,
		rate:       float64(config.RequestsPerMinute) / 60.0,
		now:        time.Now,
		buckets:    make(map[string]*bucket),
		maxBuckets: DefaultMaxBuckets,
	}
}

// Config returns the configuration this limiter was built with, so a caller (the startup line, the response
// headers) states the limit the process is actually running rather than the one it was configured with.
func (l *Limiter) Config() Config {
	if l == nil {
		return Config{}
	}
	return l.config
}

// AllowNow is Allow with the wall clock. The middleware uses this; tests use Allow.
func (l *Limiter) AllowNow(key string) Decision {
	if l == nil {
		return Decision{Allowed: true}
	}
	return l.Allow(key, l.now())
}

// Allow takes one token for key, or reports how long until one is available.
//
// The clock is a parameter so that the refill is TESTABLE: a limiter tested against the wall clock tests
// whatever the machine was doing, and a rate-limit test that has to sleep is a test nobody runs.
func (l *Limiter) Allow(key string, now time.Time) Decision {
	if l == nil {
		return Decision{Allowed: true}
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	// ROOM IS MADE ONLY FOR A CREDENTIAL THAT IS NOT HERE YET, and that ordering is the difference between a
	// bounded table and a destructive one: at the cap, making room on EVERY request means every request
	// evicts somebody -- so on a busy instance the table would spend all its time throwing away the
	// credentials it is supposed to be counting. A caller who is already tracked never costs another caller
	// its bucket.
	b, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) >= l.maxBuckets {
			l.makeRoomLocked(now)
		}
		b = &bucket{tokens: float64(l.config.Burst), last: now}
		l.buckets[key] = b
	}

	// Refill by elapsed time, capped at the burst. `last` moves only by the amount of time that has been
	// accounted for, never to `now` unconditionally, so a clock that jumps backwards cannot mint tokens.
	if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.tokens = min(float64(l.config.Burst), b.tokens+elapsed*l.rate)
		b.last = now
	}

	limit := l.config.RequestsPerMinute
	if b.tokens >= 1 {
		b.tokens--
		return Decision{Allowed: true, Limit: limit, Remaining: int(b.tokens)}
	}

	// The wait for the NEXT token, which is what Retry-After means. Rounded up to a whole second: a header
	// that says "0" invites an immediate retry that is refused again, which reads as a broken limit.
	wait := time.Duration((1-b.tokens)/l.rate*float64(time.Second)) + time.Second
	return Decision{Allowed: false, Limit: limit, Remaining: 0, RetryAfter: wait.Truncate(time.Second)}
}

// makeRoomLocked bounds the map with a hard cap, in two passes, and the cap is the point: without one, how
// much memory this process spends is decided by whoever sends the most distinct credentials.
//
// FIRST PASS, drop the buckets that are FULL AGAIN -- a credential that has not been seen since it refilled
// carries no information, so forgetting it costs the limit nothing. If that frees nothing (every credential is
// mid-spend), the SECOND PASS forgets the least recently used ones. That is a trade rather than a free lunch,
// and it is worth stating: a caller who invents enough credentials can push a real one out of the table, and
// that caller gets its burst back. It costs an attacker thousands of requests per victim and buys it a burst
// -- the alternative is memory that any client can make this process allocate without limit.
func (l *Limiter) makeRoomLocked(now time.Time) {
	full := float64(l.config.Burst)
	for key, b := range l.buckets {
		if b.tokens+now.Sub(b.last).Seconds()*l.rate >= full {
			delete(l.buckets, key)
			l.sweeps++
		}
	}
	if len(l.buckets) < l.maxBuckets {
		return
	}

	// Initialised from the first bucket rather than from `now`: seeding the search with the current instant
	// makes every bucket whose `last` is that same instant ineligible, which is exactly the case a flood of
	// credentials arriving in one moment produces -- the map would stay over the cap and the pass would look
	// like it ran. Ties then go to whichever the iteration reaches first, which is arbitrary and is the right
	// answer: credentials that are equally recent are equally forgettable.
	var oldest time.Time
	oldestKey := ""
	for key, b := range l.buckets {
		if oldestKey == "" || b.last.Before(oldest) {
			oldest, oldestKey = b.last, key
		}
	}
	if oldestKey != "" {
		delete(l.buckets, oldestKey)
		l.evictions++
	}
}

// Tracked reports whether a credential still has a bucket here. For tests and for diagnosing a limit that
// seems not to apply -- the interesting failure of a bounded table is that the caller's bucket was forgotten,
// and a test that counted buckets instead would pass while that happened.
func (l *Limiter) Tracked(key string) bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, ok := l.buckets[key]
	return ok
}

// Buckets reports how many credentials the limiter is currently holding. For tests and for an operator
// watching memory; nothing in the request path depends on it.
func (l *Limiter) Buckets() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}
