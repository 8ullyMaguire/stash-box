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

**The cost, and where it is paid.** The stored text is not rewritten when a tag is merged or
renamed, so the query keeps the OLD name — and the resolver follows it: a lookup tries the live
name or alias, then the soft-deleted row and its redirect (§4). A merge soft-deletes the source
and writes exactly that redirect, so a saved search over a merged-away name keeps meaning what it
meant. A RENAME is not covered the same way: no redirect is written for one and the old name is
not kept as an alias, so the name becomes unknown — and the query reports `unknown_name` beside
an empty result rather than narrowing in silence. Rewriting stored text on rename is a recorded
deferral; the diagnostic is what makes it visible.

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
| `-rough` | NOT that tag, transitively — §4.1 of the ontology spec | a SECOND criterion, `exclude_tags`, because one criterion carries one modifier: "tag A and not tag B" has nowhere else to live |
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

**Where a query is written.** `SceneQueryInput.query` is the entry point, not `text`: the
`text` field already exists in the schema and is DEPRECATED (it predates `title`), so reusing it
would have redefined a field clients still send. `query` says what it is, and the two halves of
the old name survive as themselves: `title` filters titles, `text:"..."` in the LANGUAGE filters
the BM25 index.

**What is built, and its limits.** `internal/service/search` (`Parse`, `Render`, `Equal`,
`Lower` behind a `Resolver`) and its wiring: `query` parses and lowers into the same filter the
builder applies, `exclude_tags` and `duration` are criteria, and a term that cannot be applied
empties the answer and reports itself in `QueryScenesResultType.diagnostics`. The limits left are
the criteria the builder has: the date criterion carries ONE value and a modifier
(`scene/query.go:241`) and has no "at or after" arm, so `date:>=x`, `date:<=x` and a date RANGE
answer nothing; so does a duration RANGE (`duration:60..600`) or `duration:>=300`, for the same
one-value reason; `score`, `similar_to` and `playlist` have no criterion yet. `parent_studio:`
names a STUDIO and resolves to its id, because the builder walks the studio tree from an id
(`scene/query.go:94`). A new filter the language learns to lower must be added to the
filter-presence list at `scene/query.go:355`, or the optimized trending path can be taken and
answer without it.

**Names resolve through a merge.** A tag name lookup tries the live name or alias, then the
soft-deleted row and its redirect (`FindTagByNameOrAliasUnfiltered` + `FindTagWithRedirect`).
A merge does NOT write the old name as an alias — it soft-deletes the source and writes the
redirect — so without that second step a query written before a merge reports `unknown_name` and
empties silently. §3.2's cost is therefore paid in the resolver, not deferred to a rewrite, and
`TestAQueryOverAMergedTagStillMeansSomething` is the test that says so.

## 5. Saved searches and their notifications (slice 2b, DONE)

The language reaches the query surface already (slice 2a); this stores a query and watches it.

Migration **113 `saved_searches`**: `id, user_id, name, query text, notify boolean DEFAULT false,
last_notified_at timestamp, created_at, updated_at`; `UNIQUE (user_id, name)` — per OWNER,
because two people may each keep "unwatched anal" and mean different things by it; a
`(user_id, updated_at DESC)` index for the list; a partial `WHERE notify` index for the job,
which is the only reader that wants the subscribed rows; and the `notification_type` value
`SAVED_SEARCH_MATCH`. Not a `jsonb` filter snapshot — §3.2.

**The text is what runs.** `SavedSearch.results(page, per_page, sort, direction)` builds a
`SceneQueryInput` whose `query` is the stored text and returns the lazy `QueryScenesResultType`,
so it takes the same parse → resolve → lower → builder path `queryScenes` takes. Two
consequences worth stating: a saved search cannot answer differently from the statement it
stores, and `results.diagnostics` reports a term the filter cannot apply exactly the way a live
query does. A client that wants a different surface (performer context, another sort) can call
`queryScenes` with the stored text, which is why the field is a convenience and not a second
query language.

Three implementation facts that are contracts, not accidents:

1. **`results` is force-resolved, not bound to the struct field.** gqlgen parses a bound field's
   arguments and then returns the field anyway, so `results(page: 2)` would have returned page 1
   with no error anywhere. `gqlgen.yml` marks the field `resolver: true`, and
   `TestSavedSearchResultsHonourTheirPagingArguments` asserts that two pages DIFFER — "it
   returned scenes" passes under the bug.
2. **The input's defaults are spelled out where the input is built in Go.** `page: 1, per_page:
   25, sort: DATE, direction: DESC` are `SceneQueryInput`'s schema defaults; a client gets them
   from gqlgen, a Go-built input does not, and `page: 0` is not "unset" — it is an offset of −25.
3. **`created_since` is a filter, not a private cursor.** `SceneQueryInput.created_since`
   filters on `scenes.created_at` — when the scene ARRIVED here, not `date`, when it was made —
   and it is in the filter-presence list at `scene/query.go:377` for the reason that list exists.
   The notification job uses it, and so may any client.

**The job.** `savedsearch.NotifyMatches`, run by a thin cron wrapper every
`saved_search_notify_interval` (default 15m) and testable without a scheduler. It writes ONE
notification per subscribed search per run, never one per match — a standing query over a busy
instance is a feed, not a mailbox — and returns how many it wrote.

- **The cursor comes from the DATABASE's clock** (`SavedSearchNow`, `now()::timestamp`). Not
  stylistic: `scenes.created_at` is a `timestamp` stamped by the database's clock, and a Go
  process in +02:00 writing its local wall clock into a `timestamp` column stores a cursor two
  hours AHEAD of the rows it is compared with — every run finds nothing, forever, with no error
  anywhere. That was the first version of this job; the integration test went red on it.
- **The cursor is taken before the run and written after**, so a run that takes a minute cannot
  move it past a scene that arrived while the run was still going.
- **A search with no cursor gets one and announces nothing.** A search saved a year ago must not
  announce a year of matches the first time it is subscribed.
- **Each search runs as its OWNER.** The filter can depend on who is asking — `favorites:` means
  the owner's favorites — so the owner's identity is put into the context for that run, and the
  query path's own use of the current user is satisfied by the reader whose search it is.
- **The notification's target is the NEWEST match**, so opening it lands on something to look at
  rather than on a search to run again; the payload carries the search's id and the count.
  `SavedSearchMatch.saved_search` is nullable because deleting a search deletes its notifications
  in the same transaction, and a notification list renders every unread row it returns.
- **A failure on one search does not stop the others.** A query saved before a syntax tightening
  must not silence every other reader.

The self-referential trap this section exists to prevent: the search's own `notify` flag IS the
subscription, so the notification is written directly rather than through
`user_notifications` — requiring a second opt-in in a settings page would mean a search that
silently never notifies.

## 6. The tag explorer (slice 3, DONE)

Two read surfaces over the phase 4 graph, both derived rather than stored.

### 6.1 Facets for a query

`QueryScenesResultType.facets(limit: Int = 25, min_count: Int = 1): TagFacets!` — resolved only
when a client selects it, so a facet list is never computed for a page that does not show one.

```graphql
type TagFacets { tags: [TagFacet!]! tagged_count: Int! }
type TagFacet  { tag: Tag! count: Int! strength: Float! }
```

**A count is what clicking it returns.** The count comes through the implication closure, in the
direction the filter walks it (specific → general, migration 111), so a scene tagged only
`cowgirl_position` counts under `woman_on_top`, because filtering by `woman_on_top` finds it. A
direct-row `COUNT(*)` over `scene_tags` would be the same number only on a flat tag vocabulary —
on a hierarchy most items carry the SPECIFIC tag, so the general tags, the ones a reader browses
by, would report nearly nothing. The test asserts the agreement the way a reader checks it: the
facet's count equals `queryScenes` with that tag added to the filter under
`CriterionModifierIncludesAll`.

**The modifier is part of the contract.** `Includes` is a UNION: `{marker, general}` with
`Includes` matches scenes carrying either, so the same fixture returns 4 where the facet says 3
and the facet looks wrong while being right. A facet is applied ON TOP of what the reader
already has, which is `IncludesAll` — "this tag AMONG these results", not "everything with this
tag".

**The list is about the result set.** The seed of the statement is `buildSceneQuery`, the same
builder the results came from, so the scope cannot drift — a copy of the WHERE clause could.
Then: the tags the filter already REQUIRES are not offered back (a facet list offers what a
reader has not decided yet); a tag no result scene carries is not listed; a tag that only
IMPLIES a filtered tag stays, and is usually the most useful row (filtering `woman_on_top` and
being told `cowgirl_position` is what people actually tagged answers the question the hierarchy
creates); EXCLUDED tags stay listed too, because "the thing you just ruled out is how 40% of
these are tagged" is a fact and hiding it would make the list quietly editorial; deleted tags
are never listed.

**`strength` is a display figure and never a filter** (the draft's §8.2 `riding (87%)`): the
facet's count divided by every scene in the INSTANCE a filter on that tag would return, so 1.0
means this result set already holds everything that tag has, and two instances holding different
corpora give the same query different numbers.

**`tagged_count`** is how many result scenes carry at least one tag — the denominator for "40%
of these". It is deliberately not the sum of the counts above: a scene carrying two tags is in
two of them.

Cost per facet request: the result set's own query once (that is what "scoped by the same
filter" costs), one grouped statement over `scene_tags` for the whole list, one instance-wide
aggregation for every facet's denominator (`CountScenesForTagsThroughImplications`, seeded with
the whole list so it is one statement and not one per tag), one count for `tagged_count`, and
one tag lookup. It is a hand-built statement rather than a sqlc query, because the seed is
dynamic and squirrel v1.5 cannot express a recursive CTE; it carries its own `-- name:` so
traces still label it.

### 6.2 The hierarchy view

`Tag.implies_transitive` / `implied_by_transitive` (phase 4) carry depth, so a client does not
walk the graph, and `Tag.scene_count` is what a filter on this tag returns — the same closure
question, seeded with one tag. Derived at read time, never stored: an ontology edit creates an
implication, and a materialised counter would be wrong the moment one lands with nothing to tell
a reader the number is stale.

The three numbers close on each other: a facet's `count` for a tag, the count of a query
filtered by that tag WITHIN the result set, and `Tag.scene_count` restricted to the same set are
the same question asked three ways.

Deferred in this slice: nothing that reads as a gap — the surfaces are complete for a client to
render (list, strength, counts, hierarchy with depth). What is NOT here is the UI (§16's
programme) and any cross-kind facet (performers, studios, sites as facets). Cross-kind facets are
deferred rather than half-built: their counts are questions about other join tables, and one
generic facet mechanism would be a query builder over builders. The tag facet is where the
hierarchy makes the number non-obvious, which is why it went first.

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
- **Slice 2** — `scripts/mutate-saved-searches.py` plus `internal/api/saved_search_integration_test.go`:
  a round trip (save, list, find, rename, subscribe, delete); ownership (another user's id is
  null, their delete is false, the owner's row survives); the stored text agreeing with
  `queryScenes` on the same fixture; two pages differing, so the paging arguments are shown to
  reach the query; an unapplicable term returning empty WITH one diagnostic naming it; and the
  job's rules — first run announces nothing, a run writes ONE notification with the newest match
  as its target and the count in its payload, a second run over the same state writes none, and
  deleting the search takes its notifications. Four mutations (cursor filtering dropped, paging
  arguments ignored, the no-cursor rule removed, ownership unchecked in delete) each redden
  exactly the test that names that behaviour and restore green.
- **Slice 3** — `scripts/mutate-scene-facets.py` plus
  `internal/api/scene_facets_integration_test.go`, on a fixture whose co-occurrence is known BY
  CONSTRUCTION and built so the hierarchy case and the flat case differ: `specific` implies
  `general`, two scenes carry only `specific`, one carries `general` directly, one outside the
  result set carries `general` alone. Then: the facet's count for `general` is 3 and NOT the 1 a
  direct-row count gives; the same number is compared against `queryScenes` filtered by
  `{marker, general}` under `IncludesAll`, so a facet is checked against the result a reader
  gets by clicking it; `specific` is 2 and every number is asserted before any absence is; the
  filter's own tag and a tag no result scene carries are both absent from the list, after the
  list is asserted non-empty and the fixture asserted visible; strength is 0.75 for `general`
  (3 of the instance's 4 scenes) and 1.0 for `specific`, with every strength in (0, 1];
  `tagged_count` is 3; `limit: 1` returns the single most-used tag and `min_count: 3` drops the
  tag with 2; and `Tag.scene_count` is 4 for `general`, 2 for `specific`, 0 for a tag nothing
  carries. Four mutations (the recursive arm of the closure removed, the applied-tag exclusion
  removed, strength reported as a raw count, and `limit`/`min_count` never reaching the
  statement) each redden exactly the test that names that behaviour and restore green.
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
7. **The notification cursor is the DATABASE's clock, not the process's** (§5). A `timestamp`
   column has no zone, so a process in +02:00 stores a cursor two hours ahead of the rows it is
   compared against and every run silently finds nothing. Measured on the first version of the
   job, and the reason `SavedSearchNow` exists as a query.
8. **A saved search runs as its OWNER, in the job as well as in the UI** (§5). `favorites:` is
   a filter about who is asking, so a run with no current user is not merely imprecise — the
   query path dereferences the user, and the first version of the job panicked on it.
9. **The search's `notify` flag IS the subscription** (§5), so the notification bypasses
   `user_notifications` on purpose: a second opt-in would produce a search that silently never
   notifies.
10. **A facet's count is a number the reader can check, so it agrees with the click** (§6.1).
    Counting the direct rows of `scene_tags` is the obvious implementation and the wrong one on a
    hierarchy: the general tags would report nearly nothing, and the number beside a tag would
    disagree with the page it opens. The count walks the closure, and the integration test
    compares it against the filtered query rather than against itself.
11. **`Tag.scene_count` is computed, never stored** (§6.2). An ontology edit adds an implication
    that changes the count of every tag above it; a materialised counter would be stale from that
    moment with nothing to tell a reader. The count is also the same question the facet asks, so
    one closure query answers both.
12. **Cross-kind facets are deferred, not stubbed** (§6.1). Performers, studios and sites as
    facets are counts over different join tables each with their own hierarchy rules (a performer
    alias, a studio parent); one generic mechanism would be a query builder over builders, and the
    tag facet is the one where the hierarchy makes the number non-obvious — which is why it is
    the one that shipped.
