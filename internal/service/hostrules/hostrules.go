// Package hostrules owns the two rules this instance obeys before it makes an automated request to
// somebody else's server: what the host's robots.txt allows, and how often this instance may knock.
//
// WHY IT IS A PACKAGE AND NOT A FEW LINES IN THE TWO CALLERS. There are exactly two production
// clients that issue automated outbound requests on their own initiative -- the ingest pipeline's
// page fetch (internal/service/ingest/extract) and the health sweep's HEAD (internal/service/content)
// -- and they must obey the SAME rules, because a second implementation would be a second definition
// of "allowed", drift between them would be invisible, and the failure direction is the one that
// matters: an instance whose fetch honours robots.txt and whose sweep does not is still a client that
// ignores robots.txt. One implementation, one robots cache, one pace, shared by both.
//
// WHAT THIS IS NOT. It is not a crawler framework: there is no frontier, no cooldown schedule, no
// per-URL retry policy. This instance fetches URLs a member submitted and URLs it already recorded;
// it does not discover URLs. The rules here exist so that those fetches are well-behaved, not so that
// more of them can be made.
package hostrules

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/stashapp/stash-box/internal/config"
)

// ErrDisallowed is returned when robots.txt REFUSES the path: the host answered, and what it answered
// was a rule. Matched with errors.Is and treated as a policy refusal by both callers -- the pipeline
// records "robots_disallowed" against the queue row, and the sweep records a check it could not make
// rather than a source that died.
var ErrDisallowed = errors.New("the host's robots.txt disallows this request")

// ErrRobotsUnavailable is returned when robots.txt could not be READ (a 5xx, a transport failure), which
// RFC 9309 says to treat as a complete refusal -- but it is a different fact from a rule, and the
// difference decides what the two callers report. "The host told us no" is a decision about this
// client; "the host could not tell us anything" is the host not answering, and for a health check that
// is the very thing the sweep exists to detect, so reporting it as a policy refusal would hide link
// rot behind a compliance message.
var ErrRobotsUnavailable = errors.New("the host's robots.txt could not be read")

const (
	// userAgent identifies this client to the hosts it fetches from. The token (before the first
	// slash) is what robots.txt groups match against, and it is deliberately self-describing: a host
	// that wants to refuse this agent can, and one that wants to contact an operator can see what
	// this is.
	userAgent = "stash-box-aggregator/1.0 (+metadata only)"

	// robotsTTL is how long a host's robots.txt is trusted. An hour is Googlebot's order of
	// magnitude and is a compromise between two costs: a shorter one multiplies the requests this
	// instance makes to hosts it already asked, and a longer one keeps a rule the host has since
	// changed unhonoured. The rate limit, not this, is what bounds actual traffic.
	robotsTTL = time.Hour

	// robotsMaxBody bounds the robots.txt read. The largest real file is a few hundred KB, and one
	// that streams forever must not be read into memory by a queue worker.
	robotsMaxBody = 256 * 1024

	// robotsTimeout bounds the robots.txt fetch itself, separately from the page fetch: a host whose
	// robots.txt hangs should refuse the fetch quickly rather than after the page timeout.
	robotsTimeout = 10 * time.Second
)

// Rules holds the per-host state both clients share, and is safe for concurrent use.
//
// The state is process-wide by construction (Default) and not per-client, for the reason the package
// comment gives: two clients with two caches would make two requests to a host where one was
// intended, and the pace this instance keeps is a property of the instance, not of a struct.
type Rules struct {
	mu          sync.Mutex
	minInterval time.Duration
	last        map[string]time.Time
	robots      map[string]*robotsEntry

	// Injectable for tests, and honest about it: a clock, a sleeper and a robots fetcher are the
	// three things that would otherwise make this untestable without a real server and real waiting.
	now   func() time.Time
	sleep func(context.Context, time.Duration) error
	fetch func(ctx context.Context, robotsURL string) (int, []byte, error)

	client *http.Client
}

type robotsEntry struct {
	rules []robotsRule
	at    time.Time
}

type robotsRule struct {
	allow bool
	// path may contain `*` (any sequence) and a trailing `$` (end of path).
	path string
}

// New builds a Rules with the given minimum interval between requests to one host.
func New(minInterval time.Duration) *Rules {
	return &Rules{
		minInterval: minInterval,
		last:        map[string]time.Time{},
		robots:      map[string]*robotsEntry{},
		now:         time.Now,
		sleep:       sleepContext,
		client:      &http.Client{Timeout: robotsTimeout},
	}
}

var (
	defaultOnce  sync.Once
	defaultRules *Rules
)

// Default is the process-wide Rules both production clients use, paced by configuration.
//
// Built once: a Rules per call would reset the robots cache and the pace on every cron tick, which
// is the same as having neither.
func Default() *Rules {
	defaultOnce.Do(func() {
		defaultRules = New(config.GetContentFetchMinInterval())
	})
	return defaultRules
}

// BeforeFetch returns nil when this instance may request rawURL now, and otherwise an error saying
// why not: ErrDisallowed when robots.txt refuses the path, or the caller's context error if it was
// cancelled while waiting for the host's pace.
//
// It WAITS rather than refusing when the only obstacle is the pacing: the request is legitimate and
// merely early, and refusing it would turn a politeness rule into a failure the queue retries with a
// backoff, which is slower for us and louder for the host.
//
// The order is robots then pace, and it is the cheap one: a refusal never costs a wait, so a queue
// full of blocked URLs drains at full speed instead of two seconds a row.
func (r *Rules) BeforeFetch(ctx context.Context, rawURL string) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Host == "" {
		// Not this function's job to validate: the normaliser refuses a URL with no host, and a
		// request built from one fails on its own terms. Returning nil here means the fetch proceeds
		// and reports its own failure.
		return nil
	}
	// The ROBOTS cache is keyed by SCHEME AND HOST, because robots.txt is served per origin: an
	// http-only host states its terms at http://host/robots.txt and the https one is a different
	// file that may not exist. The PACE is keyed by host alone, because the thing being spared is the
	// server: a host seen over both schemes is one machine, and giving each scheme its own slot would
	// double the traffic this instance sends it.
	host := strings.ToLower(parsed.Host)
	origin := strings.ToLower(parsed.Scheme) + "://" + host

	if err := r.robotsAllows(ctx, origin, parsed.EscapedPath(), parsed.RawQuery); err != nil {
		return err
	}
	return r.wait(ctx, host)
}

// wait enforces the minimum interval for a host, reserving this request's departure slot.
//
// THE SLOT IS RESERVED, NOT THE ARRIVAL, and the difference is the whole correctness of the pace:
// recording the time the decision was made would let the next request measure its interval from
// BEFORE the sleep this one is about to take, so two requests would depart back to back -- the exact
// burst a rate limit exists to prevent. The reservation is when this request will leave.
func (r *Rules) wait(ctx context.Context, host string) error {
	if r.minInterval <= 0 {
		return nil
	}

	r.mu.Lock()
	now := r.now()
	previous, seen := r.last[host]

	departure := now
	if seen && now.Sub(previous) < r.minInterval {
		departure = previous.Add(r.minInterval)
	}
	r.last[host] = departure
	r.mu.Unlock()

	if wait := departure.Sub(now); wait > 0 {
		return r.sleep(ctx, wait)
	}
	return nil
}

// robotsAllows reports whether the path may be requested at this origin, with a sentence explaining a
// refusal.
//
// A missing robots.txt (4xx) allows everything: RFC 9309 says so explicitly, and refusing everything
// because a site has no robots.txt would make the rule an obstacle to the sites that state no terms
// rather than to the ones that do. An UNAVAILABLE robots.txt (5xx, or a transport failure) refuses
// everything, which is also RFC 9309's rule and is the honest reading of "the host cannot tell us its
// terms right now": the fetch that follows would be made without knowing whether it is allowed.
func (r *Rules) robotsAllows(ctx context.Context, origin, path, rawQuery string) error {
	entry, err := r.robotsFor(ctx, origin)
	if err != nil {
		return fmt.Errorf("%w at %s: %v", ErrRobotsUnavailable, origin, err)
	}
	if entry == nil {
		return nil
	}

	// The query is part of what is matched, because a rule may name it (`/x?y`) -- and it is
	// appended with its `?` so a rule matching the bare path still matches.
	target := path
	if rawQuery != "" {
		target += "?" + rawQuery
	}

	rule, matched := matchRule(entry.rules, target)
	// The match is over Allow AND Disallow rules, and the longest one wins, so a matched Allow is a
	// permission: only a matched Disallow refuses.
	if !matched || rule.allow {
		return nil
	}
	return fmt.Errorf("%w: robots.txt at %s disallows %s for %s (Disallow: %s)",
		ErrDisallowed, origin, target, userAgent, rule.path)
}

// robotsFor returns the cached rules for a host, fetching them if the cache has no fresh entry. A nil
// entry with a nil error means the host states no terms (which allows everything).
func (r *Rules) robotsFor(ctx context.Context, origin string) (*robotsEntry, error) {
	r.mu.Lock()
	if entry, ok := r.robots[origin]; ok && r.now().Sub(entry.at) < robotsTTL {
		r.mu.Unlock()
		return entry, nil
	}
	r.mu.Unlock()

	// Fetched outside the lock. Two goroutines racing here would fetch robots.txt twice, which is a
	// wasted request to a host we are being careful with -- but this instance drains its queue in one
	// goroutine, so the race is theoretical, and a lock held across a network call is a real problem.
	status, body, err := r.fetchRobots(ctx, origin)
	if err != nil {
		return nil, err
	}

	switch {
	case status >= 200 && status < 300:
		entry := &robotsEntry{rules: parseRobots(body), at: r.now()}
		r.mu.Lock()
		r.robots[origin] = entry
		r.mu.Unlock()
		return entry, nil
	case status >= 400 && status < 500:
		// "No robots.txt here, or it is not readable by us" -- RFC 9309's allow-all. Cached as an
		// empty rule set so the TTL applies and this is not re-asked on every URL.
		entry := &robotsEntry{at: r.now()}
		r.mu.Lock()
		r.robots[origin] = entry
		r.mu.Unlock()
		return entry, nil
	default:
		return nil, fmt.Errorf("the host answered %d", status)
	}
}

// fetchRobots reads /robots.txt for an origin (scheme://host).
//
// It uses the SAME User-Agent as the fetches it governs, because a host that groups rules by agent
// must be able to answer for the client that will actually make them.
func (r *Rules) fetchRobots(ctx context.Context, origin string) (int, []byte, error) {
	if r.fetch != nil {
		return r.fetch(ctx, origin+"/robots.txt")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/robots.txt", nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := r.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, robotsMaxBody))
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, body, nil
}

// parseRobots reads the groups this instance should obey.
//
// THE AGENT MATCHING IS THE PART THAT IS EASY TO GET WRONG, so it is stated: a group applies when one
// of its `User-agent` lines is `*` or a case-insensitive PREFIX of this client's token
// (`stash-box-aggregator`), which is what RFC 9309 specifies and what makes `User-agent:
// stash-box-aggregator/1.0` and `User-agent: stash-box` both work. When several groups match, their
// rules are COMBINED and the longest match decides (see the return below for why a "most specific
// wins" reading would be wrong). A file with no matching group states no terms for this client and
// allows everything.
//
// Comments run to the end of the line, `Allow`/`Disallow` with an empty value mean "nothing", and any
// other directive (`Sitemap`, `Crawl-delay`, `Host`) is ignored -- `Crawl-delay` deliberately,
// because this instance's pace is configured and a host-set delay this instance cannot see in its
// logs would be a setting that changes behaviour from outside.
func parseRobots(body []byte) []robotsRule {
	type group struct {
		agents []string
		rules  []robotsRule
	}

	var (
		groups  []group
		current *group
		// lastWasAgent tracks whether the previous line was a User-agent, because a group is a
		// CONTIGUOUS block of agent lines followed by rules: one agent line after a rule starts a
		// NEW group rather than adding an agent to the current one.
		lastWasAgent bool
	)

	for _, rawLine := range strings.Split(string(body), "\n") {
		line := rawLine
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		field, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		field = strings.ToLower(strings.TrimSpace(field))
		value = strings.TrimSpace(value)

		switch field {
		case "user-agent":
			if !lastWasAgent || current == nil {
				groups = append(groups, group{})
				current = &groups[len(groups)-1]
				lastWasAgent = true
			}
			current.agents = append(current.agents, strings.ToLower(value))
		case "allow", "disallow":
			lastWasAgent = false
			if current == nil {
				// A rule before any agent line belongs to no group and is ignored, which is what
				// every implementation does with it.
				continue
			}
			if value == "" {
				// `Disallow:` with nothing after it states no restriction (and `Allow:` with nothing
				// states no permission), so neither becomes a rule.
				continue
			}
			current.rules = append(current.rules, robotsRule{allow: field == "allow", path: value})
		default:
			lastWasAgent = false
		}
	}

	// The most specific matching group: a group naming this client beats the wildcard one.
	haystack := strings.ToLower(userAgent)
	var (
		specific *group
		wildcard *group
	)
	for i := range groups {
		for _, agent := range groups[i].agents {
			if agent == "*" {
				wildcard = &groups[i]
				continue
			}
			// PREFIX matching against the whole User-Agent string, and the direction of the error is
			// deliberate: `stash-box`, `stash-box-aggregator` and `stash-box-aggregator/1.0` all
			// match, and so does a rule that names only `stash`. A rule naming a shorter product
			// than ours is still, as far as this instance can tell, a rule about this instance --
			// and the safe response to that uncertainty is to obey it, because the cost of obeying a
			// rule meant for somebody else is one page this instance does not read, while the cost
			// of ignoring one meant for us is the thing this package exists to prevent.
			//
			// A rule naming a LONGER product (`stash-box-aggregator-pro`, `googlebot`) does not
			// match, which is correct: those are other clients.
			if strings.HasPrefix(haystack, agent) {
				specific = &groups[i]
			}
		}
	}

	// THE GROUPS ARE COMBINED, and this is the one place where a "most specific wins" simplification
	// would be wrong in the direction that matters. A host that writes `User-agent: * Disallow:
	// /private/` and then adds a group for this client about `/named/` has stated BOTH refusals, and
	// an implementation that used only the named group would ignore the wildcard one -- fetching
	// exactly what the host refused. The named group is not an override; it is an addition. The
	// longest-match rule then arbitrates between them, which is what makes `Disallow: /` plus
	// `Allow: /ok` work whichever group each appears in.
	var combined []robotsRule
	if wildcard != nil {
		combined = append(combined, wildcard.rules...)
	}
	if specific != nil {
		combined = append(combined, specific.rules...)
	}
	return combined
}

// matchRule applies RFC 9309's selection: the longest matching pattern wins, and an Allow wins a tie.
//
// The tie rule is not cosmetic. `Disallow: /` with `Allow: /ok` is the common shape of "everything
// except this", and it works only if the longer pattern is preferred -- both patterns match `/ok`, and
// picking the shorter one (or the last one) would refuse the page the host explicitly permitted.
func matchRule(rules []robotsRule, path string) (robotsRule, bool) {
	best := robotsRule{allow: false, path: ""}
	matched := false

	for _, rule := range rules {
		if !robotsMatch(rule.path, path) {
			continue
		}
		if !matched || len(rule.path) > len(best.path) || (len(rule.path) == len(best.path) && rule.allow) {
			best = rule
			matched = true
		}
	}
	return best, matched
}

// robotsMatch reports whether one robots.txt pattern matches a path, with `*` as any sequence and a
// trailing `$` anchoring the end.
//
// Both features are real in the wild and both are implemented rather than ignored: a client that
// treats `*` as a literal would think it may fetch what the host refused, which is the failure this
// package exists to avoid. The match is a small backtracking walk done without regexp -- a pattern
// language evaluated by another pattern language is how a `Disallow` line becomes a denial of
// service, and this file is parsed from a host this instance does not control.
func robotsMatch(pattern, path string) bool {
	anchored := strings.HasSuffix(pattern, "$")
	if anchored {
		pattern = strings.TrimSuffix(pattern, "$")
	}

	segments := strings.Split(pattern, "*")
	if len(segments) == 1 {
		if anchored {
			return path == pattern
		}
		return strings.HasPrefix(path, pattern)
	}

	// The first segment must be a prefix, the last must be a suffix when anchored (or merely present
	// otherwise), and the middle ones must appear in order.
	if !strings.HasPrefix(path, segments[0]) {
		return false
	}
	rest := path[len(segments[0]):]

	for i := 1; i < len(segments)-1; i++ {
		if segments[i] == "" {
			continue
		}
		index := strings.Index(rest, segments[i])
		if index < 0 {
			return false
		}
		rest = rest[index+len(segments[i]):]
	}

	last := segments[len(segments)-1]
	if anchored {
		return strings.HasSuffix(rest, last) && len(rest) >= len(last)
	}
	return strings.Contains(rest, last)
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
