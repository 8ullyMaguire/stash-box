package sdbimport

// A minimal robots.txt, enough to honour the two directives that matter to an image mirror: which agents are
// addressed, and which paths they are told to stay out of.
//
// NOT a dependency, on purpose. The published Go robots parsers carry matching semantics for wildcards, crawl
// delays, sitemaps and per-agent group ordering -- a real spec, and a real surface to audit for the one thing
// this needs: "does the group that applies to me disallow this path?". For an image fetcher, WILDCARDS ARE
// TREATED AS MATCHING (a `*` makes the rule apply to any path that starts with the prefix before it), because
// the cost of honouring a rule that does not apply is a skipped image and the cost of ignoring one that does is
// the behaviour robots.txt exists to prevent.

import (
	"strings"
)

// robotsRules is the parsed form: for each agent token, the path prefixes it is denied, plus the catch-all.
type robotsRules struct {
	byAgent map[string][]string
	all     []string
}

func parseRobots(body string) robotsRules {
	r := robotsRules{byAgent: map[string][]string{}}
	var current []string // the agent tokens this group addresses; empty until a User-agent line
	groupSawRule := false

	flush := func() {
		current = nil
		groupSawRule = false
	}

	for _, rawLine := range strings.Split(body, "\n") {
		line := rawLine
		if i := strings.IndexAny(line, "#"); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)

		switch key {
		case "user-agent":
			// A rule after an agent line belongs to that group; another agent line after a rule starts a new
			// group. Getting this wrong is how a parser applies the wrong group's rules, so it is explicit.
			if groupSawRule {
				flush()
			}
			current = append(current, strings.ToLower(value))
		case "disallow":
			groupSawRule = true
			if len(current) == 0 {
				// A rule before any User-agent line addresses nobody in particular; treated as `*`, which is
				// the reading that keeps us out rather than the one that lets us in.
				current = []string{"*"}
			}
			if value == "" {
				continue // "Disallow:" with nothing after it means everything is allowed.
			}
			for _, agent := range current {
				if agent == "*" {
					r.all = append(r.all, value)
					continue
				}
				r.byAgent[agent] = append(r.byAgent[agent], value)
			}
		case "allow":
			// Unused: an Allow narrows a Disallow in the full spec, and this fetcher treats any Disallow that
			// applies as decisive. Narrowing is a rule an image mirror does not need, and implementing it
			// half-way would produce a fetcher that ignores a host's restrictions because its Allow was read
			// more generously than its Disallow.
			groupSawRule = true
		}
	}
	return r
}

// disallows reports whether the path is denied to this agent.
//
// The agent matches on the first token of the User-Agent, case-insensitively, which is what the spec's own
// matching section describes for the common case, plus `*` for every agent.
func (r robotsRules) disallows(path, userAgent string) bool {
	if path == "" {
		path = "/"
	}
	agent := strings.ToLower(userAgent)
	if i := strings.IndexAny(agent, "/ "); i >= 0 {
		agent = agent[:i]
	}

	for _, rule := range r.byAgent[agent] {
		if matchesRule(rule, path) {
			return true
		}
	}
	for _, rule := range r.all {
		if matchesRule(rule, path) {
			return true
		}
	}
	return false
}

// matchesRule applies one rule's prefix, with `*` treated as matching anything and a trailing `$` as an
// end-of-path anchor -- the two operators that change a legitimate image URL into a disallowed one.
func matchesRule(rule, path string) bool {
	anchored := strings.HasSuffix(rule, "$")
	rule = strings.TrimSuffix(rule, "$")

	if !strings.Contains(rule, "*") {
		if anchored {
			return path == rule
		}
		return strings.HasPrefix(path, rule)
	}

	parts := strings.Split(rule, "*")
	pos := 0
	for i, part := range parts {
		if part == "" {
			continue
		}
		idx := strings.Index(path[pos:], part)
		if idx < 0 {
			return false
		}
		if i == 0 && idx != 0 {
			return false // the first segment must be a prefix, not a substring
		}
		pos += idx + len(part)
	}
	if anchored {
		return pos == len(path)
	}
	return true
}
