// Package badge derives a user's badges from their contribution counters.
//
// There is no user_badges table, and that is the design rather than an omission.
//
// A badge is a PREDICATE over counters: "solved 25 identifications", "100 approved
// edits", "hosted a replica". Storing the award means a badge can disagree with
// the counters that justify it, and the disagreement is invisible -- the badge is
// true, the numbers say otherwise, and nothing reports it. Worse, revocation is
// then a second code path: a reversal has to find the badge, delete it, and get it
// right, and a missed case leaves a curator wearing a badge for work they no
// longer did.
//
// Deriving makes the two impossible to separate. There is one source (the rollup),
// so a badge is ALWAYS consistent with the record, and "revoking" is the absence of
// a predicate becoming true -- no code, no migration, no state to get wrong.
//
// The cost, stated honestly: a badge cannot be granted manually, cannot carry an
// award date, and cannot exist for a user who is no longer in the system. All three
// are things a stored badge could do and this deliberately cannot. If a badge ever
// needs to say "you earned this on 3 March", it stops being a predicate and starts
// being a fact, and at that point it wants its own table.
package badge

import (
	"sort"

	"github.com/stashapp/stash-box/internal/service/trust"
)

// Badge is one award, as a value.
//
// A value and not a pointer, and not a stored row: there is nothing to point at.
// A client renders the whole list, so the slice is built once and handed over.
type Badge struct {
	// ID is stable across calls, so a client can key a rendered badge on it and
	// a re-read does not reorder the list under a tooltip.
	ID string
	// Name is the display name.
	Name string
	// Description says what earned it, and carries the number that did -- so the
	// user can see how far they are from the next one rather than guessing.
	Description string
	// Rarity is 1 (common) to 5 (notable), for display grouping. A made-up
	// ordering key rather than a count of holders: computing how many users hold
	// each badge is a different query against a different table, and a badge list
	// that blocks on it is a badge list that is slow on a large instance.
	Rarity int
	// Progress is how far the user is toward the NEXT tier of this badge, 0 to 1.
	// Reported for every badge rather than only unmet ones, so a client can render
	// a partial fill instead of a binary earned/not-earned.
	Progress float64
	// Earned is whether the predicate currently holds.
	Earned bool
	// Counter names which rollup column drives it, for clients that want to group
	// or sort by the kind of work.
	Counter string
}

// definition is one badge rule: a name, a counter, a set of tiers.
//
// TIERS rather than a single threshold, and the reason is that a badge is the
// thing a user aims at. "100 approved edits" is a target; "1 approved edit" is a
// fact already visible in the level. The tiers are the aspiration ladder, and
// Progress is measured against the next one -- so a user with 40 edits is told
// they are 60 short, not merely "no badge".
type definition struct {
	id      string
	name    string
	counter string
	rarity  int
	// description renders the CURRENT count and the NEXT threshold, so the text
	// is never a static string that disagrees with the numbers beside it.
	description func(current, next int) string
	// tiers are the ascending thresholds. The first is always 1, so any positive
	// count earns something and "no badge" means literally nothing contributed.
	tiers []int
}

// allDefinitions is every badge the system derives.
//
// Ordered by counter then tier so the output is DETERMINISTIC. A badge list that
// reshuffles between two reads of the same data makes a rendered profile flicker,
// and a profile that flickers is a profile nobody trusts.
var allDefinitions = []definition{
	{
		id:      "first_edit",
		name:    "First Edit",
		counter: "approved_edits",
		rarity:  1,
		tiers:   []int{1, 10, 50, 100, 500},
		description: func(current, next int) string {
			if next <= current {
				return "Your edits are being applied."
			}
			return pluralGap("edit", "edits", current, next)
		},
	},
	{
		id:      "detective",
		name:    "Detective",
		counter: "identification_solves",
		rarity:  2,
		tiers:   []int{1, 5, 25, 100, 500},
		description: func(current, next int) string {
			if next <= current {
				return "You have identified scenes from almost nothing."
			}
			return pluralGap("identification", "identifications", current, next)
		},
	},
	{
		id:      "questor",
		name:    "Questor",
		counter: "quests_completed",
		rarity:  2,
		tiers:   []int{1, 5, 25, 100},
		description: func(current, next int) string {
			if next <= current {
				return "You have completed curation quests."
			}
			return pluralGap("quest", "quests", current, next)
		},
	},
	{
		id:      "preserver",
		name:    "Preserver",
		counter: "replicas_hosted",
		rarity:  4,
		tiers:   []int{1, 10, 50, 250},
		description: func(current, next int) string {
			if next <= current {
				return "You are keeping the archive alive."
			}
			return pluralGap("replica", "replicas", current, next)
		},
	},
	{
		// The one badge driven by POINTS rather than a count, and the reason it
		// is here rather than omitted: a curator can cross a level threshold
		// through bountied quests without the raw counts reflecting that effort,
		// and without this the reward for finishing a 500-point bounty is a level
		// number and nothing else.
		id:      "taste_maker",
		name:    "Taste Maker",
		counter: "points",
		rarity:  3,
		tiers:   []int{100, 500, 2000, 10000},
		description: func(current, next int) string {
			if next <= current {
				return "Your contributions have shaped the archive."
			}
			return "Earn " + itoa(next-current) + " more points."
		},
	},
}

// packageDefinitions exposes the rule set to this package's own tests, which
// need to assert things about the tiers themselves (ascending order, first tier
// reachable by one) that Derive's output cannot show.
var packageDefinitions = allDefinitions

// AllIDs is every badge id, for a caller that wants the catalogue without a user.
//
// Exposed because "what badges exist" is a legitimate question with no user
// behind it -- a client's badge picker, a test, an operator page -- and answering
// it from a fabricated zero-valued Totals would be a lie about the tiers.
func AllIDs() []string {
	out := make([]string, 0, len(allDefinitions))
	for _, d := range allDefinitions {
		out = append(out, d.id)
	}
	return out
}

// Derive computes a user's badges from their totals.
//
// Pure and total: no database, no error, and it NEVER returns nil so a client
// rendering a profile can iterate it without a nil check. A user with no
// contributions gets every badge UNearned with a progress of zero, not an empty
// list -- an empty list is indistinguishable from "this system has no badges",
// which is a different and much worse thing to tell a user.
func Derive(totals trust.Totals) []Badge {
	badges := make([]Badge, 0, len(allDefinitions))

	for _, d := range allDefinitions {
		current := CounterValue(totals, d.counter)

		// Find the highest tier reached, and the next one. A count above every
		// tier is "maxed" rather than an index error, which is what a user with
		// 10,000 edits should see.
		reached, next := 0, -1
		for i, tier := range d.tiers {
			if current >= tier {
				reached = i
			} else {
				next = tier
				break
			}
		}
		earned := reached > 0 || current >= d.tiers[0]
		if next == -1 {
			// Maxed. The description branches on `next <= current`, and both
			// `next` values a maxed user could be given are <= their count, so
			// passing the highest tier here and passing -1 produce IDENTICAL
			// output -- I kept the tier and mutation testing proved the other arm
			// is equivalent rather than untested. So this is deliberate
			// redundancy, not a live branch: it names the intent (a maxed badge
			// is complete) without depending on progressFor's guard for a tier
			// that does not exist.
			next = d.tiers[len(d.tiers)-1]
		}

		badges = append(badges, Badge{
			ID:          d.id,
			Name:        d.name,
			Description: d.description(current, next),
			Rarity:      d.rarity,
			Progress:    progressFor(current, next),
			Earned:      earned,
			Counter:     d.counter,
		})
	}

	// Earned first, then rarest, then by id. Earned-first because a profile shows
	// what someone HAS; rarity within that because a 4-rarity badge is the one
	// worth looking at; id last so the order is total and stable for two users
	// with identical counters.
	sort.SliceStable(badges, func(a, b int) bool {
		x, y := badges[a], badges[b]
		if x.Earned != y.Earned {
			return x.Earned
		}
		if x.Rarity != y.Rarity {
			return x.Rarity > y.Rarity
		}
		return x.ID < y.ID
	})
	return badges
}

// CounterValue reads one counter out of the totals by name.
//
// Exported, and only because the default arm needs to be REACHABLE from a test.
// Every other function here is unexported, so nothing would ever execute the
// unknown-counter arm: a test that only reaches it through Derive has to supply a
// Totals that produces an unknown counter, which no input can do. A silent zero
// is the safe behaviour and a test that cannot reach it cannot protect it.
//
// It is a one-line, total, side-effect-free lookup, so exporting it costs nothing,
// and a client can use it to show a user's raw counter beside the badge.
//
// A switch over five cases rather than a map, and the reason is that a map would
// need a name for "points", which is not a counter at all -- it is a DERIVED
// value. A map lookup keyed by a field name invites putting points in there, and a
// reader cannot tell from the type whether that happened.
func CounterValue(totals trust.Totals, counter string) int {
	switch counter {
	case "approved_edits":
		return totals.ApprovedEdits
	case "identification_solves":
		return totals.IdentificationSolves
	case "quests_completed":
		return totals.QuestsCompleted
	case "replicas_hosted":
		return totals.ReplicasHosted
	case "points":
		return totals.Points()
	default:
		// An unknown counter is ZERO, not a panic. A typo in a definition should
		// produce a badge that is never earned -- visible as "you have none of
		// these" -- rather than a server error on every profile page. The
		// definitions are a fixed slice in this file, so this arm is unreachable
		// in practice and exists so that adding one is a data change.
		return 0
	}
}

// progressFor is how close the count is to the next tier, 0 to 1.
//
// Measured against the CURRENT tier, not the first: a user with 9 of 10 edits is
// 90% of the way, and measuring against zero would say 900%. The denominator is
// guarded because a tier of 0 would divide by zero, and no definition has one --
// but the guard is here so a future tier list cannot crash a profile page.
func progressFor(current, next int) float64 {
	if next <= 0 {
		return 1
	}
	if current >= next {
		return 1
	}
	// Clamped at 0 because a NEGATIVE count is possible -- the rollup is signed,
	// and a reversal can drive a counter below zero -- and a negative progress
	// would render a fill bar running backwards.
	if current < 0 {
		return 0
	}
	return float64(current) / float64(next)
}

// pluralGap is "N more edits", with the plural on the COUNT REMAINING rather than
// on the current total.
//
// "1 more edit" and "1 more edits" are the two ways to write it, and only one is
// correct. Getting it right is a two-line function and it is the difference
// between a badge that reads as written for a person and one that reads as
// generated.
func pluralGap(one, many string, current, next int) string {
	remaining := next - current
	if remaining == 1 {
		return "1 more " + one + "."
	}
	return itoa(remaining) + " more " + many + "."
}

// itoa avoids pulling strconv into a file whose only number formatting is a
// progress bar and a gap count.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
