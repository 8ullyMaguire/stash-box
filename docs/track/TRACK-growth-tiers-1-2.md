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
| W2 Public Elo | 4, 5 | `[~]` 4 done (682c3071), 5 pending | READ sees a leaderboard — **live-confirmed** |
| W3 Completion & coverage | 6, 9, 25, 59 | `[x]` **all four done** | live bar + archive page both verified |
| W4 Discovery surfaces | 19, 22, 44, 87 | `[~]` 44 done (already existed), 87 partial | trending verified live |
| W5 Discovery by relationship | 27, 12, 21, 57 | `[~]` 12 + 57 done | collages reachable; debut sort verified |
| W6 Spotlight & social proof | 11, 23, 46, 84 | `[ ]` | no coercive framing |
| W7 Search quality | 16, 69 | `[ ]` | alias query verified working |
| W8 Retention | 15, 30, 35 | `[ ]` | follow → notify round trip |

## Items, individually

| # | Idea | Built? (measured) | WS | Status |
|---|---|---|---|---|
| 1 | SEO public pages | **no** — needs a full SSR path | — | `[-]` removed |
| 2 | Identification board | **DONE** — `/identification` + `/identification/:id`, 17 tests | W1 | `[x]` |
| 3 | Browser extension | no — and out of scope | — | `[!]` |
| 4 | Public Elo leaderboards | **FIXED** — was behind the VOTE gate; now `/leaderboard`, ungated, 7 tests | W2 | `[x]` |
| 5 | Daily matchup micro-app | no — needs the D3 anonymous-vote decision | W2 | `[ ]` |
| 6 | Completion % + CTA | **DONE** (`faf5d556`) — backend was already complete; the UI was missing | W3 | `[x]` |
| 7 | Reddit bot | no — out of scope, abuse risk | — | `[!]` |
| 8 | Stash desktop sync | no — out of scope | — | `[!]` |
| 9 | State of the Archive | **DONE** (`f26c97c9`) — `/archive`, READ, live-confirmed | W3 | `[x]` |
| 10 | GraphQL playground | **YES, already mounted** at `/playground` (`server.go:203`, non-prod) | — | `[ ]` README only |
| 11 | Detective of the Week | no | W6 | `[ ]` |
| 12 | Collage previews | **DONE** (`dbc334d4`) — GraphQL type + migration 105; live-verified | W5 | `[x]` |
| 13 | "Because you liked X" | no — **blocked on W2** (taste vector first) | — | `[!]` |
| 14 | Email digest | no — **no mail transport exists** | — | `[!]` needs owner decision |
| 15 | Stewardship program | no | W8 | `[ ]` |
| 16 | Autocomplete | **largely built** — global `SearchField`; gap is alias-awareness | W7 | `[ ]` |
| 17 | OAuth | no — out of scope, security surface | — | `[!]` |
| 18 | API rate limiting | no — out of scope, security surface | — | `[!]` |
| 19 | Random scene | no — `SceneSortEnum` has no RANDOM; needs a real `ORDER BY random()` | W4 | `[ ]` |
| 20 | Dark mode + mobile | no — real, wanted, **its own workstream** | — | `[!]` |
| 21 | Performer timeline | no | W5 | `[ ]` |
| 22 | Scene of the Week | no — a *weekly window* is new; `trending` is a 7-day count, not a ranked pick | W4 | `[ ]` |
| 23 | Contributor profiles | partial — user pages exist, no public stats | W6 | `[ ]` |
| 24 | Tag hierarchy | no — vocabulary project | — | `[!]` |
| 25 | Studio completeness race | **DONE** (`9727dbf9`) — migration 97's table was read by nothing; real read path added | W3 | `[x]` |
| 26 | RSS feeds | no | — | `[ ]` |
| 27 | Similar performers | **DONE** (`0492229a`) — co-occurrence ranking over GraphQL; the score is decomposed into checkable facts, and a bare number is explicitly not for display | W5 | `[x]` |
| 28 | Shareable lists | no — needs moderation | — | `[!]` |
| 29 | Structured reviews | no — and **no review GraphQL type exists at all** | — | `[!]` |
| 30 | Follow + notify | no | W8 | `[ ]` |
| 32, 39, 40, 48, 54 | T3–6 strays | — | — | recorded, not adopted |
| 35 | Fingerprint merge candidates | clusters exist (migration 99) | W8 | `[ ]` |
| 44 | Trending | **ALREADY BUILT** — migration 60 matviews + hourly cron; `/scenes?sort=trending` verified live | W4 | `[x]` |
| 46 | "How I found it" stories | no | W6 | `[ ]` |
| 47 | OpenGraph | no — same SSR dependency | — | `[-]` removed |
| 50 | Community recruitment | not a build | — | `[!]` |
| 57 | Debut tracking | **ALREADY BUILT** — `PerformerSortEnum.DEBUT` + integration test; order verified live | W5 | `[x]` |
| 59 | "What's missing" per studio | **DONE** (`9727dbf9`) — same read path; uncounted studios get their own list | W3 | `[x]` |
| 69 | Tag synonyms | no alias table for tags | W7 | `[ ]` |
| 84 | Preservation hero | no | W6 | `[ ]` |
| 87 | "Most wanted" | partial — `sort=popularity` exists (all-time user_count); "wanted" implies a stated intent | W4 | `[~]` |

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

### W2 — Public Elo leaderboards (item 4) — `682c3071`

**This was a bug, not a feature.** The page existed, the query existed, and
`eloLeaderboard` is `@hasRole(role: READ)` in the schema — only `eloMatchup` is
`@hasRole(role: VOTE)` (`graphql/schema/types/elo.graphql:122,134`). The page was
mounted inside the curation tree, whose only nav entry is gated on `canVote`, so a
read-only user had **no route to a surface the schema already permitted them**.

That is SPEC §7.26's defect again, and it was sitting inside a list scored as
"already built".

**Live-confirmed before the fix**, with VOTE removed from a real user: the nav
showed every entry except Curation, and no leaderboard at all. The test encodes
that observation rather than the plan's intent.

**Fix:** `/leaderboard` as a top-level route with an ungated nav entry. The
curation route is untouched and still VOTE-gated, because `eloMatchup` genuinely
requires it — and two tests assert that, so the fix **cannot over-correct** into
showing a read-only user an unauthorized page.

**7 tests, 4 mutations, all caught:**

| Mutation | Caught by |
|---|---|
| re-gate the Leaderboard nav link on `canVote` (the original defect) | nav-for-READ test |
| delete the top-level route mount | route-table test |
| over-correct: also drop the `canVote` gate on Curation | curation-still-gated test |
| break the wrapper's path so the URL is right but the page is wrong | render test |

**Live after the fix** (READ-only user, seeded Elo data): nav shows
`Leaderboard -> /app/leaderboard`, `Curation` absent; `/leaderboard` renders rank
1580/60 votes, 1512/57, 1502/3. The vote-count column is the point of the page —
a rating is a mean over the votes cast, so 3 votes and 60 votes can differ by a
point while meaning very different things.

### W3 — Completion bars (item 6) — `faf5d556`

**My tracker was wrong about this one.** It said the completion scoring did not
exist. It did: the scoring service, the weights, and the GraphQL `Completion` type
on all five entities were already built and tested. The item was a *missing UI*,
not a missing backend — so this was built against the real schema instead of
against my plan's guess at it.

The plan had also assumed the bar would show "0 of 20 fields". It shows
**"0 of 80 weighted fields filled"**, because `total` is a weight sum (a birthdate
is worth 15, an eye colour 2). Rendering it as "0 of 80" reads as *80 fields are
missing* when 12 are. The schema says so outright — "so a client can render '55 of
110'" — and I still got it wrong first, and only saw it because the live page was
open next to the plan.

**Live:** `0% complete — 0 of 80 weighted fields filled`, the 12 missing fields
named in human labels, CTA a real anchor to `/performers/<id>/edit`.

**Two bugs found by verifying rather than assuming:**

1. `<Button as={Link}>` renders `<a role="button">`, which **overrides** the
   anchor's implicit link role. A screen reader announces "button" for something
   that navigates, and open-in-new-tab does not work. Fixed with a plain
   `<Link className="btn ...">`.

   **This is only observable to a `getByRole` query.** Every href assertion and
   `querySelector("a")` passes on the broken version. This repo had **zero** tests
   querying by role before this one — so "does this suite query by role at all?"
   is now a standing question for any new UI here.

2. `completion` is deliberately **not** in `PerformerFragment`: it is a resolver
   that reads the database per entity, so the shared fragment would fire one per
   row in every list view. Detail pages ask for it; lists do not.

**15 tests, 6 mutations, all caught.**

### W3 — expected-total denominators (items 25, 59) — `9727dbf9`

**The tracker was half right, and the half that was wrong mattered.** It said
"migration 97's table exists but is not exposed in GraphQL". True, and misleading:
`expected_totals` was read by **nothing at all** — not even the sqlc model struct
got used. So this was a read path to build, not a resolver to expose. Worth
flagging, because that tracker entry is exactly the shape that sends you to write
GraphQL plumbing while the missing piece is 120 lines of SQL.

**My first draft's relationships were wrong and the database said so:**

- there is no `performer_studio` table — performers reach a studio only through
  `scene_performers`, so the count is derived and only as good as the scene links
- images link via `studio_images`, not `images.studio_id`

Both are now verified against the live schema and documented in the query file.

**Three decisions, each with a test:**

- Rank by **absolute gap**, not ratio: 2-of-400 (398 missing) outranks 40-of-60
  (20 missing). A ratio-ordered board promotes the smaller opportunity, and a
  board with no clear leader is not a race.
- **Inner JOIN**, so an uncounted studio stays off the board. A LEFT JOIN puts it
  there with a NULL score, where it looks like a participant rather than an
  absence. Those studios get their own list — each is one sourced claim from the
  board.
- An absent denominator yields an **absent score** — never 100, never 0. That is
  exactly the failure §7.24.2's table exists to prevent.

**The bug this shipped, then fixed:** `studios.deleted` is
`BOOLEAN NOT NULL DEFAULT FALSE` (migration 06), so `WHERE deleted IS NULL`
matches **nothing**. Both list queries returned zero rows forever while every
"no error" test stayed green. Found by a probe that printed the inserted total and
then a join count of 0 — the signature of this class is **a successful write and a
silently empty read**.

**Two test lessons, recorded in the code:**

- The zero-denominator test was passing for the **wrong reason**: its INSERT
  omitted `asserted_at` (NOT NULL, no default), so `require.Error` was satisfied by
  23502 rather than `CHECK (total > 0)`. Both constraint tests now assert the
  constraint **name**. `require.Error` alone is satisfied by any failure at all.
- `assert.Equal(ids[0], bigGap)` depends on every other row in the database — it
  passed alone and failed in company. Now asserts the *relative* order of the two
  studios the test created, which is what the query promises.

**8 integration tests, 5 mutations, 5 caught.** The tests call `queries.New(...)`
rather than embedding the SQL, because a test that re-types the query under test
has tested the typing — the `deleted IS NULL` mutation left every SQL-embedding
test green.

### W3 — State of the Archive (item 9) — `f26c97c9`

The backend had the **numerator** (`countIncompleteEntities`, per type) but no
**denominator**. `findPerformers { count }` and its four siblings each apply their
own filters and soft-delete rules, so composing them into an archive total would
mean five round trips whose numbers could disagree with the completion counts shown
beside them — hence one five-row `UNION ALL`, `CountArchiveEntities`.

**Framing is the design.** A page whose headline is "X incomplete" is a debt
report. The headline is what is *done*; each type's missing count is secondary and
links to that type's list; nothing counts down or says "only X left" (§7.25).

**Three defects only the running app found:**

1. **`sites` has no `deleted` column.** It is a small fixed table of scrapers, not
   curated content. A uniform `NOT deleted` failed the whole field with
   `column "deleted" does not exist` — an error naming a *column*, which sends you
   looking for a dropped column rather than at the one table that never had one.
2. **Empty types rendered "Completion not measured" AND "No records yet"** on one
   card. No numerator because there is nothing to count is not a numerator that
   failed to load; emptiness is now checked first.
3. **The card clamped with `Math.min` but not `Math.max`, and the headline had the
   mirror-image gap** — so a negative aggregate rendered `aria-valuenow="-67"`.

**Two mutations were REDUNDANT, not survived** — and the code is better for it:

- the `Math.max(…, 0)` floor on the headline numerator is dead arithmetic (`pct`
  already clamps to 0..100; removing it changed nothing in 17 tests). Removed,
  because unreachable defensiveness makes the equivalent mutant survive *every*
  test and hides that the arithmetic is no longer what is checked.
- two `?? 0` fallbacks after a null-excluding filter are unreachable; replaced with
  a named type-guard predicate, which is also what lets TypeScript narrow.

**19 tests, 9 distinct mutations, all caught.** A mutation to the *headline* reducer
survived every one-type fixture — a single type makes the headline and its card the
same number. So there is now a two-type test of very different sizes (1/2 and
99/100 = 98%) where **neither card reads 98%**.

Live: `0% complete`, `0 of 6 catalogued`, per-type cards with counts and links, for
a READ-only user.

### W4 — discovery surfaces: the tracker was wrong about three of four

Checked before building, and three items turned out to already exist:

- **44 Trending — ALREADY BUILT.** Migration 60 creates `scene_popularity_trending`
  as a MATERIALIZED VIEW over `scene_fingerprints` with a **7-day** window, and
  `internal/cron/cron.go:183` refreshes it hourly. `SceneSortEnum.TRENDING` and
  `POPULARITY` are both implemented in `internal/service/scene/query.go:284-295`,
  and `SceneList` already reads `?sort=` — so Home's `/scenes?sort=trending` link
  works. My tracker said "needs a rollup table (D4)". The rollup table has existed
  since migration 60.

  Note: the matviews do **not** appear in `information_schema.tables` (they are in
  `pg_matviews`), so a first check reported them as absent — which is how this
  nearly got "built" a second time.

- **87 Most wanted — partial.** `sort=popularity` ranks by all-time `user_count`.
  A true "most wanted" is a stated intent, not an observed count.

- **22 Scene of the Week / 19 Random scene — genuinely absent.** A weekly pick is
  not `trending`: trending is a 7-day count, not a ranked selection, and
  `SceneSortEnum` has no RANDOM.

**Verified live** (after seeding submissions, `scripts/seed-trending.sql`):
`sort=trending` returns *Trending Leader* then *Quietly Submitted* and **omits** the
scene whose only submission is 200+ days old; `sort=popularity` and
`sort=created_at` order differently. So trending genuinely is not popularity.

**Two seed bugs, both instructive:**
- `scene_fingerprints` has no `fingerprint` column — identity lives in a separate
  `fingerprints(id, algorithm, hash)` table. The first draft failed on its first
  insert. `duration` and `vote` are also NOT NULL.
- `FROM users, generate_series(1,14)` is a **cartesian product**: 196 submissions,
  all three scenes scoring 14. Trending then looked *broken* rather than
  mis-seeded, which is the more misleading failure.

### W5 — item 12 collages: the service was finished, the schema was absent

My tracker said "service only — no GraphQL type". True, and it understated the
size of the gap: snapshots, generation, frame selection with a pure sampling
rule, and an under-snapshotted quest query **all existed with integration tests**,
and no client could reach any of it.

**Type binding could not go the obvious way.** gqlgen generates into
`internal/models`, so binding a GraphQL type to a service struct makes models
import the service → queries → models. The error is
`imports internal/service/collage ... import cycle not allowed`, naming four
packages and none of the cause. Hence hand-written model types plus resolver
conversion, the rule `ClusterSceneSubmission` already follows.

**Three schema-level mistakes, all caught before a client saw them:**

- default `frameCount: 10` — the service **rejects** it (§8's range is 12–24). Every
  client calling `generateCollage` without an explicit count would have failed, with
  an error naming a range the schema never mentioned. Now 16, off `DefaultFrames`.
- `fraction: Float!` — but a duration can be *unknown*, and `0.0` is a real position
  (the start of a scene). Non-null cannot express "unknown" without inventing the
  opening frame; erroring instead failed the whole query once per frame. Nullable now.
- `sceneCollage` returns null rather than an empty collage: "no collage yet" is an
  invitation, "zero frames" is a bug.

#### `stale` could never be true — the column that was never updated

Migration 78 added `current_duration_ms` beside `source_duration_ms` precisely so a
client could tell a collage generated before a duration correction from one
generated after. **Nothing ever updated it** — written once by the collage INSERT,
no trigger, no updater, no query. So `current == source` always, `stale` was
permanently false, and the diagnostic lived only in a comment.

Both tests asserting staleness read `false`. I assumed the tests were wrong before
checking `pg_trigger` and finding nothing.

Migration 105 fires on the **scene**, not the collage: the scene's duration is what
changes, and collages are what must learn about it. A trigger rather than service
code because the correction arrives via edits, imports, federation, the API and an
operator's `psql` — only a trigger catches all of them, which is precisely the path
that had already been missed. `NULL` duration maps to `NULL`, not 0, or every
durationless scene would read stale.

**The migration had to be replayable**, and that was not foresight:
`scripts/verify-105.sh` applies it directly, so the next server start found the
trigger present, died with `trigger already exists`, and **left nothing listening**.
A hand-applied migration with no `schema_migrations` row makes the app unbootable
unless the file is idempotent.

#### The mutation harness was lying — third time this session

`git checkout -- <untracked file>` is a silent no-op, so all nine mutations stayed
live, each contaminated the next, and the harness reported **"9 caught, 0 survived"
while measuring nothing**. It now snapshots files, refuses to mutate text it cannot
find, and aborts if a `MUTANT` marker survives a restore.

With honest results, three findings:

1. Removing the nil-duration guard on `fraction` **survived**. Unreachable from a
   scene that never had a duration (`Generate` returns `ErrNoDuration`); reachable
   only when a duration is *cleared* after a collage exists. Now tested.
2. My resolver's negative-timestamp guard was an **equivalent mutant** — the service
   already rejects negatives. Removed; the rule lives in one layer, and the mutation
   retargeted there.
3. `sed -i '/^\t"errors"$/d'` to drop an unused import **deleted the guard on the
   next line too**, and the test kept passing. Found by grepping for the guard the
   harness said was missing.

11 mutations, all caught. Live: 12 frames with exact fractions; after correcting
100s → 200s, `stale` reads true, `sourceDuration` stays 100000, and fractions
recompute (0.3 → 0.15).

### W5 audit — item 57 was already built too

Fifth tracker entry in a row whose "no" was wrong. `PerformerSortEnum.DEBUT`
exists in the schema, has an integration test
(`performer_integration_test.go:838`), and sorts performers by the minimum date of
their scenes.

**Verified live** (`scripts/seed-debut.sql`): Cy (2016-04-01) → Ada (2019-01-15)
→ Bo (2021-06-20), then the unknown-debut performers **last**. Two properties that
matter and are both observable:

- `birthdate` is deliberately in a *different* order from debut (Bo was born
  1980, Ada 1985), so a sort that secretly used birthdate is detectable.
- Performers with **no scenes** sort **last**, not first. An unknown debut is not an
  early career, and a naive `ORDER BY min(date)` puts NULLs first and claims
  otherwise.

**Schema fact worth writing down: there is no performer↔studio relationship.**
No `studio_performers` table and no `performers.studio_id` — checked
`information_schema` for every column matching `%studio%`. A performer reaches a
studio only indirectly, through `scenes.studio_id → scene_performers.performer_id`.
So item 27 (similar performers) cannot lean on a shared studio, and neither can
anything else that wants to recommend performers by affiliation.

### Standing verification state

**Build the frontend BEFORE the Go binary, or you ship a blank page.**
`frontend/embed.go` does `//go:embed build`, so the bundle is frozen INTO the binary at
compile time. Building Go first and the frontend second leaves the binary serving the
previous bundle: `index.html` then requests a hashed asset name that no longer exists,
the module 404s, and the SPA renders NOTHING — an empty `#root`, no links, HTTP 200 on
every route, and not one console error, because a failed module load logs nothing. I read
it as a broken feature for several turns. Correct order:

    cd frontend && node node_modules/vite/bin/vite.js build
    cd .. && go build -o /tmp/sbx ./cmd/stash-box

Confirm with `curl -s http://127.0.0.1:9999/ | grep -o 'index-[A-Za-z0-9_-]*\.js'`
and compare against `ls frontend/build/assets/` — if they differ, the binary is stale.
Re-running the Go build alone does not help; the embed is what has to be refreshed.

**The integration DSN is `stash-box-test`, and a wrong one fails SILENTLY.**
`POSTGRES_DB` must be
`postgres@127.0.0.1:55434/stash-box-test?sslmode=disable&password=smoke_pw`.
Point it at any other database and the suite does not error — `pgDropAll` drops the
tables, migrations recreate the schema, and the tests run against an archive with
**zero data**. Every assertion that expects rows fails with a plausible "expected 1,
got 0", which reads as a bug in whatever you just wrote rather than as a fixture that
never loaded. I lost several turns to this. Other databases on this host
(`sbx-live`, `sbx_scratch`, `sbx_v2`) exist and are not interchangeable; only
`stash-box-test` is repopulated by the harness.

**Integration tests need `-p 1`.** Packages share one database and each calls
`pgDropAll` on entry, so parallel packages drop each other's tables mid-migration
(`relation "performers" does not exist`). Six packages fail that way and it looks
like a regression in whatever you just changed. The Makefile already passes `-p 1`;
running `go test ./...` by hand does not.

- integration suite green: `go test -tags=integration -count=1 -p 1 ./...`

- frontend **737 tests / 60 files**, `validate` exit 0, vite build clean
- `go build` / `go vet` / `gofmt -l` clean, full `go test` clean
- live app on 127.0.0.1:9999, SPA at `/app`, board + leaderboard confirmed in a
  real browser for both READ and VOTE roles
- both remotes pushed and verified by SHA (`origin`, `forgejo`) via
  `scripts/publish.sh`

**Not done here (deliberately, both noted in the spec):** `postIdentificationQuery`
and `resolveIdentificationQuery` have no UI. Posting needs a form to describe a
half-remembered scene, which is a separate page; resolving needs the full thread
view. Both are in the schema and neither is reachable — recorded as the next
piece of W1 rather than stubbed.