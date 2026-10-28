-- One row per evaluation run (spec: docs/spec/feature-recommendation-eval.md, W0).
--
-- WHY THIS TABLE EXISTS, AND WHAT IT IS NOT. This is a SNAPSHOT of a run -- which suite,
-- which code, which seed, which cutoff, and what came out -- not a cached ranking. A table
-- of scores per entity per algorithm would be a second answer able to drift from the
-- signals that produced it; a run is a fact about a moment and never read by a request
-- path. `internal/eval` does not insert here on its own: recording is an explicit operator
-- act (`--record`), and the git sha is refused when the tree is dirty, because a metric
-- attributed to an unknown tree is not attributable.
--
-- NO FOREIGN KEYS. A run outlives the rows it measured (a user can be deleted, a scene can
-- be merged away) and deleting the history of "what we believed on this date" with them
-- would be the wrong direction. Nothing on a read path joins this table.

CREATE TABLE "eval_runs" (
    "id" UUID NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY,

    -- The suite that produced the numbers: 'pairwise', 'engagement', and whatever W1+
    -- add. Deliberately NOT a CHECK constraint: the set of suites is expected to grow and
    -- a migration is a poor place to freeze it. The harness owns the vocabulary.
    "suite" TEXT NOT NULL,

    -- The exact tree the numbers came from. Read from `git rev-parse HEAD` by the recorder,
    -- never typed.
    "git_sha" TEXT NOT NULL,

    -- Ranker list, k, pool size, feature flags -- everything needed to describe the run
    -- without re-reading the harness source at that sha.
    "config" JSONB NOT NULL DEFAULT '{}'::jsonb,

    -- The RNG seed. Reproducibility is a property of the run, so it is stored beside the
    -- result rather than defaulted here: two rows with one seed are the same draw.
    "seed" BIGINT NOT NULL,

    -- The point in time every label and feature was cut off at. NULLABLE on purpose: the
    -- fixture suites in internal/eval have no clock and would otherwise have to invent one,
    -- and an invented cutoff is worse than an honest NULL. A suite that has a cutoff must
    -- write it.
    "cutoff" TIMESTAMPTZ,

    -- Macro metrics plus per-suite detail (coverage, n, intervals). JSONB because each suite
    -- reports different numbers and a column per metric would need a migration per metric.
    "metrics" JSONB NOT NULL DEFAULT '{}'::jsonb,

    "created_at" TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- The only question asked of this table: "the recent runs of suite X". DESC because a
-- dashboard reads newest first and the oldest rows are never paged through.
CREATE INDEX "eval_runs_suite_created_idx" ON "eval_runs" ("suite", "created_at" DESC);

COMMENT ON TABLE "eval_runs" IS
    'One row per evaluation run: suite, git sha, seed, cutoff and the metrics that came out. A snapshot, never a cached ranking.';
COMMENT ON COLUMN "eval_runs"."suite" IS
    'Which evaluation suite produced the row: pairwise, engagement (W0), later suites.';
COMMENT ON COLUMN "eval_runs"."git_sha" IS
    'git rev-parse HEAD at record time; the recorder refuses a dirty tree.';
COMMENT ON COLUMN "eval_runs"."config" IS
    'Rankers, k, pool size and flags needed to reproduce the run.';
COMMENT ON COLUMN "eval_runs"."seed" IS
    'RNG seed, so two rows with one seed are the same draw.';
COMMENT ON COLUMN "eval_runs"."cutoff" IS
    'Time cutoff for every label and feature; NULL for clock-free fixture suites.';
COMMENT ON COLUMN "eval_runs"."metrics" IS
    'Macro metrics and per-suite detail (n, coverage, intervals).';
