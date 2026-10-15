package hostrules

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// The matcher's rules, as a table. Each case names the robots.txt syntax it pins, so a failure here
// reads as "the wildcard rule broke" and not as "one of twenty patterns".
//
// The expected values are the ones a careful human reading the file would give, and two of them are
// the cases that go wrong when an implementation guesses: `Disallow: /` with `Allow: /ok` (longest
// match wins, so /ok is allowed) and `Disallow: /*.pdf$` (the `$` means the query string is NOT
// matched, so /a.pdf?x=1 is allowed).
func TestRobotsMatch(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		path    string
		want    bool
	}{
		{"a plain prefix matches", "/private/", "/private/x", true},
		{"a plain prefix does not match elsewhere", "/private/", "/public/x", false},
		{"an empty pattern would match everything and is never built", "/", "/anything", true},
		{"a star spans a segment", "/a*b", "/ab", true},
		{"a star spans many characters", "/a*b", "/axxxb", true},
		{"a star spans slashes", "/a*b", "/a/x/b", true},
		{"a star is not a literal", "/a*b", "/a/zzz", false},
		{"a dollar anchors the end", "/x$", "/x", true},
		{"and therefore does not match with more after it", "/x$", "/x/y", false},
		{"a dollar with a query refused", "/*.pdf$", "/a.pdf", true},
		{"a dollar means the query is not matched at all", "/*.pdf$", "/a.pdf?x=1", false},
		{"an unterminated star matches nothing after it", "/a*", "/a", true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := robotsMatch(c.pattern, c.path); got != c.want {
				t.Errorf("robotsMatch(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
			}
		})
	}
}

func TestMatchRulePrefersTheLongestPatternAndAllowWinsTies(t *testing.T) {
	rules := []robotsRule{
		{allow: false, path: "/"},
		{allow: true, path: "/ok"},
		{allow: false, path: "/ok/no"},
	}

	cases := map[string]robotsRule{
		"/ok":     {allow: true, path: "/ok"},
		"/ok/no":  {allow: false, path: "/ok/no"},
		"/other":  {allow: false, path: "/"},
		"/ok/yes": {allow: true, path: "/ok"},
	}
	for path, want := range cases {
		got, matched := matchRule(rules, path)
		if !matched || got != want {
			t.Errorf("matchRule(%q) = %+v (matched=%v), want %+v", path, got, matched, want)
		}
	}

	// A tie between Allow and Disallow of the SAME length resolves to Allow: the shape
	// `Disallow: /a` + `Allow: /a` is a host explicitly permitting what a broader rule refused.
	tie := []robotsRule{{allow: false, path: "/a"}, {allow: true, path: "/a"}}
	got, _ := matchRule(tie, "/a")
	if !got.allow {
		t.Error("an Allow of the same length as a Disallow must win; otherwise a host cannot carve an exception")
	}
}

// The parser, against files whose shape decides behaviour: comments, groups, a named agent, a
// wildcard group, and a group that names somebody else.
func TestParseRobots(t *testing.T) {
	body := []byte(`
# a comment
User-agent: *
Disallow: /private/
Allow: /private/ok
Crawl-delay: 10

User-agent: googlebot
Disallow: /

User-agent: stash-box
Disallow: /named/
`)
	rules := parseRobots(body)

	allowed := func(path string) bool {
		rule, matched := matchRule(rules, path)
		return !matched || rule.allow
	}

	if !allowed("/public/x") {
		t.Error("a path no rule names is allowed")
	}
	if allowed("/private/x") {
		t.Error("the wildcard group's Disallow must apply: this client is covered by *")
	}
	if !allowed("/private/ok") {
		t.Error("the longer Allow inside the Disallow must win")
	}
	if allowed("/named/x") {
		t.Error("the group naming this client must beat the wildcard group")
	}
}

// A file with no group for this client states no terms, and a group naming a different product is not
// a group for this client.
func TestParseRobotsIgnoresOtherAgents(t *testing.T) {
	rules := parseRobots([]byte("User-agent: googlebot\nDisallow: /\n"))
	if len(rules) != 0 {
		t.Fatalf("a file about another client states nothing about this one, got %d rules", len(rules))
	}

	if rules := parseRobots([]byte("# nothing but a comment\n")); len(rules) != 0 {
		t.Fatalf("an empty file allows everything, got %d rules", len(rules))
	}

	// A bare `Disallow:` states no restriction, and a rule before any agent line belongs to nobody.
	if rules := parseRobots([]byte("Disallow: /x\nUser-agent: *\nDisallow:\n")); len(rules) != 0 {
		t.Fatalf("expected no rules, got %+v", rules)
	}
}

// helper builds a Rules whose robots.txt is a canned answer and whose clock and sleep are fake.
func testRules(t *testing.T, status int, body string, minInterval time.Duration) (*Rules, *[]string, *time.Time) {
	t.Helper()

	r := New(minInterval)
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	clock := &now
	var slept []string

	r.now = func() time.Time { return *clock }
	r.sleep = func(_ context.Context, d time.Duration) error {
		slept = append(slept, d.String())
		*clock = clock.Add(d)
		return nil
	}
	r.fetch = func(_ context.Context, robotsURL string) (int, []byte, error) {
		return status, []byte(body), nil
	}
	return r, &slept, clock
}

// The three answers a robots.txt fetch can give, and the direction each one fails in.
func TestBeforeFetchHonoursRobotsAndTheThreeAnswers(t *testing.T) {
	ctx := context.Background()

	t.Run("a rule for this client refuses the path", func(t *testing.T) {
		r, _, _ := testRules(t, 200, "User-agent: *\nDisallow: /private/", time.Second)
		err := r.BeforeFetch(ctx, "https://example.invalid/private/x")
		if !errors.Is(err, ErrDisallowed) {
			t.Fatalf("want ErrDisallowed, got %v", err)
		}
		if !strings.Contains(err.Error(), "example.invalid") {
			t.Error("the refusal must name the host, or the operator cannot tell whose rule it was")
		}
	})

	t.Run("a 404 allows everything", func(t *testing.T) {
		r, _, _ := testRules(t, 404, "", time.Second)
		if err := r.BeforeFetch(ctx, "https://example.invalid/private/x"); err != nil {
			t.Fatalf("a host with no robots.txt states no terms: %v", err)
		}
	})

	t.Run("a 5xx refuses everything, as its own fact", func(t *testing.T) {
		r, _, _ := testRules(t, 503, "", time.Second)
		err := r.BeforeFetch(ctx, "https://example.invalid/anything")
		if err == nil {
			t.Fatal("an unavailable robots.txt must fail CLOSED")
		}
		if !errors.Is(err, ErrRobotsUnavailable) {
			t.Fatalf("want ErrRobotsUnavailable, got %v", err)
		}
		if errors.Is(err, ErrDisallowed) {
			t.Error("and it must NOT be reported as a rule: the callers treat the two differently, " +
				"and a health check that read 'the host said no' for 'the host said nothing' would " +
				"hide link rot behind a compliance message")
		}
		if !strings.Contains(err.Error(), "503") {
			t.Error("the refusal must say why it could not be read")
		}
	})

	t.Run("a transport failure is the same fact", func(t *testing.T) {
		r, _, _ := testRules(t, 200, "", time.Second)
		r.fetch = func(context.Context, string) (int, []byte, error) {
			return 0, nil, fmt.Errorf("dial tcp: connection refused")
		}
		err := r.BeforeFetch(ctx, "https://example.invalid/a")
		if !errors.Is(err, ErrRobotsUnavailable) {
			t.Fatalf("want ErrRobotsUnavailable, got %v", err)
		}
	})

	t.Run("the robots answer is cached, so one host is asked once", func(t *testing.T) {
		calls := 0
		r, _, _ := testRules(t, 200, "User-agent: *\nDisallow: /private/", 0)
		r.fetch = func(_ context.Context, u string) (int, []byte, error) {
			calls++
			return 200, []byte("User-agent: *\nDisallow: /private/"), nil
		}
		_ = r.BeforeFetch(ctx, "https://example.invalid/a")
		_ = r.BeforeFetch(ctx, "https://example.invalid/b")
		_ = r.BeforeFetch(ctx, "https://example.invalid/c")
		if calls != 1 {
			t.Errorf("robots.txt was read %d times for one host; the cache is not shared by path", calls)
		}
	})
}

// The pace: requests to one host are spaced, requests to different hosts are not, and a refused
// request never waits.
func TestBeforeFetchPacesEachHost(t *testing.T) {
	ctx := context.Background()
	r, slept, _ := testRules(t, 404, "", 2*time.Second)

	if err := r.BeforeFetch(ctx, "https://a.invalid/1"); err != nil {
		t.Fatal(err)
	}
	if err := r.BeforeFetch(ctx, "https://a.invalid/2"); err != nil {
		t.Fatal(err)
	}
	if len(*slept) != 1 || (*slept)[0] != "2s" {
		t.Fatalf("the second request to one host must wait the full interval, slept=%v", *slept)
	}

	// A different host is a different pace.
	if err := r.BeforeFetch(ctx, "https://b.invalid/1"); err != nil {
		t.Fatal(err)
	}
	if len(*slept) != 1 {
		t.Errorf("a different host must not wait behind another host's pace, slept=%v", *slept)
	}

	// And a third request to the first host waits again, from ITS last request rather than from the
	// one that happened in between.
	if err := r.BeforeFetch(ctx, "https://a.invalid/3"); err != nil {
		t.Fatal(err)
	}
	if len(*slept) != 2 {
		t.Errorf("expected a second wait for the first host, slept=%v", *slept)
	}
}

func TestBeforeFetchDoesNotWaitForARefusedURL(t *testing.T) {
	ctx := context.Background()
	r, slept, _ := testRules(t, 200, "User-agent: *\nDisallow: /private/", 5*time.Second)

	// Two refused URLs in a row: a policy refusal must not cost the queue five seconds a row, or a
	// queue full of blocked URLs would drain at the pace of the host that refused them.
	_ = r.BeforeFetch(ctx, "https://example.invalid/private/a")
	_ = r.BeforeFetch(ctx, "https://example.invalid/private/b")
	if len(*slept) != 0 {
		t.Errorf("a refused URL must not wait, slept=%v", *slept)
	}
}

// A URL this instance cannot parse a host out of is not this function's business: the normaliser
// refuses it and the request reports its own failure. Asserted so the nil path is not an accident.
func TestBeforeFetchIgnoresAHostlessURL(t *testing.T) {
	r, slept, _ := testRules(t, 200, "User-agent: *\nDisallow: /", time.Second)
	if err := r.BeforeFetch(context.Background(), "not a url"); err != nil {
		t.Fatalf("want nil, got %v", err)
	}
	if len(*slept) != 0 {
		t.Error("nothing to fetch means nothing to pace")
	}
	if calls := r.robots; len(calls) != 0 {
		t.Error("and nothing to ask robots.txt about")
	}
}

// The wait must be interruptible: a cancelled drain stops rather than sleeping through its context.
func TestBeforeFetchReturnsTheContextErrorWhileWaiting(t *testing.T) {
	r := New(time.Hour)
	now := time.Now()
	r.now = func() time.Time { return now }
	// Real sleep, cancelled context.
	r.fetch = func(context.Context, string) (int, []byte, error) { return 404, nil, nil }

	if err := r.BeforeFetch(context.Background(), "https://example.invalid/a"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := r.BeforeFetch(ctx, "https://example.invalid/b")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want the context's error, got %v", err)
	}
}

// Default() is built once, so the robots cache and the pace survive between cron ticks. Asserted
// because a Default() that built a new Rules each call would pass every other test in this file and
// silently pace nothing.
func TestDefaultIsTheSameInstance(t *testing.T) {
	if Default() != Default() {
		t.Fatal("Default() must return one instance: a new one per call resets the robots cache and the pace")
	}
	if Default().minInterval <= 0 {
		t.Errorf("the default pace must be positive, got %s", Default().minInterval)
	}
}

func TestNewUsesTheDocumentedAgent(t *testing.T) {
	// The agent this instance identifies itself by, asserted as a value because robots.txt groups
	// match against it: changing it silently changes which rules apply.
	if !strings.HasPrefix(userAgent, "stash-box-aggregator/") {
		t.Errorf("the client's product token is part of its behaviour, not a label: %q", userAgent)
	}
	// And the parser must recognise it, or every named group would be ignored.
	rules := parseRobots([]byte("User-agent: stash-box-aggregator\nDisallow: /\n"))
	if len(rules) != 1 {
		t.Fatalf("a group naming this client's product token must apply, got %+v", rules)
	}
	_ = fmt.Sprintf("%v", rules)
}
