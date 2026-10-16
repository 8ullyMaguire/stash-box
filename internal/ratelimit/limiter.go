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

// The tiers a credential can count against. TierFree is the empty string rather than a word, so that a
// limiter built by `New` -- which knows no tiers at all -- and a limiter built by `NewWithTiers` count the
// same caller in the same bucket, and so that the zero value of a tier variable is the free tier.
const (
	TierFree    = ""
	TierPremium = "premium"
)

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
	// premium is the second limit an entitled credential is counted against (§18.1's "higher API limits",
	// plan phase 15). Zero value means this instance has one limit, which is every configuration shipped
	// before the tier existed and every configuration today unless an operator turned the stream on.
	premium Config
	// premiumCredential decides which limit a credential counts against. It is supplied by the caller
	// because this package counts what it is handed and has no business resolving identities -- the API
	// key's owner is monetization's question, not the limiter's.
	premiumCredential func(key string) bool

	now func() time.Time

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
	// burst and rate are THE LIMIT THIS BUCKET COUNTS AGAINST, carried on the bucket rather than read from the
	// limiter at refill time. With one limit the distinction is invisible; with two it is a defect in both
	// directions: the sweep decides a bucket is "full again" by comparing it to the burst, so measuring a
	// premium bucket against the free burst would discard buckets that are still spending, and the refill cap
	// would cap a premium caller at the free burst.
	burst float64
	rate  float64
}

// DefaultMaxBuckets is the point at which the limiter sweeps. It is far above the number of credentials a real
// instance has and far below the point where the map is a memory problem.
const DefaultMaxBuckets = 10000

// New builds a limiter. A disabled configuration returns nil, and every method tolerates a nil receiver, so
// the wiring has one shape whether or not a limit is set.
func New(config Config) *Limiter {
	return NewWithTiers(config, Config{}, nil)
}

// NewWithTiers builds a limiter that counts a credential against ONE OF TWO limits, chosen by
// premiumCredential (plan phase 15: §18.1's "higher API limits").
//
// THE FREE LIMIT KEEPS ITS MEANING. There is no arithmetic here that could raise what an ungranted caller
// gets: a credential the predicate does not claim is counted against `free`, and the premium configuration is
// reachable only through the predicate. A premium configuration that is disabled (zero means off, as
// everywhere in this package) therefore grants unlimited requests to the credentials it claims -- which is a
// posture an operator can choose deliberately and is not the default.
//
// nil premiumCredential means one limit, which is what every caller before the tier existed wanted.
func NewWithTiers(free, premium Config, premiumCredential func(key string) bool) *Limiter {
	if free.Disabled() {
		return nil
	}
	return &Limiter{
		config:            free,
		premium:           premium,
		premiumCredential: premiumCredential,
		now:               time.Now,
		buckets:           make(map[string]*bucket),
		maxBuckets:        DefaultMaxBuckets,
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

// PremiumConfig is the second limit, or a disabled Config when this limiter has one limit. For the startup
// line and for tests; nothing in the counting path reads it, because the bucket carries its own limit.
func (l *Limiter) PremiumConfig() Config {
	if l == nil {
		return Config{}
	}
	return l.premium
}

// tierOf reports which limit a credential counts against, by asking the predicate the wiring supplied.
//
// ONE PLACE DECIDES. The middleware does not compute a tier and neither does the response: the limiter asks,
// counts, and states the limit it used in the Decision, so "which limit applied to this request" has one
// answer on the wire (X-RateLimit-Limit) and one in the log.
func (l *Limiter) tierOf(key string) string {
	if l == nil || l.premiumCredential == nil {
		return TierFree
	}
	if l.premiumCredential(key) {
		return TierPremium
	}
	return TierFree
}

// configFor is the configuration a tier counts against.
func (l *Limiter) configFor(tier string) Config {
	if tier == TierPremium {
		return l.premium
	}
	return l.config
}

// Allow takes one token for key, or reports how long until one is available.
//
// The clock is a parameter so that the refill is TESTABLE: a limiter tested against the wall clock tests
// whatever the machine was doing, and a rate-limit test that has to sleep is a test nobody runs.
func (l *Limiter) Allow(key string, now time.Time) Decision {
	if l == nil {
		return Decision{Allowed: true}
	}

	tier := l.tierOf(key)
	config := l.configFor(tier)
	if config.Disabled() {
		// The limit this credential counts against is switched off, and zero means off as it does
		// everywhere in this package: no limit, no bucket, no memory spent counting it.
		return Decision{Allowed: true}
	}

	// THE BUCKET IS KEYED BY TIER AND CREDENTIAL, and the separation is the point: tokens are counted
	// against a LIMIT, so when a credential's limit changes the count in hand was an answer about a
	// different question. A member who becomes premium therefore gets the premium bucket immediately, and a
	// lapsed one gets the free bucket, instead of inheriting tokens minted against the other limit.
	keyAt := tier + "\x00" + key

	l.mu.Lock()
	defer l.mu.Unlock()

	// ROOM IS MADE ONLY FOR A CREDENTIAL THAT IS NOT HERE YET, and that ordering is the difference between a
	// bounded table and a destructive one: at the cap, making room on EVERY request means every request
	// evicts somebody -- so on a busy instance the table would spend all its time throwing away the
	// credentials it is supposed to be counting. A caller who is already tracked never costs another caller
	// its bucket.
	b, ok := l.buckets[keyAt]
	if !ok {
		if len(l.buckets) >= l.maxBuckets {
			l.makeRoomLocked(now)
		}
		b = newBucket(config, now)
		l.buckets[keyAt] = b
	}

	// Refill by elapsed time, capped at the burst. `last` moves only by the amount of time that has been
	// accounted for, never to `now` unconditionally, so a clock that jumps backwards cannot mint tokens.
	if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.tokens = min(b.burst, b.tokens+elapsed*b.rate)
		b.last = now
	}

	limit := config.RequestsPerMinute
	if b.tokens >= 1 {
		b.tokens--
		return Decision{Allowed: true, Limit: limit, Remaining: int(b.tokens)}
	}

	// The wait for the NEXT token, which is what Retry-After means. Rounded up to a whole second: a header
	// that says "0" invites an immediate retry that is refused again, which reads as a broken limit.
	wait := time.Duration((1-b.tokens)/b.rate*float64(time.Second)) + time.Second
	return Decision{Allowed: false, Limit: limit, Remaining: 0, RetryAfter: wait.Truncate(time.Second)}
}

// newBucket starts a full bucket for one limit, and the limit travels WITH the bucket. See the bucket type's
// comment for why that is not an optimisation.
func newBucket(config Config, now time.Time) *bucket {
	return &bucket{
		tokens: float64(config.Burst),
		last:   now,
		burst:  float64(config.Burst),
		rate:   float64(config.RequestsPerMinute) / 60.0,
	}
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
	for key, b := range l.buckets {
		// Measured against the bucket's OWN limit (see the bucket type's comment): a bucket is forgettable
		// when it is full again, and "full" is a fact about the limit it counts against.
		if b.tokens+now.Sub(b.last).Seconds()*b.rate >= b.burst {
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
	_, ok := l.buckets[l.tierOf(key)+"\x00"+key]
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
