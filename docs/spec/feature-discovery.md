# SPEC — discovery (aggregator programme, phase 7)

Owner priority, 2026-10-14: *"content aggregation and discovery (algorithms, search, etc)
should be the priority"*. Phase 4 built the tag graph this phase queries, and
`docs/spec/feature-tag-ontology.md` §4.1 is the expansion the search already applies.

Source draft: `~/secondbrain/10-Projects/nexusxxx/2026-10-14-nexusxxx-spec-v2.md` §8.1–§8.9.
Parity rows: `docs/spec/feature-content-aggregator.md` §3 — `§8.1–8.4` (eight modes, the query
language, trending/freshness), `§8.5` (content DNA, similarity, explanations), `§8.6` (taste
profile, For You), `§8.7–8.8` (social discovery, serendipity), `§8.9` ("where I left off").

The section point of the draft: *"discovery is not just search — it's a multi-layered system
that helps users find content through at least six distinct mechanisms, each explainable and
controllable."* **Explainable** and **controllable** are the two words that decide the design
below: every mode either says why it returned something, or lets a person change the answer.

## 1. The eight modes, and where each one lands

| NexusXXX §8.1 mode | Here | Slice |
|---|---|---|
| Tag search (boolean, against the ontology) | the query language, §4 | 1 |
| Curated feeds (saved searches + playlists) | saved searches, §5 | 2 |
| Tag explorer (browse the hierarchy, counts, co-occurrence) | §6 | 3 |
| Trending (time-decayed popularity) | §7 | 4 |
| Similar to (vector similarity on content DNA) | §8 | 5 |
| For You (taste profile, hybrid) | §9 | 6 |
| Serendipity (random with constraints) | §9 | 6 |
| Social discovery (trusted users' activity) | §10 | 7 |
| "Where I left off" | §11 | 7 |

Search that is more than free text is the point of the first slice, because it is the one mode
that needs no new signal, no new table and no new algorithm: the ontology already answers it,
and everything else in this phase is ranked BY it.

## 2. What exists, measured

Measured against `sbx-scratch` (the full migration chain) and the tree, 2026-10-14:

- **Free text search is real and ranked**: `scene_search` is a ParadeDB BM25 index
  (`pg_search 0.21.8`), rebuilt by migrations 35, 56, 61 and extended additively by 103 to
  carry `scene_details`, `scene_director` and `tag_names`. `Scene.SearchScenesWithCount(term,
  limit, offset)` queries it, and `Scene.Drift()` is the drift check that says whether the
  index still matches its base tables. `performer_search`, `studio_search` and `tag_search`
  exist alongside it.
- **`SceneQueryInput.Text` already exists** (`json:"text"`): the free-text term is a field of
  the same filter struct everything else lives in.
- **Structured filters already exist** and already expand through the tag graph:
  `SceneQueryInput` carries `Tags`, `Performers`, `Studios`, `ParentStudio`, `ID`,
  `Date`, `ProductionDate`, `Favorites`, `HasFingerprintSubmissions`, `Sort`, `Direction`.
- **"Trending" is not engagement.** `scene_popularity` (migration 60) is a materialized view
  over `scene_fingerprints` counting **distinct users who submitted a fingerprint**, with a
  second count for the last 7 days. `SceneSortEnumTrending` sorts by that. Nobody's view, vote
  or playlist-add feeds it. §8.4's formula needs all four.
- **The engagement signals that exist** are per-user and per-scene-or-item, and each lives in
  the table that owns it:

  | §8.6 signal | Where it lives today | Weights available |
  |---|---|---|
  | owns it (`+1.0` favorite / `+0.3` view) | `scene_fingerprints (user_id, scene_id, vote, created_at)` | `vote` is `smallint`, so ±1 is recorded |
  | `+0.8` playlist add | `list_items (list_id, entity_type, entity_id, created_at)` | none |
  | a review | `reviews (author_id, entity_type, entity_id, rating, created_at)` | `rating` |
  | `+0.5` upvote, `-0.5` downvote | nothing for scenes. `elo_votes`, `edit_votes` and `identification_candidate_votes` are about other objects | — |
  | `+0.3`/`-0.1` view completion | **nothing**: there is no player and no view table until phase 10 | — |
  | `-1.0` not interested, `-0.05` skip | nothing for scenes | — |
- **No `user_follows`, no `saved_searches`, no `session_state` table.** DMs, follows and
  sessions do not exist in this fork.
- **`notifications (user_id, type notification_type, id, created_at, read_at, data jsonb)`**
  exists, so a saved-search match notification has a home — and adding a value to the
  `notification_type` enum is a migration, not a new table.
- **pgvector is NOT available on this instance's Postgres.** `pg_available_extensions` has
  `bktree`, `pg_search`, `pg_trgm` and `cube` (5 dims is not 512). §8.5's vector index cannot
  be a `vector` column here without changing the image this instance is built from.
- `scene_search` has **no `tag_ids`-independent facet surface**: nothing returns "the tags that
  co-occur with this tag, with counts". §8.2's Related Tags and §8.3's explorer both need it.

## 3. The two architectural decisions

### 3.1 The language parses into the EXISTING filter structs

`search.Parse(text) (*Expr, error)` is pure: no database, no ids, no I/O. `search.Lower(expr,
tagIDs)` then merges the expression into a `models.SceneQueryInput` — the struct the scene
query builder already understands, including the expansion phase 4 added. A second query
builder would be a second place the tag hierarchy, the tier rules and the counting form have
to be applied, and they would disagree on the first change to either.

The split also decides where names become ids: a tag term is a **name**, because that is what
a person types and what a URL can carry; resolution is a database step in `Lower`, so a filter
naming a tag that does not exist yields an empty criterion that matches nothing instead of an
error the reader cannot act on.

### 3.2 A saved search stores TEXT, and the filter is derived at use time

The stored form is the query text, because that is the thing a person edits, shares and
re-reads. The `SceneQueryInput` is derived by parsing it, every time. The alternative — a
`jsonb` snapshot of the resolved filter — is a second source of truth that disagrees with the
text the user sees the first time one of them is edited, which is the mistake phase 4 refused
when it declined to materialise implications onto `scene_tags`.

**The cost, stated:** a tag rename or merge does not rewrite saved-search text, so a saved
search over a merged-away name narrows silently. Slice 2 rewrites the token on merge (the
merge path in `internal/service/edit/tag.go` is the place); if it does not land there, it is a
recorded deferral with this paragraph as its home.

## 4. The search language (slice 1)

```
query      := term*
term       := '-'? ( field ':' value | word | quoted-word )
field      := 'tag' | 'text' | 'studio' | 'parent_studio' | 'performer' | 'type'
            | 'duration' | 'date' | 'score' | 'sort' | 'similar_to' | 'playlist'
value      := word | quoted-word | range | comparison
range      := number '..' number | date '..' date      (either side may be empty)
comparison := ('>' | '>=' | '<' | '<=') number
quoted-word := '"' any-but-quote '"'
```

| Term | Meaning | Lowers to |
|---|---|---|
| `anal` | a TAG (the draft's core mode: bare words are tags) | `Tags` INCLUDES, resolved by name |
| `-rough` | NOT that tag, transitively — §4.1 of the ontology spec | a SECOND criterion, `exclude_tags`, which slice 2 adds to `SceneQueryInput`: the existing `Tags` field carries ONE modifier, so "tag A and not tag B" cannot be held in it |
| `tag:anal` | the same, spelled out | as above |
| `"hotel room"` | a tag whose name has a space | as above |
| `text:"hotel room"` | free text against the BM25 index | `Text` (the existing field) |
| `performer:name` | a performer, by name | `Performers` INCLUDES |
| `studio:name`, `parent_studio:name` | a studio, or the studio tree | `Studios`, `ParentStudio` |
| `type:scene` | content kind | accepted for `scene`, refused for the kinds phase 3 adds |
| `duration:60..600`, `duration:>300` | seconds | `Duration` criterion |
| `date:2020-01-01..2020-12-31`, `date:>2020` | release date | `Date` criterion |
| `score:>10` | community score floor | `Rating`/score criterion when phase 5 lands the column; refused until then |
| `sort:date`, `sort:-date` | order, `-` descending | `Sort`, `Direction` |
| `similar_to:<id>` | more like this | slice 5 |
| `playlist:<name>` | items of a list | slice 2/3 |

**Rules that are decisions, not details:**

1. **Bare words are TAGS, not free text.** The draft's own examples are `anal -rough` and
   `anal pov`. Free text keeps its existing home (`text:`, and the existing search box and its
   `Text` field), so nothing regresses: the language is a NEW input, and a person who wants
   both writes both.
2. **An unknown field is an ERROR, with its position.** `duratoin:60` must not quietly become
   a tag named `duratoin:60` or a free-text phrase; a filter silently dropped is a wrong answer
   presented as a right one. The error names the field, the offset and the nearest known field.
3. **A term with an empty value is an error** (`duration:`, `tag:`), for the same reason.
4. **`-` before a bare word is exclusion**, not negation of the next field. `-tag:x` is also
   accepted and is the same thing.
5. **Unknown tag NAMES are not errors.** Whether a name exists is a database question, and
   §3.1 keeps `Parse` pure. An unresolved name lowers to a criterion that matches nothing, and
   the search surface reports it as a diagnostic next to the results rather than as a failure.
6. **`sort:` and paging are the only terms that do not narrow.** They are last-wins on repeat.
7. **Rendering is exact inverse of parsing.** `search.Render(expr)` produces text that parses
   back to the same expression, and it is what a saved search stores and what the UI shows.
   Without it a saved search is a string no one can re-derive, and the round trip is the test
   that keeps the two directions honest.
8. **`-sort:date` and `sort:-date` are the same query.** Both are how people write "newest
   first"; the canonical form is `-sort:date`, and `Equal` compares canonical forms, so the
   order of a saved search does not depend on which spelling was typed.
9. **A term that cannot be applied faithfully is never dropped.** `Lower` reports it AND sets
   `NoMatch`, so the answer is empty with the reason beside it, rather than a set larger than
   what was asked for presented as the answer. Dropping an unresolved tag, or not applying a
   `duration:`, both return MORE than was asked for; an empty answer that says why is the
   honest one.

**What slice 1 built, and its limits.** `internal/service/search`: `Parse`, `Render`, `Equal` and
`Lower`, with a `Resolver` interface so the language is testable without a database. The limits
that remain are in the criteria the builder has today, and each is refused with its reason rather
than approximated: the date criterion carries ONE value and a modifier (`scene/query.go:241`) and
has no "at or after" arm at all, so `date:>=x`, `date:<=x` and a date RANGE answer nothing;
`duration`, `score`, `similar_to` and `playlist` have no criterion yet. `parent_studio:` names a
STUDIO and resolves to its id, because the builder walks the studio tree from an id
(`scene/query.go:94`). Slice 2 adds `exclude_tags` and `duration`, and **must also add them to the
filter-presence list at `scene/query.go:309`** — that block decides whether the custom-plan path
may be taken, and a filter missing from it can be answered by a fast path that never saw it.

## 5. Saved searches and their notifications (slice 2)

Migration **113 `saved_searches`**: `id, user_id, name, query text NOT NULL, notify boolean NOT
NULL DEFAULT false, created_at, updated_at`, `UNIQUE (user_id, name)`, index on
`(user_id, updated_at DESC)`. Not a `jsonb` filter snapshot — §3.2.

GraphQL: `SavedSearch` type, `savedSearches` (own only, `@isUserOwner`), `createSavedSearch`,
`updateSavedSearch`, `deleteSavedSearch`, and a `results` field that runs the query. A saved
search is a `SceneQuery` variant, so it is reachable from the existing query surface rather
than being a second one.

Notifications: when `notify` is true, a periodic job (the existing `internal/cron`) runs each
subscribed search with a `created_since` cursor and writes one `notifications` row per user per
run, never one per match — the draft's "my ongoing interests" is a feed, not a mail bomb. The
`notification_type` enum gains a value in the same migration.

## 6. The tag explorer (slice 3)

Two read surfaces over the phase 4 graph, both derived rather than stored:

- **Facets for a query**: for the tags that co-occur with the current result set, the count of
  items carrying each. One grouped query over `scene_tags`, scoped by the same filter the
  results use, so "related tags" cannot disagree with the results.
- **The hierarchy view**: `Tag.implies_transitive` / `implied_by_transitive` (phase 4) plus
  per-tag item counts. The depth-bearing fields exist so the client does not walk the graph.

Co-occurrence **strength** (§8.2's `riding (87%)`) is `count(together) / count(one)`, computed
in the query, and it is a display figure — it must never become a filter, because then two
instances with different corpora answer the same query differently.

## 7. Trending and freshness (slice 4)

§8.4's score, with the signals §2 measured:

```
freshness = Σ over signals of  weight(signal) × decay(age_hours)
decay(age) = 1 / (1 + age_hours / 48) ^ 1.5
```

| Signal | Weight | Source |
|---|---|---|
| fingerprint submitted (`vote = +1`) | 3 | `scene_fingerprints` |
| fingerprint withdrawn (`vote = -1`) | -3 | `scene_fingerprints` |
| added to a list | 3 | `list_items` |
| review written | 2 × (rating − 3)/2 | `reviews` |
| view completion | not implemented — no producer until phase 10 | — |

Materialized as `scene_freshness (scene_id, score, computed_at)` refreshed by the same cron
that refreshes popularity, with `SceneSortEnumFresh` for the "Trending Now" feed and separate
windows for "Rising" (score over the last 24h vs the preceding 24h) and "Best of" (a period
parameter). The draft's `votes_up - votes_down` term is absent because **there is no scene
vote table**; when one exists the weight is a constant here, not a migration.

## 8. Content DNA and similarity (slice 5)

`VectorStore` is an interface with two implementations: `Noop` (returns no neighbours, the
default) and `InMemory` (a `[]float32` per item, cosine, brute force over a candidate set the
structured filters already narrowed). §2's measurement is why: **pgvector is not available on
this instance**, so the "cheap first implementation" the plan assumed is not available either,
and `cube` cannot hold 512 dimensions.

The explanation half (§8.5's `similar_because`) lands BEFORE the vector half, because it is the
part that is honest today: shared tags (weighted by the ontology distance), the same performer,
the same studio, similar duration. Similarity without an explanation is the recommendation
nobody can act on, and an explanation built from tags is correct at any scale.

The threshold that justifies an ANN store is **measured, not guessed**, and recorded here when
it is measured: candidate-set size and p95 latency at which `InMemory` stops being enough.

## 9. Taste profile, For You, and the serendipity parameter (slice 6)

The affinity table of §8.6 with the weights above, computed at read time by one query per
signal kind, UNIONed — derived, not stored, for the reason §3.2 gives. Propagation through the
hierarchy uses phase 4's closure: liking `cowgirl_position` raises `woman_on_top`'s affinity at
a fraction of the direct weight (the draft's "less strongly", made a constant).

`For You` = the query language plus the affinity ranking plus the filters §8.6 lists (tier,
blacklist, already-viewed, score floor). `serendipity_factor` picks the mix, and the test the
plan names is a **degenerate-output test**: two contrasting settings over the same seed must
not return near-identical sets — otherwise the parameter is decoration.

Signals whose producer does not exist (view completion, skip, not-interested) are deferred to
the phase that creates the producer, because a signal table with no writer is a column nobody
fills and a weight nobody can explain.

## 10. Social discovery (slice 7)

Migration **114 `user_follows`** `(follower_id, followed_id, created_at, activity_visibility
enum('private','followers','public'))`. The activity feed is chronological by contract (the
draft: *"no algorithmic reordering"*), assembled from `favorites`-equivalent rows, list
additions and reviews by followed users, with the visibility default **private** for anything
new — the safer default is the one that cannot leak.

## 11. "Where I left off" (slice 7)

Migration **115 `session_states`** `(user_id, kind, key, cursor jsonb, storage
enum('local','synced'), updated_at)`, `UNIQUE (user_id, kind, key)`. **Local-first**: the
browser keeps the state and the server copy is opt-in per user, which is the draft's own
`storage: enum [local, synced]`. The server surface is only there so a second device can
continue; nothing reads it unless the user turned it on.

## 12. What is deferred, and to what

| NexusXXX | Here |
|---|---|
| §8.2 autocomplete with counts and category icons | slice 3 (it is the facet query with a prefix filter) |
| §8.2 tag cloud / bubble visualisation | phase 16 (UI) |
| §8.4 "Trending Tags" | slice 4, a grouped count by tag over the freshness window |
| §8.5 visual/textual embeddings (CNN/NLP) | phase 10 — there is no media plane to extract from yet, and no model installed (programme spec §5.5) |
| §8.6 view-completion and skip weights | phase 10 (the producer) |
| §8.6 "reset taste profile", manual affinity editing | slice 6 surface, phase 16 UI |
| §8.7 follow notifications | phase 11 (growth owns notifications about people) |
| §8.9 multiple saved session bookmarks | phase 16 (UI) |

## 13. Verification, per slice

- **Slice 1** — `internal/service/search/query_language_test.go`: a table of (text, expected
  criterion) per term kind, the error cases WITH their offsets (`duratoin:60`, `duration:`,
  an unterminated quote), the exclusion direction, and the round trip
  `Render(Parse(t)) == t` for every case in the table. Plus an integration test through
  GraphQL: `anal -rough` returns what the equivalent struct filter returns, asserted against
  the same fixture both ways — a language that agrees with itself and not with the query
  builder is the failure this catches.
- **Slice 2** — a migration harness (`scripts/verify-113.sh`) for the constraints, and an
  integration test that a saved search survives a round trip (save, list, run, rename) and that
  a notified search writes ONE notification per run, not one per match.
- **Slice 3** — facets: the counts a query returns for a fixture whose co-occurrence is known
  by construction; and the scope test, that a facet count agrees with the result set it sits
  next to.
- **Slice 4** — the freshness score against hand-computed values for a fixture with known
  ages; and the ordering test, that a scene with older but more numerous signals sorts below a
  newer one.
- **Slice 5** — `Noop` returns nothing and does not panic; `InMemory` ranks a constructed
  pair above a constructed non-pair; and every explanation names a reason that is checkable
  against the fixture (a shared tag that is really shared).
- **Slice 6** — the degenerate-output test for the serendipity parameter, plus: at
  `serendipity=0` the feed is a subset of the affinity-ranked set, and propagation raises the
  parent's affinity but by less than the direct weight.
- **Slice 7** — follows: the visibility default is private (asserted, not assumed); the feed
  is chronological; and a private activity produces no row in another user's feed — an absence
  assertion, so it is preceded by counting the subject.

## 14. Decisions and reversals this phase records

1. **Bare words are tags, not free text** (§4.1). The draft's examples require it; the
   existing free-text box keeps its field.
2. **The language is parsed, not executed**: no second query builder (§3.1).
3. **A saved search stores text, not a filter snapshot** (§3.2), with the rename cost stated.
4. **The one-architectural-decision the plan got wrong for this instance**: §8.5's vector
   store cannot begin as a `pgvector` column, because pgvector is not installed here (§2,
   §8). The interface stays; the first implementation is in-process.
5. **Trending is redefined**, not extended: today's "trending" counts fingerprint submissions,
   which is not the engagement §8.4 describes, and the freshness score is a new surface rather
   than a new sort over the old one.
6. **The explanation half of similarity ships before the vector half** (§8), because it is
   correct today and the vector half is not yet possible.
