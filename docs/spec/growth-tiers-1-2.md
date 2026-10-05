# Growth surfaces, Tiers 1–2 — SPEC

Status: **Written 2026-10-05, before implementation.** Source: a 100-idea list
ranked by impact ÷ effort, of which this adopts **Tiers 1 and 2 (items 1–30)**.
Items 31–100 are recorded in `docs/ideas/growth/` and deliberately **not** adopted
here; §"Not adopted" says why for the ones that were tempting.

Branch: `main` at `3b6b4ffe`.

---

## The headline finding: the list's "already built" column is largely wrong

The list marks eleven items "already built" and rates them cheap. I checked each
against the tree instead of carrying the claim forward, because a spec that
inherits an unchecked claim inherits a wrong plan.

**The GraphQL surface is 15 query fields wide.** Enumerated from
`graphql/schema/types/*.graphql`:

| Area | Fields |
|---|---|
| Elo | `eloMatchup`, `eloRating`, `eloLeaderboard`, `voteElo` |
| Completion | `countIncompleteEntities` |
| Identification | 10 (`listOpenIdentificationQueries`, `postIdentificationQuery`, `voteIdentificationCandidate`, `resolveIdentificationQuery`, …) |
| Everything else | 0 gamification/discovery fields |

**No `extend type Query` field exists for**: completion percentage on an entity,
`expectedTotals`, collages, reviews, trending, random, notifications-for-entities,
or a "state of the archive" aggregate.

### Claims checked, and the result

| # | List's claim | Measured |
|---|---|---|
| 2 | identification board "already built" | **Backend yes, frontend NO.** 10 query fields and `internal/service/identification` exist. `frontend/src/pages/` has **no** identification directory — the board has no page and no route. |
| 6 | completion % "already built, flip a switch" | **NO.** `countIncompleteEntities` returns a count of *incomplete* records. There is no per-entity completion percentage anywhere in the schema. |
| 9, 25 | `expected_totals` basis | **Table exists** (migration 97), **not exposed in GraphQL.** Nothing outside SQL can read it. |
| 12 | collages "built" | **Service yes, no GraphQL type, no frontend.** `internal/service/collage` exists; `ollage` appears only in `identification.graphql` and `completion.graphql`, never as an entity type. |
| 4 | Elo leaderboards "built" | **Query yes, page no.** `EloLeaderboardPage.tsx` exists and is routed under `/curation/leaderboard`, which is inside the `canVote`-gated curation tree. **A READ-only user cannot see a leaderboard**, the same defect class as §7.26. |
| 16 | autocomplete "table stakes, not built" | **Substantially BUILT.** A global `SearchField` (as-you-type, `SearchType.Performer`) is in the nav on every page and is the engine behind `PerformerSelect`. `react-select` 5.8.3 is installed. What is missing is alias-awareness in results. |
| 3, 7, 8, 45, 70 | extension / Reddit bot / Stash sync / Discord bot / scene-match | **Confirmed absent.** No `manifest.json`, no browser-extension code. Out of scope here regardless — each is a separate product. |
| 17, 18, 20 | OAuth / rate limiting / mobile | **Confirmed absent.** No auth provider beyond invite+email, no rate limiter, and `User.tsx` uses `col-lg-10`/`Col` with no `container`/`col-md` responsive audit. |
| 26, 44, 47 | RSS / trending / OpenGraph | **Confirmed absent.** |

### What this changes

The list's own conclusion was *"the highest-scoring ideas are not features, they
are surfaces for data the fork already has — the work is exposure, not
construction."* **That is right about the data and wrong about the surfaces.**
The data exists; the surfaces mostly do not. So the work is *more* construction
than the scores assume, and the scores are optimistic by roughly the ratio of
"missing page" to "missing query".

**Adopted framing: build the surfaces, in the order that needs the least new
machinery first.** Every adopted item is a page over a query that either exists
or is one aggregation away — with the single exception of the removal above.

---

## Scope

**In — Tier 2 (20) and the Tier 1 items that are genuinely cheap, as 8
workstreams.** Items 1 and 47 are **removed from the roadmap entirely**, not
deferred; see "Removed, not deferred" below.

- **W1 Identification board UI** — item 2. The board's 10 queries have no page.
  **Now first**, because it is the highest remaining impact ÷ effort and nothing
  else depends on it.
- **W2 Public Elo surfaces** — items 4, 5. Leaderboard out of the VOTE gate;
  anonymous daily matchup.
- **W3 Completion & coverage** — items 6, 9, 25, 59. Per-entity completion %,
  "help complete this", State of the Archive, studio race.
- **W4 Discovery surfaces** — items 19, 22, 44, 87. Random, Scene of the Week,
  trending, most-wanted.
- **W5 Discovery-by-relationship** — items 27, 12, 21, 57. Similar performers,
  collage preview, career timeline, debut.
- **W6 Spotlight & social proof** — items 11, 23, 46, 84. Detective of the
  Week, contributor profiles, solve stories, preservation hero.
- **W7 Search quality** — items 16 (alias-aware), 69 (tag synonyms).
- **W8 Retention** — items 15, 30, 35. Stewardship, follows + notifications,
  fingerprint merge candidates.

**Removed, not deferred — items 1 and 47.**

The list scored SEO public pages at **impact 10 / effort 2 / score 5.0**, ranked
it the single largest traffic source, and put it at #8 in the "build first" list.
Measured: **effort 2 is off by a wide margin.**

`internal/api/routes_frontend.go:39` serves `index.html` for *every* unmatched
path. There is no SSR, no template layer, and no per-route HTML. An SEO surface
is therefore not a matter of adding `<meta>` tags to existing pages — it is a
second rendering path over the entity graph: templating, escaping, canonical
URLs, structured data, pagination over every table, cache headers, and a sitemap
walker that does not time out on real data. That is a project, not a task.

**And it would gate everything else.** W1 was the template every public surface
rendered through, so building W2–W6 first without it means those items lose the
SEO rationale that was most of their scored impact — a real loss, and not one
worth paying for an unmeasurable gain.

**Decision: removed.** The remaining items are worthwhile as in-app surfaces for
existing users. Their acquisition value is lower than the list claimed, and this
SPEC says so rather than quoting scores that no longer describe the work.

Item 47 (OpenGraph/Twitter cards) goes with it: per-page OG metadata needs the
same server-rendered surface, and a single site-wide OG tag on the SPA shell is
not worth a workstream.

Item 10 is already mounted at `/playground` (`server.go:203`) and stays a
README paragraph, folded into W1's documentation.

**Out — the rest of Tier 1/2, with reasons:**

| # | Idea | Why not now |
|---|---|---|
| 3 | Browser extension | Separate product: review policy, update cadence, two stores. Not a growth surface we can ship and forget. |
| 7 | Reddit bot | Automating replies on a third-party platform is the single highest-abuse-risk item in the list. Needs the owner's explicit call, and it is not a build. |
| 8, 70 | Stash desktop sync / scene match | Requires reverse-engineering another app's library format. Weeks, and unverifiable without their source. |
| 10 | GraphQL playground | **Half built and already deployed.** `internal/api/server.go` serves one; it is merely undocumented. A README section, not a workstream. Folded into W1's docs. |
| 13 | "Because you liked X" | Collaborative filtering. §7.4 mandates it; it needs the taste vector populated first, which is W3's job. **Blocked on W3.** |
| 17 | OAuth | Auth is the security surface. Not bundled into a growth workstream. |
| 18 | Rate-limited API tier | Idempotent with OAuth decisions; also a security surface. |
| 20 | Dark mode + mobile | Real and important, and **not a growth surface** — it is table stakes for existing users. Its own workstream, and a large one. |
| 24 | Tag hierarchy | A vocabulary project. §7.25.4/69 is the cheap 80%; hierarchy is the expensive remainder. |
| 28 | Shareable lists | UGC + moderation. W2's board proves the pattern cheaply first. |
| 29 | Structured reviews | **No review GraphQL type exists at all** — the list says "§7.10 mandates reviews" and the schema has none. That is a much larger gap than item 29 implies. |
| 32, 39, 40, 48, 54 | T3–T6 strays | Recorded, not adopted. |

---

## Decisions

### D1 — Ship as SPA routes, verified by rendering

With no SSR (measured above), every surface in this plan is an SPA route. The
consequence for verification: **a mounted route returning HTTP 200 proves
nothing**, because the catch-all serves `index.html` with 200 for every path.

So each new route is verified by asserting its rendered content in a test, not by
asserting a status code. This is the §7.26 lesson applied forward — the failure
mode is a route that exists and serves nothing.

### D2 — Completion % is derived, never stored

§7.7's completion score is computed on read. A stored percentage would drift from
the fields it summarises and nothing would report the disagreement — the same
fault as §7.25.2's drift check, and the reason §7.24.1 needed verified-unknown
markers. Derived, and recomputed per request.

### D3 — Anonymous voting is allowed; anonymous *identity* is not

Item 5 asks for a daily matchup needing no account. `voteElo` is `@hasRole(role:
VOTE)`. Options: (a) loosen the gate, (b) issue an anonymous session.

**Chosen: loosen the gate to READ, and attribute the vote to a rotating
anonymous `trust_events` row that expires.** Rationale: the Elo update needs an
actor for the audit trail, and an audit trail row that expires in 30 days is
strictly better than an unattributable vote that cannot be rolled back at all.
This is the one place I am choosing *against* the list's "no friction" framing,
because an unattributable mutation on a shared leaderboard is a correctness
problem before it is a conversion problem. **Reversible in one commit if the
owner disagrees.**

### D4 — Trending is a materialised view, not a live aggregate

Item 44. A live aggregate over `trust_events` is a full scan per homepage view.
A daily-refreshed rollup table is one indexed read. Same reasoning as D2.

### D5 — No ML, no embeddings, anywhere in Tier 1–2

§7.25.7 rejects the ML stack. Items 74, 96, 49 are deferred for the same reason
and stay deferred here.

---

## Non-goals

- **No dark mode.** Real, wanted, and a separate workstream (item 20).
- **No OAuth, no rate limiter.** Security surfaces, deliberately not bundled.
- **No federation-dependent item.** 41, 73, 89 need the mesh; §7.2 is Phase 4.
- **Nothing that fabricates scarcity, urgency or loss.** Same line as §7.25's
  "Detective of the Week": a spotlight is a *fact about who did the work*, never
  a countdown.

---

## Completion bar

| Check | Command | Expected |
|---|---|---|
| Backend build | `go build ./...` | clean |
| Unit suite | `go test $(go list ./... \| grep -vE 'internal/image$') -count=1` | all ok |
| Integration | `go test -tags=integration -count=1 ./internal/api/` | pass against real PG |
| Frontend validate | `pnpm run validate` | exit 0 |
| Frontend suite | `pnpm vitest run` | green |
| Build | `node node_modules/vite/bin/vite.js build` | succeeds |
| **Each new route renders** | vitest asserting the route's own content | content present, not just HTTP 200 |

That last row is the one this SPEC exists to enforce. **Not a curl for 200** —
`routes_frontend.go:39` serves `index.html` with 200 for every path, so a status
code is indistinguishable from a blank page.

## Risks

- **sqlc cannot see columns added to `scene_search` by a later migration.** W8
  depends on §7.25, which is still blocked on that decision. See
  `docs/track/HANDOFF-scene-recall-2026-10-05.md`.
- **A stale frontend build renders every route blank at HTTP 200.** Rebuild
  before browser-verifying, or "it loads" is not evidence.
- **No SSR means no crawlable content.** Nothing in this plan is discoverable by
  a search engine. That is accepted, not solved; see "Removed, not deferred".
- **`pnpm build` is wrong here** — hoisted linker. Use
  `node node_modules/vite/bin/vite.js build`.