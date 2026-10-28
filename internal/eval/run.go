package eval

import "fmt"

// Evaluate runs one ranker over every case and returns the report.
//
// It is the whole loop: validate the inputs once, then per case build the pool, ask the
// ranker, check its answer, and measure it. Validation is an error rather than a skip
// because every skip is a case that quietly left the denominator -- a suite that silently
// evaluated three of its eight cases reports a mean that describes a different experiment.
func Evaluate(cases []Case, corpus *Corpus, ranker Ranker, k int) (Report, error) {
	report := Report{Ranker: ranker.Name, K: k}
	if corpus == nil {
		return report, fmt.Errorf("eval: nil corpus")
	}
	if k < 1 {
		return report, fmt.Errorf("eval: k must be at least 1, got %d", k)
	}
	if ranker.Rank == nil {
		return report, fmt.Errorf("eval: ranker %q has no Rank function", ranker.Name)
	}
	// An empty case list is a fact, not an error: it produces a report with n=0 and zero
	// means, which is how a suite with no labels announces itself instead of crashing.
	if len(cases) == 0 {
		return report, nil
	}

	report.Cases = make([]CaseResult, 0, len(cases))
	var sumRecall, sumPrecision, sumRR float64
	var covered int

	for _, c := range cases {
		seed, ok := corpus.Get(c.Seed)
		if !ok {
			return report, fmt.Errorf("eval: case %q: seed %q is not in the corpus", c.Name, c.Seed)
		}
		if len(c.Relevant) == 0 {
			return report, fmt.Errorf("eval: case %q: no relevant neighbours; the case can never be hit", c.Name)
		}
		for _, id := range c.Relevant {
			if _, ok := corpus.Get(id); !ok {
				return report, fmt.Errorf("eval: case %q: relevant neighbour %q is not in the corpus", c.Name, id)
			}
		}

		ranking, err := checkRanking(c, corpus, ranker.Rank(seed, corpus.Pool(c.Seed)))
		if err != nil {
			return report, err
		}

		top := head(ranking, k)
		result := CaseResult{
			Case:           c.Name,
			Seed:           c.Seed,
			Top:            append([]ID(nil), top...),
			Total:          len(idSet(c.Relevant)),
			Abstained:      len(ranking) == 0,
			Recall:         RecallAtK(ranking, c.Relevant, k),
			Precision:      PrecisionAtK(ranking, c.Relevant, k),
			ReciprocalRank: ReciprocalRank(ranking, c.Relevant),
		}
		want := idSet(c.Relevant)
		for _, id := range top {
			if _, ok := want[id]; ok {
				result.Hits++
			}
		}

		if !result.Abstained {
			covered++
		}
		sumRecall += result.Recall
		sumPrecision += result.Precision
		sumRR += result.ReciprocalRank
		report.Cases = append(report.Cases, result)
	}

	n := float64(len(report.Cases))
	report.Metrics = Metrics{
		Cases:     len(report.Cases),
		Coverage:  float64(covered) / n,
		Recall:    sumRecall / n,
		Precision: sumPrecision / n,
		MRR:       sumRR / n,
	}
	return report, nil
}

// checkRanking rejects what a ranker is not allowed to return: an id outside the pool, the
// seed itself, or the same item twice. All three are ranker bugs that would corrupt the
// measurement if accepted -- the first by scoring an item nobody could be shown, the second
// by a free hit on a "find similar" case, the third by filling the top k with one answer.
func checkRanking(c Case, corpus *Corpus, ranking []ID) ([]ID, error) {
	seen := make(map[ID]struct{}, len(ranking))
	for i, id := range ranking {
		if id == c.Seed {
			return nil, fmt.Errorf("eval: case %q: ranker returned the seed %q at position %d", c.Name, id, i+1)
		}
		if _, ok := corpus.Get(id); !ok {
			return nil, fmt.Errorf("eval: case %q: ranker returned %q, which is not in the corpus", c.Name, id)
		}
		if _, dup := seen[id]; dup {
			return nil, fmt.Errorf("eval: case %q: ranker returned %q twice", c.Name, id)
		}
		seen[id] = struct{}{}
	}
	return ranking, nil
}
