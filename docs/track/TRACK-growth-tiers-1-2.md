# Tracker — growth surfaces, Tiers 1–2

Spec: `docs/spec/growth-tiers-1-2.md` · Plan: `docs/plan/growth-tiers-1-2.md`
Started: 2026-10-05 · Branch: `main`

**Read the spec's "The headline finding" first.** The source list marked eleven
items "already built"; measured against the tree, most are not. Scores below are
re-derived from the tree, not inherited.

Status key: `[ ]` not started · `[~]` in progress · `[x]` done + verified ·
`[!]` blocked, reason given · `[-]` removed from the roadmap

---

## Removed from the roadmap

| Item | Was scored | Why removed |
|---|---|---|
| 1 SEO public pages | 10 / 2 / **5.0**, #8 on "build first" | **Effort 2 is wrong by a wide margin.** `routes_frontend.go:39` serves `index.html` for every path — no SSR, no template layer. A real SEO surface is a second rendering path over the entity graph: templating, escaping, canonicals, structured data, sitemap pagination, cache headers. It also gated every other public surface, so building the rest without it forfeits most of their scored acquisition value. |
| 47 OpenGraph / Twitter cards | 6 / 1 / 6.0 | Same dependency. A single site-wide OG tag on the SPA shell is not a workstream. |

**Stated consequence:** nothing in this plan is discoverable by a search engine.
These are surfaces for people already using the instance. Re-add both if the
instance ever gets a real deployment and SSR becomes worth its cost.

---

## Workstreams

| WS | Items | Status | Gate |
|---|---|---|---|
| W1 Identification board UI | 2 | `[x]` 01d2c514, dcba9b10 | reachable + votable, mutation-verified |
| W2 Public Elo | 4, 5 | `[ ]` | READ-only user sees a leaderboard |
| W3 Completion & coverage | 6, 9, 25, 59 | `[ ]` | derived % correct, no drift |
| W4 Discovery surfaces | 19, 22, 44, 87 | `[ ]` | 4 routes render content |
| W5 Discovery by relationship | 27, 12, 21, 57 | `[ ]` | collage type exposed |
| W6 Spotlight & social proof | 11, 23, 46, 84 | `[ ]` | no coercive framing |
| W7 Search quality | 16, 69 | `[ ]` | alias query verified working |
| W8 Retention | 15, 30, 35 | `[ ]` | follow → notify round trip |

## Items, individually

| # | Idea | Built? (measured) | WS | Status |
|---|---|---|---|---|
| 1 | SEO public pages | **no** — needs a full SSR path | — | `[-]` removed |
| 2 | Identification board | **DONE** — `/identification` + `/identification/:id`, 17 tests | W1 | `[x]` |
| 3 | Browser extension | no — and out of scope | — | `[!]` |
| 4 | Public Elo leaderboards | query yes, **page behind the VOTE gate** — a §7.26-shaped bug | W2 | `[ ]` |
| 5 | Daily matchup micro-app | no | W2 | `[ ]` |
| 6 | Completion % + CTA | **no** — no per-entity % in the schema at all | W3 | `[ ]` |
| 7 | Reddit bot | no — out of scope, abuse risk | — | `[!]` |
| 8 | Stash desktop sync | no — out of scope | — | `[!]` |
| 9 | State of the Archive | no | W3 | `[ ]` |
| 10 | GraphQL playground | **YES, already mounted** at `/playground` (`server.go:203`, non-prod) | — | `[ ]` README only |
| 11 | Detective of the Week | no | W6 | `[ ]` |
| 12 | Collage previews | service only — no GraphQL type | W5 | `[ ]` |
| 13 | "Because you liked X" | no — **blocked on W2** (taste vector first) | — | `[!]` |
| 14 | Email digest | no — **no mail transport exists** | — | `[!]` needs owner decision |
| 15 | Stewardship program | no | W8 | `[ ]` |
| 16 | Autocomplete | **largely built** — global `SearchField`; gap is alias-awareness | W7 | `[ ]` |
| 17 | OAuth | no — out of scope, security surface | — | `[!]` |
| 18 | API rate limiting | no — out of scope, security surface | — | `[!]` |
| 19 | Random scene | no | W4 | `[ ]` |
| 20 | Dark mode + mobile | no — real, wanted, **its own workstream** | — | `[!]` |
| 21 | Performer timeline | no | W5 | `[ ]` |
| 22 | Scene of the Week | no | W4 | `[ ]` |
| 23 | Contributor profiles | partial — user pages exist, no public stats | W6 | `[ ]` |
| 24 | Tag hierarchy | no — vocabulary project | — | `[!]` |
| 25 | Studio completeness race | table exists (migration 97), **not exposed in GraphQL** | W3 | `[ ]` |
| 26 | RSS feeds | no | — | `[ ]` |
| 27 | Similar performers | no — cheapest recommendation available | W5 | `[ ]` |
| 28 | Shareable lists | no — needs moderation | — | `[!]` |
| 29 | Structured reviews | no — and **no review GraphQL type exists at all** | — | `[!]` |
| 30 | Follow + notify | no | W8 | `[ ]` |
| 32, 39, 40, 48, 54 | T3–6 strays | — | — | recorded, not adopted |
| 35 | Fingerprint merge candidates | clusters exist (migration 99) | W8 | `[ ]` |
| 44 | Trending | no — needs a rollup table (D4) | W4 | `[ ]` |
| 46 | "How I found it" stories | no | W6 | `[ ]` |
| 47 | OpenGraph | no — same SSR dependency | — | `[-]` removed |
| 50 | Community recruitment | not a build | — | `[!]` |
| 57 | Debut tracking | no — `min(date)` | W5 | `[ ]` |
| 59 | "What's missing" per studio | table exists, not exposed | W3 | `[ ]` |
| 69 | Tag synonyms | no alias table for tags | W7 | `[ ]` |
| 84 | Preservation hero | no | W6 | `[ ]` |
| 87 | "Most wanted" | no | W4 | `[ ]` |

---

## Resolved blockers

- **§7.25 recall migration** — shipped `33458d57`. sqlc's refusal to see
  `ALTER TABLE scene_search` worked around by declaring the columns in migration
  61's `CREATE TABLE`; three triggers mutation-verified; down→up round trip green.

## Open questions for the owner

1. **Item 14 (email digest) needs a mail transport.** Nothing sends SMTP today.
   Ask before building; do not pick one silently.
2. **Item 5 (anonymous voting).** The spec's D3 loosens `voteElo` to READ and
   attributes votes to a rotating anonymous `trust_events` row expiring in 30
   days. That is me choosing *against* the list's "no friction" framing, because an
   unattributable mutation on a shared leaderboard is a correctness problem first.
   One commit to reverse.
3. **Editing migration 61** breaks its checksum for any database created before
   `33458d57`. Every database in this project is a scratch DB, so nothing is
   blocked today — but it is a real cost if that changes.

## Not adopted from Tiers 1–2, and why

Items 1 and 47 are **removed** (see the table at the top). Everything else in this
section was considered and left out on purpose:

Browser extension, Reddit bot, Stash sync — separate products, or (for the bot)
automating replies on a third-party platform, which is the highest abuse-risk
item in the list and is not mine to authorise. OAuth and rate limiting are
security surfaces and do not belong inside a growth workstream. Dark mode is
table stakes for existing users, not acquisition, and deserves its own plan.
Collaborative filtering (13) is blocked on Elo work landing first. Item 14 needs
a mail transport decision. Tag hierarchy (24) and structured reviews (29) are
vocabulary projects; reviews are larger than item 29 implies, since **no review
type exists in the schema at all**.

---

## Verification log

Append one line per workstream completion: what was built, which mutations were
applied and caught, and what was measured. A workstream with tests but no
mutation record is not verified, only exercised.

### W1 — Identification board UI — `01d2c514`, `dcba9b10`

Built `/identification` (open queue) and `/identification/:id` (candidates,
voting). GraphQL documents, three hooks, three mutations, nav entry, route mount.

**17 tests, 13 mutations applied, 13 caught.** Each mutation is named in a
comment beside the test that catches it:

| Mutation | Caught by |
|---|---|
| drop the `canVote` gate on the vote button | role-gating test |
| ignore `votedByMe` (always actionable Vote) | "Voted as a state" test |
| delete the candidate note | note-rendering test |
| render a deleted entity as a live link | null-entity test |
| blank the description on the list | list-content test |
| drop the empty state | empty-state test |
| coercive urgency in the age line | no-coercive-framing test |
| gate the nav link on `canVote` | nav reachability test |
| point the nav at `/curation` | nav href test |
| delete the route mount | route-table test |
| nest the route inside the curation branch | route-table test |
| collapse `:id` onto the list path | route-declaration test |
| drop the id from the question href | **new** link-href test |

**The last one was found by mutation, not by reading.** Replacing
`replace(":id", query.id)` with `replace(":id", "")` left every other test green —
the description still rendered, the row still existed, and the user simply
landed back on the list page. Silent. The test that now guards it did not exist
when the first mutation pass ran; that is the process working, not a gap in it.

**Two mutations failed to apply on first attempt** because biome had reformatted
the target text. The lesson recorded: read the committed bytes for a mutation
target rather than assuming the pre-format string still matches.

**Gate:** `validate` exit 0 · frontend 713/713 across 58 files · `vite build`
clean · `go build`/`go vet`/`gofmt -l` clean · `go test` clean.

### Live verification (browser, real server, real database)

Run against `sbx-live` — a **separate database from `sbx-scratch`**, because the
integration suite calls `pgDropAll` at teardown and wipes everything it shares.
Using the scratch DB meant the live user and seed vanished after every test run,
which reads as "login is broken".

Server: `scripts/` + `/tmp/sbx --config_file .config-dev/config.yml` on
127.0.0.1:9999, SPA mounted at `/app`. Four config traps, all of which report a
symptom pointing somewhere else, recorded in the config file itself:

| Mistake | Symptom | Real cause |
|---|---|---|
| `database:` as a nested host/port block | `dial tcp [::1]:5432: connection refused` | one DSN string key, not a block |
| DSN **with** `postgres://` | `lookup postgres: no such host` | `database.Initialize` prepends the scheme |
| `host: http://127.0.0.1:9999` | `too many colons in address` | `host` is a bare host |
| `frontends[].prefix: ""` | `skipping frontend with ... reserved prefix` | `""` and `"/"` are both rejected |
| no `VITE_BASE_PATH` on the vite build | every route redirects to `/login` | `BrowserRouter basename={BASE_URL}` |

The last one is a §7.26 cousin: a **stale/wrong-base build renders every route
blank at HTTP 200**, and the redirect looks like an auth problem.

**Confirmed in the browser, not inferred:**

- board list shows the seeded question, "asked today", and the summed tally
- detail page shows both candidates, the note, the entity link, and the
  deleted-entity branch ("suggested entity no longer exists")
- a VOTE user gets a **Vote** button per candidate
- a READ user gets the whole thread, the tally, the note, the explanatory line,
  and **zero** vote buttons
- the nav shows **Identify** for READ and **hides Curation** — the canVote gate,
  and live confirmation of the W2 item-4 defect (leaderboard sits behind it)

### Three backend defects the live check found, all tests green

Every one of these passed the full unit and integration suite before the browser
was opened. All three are the same shape: **a value exists in the database and
never crosses the API boundary.**

1. `Query.CreatedAt` never copied → `createdAt: Time!` null → the **entire**
   `listOpenIdentificationQueries` query errored, board was an error page.
2. `ListOpenIdentificationQueries` never called `withCandidates` → every row had
   an empty candidate list, so the board claimed "no suggestions yet" about
   questions that had two.
3. `identification.Candidate` had **no** `CreatedAt` field at all → same
   non-null failure one level deeper, at `candidates[0].createdAt`.

(3) is the instructive one. The new test **passed in isolation and failed in the
full run**, at list index 1 — index 0 was the query the test itself posted with no
candidates. The obvious read is "another test polluted mine". It was not: the
assertion was working and the isolated pass was the accident. **A test that only
fails in company is still a real failure**, and explaining it away as interference
would have shipped a board that was broken for every question with a suggestion.

All three are mutation-verified against the exact production error text.

**Not done here (deliberately, both noted in the spec):** `postIdentificationQuery`
and `resolveIdentificationQuery` have no UI. Posting needs a form to describe a
half-remembered scene, which is a separate page; resolving needs the full thread
view. Both are in the schema and neither is reachable — recorded as the next
piece of W1 rather than stubbed.