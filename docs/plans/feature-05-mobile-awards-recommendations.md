# Phase 5 — mobile app, annual awards, advanced recommendation engine, mesh-wide curation campaigns

**Status: not started. Depends on Phases 1–4.**

The last phase. Each of the four deliverables depends on data that only exists
after the earlier phases, which is why none of it can be pulled forward.

---

## Step 1 — the recommendation engine

Vision §4 and §14. This is the flywheel made concrete, and it is the component
that makes the whole thing worth returning to.

Start with the simplest thing that can be shown to work, and **measure it before
adding sophistication**. A recommendation system whose quality is asserted rather
than measured is the usual failure.

| Stage | Algorithm | Why |
|---|---|---|
| 1 | Most-elo-rated among a user's co-voted entities | A popularity baseline. If this is not beaten, nothing later will be worth shipping. |
| 2 | Item-item collaborative filtering over `elo_votes` | The natural next step; the vote data is already shaped for it. |
| 3 | Blend with taste-profile similarity | Uses the federation work from Phase 4. |

```sql
CREATE TABLE recommendations (
    entity_type TEXT NOT NULL,
    entity_id UUID NOT NULL,
    -- 'popular' | 'cf' | 'blended'
    algorithm TEXT NOT NULL,
    score REAL NOT NULL,
    generated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (entity_type, entity_id, algorithm)
);
```

**Store all three algorithms and switch between them by config.** That is what
makes the quality comparison possible; storing only the current one destroys the
evidence for every future tuning decision.

**Ranking must be deterministic for the same inputs.** Go map iteration order
leaks into sorted output constantly. Any sort over a map needs an explicit
tie-break, and a test that asserts exact ordering on a fixed input is the only
thing that catches it.

```bash
go test ./internal/recommend/ -count=1 -v
```

Required: identical inputs produce identical ordering across 100 runs (this is
the test that catches map-order leakage, and it must run the loop); a user with
no votes gets the popularity baseline rather than an empty list; an entity with
no votes does not appear; the blended score is between its components.

The last one is the guard against a blend formula that is not actually a blend.

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
