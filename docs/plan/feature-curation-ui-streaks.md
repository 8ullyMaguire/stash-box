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

## Data model — no new table for streaks

A streak is derivable from `trust_events (user_id, created_at)` plus its
`delta`. No `user_streaks` table is added, because a stored streak is a cached
answer to a question the log already answers exactly, and a cached answer
disagrees with the log the first time an event is backdated or deleted.

The one thing the log cannot give for free is *which* kind of action counts.
`trust_events.kind` is free text by design ("the set grows with each roadmap
phase, and a new event kind should not require a migration"). So the counting
rule is an explicit allow-list in one place, not a guess over every kind.

**Counting rule:** a day counts if the user recorded a trust event of a kind in
`StreakKinds` (`edit_approved`, `identification_solved`, `quest_completed`,
`replica_hosted`) — i.e. **curation that produced durable value**, not activity
for its own sake. Read-only browsing deliberately does not count: rewarding
page views would make a streak measure mouse movement.

## Implementation

### Backend (new, small)

1. `internal/service/streak/streak.go`
   - `Streak(ctx, userID) Streak` — walks distinct qualifying days descending
     from today, counts the consecutive run, and returns `best` from a second
     pass over all days. Today not yet active is **not** a break.
   - DST/timezone: computed against the **instance** timezone from config, not
     UTC, because "did you curate today" is a local-calendar question. A user in
     UTC+13 should not lose a streak at 10:00 UTC.
2. `internal/database/migrations/postgres/92_add_user_streak_cache.up.sql` —
   **not created.** Deliberate: see "no new table". The computation is one index
   scan over a single user's events and needs no cache.
3. GraphQL `type Streak { current: Int! best: Int! activeToday: Boolean! }`,
   `extend type Query { userStreak: Streak! @hasRole(role: READ) }`, in a new
   `graphql/schema/types/streak.graphql`.
4. Regenerate: `sqlc` → `gqlgen`. Generation order is a real dependency.

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

## Completion bar

- `go build ./...`, `go vet ./...`, `gofmt -l ./internal/` all clean.
- `go test ./internal/service/streak/` green, including: a streak crossing a DST
  boundary; a user with no events; a user whose only events are non-qualifying
  kinds; today-not-yet-active not breaking a run; and a run of exactly 1.
- Mutation check on the streak walk: removing the "today is not a break" clause
  and removing the kind allow-list must both be **killed**.
- `pnpm run generate` exit 0; `pnpm run validate` exit 0.
- Frontend suite green with no other suite on the box (this repo's suite is
  load-sensitive and reports phantom failures under contention).

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