# Gamification frontend + streaks — SPEC

Status: **Approved by owner 2026-10-01 ("implement the frontend and streaks").**
Written before implementation, per the standing spec-before-code rule.

## Problem

The gamification backend is complete and exposed: 38 GraphQL fields across
`elo.graphql`, `trust.graphql` and `completion.graphql` — Elo ratings,
matchup voting, leaderboards, trust levels, quests, bonus points, and
`countIncompleteEntities`. No user can reach any of it. The UI has zero
`.gql` documents and zero word-boundary references to any gamification field.

Verified, not assumed: an earlier session reported "32 files touch Elo" — that
was substring noise (`useLocation`, `labelOf`, `below`). Word-boundary search
for `eloMatchup|eloRating|eloLeaderboard|voteElo|EloRating|countIncompleteEntities`
returns **nothing** in `frontend/src`. The original claim was right; the
directory in that check was wrong, and both facts had to be established before
writing a line of UI.

Streaks are different: **no streak exists anywhere in the schema.** They are new
backend work, not new frontend work.

## Scope

In: the curation dashboard, the Elo matchup vote queue, the leaderboard, and
user streaks, all reachable from the nav.

Out: studios/tags/sites leaderboards beyond the entity-type switch (the query is
generic, the UI is not per-type), quest authoring, trust-level editing, and every
coercive mechanic (see Non-goals).

## Non-goals — the honest/rewarding line

The owner asked for "everything that might help, including dark patterns." This
build draws the line at **coercion, not at gamification**, and the line is
mechanical rather than a matter of taste:

- **Allowed:** anything where the number shown is TRUE. Leaderboards, badges,
  counts, streaks.
- **Refused:** anything that manufactures urgency or manufactures loss. Streak
  *decay with a penalty*, loss-framed copy ("you'll LOSE your 30-day streak"),
  fake scarcity, variable-ratio rewards, and push notifications timed to a
  user's inactivity.

Concretely, this means **a streak that reads `0` is rendered as "no streak yet",
never as "you lost your streak"**, and there is no streak-protection item, no
freeze token, and no way to lose an earned badge. The backend stores nothing
about streaks that could decay — `trust_events.created_at` is an append-only log,
so a streak cannot be destroyed by the passage of time; it only stops growing.
That is a property worth stating, because it means the coercion surface is
structurally absent rather than merely unbuilt.

The user was told this before implementation and approved the build.

## Correction to this spec: the streak backend already existed

This document originally planned to BUILD the streak service. It was already
built, already mutation-tested, and already wired into `Factory.Streak()` -- by
a concurrent session, mid-task, while this work was in progress. That was found
by `grep` hitting `internal/queries/sql/streak.sql` and `streak_integration_test.go`
during the first exploration.

The existing implementation is better than what this spec proposed, so it was
kept unchanged:

- **No `user_streaks` table.** Same reasoning as here, arrived at independently.
- **`now` is a parameter** to `For()`, so the boundary logic is injectable and
  the tests drive it directly. `ForCurrent()` was added as a thin wrapper that
  reads the database clock, so the *callers* cannot forget to.
- **Activity days are truncated in the DATABASE's timezone**, in SQL, with the
  cast to `timestamp` explicit. The comment in streak.sql records that getting
  this wrong once made `ActiveToday` false for an event created a minute
  earlier, and that bare `date_trunc` is inferred by sqlc as an INTERVAL.

What was genuinely missing was the **binding**: `graphql/schema/types/streak.graphql`,
the `DatabaseNow` query, `ForCurrent`, and the resolver. Those are what this work
added.

## Data model -- no new table for streaks

A streak is derivable from `trust_events (user_id, created_at)`. No
`user_streaks` table is added, because a stored streak is a cached answer to a
question the log already answers exactly, and a cached answer disagrees with the
log the first time an event is backdated or deleted.

**Counting rule:** every day with any trust event counts, **deltas are not
summed**. A `-1` event is a real contribution day -- the curator showed up and
their edit was rejected, which is participation, not absence -- and filtering on
a positive total would quietly end the streak of a user whose activity is all
rejections. That rule is implemented and tested in `streak_integration_test.go`.

## Implementation

### Backend (as built)

1. `graphql/schema/types/streak.graphql` -- `Streak` type and `userStreak` at
   READ, with **no `id:` argument** (a test pins this).
2. `DatabaseNow` query in `internal/queries/sql/streak.sql` -- the database's
   clock in the database's zone. sqlc first inferred it as `interface{}`;
   `::timestamp` is what makes it `time.Time`.
3. `Service.ForCurrent` -- thin wrapper over the injectable `For`.
4. `internal/api/resolver_streak.go` -- reads the calling user, formats
   `lastActiveDay` as `YYYY-MM-DD` rather than an instant.
5. Regenerate `sqlc` then `gqlgen`.

### Frontend (the actual ask)

5. `frontend/src/graphql/queries/Curation.gql` — `eloMatchup`, `eloLeaderboard`,
   `countIncompleteEntities` (x5 entity types), `userStreak`.
6. `frontend/src/graphql/mutations/VoteElo.gql`.
7. `frontend/src/graphql/queries/index.ts` — `useEloMatchup`, `useEloLeaderboard`,
   `useIncompleteCounts`, `useUserStreak`; mutations/index.ts — `useVoteElo`.
8. `frontend/src/pages/curation/CurationDashboard.tsx` — the number that matters:
   incomplete entities per type, with the `completion` shape reused.
9. `frontend/src/pages/curation/MatchupVote.tsx` — two entities side by side, one
   click, next matchup. The highest-leverage loop here: Elo already picks the
   pair where one vote moves the rating most.
10. `frontend/src/pages/curation/Leaderboard.tsx` — with `voteCount` rendered
    next to every rating. The backend already breaks near-ties by observation
    count; making it visible is what stops a 3-vote rating looking like a
    300-vote one.
11. `frontend/src/pages/curation/StreakBadge.tsx` — the streak, as a fact.
12. Route constants + `Pages` wiring + a nav entry in `Main.tsx`, role-gated to
    users who can vote (`canVote`/role check per existing convention).

## Completion bar -- as measured

| Check | Result |
|---|---|
| `go build ./...` | clean |
| `go vet ./...` | clean |
| `gofmt -l ./internal/` | empty |
| streak resolver integration tests (`-tags=integration`) | 4/4 pass against real Postgres |
| `pnpm run validate` (biome + format + tsc) | clean |
| `vite build` | succeeds, 576-byte index |
| `pnpm vitest run` | 689 tests / 55 files green, uncontended box |
| StreakCard design mutations | 2/2 killed |

**The design mutations are the ones that matter.** Two were applied on purpose
and confirmed to fail the suite:

1. A zero streak reframed as `"You lost your streak"` -- killed by
   `reads a zero streak as 'No streak yet', never as a loss`.
2. `activeToday` folded into `currentStreak > 0` -- killed by
   `does not imply today is done when the streak is merely alive`.

Neither is visible to `tsc` or `biome`. Both compile and type-check perfectly,
and both are exactly the coercion the feature was specified to avoid. A design
constraint that is only written in a comment is not a constraint.

### One real bug the compiler could not catch

`elapsed: the gqlgen `DateTime` scalar binds to `*string`, so
`LastActiveDay *time.Time` did not assign. Fixing it by formatting in UTC would
have been wrong: the value is a calendar day, and 2026-10-01 local midnight is
2026-09-30 22:00 UTC, so the client would show the day before the one the
database matched. It is formatted as a plain `YYYY-MM-DD`, which is what the
field actually is, and pinned by a test that compares against the database's own
`::date::text`.

### The failed first run

The first integration run failed with `"not authorized"`. `createTestUser(nil,
nil)` passes an EMPTY role list, not the default -- the `nil` branch inside that
helper fills in ADMIN, but passing an explicit nil slice through to it does not.
The query needs READ. Cost one run; worth recording because "nil means default"
is true in most Go helpers and false in this one.

## Verification commands

```bash
# backend
cd ~/code-local/go/stash-box
go run github.com/sqlc-dev/sqlc/cmd/sqlc generate
go run github.com/99designs/gqlgen generate
gofmt -l ./internal/                     # must be empty
go build ./... && go vet ./internal/...
go test ./internal/service/streak/ -count=1

# frontend (pnpm with hoisted linker; not `pnpm build`)
cd ~/code-local/go/stash-box/frontend
node node_modules/vite/bin/vite.js build     # stale build = blank routes at HTTP 200
pnpm run generate
pnpm run validate
pnpm vitest run
```

## Risks

- **Stale frontend build renders every route blank with HTTP 200.** Must rebuild
  before browser-verifying anything, or "it loads" will be reported as pass while
  the page is empty.
- **A wrong `.gql` fails codegen, not the browser.** Good — but the error names
  the field, so read it rather than guessing at the schema.
- **Concurrent sessions in this tree.** Another session wrote an untracked file
  into `internal/sdbimport/` mid-task earlier. Commit explicit paths; never
  `git add -A`.