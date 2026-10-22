# SPEC — recommendation surfaces and their evaluation

Status: **written 2026-10-22, before implementation.** Source: a ranked 100-idea list
for a recommendation surface plus its evaluation loop (the "Kindred" list), which was
written for an AO3-shaped archive. This document adapts the ideas that fit **this**
archive, refuses the ones whose premise does not exist here, and puts the evaluation
loop first because every surface below is otherwise an assertion.

Normative siblings: `docs/spec/feature-discovery.md` (phase 7 — the search language,
saved searches, facets, freshness, similarity, For You, serendipity, social discovery,
session state: all built), `docs/spec/feature-tag-ontology.md` (the graph the tag
surfaces read), and `docs/plan/feature-content-aggregator.md` (the programme this is
phase 7b of).

---

## 1. The headline finding

The list's highest-scoring item is *"an arena A/B framework"*, scored 10.0 on the
assumption that the arena already exists and only needs a wrapper. Here it does not
exist in that form, and the data that would evaluate it does. That inversion decides
the order of this phase: **the labelled data is already being written every day; the
thing that reads it does not exist at all.**

Measured against the tree and `sbx-scratch`, 2026-10-22:

| Claim | Measured |
|---|---|
| Discovery backend is built | Phase 7's seven slices are DONE: `queryScenes` language, saved searches + notifications, facets, `FRESH`/`RISING`, `similarScenes`, `forYou`, activity feed, session state. |
| Discovery surfaces are reachable | **No.** `grep -rn 'forYou\|similarScenes\|similarPerformers' frontend/src --include='*.tsx' --include='*.gql'` returns **0**. Every discovery surface is API-only. |
| There is an evaluation harness | **No.** `grep -rni 'ndcg\|recall@\|AUC' --include='*.go'` returns 5 hits, all `CAUCASIAN` in generated enums/import mapping. No eval package, no eval command, no metric test. |
| There are human preference labels | **Yes, append-only.** `elo_votes (user_id, winner_id, loser_id, winner_type, loser_type, picked_side, weight, created_at)` — one row per matchup. `winner_id`/`loser_id` are the **display slots**, not the outcome; the outcome is `picked_side`. |
| The arena covers entity kinds | **No.** `EloEntityType` accepts performer/scene/studio/site/tag/list/instance/content_item, but `QueryMatchupCandidates` joins `performers` (`internal/queries/sql/elo_matchup.sql:31`) and `EloMatchup.left/right` are `Performer` (`graphql/schema/types/elo.graphql`). The ratings and leaderboard are entity-kind-aware; the **matchup is performer-only end to end**. |
| The leaderboard names non-performers | **No.** `queryResolver.EloLeaderboard` populates `entry.Performer` only when `kind == elo.EntityPerformer` (`internal/api/resolver_elo.go:110`). A scene leaderboard returns ids and numbers with no names. |
| Tag similarity exists | **No.** No `relatedTags`, `TagFacet` covers result-set facets only, and `tag_implications` is the ontology edge — there is no tag-to-tag similarity surface. |
| Embeddings exist | **No producer.** `similar_vector_store` defaults to `noop`; phase 7 §8.5 records that nothing turns a scene into a vector. `pgvector` is not installed (phase 7 §2), so a `vector` column is not available here. |
| Impressions/views exist | **No.** There is no view table and no impression log; phase 7 §9.5 substituted "already engaged" for "already viewed". |
| Sessions exist | **No anonymous sessions.** `session_states` stores opaque client cursors, not identity or history. Every signal is `user_id`-scoped. |

### 1.1 What the list's vocabulary maps to here

| List term | This archive |
|---|---|
| work / fic | `scenes` (and `content_items`) |
| author | `performers` (also `studios`, `sites`) |
| fandom | a root `tag` (or a `studio`); there is no separate fandom entity |
| kudos / hits / bookmarks | `scene_fingerprints.vote = 1` (ownership), `list_items`, published `reviews` (rating nullable), `performer_favorites`, `studio_favorites` |
| arena judgement | `elo_votes` — Glicko-2, trust-weighted, with `weight` snapshotted at cast time |
| leaderboard | `eloLeaderboard` (public page), plus the contribution `leaderboard` |
| CTR / skipped / seen | **nothing** — no impression stream |

The substitution is not cosmetic. "Readers who bookmarked this also bookmarked" becomes
"readers who fingerprinted or listed this also did X", and "not interested" has no
producer until a preference UI exists (phase 16). Every idea that needs a click, a view
or a session is therefore either refused or re-based on a signal that does exist.

### 1.2 The two taste models already exist, and nobody has compared them

- **`taste_vectors`** (migration 77): one row per user, JSONB of **entity ids** with
  `+1` per winning slot and `−1` per losing slot, rebuilt from the whole vote log on
  every vote (`elo.rebuildTasteVector`). Item-based; no tag keys; no GraphQL field.
- **`forYou` affinity** (phase 7 §9): tag weights derived at read time from fingerprint
  votes, list adds and published review ratings, with hierarchy propagation at 0.4 per
  hop. Tag-based; never stored; exposed as `ForYouScene.contributions`.

They answer the same question with different features and different data, and the fork
has never measured either. This spec treats them as **competing rankers, not layers**:
E1/E2 compare them, and no surface is allowed to present one as "the" taste profile
until one wins on held-out data. This also revises the phase 5 plan's assumption that a
`recommendations` table would hold "popular / cf / blended" side by side; that plan's
Step 1 now carries the status note and points here.

### 1.3 The list's "offline north star" is already possible, and is the place to start

`elo_votes` is a stream of **human pairwise labels with timestamps**. That is a better
offline dataset than held-out bookmarks: no negative sampling assumptions, no exposure
bias inside a list, and a label per row. It evaluates *performer* ranking directly, and
it is the only labelled data for that entity kind. Scene ranking has no such labels
today because the matchup never offers scenes; it gets held-out engagement (E2) offline
and the ranking duel (E3) online.

---

## 2. The evaluation triad, adapted

### 2.1 E1 — offline pairwise replay (`internal/eval/pairwise.go`)

**Question:** given the votes cast before time *T*, does ranker *R* predict which side a
person chose after *T*?

**Why a replay and not the stored rating:** `elo_ratings` is a cache of the *latest*
state. Evaluating a test vote against it is leakage — the rating already contains the
answer. E1 replays every vote in `(created_at, id)` order up to a global cutoff, applies
the same Glicko-2 update the service applies (including trust `weight` and elapsed-day
growth), and evaluates only votes after the cutoff. Deterministic ordering is a contract:
two runs over the same rows must produce identical ratings.

**Labels.** For a row, the chosen entity is `picked_side = 0 ? winner_id : loser_id`; the
other is the loser. The displayed left slot is `winner_id` and the right is `loser_id`
(the columns' names are historical — `internal/service/elo/models.go` says so).

**Split.** Global time cutoff at the 80th percentile of vote timestamps, with a minimum
of 20 test votes; per-user cutoffs are reported as a second, stricter view (they remove
users whose votes are all in one burst). Votes naming a deleted or missing entity are
excluded and counted; the report always prints `excluded`.

**Baselines, mandatory and always printed:**

| id | Ranker | What it proves |
|---|---|---|
| `random` | seeded coin flip | the metric is not just the class balance |
| `always-left` | predict the left slot | position bias is measured, not assumed |
| `popularity` | vote count before the cutoff | the "more votes = better" null |
| `glicko-replay` | replayed rating, higher wins | the shipped rating is better than popularity |
| `content` | tag/performer/studio overlap, same weights as `similarScenes` | content beats nothing |
| `affinity` | `forYou` tag affinity from signals strictly before the cutoff | the shipped feed beats its baseline — **scene votes only**; a performer pair has no tag affinity, so the cell says `n/a` rather than inventing one |
| `cf` | item-item cosine over pre-cutoff vote co-occurrence | the classic recsys baseline |
| `taste-vector` | `taste_vectors`-style ± entity counts, rebuilt to the cutoff | the stored model earns its place |

A ranker that has no definition for an entity kind is printed as `n/a`, not silently
skipped: the row is part of the report even when it does not apply, because a missing row
looks like a win by omission. The only type the shipped UI and the seed script produce is
`performer` (`scripts/seed-elo.sql`; `QueryMatchupCandidates` joins `performers`), so the
scene rows will be empty until the ranking duel (E3) produces labels, and the report says
"no labels" for them.

Ties are **abstentions**: a ranker that cannot score one side is not credited with a
win. `coverage` is a first-class column, because a ranker that abstains on 90% of pairs
and wins the remaining 10% is not better.

**Metrics:** accuracy, AUC over score differences, tie rate, coverage, and a
cluster-bootstrap 95% interval resampled **by user** (not by vote: one enthusiastic
voter must not narrow the interval). `log-loss` is printed only for rankers that emit a
calibrated probability (`glicko` does; the content rankers do not, and inventing one
would be a fake).

**Verification:** `go test ./internal/eval/ -count=1 -v` includes a toy dataset where an
oracle scores accuracy 1.0, the reversed ranker 0.0, random is within 0.35–0.65 over
10,000 seeded draws, and an abstaining ranker has coverage 0 and is not counted as
correct. A leakage test asserts that adding a held-out vote to the training set changes
no metric.

### 2.2 E2 — offline held-out engagement (`internal/eval/engagement.go`)

**Question:** for a user with a history, does ranker *R* put the scenes they engage with
next near the top of a reproducible candidate pool?

**Labels.** Per user, deduplicated by `(user, scene)` and ordered by `created_at`:
`scene_fingerprints.vote = 1`, `list_items` with `entity_type = 'scene'`, and published
`reviews` with `rating >= 4`. A published review with `rating <= 2` is a **negative**
label and is reported separately; an unrated review is not a label (phase 7 §7.2's rule,
applied to evaluation).

**Pool.** All non-deleted scenes that existed before the user's cutoff (arrival
`created_at`, not release `date` — phase 7 §9.3 found that release-date ordering silently
drops undated scenes). The pool is the 2,000 most recently arrived scenes plus the
user's held-out positives, so a positive is never unrankable. Negatives are sampled
seeded and recorded in the run manifest; the pool is the same for every ranker, which is
what makes the comparison fair.

**Metrics:** macro-averaged Recall@10, NDCG@10, MRR, coverage; leave-one-out
"similar-to-last-engaged" as a baseline; popularity (fingerprint count before cutoff)
and freshness (arrival order) as the nulls. Paired bootstrap over **users**, 1,000
resamples, seeded; a ranker is only "better" when the paired delta's 95% interval
excludes zero. Single-number wins are not conclusions.

**Leakage guard:** every feature is computed from rows strictly before the cutoff, and
a test adds one held-out signal and asserts every score is unchanged.

### 2.3 E3 — the ranking duel (`ranking_duels`, migration 146)

**Question:** when two rankers each pick a scene for the same person from the same pool,
which pick does the person prefer?

This is the honest version of the list's "arena A/B framework". The entity arena cannot
be reused: an `elo_votes` row moves a Glicko rating, and a vote between two rankers'
picks is not a vote about either scene's quality. Contaminating `elo_ratings` with
evaluation data would corrupt the leaderboard it is supposed to measure.

```sql
CREATE TABLE ranking_duels (
  id uuid PRIMARY KEY,
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  surface text NOT NULL CHECK (surface IN ('for_you', 'similar_scenes')),
  context jsonb NOT NULL DEFAULT '{}'::jsonb,
  left_scene_id uuid NOT NULL REFERENCES scenes(id) ON DELETE CASCADE,
  right_scene_id uuid NOT NULL REFERENCES scenes(id) ON DELETE CASCADE,
  left_ranker text NOT NULL,
  right_ranker text NOT NULL,
  left_score double precision,
  right_score double precision,
  picked_side smallint CHECK (picked_side IN (0, 1)),
  created_at timestamptz NOT NULL DEFAULT now(),
  answered_at timestamptz,
  CONSTRAINT ranking_duels_distinct_scenes CHECK (left_scene_id <> right_scene_id),
  CONSTRAINT ranking_duels_distinct_rankers CHECK (left_ranker <> right_ranker),
  -- An answer and its timestamp are the same fact; one without the other is a bug.
  CONSTRAINT ranking_duels_answer_complete
    CHECK ((picked_side IS NULL) = (answered_at IS NULL))
);
CREATE INDEX ranking_duels_user_idx ON ranking_duels (user_id, answered_at);
CREATE INDEX ranking_duels_surface_idx ON ranking_duels (surface, created_at DESC);
```

**Offer.** `rankingDuel(surface: RankingSurface!): RankingDuelOffer` builds both
rankers' top `N` for the caller, samples one item from each list with a seeded RNG (not
necessarily #1 — always taking #1 measures the head only), excludes the caller's already
engaged scenes and duplicates, randomizes which ranker gets the left slot, and inserts
the offer row with `answered_at` NULL. An unanswered offer is not a datum; the analysis
filters `answered_at IS NOT NULL`.

**Answer.** `voteRankingDuel(id: ID!, pickedSide: Int!)` updates one row where
`user_id = caller AND answered_at IS NULL`, and returns it. A second answer changes
nothing. There is no `elo` call anywhere in this path, and a test asserts the row counts
of `elo_votes` and `elo_ratings` are unchanged after a duel.

**Why one table and not a signed token:** a token would avoid storing offers, but it
needs a server secret, an expiry policy and a canonical encoding, and a replay is then
undetectable. A row is inspectable, answerable once, and deletable with the user. Offers
older than 24 hours are deleted by a `cleanRankingDuels` job registered beside the
existing `cleanDrafts`/`cleanNotifications` jobs in `internal/cron/cron.go` — the shape
this repo already uses for expiring rows. The delete count is logged, not silently swept.

**Analysis.** Win rate per ranker pair, with a Wilson interval; sliced by `surface` and
by `context` (studio/tag). A pair needs **30 answered duels** before its rate is printed
with a conclusion; below that the report says "not enough". No automatic switching: the
report is an input to a person, and the default ranker changes only in a commit that
names the run.

### 2.4 What is not built: interleaving, and why

The list ranks interleaved ranking (#4) second. It is refused **for now**, not because it
is a bad idea but because it requires an impression log — what was shown, to whom, in
which position, at which time — and this fork has deliberately refused a view/impression
table (phase 7 §9.5). Adding one would turn every read into a tracking write, and the
existing privacy posture is that the archive records what a member *did*, not what it
showed them.

The duel is the substitute: explicit, sparse, and opt-in by the act of answering. The
revisit condition is stated rather than left implicit: if duel volume stays below the
30-answer floor per ranker pair for a month, the honest options are (a) accept a slower
loop or (b) add an impressions table with its own retention and disclosure, and that is
the owner's call, not a quiet schema addition.

### 2.5 What this evaluation cannot settle

- **A preference is not a quality.** `elo_votes` says who a person picked, not who is
  better; E1 measures agreement with the picker, and a ranker that predicts a biased
  population better is not a better recommender.
- **The population is self-selected.** People who vote in matchups are not the people
  who browse; the report prints the voter count and the abstention rate beside every
  result.
- **Coverage beats accuracy in the small.** On a young instance the intervals will be
  wide and most deltas will include zero. The harness must print that, not hide it.
- **No counterfactual.** Nothing here answers "would they have found it anyway"; that
  needs the impressions the fork does not keep.

---

## 3. The workstreams

Ordered by dependency and by what can be verified without guessing. Each is one or more
commits; each has a verification command whose output belongs in the commit message.

### W0 — the evaluation harness (first, and alone)

`internal/eval/` (`types.go`, `replay.go`, `pairwise.go`, `engagement.go`, `rankers.go`,
`metrics.go`), `cmd/sbx-eval/main.go`, `scripts/eval-scratch.sh`, migration **145
`eval_runs`** (`id, suite, git_sha, config jsonb, seed bigint, cutoff timestamptz,
metrics jsonb, created_at`; index `(suite, created_at DESC)`). Migration numbers are the
next free pair at the time of writing (144 is the highest applied); renumber above the
highest version actually applied when building, per the append-only rule.

- **No production code path imports `internal/eval`.** It reads the database and prints;
  it does not rank anything a member sees. That is what keeps the first slice from
  becoming a second ranker by accident.
- **`--record` is explicit.** A default run writes nothing; CI runs default. Recording a
  run is an operator act, and `git_sha` is read from `git rev-parse HEAD` and refused if
  the tree is dirty (a metric attributed to a dirty tree is not attributable).
- **The report is Markdown + JSON**, because the Markdown is read by a person and the
  JSON is read by W7.

**Verification:**

```bash
go test ./internal/eval/ -count=1 -v
go run ./cmd/sbx-eval --db "$POSTGRES_DB" --suite pairwise --seed 1 --format md
go run ./cmd/sbx-eval --db "$POSTGRES_DB" --suite engagement --seed 1 --format md
```

Expected: the toy tests above; then a table whose every row carries `n`, `coverage`,
the metric and its interval, with `random`, `always-left` and `popularity` present. On an
empty database the run exits 0 and prints "no labels" — a suite with no data is a fact,
not an error.

**Mutation-checked** by `scripts/mutate-eval.py`: a constant AUC, a reversed tie-break,
coverage counting abstentions as correct, a train/test split that leaks one held-out
row, and a bootstrap that resamples votes instead of users each redden exactly the test
that names the behaviour and restore green.

### W1 — surfaces over what already exists

The cheapest work in the phase, and the one that makes the labels flow:

- `frontend/src/pages/scenes/Scene.tsx` + `frontend/src/pages/scenes/components/SimilarScenes.tsx`
  + `frontend/src/graphql/queries/SimilarScenes.gql`: a **Similar** tab over
  `Scene.similarScenes`, rendering the reasons (shared tag / kind-of / performer / studio
  / runtime) and a "more like this" link that lands on the tab.
- `frontend/src/pages/performers/Performer.tsx` + `components/SimilarPerformers.tsx` +
  `SimilarPerformers.gql`: the existing co-occurrence list with its `sharedScenes`
  evidence.
- `graphql/schema/types/elo.graphql` + `internal/api/resolver_elo.go`: every
  `EloLeaderboardEntry` gains nullable `scene`, `studio`, `site`, `tag`, `list` and
  `content_item` fields, loaded by kind; `EloLeaderboardPage.tsx` gains a kind selector
  and renders `deviation` as a "needs more votes" badge when it is above 100. The number
  is already in the schema; only the page ignored it.
- `frontend/src/pages/curation/MatchupVote.tsx`: a speed round — ten pairs on one page,
  `1` / `2` / `0` (skip), one request per answer. This multiplies the only explicit
  label stream the archive has, which is why it sits in the evaluation phase rather than
  a UI phase.
- `eloMatchup(entityType, tagId, studioId)` + `QueryMatchupCandidates` scope filters
  (`EXISTS` against `scene_tags` / `scene_performers`). A **scoped leaderboard** is
  refused: Glicko stores one rating per entity, so a per-studio board would need a
  second rating table and a second meaning of "the rating".

**Verification:** `go test ./internal/api/ -run 'EloLeaderboard|MatchupScope'`; vitest
tests asserting each new panel's own heading and one row; `node
node_modules/vite/bin/vite.js build`; `/leaderboard` and `/curation/matchup` added to
`e2e/tests/a11y.spec.ts`'s `SWEEP_ROUTES`. A route that returns 200 is not evidence —
`internal/api/routes_frontend.go` serves `index.html` for every path, so a status code is
indistinguishable from a blank page (`docs/spec/growth-tiers-1-2.md` §D1).

### W2 — the ranking duel (online labels for scene ranking)

Migration **146**, the service in `internal/service/rankingduel/`, the two GraphQL
operations, the UI at `frontend/src/pages/curation/RankingDuel.tsx`, and the analysis in
`cmd/sbx-eval --suite duels`.

**Verification:** integration tests (offer sides differ and are scoped; answering once
persists; answering twice is a no-op; another user's id is refused; `elo_votes` and
`elo_ratings` counts unchanged; an unanswered offer is excluded from analysis); vitest
renders the duel and both keyboard paths; the a11y sweep includes the route.

### W3 — tag discovery

`Tag.relatedTags(limit: Int = 10, minCount: Int = 2): [TagRelation!]!` — derived from
`scene_tags` (co-occurrence count, Jaccard, a quality weight from `scene_freshness`),
plus the ontology edges tagged with their kind (`co_occurs`, `implies`, `implied_by`).
`trendingTags(limit, days)` — tag frequency in the recent window against the prior
window, smoothed, with a minimum count. `Tag.trend` on the tag page. The tag profile
page reads these plus `scene_count` and the facets already built.

**Why a `TagRelation` carries its kind:** "these two tags appear together" and "one
implies the other" are different claims, and a UI that renders them as one list teaches a
reader that the ontology is a popularity contest. The kind is what makes the list
explainable.

**Verification:** an integration fixture where A and B co-occur three times, A implies C,
and D co-occurs once (below `minCount`): counts 3/1, Jaccard computed by hand, kinds
separate, self and deleted tags absent, and every count equal to `queryScenes` filtered
by that tag under `IncludesAll` (the facet's rule, phase 7 §6.1). Mutation-check the
closure direction, the Jaccard denominator, the `minCount` filter and the self-exclusion.
The tag page (`frontend/src/pages/tags/Tag.tsx`) gets the related-tags panel here, with a
vitest render test, because this is where the field exists.

### W4 — performer similarity, second signal

`Performer.relatedPerformers(limit: Int = 10): [RelatedPerformer!]!` — a new type, not a
widening of `SimilarPerformer`:

```graphql
type RelatedPerformer {
  performer: Performer!
  score: Float!
  reasons: [PerformerSimilarityReason!]!
}
type PerformerSimilarityReason {
  kind: PerformerSimilarityReasonKind!   # co_occurrence | tag_overlap | co_favorite
  weight: Float!
  # The checkable fact: shared scenes, the shared tag names, or the readers who
  # favourited both. A reason without its fact is a number with a story.
  detail: String!
}
```

The existing `SimilarPerformer` is not changed: its `minShared` contract is about shared
scenes, and widening it would silently change a shipped surface.

**The tag profile is derived, not stored.** The schema has no `performer_tags` table and
no performer→studio column (`graphql/schema/types/performer_similar.graphql` says so),
but `scene_performers → scene_tags` is enough: a performer's profile is the normalized
sum of the tags of the scenes they appear in. That derivation is the feature; a
materialized profile would be stale after the next scene edit.

**Verification:** cosine unit tests; an integration fixture with two performers who share
no scene but many tags — `relatedPerformers` returns them, `similarPerformers` does not
(the no-regression assertion); the score reconstructs from the reasons; E1 gains a
`content-v2` ranker (the W0 `content` ranker stays fixed, so the comparison is
like-for-like) and the run is recorded.

### W5 — scene co-engagement

`Scene.coEngagedScenes(limit: Int = 10, minSupport: Int = 2): [CoEngagedScene!]!` —
readers who fingerprinted, listed or reviewed the subject also did X. Three bounded arms
merged in Go, the phase 7 §10.2 pattern, because sqlc cannot resolve the `UNION` arms
with their own `ORDER BY`/`LIMIT` (measured there). Each row carries `support` (distinct
users), the signal kinds that produced it, and an exclusion of the viewer's own engaged
scenes when there is a viewer.

**Why not one join:** `scene_fingerprints` can hold several rows per (user, scene) for
different hashes, so the support count must be `COUNT(DISTINCT user_id)` per scene, and
`list_items` is polymorphic. A single statement would either double-count or need a
`CASE` that hides nullability from sqlc — the exact trap phase 7 §10.3 recorded.

**Verification:** a fixture with users U1/U2 on A and B, U1 on A and C, U3 on C only:
B has support 2, C has support 1 and is dropped at `minSupport: 2`, the subject is never
returned, and the viewer's own engagements are excluded. Mutation-check the distinct
count, the support floor, the exclusion and the per-arm limit.

### W6 — the ranker lab (nothing ships by default)

Each candidate is a flag, evaluated on E1 or E2, and promoted only when its paired
bootstrap interval excludes zero:

| Candidate | Idea | Where |
|---|---|---|
| recent-window affinity | 6 | `forYou` signal window parameter, derived |
| IDF tag weights | 56 | affinity and tag similarity |
| MMR diversity | 22 | post-rank rerank on tag Jaccard |
| reciprocal-rank fusion | 24 | fusion of rating/affinity/content |
| item-item CF over votes | 89 | E1/E2 ranker |
| shrunk Glicko | 10 | leaderboard ordering candidate (display only) |
| Bradley–Terry vs Glicko | 53 | E1 ranker; rank correlation reported |

The default ranker does not change in this workstream. A candidate that wins in the lab
gets its own small spec note and a commit that names the run id.

### W7 — the dashboard

`Query.evalRuns(limit: Int = 20): [EvalRun!]!` (`@hasRole(ADMIN)`) over migration 145,
and `frontend/src/pages/metrics/` gains a **Ranking quality** section: latest runs, the
metric table, coverage, intra-list diversity, and the per-kind / per-studio breakdown
stored in `metrics jsonb`. Read-only; it computes nothing.

**Verification:** an integration test inserts two runs and asserts newest-first and the
admin gate; a vitest render test asserts the table shows a run's metrics; the a11y sweep
includes the route.

---

## 4. The 100 ideas, disposed

Every id appears exactly once. `BUILT` means the tree already does it (or a composition
of existing inputs does); `ADOPTED` names the workstream; `DEFERRED` names the condition;
`REFUSED` names the premise that is absent.

| # | Idea (short) | Disposition |
|---|---|---|
| 1 | Arena A/B for algorithms | ADOPTED W2 — as ranking duels; the entity arena cannot carry it (§2.3) |
| 2 | Tag-page similar tags | ADOPTED W3 |
| 3 | Author recs by tag cosine | ADOPTED W4 — performers |
| 4 | Interleaved ranking | DEFERRED — needs an impressions table (§2.4) |
| 5 | Co-bookmarked works | ADOPTED W5 — co-engagement |
| 6 | Session taste vector | ADOPTED W6 — recent-window affinity; no sessions exist |
| 7 | Fandom-scoped arena | ADOPTED W1 — tag/studio-scoped matchup; scoped leaderboard refused |
| 8 | "Why this author" evidence | ADOPTED W4 |
| 9 | Offline held-out evaluation | ADOPTED W0 — plus pairwise replay (E1) |
| 10 | Popularity-dampened sort | ADOPTED W6 — shrunk Glicko, display only |
| 11 | Tag embedding neighbours | REFUSED — no embedding producer |
| 12 | Character co-occurrence | BUILT (partial) — `similarPerformers`; no relationship entity to find "emerging ships" |
| 13 | Author leaderboard | ADOPTED W1 — kind selector + names |
| 14 | "More like this" | ADOPTED W1 |
| 15 | Tags you'd like from history | DEFERRED — a tag-affinity profile is a privacy/UI decision; the ranker is measured in W0 first (§8) |
| 16 | Evaluation dashboard | ADOPTED W7 |
| 17 | Tag relation types | ADOPTED W3 — complements and implications; "rivals" refused (no defensible definition) |
| 18 | "Not interested" | DEFERRED — phase 16 preference UI; no block store |
| 19 | Fandom similarity matrix | ADOPTED W3 — tag/studio similarity |
| 20 | Glicko confidence intervals | ADOPTED W1 — deviation is already in the schema |
| 21 | Tag PageRank | REFUSED — no graph-ML infrastructure; curated implications are the hierarchy |
| 22 | MMR diversity | ADOPTED W6 |
| 23 | Surprise within fandom | BUILT — `forYou(serendipity: 1, query: "tag:x")` |
| 24 | Reciprocal rank fusion | ADOPTED W0/W6 — an evaluated ranker |
| 25 | Author tag diversity | ADOPTED W4 |
| 26 | Work freshness | BUILT — `FRESH`/`RISING` (phase 7 §7) |
| 27 | Tag-work relevance arena | REFUSED — no consumer; facets already learn this from queries |
| 28 | Hidden gems | REFUSED — no quality signal independent of popularity |
| 29 | Embedding work similarity | REFUSED — no embeddings |
| 30 | Reader cohort clustering | REFUSED — no sessions and no ML stack |
| 31 | Tag trends | ADOPTED W3 |
| 32 | Completion-aware ranking | REFUSED — scenes have no completion field; completion is metadata coverage |
| 33 | Multi-seed intersection | DEFERRED — `similar_to:` has no criterion yet |
| 34 | "Works this tag needs" | DEFERRED — writer-facing; no writer surface |
| 35 | Author co-readership | ADOPTED W4 — co-favorite evidence |
| 36 | Taste vector export | DEFERRED — a portability/privacy surface; owner call |
| 37 | Tag hierarchy from clusters | REFUSED — hierarchy is curated via implications |
| 38 | Quality-adjusted co-occurrence | ADOPTED W3 |
| 39 | Bayesian tag quality | ADOPTED W6 — tag ratings + shrinkage |
| 40 | Ordered reading path | DEFERRED — no session model |
| 41 | Length binning | BUILT — `duration` criterion + runtime weight in `similarScenes` |
| 42 | Calibrated confidence | DEFERRED — W0 coverage first; no calibrated model |
| 43 | Anti-recommendations | REFUSED — no product need; wastes a page |
| 44 | Seasonal context | DEFERRED — seasonal events exist; a tag→month mapping does not |
| 45 | Tag canonical form | BUILT — aliases + merge redirects (phase 7 §4) |
| 46 | Per-fandom quality tracking | ADOPTED W7 |
| 47 | Work cluster labels | REFUSED — no embeddings |
| 48 | Reader velocity | DEFERRED — no sessions |
| 49 | Tag polarity | DEFERRED — no block/preference store |
| 50 | Complete the collection | REFUSED — lists are queryable; no "collection" entity |
| 51 | Series-aware recs | REFUSED — no series entity in the schema |
| 52 | Summary embeddings | REFUSED — no model |
| 53 | Bradley–Terry vs Glicko | ADOPTED W6 |
| 54 | Tag profile page | ADOPTED W3 |
| 55 | Explanation quality eval | DEFERRED — needs a human protocol |
| 56 | IDF tag weighting | ADOPTED W6 |
| 57 | Cross-fandom bridge works | REFUSED — no fandom clusters; "bridge" is undefined here |
| 58 | Productivity/quality scatter | DEFERRED — low value; performer page carries the numbers |
| 59 | t-SNE tag map | REFUSED — no embeddings |
| 60 | Works you keep skipping | REFUSED — needs impressions |
| 61 | RSS of recommendations | REFUSED — saved-search notifications already deliver this; no RSS surface |
| 62 | OpenSearch descriptor | REFUSED — no SSR/crawlable surface |
| 63 | Jaccard similarity | ADOPTED W3 |
| 64 | Tag DNA | ADOPTED W3 — scene page distinctive tags |
| 65 | New since last visit | DEFERRED — needs a last-seen cursor |
| 66 | Author rec from one work | ADOPTED W1 — similar panel on the scene page |
| 67 | Default seeds for new readers | BUILT — `forYou` falls back to arrival order (phase 7 §9.6) |
| 68 | Tag "temperature" | DEFERRED — needs tag vote variance |
| 69 | Work controversy | DEFERRED — needs scene vote variance |
| 70 | Batch arena shortcuts | ADOPTED W1 |
| 71 | Per-tag rec quality | ADOPTED W7 |
| 72 | Embedding dimension search | REFUSED — no embeddings |
| 73 | Pareto ranking | DEFERRED — research; no product decision |
| 74 | Random walks | REFUSED — no graph-ML justification over affinity |
| 75 | Cross-fandom trope transfer | BUILT — `studio:x tag:y` in the query language |
| 76 | Embedding drift | REFUSED — no embeddings |
| 77 | Tag suggestions for uploaders | DEFERRED — curation UX decision |
| 78 | Pair similarity API | DEFERRED — no consumer |
| 79 | Diversity metric | ADOPTED W0 |
| 80 | Seasonal tag auto-detection | REFUSED — no need; manual mapping absent |
| 81 | Federated taste profiles | DEFERRED — phase 12 federation |
| 82 | Bandit exploration | REFUSED — needs impressions and a reward |
| 83 | Wrangling-graph transfer | REFUSED — merge data is too sparse to train on |
| 84 | Counterfactual evaluation | DEFERRED — needs impressions |
| 85 | Causal inference | REFUSED — beyond what the log can support |
| 86 | Bandit weight tuning | REFUSED — same as 82 |
| 87 | Graph neural network | REFUSED — no ML stack |
| 88 | Attention multi-seed | REFUSED — no model |
| 89 | ALS / implicit MF | ADOPTED W6 — CF ranker |
| 90 | Reinforcement learning | REFUSED — no session MDP |
| 91 | Cross-lingual recs | REFUSED — single-language tag corpus |
| 92 | LLM explanations | REFUSED — no local model; structured evidence is the explanation |
| 93 | Fairness audit | DEFERRED — no group attributes collected, by design |
| 94 | Social recommendation layer | DEFERRED — depends on 81 |
| 95 | Work lifecycle prediction | REFUSED — no crawl history to fit |
| 96 | Variable embedding dimensions | REFUSED — no embeddings |
| 97 | Haptic mobile arena | REFUSED — no mobile/PWA surface in this repo |
| 98 | Narrative arc matching | REFUSED — no chapter structure |
| 99 | Natural-language query | REFUSED — needs a model mapping prose to tags |
| 100 | Autonomous agent | REFUSED — no product or privacy basis |

---

## 5. Decisions

1. **Evaluation before new rankers.** W0 ships alone. A surface added before the harness
   is a claim without a number, and this repo has already recorded two reversals that
   tests found and review did not (phase 7 §14, decisions 18 and 24).
2. **The labelled stream is `elo_votes`, and it is read as slots.** `winner_id` is the
   left slot, not the winner. Reading it as the outcome inverts the metric on every
   `picked_side = 1` row — a defect that would look like a mediocre ranker rather than a
   bug.
3. **E1 replays ratings; it never reads `elo_ratings`.** The stored row is the latest
   state and contains the test answer. Replay is the only leakage-free view of a rating
   at a past time.
4. **Two taste models compete.** `taste_vectors` (item-based, stored) and `forYou`
   affinity (tag-based, derived) are rankers in E1/E2, not layers to merge. Neither is
   presented as a profile until one wins held-out data.
5. **Duels do not move ratings.** A `ranking_duels` row is an evaluation datum, not an
   Elo vote; the two tables never write to each other, and a test asserts it.
6. **No impression log.** Interleaving and bandits are deferred with their precondition
   named, rather than added as a tracking surface nobody asked for.
7. **Tag relations are derived.** Co-occurrence, Jaccard and trend are read from
   `scene_tags` and `scenes`; a stored relation table would be stale the moment a tag
   edit lands, and phase 7 §6.2 already refused a stored `scene_count` for that reason.
8. **No embeddings, and the refusal is structural.** `pgvector` is not installed and
   there is no producer; an idea whose first requirement is a vector column is not
   "later", it is unavailable on this instance.
9. **Baselines are mandatory.** Random, always-left and popularity are printed in every
   run. A ranker that cannot beat "always left" has not been evaluated, it has been
   demonstrated.
10. **Macro-average, cluster bootstrap, intervals.** Per-user (E2) and per-voter (E1)
    resampling; no single-number claim, no p-value theatre.
11. **The score is for ordering; the reasons are for reading.** This is phase 7 §8.1's
    rule carried into every new surface: `relatedPerformers`, `coEngagedScenes` and
    `relatedTags` all carry their evidence, and the evidence is what a client renders.
12. **A gained surface needs a rendering test.** `routes_frontend.go` serves
    `index.html` with 200 for every path, so a route that exists and serves nothing
    passes every status-code check. The vitest assertion is the gate.

---

## 6. Refused, with reasons that are not "later"

| Group | Ideas | Why |
|---|---|---|
| Embeddings / vector spaces | 11, 21, 29, 37, 47, 52, 59, 72, 76, 87, 88, 96 | No producer, no `pgvector` on this instance (phase 7 §2, §8.5). A brute-force in-memory store exists for the bounded candidate pool and is not an archive-wide index. |
| ML / RL / bandits | 30, 82, 85, 86, 87, 88, 89*, 90, 95 | No training stack, no reward stream, no sessions. *89 survives as a plain item-item CF ranker, which is counting, not training. |
| Impressions / clicks / views | 4, 48, 60, 65, 84 | No impression log, deliberately (§2.4). Every one of these needs to know what was shown. |
| SSR / feed formats | 61, 62 | The frontend is a static SPA with a catch-all; there is no server-rendered page to put metadata or an RSS document on. Saved-search notifications already cover the "tell me when" use case. |
| No such entity or signal | 27, 28, 32, 41*, 50, 51, 57, 68, 69, 80, 91, 93, 97, 98, 99, 100 | Series, fandoms, chapters, blocks, groups, a mobile app and a language model do not exist here. *41 is already served by `duration` + the runtime weight. |
| Explanation as generation | 55, 92 | The explanation contract is structured evidence a reader can check (phase 7 §8.1). A generated sentence is a second claim that cannot be verified against the row. |

Two refusals are worth stating in the positive: **idea 23 is already built** by the
composition of `serendipity` and the query language, and **idea 67 is already built** by
the no-signals fallback; neither should be rebuilt as a feature.

---

## 7. Verification and the completion bar

| Check | Command | Expected |
|---|---|---|
| Eval unit tests | `go test ./internal/eval/ -count=1` | ok; oracle 1.0, reversed 0.0, abstention coverage 0 |
| Pairwise run | `go run ./cmd/sbx-eval --db "$POSTGRES_DB" --suite pairwise --seed 1` | Markdown table with baselines, `n`, coverage, intervals |
| Engagement run | `go run ./cmd/sbx-eval --db "$POSTGRES_DB" --suite engagement --seed 1` | same, plus macro Recall@10/NDCG@10 |
| Leakage | `go test ./internal/eval/ -run TestNoHeldOutSignalIsVisible` | pass |
| Elo untouched by duels | `go test -tags integration ./internal/api/ -run TestRankingDuelDoesNotTouchElo` | pass |
| Backend | `go build ./...` | clean |
| Unit suite | `go test $(go list ./... \| grep -vE 'internal/image$') -count=1` | all ok |
| Integration | `go test -tags=integration -count=1 -p 1 ./internal/api/` | pass against real PG |
| Frontend build | `node node_modules/vite/bin/vite.js build` | succeeds |
| Frontend tests | `pnpm vitest run` | green, with a render assertion per new panel |
| Accessibility | `pnpm e2e -- a11y` | every `SWEEP_ROUTES` entry audited in both palettes |

The completion bar for the phase is not "the dashboard renders". It is: **a recorded
`eval_runs` row exists for each baseline and each candidate, and every surface added in
W1–W5 has at least one eval row that names it or its input signal.** A surface that
cannot be named by a run is not finished.

---

## 8. What this phase does not do

- It does not change any default ranking. W6 is a lab; promotion is a separate commit
  that cites a run.
- It does not add a public "recommended for you" page. `forYou` exists and W1 exposes
  similarity, not the feed; a feed page is a UI decision (phase 16) with its own empty
  state and controls.
- It does not merge the two taste models, expose `taste_vectors`, or add a "reset my
  taste" mutation. The first is unproven, the second is a profile surface with privacy
  consequences, and the third is a data-deletion surface.
- It does not implement `similar_to:` in the query language. That is a criterion the
  scene builder does not have (phase 7 §4); multi-seed ideas stay blocked on it.
- It does not make the arena entity-generic. Scoped performer matchups are in W1; a
  scene arena needs a scene card in the matchup UI and a candidate query that is not
  `performers`, and that is a product decision before it is a query.
