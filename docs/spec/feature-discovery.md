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

## 7. Trending and freshness (slice 4, DONE)

§8.4's score, with the signals §2 measured:

```
freshness = Σ over signals of  weight(signal) × decay(age_hours)
decay(age) = 1 / (1 + age_hours / 48) ^ 1.5
```

| Signal | Weight | Source |
|---|---|---|
| fingerprint vote | 3 × `vote` (`+1` submitted, `−1` withdrawn) | `scene_fingerprints.vote` |
| added to a list | 3 | `list_items`, `entity_type = 'scene'` |
| review written | rating − 3 (the draft's `2 × (rating − 3)/2`) | `reviews`, `entity_type = 'scene'` |
| view completion | not implemented — no producer until phase 10 | — |
| `votes_up − votes_down` | **absent** — there is no scene vote table | — |

### 7.1 It is a materialized view, refreshed by the cron

`scene_freshness (scene_id, score, recent_score, earlier_score, computed_at)` — a MATERIALIZED
VIEW, not a table with a cron writer, and the same shape as `scene_popularity_trending`: the score
is a pure function of the signal rows and the current time, so a stored copy would be a second
answer that can drift from them. `REFRESH MATERIALIZED VIEW CONCURRENTLY` on the same `@every 1h`
cadence as the popularity refresh (a unique index on `scene_id` is what CONCURRENTLY requires).

**The refresh is where the decay is evaluated**, so a score is as old as the last run and the
sorts read a stable snapshot. The sorts therefore read the VIEW and never the signal tables
themselves: deciding the score in the query would give two pages of one feed two different
rankings.

**The window is 90 days**, stated as arithmetic rather than as a preference: at 2160 hours a
signal contributes `1/(1+45)^1.5 = 0.0033` of its weight, under a third of one percent, below the
resolution of a ranking whose top rows differ by whole signals — and the window is what keeps the
refresh a bounded scan of recent rows instead of the whole signal history.

### 7.2 The withdrawal is a NEGATIVE signal, and the score can go below zero

`scene_fingerprints.vote` is `SMALLINT NOT NULL CHECK (vote = -1 OR vote = 1)` (migration 40), and
a client withdraws a fingerprint by submitting it with `vote: REMOVE`, which the upsert stores as
−1 rather than deleting the row. So the draft's −3 is a signal with a timestamp, not an absence,
and `3 × vote` is both terms in one expression. A scene whose only fingerprint was voted down
therefore scores −3 and ranks BELOW a scene nobody has touched: a disputed claim does not promote.
A withdrawal keeps the original submission's `created_at` (the upsert rewrites duration and vote
only), so it retracts an old signal rather than burying a scene.

An unrated review is not a signal at all. `rating` is nullable because a review of a studio's
ethics or a performer's aliases is not a 1-to-5 quantity, and averaging one in as a 3 would invent
a signal — `rating IS NULL` contributes nothing.

### 7.3 The sorts

- **`FRESH`** ("Trending Now"): `ORDER BY score DESC`, a LEFT JOIN so a scene nobody has engaged
  with yet is last rather than missing.
- **`RISING`**: `ORDER BY recent_score − earlier_score DESC` — the last 24 hours against the 24
  before them. It is a COMPARISON, not a bigger score: a scene with two signals today and three
  yesterday is worth more on `FRESH` and less on `RISING` than a scene with one signal today and
  nothing yesterday, and that disagreement is the entire feature.
- **"Best of a period"** needs no new mechanism, and deliberately gets none: it is the existing
  `Date` criterion plus `FRESH`. A period parameter on the SORT would be a field that silently does
  nothing when paired with another sort, which is the class of input this spec keeps removing.

## 8. Content DNA and similarity (slice 5, DONE)

`VectorStore` is an interface with two implementations: `Noop` (returns no neighbours, the
default) and `InMemory` (a `[]float32` per item, cosine, brute force over a candidate set the
structured filters already narrowed). §2's measurement is why: **pgvector is not available on
this instance**, so the "cheap first implementation" the plan assumed is not available either,
and `cube` cannot hold 512 dimensions.

The explanation half lands BEFORE the vector half, because it is the part that is honest today
and the part a reader can check. Similarity without an explanation is the recommendation nobody
can act on, and an explanation built from tags is correct at any scale.

### 8.1 The evidence, the weights, and what the score is for

| Evidence | Weight | Source |
|---|---|---|
| both carry the same tag | 3.0 | `scene_tags` |
| the candidate carries a tag that is a KIND of one of the subject's | 1.5 | `tag_implications`, walked specific-wards |
| the candidate carries a tag one of the subject's tags IMPLIES | 1.0 | `tag_implications`, walked general-wards |
| the same performer appears in both | 5.0 | `scene_performers` |
| both belong to the same studio | 2.0 | `scenes.studio_id` |
| runtimes within half an hour, linearly | up to 2.0 | `scenes.duration`, in seconds |

`score = earned / capacity`, where capacity is what the SUBJECT offers: `3 × its tags + 5 × its
performers + 2 if it has a studio + 2 if it has a duration`. A scene with no runtime offers
nothing for the runtime term, so the term is neither earned against it nor counted in the
denominator. This is the same asymmetric denominator `similarPerformers` uses, for the same
reason: a scene with forty tags shares *something* with everything, and dividing by the shared
count's own total lets it outrank a genuine match against a scene with three.

**The score is for ordering and debugging; the reasons are what a client renders.** It is not
clamped to 1.0: a candidate can carry many tags that are kinds of a single tag the subject has,
so a narrow subject can be exceeded by a rich candidate, and clamping would collapse all of
those onto one number. Every reason carries its own `weight`, so a client can reconstruct the
score it was shown from the reasons it was shown.

An unrated review, an unknown runtime and a soft-deleted performer are all NOT evidence of
likeness: none of them is a fact about content, and each is excluded on both sides so that the
counts and the names cannot disagree.

### 8.2 The ontology is read in BOTH directions, and the directions are not the same claim

Migration 111 does not write an implied tag onto `scene_tags`, so a scene tagged
`cowgirl_position` carries one row and a reader asking for `woman_on_top` finds it by walking
the DAG. Similarity walks the same graph twice:

- **Downwards** (`reach_down`): the candidate carries a tag that SATISFIES one of the subject's
  tags — a specific instance of a claim the subject makes. 1.5.
- **Upwards** (`reach_up`): the candidate carries a tag one of the subject's tags IMPLIES — a
  more general claim, a family relative. 1.0.

Both directions matter and neither is a stricter version of the other: the first is a claim the
archive recorded (`cowgirl_position` implies `woman_on_top`), the second only shares a family.
A one-directional rule would work for exactly half the archive, silently, depending on whether
the subject happened to be tagged the general way — which is invisible on a single-tag fixture
and obvious on a real one. A tag reachable by several of the subject's tags, or in both
directions, produces ONE reason: the shallowest explanation wins, then the downward direction,
then the name.

### 8.3 The vector half REORDERS and never scores

`ranking = score + max(0, semantic)`; `semantic` is the cosine of the two embeddings, exposed as
`semanticScore` and nil when the store holds nothing for the pair. A NEGATIVE cosine is reported
and not used: it is a claim from a different system that the structured match is wrong, and
acting on it would let an unexplained number push a chosen recommendation DOWN. A candidate is
only ever promoted.

**Nothing about the vector half touches `score`**, which is the sum of things a reader can
check. The first implementation added the bonus into `score` and the integration test caught it
by reading `1.307` where the evidence said `0.6` — the number a client was shown had stopped
being traceable from the reasons beside it.

The store is built ONCE per service factory and not per call. A store constructed per request is
always empty, so embeddings put into it could never reorder anything, however many there were —
the vector half would look wired and behave exactly like the no-op store. `similar_vector_store`
(`noop` by default, `inmemory`) is read there; an unknown value logs and degrades to the no-op
rather than refusing to serve scenes.

### 8.4 The threshold that justifies an approximate index: measured, and recorded here

`go test ./internal/service/similar/ -run TestNearestLatencyReport -v` with `SIMILAR_BENCH=1`,
512 dimensions, 200 samples per size, in-memory cosine over every stored vector:

| stored vectors | p50 | p95 | mean |
|---|---|---|---|
| 1,000 | 1.17 ms | 1.69 ms | 1.27 ms |
| 10,000 | 16.1 ms | 18.3 ms | 16.4 ms |
| 100,000 | 186.7 ms | 195.6 ms | 187.4 ms |

The scaling is linear at ~1.8 µs per vector, and the distribution is tight — a brute-force scan
does the same work per vector every time — so the mean is representative and the p95 is not
hiding a tail.

**The conclusion, which is not the obvious one:** no approximate index is needed for this
feature at any scale, because the store is never asked about the archive. It is asked about the
candidate pool (`candidatePoolSize`, at most 500 ids), and `Scores` costs one map lookup and one
cosine per id in that pool — 500 × 1.8 µs ≈ 0.9 ms, independent of how many vectors are stored
beyond it. **A whole-archive nearest-neighbour surface is a different question, and that is where
10,000 is the threshold**: at 10,000 × 512 a page of neighbours already costs 16 ms, and at
100,000 it costs 187 ms, which is over any interactive budget. So an ANN store is justified when
something asks "the nearest scenes to this vector" over everything, and not before — and the
number that says so is the one above rather than a guess.

### 8.5 What is deliberately absent

- **No `minShared` floor**, unlike `similarPerformers`. A floor there removes coincidence from a
  short list of people; here a weak match sinks to the bottom of a page the client paginates,
  and a floor would silently hide a strong match with little shared vocabulary. Both `score` and
  `reasons` are returned, so a client filters on what it can see.
- **No count field**, for the opposite reason: the candidate pool is bounded, so any count would
  be a count of the pool rather than of the archive. "There are 500" would be a lie with a number
  on it.
- **No embedding producer.** Nothing on this instance turns a scene into a vector, so the vector
  half's candidate-selection value is unproven at scale, and the explanation carries the feature
  until something does. `Noop.Put` returns an error rather than succeeding quietly: a seeding
  pipeline that stored nothing must not report itself healthy.

## 9. Taste profile, For You, and the serendipity parameter (slice 6, DONE)

Taste is `internal/service/taste/` and the GraphQL `forYou` field. Affinity is DERIVED AT READ
TIME — computed by one query per signal kind, UNIONed — and never stored, for the reason §3.2
gives: a stored copy is a second answer able to drift from the signals it came from, and every
write path would have to remember to update it. Nothing outside §8.6's own list of signals feeds
it, which is what makes the number explainable.

### 9.1 The weights, and where each one comes from

| signal | weight | why |
|---|---|---|
| a fingerprint vote | `+1.0` / `−1.0` | §8.6's "owns it": the strongest thing a user states about a scene. The `−1.0` is a withdrawal, and it is a signal rather than the absence of one |
| a list add | `+0.8` | §8.6's playlist add, deliberately below a fingerprint: filing is weaker than owning |
| a review's rating | `(rating − 3) / 4` — `±0.5` at the extremes | half of §8.6's magnitude, because a review is an OPINION about a scene rather than a claim to own it, and a five-star review must not outweigh the user's own fingerprint. An UNRATED review contributes NOTHING: `rating` is nullable because a review of a studio's ethics is not a 1-to-5 quantity, and inventing a 3 would be inventing a preference |
| view completion `+0.3`/`−0.1`, skip `−0.05`, not-interested `−1.0` | — | **deferred to phase 10 with their producer.** Nothing writes them: there is no player and no view table. A weight for a signal nobody emits is a constant nobody can explain |

Only PUBLISHED reviews count. A flagged or removed review is a moderator's decision about whether
the text may stand; letting it shape a recommendation would give it the same weight as a published
one while the reader cannot see it. The mutation script reddens a test for each of these four
clauses, because each is a separate claim.

**The weights are a named convention, not a measurement**, and the justification for each is the
ordering it produces (owning > filing > opining) rather than a fit to data nobody has collected.

### 9.2 Propagation: the parent is raised at a fraction, and that is a test

Affinity is kept per TAG, and the hierarchy is applied at SCORING time rather than folded into the
stored signals: a scene's affinity is the sum over its own tags of the viewer's affinity for that
tag, PLUS, for each tag that IMPLIES it (the direction migration 111 records), that tag's affinity
times `propagationFactor = 0.4` per hop, transitively.

Read as the §9 acceptance criterion, that is: **liking `cowgirl_position` raises `woman_on_top`'s
affinity, and by LESS than the direct weight.** A scene tagged only the parent scores 0.4 where one
tagged the child scores 1.0 — strictly between nothing and a direct hit, asserted literally in
`TestForYouPropagatesToTheParentAtAFractionOfTheDirectWeight`, not merely described. Two hops are
0.16, so depth fades on its own and the walk needs only the same depth bound the rest of the
codebase uses.

The fraction is not a second denominator and not a percentage: it is the same "a parent claim is
weaker than the child's" rule §8.2 applies to similarity, with a different number because the two
answer different questions.

### 9.3 What the feed is, and what it refuses to be

`For You` = **the query language plus the affinity ranking plus the mix**, and the first of those
is the EXISTING language rather than a second implementation: `forYou(input: { query: "anal -rough" })`
is lowered by the same code `queryScenes` uses, so a feed cannot show a scene the equivalent search
would hide — including the ones the viewer's own role may not see, because the pool is built by the
scene query and that is where visibility is applied. A `sort:`/`direction:` in the query string is
accepted and IGNORED: the feed's order is affinity, and honouring a sort would return a page ordered
by something the caller was not told about.

The pool (`limit × 6 + 60`, capped at 600) is drawn in the archive's own order — **`CREATED_AT`
DESC, the order of ARRIVAL at this instance** — and affinity is not that order, so a pool of exactly
`limit` would make the ranking a no-op. Bounded because the scoring query carries the candidate ids
and walks the tag graph for each of them.

The sort is `CREATED_AT` rather than the scene query's default (`DATE`, the release date), and the
full test suite is what found out why: with the release-date default on an archive with more than
600 dated scenes, the pool fills with scenes that HAVE a release date and a scene with none is
never even scored — so the feed silently stops recommending anything recently added. Arrival order
is total (every row has a `created_at`) and it is what "the archive's own order" should mean for a
discovery feed.

**AFFINITY IS NOT A SCORE OUT OF ANYTHING.** It is a sum of weights with no natural maximum, and it
can be NEGATIVE. It is exposed for ordering and debugging; a client renders `contributions`, which
name the tags and carry their signed weights. Rendering a percentage from it would invent a
precision the number does not have.

### 9.4 Serendipity is an input, not a policy

`serendipity` sets a STRIDE, not a sample size: one slot in `round(1/serendipity)` is filled from
outside the affinity order, so `0` is a pure affinity page (and therefore a SUBSET of the affinity
ranking, which is how the acceptance criterion is read), `0.5` replaces every second slot, and `1`
replaces every slot. The surprises come from a seeded shuffle of the pool minus the affinity page,
so a surprise is never a duplicate of a pick and is always something the affinity order would not
have led with.

A random sample of `serendipity × limit` rows was the obvious alternative and it is worse: the
surprises arrive in a clump at the end of the page, or in a scatter that differs on every call.
`seed` is an input for the same reason — without it the parameter could not be tested, because a
difference between two pages could always be the shuffle.

The **degenerate-output test** the plan named is `TestForYouSerendipityZeroIsTheAffinityOrderAndOneIsNot`
at the resolver (every row of a `serendipity: 0` page carries affinity > 0; a `serendipity: 1` page
shares under half of its rows with it AT THE SAME SEED; and the same seed returns the same page,
which is what makes the comparison attributable to the parameter) plus the pure-function tests in
`internal/service/taste/mix_test.go`.

### 9.5 Filters: the ones with a producer, and the substitution

§8.6 lists tier, blacklist, already-viewed and a score floor. What ships is **already-viewed, read
as already-ENGAGED**: there is no view table until phase 10, so the closest honest substitute is
"the viewer has a fingerprint, a list item or a review on it" — which is exactly the failure the
filter exists to prevent (offering a scene back to the person who owns or reviewed it) and is
checkable. `tier` is applied where it has always been applied, in the scene query that builds the
pool, rather than duplicated here. A `blacklist` and a `score floor` have no producer and no
stored preference yet, and are deferred to the surface that creates them (phase 16 UI).

### 9.6 A viewer with no signals

The feed returns the pool in the archive's own order (recency) with zero affinity and no
contributions, rather than an id-ordered tie: a new account that lands on uuid order looks broken,
one that lands on what arrived today looks like an archive. Nothing is claimed about their taste,
because nothing is known about it.


## 10. Social discovery (slice 7, DONE)

Migration **115 `user_follows`** `(follower_id, followed_id, activity_visibility, created_at)`,
`PRIMARY KEY (follower_id, followed_id)`, `activity_visibility IN ('private','followers','public')`
defaulting to **`private`**. The activity feed is chronological by contract (the draft: *"no
algorithmic reordering"*), assembled from fingerprint submissions, list additions and published
reviews by followed users.

### 10.1 The level lives on the follow row, and what that forces

The draft puts `activity_visibility` on the follow, so it is **per follow**: `followers` publishes
to the ONE follower that row names, and it does not publish to the author's other followers. The
consequence is that `public` has to carry the remaining load — "let anyone see this, follower or
not" — because a per-row column cannot express "all of my followers". Both readings are asserted:
a per-follow grant reaches the named follower and not a third user, and a public grant reaches a
reader who follows nobody at all.

**Following grants nothing.** A follow row is a statement about who watches whom; it is not
consent to publish. The default is `private` in the COLUMN, `SetActivityVisibility` can only be
called by the AUTHOR of the activity (the current user, never the `followerId` argument), and
following someone twice is one relationship that changes no level. A test drives the whole rule
through the API: the follower's own legitimate grant to someone else leaves the author's grant to
them untouched, read back from the row.

### 10.2 Three bounded arms, merged in Go

Each kind of activity is one indexed query with its own `LIMIT` (`FindFingerprintActivity`,
`FindListActivity`, `FindReviewActivity`), and the merge is in the service: drop the viewer's own
activity, order by timestamp descending, cut to the page. **Not one `UNION`**: sqlc cannot resolve
a CTE alias across `UNION` arms that carry their own `ORDER BY`/`LIMIT` (measured — *"table alias f
does not exist"*), and three bounded queries plus a merge is the same work with a smaller blast
radius. A per-arm `LIMIT` of the page size is exactly sufficient, because the merged page cannot
contain more than one page of any single arm.

The order is the timestamp with a deterministic tiebreak (kind, then scene id), because a reader
paging a feed must not see a row move between pages. There is no score, no decay and no diversity
adjustment, and the package doc says a future reader should find that written down rather than
infer it from the absence of a ranking.

### 10.3 What a row reports, and what it withholds

- **A withdrawal is not the activity.** `scene_fingerprints.vote = -1` means *I do not have this*,
  so the arm filters `vote = 1`; the signal still counts for freshness (§7.2), which is a
  different question.
- **An unpublished list is not named.** The activity is "someone filed this scene", which the
  visibility grant covers; *which* list they filed it into is theirs until they publish it. The
  rule is a named Go function with its own test, not a `CASE` in SQL: wrapping the columns in
  `CASE ... ELSE NULL::uuid END` reads well and lies to sqlc — the cast makes the column
  non-nullable, the rows still contain NULLs, and scanning one into a non-pointer field is a
  runtime error rather than a wrong value.
- **A scene that no longer loads is dropped.** The page is one batched load; a row whose scene is
  gone is a link to a 404, not a recommendation.
- **Ratings are nullable**, and an unrated review is a complete review.
- **Paging is a keyset cursor** (`before`, strictly older), because an offset shows a reader the
  same row twice whenever something is added between pages — the normal case on a feed. The page
  is 20 by default and 50 at most, clamped.

### 10.4 What is deferred

Follow notifications are phase 11 (growth owns notifications about people), and the visibility
editor's UI is phase 16. §12 carries both.

## 11. "Where I left off" (slice 7, DONE)

Migration **116 `session_states`** `(user_id, kind, key, cursor jsonb, storage, updated_at)`,
`PRIMARY KEY (user_id, kind, key)`, with

```sql
CHECK (("storage" = 'synced' AND "cursor" IS NOT NULL)
    OR ("storage" = 'local'  AND "cursor" IS NULL))
```

**Local-first, and the schema is what enforces it.** The browser keeps the state, and the server
copy is opt-in per user — the draft's own `storage: enum [local, synced]`. A `local` row holds the
CHOICE and no payload, and the CHECK makes a local row with a payload unrepresentable rather than
merely unintended: the service ignores a cursor sent with a local save, and a test inserts one
directly through SQL to prove the refusal is the database's and not the service's. Switching a
surface from synced to local REMOVES the stored payload — a setting that leaves the data behind is
not an opt-out.

**The cursor is opaque, and opacity is about MEANING, not bytes.** The server validates that it is
JSON and at most 4096 bytes, stores it and returns it; it never interprets it, which is what keeps
a client free to change what a cursor means. The column is `jsonb`, so the VALUE is preserved and
the TEXT is Postgres's: whitespace and key order may differ from what the client sent. The
schema's doc comment says so, because a client comparing cursor strings byte for byte would see
its own write come back different.

Everything is scoped to the authenticated user — **there is no id argument anywhere in this
surface** — `kind` and `key` are free text (a new surface is not a migration), and there is no
expiry: a TTL on "where I left off" would silently forget a position someone came back for. The
`clearSessionState` mutation is the explicit removal and reports whether there was one. Reads are
one key (`sessionState`) or one surface (`sessionStates(kind)`), which is what a second device
reads on arrival.

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

### 12.1 What this phase does not measure

Phase 7 shipped ranking surfaces and no evaluator. `elo_votes` is an append-only stream
of human pairwise labels, and nothing reads it as evidence; `forYou`, `similarScenes` and
`similarPerformers` have no held-out score and no baseline. That is not a gap in this
spec — it is the next phase's starting point, and it is specified separately in
`docs/spec/feature-recommendation-eval.md` (programme phase 7b):

- pairwise replay (E1) over `elo_votes`, with a Glicko replay rather than the stored
  rating, because the stored row already contains the test answer;
- held-out engagement (E2) over fingerprints, list items and published reviews;
- ranking duels (E3) for scene ranking, which the performer-only matchup cannot label.

The discovery-adjacent ideas that fit this archive — tag relations, co-engagement,
scoped matchups, a tag profile — are the adopted workstreams there. The deferrals in the
table above are unchanged; this subsection adds a reader, not a promise.

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
- **Slice 4** — `scripts/mutate-scene-freshness.py` plus
  `internal/api/scene_freshness_integration_test.go`, on fixtures whose SIGNAL AGES the test
  controls (the API never lets a caller choose when a signal happened, so the rows are written and
  then aged in SQL, and the scene fixture's own submission is deleted first — it is a real +3 that
  would otherwise sit inside every expectation):
  the decay against numbers computed from the spec's formula BY HAND (`1.060660` at 48 hours,
  `0.314270` at a week, `0.010828` at 83 days) rather than against the SQL, so a wrong constant
  cannot pass; the three signals summing (`3 + 1.060660 + 0.707107`); a review with no rating
  contributing NOTHING and not a −3; a withdrawal scoring −3 and ranking BELOW an untouched scene;
  the 90-day window as a boundary (83 days counts, 125 days leaves no row at all); ten signals ten
  days old ranking below one signal now; `FRESH` and `RISING` disagreeing on one fixture in the
  direction the spec names (a scene with two signals today and three yesterday leads on `FRESH`
  and loses on `RISING` to a scene with one signal today and nothing before it); and a new signal
  changing NOTHING until the refresh runs, which is what makes "materialized, refreshed hourly"
  observable. Five mutations — the decay half-life, the review weight, `3 * vote` reduced to `3`,
  the window widened to 180 days, and `RISING` totalling the two days instead of comparing them —
  each redden exactly the test that names that behaviour and restore green. Four of the five mutate
  the MIGRATION, because the score is computed in the database and a Go-side mutation would prove
  nothing about it.
- **Slice 5** — `Noop` returns nothing and does not panic; `InMemory` ranks a constructed
  pair above a constructed non-pair; and every explanation names a reason that is checkable
  against the fixture (a shared tag that is really shared).
- **Slice 6** — the degenerate-output test for the serendipity parameter, plus: at
  `serendipity=0` the feed is a subset of the affinity-ranked set, and propagation raises the
  parent's affinity but by less than the direct weight.
- **Slice 7** — `scripts/mutate-social-activity.py` plus
  `internal/api/activity_integration_test.go` (9 tests) and
  `internal/api/session_state_integration_test.go` (7 tests), with unit tests for the two pure
  rules (`internal/service/activity/service_test.go` — the merge order, the tiebreak's stability
  across arrival orders, the page cut, the unpublished-list rule; `.../follow/service_test.go`,
  `.../sessionstate/service_test.go` — the level and storage spellings, the refusals).
  The two claims the drafts state as acceptance criteria:
  **the visibility default is private — asserted, not assumed** (following a user with activity
  yields an EMPTY feed, and the same activity appears the moment the author publishes, through the
  mutation) and **the feed is chronological** (three rows inserted in one order and then AGED in
  SQL into another come back in the timestamp order, with the pairwise descending check as well as
  the example, and a 4-row walk in pages of 2 that never repeats a row and returns the four in
  sequence).
  Absence assertions all count their subject first, which is this repo's rule and is what the
  slice needed three times: the private grant is shown to be the thing that removed the row, a
  per-follow grant is shown to reach its named follower while a third user sees nothing, and the
  follower's own successful grant is shown not to have widened their access to the author.
  The local-first claim is asserted at the COLUMN (`SELECT cursor IS NULL`) rather than through
  the API, and one test inserts a `local` row with a payload through raw SQL to prove the
  database refuses it — the API returning null would also be true of a resolver that stored the
  payload and hid it.
  Eleven mutations, each reddening exactly the test that names that behaviour and restoring green:
  the follow's default visibility, the "who granted ME something" filter, the per-follow vs
  per-user grant, the public broadcast, the withdrawal filter, the unpublished-list rule, the
  viewer's own activity, the sort direction, the page cut, the local save's payload, and the
  CHECK itself (four of them mutate the GENERATED `follow.sql.go`, because sqlc compiles each
  statement into a Go string literal and a `.sql` edit changes nothing that runs).
  All of it is green in the package's FULL run, not only per-test.

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
13. **Freshness is a materialized view over the signal rows, not a table a job writes** (§7.1).
    The score is a pure function of those rows and `now()`, so a stored copy would be a second
    answer able to drift from them; the popularity views already establish the shape, and the
    hourly `REFRESH ... CONCURRENTLY` is where the decay is evaluated. The corollary is a
    contract the sorts depend on: they read the VIEW, because a feed paged over two queries that
    each recomputed the decay would be two rankings.
14. **A withdrawal is a −3 signal, not a deletion** (§7.2). The earlier draft of this document
    said the term was unimplementable because a withdrawal deletes the row — that was WRONG:
    `scene_fingerprints.vote` has existed since migration 40 as `CHECK (vote = -1 OR vote = 1)`,
    and the upsert records a withdrawal as −1. `3 * vote` is therefore both of the draft's
    fingerprint terms in one expression, and the score can go below zero, which puts a scene whose
    only claim was retracted below scenes nobody has touched.
15. **"Best of a period" is not a new sort** (§7.3). It is the existing `Date` criterion plus
    `FRESH`. A period parameter attached to a SORT would be an input that silently does nothing
    when the caller sorts any other way, and this phase has already removed two of those.
16. **Migration 114 is `scene_freshness`; social discovery moves to 115 and 116** (§10, §11).
    Both this section's earlier text and §10 claimed 114. The earlier slice wins the number
    because a migration that has been applied cannot be renumbered, and a plan's slice order is
    the order they are written in.
17. **The score is for ordering and the reasons are for reading** (§8.1). The weights are a
    named convention, not a measurement, so every one of them is exposed beside the number it
    produced and a reader who disagrees can still see the facts. The score is NOT clamped to
    1.0 and NOT a percentage: a narrow subject can be exceeded by a rich candidate, and clamping
    would collapse those onto one value.
18. **The vector half reorders and never scores** (§8.3), which is a reversal of the first
    implementation. The bonus was added into `score`, so the number a client was shown stopped
    being the sum of the reasons beside it — caught by an integration test reading `1.307` where
    the evidence said `0.6`. The ranking value and the evidence score are now separate, and the
    store is built once per factory: a store constructed per call is a store that is always
    empty, which would have made the vector half look wired while behaving exactly like the
    no-op store.
19. **The ANN threshold is recorded as a measurement, and it says an ANN index is not needed
    here** (§8.4). Brute force is 16 ms at 10,000 × 512 and 187 ms at 100,000; this feature only
    ever asks the store about a bounded candidate pool, so the numbers justify an approximate
    index for a whole-archive nearest-neighbour surface and nothing sooner. The promise in the
    previous draft of this section — that the threshold would be measured rather than guessed —
    is kept with numbers.
20. **The candidate query is the feature; the score is the explanation** (§8.1, §8.2). A scene
    the predicate does not select cannot be scored, so the ontology walk lives in SQL (both
    directions, with the depth bound `ExpandTagIDsByImplications` already uses) and the SQL
    pre-rank is deliberately UNWEIGHTED: a weighted pre-rank would be a second copy of the
    scorer's weights in a file the scorer does not read, and the two would drift.
21. **The taste weights are a named convention with an ORDER, not a fit** (§9.1). Owning beats
    filing beats opining (1.0 / 0.8 / 0.5), and each number is justified by the ordering it
    produces rather than by data nobody collected. The alternative — one weight per signal kind
    chosen to look principled — would have been three constants with no claim attached, and the
    mutation script could not have reddened a test for any of them.
22. **Affinity is per TAG, and the hierarchy is applied at scoring time** (§9.2), which is a
    reversal of the first design, where the signals were propagated into the affinity map itself
    before scoring. Propagating into the map means a viewer's love of `cowgirl_position` is
    written into their affinity for `woman_on_top`, and then a scene tagged the PARENT collects
    the parent's raised value AND the child's — the same preference counted twice, for a scene
    the viewer never signalled. Applying the fraction at scoring time keeps one direct value per
    tag and applies "the parent is weaker" once, which is also what makes the acceptance
    criterion checkable as a pair of numbers (0.4 against 1.0).
23. **`serendipity` is a stride, and the seed is an INPUT** (§9.4). The stride makes `0` a
    provable subset of the affinity ranking and puts the surprises through the page rather than in
    a clump at the end; the seed is what turns "the parameter changed the output" from an
    unfalsifiable claim into a test, because without it a difference between two pages could
    always be the shuffle.
24. **What the tests found, again, is the argument for integration tests over unit tests in this
    area** (both defects real, both invisible to review): `lists` has no `user_id` column — it is
    `owner_id` — and **sqlc did not catch it**, because the statement was type-checked against
    generated row types rather than the live schema, so the feed failed at the first request with
    `column l.user_id does not exist`; and the recursive CTE in the scoring query exposed its
    column as `weight` while the outer term read `a.affinity`. Neither is a logic error and
    neither would have been found by reading the code: they are the SQL and the Go disagreeing
    about a name, and only a real database says so.
25. **A test that passes alone and fails in the suite is measuring the suite, and the fix is at the
    FIXTURE, not in the assertion** (§9, plan slice 6). Three of the eight feed tests passed in
    isolation and failed in the full run, for two different reasons that both came from the shared
    database and the shared viewer: the fixture scenes were not in the top page once other tests had
    given the viewer more affinity than they had, and (after that was fixed by filtering the pool to
    the fixture) the FILTER TAG itself turned out to be a signal — affinity is computed from the
    tags of the scenes a user has engaged with, so a tag carried by a fingerprinted scene earns
    weight and appears in the contribution list. Each test now narrows the pool with a tag of its own
    and puts that tag on every fixture scene EXCEPT the one the viewer signals, which is a claim
    about the feature rather than about tidiness: filtering must not also be tasting.
26. **The other direction of the same defect: a fixture that pollutes the tests that follow it.**
    The mirror image, found the same session: ~35 new tags whose names sort before `testtag…`
    pushed `TestQueryTags`' tags out of its UNFILTERED page-1 window, so a test about tag lookup
    started failing because of a test about a feed. A test that reads "page 1 of everything" over a
    database the whole package shares is asserting a property of the database, not of the code; it
    now looks its tags up by name.
27. **A grant is a row, and the row's default is the safe direction** (§10.1). `activity_visibility`
    defaults to `private` in the COLUMN rather than in the service, so a follow created by any path
    — the API today, a migration later — publishes nothing until someone decides otherwise. The
    same reasoning makes `SetActivityVisibility` take the AUTHOR from the session and the follower
    from the argument: a signature that could not tell them apart is a signature a resolver can
    get wrong in the direction that leaks.
28. **`followers` is per FOLLOW, so `public` has to mean "everyone"** (§10.1). The draft puts the
    level on the follow row; the consequence is that a row cannot express "all my followers", and
    the only level that reaches a reader who follows nobody is `public`. Both halves are asserted,
    because the failure mode of getting this wrong (a grant that reaches one person more than it
    should) is invisible from the account that granted it.
29. **An enum with two spellings needs a mapping in BOTH directions, and a test that goes through
    both** (§10, §11). The GraphQL enums are upper case by convention and the columns are lower
    case to match their CHECK constraints, so `string(input.Visibility)` sends `'FOLLOWERS'` to a
    constraint that accepts `'followers'`, and `string(state.Storage)` returns `'local'` as an enum
    value that does not exist. The integration tests caught this TWICE in one slice — the second
    time on the read path, which no unit test would have reached — and the mappings are now named
    functions (`storedVisibility`/`visibilityEnum`, `storedStorage`/`graphqlStorage`) rather than
    conversions.
30. **A jsonb column preserves the VALUE and owns the TEXT** (§11). The cursor is opaque to the
    server, which is about meaning: Postgres normalises whitespace and key order, so a client
    comparing cursor strings byte for byte sees its own write come back different. Written into the
    schema's doc comment and asserted with a JSON comparison rather than string equality, because
    the test that said "byte for byte" was the test that was wrong.
31. **A `CASE ... ELSE NULL::uuid END` in a feed query is a nullability lie** (§10.3). It reads
    well and it compiles, but the cast tells sqlc the column is non-nullable while the rows contain
    NULLs, so a scan into a non-pointer field is a runtime error rather than a wrong value. The
    rule moved into a named Go function (`HideUnpublishedList`) with its own test, which is also
    where a reader can find out WHAT the rule is.
32. **A shared integration database makes every absence assertion a claim about the suite** (§10,
    §13). The feed is instance-wide, so a public grant made by one case is a row in every later
    case's feed, and the local tests' first versions failed for reasons that had nothing to do with
    what they named: a case reused the `(moderate -> read)` pair that an earlier case had already
    published. Each absence case now owns a distinct `(author -> viewer)` pair and filters its feed
    by a per-case title prefix, and the one case that assumed its rows were the HEAD of a global
    page now walks the whole feed instead. Same family as §25 and §26: the assertion was fine and
    the fixture was measuring the wrong thing.
