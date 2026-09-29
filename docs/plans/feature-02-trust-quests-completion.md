# Phase 2 — trust levels (extended), opt-in content viewing, gamification, curation quests, completion scores

**Status: not started. Depends on Phase 1.**

Trust levels are built in **Phase 1 Step 1**, because "public" cannot be
implemented without them. This phase takes them further: content access gated on
the level-4 opt-in, the full XP/badge surface, curation quests, and completion
scores.

Read `phase-1-portal-elo-identification.md` first. This file assumes Step 1
(`user_trust`, `trust_events`, the thresholds map) is done.

---

## Step 1 — opt-in content viewing

Vision §6: high-trust users **explicitly opt in** to viewing content. Three
states, not two: not eligible, eligible and opted out, eligible and opted in.

The existing `user_trust.content_viewing_opt_in` boolean covers only two of the
three. Add eligibility as a derived value (level >= 4) and keep the boolean as
the user's choice. **Do not store eligibility** — it changes when thresholds do.

The gate is in the image-serving path, `internal/api/routes_image.go`, which
already branches on SVG:

```bash
grep -n "image/svg+xml\|shouldResize" internal/api/routes_image.go
```

Two guards, both required in tests:

- A level-3 user is refused even with the opt-in set. Opting in is not a
  bypass.
- The opt-in is revocable and takes effect immediately — no cache. Check
  `cacheManager`, which caches image bytes keyed by `(image_id, requested_size)`:
  **a revoked user must not be served from that cache.** This is a real
  correctness risk, not a theoretical one; the cache key has no user in it.

That second point is the one most likely to be missed and is why it is called out
here rather than left to the implementer.

Verification:

```bash
export POSTGRES_DB="postgres:[REDACTED]@127.0.0.1:5434/stash-box-test?sslmode=disable"
go test -tags=integration -count=1 -run TestContentOptIn ./internal/api/
```

## Step 2 — curation quests and bounties

Vision §7: "Add missing birthdates for 5 performers", "Link 10 unlinked scenes".

```sql
CREATE TABLE quests (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- e.g. 'performer_missing_birthdate'
    kind TEXT NOT NULL,
    title TEXT NOT NULL,
    description TEXT NOT NULL,
    target_count INTEGER NOT NULL,
    -- XP multiplier for a high-impact gap (vision §7, "Bounties").
    bounty_multiplier REAL NOT NULL DEFAULT 1.0,
    -- NULL means generated from the data rather than authored.
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE quest_claims (
    quest_id UUID NOT NULL REFERENCES quests(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    claimed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ,
    PRIMARY KEY (quest_id, user_id)
);
```

**The quest predicate must be a Go function, not SQL**, keyed by `kind`, in one
switch. Reasons: the same gap logic is needed for completion scores (Step 3) and
for the frontend progress bars, and a SQL implementation would be duplicated
three times. One function, three callers.

The quest generator is the same predicate applied to a query:

```bash
grep -n "func.*Count" internal/queries/sql/performer.sql | head
```

Verification:

```bash
go test ./internal/service/quest/ -count=1 -v
```

Required, in order:

1. A quest's progress is the count of entities that now satisfy the predicate.
   Assert it *increases* when one is fixed and does not move otherwise.
2. Two users claiming the same quest both make progress independently.
3. The bounty multiplier applies to XP and not to the claim count. Assert the XP
   figure directly, not a rank.
4. **Guard:** a quest that is already complete claims nothing. A progress
   calculation that returns a negative or inflated count is the classic bug here.

## Step 3 — completion scores

Vision §7: every entity has a completion score from missing metadata, snapshot
coverage, performer links, studio links, tag coverage, source links, review
coverage, duplicate confidence.

```sql
ALTER TABLE performers ADD COLUMN completion_score REAL;
ALTER TABLE performers ADD COLUMN completion_scored_at TIMESTAMPTZ;
```

**Recompute as a background job and store the result.** Computing it per request
means a fan-out to every related table for every entity in a list, and the
performer list is paginated to 25–10000 (see the `PerPage` trap in the standing
rules).

Scoring lives in one function per entity type, sharing a helper:

```go
// scoreFromMissing returns 1 - (missing/considered), clamped to [0,1].
// considered == 0 must return 1, not NaN: an entity with nothing to rate is
// complete by definition, and a NaN here propagates into every sort that
// references the score.
```

Verification:

```bash
go test ./internal/service/completion/ -count=1 -v
```

Required: a brand-new entity with nothing filled in has a *defined* score and it
is low; an entity with every field set scores 1.0; a division by zero returns 1
and does not produce NaN. That last one is the guard, and it is the reason the
comment above exists.

## Step 4 — the full gamification surface

Extend Phase 1's `user_xp` and badge system with the phase-2 sources: completed
quests, hosted replicas (a no-op until Phase 4, so the column exists and stays
zero), streak tracking, and the leaderboards.

**Leaderboards are read from `elo_ratings` and `user_xp`, never from a
materialised table.** A materialised leaderboard is a cache that will be wrong,
and the vision makes leaderboards the daily-return mechanic, so a wrong one is
directly user-visible.

Streaks need care: a streak is a function of *consecutive days with activity*, and
the boundary is timezone-dependent. Store UTC day numbers and document that
choice in a comment on the column, because "which timezone" is otherwise an
argument every time.

Verification:

```bash
go test ./internal/service/gamification/ -count=1 -v
```

The required guard: a streak survives a day with no activity *only* up to the
configured grace period, and a test that checks the day boundary — not just
"two consecutive days give a streak of 2".

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
