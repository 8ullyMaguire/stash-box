package ratelimit

import (
	"testing"
	"time"
)

// THE TWO TIERS (plan phase 15, spec §18: §18.1's "higher API limits").
//
// The rules these tests hold, each of which the first implementation got wrong somewhere:
//
//   - a credential the predicate does not claim is counted against the FREE limit, and the presence of a
//     premium limit cannot move that number;
//   - a tier change starts a fresh bucket, because tokens are counted against a limit;
//   - the sweep measures each bucket against ITS OWN limit, not the free one;
//   - a premium limit that is switched off (zero means off, as everywhere in this package) is no limit at all,
//     not a fallback to the free one.

func tiersForTest(premiumKeys ...string) func(string) bool {
	return func(key string) bool {
		for _, k := range premiumKeys {
			if k == key {
				return true
			}
		}
		return false
	}
}

func TestAPremiumCredentialCountsAgainstThePremiumLimitAndAFreeOneDoesNot(t *testing.T) {
	free := Config{RequestsPerMinute: 60, Burst: 2}
	premium := Config{RequestsPerMinute: 6000, Burst: 10}
	l := NewWithTiers(free, premium, tiersForTest("premium-key"))
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

	if got := l.Allow("premium-key", now); got.Limit != premium.RequestsPerMinute {
		t.Errorf("a premium credential's limit = %d, want %d", got.Limit, premium.RequestsPerMinute)
	}
	if got := l.Allow("free-key", now); got.Limit != free.RequestsPerMinute {
		t.Errorf("an ungranted credential's limit = %d, want the free %d", got.Limit, free.RequestsPerMinute)
	}

	// The premium burst is the premium burst: two requests exhaust the free burst and must not exhaust this.
	l.Allow("premium-key", now)
	if got := l.Allow("premium-key", now); !got.Allowed {
		t.Errorf("a premium credential was refused after 3 requests against a burst of %d (remaining %d)",
			premium.Burst, got.Remaining)
	}
}

// THE FREE LIMIT IS THE SAME NUMBER WHETHER OR NOT THE TIER EXISTS. This is the assertion §18.2's "API access
// (rate-limited)" item turns on: a premium tier is a second limit, never a reduction of this one.
func TestTheFreeLimitIsUnchangedByThePresenceOfATier(t *testing.T) {
	free := Config{RequestsPerMinute: 60, Burst: 3}
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

	without := New(free)
	with := NewWithTiers(free, Config{RequestsPerMinute: 6000, Burst: 30}, tiersForTest("someone-else"))

	for i := range 4 {
		a := without.Allow("a-key", now)
		b := with.Allow("a-key", now)
		if a != b {
			t.Fatalf("request %d: the free decision changed with a tier present: %+v vs %+v", i+1, a, b)
		}
	}
}

// A TIER CHANGE STARTS A FRESH BUCKET, in both directions, and the free direction is the one worth stating: a
// lapsed member gets the FREE bucket, which is a bucket that may well be empty from before -- they do not
// inherit the premium bucket's tokens, and they do not get the premium limit back.
func TestATierChangeStartsAFreshBucket(t *testing.T) {
	free := Config{RequestsPerMinute: 60, Burst: 2}
	premium := Config{RequestsPerMinute: 6000, Burst: 10}
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

	premiumNow := false
	l := NewWithTiers(free, premium, func(string) bool { return premiumNow })

	// Spend the free bucket dry: two tokens, two requests.
	l.Allow("a-key", now)
	l.Allow("a-key", now)
	if got := l.Allow("a-key", now); got.Allowed {
		t.Fatal("the free burst of 2 allowed a third request: the test cannot see a bucket at all")
	}

	// The member is granted premium. The next request is allowed, against the PREMIUM limit -- not refused on
	// the free bucket's empty tokens, and not allowed at the free limit.
	premiumNow = true
	granted := l.Allow("a-key", now)
	if !granted.Allowed {
		t.Error("a credential that just became premium was refused on the free bucket it had exhausted")
	}
	if granted.Limit != premium.RequestsPerMinute {
		t.Errorf("after the grant the limit is %d, want the premium %d", granted.Limit, premium.RequestsPerMinute)
	}

	// And back: the lapse returns the member to the free bucket, which still has no tokens.
	premiumNow = false
	lapsed := l.Allow("a-key", now)
	if lapsed.Allowed {
		t.Error("a lapsed credential was allowed at the free limit on tokens it spent before the grant")
	}
	if lapsed.Limit != free.RequestsPerMinute {
		t.Errorf("after the lapse the limit is %d, want the free %d", lapsed.Limit, free.RequestsPerMinute)
	}
}

// THE SWEEP MEASURES A BUCKET AGAINST ITS OWN LIMIT. The first implementation compared every bucket to the FREE
// burst, which with two tiers means discarding buckets that are still spending -- and the discard is silent:
// the premium caller simply gets a full bucket again, over and over, exactly when the table is under pressure.
//
// THE SWEEPS COUNTER IS THE SIGNAL, not the bucket's presence, and the first version of this test learned that
// the hard way: makeRoomLocked has TWO passes, and the SECOND one (least-recently-used) is ALLOWED to evict a
// bucket that is still spending -- that is its documented trade. So the assertion is `sweeps == 0` plus a map
// at the cap, where the LRU pass is the only pass that may delete anything, and the bucket is created LAST so
// that the LRU pass picks one of the others.
func TestTheSweepMeasuresEachBucketAgainstItsOwnLimit(t *testing.T) {
	free := Config{RequestsPerMinute: 60, Burst: 2}
	premium := Config{RequestsPerMinute: 6000, Burst: 10}
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

	l := NewWithTiers(free, premium, tiersForTest("premium-key"))
	l.maxBuckets = 3

	// Two free buckets, mid-spend (one token each of a burst of 2), created first so they are the older ones.
	l.Allow("free-key-one", now)
	l.Allow("free-key-two", now)

	// The premium bucket, mid-spend at 8 of its 10 tokens, and the newest -- so the LRU pass leaves it alone.
	// Against the FREE burst of 2 it projects as full again and would be swept; against its own it is not.
	//
	// THE MILLISECOND MATTERS, which is why the fixture states it: the sweep's first pass projects each
	// bucket's refill, and a premium limit of 6000/minute replenishes two tokens in 20ms -- so a sweep even a
	// second later would find the premium bucket legitimately full again and the assertion would be about the
	// clock rather than about which limit the pass measured.
	premiumAt := now.Add(time.Millisecond)
	l.Allow("premium-key", premiumAt)
	l.Allow("premium-key", premiumAt)
	if l.Buckets() != 3 {
		t.Fatalf("the fixture built %d buckets, want 3: nothing below is observable", l.Buckets())
	}

	l.makeRoomLocked(premiumAt)
	if l.sweeps != 0 {
		t.Errorf("the sweep dropped %d bucket(s): a premium bucket with 8 of 10 tokens left is not full again",
			l.sweeps)
	}
	if !l.Tracked("premium-key") {
		t.Error("the premium bucket was swept while it was still spending")
	}

	// The control, so the assertions above cannot pass by the sweep doing nothing at all: after an hour of
	// refill every bucket IS full again, and the first pass takes them.
	l.makeRoomLocked(now.Add(time.Hour))
	if l.sweeps == 0 {
		t.Error("no bucket was swept after an hour of refill: the sweep is inert and the test above proves nothing")
	}
}

// A PREMIUM BUCKET REFILLS AT THE PREMIUM RATE, and the mutation battery is why this test exists: the first
// version of the battery mutated a bucket's refill rate to a constant and NOTHING noticed, because every
// assertion about refilling was made when the bucket had time to reach its full burst -- and the burst is a
// cap, so a bucket that refills too fast looks exactly like one that refills correctly.
//
// The discriminating case spends nine of ten tokens and then waits ten seconds against a limit of six per
// minute: one token is earned, so the same request that leaves one token in reserve leaves NINE if the bucket
// refills at a token a second.
func TestAPremiumBucketRefillsAtItsOwnRate(t *testing.T) {
	free := Config{RequestsPerMinute: 60, Burst: 5}
	premium := Config{RequestsPerMinute: 6, Burst: 10}
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

	l := NewWithTiers(free, premium, tiersForTest("premium-key"))

	// Nine of the ten tokens, all at the same instant.
	var last Decision
	for range 9 {
		last = l.Allow("premium-key", now)
		if !last.Allowed {
			t.Fatalf("the premium burst of %d refused a request: %+v", premium.Burst, last)
		}
	}
	if last.Remaining != 1 {
		t.Fatalf("after nine of ten requests the premium bucket reports %d left, want 1: the fixture is wrong",
			last.Remaining)
	}

	// Ten seconds at six per minute earns exactly one token: ten is the cap, so a faster refill is invisible
	// in the token count -- it shows in what is left after the request.
	got := l.Allow("premium-key", now.Add(10*time.Second))
	if !got.Allowed {
		t.Fatalf("a request was refused ten seconds after nine of ten: %+v", got)
	}
	if got.Remaining != 1 {
		t.Errorf("ten seconds at six per minute left %d tokens, want 1: a bucket that refills at some other "+
			"rate is a limit that is not the one the configuration states", got.Remaining)
	}
	if got.Limit != premium.RequestsPerMinute {
		t.Errorf("the refilled request was counted against %d, want the premium %d", got.Limit,
			premium.RequestsPerMinute)
	}
}

// A PREMIUM LIMIT THAT IS OFF IS NO LIMIT, not a fallback to the free one: zero means off, as it does
// everywhere in this package, and an operator who sets the premium rate to zero has said "no limit for these
// callers" -- which is a posture this file permits and the startup validation lets through.
func TestAPremiumLimitOfZeroIsNoLimitRatherThanTheFreeOne(t *testing.T) {
	free := Config{RequestsPerMinute: 1, Burst: 1}
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

	l := NewWithTiers(free, Config{}, tiersForTest("premium-key"))
	for i := range 50 {
		if got := l.Allow("premium-key", now); !got.Allowed {
			t.Fatalf("request %d was refused: a disabled premium configuration is a limit, not the absence of one", i+1)
		}
	}
	if l.Tracked("premium-key") {
		t.Error("a credential with no limit spends no memory: no bucket should exist for it")
	}
	if got := l.Allow("free-key", now); !got.Allowed {
		t.Error("the free credential was refused its first request")
	}
	if got := l.Allow("free-key", now); got.Allowed {
		t.Error("the free credential's burst of 1 allowed a second request")
	}
}

// One limit, as before the tier existed: a nil predicate means no credential is premium, and the limiter
// behaves exactly as `New` does.
func TestANilPredicateMeansOneLimit(t *testing.T) {
	free := Config{RequestsPerMinute: 60, Burst: 1}
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

	l := NewWithTiers(free, Config{RequestsPerMinute: 6000, Burst: 100}, nil)
	if got := l.Allow("any-key", now); got.Limit != free.RequestsPerMinute {
		t.Errorf("with no predicate the limit is %d, want the free %d", got.Limit, free.RequestsPerMinute)
	}
	if l.PremiumConfig().Disabled() {
		t.Error("PremiumConfig reports the premium limit disabled when it was passed: PostureLine would state " +
			"a single limit on an instance that counts two")
	}
}
