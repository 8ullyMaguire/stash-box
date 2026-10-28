# The evaluation harness (`internal/eval`)

Phase 7b adopted 33 recommendation ideas and could measure none of them: there was no
recall, precision or MRR anywhere in the tree. This package is the measuring instrument. It
takes labelled cases, asks a ranking function for an answer, and reports what it found —
in memory, with no database and no catalogue.

Spec: [`docs/spec/feature-recommendation-eval.md`](spec/feature-recommendation-eval.md), W0.
Migration: `145_eval_runs`.

## What it measures

A **case** names a seed item and the neighbours that count as a correct answer. A **ranker**
is a function that receives the seed and the candidate pool and returns item ids best-first.
`Evaluate` runs one ranker over every case at one cutoff `k` and returns a `Report`.

| Metric | Definition | Denominator |
|---|---|---|
| Recall@k | relevant items in the first k / size of the relevant set | the case's labels |
| Precision@k | relevant items in the first k / k | **k**, not the length of the ranking |
| MRR | mean of 1/rank of the *first* relevant item, over the whole ranking | one per case |
| Coverage | fraction of cases where the ranker returned at least one item | all cases |

Three decisions are worth knowing before reading a number:

- **An abstention is not a win.** Returning nothing (or fewer items) scores zero on all
  three means and lowers `coverage`. Abstentions stay in the denominator; excluding them
  would let a ranker improve its mean by declining the cases it would lose.
- **Precision divides by k.** A ranker that returns two good items and stops has not earned
  the precision of a full list.
- **MRR uses the whole ranking**, not the top k, so it can be non-zero on a run whose
  recall@1 is zero. That is the reason the report prints three columns instead of one score:
  a single number hides which of "did not find it" and "found it late" happened.

The report also prints hits per case (`2/3`, or `abstained`), because a mean over four
cases with one miss and a mean over four cases each half-right are different facts.

## How to add a case

A case needs a corpus (items with features the ranker may use) and at least one label. The
example in `eval_test.go` is the whole shape:

```go
corpus, err := eval.NewCorpus(
    eval.Item{ID: "a", Features: map[string]float64{"action": 1}},
    eval.Item{ID: "b", Features: map[string]float64{"action": 1, "comedy": 1}},
    // ...
)

cases := []eval.Case{
    {Name: "action neighbour", Seed: "a", Relevant: []eval.ID{"b"}},
}

report, err := eval.Evaluate(cases, corpus, eval.Ranker{
    Name: "my-idea",
    Rank: func(seed eval.Item, pool []eval.Item) []eval.ID {
        // score pool items against the seed, then:
        return eval.RankByScore(scored) // ties broken by id, deterministically
    },
}, 10)

fmt.Println(report.Markdown())
```

Rules the harness enforces, each of which is a ranking bug that would corrupt a mean:

- the seed must be in the corpus, and is never offered in its own pool;
- every relevant id must be in the corpus, and a case with no labels is refused;
- a ranker may not return the seed, an id outside the corpus, or the same item twice;
- `k >= 1`.

Ranking order is measured **as returned**. The harness does not re-sort: a ranker's
tie-break is part of what is being measured, and two rankers emitting the same scores in
different orders are different rankers. `RankByScore` exists so fixtures do not each invent
a tie-break.

Run the example, which prints the table above with real numbers:

```bash
go test ./internal/eval/ -run TestNeighbourEvalExample -v
```

## What it deliberately does not do

- **No production data, and no production path.** `internal/eval` reads no database and is
  imported by nothing outside its own tests. That is what keeps the harness from becoming a
  second ranker by accident.
- **No ranker of its own.** The ranking function is supplied by the caller. W0 ships the
  instrument, not an algorithm; the test fixture ranks by shared tag count purely so the
  arithmetic has something to measure.
- **No grades, and therefore no NDCG.** Labels are binary in W0. A grade scale belongs to
  the engagement suite, where the data carries different amounts of evidence.
- **No significance.** No confidence intervals, no bootstrap, no paired deltas. A mean with
  no interval is a description, not a conclusion; those arrive with the database-backed
  suites (W1+).
- **No recording.** `eval_runs` exists and is empty; the `cmd/sbx-eval --record` path that
  would write it — and the git-sha/dirty-tree refusal — is not part of this slice. A default
  run must write nothing.
- **No fixture that looks like a catalogue.** The toy corpus is eight items and four cases.
  It tests the instrument; it does not claim anything about recommendations.
