package eval

// The metric functions are exported and pure so that a suite can score a ranking it built
// elsewhere, and so that the arithmetic can be tested against hand-computed numbers rather
// than against the harness's own output.

// RecallAtK is the fraction of relevant items that appear in the first k of ranking.
//
// The denominator is the size of the relevant set, not k: recall asks "how much of the
// answer did it find", and a ranker is not penalised for a short relevant set.
func RecallAtK(ranking []ID, relevant []ID, k int) float64 {
	want := idSet(relevant)
	if len(want) == 0 || k <= 0 {
		return 0
	}
	hits := 0
	seen := make(map[ID]struct{}, k)
	for _, id := range head(ranking, k) {
		if _, ok := want[id]; !ok {
			continue
		}
		// A ranking is a list of distinct answers; a repeated id is one hit, not two.
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		hits++
	}
	return float64(hits) / float64(len(want))
}

// PrecisionAtK is the fraction of the first k slots that hold a relevant item.
//
// The denominator is k, not the length of the ranking: a ranker that returns two good items
// and stops has not earned the precision of a full list, and treating a short list as a
// full one is how abstention becomes a strategy (see Coverage for the honest reading of it).
func PrecisionAtK(ranking []ID, relevant []ID, k int) float64 {
	want := idSet(relevant)
	if len(want) == 0 || k <= 0 {
		return 0
	}
	hits := 0
	seen := make(map[ID]struct{}, k)
	for _, id := range head(ranking, k) {
		if _, ok := want[id]; !ok {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		hits++
	}
	return float64(hits) / float64(k)
}

// ReciprocalRank is 1/rank of the first relevant item, or 0 if the ranking holds none.
// Only the first hit counts: a list that finds the answer seventh is worse than one that
// finds it first, and every later hit is discarded.
func ReciprocalRank(ranking []ID, relevant []ID) float64 {
	want := idSet(relevant)
	if len(want) == 0 {
		return 0
	}
	for i, id := range ranking {
		if _, ok := want[id]; ok {
			return 1 / float64(i+1)
		}
	}
	return 0
}

// head returns at most the first k ids of ranking.
func head(ranking []ID, k int) []ID {
	if k >= len(ranking) {
		return ranking
	}
	if k < 0 {
		return nil
	}
	return ranking[:k]
}

// idSet turns a label list into a set. Duplicate labels collapse: "relevant twice" is not
// two hits, and a caller that repeats an id has made a labelling mistake, not a stronger
// claim.
func idSet(ids []ID) map[ID]struct{} {
	out := make(map[ID]struct{}, len(ids))
	for _, id := range ids {
		out[id] = struct{}{}
	}
	return out
}
