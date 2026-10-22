# Phase 5 — mobile app, annual awards, advanced recommendation engine, mesh-wide curation campaigns

**Status: not started as a phase. The recommendation step's discovery half shipped in
programme phase 7 (`forYou`, `similarScenes`, `similarPerformers`) and its evaluation half
is specified in phase 7b (`docs/spec/feature-recommendation-eval.md`); annual awards,
mesh-wide campaigns and the mobile app remain. Depends on Phases 1–4.**

The last phase. Each of the four deliverables depends on data that only exists
after the earlier phases. The recommendation step is the exception that proved the
rule: phase 7 built the discovery data it needed, so its discovery half shipped there
and its evaluation half is phase 7b; the remaining three deliverables stay here.

---

## Step 1 — the recommendation engine

**Status: superseded in part, 2026-10-22.** The discovery phase (programme phase 7)
shipped `forYou` (tag affinity derived from fingerprints, lists and reviews, with
serendipity), `similarScenes` (evidence-based), `similarPerformers` (co-occurrence), the
search language, saved searches and the freshness views. This step was written before
that, so its storage decision is now wrong and its three stages are already half built.
The live specification is `docs/spec/feature-recommendation-eval.md` (programme phase
7b). Read that before writing code here.

What phase 7b decides, in place of the text below:

- **The evaluation harness comes first** (W0): pairwise replay over `elo_votes` and
  held-out engagement over fingerprints/lists/reviews, with random, always-left and
  popularity baselines printed beside every candidate.
- **The two taste models compete; no third is invented.** `taste_vectors` (item-based,
  stored, migration 77) and `forYou` affinity (tag-based, derived, phase 7 §9) are
  rankers in the harness. Neither is exposed as "the" profile until one wins.
- **There is no `recommendations` table.** The table below stores a score per entity per
  algorithm; that is a second answer able to drift from the signals it came from, which
  is exactly what phase 7 §9 refused. The equivalent is `eval_runs`
  (migration 145): one row per *run*, carrying the config, seed, cutoff, git sha and
  metrics — a snapshot of the experiment, not a cached ranking.
- **The three families survive as candidates, not as columns.** Most-elo-rated among
  co-voted entities is the `glicko-replay`/`cf` baseline; item-item CF is a W6 ranker;
  the taste blend is the `affinity` ranker and RRF fusion. Each is promoted only when
  its paired bootstrap interval excludes zero.
- **"Store all three and switch by config" becomes "record all three and switch after
  the evidence."** A config switch with no measured delta is a preference, not an
  algorithm decision.

What remains true from the original step, unchanged: the popularity baseline must be
beaten before anything sophisticated ships; ranking must be deterministic for the same
inputs (a map-iteration leak is caught only by exact-order tests); a user with no
signals gets the baseline rather than an empty list; an entity with no signal does not
appear; and a blended score must lie between its components.

### For the record — the superseded text

| Stage | Algorithm | Why |
|---|---|---|
| 1 | Most-elo-rated among a user's co-voted entities | A popularity baseline. If this is not beaten, nothing later will be worth shipping. |
| 2 | Item-item collaborative filtering over `elo_votes` | The natural next step; the vote data is already shaped for it. |
| 3 | Blend with taste-profile similarity | Uses the federation work from Phase 4. |

```sql
CREATE TABLE recommendations (          -- NOT built; see the status note above.
    entity_type TEXT NOT NULL,
    entity_id UUID NOT NULL,
    algorithm TEXT NOT NULL,            -- 'popular' | 'cf' | 'blended'
    score REAL NOT NULL,
    generated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (entity_type, entity_id, algorithm)
);
```

The two verification sentences that still apply are in phase 7b's completion bar: a
ranker that cannot beat `always-left` or `popularity` has not been evaluated, and every
new surface must be named by at least one recorded run.

## Step 2 — annual awards

Vision §9: community votes + Elo + reviews determine the best of the year.

This is mostly a **query and a schedule** over data that already exists. The
parts worth care:

- **Eligibility is time-boxed**: only entities with activity in the window.
  Without it, an entity that was dominant five years ago wins forever, because
  Elo decays too slowly to prevent it. Vision §9 asks for time decay explicitly.
- **The result is a snapshot**, stored, not recomputed on read. Awards are
  announced and must not change afterwards.

```sql
CREATE TABLE awards (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    year INTEGER NOT NULL,
    category TEXT NOT NULL,   -- 'performer' | 'scene' | 'studio' | 'site'
    entity_id UUID NOT NULL,
    final_score REAL NOT NULL,
    -- The component scores, so the result is explainable to a user who
    -- disagrees with it.
    elo_score REAL NOT NULL,
    vote_score REAL NOT NULL,
    review_score REAL NOT NULL,
    UNIQUE (year, category, entity_id)
);
```

The component columns are not optional. "Why did I lose?" is a question every
ranked system gets, and storing only the composite means the answer cannot be
given.

Verification: the same input data produces the same awards on a re-run. A
nondeterministic award is a bug that surfaces in public.

## Step 3 — mesh-wide curation campaigns

Vision §7 and §12: instances with similar taste run joint campaigns; guilds work
on a niche or studio together.

Phase 4 supplies the similar-taste grouping; this phase adds the campaign
coordination.

```sql
CREATE TABLE campaigns (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title TEXT NOT NULL,
    quest_id UUID NOT NULL REFERENCES quests(id) ON DELETE CASCADE,
    -- Federated campaigns span instances; a solo campaign is the degenerate case.
    coordinating_instance TEXT,
    starts_at TIMESTAMPTZ NOT NULL,
    ends_at TIMESTAMPTZ NOT NULL,
    CHECK (ends_at > starts_at)
);

CREATE TABLE campaign_participants (
    campaign_id UUID NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    instance_key TEXT NOT NULL,
    contribution INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (campaign_id, user_id)
);
```

`instance_key` is on the participant, not derivable from the user, because one
user may be in several instances (vision §2: users can join multiple). Aggregating
per-user across instances is the whole point of the feature, so the instance must
be recorded explicitly.

## Step 4 — mobile app

Vision §11: vote in matchups, complete quests, write reviews, solve
identifications, get notifications, manage the archive.

**Out of process, like the browser extension in Phase 3.** A separate codebase
outside this Go module. What belongs here:

- The API contract (Phases 1–3 already provide it).
- Push notifications, which need a new delivery path.

For push, the honest position: this fork has an email manager
(`internal/email/`) and no push infrastructure. Adding APNs/FCM is a dependency
and an operational burden. **Recommend mobile push as the last thing in this
phase, and only if there is a user asking for it** — the API already supports
everything else the app needs, and polling works for a first release.

Do not record the mobile app as covered by `go test`; it is not, and the worklog
should say so plainly.

---

## Standing rules for every phase

These are the traps that actually cost time across the 26 issues already closed.
They are not hypothetical; each one produced a wrong result that had to be undone.

1. **Mutation-check every test.** Delete the implementation, run the test, watch
   it fail, restore. A test that passes on unfixed code is worse than no test.
   Five issues turned on this: #525, #950, #1007, #1177, #605.
2. **A test that exercises a helper is not a test of the caller.** In #605, four
   tests covered a content-type detector and all four stayed green when the line
   that *called* it was deleted. Always assert the wiring.
3. **The shared integration database is not isolated.** Any assertion about
   membership in an unfiltered collection must pin `PerPage`; the default is 25
   and other tests' fixtures push yours off the end. Hit in both directions
   (#829, #1007).
4. **A destroy mutation hard-deletes; a destroy EDIT soft-deletes.** Any test
   about `deleted = true` must apply the edit, or it tests a row that no longer
   exists. This silently produced four vacuous tests in #1007.
5. **`approveEdit` calls `t.Errorf` on any error**, so it cannot assert an
   expected failure. Call `ApproveEdit` directly and assert the `Applied` flag or
   the modbot comment — apply failures become `Unknown Error: %v` on the comment
   and the edit is marked failed, never returned as an error.
6. **A regex that matches anything proves nothing.** The e2e cooldown assertion
   in #1277 matched `/cooldown|wait/i` and so passed through the entire life of
   the bug it should have caught.
7. **Regenerate, then verify idempotence.** `sqlc generate` and `gqlgen` must be
   run twice with no diff. A green test run after failed codegen is not a pass.
8. **`internal/image` must stay in the unit run.** It was excluded until #1205,
   which is how a missing PNG/JPEG decoder (#948) stayed hidden behind a
   build-tag split.
9. **Rebuild the frontend before browser-verifying anything.** A stale
   `frontend/build` renders every route blank with HTTP 200. Use
   `node node_modules/vite/bin/vite.js build`, not `pnpm build` (hoisted linker).
10. **Do not hand-edit generated files.** `internal/queries/*.sql.go`,
    `internal/models/generated_*.go` and `graphql/generated.go` come from sqlc and
    gqlgen. Change the `.sql` / `.graphql` and regenerate.
