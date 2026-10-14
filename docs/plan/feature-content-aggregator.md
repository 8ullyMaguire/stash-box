# PLAN — the content aggregator programme (NexusXXX parity)

Status: **in progress** — phases 1, 2, 4 and 7 done; phases 3 and 5–17 not started. Phase 7's seven
slices are all complete, so the discovery programme this phase exists for is finished: the query
language, the tag explorer, freshness and trending, similarity, the taste profile and For You, the
social feed and "where I left off". The state table is
`docs/track/TRACK-content-aggregator.md`, which `docs/goal-check.py` clause C8 reads.

Implements `docs/spec/feature-content-aggregator.md`. Phase-level, because that is
the only level at which the ordering is decidable: each phase's code depends on the
previous phase's landed schema, and this repo's own commit log records what happens
when code is written against a schema that does not exist yet — *"Third round of
invented APIs in this feature … I wrote `WithTx(ctx)`, `GetUser`, `GetListItem`,
`OwnedBy`, `PublisherOf`, `User().Get` and `ListEntityTypeScene` before checking any
of them. Every one was wrong … Seven build cycles on fictions I could have read off
the tree in one grep."*

So: **every symbol in this plan was checked against the tree before being written
down**, marked with `Verified:`. Where a phase needs step-level code, that detail is
written when the phase starts, against the schema the previous phase landed — which
is this repo's existing pattern (spec + plan per feature, both written before that
feature's code, both revised as measured facts arrive).

## Environment (do this first — Phase 1 cannot be verified without it)

```bash
sudo systemctl start docker.socket docker.service      # docker is disabled at boot
docker run -d --name sbx-pg \
  -e POSTGRES_PASSWORD=smoke_pw -e POSTGRES_USER=postgres \
  -e POSTGRES_DB=stash-box-test \
  -p 127.0.0.1:55434:5432 stashbox-pg18:latest
export POSTGRES_DB='postgres:smoke_pw@127.0.0.1:55434/stash-box-test?sslmode=disable'
```

`POSTGRES_DB` is **not a URL** — a `postgres://` scheme makes pgx parse
`user=alvaro` and the suite dies in `pgDropAll` with `lookup postgres: no such host`
(`docs/track/HANDOFF-R074.md`). The image `stashbox-pg18:latest` already exists on
this host and carries `pg_search` + `bktree`; the bare `postgres` package on the
host does not, and migration 56 fails without them.

**Proven this session:** `go test -tags=integration -count=1 -p 1 ./internal/api/
-run 'TestListCreateReturnsAPrivateListWithTheRightShape'` → `ok … 1.835s`, and
`./scripts/provision-scratch-db.sh sbx-scratch` → `migrations: 104 applied`,
`version 107`, `tables: 86`.

## Order

**Priority order, and it is not the dependency order.** Owner instruction
2026-10-14: *"content aggregation and discovery (algorithms, search, etc) should be
the priority"* — so this section supersedes the sequence the first cut of this plan
carried (which was dependency-ordered and put discovery seventh). Phase NUMBERS are
unchanged, because the spec's parity table and `goal-check.py`'s clause C8 both
reference them; only the order they are worked in changes.

### 1. The aggregation spine, first and uninterrupted

**Phase 1** (source layer — half landed) → **Phase 2** (ingestion pipeline).

A URL becomes a content record: normalised, deduplicated against URL and
fingerprint, metadata extracted, tiered, health tracked. Nothing else in this
programme can be evaluated without it, and it is the one thing where "does it
work?" has an unambiguous answer a test can give.

### 2. Discovery, second — and this is the point of the programme

**Phase 4** (tag ontology: implications, hierarchy, ontology versioning) →
**Phase 7** (discovery: the query language, saved searches, tag explorer, freshness
and trending, then similarity, taste profiles, For You, serendipity, social
discovery, "where I left off").

Why 4 before 7 rather than after, which is also a reversal of the first cut:
faceted, structured tag search is the single differentiator §1's comparison table
names first, and the ontology is what makes a tag a query term instead of a string.
The current model already has aliases and nested categories, so this is an
extension, not a rebuild — and the discovery query language is written against the
finished model instead of being retrofitted onto a changing one.

Why the algorithm half (7.5 onwards) rather than a separate phase: it consumes the
same tag graph and the same search surface, and a similarity feature that cannot be
reached from a query is a feature nobody finds.

### 3. Breadth and durability, in parallel with the end of discovery

**Phase 3** (image/gallery/text/audio/comic on `content_items`) → **Phase 9**
(health daemon at scale, mirror resolution, resurrection, `expected_totals`
integration).

3 is deliberately after 7: the discovery machinery works over any content kind, so
it is built once against scenes and then extends uniformly to five more — the
repo's own "extend, do not fork per kind" rule. Aggregated content that is indexed
but never checked goes stale silently, so 9 follows immediately.

### 4. The supporting tiers, then everything else

**Phase 5** (tiers and gates; **before any public launch**, and its cost is recorded
in the spec) → **Phase 8** (social: typed comments that write edits, forums,
playlist extensions) → **Phase 6** (trust, sub-scores, gamification) → **Phase 11**
(growth) → **Phase 13** (API, interop, exports) → **Phase 14** (policy text) →
**Phase 10** (media plane) → **Phase 12** (federation extensions) → **Phase 15**
(monetisation) → **Phase 16** (UI, metrics, accessibility), with **Phase 17** (a
goal-check clause per phase) threaded through every phase rather than left to the
end.

The two hard constraints survive this reordering intact:

- **14's policy text precedes 10's first stored byte.** The moment this instance
  holds content it is a host, and §7.18 already schedules replication of member
  files across peers. Legal text after the fact is legal text that was never
  written. Note that this is *not* licence to store bytes before the gate (5) or
  the policy: discovery links to content that lives elsewhere, which is the whole
  reason the aggregator model was chosen.
- **`frontend` builds before `go build`, always.** `frontend/embed.go` is
  `//go:embed build`, so a stale SPA serves HTTP 200 and a blank page with no
  console error. The ordering rule is stated in
  `docs/plan/growth-items-19-20-21-24-28-29.md` and it applies to every phase below
  that touches the UI.

---

## Phase 1 — the source layer (the aggregator's first brick) — **DONE**

**Goal:** a scene can be a record of content that exists at a URL and nobody here
owns, with the URL's health tracked. NexusXXX §3.2 `sources[]` + §7.3's status
machine, on the existing `scene_urls` table.

**Verified:** `scene_urls` is `(scene_id uuid, url varchar, site_id uuid)` with
`UNIQUE (scene_id, url)` and an index on `lower(url)`; it has **no** status,
platform, timestamp or archive column. `sites` exists with a favicon and categories.
`EntityType` has exactly five values (`performer, scene, studio, site, tag`) —
adding a sixth is a schema change other code reads, so Phase 1 does not.

**Migration 108 `content_sources`:**

- `content_sources (id uuid pk, entity_type varchar(20) not null, entity_id uuid
  not null, url text not null, site_id uuid null, platform varchar(50) null,
  status varchar(20) not null default 'unknown', canonical boolean not null
  default false, last_checked timestamptz null, last_ok timestamptz null,
  first_seen timestamptz not null default now(), check_failures int not null
  default 0, archived_url text null, archived_at timestamptz null,
  created_by uuid null references users(id))`
- `CHECK (status IN ('unknown','live','intermittent','dead','dmca','archived'))`
- `UNIQUE (entity_type, entity_id, url)`, `INDEX (status, last_checked)` for the
  health sweep, `INDEX (lower(url))` for URL dedup.
- Backfill: `INSERT … SELECT 'scene', scene_id, url, site_id, 'live' FROM
  scene_urls` and **keep `scene_urls` in place** — 30 call sites read it and
  `internal/service/scene/service.go:461` loads it through a dataloader. The
  migration states which of the two is authoritative (answer: `content_sources`,
  with a `scene_urls` compatibility trigger, because the alternative is two sources
  of truth that disagree on the first write).

**Files:** `internal/database/migrations/postgres/108_content_sources.up.sql` (and its
`.down.sql`),
`internal/queries/sql/content_source.sql` (+ its generated `content_source.sql.go` and
`querier.go`), `internal/models/content_source.go` (the read model, hand-written — the
generated row struct is not the API shape), `internal/service/content/source.go`,
`internal/service/factory.go` (the `Content()` accessor), `internal/dataloader/
contentsourcesloader_gen.go` + `loaders.go` + the `Makefile` loader target,
`graphql/schema/types/content.graphql`, `graphql/schema/types/scene.graphql` (the
`sources` field), `internal/api/resolver_content.go`, `internal/api/resolver.go` (the
`ContentSource()` resolver accessor), `internal/api/scene_sources_integration_test.go`,
`scripts/verify-108.sh`, and later `frontend/src/graphql/{queries,types.generated}`.

**Status:** schema, reads, service, dataloader and the GraphQL field are landed and
covered by both harnesses. The frontend consuming `sources` is not, and there is no
mutation yet: electing a canonical source and recording a check result are phase 9's
callers, so the queries exist and nothing calls them (see the note on inert code in
phase 9).

**Verification:** `./scripts/verify-108.sh` (CHECK constraints proven against the
schema directly — a service test cannot tell "the database rejected this" from "the
service never asked"), then `go test -tags=integration -p 1 ./internal/api/
-run TestContentSource` asserting: a source with `status='dead'` is returned by the
scene query with that status; a duplicate URL for the same entity is rejected; a
`dmca` source is not served to a `READ`-only caller.

## Phase 2 — ingestion pipeline — **DONE**

**Goal:** submit a URL and get a content record — NexusXXX §7.2's pipeline, §7.1's
eight source classes, §3.3's dedup, §3.4's versioning.

**Design rule:** the extractor is an interface with one implementation
(`internal/service/ingest/extract/`) so the yt-dlp dependency is swappable and
testable without network. **Verified:** no yt-dlp wrapper exists in the tree; the
only importer is `internal/sdbimport` (stashdb.org dumps).

Steps, in one phase because each is useless alone: URL normalisation (strip tracking
params, resolve redirects) → dedup (URL match → attach source; fingerprint match →
cluster; else new scene with `origin='aggregate'`) → metadata extraction →
thumbnail + preview via the existing `collage` service → auto-tier (Phase 5's table,
defaulting T0 until it lands) → `pending` review when confidence is low, auto-approve
when the source site is trusted and confidence is high → archive submission.

**Files:** `internal/database/migrations/postgres/109_scene_origin_and_ingest_queue.up.sql`,
`internal/database/migrations/postgres/110_content_source_normalized_url.up.sql` (both with
their `.down.sql`),
`internal/queries/sql/ingest.sql` (+ generated), `scripts/verify-109.sh`,
`internal/service/ingest/` (normalise + dedup decision as pure functions, then the
pipeline), `internal/service/ingest/extract/` (the extractor interface, the production
implementation and the fake), `internal/cron` entry for the queue drain,
`internal/api/resolver_mutation_content.go` (mutation `submitContentUrl(url:, sceneId:)`),
`scripts/verify-110.sh`, `internal/api/ingest_integration_test.go`,
`docs/spec/feature-ingestion.md`.

**Not yt-dlp (a correction to the first cut of this plan).** The extractor reads OpenGraph
tags over `net/http` — no media toolchain in the dependency graph, no download, and no
fingerprints, which is stated at the package comment rather than discovered later. The
interface is what makes swapping in a yt-dlp-backed extractor a small change when a phase
needs one; the first cut needs a title and a length, and every yt-dlp-shaped dependency
would be a toolchain to install for that.

**Status:** LANDED — phase 2 is complete. Schema and decisions as above, and now the
pipeline itself: `internal/service/ingest/extract/` (the `Extractor` interface, the
production `HTTPExtractor` reading OpenGraph tags with nothing beyond `net/http`, and the
`Fake` every rule test uses), `internal/service/ingest/pipeline.go` (`Submit`, `Drain`,
`process`), the `@every 1m` drain in `internal/cron`, and the `submitContentUrl(url:,
sceneId:)` mutation under an Edit role rule.

Three decisions the implementation forced, each recorded where it is made and not only
here:

1. **The fetch happens only when a record might be created.** Stage 3's URL-level
   questions (refused? already claimed? a named target?) are answered from the database
   BEFORE the network is touched. The first cut fetched first and decided second, which
   meant a URL a human had taken down was requested again -- and a URL this instance
   already had was re-fetched for metadata nobody would read. `confidence` is NULL on
   those rows rather than 0.0, because 0.0 would say the extractor looked and found
   nothing.
2. **`gone` is `failed`, not `done`.** The spec's §5 table said a 404 marks the source
   dead and closes the row as done. That is wrong for this path: there is no source row
   to mark when nothing claims the URL, and where something does claim it, health is the
   sweep's job (phase 9) rather than the ingest path's. Corrected in the spec.
3. **The pipeline creates the directory entry for a host it has never seen.**
   `scene_urls.site_id` is NOT NULL, so without a site row there is no MEMBERSHIP -- and
   the scene page, the completion score and every other reader of a scene's URLs read
   `scene_urls`, not `content_sources`. A pipeline that wrote only the health row
   produced sources that existed, were queryable by hand, and appeared on no page.
   `EnsureIngestSite` upserts on the host name with `highlighted = FALSE` (overriding the
   column's true default: the directory's featured list is a curated one) and the host as
   its regex, which is what makes it run once per host instead of once per URL.

**Verification:** `./scripts/verify-110.sh` — 17 checks: the integration run's exit code,
that nothing was SKIPPED (a skipped integration test exits 0 and reads as a pass), that
the `-run` filter matched tests at all, every named test's PASS line, the real-extractor
test, and the unit suites beneath it. The suite is
`internal/api/ingest_integration_test.go` — real database, real migration chain, and for
its last two tests a real HTTP server read by the production extractor, so the only thing
faked anywhere is the public internet. Four mutations, each reddening exactly its own
test: the named-target rule deleted (`attached` became `created`), the takedown denial
disabled (the page was fetched and the URL re-attached), the `scene_urls` membership write
removed (the created record's source list came back empty — the bug the integration test
was written to catch, and it caught it), and the retry policy made uniformly transient (a
404 landed `pending` instead of `failed`).

## Phase 3 — the other five content kinds

**Goal:** NexusXXX §3.1's image/gallery/animation/text/audio/comic alongside video.

**Decision (from the spec):** the five non-video kinds land in `content_items` with
`EntityType` extended by one value, `content_item`, rather than five entity types —
one new enum member instead of five, and every polymorphic table (reviews,
completion, elo, lists, expected_totals, denylist) works unchanged.
`content_items (id, kind varchar(20) not null check (kind in
('image','gallery','animation','text','audio','comic')), title, details,
release_date, created_at, updated_at)` plus `content_item_chapters` for text/comic
progress and `content_item_media` for variant URLs (Phase 10 fills it).

**Verified:** adding an `EntityType` member touches `graphql/schema/types/
completion.graphql:57`, `internal/models`, and any `switch` over the enum — `rg
'EntityType' internal/` before writing, and every switch must gain an explicit case
or the compiler says so (Go exhaustiveness is manual; the plan requires a test that
enumerates the type list and asserts each has a completion mapping, which is how
`internal/api/completion_weight_parity_test.go:143` already guards weights).

**Verification:** integration test that a text item takes tags, a review, an elo
matchup and a list entry with no new plumbing.

### Slice 3a — the tables and the data layer — DONE (`ac5b8791`)

Migration 117 (`content_items`, `content_item_chapters`, `content_item_media`,
`content_item_tags`), the generated statements for all four, and four integration
tests (10 cases) written over those statements rather than over a service — the
repository's rule, because a service-level test cannot tell "the constraint refused
this" from "the service never asked". 3/3 mutations killed, two of them editing the
MIGRATION itself (the harness re-runs migrations, so a schema mutation is genuinely
applied on the next run).

**Three places this slice departs from the text above, each recorded:**

1. **`deleted` is in the table now, not in a later migration.** `scenes` has carried
   it since the initial schema and every read filters it; retrofitting that filter
   across N queries is the omission class this codebase keeps finding. The plan's
   column list did not name it.
2. **Tags needed a link table, so "no new plumbing" is true of the polymorphic
   surfaces and NOT of tags.** `scene_tags` is scene-only (migration 01), and the
   verification above asks that a text item take tags. `content_item_tags` mirrors
   `scene_tags` rather than replacing it with a generic `entity_tags`: the tag
   queries, facet counts and the implication closure are all scene-shaped and
   hot-path, and rewriting them for one new kind is the larger cost. The migration's
   own comment states the trade and the point at which it flips (a third taggable
   kind).
3. **The polymorphism claim is measured, not test-proven at this layer.** The
   planning note said to check `rg 'EntityType' internal/` before writing; the
   measurement is stronger than that — **no migration enumerates entity kinds in a
   CHECK**, and 108's `content_sources_entity_type_check` already accepts
   `('scene', 'content_item')`, so the kind needed no constraint change anywhere.
   What is NOT yet true is acceptance by the Go vocabularies that enumerate types
   (`completion.AllEntityTypes`, `EloEntityType`, the review validator). Those are
   closed lists in code, so slice 3b adds the member and the compiler plus
   `completion_weight_parity_test.go` force every site. A DB row that no vocabulary
   knows about is a row that is written and never counted, which is why 3b is next
   and why this slice deliberately did not expose the kind on the GraphQL enum yet.

## Phase 4 — the tag ontology — **DONE**

**Goal:** NexusXXX §4.1–§4.6. **Verified:** `tags` has `name, description, aliases,
deleted, category_id, created_at, updated_at`; `tag_categories` nests (migration 106,
`tag_categories_parent_id_idx`, cycle trigger, `TagCategoryTreeNode` with
`parent/ancestors/descendants`); there is **no** tag-to-tag relation and no ontology
version.

**Migrations:** 110 `tag_implications` (`tag_id, implied_tag_id, created_by`,
cycle-guarded the same way 106 is — reuse the CTE approach, do not invent a second
one), 111 `ontology_versions` (`version int, snapshot jsonb, created_at,
created_by`) + `ontology_changes` (proposal → discussion → vote → implement, with
the change kind enumerated). Tag `content_tier` and `requires_opt_in` arrive in
Phase 5 with the tier table.

**Decision:** implications are **derived at read time with one owning table**, never
duplicated onto tag rows. The fork's §3.2 "generated vs hand-written — know which is
which" is the rule; a materialised implication list is a second source of truth that
disagrees on the first merge.

**Status:** LANDED — phase 4 is complete. Migrations **111** `tag_implications` (the pair, the
uniqueness, the implied-tag index, the cycle trigger) and **112** `ontology_versions` (the
version table and `record_ontology_version`); `internal/queries/sql/tag_ontology.sql`;
`internal/service/tagontology/` (the reader: the closure, the recursive SQL, the merge
retarget); the surface in `graphql/schema/types/tag.graphql` (`Tag.implies`, `implied_by`,
`implies_transitive`, `implied_by_transitive` with `TagAtDepth`, `TagImplication`,
`OntologyVersion`, and `added_implications`/`removed_implications` on the edit details); and
`ApplyMultiIDCriterionExpanded` in `internal/service/query/criterion.go`.

Three things the implementation decided, each also recorded where it is made:

1. **`INCLUDES_ALL` is one `EXISTS` per REQUESTED tag, not a count over an expanded list.**
   "All of these" is a per-requested-tag question: an item tagged only `cowgirl_position`
   satisfies both `woman_on_top` and `vaginal_sex` — which it implies — while contributing
   ONE join row, so `COUNT(*) = len(expanded)` refuses it. The counting form stays for the
   no-hierarchy case, where it is correct and equivalent.
2. **The version bump is a DATABASE trigger, not applier code.** One version per change
   STATEMENT covers the edit applier, a merge's retarget, a tag deletion's cascade and a
   future seeding migration — a counter maintained in Go would miss the paths that do not go
   through it, which is this project's recurring bug class. The cost is in the migration: a
   merge that retargets three implications writes three versions. The bump takes
   `LOCK TABLE ontology_versions IN EXCLUSIVE MODE`, because a concurrent writer otherwise
   computes the same `MAX(version)+1` and dies on the primary key.
3. **The expansion is wired into the scene query, which is the only tag-filtered item
   surface this codebase has.** `SceneQueryInput.Tags` is the one tag criterion
   (`PerformerScenesInput.Tags` builds a `SceneQueryInput` and flows through the same
   builder), so "search" here means that builder rather than five call sites.

**Verification:** `scripts/verify-111.sh` (21/21) and `scripts/verify-112.sh` (28/28) against
`sbx-scratch`, covering the constraints, the trigger's two arms, the cascade, the ordering of
the merge retarget (the statements extracted BY NAME from the query file, so the harness
tests what sqlc generates from), and the version lock; `criterion_expansion_test.go` for the
SQL shape per modifier; `tag_ontology_integration_test.go` — six tests through GraphQL against
a real database, each asserting the negative before the implication exists; two mutations,
each reddening exactly one test and no neighbour; and the lock proven by dropping `LOCK TABLE`
from the LIVE function and watching the duplicate-key failure it exists to prevent.
`docs/plan-verify.py` and `docs/goal-check.py` are run over this file and the track row.

## Phase 5 — tiers, warnings and gates

**Goal:** NexusXXX §10. **Verified:** `content_denylist_tag/studio/performer`
(migration 87), `AccessRuleSet` reporting `enforced` vs `unenforced`, and the
migration-87 argument that an entity id is checkable while a region or User-Agent is
a client claim.

**Migration 112 `content_tiers`:** `tags.content_tier smallint not null default 0`
with `CHECK (0 <= content_tier and content_tier <= 3)`,
`tags.requires_opt_in boolean not null default false`,
`users.opted_in_tiers smallint[] not null default '{}'`, and a view
`entity_effective_tier (entity_type, entity_id, tier)` computed as the **max** of its
tags' tiers. **Denylist-shaped**: a tier can only ever remove visibility, never grant
it — the rule migration 87 states and the reason an allowlist was refused.

**Also:** the onboarding wizard (frontend), the warnings-vs-gates split, and the
cooling period for T3.

**Verification:** a T2-tagged scene is absent from search, feeds, recommendations and
similar-to for a member who has not opted in — all four paths asserted separately,
because "invisible" is an absence claim and this repo already had an absence
assertion pass with the thing on screen (`docs/track/WORKLOG.md`: *"a test asserting
'no nav label appears twice' PASSED WITH THE DUPLICATE ON SCREEN"*). Each absence
assertion is preceded by a count assertion of the subject.

## Phase 6 — trust, sub-scores, gamification

**Goal:** NexusXXX §5.2–§5.4, §6.1–§6.5. **Verified:** `trust` is derived by a curve
in `internal/service/trust`, exposed as `UserTrust` behind `@isUserOwner`;
`streak.graphql` has `currentStreak/longestStreak/totalActiveDays/activeToday/
lastActiveDay`; `quest`, `badge`, `award`, `authored` services exist; `elo` provides
leaderboards.

**The decision this phase exists for:** which sub-scores become public. The draft
makes them public (§5.2, §20.2); `user.graphql` says a user's standing is not other
users' business. Resolution to implement: **level public, weights and error rates
private**, with the visibility rule stated in the schema comment so a reader does not
have to infer it — the pattern `list.graphql` uses.

**Also:** decay with grace and recovery (§5.3), streak freeze, vouching with a
maximum of three active, contribution heatmap, leaderboard filters by level and
opt-out, seasonal events.

**Verification:** a member with two flags loses TL3 after the grace period and
regains it in 14 days; a fourth vouch is refused; a member who opts out of
leaderboards keeps every capability.

## Phase 7 — discovery

**Goal:** NexusXXX §8. **Spec:** `docs/spec/feature-discovery.md` (the eight modes, the language
grammar, the decisions and what each slice defers).

**Verified:** search exists and is ranked (`scene_search`, ParadeDB BM25 via `pg_search`, with
`details`/`director`/`tag_names` added additively by migration 103 plus a statement-level
trigger set and `Scene.Drift()`); `SceneQueryInput.Text` is already the free-text field of the
same struct every other filter lives in; "trending" since migration 60 is a materialized view
over `scene_fingerprints` counting distinct fingerprint submitters — **fingerprint
submissions, not engagement**; `elo` supplies ranking; `performer_similar` is the nearest thing
to similarity; and pgvector is **not installed** on this instance (§2 of the spec: only
`bktree`, `pg_search`, `pg_trgm` and `cube` are available), so the plan's "a pgvector column is
the cheap first implementation" is not available here. That correction is recorded in the
spec's §14.

**Seven slices, in order.** Each is one commit with one verification command whose output is
part of the commit message.

1. **The query language. DONE** (commit `35ddd125`).
   `internal/service/search/query_language.go`
   (`Parse`, `Render`, `Equal`, the `Expr` tree), `lower.go` (`Lower`: the expression merged into
   a `models.SceneQueryInput` through a `Resolver` interface, plus the `Diagnostic`s and
   `NoMatch`), `query_language_test.go`, `lower_test.go`.
   **Verification:** `go test ./internal/service/search/ -v` → 25 cases over the term kinds
   (each row also asserting the round trip and that rendering is idempotent), the term-shape
   assertions, the 9 error cases with their offsets, and 12 lowering tests.
   **Mutation-checked** by `scripts/mutate-search-language.py` (four mutations, each restored and
   re-run): `NoMatch` forced false, the unresolved-name append removed, the unknown-field check
   disabled, and the `sort:-date` normalisation disabled — each reddened exactly the tests that
   name the behaviour and nothing else, and restoring returned the suite to green. Nothing is
   wired yet, so this slice's own tests are its only surface, which is why the integration test
   belongs to slice 2, where the text reaches a resolver.
2a. **The language reaches the query surface. DONE** (commit `fb73769d`).
   `SceneQueryInput.query` (NOT the deprecated `text` field, which clients still send),
   `exclude_tags` and `duration` criteria with their builder branches, the two new fields added
   to the filter-presence list at `scene/query.go:355`, `QueryScenesResultType.diagnostics`,
   `internal/service/scene/query_language.go` (the `Resolver` over `FindTagByNameOrAlias` /
   `FindPerformerByName` / `FindStudioByName`, plus the redirect fallback that makes a
   merged-away tag name still resolve), and
   `internal/api/scene_query_language_integration_test.go`.
   **Verification:** `POSTGRES_DB=... go test -tags integration -p 1 -run 'Language|Unapplicable|DurationComparison|MergedTag' ./internal/api/` → ok: the language and the hand-written
   filter return the SAME scenes against a real database (`anal -rough`); an unapplicable term
   (`score:`, a misspelling, an unknown tag) returns EMPTY with exactly one diagnostic naming it;
   `duration:>300` reaches SQL; and a query over a merged-away tag still finds its scene.
   **Mutation-checked** by `scripts/mutate-query-language-integration.py`: dropping the
   `exclude_tags` criterion, disabling the duration branch, dropping the redirect fallback
   (the name-only lookup that FAILED the first time the merge test ran), and disabling the
   `noMatch` short-circuit each reddened exactly one test and restored green.
2b. **Saved searches and their notifications. DONE** (commit `af0669eb`).
   Migration **113** (`saved_searches`: `UNIQUE (user_id, name)`, `last_notified_at` cursor, a
   partial `WHERE notify` index, the `SAVED_SEARCH_MATCH` enum value), its sqlc queries,
   `internal/service/savedsearch` (CRUD scoped by owner, plus `NotifyMatches`),
   `internal/service/scene/query.go`'s `created_since` criterion (in the filter-presence list),
   the GraphQL type and its five operations, and a 15-minute cron wrapper
   (`saved_search_notify_interval`).
   **Verification:** `POSTGRES_DB=... go test -tags integration -count=1 -run 'SavedSearch|AnotherUsersSavedSearch|ATermItCannotApply' ./internal/api/` → ok: save/list/find/rename/
   subscribe/delete; another user's id is null, their delete is false, the owner's row survives;
   `results` agrees with `queryScenes` on the same fixture; page 2 differs from page 1 (so
   the paging arguments are shown to reach the query); an unapplicable term returns empty WITH
   one diagnostic naming it; and the job announces nothing on the first run, writes ONE
   notification whose target is the newest match and whose payload names the search, writes
   none on a second run over the same state, and loses its notifications when the search is
   deleted.
   **Mutation-checked** by `scripts/mutate-saved-searches.py`: dropping the `created_since`
   clause, ignoring the paging arguments, removing the first-run cursor rule, and removing the
   ownership check in delete each reddened exactly the test that names that behaviour and
   restored green.
   **Two failures found by the tests, not by review:** a Go process in +02:00 writing its local
   wall clock into a `timestamp` cursor sat two hours ahead of `scenes.created_at` (every run
   found nothing, silently — the cursor now comes from `SavedSearchNow`), and the job ran with no
   current user in its context, which the query path dereferences (it now runs as the search's
   owner, which is also the only correct reading of `favorites:`).
3. **The tag explorer. DONE.**
   `internal/service/scene/facets.go` (the facet statement: a hand-built `WITH RECURSIVE` whose
   seed IS `buildSceneQuery`, so the counts are scoped by the same builder the results came from;
   the closure arm that makes a count equal what a filter on that tag returns; the exclusion of
   the tags the filter already applies; strength against the instance), the sqlc query
   `CountScenesForTagsThroughImplications` (the same closure seeded with a SET, so one statement
   answers every facet's denominator — and `Tag.scene_count` reuses it with a single seed),
   `internal/models/model_tag_facets.go`, `TagFacets`/`TagFacet` and
   `QueryScenesResultType.facets(limit, min_count)` in `graphql/schema/types/{tag,scene}.graphql`,
   the `querySceneResolver.Facets` and `tagResolver.SceneCount` resolvers, and
   `internal/api/scene_facets_integration_test.go`.
   **Verification:** `POSTGRES_DB=... go test -tags integration -count=1 -p 1 -run 'Facet|TagSceneCount' ./internal/api/` → ok, on a fixture where `specific` implies `general` and the
   counts differ from the naive ones by construction: the facet for `general` is 3 (one direct
   plus two through the hierarchy) and the same number is compared against `queryScenes` with
   `{marker, general}` under `IncludesAll`; `specific` is 2; the filter's own tag and a tag no
   result scene carries are absent; strength is 0.75 for `general` (3 of the instance's 4) and
   1.0 for `specific`, every strength in (0, 1]; `tagged_count` is 3; `limit: 1` returns the
   most-used tag alone and `min_count: 3` drops the tag with 2; `Tag.scene_count` is 4, 2 and 0.
   **Mutation-checked** by `scripts/mutate-scene-facets.py` — 4/4 caught and restored: removing
   the closure's recursive arm (counts become the plausible-looking direct ones), removing the
   applied-tag exclusion, reporting strength as a raw count, and hardcoding `limit`/`min_count`
   so the arguments never reach the statement.
   **One test bug found by the test itself:** the agreement check was first written with
   `CriterionModifierIncludes`, which is a UNION — `{marker, general}` matched the scene outside
   the result set too, returning 4 against the facet's 3. The facet was right and the comparison
   was wrong; `IncludesAll` is the modifier that means "this tag among these results", and the
   spec records why the distinction is part of the contract rather than an implementation
   detail.
4. **Trending and freshness. DONE.**
   Migration **114** (`scene_freshness`: a materialized view — `score`, `recent_score`,
   `earlier_score`, `computed_at` — with the unique index CONCURRENTLY needs, the two supporting
   indexes, and the decayed sum over the three signals), `Scene.RefreshFreshness`,
   `cron.refreshSceneFreshness` on the popularity refresh's hourly cadence,
   `SceneSortEnumFresh` and `SceneSortEnumRising` with their branches in
   `internal/service/scene/query.go`, and `internal/api/scene_freshness_integration_test.go`.
   **Verification:** `POSTGRES_DB=... go test -tags integration -count=1 -p 1 -run 'Freshness|Rising|OlderButBusier|SceneWithNoSignals|Withdrawn' ./internal/api/` → ok, on fixtures whose
   signal ages the test controls: the decay against hand-computed values (`1.060660` at 48h,
   `0.314270` at a week, `0.010828` at 83 days), the three signals summing, an unrated review
   contributing nothing, a withdrawal worth −3 and ranking below an untouched scene, the 90-day
   window as a boundary, ten old signals ranking below one fresh one, `FRESH` and `RISING`
   disagreeing on one fixture, and a signal staying invisible until the refresh runs.
   **Mutation-checked** by `scripts/mutate-scene-freshness.py` — 5/5 caught and restored, four of
   them mutating the MIGRATION because the score is computed in the database: the decay half-life,
   the review weight, `3 * vote` reduced to `3`, the window widened to 180 days, and `RISING`
   summing the two days instead of comparing them.
   **One correction the implementation forced, and it was mine:** this plan and the spec both said
   the draft's fingerprint-withdrawal term (−3) was unimplementable because a withdrawal deletes
   the row. `scene_fingerprints.vote` has existed since migration 40 (`CHECK (vote = -1 OR vote =
   1)`), the upsert stores a withdrawal as −1, and the GraphQL input takes `vote: REMOVE`. So the
   term is there as `3 * vote`, and the spec's §14 decision 14 records the reversal.
   **Also corrected:** `fingerprints.hash` is a BIGINT (migration 52 — a phash is a 64-bit number,
   which is what its bktree index needs), so a fixture hash is an integer; and the package's scene
   fixture submits a fingerprint as part of creating a scene, so every score it makes carries a real
   +3 that the test must remove before it can assert a known number.
5. **Similarity and its explanation. DONE.**
   `internal/service/similar/`: `store.go` (the `VectorStore` interface with `Noop` and
   `InMemory` — cosine, deterministic tie-break, refusals for a wrong width and a zero vector),
   `explain.go` (the weights and the reason builder, pure), `scene.go` (candidate selection, the
   ontology walk, the score, the vector re-rank), `internal/queries/sql/scene_similar.sql` (the
   three queries), the `Scene.similarScenes` field with `SimilarScene`/`SimilarityReason`, the
   `SimilarityReasonKind` enum and `internal/api/resolver_scene_similar.go`, and
   `similar_vector_store` in the config read by the factory.
   **Verification:** `go test ./internal/service/similar/` (cosine against known angles; the
   refusals; ranking and tie stability; the weights, the three tag kinds, the duration falloff
   and the absent-runtime case; the capacity denominator and its zero guard) and
   `POSTGRES_DB=... go test -tags=integration -count=1 -p 1 ./internal/api/ -run TestSimilarScene`
   → 6 tests green, each reconstructing the score from the reasons it was sent and asserting the
   ontology in BOTH directions on independent tag pairs. Two real defects were found by those
   tests rather than by review: `sameStudio` was never set, so the candidate was selected on the
   studio and no reason was ever emitted (the score read lower than its own reasons could
   explain); and the vector bonus was added into `score`, contradicting the schema.
   **Mutation-checked** by `scripts/mutate-scene-similar.py` — 7/7 caught and restored: the
   performer weight, the duration half-width, the studio reason, the score/ranking separation,
   the two ontology directions, and two candidate-predicate cases that had to mutate the
   GENERATED `scene_similar.sql.go` because sqlc compiles the statement into Go (mutating the
   `.sql` changed nothing that runs, and the case reported NOT CAUGHT until that was fixed).
   **Measured** (`SIMILAR_BENCH=1`, 512 dims, 200 samples): 1.3 ms at 1,000 vectors, 16.4 ms at
   10,000, 187 ms at 100,000 — recorded in spec §8.4 with the conclusion that an ANN index is
   justified for a whole-archive nearest-neighbour surface above ~10,000 vectors and not for
   this feature, which only ever asks the store about a bounded candidate pool.
   **Not here, deliberately:** no `minShared` floor, no count field (the pool is bounded, so a
   count would be a lie with a number on it), and no embedding producer — nothing on this
   instance turns a scene into a vector.
6. **Taste profile, For You, serendipity. DONE.**
   `internal/service/taste/` (`service.go` — the pool, the affinity scoring, the contribution
   ordering, the no-signals fallback, and `mixFeed`, the pure stride rule; `mix_test.go` — the
   subset property at 0, the contrast at 1 over the same seed, the stride slots, the degenerate
   pools), `internal/queries/sql/taste.sql` (the affinity map with one arm per signal kind, the
   recursive scoring walk with the per-hop fraction, and the engaged-scene exclusion), the
   `forYou(input: ForYouInput)` query with `ForYouScene`/`AffinityContribution`, and
   `Tag.FindByIds` (one query for the tags the contributions name, not one per contribution).
   **Verification:** `go test ./internal/service/taste/` and
   `POSTGRES_DB=... go test -tags=integration -count=1 -p 1 -run TestForYou ./internal/api/`
   → 8 tests green: each signal's weight (`1.0` / `0.8` / `±0.5`), the four clauses that are
   separate claims (published-only, nullable-rating, the negative sign, the withdrawal), the
   §9 acceptance criterion as two numbers (parent `0.4` against direct `1.0`), the engaged
   exclusion covering all three signal kinds, the query language really lowering the filter
   (`-tag`), the degenerate-output test at the resolver over the same seed, and the no-signal
   page making no claims. Three real defects were found by these tests rather than by review:
   `lists` has no `user_id` column (**which sqlc did not catch** — the statement was checked
   against generated types, not the schema, so the feed failed with `column l.user_id does not
   exist`), a recursive CTE exposing `weight` while the outer term read `a.affinity`, and a
   fixture colliding on `lists_unique_name_per_owner`.
   **Mutation-checked** by `scripts/mutate-scene-for-you.py` — 11/11 caught and restored: the
   three weights, the published-only and nullable-rating clauses, the propagation factor, two of
   the three engaged arms, the zero-weight filter, the serendipity stride, and the off-by-one in
   the stride rule (a unit test). Eight of them mutate the GENERATED `taste.sql.go`, for the
   reason slice 5 recorded: sqlc compiles each statement into a Go string literal, so a `.sql`
   edit changes nothing that runs.
   **Not here, deliberately:** no stored taste profile (derived at read time), no view/skip
   signals (no producer until phase 10), no blacklist or score floor (no stored preference until
   phase 16), and no "reset my taste" mutation — §12 lists it as deferred, and a delete-the-
   signals button is a data-deletion surface, not a feed feature.
7. **Social discovery and "where I left off". DONE.** Migration **115** (`user_follows`:
   the pair, `activity_visibility` defaulting to `private`, the index the feed reads) and **116**
   (`session_states` with the CHECK that makes a `local` row with a payload unrepresentable); the
   three-arm chronological activity feed with its opt-in sync surface.
   `internal/service/follow/` (the levels, `Follow`/`Unfollow`/`SetVisibility`, where the AUTHOR is
   the session and the follower is the argument), `internal/service/activity/` (`Feed`, the three
   bounded arms, and `mergeItems` — the viewer's own activity, the timestamp order with its
   deterministic tiebreak, the page cut — plus `HideUnpublishedList`), `internal/service/sessionstate/`
   (validation, the local-storage rule, the per-user reads), `internal/queries/sql/follow.sql` and
   `session_state.sql` with their generated counterparts, the `activityFeed`/`following`/`followers`
   queries and the `followUser`/`unfollowUser`/`setActivityVisibility` mutations, and
   `sessionState`/`sessionStates`/`saveSessionState`/`clearSessionState`.
   **Verification:** `go test ./internal/service/activity/ ./internal/service/follow/
   ./internal/service/sessionstate/` and
   `POSTGRES_DB=... go test -tags integration -count=1 -p 1 -run 'Activity|Followers|Feed|Follow|
   SessionState|SwitchingToLocal|DatabaseRefuses' ./internal/api/` → 16 integration tests and three
   unit packages green, in the package's FULL run and not only alone.
   **The two acceptance criteria are asserted, not assumed:** the visibility default is private
   (following an author with activity yields an empty feed, and the same activity appears the
   moment the author publishes through the mutation) and the feed is chronological (rows inserted
   in one order and AGED in SQL into another come back in timestamp order, with a pairwise
   descending check and a full walk in pages of two that never repeats a row).
   **The absence assertions count their subject first** — this slice needed it three times, and it
   is what caught the design's own leak risk: a per-follow grant reaches its named follower while
   a third user sees nothing, and a follower's own successful grant leaves their access to the
   author's activity unchanged (read back from the row). The local-first claim is asserted at the
   COLUMN (`SELECT cursor IS NULL`), and one test inserts a `local` row with a payload through raw
   SQL to prove the DATABASE refuses it — the API returning null would also be true of a resolver
   that stored the payload and hid it.
   **Mutation-checked** by `scripts/mutate-social-activity.py` — 11/11 caught and restored: the
   follow's default visibility, the "who granted me something" filter, the per-follow vs per-user
   grant, the public broadcast, the withdrawal filter, the unpublished-list rule, the viewer's own
   activity, the sort direction, the page cut, the local save's payload, and the CHECK itself.
   Four mutate the GENERATED `follow.sql.go`, for the reason slice 5 recorded.
   **What the tests found, not review:** the GraphQL enum and the column disagree about case, in
   BOTH directions (`'FOLLOWERS'` against a CHECK that accepts `'followers'`, and `'local'`
   returned as an enum value that does not exist) — two failures in one slice, the second on the
   READ path; a `CASE ... ELSE NULL::uuid END` that hid real NULLs from sqlc behind a cast, so the
   rule moved into a named Go function; and the paging test that assumed its four rows were the
   head of a global feed, rewritten to walk the feed instead.
   **Not here, deliberately:** no follow notifications (phase 11), no visibility-editor UI (phase
   16), no TTL on a session cursor (an expiry would silently forget a position someone came back
   for), and no ranking input on the feed at all.

## Phase 8 — social

**Goal:** NexusXXX §9. Typed comments (`discussion`, `source_info`, `performer_id`,
`timestamp`, `correction`, `request`, `review`) with **structured fields**, routed
into an **edit** rather than writing metadata directly — §7.25.3's "a vote is
evidence, never authority" is the rule that survives contact with the identification
board. Forums (searchable, governance-gated), playlist extensions (fork, auto-update
from a saved search, collaborative) on the existing `list` tables.

**Verification:** a confirmed `performer_id` comment creates an edit, and applying
that edit links the performer — the test asserts the *edit* exists, because the
failure mode is a comment that silently writes the link.

## Phase 9 — preservation at scale

**Goal:** NexusXXX §7.3–§7.4 on top of Phase 1's status column: the health daemon
(frequency by popularity × reliability), status transitions with the documented
failure thresholds, resurrection queue, mirror resolution through the fingerprint
cluster, Wayback submission, `expected_totals` integration (a dead source lowers
completion and raises a quest), performer profile snapshots.

**Verified:** `expected_totals` (migration 97) already scores studio/network totals
and points at a url row via `(source_entity_type, source_url)` — Phase 9 makes that
pointer resolve to a `content_sources` row.

**Verification:** kill a seeded source, run the sweep, assert status transitions
`live → intermittent → dead` at the documented thresholds and that the entity's
completion score drops; restore, assert resurrection.

## Phase 10 — media plane

**Placed elsewhere (2026-10-14):** the local models this phase wants — a face matcher
(StashFace ONNX / MegaFace) and image taggers (WD14 / pixai) — are specified as
*identification sources* in `docs/spec/feature-04-identification-federation.md` §8 (F7–F12),
with their build steps in that feature's plan (step 7c/7d). Read those before designing this
phase: two decisions there are not negotiable here — a model's output is a SUGGESTION (for tags,
an edit draft, because the edit system is the only path that writes a tag), and face detection
serves face-centred crops (complaint #84) as well as the identification board.

**Goal:** NexusXXX §7.5, §13.1–§13.5. **This phase is gated on Phase 14's policy
text.** Hosting (MinIO/S3 via the existing `storage` abstraction), variant ladder,
transcoding (ffmpeg — a new dependency, so a decision record and a fallback plan),
the five players, PWA with offline caching, and the CDN/edge discussion in §19.1.

**Verified:** `internal/storage` and `internal/image` already do local/S3 with
resizing and caching; `collage` already extracts frames; `frontend/embed.go` is
`//go:embed build`.

**Verification:** upload → transcode → play at three qualities through the real
handlers; a text item renders a reader with progress persisted; a comic honours
right-to-left; the PWA installs and serves a cached page offline.

## Phase 11 — growth: invites, referrals, cohorts

**Goal:** NexusXXX §11. **Verified:** `InviteKey` (uses, expiry, `invited_by`) and
`RoleEnum {…, INVITE, MANAGE_INVITES}` exist; nothing allocates, regenerates, caps or
rewards.

**Verification:** allocation by level with regeneration and caps; a referral chain
pays on the invitee reaching TL1 (never on signup) with one level of depth; a QR code
renders and its scan/signup/activation counters move; a cohort campaign's fast-track
applies only to members of that cohort.

## Phase 12 — federation extensions

**Goal:** NexusXXX §12.3–§12.6 on top of the existing mesh (§7.17/§7.18): ontology
version federation and mapping, the content hash network, specialised instances,
ActivityPub last. **Verified:** `federation` service with peers and foreign
candidates; `FederatedContentRef`-shaped work is already further along here than in
the draft.

**Verification:** two instances exchange an ontology version and resolve a local tag
against a core tag; a hash lookup returns mirrors; an instance that declares core
v1 is refused a v2-only field rather than silently mis-mapping.

## Phase 13 — API, interoperability, exports

**Placed elsewhere (2026-10-14):** exposing stash-box as an **MCP server** (search scenes and
performers, read records, identify files by fingerprint) is the cheapest interop in the
programme, because every surface it needs already exists. Consuming an external MCP source
(ThePornDB, desaira) is NOT a second ingestion pipeline: it is an identification *provider*
in the sense of `docs/spec/feature-04-identification-federation.md` §8 — evidence with
provenance, never a vote.

**Goal:** NexusXXX §15. **Verified:** GraphQL only; `internal/webhook` exists;
no REST, no RSS, no data export.

**Verification:** RSS for a saved search validates against a feed parser; a webhook
fires on a named event; the export round-trips into Hydrus-shaped JSON; rate limits
are enforced per the §15.1 table and a limit breach returns 429 with the same body
shape as every other error.

## Phase 14 — moderation and legal (policy text before Phase 10)

**Goal:** NexusXXX §17 — CSAM hash matching against NCMEC/IWF lists, DMCA with
counter-notice and a registered agent, performer age verification as a precondition
for real-person content, verified consent removal within 24 hours, appeals heard by a
different moderator, elected moderators, published transparency reports.

**Verified:** the fork's SPEC contains **zero** DMCA/takedown/CSAM/consent/age text
(the only "DMCA" occurrence in the whole repository is inside a captured upstream
issue body in `docs/track/issues-open-full.json`), while §7.6 level 4 grants upload
and download and §7.18 replicates member files between peers.

**Written per-instance and per-peer relationship**, not per-platform: what a peer
instance must do when it receives a takedown, and what happens when it refuses.
`content_sources.status='dmca'` (Phase 1) is the enforcement point.

**Verification:** a hash-matchable seeded item is refused at ingest; a DMCA'd source
stops being served while its metadata stays (the metadata/media split's whole
point); an appeal is heard by a different moderator — asserted by identity, not by
role.

## Phase 15 — monetization

**Goal:** NexusXXX §18 as **operator configuration**, open code, nothing behind it
that §18.2 lists as always free. Premium (API limits, CDN priority, synced history,
themes), creator subscriptions, contextual-only ads (max two slots), data licensing
of aggregates, a commercial API tier.

**Verification:** with monetization off, every feature is reachable; with it on, the
free list is still fully reachable and asserted one feature at a time — the risk is a
paywall leaking onto a core path.

## Phase 16 — UI, metrics, accessibility

**Goal:** NexusXXX §20–§23: the five key pages (home dashboard, search results,
content detail, tag page, performer page) on the existing React SPA and its 25 page
dirs, plus the §23 metrics as first-class counters, the dark-pattern transparency
report, keyboard navigation, screen-reader alt text, high contrast, reduced motion
and WCAG 2.1 AA.

**Verification:** the metrics read from the database, not from a document (the rule
`goal-check.py` states: *"Never trust a count written in a document. Recompute it."*);
keyboard-only navigation reaches every control on each key page; the a11y target is
recorded with the tool that measured it.

## Phase 17 — the predicate grows (threaded, not separate)

Every phase adds its clause to `docs/goal-check.py`, asserting an observable surface:
a table exists, a GraphQL field resolves, a suite name is present, a route answers.
A clause that cannot be evaluated reports `UNKNOWN`, never `PASS` — the file's own
rule. Phase N's clause is written in Phase N's commit, so the predicate is never
behind the code.

**Current outstanding, measured this session:** C1 (4 undecided PRs), C3 (6 plans
not Built), C6b (integration suite — now runnable, so this becomes PASS or FAIL),
C7 (5 files unformatted, one of them the last committed test file). C7 is a
one-command fix and it blocks a clause that otherwise says nothing about the new
programme.

## Deviations

Decisions this plan did not carry, newest-first. Each is recorded where it is made as
well as here (in the spec or in the phase's own Status block); this is the index, and a
reader who only reads this section still knows what changed.

1. **Phase 4's migrations are 111 and 112, not 110 and 111.** 110 was taken by phase 2
   (`content_source_normalized_url`). The files that exist are
   `111_tag_implications.up.sql` and `112_ontology_versions.up.sql`; the phase 4 text above
   still shows the numbers it was written with, deliberately, so the reversal is visible.
2. **`ontology_changes` is not a table.** The plan lists one with its own proposal →
   discussion → vote → implement status machine. The edit system IS that machine
   (`edits`, `edit_comments`, `edit_votes`, and the implication fields on
   `TagEditDetailsInput`), and a second state machine would be a second place a change's
   state lives. Phase 4 contributed the half the edit system does not have:
   `ontology_versions`.
3. **Phase 1's frontend half is deferred to phase 16.** The phase 1 file list ends with
   "and later `frontend/src/graphql/...`": `sources` is served by GraphQL and consumed by
   nothing in the UI yet, recorded in phase 1's Status.
4. **The extractor is not yt-dlp** (phase 2's Status): OpenGraph tags over `net/http`, no
   media toolchain, no download, no fingerprints. The `Extractor` interface is what keeps a
   yt-dlp-backed implementation a small change later.
5. **A URL that is gone closes as `failed`, not `done`** (phase 2's Status and the spec's
   §5): `gone` and a policy refusal are different outcomes, and the fetch happens only when
   a record might be created.
