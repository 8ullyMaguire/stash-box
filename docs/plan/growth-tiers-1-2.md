# Growth surfaces, Tiers 1–2 — PLAN

Spec: `docs/spec/growth-tiers-1-2.md`. Written before implementation, per the
standing spec-first rule.

Branch: `main`. §7.25 landed as `33458d57`, so this plan starts from a clean
backend and the migration blocker is resolved.

---

## Removed from this plan

**Item 1 (SEO public pages) and item 47 (OpenGraph/Twitter cards) are removed,
not deferred.** The list scored item 1 at impact 10 / effort 2 and ranked it the
single largest traffic source. Measured, `internal/api/routes_frontend.go:39`
serves `index.html` for every unmatched path: there is no SSR, no template layer
and no per-route HTML. A real SEO surface is a second rendering path over the
entity graph — templating, escaping, canonicals, structured data, sitemap
pagination, cache headers — which is a project.

It also gated everything else: it was the template the other public surfaces
rendered through, so building the rest first without it means those items lose
the acquisition rationale that was most of their scored impact.

**Consequence, stated so it is not discovered later: nothing in this plan is
discoverable by a search engine.** These are surfaces for people already using
the instance. Item 10 stays a README paragraph — `/playground` is already mounted
at `server.go:203`, gated on non-production.

## The one structural fact that shapes everything

Every unmatched path returns `index.html` with **HTTP 200**
(`routes_frontend.go:39`). Therefore:

> **A status code is not evidence that a page works.** Any route test in this
> plan asserts *rendered content*, never a 200.

This is the §7.26 failure mode in a new place, and it is why "wire up a route"
and "the page works" are separate claims throughout.

There is no server-side auth redirect to design around either: `routes_frontend.go`
carries no auth middleware, so the login check happens in JavaScript after the
shell loads.

---

## Sequencing, and why

```
W1 Identification board UI     ← highest remaining impact ÷ effort, no deps
W2 Public Elo surfaces         ← item 4 is a BUG (VOTE gate), fix first
W3 Completion & coverage       ← backend first; the API has no per-entity %
W4 Discovery surfaces          ← 4 small queries, needs a rollup table for trending
W5 Discovery by relationship   ← needs a collage GraphQL type (service exists)
W6 Spotlight & social proof     ← independent
W7 Search quality               ← independent
W8 Retention                    ← needs a follow table; email digest EXCLUDED
```

W1 first: ten query fields with no page is the largest single gap in the adopted
set, and it is a pure frontend build over a finished backend.

W2 before W3 despite W3 scoring higher, because item 4 is a **reachability bug**
of exactly the kind §7.26 just fixed — `EloLeaderboardPage.tsx` exists but sits
inside the `canVote`-gated `/curation` tree, so a READ-only user cannot see a
leaderboard. That is a regression-shaped defect sitting in a "growth" list, and
it should not queue behind three new pages.

---

## W1 — Identification board UI (item 2)

Backend: 10 query fields (`graphql/schema/types/identification.graphql:181-262`),
service `internal/service/identification`, migration 79. **Frontend: nothing.**

| File | Contents |
|---|---|
| `frontend/src/pages/identification/index.tsx` | `/identification` and `/identification/:id` |
| `frontend/src/pages/identification/IdentificationBoard.tsx` | open queries, paginated |
| `frontend/src/pages/identification/IdentificationQuery.tsx` | one query + candidates + vote |
| `frontend/src/graphql/queries/Identification.gql` | the fields as used |
| `frontend/src/graphql/queries/index.ts` | `useIdentificationBoard`, `useIdentificationQuery`, `usePostIdentificationQuery`, `useVoteIdentificationCandidate`, … |
| `frontend/src/constants/route.ts` | `ROUTE_IDENTIFICATION`, `ROUTE_IDENTIFICATION_QUERY` |
| `frontend/src/pages/index.tsx` | mount |
| `frontend/src/Main.tsx` | nav entry |

**Gate:** navigate to `/identification` and see open queries; vote on a candidate
and see the count change on reload. Mutate by removing the nav entry — the route
test must go red. Do not accept "the page renders" as the check for reachability.

---

## W2 — Public Elo (items 4, 5)

**Item 4 first, as a bug fix.** Move `EloLeaderboardPage.tsx` out of
`frontend/src/pages/curation/index.tsx` to `/leaderboard/:entityType`, ungated,
with a nav link outside the `canVote` check. Test = the §7.26 pattern: render as
a READ-only user, assert the leaderboard is present. Mutation: re-add
`canVote(user) &&` around the nav link.

**Item 5 (daily matchup)** needs the D3 decision — `voteElo` is
`@hasRole(role: VOTE)`. Per the spec: loosen to READ and attribute the vote to a
rotating anonymous `trust_events` row expiring in 30 days. Schema + service, so
migration `104`. **Owner decision pending** (see tracker).

---

## W3 — Completion & coverage (items 6, 9, 25, 59)

Backend first; the frontend cannot render a number the API does not have.

1. **Per-entity completion percentage** on performer, scene, studio, site, tag.
   Derived, never stored (D2). The per-entity score already exists in
   `completion.sql` — **expose it, do not recompute it**.
2. **`expectedTotals` exposure** — migration 97 created the table and nothing
   outside SQL can read it. Items 25 and 59 both depend on this.
3. **`archiveSummary`** — one aggregate backing item 9.
4. **Frontend** — completion bar + "help complete this" per entity page; State of
   the Archive at `/archive`.

**Verification:** a scene at 40% reports 40, **and editing a field changes it on
the next read.** The second half is D2's whole requirement — a stored percentage
drifts silently.

---

## W4 — Discovery surfaces (items 19, 22, 44, 87)

Four queries, one page each: `/random`, `/scene-of-the-week`, `/trending`,
`/most-wanted`.

`trending` needs a **materialised view** (D4): a daily-refreshed rollup
(`104_trending_rollup.up.sql`), not a live aggregate over `trust_events`.
**Check for an existing scheduler before adding one** — `internal/service/` has
none, so this may be net-new, and that is worth flagging rather than assuming.

---

## W5 — Discovery by relationship (items 27, 12, 21, 57)

- **12 (collage preview)** needs a GraphQL collage type. `internal/service/collage`
  exists; `ollage` appears in no entity type. Expose `scene.collage` (millisecond
  offsets + frame count) and hover-scrub it on the existing `SceneCard`.
- **27 (similar performers)** — co-star overlap, one query over
  `scene_performers`. **Cheapest recommendation available; do before W2's
  "because you liked X" work**, which is deferred.
- **21 (career timeline)** — group `scenes.date` by year. Query only.
- **57 (debut)** — `min(scenes.date)` per performer. Query only.

---

## W6 — Spotlight & social proof (items 11, 23, 46, 84)

Four queries over existing tables: top solver this week, contributor stats, solve
narratives, preservation-host counts.

**Line to hold, same as §7.25:** a spotlight states a fact ("X solved 7 this
week"). No countdown, no "you are falling behind", nothing that manufactures
loss.

---

## W7 — Search quality (items 16, 69)

- **16 is largely built.** A global `SearchField` with as-you-type performer
  search is in the nav on every page and is the engine behind `PerformerSelect`;
  `react-select` 5.8.3 is installed. **Verify first with a real alias query** — if
  `studio_aliases` matching already works, this workstream is a test, not a
  feature. Do not build on the list's assumption.
- **69 (tag synonyms)** — `tags` has no alias table; `studios` has
  `studio_aliases`. Copy that shape. Migration `105`.

---

## W8 — Retention (items 15, 30, 35)

**Item 14 (email digest) is excluded from this plan** — nothing sends SMTP today
and picking a mail transport is the owner's call, not an assumption to bake in.

- **30 (follow + notify)** — `follows` table + notification type. Do first here.
  `internal/service/notification` exists; follow and webhook do not.
- **15 (stewardship)** — a steward claim on performer/studio + change notifications.
- **35 (merge candidates)** — fingerprint clusters exist (migration 99). Surfacing
  them as voteable merges is a UI plus a query over existing data, **not** new
  duplicate detection.

---

## Per-workstream gate

Every workstream, without exception:

```bash
cd ~/code-local/go/stash-box
go build ./... && go vet ./... && gofmt -l ./internal/          # empty
go test $(go list ./... | grep -vE 'internal/image$') -count=1
POSTGRES_DB='postgres:smoke_pw@127.0.0.1:55434/sbx-scratch?sslmode=disable' \
  go test -tags=integration -count=1 ./internal/api/
cd frontend && pnpm run validate && pnpm vitest run
node node_modules/vite/bin/vite.js build
```

Plus **one applied mutation per new test**, recorded in the commit message. The
§7.26 finding is why: three wiring tests were green and wrong in a row, and only
the mutation revealed it.

---

## Risks

- **Migration numbering.** 103 is taken; next is `104`. Apply with
  `scripts/provision-scratch-db.sh`, which sorts correctly — see its header for
  why `sort -t_ -k1,1n` on full paths is wrong.
- **Do not run the integration suite while mutating the database.** Shared
  database; it invalidates both.
- **`pnpm build` is wrong here** — hoisted linker. Use
  `node node_modules/vite/bin/vite.js build`.
- **A stale frontend build renders every route blank at HTTP 200**, which is
  indistinguishable from a working page on the status code alone. Rebuild before
  checking anything in a browser.
- **Editing migration 61 changed its checksum.** Databases created before
  `33458d57` will refuse to start until 61 is re-applied or the checksum
  repaired. All databases in this project are scratch DBs, so nothing is blocked
  today.
- **Item 14 needs an owner decision** on mail transport. Flagged, not assumed.