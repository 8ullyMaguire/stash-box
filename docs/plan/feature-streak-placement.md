# Streak placement and UI reachability — PLAN

Spec: `docs/spec/feature-streak-placement.md`. Written before implementation.

Branch state: `d07064a4`, with uncommitted §7.25 recall work in the tree
(`internal/queries/sql/scene.sql`, `internal/service/scene/service.go`, migration
103, `internal/api/scene_search_recall_integration_test.go`). **That work is
blocked on an unresolved decision** (editing migration 61 vs. a DROP/CREATE
rebuild in 103) and is not to be committed by this plan. This plan touches only
frontend files, so the two do not collide — but commit explicit paths, never
`git add -A`.

---

## Step 1 — Give the streak its own query document

`userStreak` is currently selected inside `CurationDashboard.gql`, which is why it
cannot be rendered without also fetching five curation counts. Split it.

**Create** `frontend/src/graphql/queries/UserStreak.gql`:

```graphql
query UserStreak {
  userStreak {
    currentStreak
    longestStreak
    totalActiveDays
    activeToday
    lastActiveDay
  }
}
```

**Remove** the `userStreak { ... }` block from
`frontend/src/graphql/queries/CurationDashboard.gql`.

Both stay. `CurationDashboard` still needs its own streak — the spec keeps the
curation copy, and the curation copy and the profile copy must read the same
query so they cannot disagree.

**Then regenerate.** `UserStreak.gql` will not appear in `queries/index.ts` until
codegen runs, and `useUserStreak` will not exist as an import until it does:

```bash
cd ~/code-local/go/stash-box/frontend
pnpm run generate          # graphql-codegen; writes types.ts + queries/index.ts
```

Verify — both hooks exist and are distinct:

```bash
grep -n "useUserStreak" src/graphql/queries/index.ts
grep -n "UserStreakDocument" src/graphql/types.ts | head -1
```

Expected: one hook, one document. If `useUserStreak` is absent, codegen did not
see the new file — check the filename matches the `query` name, which the repo
does by convention (`CurationDashboard.gql` → `useCurationDashboard`).

---

## Step 2 — Move the card into the user page, self only

`frontend/src/pages/users/User.tsx` renders the profile. It already has an
`isOwner`-gated row of self-service buttons at lines ~312-332 and an `isSelf`
flag from `useCurrentUser()`.

Add the streak card **inside the `isOwner` block**, above the buttons row's
parent `<hr />`, so it is on the user's own page and nowhere else.

```tsx
import StreakCard from "src/pages/curation/StreakCard";
```

Importing a component from `pages/curation/` into `pages/users/` is a slight
inversion of the directory's grain. It is deliberate: the component is shared by
two routes, and duplicating it would create two StreakCards free to disagree —
exactly the failure the spec names. If the shared-ownership discomfort becomes
real, the move is to `src/components/StreakCard.tsx`; do not duplicate it in the
meantime.

The hook, and the guard that keeps it off other people's pages:

```tsx
const { data: streakData } = useUserStreak(!isSelf(user));
```

`useUserStreak` takes an Apollo `skip` boolean. Passing `!isSelf(user)` means the
query does not run at all for another user's profile, so no request is made and
nothing can be shown. The card is additionally not rendered for non-self, so
there is no empty card either.

Render:

```tsx
{isSelf(user) && <StreakCard streak={streakData?.userStreak} />}
```

---

## Step 3 — Notification preferences get a link (finding C)

`/users/:name/notifications` is mounted, works, and is linked from nowhere.
`User.tsx`'s `isOwner` row is the right home.

Add to the imports from `src/constants/route`:

```tsx
ROUTE_USER_NOTIFICATION_SUBSCRIPTIONS,
```

and inside the existing `isOwner` fragment, after Image Preferences:

```tsx
<Link
  to={createHref(ROUTE_USER_NOTIFICATION_SUBSCRIPTIONS, user)}
  className="ms-2"
>
  <Button variant="secondary">Notification Preferences</Button>
</Link>
```

`createHref` is already imported and already used for the sibling buttons — use
it rather than a literal, so the link follows the same `:name` interpolation the
route expects.

---

## Step 4 — Dead route constants (finding B)

`ROUTE_CONFIRM_EMAIL` and `ROUTE_CHANGE_EMAIL` have no references. The routes are
mounted as `/users/:name/confirm-email` and `/users/:name/change-email`, nested
in `UserLoader`, so the constants' values (`/users/confirm-email`,
`/users/change-email`) do not name anything that resolves.

They are not user-facing broken: `UserConfirmChangeEmail.tsx` and
`UserValidateChangeEmail.tsx` are reached from the email flow, not from a link.

Delete both constants. Do **not** add routes for them, and do not rewrite them
to `/users/:name/...` — nothing consumes them, and inventing a consumer for a
constant is the same mistake in the other direction. `ROUTE_DRAFT` is a
false positive in the audit output (it is a substring of `ROUTE_DRAFTS`, which is
used); leave it alone.

---

## Step 5 — The tests, which are the actual work

`frontend/src/pages/users/__tests__/` exists. Create
`UserReachability.test.tsx` there.

Four assertions, each paired with the mutation that kills it:

1. **Notification preferences are reachable from the profile.** Render `User.tsx`
   for the self user, assert the button exists. *Mutation:* delete the `<Link>`
   from Step 3 → red.
2. **The streak card is on the user's own profile.** *Mutation:* change
   `isSelf(user)` to `true` unconditionally → still green, so this one must be
   asserted the other way: assert the card is **absent** for a non-self user and
   present for self. The absence assertion needs its subject counted first —
   assert the self case renders exactly one `StreakCard` before asserting the
   non-self case renders zero. An absence assertion with no count first is
   vacuously true.
3. **The curation dashboard still has a streak copy.** *Mutation:* delete
   `<StreakCard streak={data.userStreak} />` from `CurationDashboard.tsx` → red.
4. **A READ-only user is not blocked from the profile.** `canVote` must not gate
   the profile route. *Mutation:* add `canVote(user) &&` around the streak →
   red.

Follow the existing pattern in
`frontend/src/pages/curation/__tests__/StreakCard.test.tsx` for render setup,
and use `MockedProvider` from `@apollo/client/testing` with the generated
`UserStreakDocument` as the mock — the repo's other GraphQL tests do this.

```bash
cd ~/code-local/go/stash-box/frontend
pnpm vitest run src/pages/users/__tests__/UserReachability.test.tsx
```

---

## Step 6 — Full verification

```bash
cd ~/code-local/go/stash-box/frontend

pnpm run validate                              # biome + format + tsc; must be clean
pnpm vitest run                                # whole suite; must be green
node node_modules/vite/bin/vite.js build       # NOT pnpm build (hoisted linker)
```

Then measure the rendered page rather than trusting the tests alone:

```bash
# dev server, then confirm the card is on the profile and the nav still offers Curation
node node_modules/vite/bin/vite.js dev
```

A stale build renders every route blank at HTTP 200, so "it loads" proves
nothing. Open `/users/<yourname>` and look for "active days, all time".

Backend is untouched by this plan; `go build ./...` is a formality, not a gate.

---

## Step 7 — Record it

- Append a §7.26 subsection to `docs/SPEC.md` summarising the placement decision
  and the audit findings, since §7.25 established the pattern of recording
  reachability as a first-class concern.
- Add a line to `docs/track/WORKLOG.md`.
- Update the vault note at `~/secondbrain/10-Projects/`.

Commit with explicit paths:

```bash
cd ~/code-local/go/stash-box
git add frontend/src/graphql/queries/UserStreak.gql \
        frontend/src/graphql/queries/CurationDashboard.gql \
        frontend/src/graphql/queries/index.ts \
        frontend/src/graphql/types.ts \
        frontend/src/pages/users/User.tsx \
        frontend/src/pages/users/__tests__/UserReachability.test.tsx \
        frontend/src/constants/route.ts \
        docs/spec/feature-streak-placement.md docs/plan/feature-streak-placement.md
git commit -m "fix(ui): streak belongs on the user profile, and notification prefs need a link"
```

---

## Risks

- **A wrong `.gql` fails codegen, not the browser.** The error names the field;
  read it rather than guessing at the schema.
- **`skip` semantics.** Apollo's `skip: true` prevents the request but still
  returns `data: undefined` — so the card must also be guarded in JSX. Doing only
  one of the two gives a card that renders for other users' profiles as an empty
  box, which is the misreading the spec forbids.
- **Concurrent sessions in this tree.** Another session wrote an untracked file
  into `internal/sdbimport/` previously. Explicit paths only.
- **Do not run `pnpm build`.** Hoisted linker; use
  `node node_modules/vite/bin/vite.js build`.
- **The §7.25 migration work is still uncommitted and blocked.** `git status`
  will show it. Leave it alone; do not sweep it into this commit.