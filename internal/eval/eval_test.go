package eval

import (
	"strings"
	"testing"
)

// The fixture is deliberately tiny and hand-checkable: eight items described only by tag
// weights, which is all this package knows about an entity. It is NOT a recommendation
// algorithm and it is not imported by anything -- the point of W0 is that an idea can be
// measured against a fixture before a catalogue exists.
func fixtureCorpus(t *testing.T) *Corpus {
	t.Helper()
	items := []Item{
		{ID: "a", Features: map[string]float64{"action": 1, "outdoor": 1}},
		{ID: "b", Features: map[string]float64{"action": 1, "outdoor": 1}},
		{ID: "c", Features: map[string]float64{"action": 1}},
		{ID: "d", Features: map[string]float64{"outdoor": 1}},
		{ID: "e", Features: map[string]float64{"comedy": 1}},
		{ID: "f", Features: map[string]float64{"action": 1, "comedy": 1}},
		{ID: "h", Features: map[string]float64{"drama": 1}},
	}
	corpus, err := NewCorpus(items...)
	if err != nil {
		t.Fatalf("NewCorpus: %v", err)
	}
	return corpus
}

// sharedTags is the fixture ranker: count the features both items carry. Ties are broken by
// id through RankByScore, so the expected order in the test below is fully determined.
func sharedTags(features ...string) func(seed Item, pool []Item) []ID {
	return func(seed Item, pool []Item) []ID {
		scored := make([]Scored, 0, len(pool))
		for _, item := range pool {
			score := 0.0
			for _, f := range features {
				if seed.Features[f] > 0 && item.Features[f] > 0 {
					score++
				}
			}
			scored = append(scored, Scored{ID: item.ID, Score: score})
		}
		return RankByScore(scored)
	}
}

// seedFixtureCases are the labelled questions. Each relevant id is a neighbour the shared-tag
// ranker should plausibly find; the third case is one it MISSES at k=2, because a fixture
// where everything scores 1.0 would not exercise the metric.
func seedFixtureCases() []Case {
	return []Case{
		{Name: "action-outdoor pair", Seed: "a", Relevant: []ID{"b"}},
		{Name: "single-tag action", Seed: "c", Relevant: []ID{"b", "f"}},
		{Name: "action+comedy neighbours", Seed: "f", Relevant: []ID{"c", "e"}},
		{Name: "comedy only", Seed: "e", Relevant: []ID{"f"}},
	}
}

// TestNeighbourEvalExample is the end-to-end example: corpus -> cases -> ranker -> metrics,
// printed as the Markdown table a person reads. The asserted numbers are computed by hand
// below, so a change in the arithmetic fails here as well as in the metric tests.
//
// k=2, four cases:
//
//	seed a: rank b,c,...            rel {b}    -> recall 1/1,  prec 1/2, rr 1
//	seed c: rank a,b,...            rel {b,f}  -> recall 1/2,  prec 1/2, rr 1/2
//	seed f: rank a,b,c,...          rel {c,e}  -> recall 0,    prec 0,   rr 1/3
//	seed e: rank f,a,...            rel {f}    -> recall 1/1,  prec 1/2, rr 1
//
//	macro recall (1+0.5+0+1)/4 = 0.625
//	macro precision (0.5+0.5+0+0.5)/4 = 0.375
//	macro MRR (1+0.5+0.3333+1)/4 = 0.7083
func TestNeighbourEvalExample(t *testing.T) {
	report, err := Evaluate(seedFixtureCases(), fixtureCorpus(t), Ranker{
		Name: "shared-tags (fixture)",
		Rank: sharedTags("action", "outdoor", "comedy", "drama"),
	}, 2)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	t.Logf("\n%s", report.Markdown())
	for _, c := range report.Cases {
		t.Logf("case %-24s top=%v hits=%d/%d", c.Case, c.Top, c.Hits, c.Total)
	}

	assertClose(t, report.Metrics.Recall, 0.625)
	assertClose(t, report.Metrics.Precision, 0.375)
	assertClose(t, report.Metrics.MRR, (1+0.5+1.0/3.0+1)/4)
	assertClose(t, report.Metrics.Coverage, 1)
	if report.Metrics.Cases != 4 {
		t.Fatalf("cases = %d, want 4", report.Metrics.Cases)
	}

	// The miss is the case that matters: if a metric change makes this hit, the fixture lost
	// its only negative example.
	if got := report.Cases[2]; got.Hits != 0 || got.Recall != 0 {
		t.Fatalf("seed f should miss at k=2, got hits=%d recall=%v", got.Hits, got.Recall)
	}
}

// TestOracleRankerScoresOneIsTheCeiling pins the upper bound: a ranker that puts the
// expected set first must score recall 1 and MRR 1 on every case.
func TestOracleRankerScoresOneIsTheCeiling(t *testing.T) {
	oracle := Ranker{
		Name: "oracle",
		Rank: func(seed Item, pool []Item) []ID {
			want := make(map[ID]struct{})
			for _, c := range seedFixtureCases() {
				if c.Seed == seed.ID {
					for _, id := range c.Relevant {
						want[id] = struct{}{}
					}
				}
			}
			first := make([]ID, 0, len(want))
			rest := make([]ID, 0, len(pool))
			for _, item := range pool {
				if _, ok := want[item.ID]; ok {
					first = append(first, item.ID)
				} else {
					rest = append(rest, item.ID)
				}
			}
			return append(first, rest...)
		},
	}
	report, err := Evaluate(seedFixtureCases(), fixtureCorpus(t), oracle, 2)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	assertClose(t, report.Metrics.Recall, 1)
	assertClose(t, report.Metrics.MRR, 1)
	// Precision is bounded by how many labels each case has: 1/2, 2/2, 2/2, 1/2.
	assertClose(t, report.Metrics.Precision, 0.75)
}

// TestReversedRankerScoresZero is the lower bound: the oracle's exact inverse puts no
// relevant item in the first slot, so recall@1 and precision@1 are zero. MRR is measured
// over the WHOLE ranking, so it is not zero here -- the relevant items are still found,
// later -- and that difference is exactly why the report prints recall, precision and MRR
// as three columns instead of one score.
func TestReversedRankerScoresZero(t *testing.T) {
	forward := sharedTags("action", "outdoor", "comedy", "drama")
	reversed := Ranker{
		Name: "reversed",
		Rank: func(seed Item, pool []Item) []ID {
			ids := forward(seed, pool)
			for i, j := 0, len(ids)-1; i < j; i, j = i+1, j-1 {
				ids[i], ids[j] = ids[j], ids[i]
			}
			return ids
		},
	}
	report, err := Evaluate(seedFixtureCases(), fixtureCorpus(t), reversed, 1)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	assertClose(t, report.Metrics.Recall, 0)
	assertClose(t, report.Metrics.Precision, 0)
	// Relevant ranks in the reversed lists are 6, 4, 3 and 6 -> (1/6+1/4+1/3+1/6)/4 = 11/48.
	assertClose(t, report.Metrics.MRR, 11.0/48.0)
	// It still recommended something, so coverage is 1: zero recall is not the same fact as
	// an abstention, and the report must not conflate them.
	assertClose(t, report.Metrics.Coverage, 1)
}

// TestAbstainingRankerHasZeroCoverage: returning nothing is an abstention. It is not scored
// as a correct answer, it is not excluded from the denominator, and it is visible.
func TestAbstainingRankerHasZeroCoverage(t *testing.T) {
	report, err := Evaluate(seedFixtureCases(), fixtureCorpus(t), Ranker{
		Name: "abstains",
		Rank: func(seed Item, pool []Item) []ID { return nil },
	}, 2)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	assertClose(t, report.Metrics.Coverage, 0)
	assertClose(t, report.Metrics.Recall, 0)
	assertClose(t, report.Metrics.Precision, 0)
	assertClose(t, report.Metrics.MRR, 0)
	for _, c := range report.Cases {
		if !c.Abstained || c.Hits != 0 {
			t.Fatalf("case %q: abstained=%v hits=%d, want true/0", c.Case, c.Abstained, c.Hits)
		}
		if !strings.Contains(report.Markdown(), "abstained") {
			t.Fatal("abstention is not visible in the report")
		}
	}
}

// TestEvaluateRefusesIllFormedInput: every one of these is a bug in a suite, and each would
// corrupt the mean if it were skipped instead of refused.
func TestEvaluateRefusesIllFormedInput(t *testing.T) {
	corpus := fixtureCorpus(t)
	ok := Ranker{Name: "ok", Rank: func(seed Item, pool []Item) []ID { return []ID{"b"} }}

	tests := []struct {
		name    string
		cases   []Case
		ranker  Ranker
		k       int
		wantErr string
	}{
		{"zero k", seedFixtureCases(), ok, 0, "k must be at least 1"},
		{"nil rank fn", seedFixtureCases(), Ranker{Name: "nil"}, 2, "no Rank function"},
		{"seed not in corpus", []Case{{Name: "bad", Seed: "zz", Relevant: []ID{"a"}}}, ok, 2, "not in the corpus"},
		{"no relevant", []Case{{Name: "bad", Seed: "a"}}, ok, 2, "no relevant neighbours"},
		{"label not in corpus", []Case{{Name: "bad", Seed: "a", Relevant: []ID{"zz"}}}, ok, 2, "not in the corpus"},
		{"empty corpus", nil, ok, 2, "nil corpus"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := corpus
			if tc.name == "empty corpus" {
				c = nil
			}
			_, err := Evaluate(tc.cases, c, tc.ranker, tc.k)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestEvaluateRefusesImproperRankings(t *testing.T) {
	corpus := fixtureCorpus(t)
	cases := []Case{{Name: "one", Seed: "a", Relevant: []ID{"b"}}}
	tests := []struct {
		name    string
		ranking []ID
		wantErr string
	}{
		{"returns the seed", []ID{"a"}, "returned the seed"},
		{"invents an id", []ID{"zz"}, "not in the corpus"},
		{"repeats an item", []ID{"b", "b"}, "twice"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Evaluate(cases, corpus, Ranker{
				Name: "bad",
				Rank: func(seed Item, pool []Item) []ID { return tc.ranking },
			}, 2)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestEvaluateWithNoCasesReportsZero(t *testing.T) {
	report, err := Evaluate(nil, fixtureCorpus(t), Ranker{
		Name: "unused",
		Rank: func(seed Item, pool []Item) []ID { return nil },
	}, 10)
	if err != nil {
		t.Fatalf("empty case set must not be an error: %v", err)
	}
	if report.Metrics.Cases != 0 || report.Metrics.Coverage != 0 {
		t.Fatalf("metrics = %+v, want zeros", report.Metrics)
	}
}

func TestCorpusPoolExcludesSeedAndKeepsOrder(t *testing.T) {
	corpus := fixtureCorpus(t)
	pool := corpus.Pool("c")
	if len(pool) != corpus.Len()-1 {
		t.Fatalf("pool length = %d, want %d", len(pool), corpus.Len()-1)
	}
	seen := make(map[ID]bool, len(pool))
	for i, item := range pool {
		if item.ID == "c" {
			t.Fatal("pool contains the seed")
		}
		if seen[item.ID] {
			t.Fatalf("pool repeats %q", item.ID)
		}
		seen[item.ID] = true
		_ = i
	}
	if pool[0].ID != "a" || pool[len(pool)-1].ID != "h" {
		t.Fatalf("pool order = %v .. %v, want corpus order a .. h", pool[0].ID, pool[len(pool)-1].ID)
	}
}

func TestRankByScoreTieBreaksByID(t *testing.T) {
	got := RankByScore([]Scored{
		{ID: "z", Score: 1},
		{ID: "a", Score: 1},
		{ID: "m", Score: 3},
	})
	want := []ID{"m", "a", "z"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("RankByScore = %v, want %v", got, want)
		}
	}
}
