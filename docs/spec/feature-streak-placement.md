# Streak placement and UI reachability — SPEC

Status: **Written 2026-10-05, before implementation.** Author: Hermes, from the
owner's instruction: *"streaks should be on user dashboard but currently is on
the curation route, fix that. ensure all functionality is reachable from ui and
is where it should be."*

The second sentence is the real scope. "Move the streak" is one edit; "ensure
all functionality is reachable" is an audit, and the audit found three more
things that were broken in the same family.

## Problem

`userStreak` is a **READ**-gated query about the calling user's own contribution
history, and it renders only inside `CurationDashboard`, which is reachable only
through the **Curation** nav link — gated on `canVote`. So a user with READ but
not VOTE cannot see their own streak anywhere in the product. It is not
misplaced; it is unreachable for a third of the roles that are allowed to read
it.

The cause is structural, not a forgotten link: `userStreak` is selected inside
`CurationDashboard.gql`, so reaching it means running the query that fetches
five `countIncompleteEntities` counts. The streak is coupled to the curation
dashboard by the shape of one `.gql` file.

## Placement decision

**A streak is a fact about a user, so it belongs on that user's page.** Three
candidate homes, and why two lose:

- **The user's own profile** (`/users/:name`) — adopted. Every other fact about
  a user is already there: edit counts, vote counts, roles, invite tokens. A
  streak is the same kind of object.
- **The nav as a global badge** — rejected. It would need its own query on every
  page load, and the value is a per-user fact with no meaning when read at a
  glance beside "Notifications".
- **Its own top-level route** (`/streaks`) — rejected as the *primary* home. It
  is four numbers and one badge; a route for it is a page with nothing to do on
  it, and the curation dashboard already demonstrates that a single fact gets a
  whole page and then goes unread.

### Ownership and the visibility rule

The streak shows **on the viewing user's own page only**. It is not shown on
another user's page.

This is not a UI preference, it mirrors a schema decision that predates this
work. `streak.graphql` states: *"There is no `userStreak(id:)` on purpose: this
is a private measure of one's own contribution, and exposing it for arbitrary
users turns a progress indicator into a public ranking of who has been
absent."* The backend has no `id:` argument and a test pins its absence, so the
UI cannot fetch another user's streak even if it wanted to. The UI must therefore
not imply it could: a "Streak" section on someone else's profile would render
empty, which reads as *they have no streak* — the exact misreading the schema
comment refuses to allow, arrived at by a different route.

## Curation keeps its copy — deliberately

`StreakCard` stays in `CurationDashboard`.

Two reasons, and the second is the one that matters:

1. A curator arriving at `/curation` to vote should see their own history without
   a second navigation.
2. **The curation dashboard is role-gated and the profile is not.** So for a
   READ-only user the profile copy is the *only* copy. Removing the curation
   copy would keep the original bug and relocate it.

The consequence to state plainly: there are now two places that render a streak,
so there is a chance they disagree. They cannot, and that is worth a test rather
than a comment — both read the same query and the same component.

## Reachability audit — findings

Every `ROUTE_*` constant was checked for a reference outside its own definition,
every page directory for an import in `pages/index.tsx`, and every `.gql` for an
import. Four findings, all real:

| # | Finding | Severity |
|---|---|---|
| A | Streak unreachable without VOTE (§ above) | the reported bug |
| B | `ROUTE_CONFIRM_EMAIL` (`/users/confirm-email`) and `ROUTE_CHANGE_EMAIL` (`/users/change-email`) are referenced **nowhere**. The routes exist, but nested under `/users/:name/*`, so they actually serve `/users/:name/confirm-email`. The constants' values do not match the mounted routes. | dead constants; no user-facing break |
| C | `ROUTE_NOTIFICATION_SUBSCRIPTIONS` (`/users/:name/notifications`) has a mounted route and a working page, and **no link to it from anywhere**. Notification preferences are reachable only by hand-typing the URL. | real: a feature nobody can find |
| D | `QueryNotifications.gql` appears unused | **false positive** — imported as `useNotifications`. Recorded so the next session does not re-investigate it. |

Finding D is listed because an audit that reports only real findings is not
auditable; a reader must be able to see what was checked and dismissed.

### What finding C implies beyond adding a link

`User.tsx` has a row of buttons gated on `isOwner` — My Fingerprints, Image
Preferences, Change Password, Change Email — and notification preferences are
missing from it while every sibling is present. The fix is one button in that
row, not a new nav entry: the row is already the "things you can configure
about yourself" surface, and adding a sixth top-level destination for a
preferences form would be the over-correction.

## Non-goals

- **No schema change.** `userStreak` already exists at READ with the right shape.
- **No aggregation or decay.** §7.25 and the existing `StreakCard` contract are
  unchanged: no "you lost your streak", no reminder, no streak-freeze item.
- **Not every feature gets a route.** The audit's job is that everything is
  *reachable*, not that everything is *top-level*.

## Completion bar

| Check | Expected |
|---|---|
| `ROUTE_*` constants with zero references outside `route.ts` | only the ones this section names as intentionally unused |
| Notification preferences reachable from `User.tsx` | yes, and a test asserts it |
| Streak card renders on `/users/:name` for self | yes |
| Streak card absent on another user's profile | yes, asserted |
| Streak card still on `/curation` | yes, asserted |
| READ-only user can reach a streak | by navigating directly, with no VOTE role |
| `pnpm run validate` | clean |
| `pnpm vitest run` | green, plus the new tests |
| `vite build` | succeeds |

### Why each test exists

Wiring tests, not component tests — the failure mode here is a fact that is
rendered but unreachable, which a component test cannot see because it mounts
the component directly. Each test is paired with the mutation that kills it
(remove the button → the reachability test goes red; show the card for non-self →
the privacy test goes red). A wiring test that has never been broken is a
claim, not evidence.