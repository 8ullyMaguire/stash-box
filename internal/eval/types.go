// Package eval measures a ranking function against labelled cases, in memory.
//
// WHAT IT IS FOR. Phase 7b of the aggregator plan adopted 33 recommendation ideas that
// nothing could measure. This package is the measuring instrument: a case says "given this
// seed, these neighbours are the right answer", a ranker says what it would put first, and
// the metrics say how often the answer was in the list. A ranker is a function the caller
// supplies, so an idea can be compared against the current behaviour before any surface
// ships -- and without a catalogue, a database, or a request path.
//
// WHAT IT DELIBERATELY DOES NOT DO. It does not rank anything a member sees: no production
// code path imports this package, which is what keeps the first slice from becoming a
// second ranker by accident. It does not read the database, it does not pick the metric,
// and it does not decide that a delta is real -- coverage is reported beside every mean
// because a ranker that abstains on most cases and wins the rest is not better. Confidence
// intervals, bootstrap resampling and the database-backed suites arrive with W1+; a mean
// with no interval is a description, not a conclusion.
package eval

import (
	"fmt"
	"sort"
	"strings"
)

// ID identifies an entity in a corpus. The harness never interprets it beyond equality and
// ordering, so it is the caller's entity id as a string.
type ID string

// Item is one candidate in the corpus.
//
// Features are the attributes a ranker may look at: tag weights, studio membership, a
// popularity count. They are deliberately unstructured -- the harness does not know what a
// tag is, and a ranker that needs typed entities can carry them in the caller's own struct
// and translate to a ranking here.
type Item struct {
	ID       ID
	Features map[string]float64
}

// Corpus is an ordered set of items. Order is the corpus order the caller supplied: it
// matters for a ranker that breaks ties by "first seen", and the harness does not reorder
// it, because a silently sorted pool would hide a ranker that depends on insertion order.
type Corpus struct {
	items []Item
	index map[ID]int
}

// NewCorpus builds a corpus from items, refusing what the metrics could not interpret.
func NewCorpus(items ...Item) (*Corpus, error) {
	c := &Corpus{
		items: make([]Item, 0, len(items)),
		index: make(map[ID]int, len(items)),
	}
	for i, item := range items {
		if item.ID == "" {
			return nil, fmt.Errorf("corpus item %d has an empty id", i)
		}
		if _, dup := c.index[item.ID]; dup {
			return nil, fmt.Errorf("corpus has two items with id %q", item.ID)
		}
		c.index[item.ID] = i
		c.items = append(c.items, item)
	}
	return c, nil
}

// Len is the number of items in the corpus.
func (c *Corpus) Len() int { return len(c.items) }

// Items returns a copy of the corpus in order. Callers get a copy so a ranker cannot
// reorder the corpus for the next case.
func (c *Corpus) Items() []Item {
	out := make([]Item, len(c.items))
	copy(out, c.items)
	return out
}

// Get returns the item with the given id.
func (c *Corpus) Get(id ID) (Item, bool) {
	i, ok := c.index[id]
	if !ok {
		return Item{}, false
	}
	return c.items[i], true
}

// Pool returns every corpus item except the seed, in corpus order. The seed is removed
// because recommending the item a member is already looking at is not a recommendation,
// and letting a ranker return it would inflate every metric by one free hit.
func (c *Corpus) Pool(seed ID) []Item {
	out := make([]Item, 0, len(c.items))
	for _, item := range c.items {
		if item.ID == seed {
			continue
		}
		out = append(out, item)
	}
	return out
}

// Case is one labelled question: for Seed, Relevant is the set of neighbours that count as
// a correct answer.
//
// Relevant is binary in W0. Grades (and therefore NDCG) arrive with the engagement suite,
// where "watched twice" and "did not watch" are different amounts of evidence; inventing a
// grade scale here would be inventing a label the data does not carry yet.
type Case struct {
	// Name is what the report prints. It is not required to be unique.
	Name string
	// Seed is the item the ranker is asked about. It must be in the corpus.
	Seed ID
	// Relevant is the expected answer set. It must be non-empty: a case with no expected
	// neighbours can never be hit, so it would silently drag every mean down.
	Relevant []ID
}

// Scored is one item and the score a ranker gave it.
type Scored struct {
	ID    ID
	Score float64
}

// Ranker is a named ranking function.
//
// Rank receives the seed and the pool (in corpus order) and returns item ids in rank order,
// best first. It may return fewer ids than the pool, including none: returning nothing is
// an ABSTENTION, and it is counted as "no recommendation" rather than as a wrong one. An
// abstention is visible in the report's coverage column, so a ranker cannot improve its
// precision by refusing the cases it would lose.
//
// The harness measures the order as given. It does not re-sort by a score the ranker did
// not return, because a ranker's tie-break is part of what is being measured: two rankers
// that emit the same scores in different orders are different rankers.
type Ranker struct {
	Name string
	Rank func(seed Item, pool []Item) []ID
}

// RankByScore orders scored items best-first, breaking ties by id ascending.
//
// This exists so that a score-based ranker in a test or a suite does not each invent its own
// tie-break; an explicit order is still required, because the harness will not sort for it.
// The tie-break is a fixed rule rather than the input order so the same scores always give
// the same ranking, whatever order the pool happened to arrive in.
func RankByScore(scored []Scored) []ID {
	ordered := make([]Scored, len(scored))
	copy(ordered, scored)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Score != ordered[j].Score {
			return ordered[i].Score > ordered[j].Score
		}
		return ordered[i].ID < ordered[j].ID
	})
	out := make([]ID, len(ordered))
	for i, s := range ordered {
		out[i] = s.ID
	}
	return out
}

// Metrics are the macro averages over a report's cases.
//
// Abstentions count as zero in all three means and are disclosed by Coverage: excluding
// them from the denominator would let a ranker improve a mean by declining the hard cases.
type Metrics struct {
	// Cases is the number of cases evaluated.
	Cases int
	// Coverage is the fraction of cases where the ranker returned at least one item.
	Coverage float64
	// Recall is the mean fraction of each case's relevant set that appeared in the top k.
	Recall float64
	// Precision is the mean fraction of the top k slots that were relevant.
	Precision float64
	// MRR is the mean reciprocal rank of the first relevant item (0 when absent).
	MRR float64
}

// CaseResult is one case, measured.
type CaseResult struct {
	Case  string
	Seed  ID
	Top   []ID // the ranking truncated to k, in rank order
	Hits  int  // relevant items in Top
	Total int  // size of the case's relevant set

	Recall    float64
	Precision float64
	// ReciprocalRank is 1/rank of the first relevant item, 0 if none was in the ranking.
	ReciprocalRank float64
	// Abstained is true when the ranker returned no items at all.
	Abstained bool
}

// Report is the outcome of one ranker over one case set at one k.
type Report struct {
	Ranker  string
	K       int
	Metrics Metrics
	Cases   []CaseResult
}

// Markdown renders the report as the table a person reads. It is the format the harness
// prints by default: every row carries n, coverage and the metric, so a single number is
// never read without the evidence behind it.
func (r Report) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "## ranker: %s (k=%d)\n\n", r.Ranker, r.K)
	b.WriteString("| case | hits | recall | precision | rr |\n")
	b.WriteString("|---|---|---|---|---|\n")
	for _, c := range r.Cases {
		hits := fmt.Sprintf("%d/%d", c.Hits, c.Total)
		if c.Abstained {
			hits = "abstained"
		}
		fmt.Fprintf(&b, "| %s | %s | %.3f | %.3f | %.3f |\n",
			escapeCell(c.Case), hits, c.Recall, c.Precision, c.ReciprocalRank)
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "**n=%d  coverage=%.3f  recall@%d=%.3f  precision@%d=%.3f  mrr=%.3f**\n",
		r.Metrics.Cases, r.Metrics.Coverage, r.K, r.Metrics.Recall,
		r.K, r.Metrics.Precision, r.Metrics.MRR)
	return b.String()
}

// escapeCell keeps a case name from breaking the table it is printed in.
func escapeCell(s string) string {
	return strings.ReplaceAll(s, "|", `\|`)
}
