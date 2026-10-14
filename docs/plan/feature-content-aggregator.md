# PLAN — the content aggregator programme (NexusXXX parity)

Status: **in progress** — phases 1, 2, 3, 4 and 7 done; 5, 6 and 8–17 open. Phase 3 (the
five non-video content kinds) closed on 2026-10-14 with slice 3g: they are stored, scoped,
scored, tagged, reviewed, rated, listed, and now creatable through the edit system, which is
the repository’s one route for shared metadata. This doc is deliberately NOT marked Built
while any phase is open — the predicate that reads this line is a goal predicate, and a plan
that claims to be built is a gate that has stopped measuring.

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

**Verified, and corrected after doing it:** adding an `EntityType` member touches the
completion vocabulary (four places, three of them SQL), the read surface, and every
CLOSED VOCABULARY in Go — `rg 'EntityType' internal/` before writing. The prediction
that "the compiler says so" was wrong: Go has no exhaustiveness, and the vocabularies
that matter (`review.validate`, `list.validEntityType`, `elo.AllEntityTypes`) refuse an
unknown type at RUNTIME, so a missed case is silent. What replaces the compiler is a
test that enumerates the type list and asserts each member has a mapping
(`completion_weight_parity_test.go:143` guards the weights), plus slice 3e's pair
assertions at each vocabulary's boundary.

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


### Slice 3b — the completion vocabulary — DONE (`e92ae20c`)


`completion.EntityContentItem` with its own weights (title 0, details 20, release
date 15, media 30, tags 35), a `ContentItem` reader over a new
`ContentItemCompletionInputs` query, and the three SQL places a type has to appear:
the `CountEntitiesWithCompletionBelow` CASE, the `ListIncompleteEntities` union, and
`CountArchiveEntities` (the denominator — a count without it is a page that says
"0 of 0").

The test that matters is the one that walks `completion.AllEntityTypes` rather than
naming types: every type must have weights AND a row in the count query, so the next
type added is covered without anyone extending the file. Two more assert that a
content item's score **moves** when it gains its heaviest fields (a score computed
from something other than the row would not) and that an incomplete item is
**findable** by the quest query (a forgotten union arm returns an empty page, not an
error).

**The mutation probes were wrong twice before they were right**, and the correction
is the reusable part: the first pair edited `internal/queries/sql/*.sql`, which
sqlc compiles into Go string literals, so the process ran the OLD statement and both
probes reported SURVIVED against code that had not changed. Re-cut against the
generated `.sql.go`, the count-arm probe kills. The listing-arm probe remains
**unscored** (its generated text differs from the `.sql`), which is reported as "not
measured" rather than counted as covered.

**One pre-existing test was corrected, not weakened:** `TestArchiveCountsCoverEveryScoredType`
asserted `Len(rows, 5)` and there are now six rows. It compares against
`len(completion.AllEntityTypes)`, which is what it meant — one row per scored type,
so a missing arm is a missing ROW rather than a zero.

**Still not client-visible.** The `content_item` member of the GraphQL `EntityType`
enum and the resolver switches it forces are slice 3c, deliberately: half-exposing
the kind would let a client ask for it and get a wrong answer rather than a refusal.

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

### Slice 3c — the read surface and the enum member — DONE (`529b81da`)


`contentItem(id:)` and `queryContentItems(kind:, limit:)`, the `ContentItem` type with
its `ContentItemKind` enum, `releaseDate` and `completion`, and `content_item` on the
GraphQL `EntityType` enum — so slice 3b's vocabulary is reachable from a query and not
only from Go. `internal/service/contentitem` is **read-only on purpose**: a content item
is created by ingestion or by an edit, this repository has one write path for shared
metadata (propose, vote, apply), and a create mutation here would be a second writer the
vote machinery cannot see. The write side belongs with the edit types and phase 2's
pipeline.

**The trap this slice is mostly about.** gqlgen binds a schema field to a struct field
by default. For the types this repository generates *from* the schema — unlike its
hand-written models — `completion` would have been generated into `ContentItem`, the
field resolver would never run, and every client would read `"completion": null` while
every Go test still passed, because a null is not an error. `gqlgen.yml` declares the
field external, and **only an executed query can see the difference**: there is a
round-trip test through the GraphQL handler for exactly this, and a probe that removes
the yml entry and regenerates kills it (the generated tree is hash-verified identical
after restore).

**Two unreachable branches, named rather than implied.** `ParseKind`'s refusal and
`kindEnum`'s refusal cannot be reached while the enum stays closed and migration 117's
CHECK holds. They stay — a loosened CHECK would otherwise surface as a silently invalid
enum member on the wire — and they are recorded as carrying no test claim.

**A page size above 100 is refused, not clamped**: a truncated list that looks complete
is the failure the ceiling exists to prevent. No limit means the service default.

**Mutation evidence, including the probe that first survived.** The yml probe and the
page-ceiling probe kill. The kind-filter probe **survived the first cut**: the test
asserted only that every returned member carried the asked-for kind, which is vacuous
when the unfiltered page happens to be homogeneous — the same family as an absence
assertion whose subject was never counted. The fix is a *discriminating* fixture created
inside the test (an item of another kind, newest, so an unfiltered page would contain
it); re-cut, the probe kills. The lesson generalises: a filter test needs a member the
filter must EXCLUDE, created where the test can see it.

### Slice 3d — chapters and media — DONE (`63a9f1de`)


`ContentItem.chapters` and `.media`, with `ContentItemChapter`, `ContentItemMedia` and a
`ContentItemMediaKind` enum. Media is ordered by **what the URL is**, not alphabetically:
`original` is the item itself and a client renders it as the item, while alphabetical order
puts `archive` first. Ties break on the url so the order is total — an order that depends on
the plan is an order that reorders itself between requests.

Both fields are declared external in `gqlgen.yml`, and the wire test creates a chapter and a
media row **for the item it queries**, so the `[]` that a dead resolver produces cannot pass.
That is slice 3c's trap, applied deliberately.

### Slice 3e — the polymorphic surfaces accept the kind — DONE (`63a9f1de`)


Phase 3's acceptance criterion — *a text item takes tags, a review, an elo matchup and a list
entry with no new plumbing* — is now a test rather than an assumption.

- `CONTENT_ITEM` in `review`'s validator and its GraphQL enum; `CONTENT_ITEM` in `list`'s
  `validEntityType` and its enum; `content_item` in `elo`'s `EntityType`/`AllEntityTypes` and
  its enum. Tags needed **nothing**: migration 117's link table and statements already existed.
- reviews and list_items store UPPERCASE `entity_type` while elo_ratings stores lowercase, so
  a content item has **two** stored spellings depending on which surface wrote it. That is
  pre-existing for every other kind; unifying it is a data migration across four tables and
  their indexes, which is not this slice. The constants are asserted, so a third spelling
  cannot be introduced quietly.

**The plan predicted the compiler would catch a missed switch. It does not.** Go has no
exhaustiveness, and these three vocabularies refuse an unknown type at *runtime* — so a missed
case reads as "the caller did something wrong" rather than as a broken build. The acceptance
test therefore asserts each claim as a **pair**: a bogus type produces the vocabulary's refusal
(proving the refusal exists and that the test would see it) and the content-item type does not.
An assertion that only checked "no error" would pass against a vocabulary that refused nothing.

**Known limitation, recorded rather than left to be discovered:** elo's matchup *pool*
(`QueryMatchupCandidates`) is hard-coded to `FROM performers` **for every entity type**, not
just this one, so `eloMatchup(entityType: content_item)` has no candidates to offer. The
vocabulary accepts the type and ratings for it can be recorded and read; sourcing a matchup
pool for non-performers is its own slice.

### Slice 3f — the write path's data layer — DONE (`9169b625`)


Migration 118 (`content_item_edits`, the edit → item join, mirroring `tag_edits`), the
`CONTENT_ITEM` arm of `GetEditTargetID`, `CreateContentItemEdit`,
`FindContentItemEditsForItem` (newest first, scoped to its item) and `UpdateContentItem`.

**`edits.target_type` was varchar(10)** — enough for SCENE, STUDIO, PERFORMER and TAG, not for
CONTENT_ITEM (12). Migration 05 chose the width for the four types it knew about, and this is
the first type to exceed it. **Found by writing the row**: every vocabulary test passed,
because a closed vocabulary in Go and a varchar width in Postgres are different things. Every
other `entity_type` column is wide enough (reviews TEXT, list_items 50, elo_ratings 32,
expected_totals 20, content_sources 20), which is why the narrow one looked like all the
others. Widening is non-destructive: every existing value is 9 characters or fewer.

**The acceptance test now writes.** Slice 3e's version stopped at "the vocabulary did not refuse
the type" and used an author id that could not exist, so it could not see a column refuse the
row. It now submits a real review and a real list entry and reads both back through the
statements.

**A suite interaction, found by the full run and not by the isolated test:** a PENDING edit of a
type whose processor does not exist yet is a row the vote sweeps try to apply, and they fail in
packages unrelated to content items. The fixture edit is created CLOSED, with the reason in the
test. The rule generalises: **a slice that lands an edit type's data layer before its processor
must not leave an applyable row behind.**

#### What the edit TYPE itself still needs — 7 sites, measured

1. `graphql/schema/types/content_item.graphql` — `ContentItemEditDetailsInput`,
   `ContentItemEditInput`, `type ContentItemEdit`.
2. `graphql/schema/types/edit.graphql` — the `EditDetails` union member, `TargetTypeEnum`
   `CONTENT_ITEM`, the `EditTarget` union member.
3. `graphql/schema/schema.graphql` — `contentItemEdit` and `contentItemEditUpdate` mutations.
4. `internal/models/extension_edit_details.go` — `ContentItemEdit`/`ContentItemEditData`,
   `FromDiff`/`FromCreate` (+ Checked), `CopyFromContentItemEdit`, and `GetContentItemData` in
   `model_edit.go`.
5. `internal/service/edit/contentitem.go` — the processor (create/modify/destroy; MERGE has no
   meaning for these kinds and must be refused by name).
6. `internal/service/edit/service.go` — `CreateContentItemEdit`/`UpdateContentItemEdit`, the two
   dispatch blocks, and the apply dispatch case.
7. `internal/api` — the two mutation resolvers and `editResolver.ToContentItemEdit`.


### Slice 3g — the edit type itself, which closes phase 3 — DONE

The 7 sites the last slice measured are now implemented: the schema (the input pair and the
edit type), the unions (`EditDetails`, `EditTarget`), `TargetTypeEnum.CONTENT_ITEM`, the two
mutations, the models (hand-written like every edit type: `EditID` is not a schema field), the
processor, the two service methods with their dispatch blocks, and the two mutation resolvers.

**Content items are created by an edit.** That is the whole point of this phase's write path:
this repository has exactly one route for shared metadata -- propose, vote, apply -- and before
this slice the aggregator's only writer was ingestion, so a curator could not add an item at all.

Two decisions worth the ink:

- **MERGE is refused by name** (`content items cannot be merged: their kind is a media type, not
  an identity`). A merge needs two rows that are the same thing, which is an identity question --
  two performers with one name, two studios that turned out to be one. Two text items are two
  text items however alike they look. Refusing generically would tell the moderator nothing.
- **Details are required for create and modify and NOT for destroy.** A destroy names its target
  and carries no fields. My first version checked `input.Details == nil` once at the top of
  `Edit()` and the destroy test failed by name: *content item edits need details*.
- **Clearing a date is spelled as the empty string**, because null means "leave it alone"
  everywhere in this edit system; the test walks it (a modify that names only `release_date: ""`
  clears the column and leaves the details alone).

**`models.ContentItem.IsEditTarget` already existed** -- gqlgen generates the union marker for a
schema type, so the hand-written one I added was a duplicate declaration rather than a missing
method. Found by the generator, not by reading.

**A nil applier used to panic the vote sweep.** The apply dispatch builds an `editApplyer` from
the target type and dereferences it; an unimplemented type fell through the switch and panicked
inside whatever else the sweep was doing. That is how the PENDING-fixture interaction in slice 3f
presented itself from the other side. The switch now has a `default` that returns a named error.

**The join gets a primary key (migration 119).** Migration 118 mirrored `tag_edits`, which has
none, which leaves an item's edit history able to list the same edit twice -- and a duplicate in
a history says an item was changed twice when it was changed once. `CreateContentItemEdit` is
`ON CONFLICT DO NOTHING`, and the update path re-links through it, so re-proposing an edit is
idempotent rather than a unique violation. The update path is what exercises it.

#### Evidence

Both tests are end-to-end through the resolvers: create (proposed, applied through the vote path,
target resolved, item read back), modify (old and new diffs asserted, update-the-proposal
included, unnamed fields asserted untouched, date cleared), destroy (gone from the read surface),
and five refusals by name (merge, no kind, blank title, unparseable date, no changes).

Three mutation probes, all killed: the union's `Details()` case removed, the apply switch's
applier removed, and `CopyFromContentItemEdit` clobbering fields the edit did not name.

**My probe harness had a bug that mattered:** its restore moved every file's backup after every
probe, so the second restore crashed and left the third probe's mutation in the tree while the
run reported on it. Caught it by checking whether the applier was still there. Restore ONE file
per probe.

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

**Migration 120 `content_tiers`** (the number in this plan said 112; 120 is what shipped):
`tags.content_tier smallint not null default 0` with
`CHECK (0 <= content_tier and content_tier <= 3)`,
`tags.requires_opt_in boolean not null default false`,
`user_trust.visible_tiers smallint[] not null default '{}'` (on `user_trust`, NOT
`users.opted_in_tiers` — consent is trust state, and it is stored as SENT: the array is the record of
consent and the gate uses its maximum as the ceiling), and a view
`entity_effective_tier (entity_type, entity_id, tier)` computed as the **max** of its
tags' tiers. **Denylist-shaped**: a tier can only ever remove visibility, never grant
it — the rule migration 87 states and the reason an allowlist was refused.

**Also:** the onboarding wizard (frontend), the warnings-vs-gates split, and the
cooling period for T3.

**PHASE 5 STATUS: the server work is done** (slices 5a-5i, migrations 120-123). The onboarding wizard's
SCREEN is explicitly out of scope here -- `frontend/` is the vendored application and this repository owns
the query it reads. Nothing remains in phase 5 that this repository owns.

**Verification:** a T2-tagged scene is absent from search, feeds, recommendations and
similar-to for a member who has not opted in — all four paths asserted separately,
because "invisible" is an absence claim and this repo already had an absence
assertion pass with the thing on screen (`docs/track/WORKLOG.md`: *"a test asserting
'no nav label appears twice' PASSED WITH THE DUPLICATE ON SCREEN"*). Each absence
assertion is preceded by a count assertion of the subject.

### Slice 5a — the tier model, and the gate wired to a page — DONE

**The find that reshaped this slice: the content gate existed and nothing used it.**
`trust.EvaluateContentAccess` has been a pure function with a decision-table test suite since
the content-access work, and `trust.ContentDenylist` has been able to load the denylist rows --
but the only caller of `NewAccessRuleSet` is the **`AccessRules` query, which REPORTS the rules**
(`enforced` vs `unenforced`). No read path consulted any of it. Phase 5's real work is therefore
wiring, not inventing, and the tier model is the second reason to do it.

**Migration 120** (`content_tiers`): `tags.content_tier smallint not null default 0` with
`CHECK (0 <= content_tier <= 3)`, `tags.requires_opt_in boolean not null default false`,
`user_trust.visible_tiers smallint[] not null default '{}'` with
`CHECK (<@ ARRAY[0,1,2,3])`, and the report view `entity_effective_tier (entity_type, entity_id,
tier)` = the MAX over an entity's tags (scenes and content items; LEFT JOINs so an untagged
entity reports tier 0 rather than being absent, which is a different claim).

**Denylist-shaped, and additive.** The effective tier is a MAXIMUM, so a tag can only raise a
thing out of a reader's view; nothing can lower an entity INTO a view. And because unclassified
content is tier 0 while an unasked viewer's ceiling is also 0, the comparison
(`EntityTier > VisibleTier`) changed the verdict for **zero existing requests** -- which is what
makes it safe to add a seventh condition to a gate whose comment says the order is load-bearing.

**Consent is a record and a ceiling.** `visible_tiers` stores the array AS SENT because that is
what an audit reads ("what did they agree to"); the gate derives its ceiling from the MAXIMUM,
because opting into tier 3 is opting into 1 and 2. `[3]` and `[1,2,3]` are the same ceiling and
different consents, so the query field returns the array and the gate takes the maximum.

**No role may consent.** The tier condition is NEVER bypassable -- vanguard and admin override
substitute for the level check and nothing else -- and the integration test asserts it from the
strongest angle available: **the admin who classified the tag cannot see the scene they
classified**, because they have not opted into that tier. Two unit tests state the same thing as
a decision table.

**The page path** (`buildSceneQuery`, which search, facets, the performer-scene list and the
discovery language all build on) excludes with a `NOT EXISTS` against the link table, which stops
at the first tag above the ceiling and uses the existing index, rather than joining the aggregate
view. Skipped entirely at the ceiling. **The predicate is also added to `hasOtherFilters`**: the
optimized trending path LIMITS the subquery before the filters run, so a page that must exclude
tiered scenes cannot take it -- and omitting it there returns a first entry the reader must not
see, which reads as a bug in the tier.

#### The test lesson that cost the most time

`queryResolver.QueryScenes` returns `&models.SceneQuery{Filter: input}` with `SearchResults` nil;
**the paginated page is produced by the `querySceneResolver.Scenes` FIELD resolver**, which is
only reached through the GraphQL handler. A test that calls the resolver and reads
`result.SearchResults` asserts against an empty struct -- and an empty page is exactly what a
tier gate produces, so **the broken test looked like the feature working**. The probe goes
through the GraphQL client (`runner.client.queryScenes`), which is also what makes the assertion
about the schema rather than about Go.

#### Evidence

Four gate unit tests (tier 0 visible with no consent; above the ceiling refused; the ceiling
inclusive; vanguard and override cannot consent to one more tier). Two integration tests: the
scene is COUNTED through the exact query before the tag is classified, then absent for an
unconsented viewer, present for a viewer on `{1,2}`, absent for the admin who classified it; and
a tier-3 scene absent at a ceiling of 2, present after opting into 3.

Five mutation probes. Four killed behaviourally: the comparison `>=` instead of `>`, the gate's
`<` instead of `>`, collapsing the consent array to its maximum, and inactivating the page
predicate (which reddened the absence assertion with its own message). **The first version of
that last probe did not compile** -- it deleted the predicate and left `visibleTier` unused, so
the verdict was "BROKEN" twice, and a mutation that does not compile has proved nothing. It was
re-cut to keep the bind argument and make the comparison unsatisfiable.

#### Still open in phase 5

`similar-to`, the activity feed and `forYou` do **not** go through `buildSceneQuery`, so each
needs the same predicate and its own absence assertion -- the plan's four-path verification is
one path (search, plus facets and the performer list by construction) done and three to go. Also
open: the warnings-vs-gates split, the T3 cooling period, the onboarding wizard, and a test for
the trending-path guard, which is written and has no coverage.

### Slice 5b — the tier predicate reaches similarity and the feed — DONE

**One predicate, not four copies.** `query.ApplySceneTierCeiling` now holds the comparison and the
`NOT EXISTS`; `buildSceneQuery` was the first caller and the similarity candidate pool is the
second. Four hand-written copies of one rule in four files is four chances for one of them to be
`>=` instead of `>` or to name the wrong column, and the failure is a reader seeing what they
opted out of.

**Similarity** (`FindSimilarSceneCandidates`) gets the exclusion with a `visible_tier` argument,
because a similarity list is a RECOMMENDATION: the viewer did not ask for the scene and did not
know it existed. Offering what they opted out of there is worse than in a search result they
typed. The candidate query filters *and* ranks, so the predicate sits in the candidate WHERE --
before the rank, which is where a filter belongs.

**The activity feed** is filtered in Go, and that is a departure worth naming: the feed is
assembled from three arms (fingerprints, list additions, reviews) that share no statement, each
asked for at most `limit` rows. Pushing the predicate into each arm means adding the same join to
three statements whose visibility rules differ; the cost here is a page that can come back SHORT
when its items are above the ceiling -- the same trade the function already makes when it drops
the viewer's own items. The DECISION is a pure function (`filterAboveCeiling`) with its own table,
so the comparison is testable without a database, and the wrapper above it does only the lookup
(the `entity_effective_tier` view, which is exactly what a view over every entity is for: a known,
small, batch of ids).

**Correction to slice 5a's note: `forYou` DOES inherit the predicate.** `taste` calls
`s.scenes.Query`, which is the tier-filtered statement, so the discovery feed is covered by
construction -- slice 5a's "forYou does not go through buildSceneQuery" was wrong, and this is the
record of it. Covered by construction is still a claim without a test: `taste` has no database
tests, and an end-to-end For You assertion needs an affinity fixture (a fingerprint vote or a list
addition by the viewer), so it is listed as open rather than asserted.

**A DECISION, so it is not later discovered as a bug: `findScene(id)` is not tier-gated.** A tier
removes a thing from a VIEW; a direct link is not a view. The plan's verification names search,
feeds, recommendations and similar-to, and those are the paths that enumerate. Gating the direct
lookup would also mean the SUBJECT of a similarity query has to be visible before it can be asked
about, which is not what a tier means.

#### The suite interaction this slice hit

`asRead(t)` hands out **one shared user for the whole package**. Slice 5a's test set that user's
ceiling to `{1,2}`, so the similarity test -- which asks for a scene at tier 2 and expects it
absent -- **passed alone and failed in the package run**, with the shared user still consented from
the earlier test. Every test in the file now STATES the consent it means to test
(`UserSetVisibleTiers(ctx, []int{})` first), which is the "capture the value you assert about"
rule applied to a fixture: the default is not a default when another test can change it.

#### Evidence

The similarity test counts the subject first (with the tag unclassified the candidate IS offered),
then asserts the absence, then opts in and asserts it returns. The feed's decision has two unit
tests: what is at or below the ceiling is kept (including a scene with NO tier row, whose zero
value has to mean tier 0 rather than "keep" or "drop" by accident) and an item with no scene is
not this function's business. Two mutation probes killed: the candidate predicate inactivated in
the **generated** statement (a `.sql` edit alone proves nothing -- sqlc bakes the statement into a
Go string literal, so the generated file is what runs) and the feed's comparison inverted.

#### Still open in phase 5

An end-to-end feed assertion (the wrapper is wired; the decision is unit-tested; the two are not
proven to meet), an end-to-end For You assertion, the warnings-vs-gates split, the T3 cooling
period, the onboarding wizard, and coverage for the trending-path guard from slice 5a.

### Slice 5c — the four paths verified end to end — DONE

Slice 5b left two of the plan's four paths asserted only at the level of the piece that had changed:
the feed's decision had a unit table and its wrapper was wired, and For You was covered by
construction. Neither is a verification, and the plan names all four paths. Both are now asserted
through the GraphQL API.

**The activity feed.** A follow row alone shows NOTHING -- that is the schema's own contract
("nothing is visible until the author publishes it", default PRIVATE) -- so the fixture makes a
relative grant, has the followed user submit a fingerprint, and only then asks. The test counts the
row first for a reason the fixture cannot avoid: an empty feed has three uninteresting causes (no
follows, a private grant, an unpublished scene) and every one of them would make the absence pass
for something that is not the tier. Probed by unwiring the filter (`return out, nil`): the absence
reddened with its own message, so the decision and the lookup are proven to MEET, not merely to
exist.

Writing that fixture cost a wrong guess worth recording: the column is `activity_visibility`, not
`visibility`. The schema comment paraphrases the field and the migration has its name -- read the
MIGRATION before writing fixture SQL.

**For You.** `serendipity: 0` on purpose: the page is then a pure affinity ranking, so a scene that
is absent cannot be absent because it lost a coin toss (the seed makes the shuffled page testable,
but not a page for an absence assertion). The signal is the viewer's own list addition (+0.8,
derived at read time, so nothing has to be recomputed for it to count), and `listAddItem` requires
MODIFY -- which is also why this test's viewer is the modify user rather than the read user.

With this, the four paths the plan names -- search (with its facets and performer-scene riders),
similarity, the feed, and For You -- each have an absence assertion preceded by a count of the
subject in that exact query.

**A readability fix in the same pass:** the id lists these assertions compare are `[]string`, not
`[]uuid.UUID`. An `assert.NotContains` failure prints the container it searched, and
`[]uuid.UUID{[16]uint8{0x1, 0xa1, ...}}` hides the one thing a failure message exists for.

#### Evidence

Unit clean; 44 integration packages, 0 FAIL (serial); fmt and vet clean. One probe, killed, plus the
two from slice 5b. The For You test needed no probe of its own: its ABSENCE is non-vacuous by
construction, because the count-first assertion requires the same query to have returned the scene
one statement earlier.

#### Still open in phase 5

The warnings-vs-gates split, the T3 cooling period, the onboarding wizard, and coverage for the
trending-path guard from slice 5a.

### Slice 5d — the trending-path guard, verified — DONE

Slice 5a added `tierFiltered` to `hasOtherFilters` so a page that must exclude tiered scenes does not
take the optimized trending path (which LIMITs its subquery before the filters run). That guard was
written and unexercised. It is now tested, and the probe produced a more precise finding than the note
predicted:

**The guard's absence does not LEAK the tiered scene — it STARVES the page.** Removing `tierFiltered`
from `hasOtherFilters` makes the optimized subquery take the one slot with the tiered scene, which the
tier filter then removes: the reader gets an EMPTY page rather than a page with one scene on it. The
probe's message is `Should NOT be empty, but was []`. Both halves matter — the assertion is not only
"the tiered scene is absent" but "and the page falls through to the next scene".

Building the fixture cost two wrong assumptions, both recorded here because the second one would have
made the test pass for the wrong reason:

* `scene_popularity_trending` is `COUNT(DISTINCT user_id) ... WHERE created_at >= now()::DATE - 7`
  (migration 65) — **a count of distinct users in a seven-day window, NOT the decayed freshness score**
  (that is `scene_freshness`, migration 114). A fixture that ranks by signal AGE gets a scene with no
  row in the view at all, and an empty page.
* Two signals from ONE user count once, so a scene needs more distinct users than its rival. A tie is
  broken by `scene_id DESC` — that is, by a random uuid — so a fixture that relies on a tie passes or
  fails per run.

### Slice 5e — the T3 cooling period — DONE

Migration 121 adds `user_trust.t3_consented_at`. T0–T2 take effect immediately; T3 does not, for
`T3CoolingHours` (24, a named Go constant).

**The rule lives in ONE query.** `UserVisibleTier` is the single place the ceiling is computed, and all
four read paths plus the gate call it — so one of them calling a different query that forgot to cool
would be the whole rule gone. The period's LENGTH is passed in as a parameter, so the policy number is
not buried in SQL, and the comparison is `NOW() < t3_consented_at + make_interval(hours => ...)`.

**Stamped on GAIN, cleared on LOSS, untouched otherwise.** That ELSE is the point: a member who re-sends
the same numbers in a different order has not re-consented, and a stamp-on-every-write version lets
anyone replaying the request restart the clock at will.

**NULL means NOT COOLING.** A consent that predates the column is not retroactively aged, so no existing
member loses access for a day — the additive, denylist-shaped move.

**The stamp is data, so the test does not wait:** it backdates `t3_consented_at` by 25 hours. Four
assertions, in this order: {1,2,3} does not reveal a tier-3 scene while tier 2 IS immediate; backdating
reveals it; **re-sending {3,2,1} does not put it away again** (the property a naive implementation
fails); and an explicit {1,2} cannot see it (the control, so the first is not vacuous).

**Probes, both killed in the GENERATED statement** (sqlc bakes the SQL into a Go string literal, so a
`.sql` edit alone proves nothing): the cooling branch answering 9 instead of 2 reddened "consenting to
tier 3 must not reveal it in the same minute", and the stamp moved from gain to every write reddened
"re-ordering the same consent is not a change of consent".

### The three remaining phase-5 items, designed

The plan's phase heading names all three in one line ("the onboarding wizard (frontend), the
warnings-vs-gates split, and the cooling period for T3") and nothing else in the repo defines them.
These are the decisions, so the implementation that follows is not inventing a policy. (The cooling
period has since been BUILT — slice 5e above — and its design text is kept here for the record.)

#### A0. THE FINDING THAT RESHAPES A: the per-entity denylist is enforced by NOTHING

Checked rather than assumed, and the assumption in A below ("a read path that consults a warn entry")
is **false**:

* `trust.ContentDenylist` has exactly one caller, `resolver_trust.go`, which feeds `NewAccessRuleSet`
  — the OPERATOR REPORT — and nothing else.
* `PerEntityRule.Denies(id)` is a decision method that no read path calls.
* The four enumerating paths (the scene query, similarity, the activity feed, For You) contain zero
  references to the denylist.

So `AccessRuleSet` reports `per_entity_denylist` as `Enforced: true, EnforcedBy: internal/service/trust`
while no read path consults it: an operator-facing report is wrong about a security control. This is the
same shape slice 5a found in the tier gate ("the gate existed and nothing called it"), one layer over.
A is therefore two pieces, in this order:

1. **Wire the denylist into the four read paths**, with the discipline the tiers got: one shared
   predicate, `gate` entries excluding.
2. **Then** the `mode` column and the `warnings` list, which are only meaningful once something reads
   the table.

The report is not the thing to patch: `Enforced: true` becomes true by wiring, and if the wiring is not
done the claim has to be corrected instead. Either way the operator-facing sentence must match the code.

#### A. Warnings vs gates

BUILT — see slice 5h (migration 122). Design as written, with one addition the implementation needed:
the visibility guard on the warnings list lives in the SERVICE, so `resolveWarnings` is only the wiring.

**Before slice 5f there was one instrument and it was all-or-nothing:** `EvaluateContentAccess` refuses (seven
conditions) and the per-entity denylist blocks — once something consults it. Nothing can tell a reader
"this is restricted" while letting them proceed, so an instance's only options are silence and refusal.

**Design.** `content_denylist_tag` / `_studio` / `_performer` (migration 87) gain
`mode VARCHAR(4) NOT NULL DEFAULT 'gate' CHECK (mode IN ('gate', 'warn'))`. Additive, and the default
preserves today's behaviour exactly, so no existing row changes meaning. A read path that consults a
`warn` entry reports it instead of refusing. New type `ContentWarning { reason: String!, source: String! }`
and `warnings: [ContentWarning!]!` on `Scene` — an empty LIST, never null, because "no warnings" and
"this surface does not report warnings" must not be the same response.

**Warnings never grant and never remove visibility.** That is the tier/denylist rule restated, and the
reason this is a mode on the existing table rather than a second kind of tier: a warning is an
annotation on a view, a gate is a decision about one.

**Verification.** A `gate` entry removes the scene from search, similarity, the feed and For You (the
four paths, as the tiers do — a precondition of everything below, since today none of them consults the
table at all); a `warn` entry leaves it present AND reports it; a row inserted without `mode` blocks
(the default is the safe direction); and a warning is never returned for a scene the viewer cannot see
at all (a warning about invisible content leaks it).

#### B. The cooling period for T3

BUILT — see slice 5e. Design as written: `user_trust.t3_consented_at`, set when the array GAINS 3 and
cleared when it loses 3; the gate uses a ceiling of 2 while `now() < t3_consented_at + T3CoolingHours`.
T0–T2 stay immediate, the period is a named constant rather than a setting (a policy knob with no
operator surface is a guess), and the value is DATA so a fixture backdates it instead of sleeping.

#### C. The onboarding wizard

**SERVER HALF BUILT — slice 5i.** As designed, with three differences the build chose deliberately:
`optedIn` rather than `optInSet` (it names what it is: the member's own switch toggling content viewing
at all), `contributionScore { current, required, met }` rather than `{ current, threshold }` (the
comparison is made server-side so a client cannot re-implement the gate's arithmetic), and the tier
ladder gained `availableAt` reporting the cooling period. Migration 123 also gave the gate's terms
condition its first column: until then `ReasonTerms` was a refusal NO MEMBER COULD SATISFY, because
nothing in the instance recorded acceptance. `userAcceptContentTerms` (READ) records it and keeps the
FIRST acceptance, so re-reading the terms cannot rewrite when a member first agreed.


**Design, and a scope decision.** The wizard's SERVER half is what this repository owns. A query
`contentAccessSetup: ContentAccessSetup!` (READ) reports, for each condition of the gate, whether THIS
viewer satisfies it and what remains: `termsAccepted`, `optInSet`,
`contributionScore { current, threshold }`, and `tiers { tier, optedIn, availableAt }` — computed from
the SAME predicates `EvaluateContentAccess` uses, so the wizard cannot drift from the gate that will
actually refuse the member.

**The frontend half is out of scope here.** `frontend/` is the vendored application; the wizard is a
screen over this query, and building a React screen in this repo would be a different piece of work than
the programme this plan describes.

**Verification.** Each reported field is asserted against the gate's own verdict for the same caller —
the drift guard: if `termsAccepted` is false, `EvaluateContentAccess` must refuse that member with
`ReasonTerms`. A wizard that is right about everything except what the gate does is the failure mode.

### Slice 5f — the denylist wired into the four paths — DONE

Slice 5e found that the per-entity denylist was enforced by nothing. This is the wiring, and it is the
first half of A: `mode` and the warnings list follow, because they are only meaningful once something
reads the table.

**One predicate, three arms, columns named by the caller.** `query.ApplySceneDenylist(query, sceneID,
studioID)` excludes a scene whose tag, studio or performer is denylisted, and the caller supplies the
id expressions (`"scenes.id"`/`"scenes.studio_id"` in the scene query, `"c.id"`/`"c.studio_id"` in the
similarity CTE). The alternative — the predicate written twice — is exactly the shape that fails
silently, because a copy that checks two of the three tables looks perfectly healthy from outside.

**The studio arm compares the denylisted studio to the scene's OWN studio column**, not to the scene id
and not to each performer's studio. That is the arm most likely to be got wrong, and the test asserts
each arm separately for that reason.

**Where it went:**

* **the scene query** (search, facets, performer-scene lists, the discovery language): the predicate,
  plus a new `ContentDenylistActive` query feeding `hasOtherFilters`. A non-empty denylist causes the
  same STARVATION the tiers do — the optimized trending path LIMITs its subquery before the filters
  run, so the page would come back short rather than wrong. The guard's input is a runtime fact, not a
  property of the request, which is why it is asked there instead of listed with the user's filters.
* **the similarity candidate pool**: the same three arms spelled out in SQL, with `den_*` aliases
  because the CTEs already own `st` and `t`. A recommended denylisted scene is worse than a searched
  one: nobody asked for it.
* **the activity feed**: `DeniedSceneIDs` (a batch over the page's ids) plus the pure `filterDenied`,
  because this feed is assembled in Go from three arms and has no statement to hang a predicate on.
  The cost is the one the tier filter already pays — a short page.
* **For You**: inherited through `scenes.Query`, and asserted anyway.

**A fixture trap worth keeping:** the recommendation test denylists a tag in its similarity half and
then measures For You in the same function. The For You candidate had to be a DIFFERENT scene with a
DIFFERENT denied tag, because the count-first assertion in the second half was otherwise unpassable for
a reason that had nothing to do with the second half. Two halves, two subjects.

**The denylist is instance-wide state, so the fixtures clean up after themselves.** A row left behind
makes `ContentDenylistActive` true for every later test in the package, which changes which path the
trending query takes — the same class of cross-test surprise as a consent left on the shared user. Each
deny helper registers a `t.Cleanup` that deletes exactly the row it added.

**Probes, three, all killed:** the tag arm of the shared predicate removed (which reddened BOTH the
search assertion and the recommendation assertion, since they share the predicate — the sharing made
visible), the feed's `dropDenied` call unwired, and the similarity candidate's tag arm removed in the
generated statement.

### Slice 5g — the denylist on DIRECT access — DONE

**As built**, and the design below held with four additions the build forced:

* **One helper, four call sites, fail closed.** `scene.deniedSceneIDs` returns the denied subset as a set
  (one `DeniedSceneIDs` batch, the same query the feed uses). Every arm checks it, and an error in the
  check FAILS the read rather than proceeding -- a gate that serves content when its own check errored is
  not a gate. In `LoadIds` that error is repeated per requested id, because the loader's contract is one
  error per key.
* **The four arms are `FindByID`, `LoadIds` (which is what the `SceneByID` dataloader's fetch calls, so
  the loader is gated rather than the query), `FindScenesBySceneFingerprints` and `Changelog`.** Gating
  `LoadIds` is what makes `findScenes(ids: [...])` not a bypass; the fingerprint arm drops denied matches
  from their group; the changelog drops denied entries.
* **THE CHANGELOG PAGE GETS SHORTER, deliberately.** Dropping entries does not backfill to keep the page
  size steady: a query loop whose only purpose is to hide a hole is worse than a short page, and a client
  paginates by the cursor it was given, so a short page is not a stall.
* **The edit path and the operator report stay open, and the test says so.** Editing a denied scene works
  (keeping its denied tag), which is also how an operator un-denylists something: the denylist names
  ENTITIES, so the same mutation that keeps a scene denied lifts it when the denied tag is removed -- the
  test asserts both directions. And `AccessRules` still lists the rule, with `EnforcedBy` corrected to
  name BOTH halves (`internal/service/query` for the enumerating paths, `internal/service/scene` for the
  direct ones), because naming only the first was true of enumeration and quiet about the rest.

**Probes: five, all killed, each with its own message** -- one per arm, including the shared predicate
from 5f. Two harness lessons came out of them: a needle written with two tabs matches inside a four-tab
line, so a mutation can hit the WRONG arm while reporting KILLED (the loader's arm had to be re-probed
with an anchor that included its body), and the arm you did not probe is the arm that is not covered.

### The design as written before the build

Slice 5f gates the four ENUMERATING paths. Direct access still serves a denylisted scene, and that is a
gap with a different character from the tier gap: a tier removes a thing from a VIEW (and `findScene(id)`
was left ungated deliberately), while migration 87's own words for this denylist are "entities whose
CONTENT ACCESS is denied" — access, not visibility.

**The paths to gate, measured rather than guessed:** `FindByID` (resolver_query_scene.go), the
`SceneByID` dataloader that `FindScenes(ids)` and every entity embedding a scene go through,
`FindScenesBySceneFingerprints`, and the changelog. One predicate, and the honest way to apply it is at
the loader — gating only `FindByID` leaves `findScenes(ids: [...])` as a bypass.

**The decision to make first: a denylisted scene reads as NOT FOUND.** Not an error, and not a
distinguishable "denied": to a reader, "this instance refuses to serve it" and "there is no such scene"
must be the same answer, or the response itself tells them the content exists. That is also why the
tier decision does not carry over — a tier is a preference of the viewer, the denylist is a decision of
the instance.

**Not gated: the operator surface and the edit paths.** `AccessRules` must still see the entries (an
operator audits their own denylist), and edits/moderation of a denylisted scene must still work —
otherwise the only way to un-denylist something is to bypass the API.

**Until this is built, `AccessRuleSet`'s `per_entity_denylist: Enforced: true, EnforcedBy:
internal/service/trust` is true of enumeration and not of direct access.** The field's contract says a
reason is empty exactly when `Enforced` is true, so the honest options are to build 5g or to narrow the
claim — not to leave a half-true sentence in an operator-facing report.

### Slice 5h — warnings vs gates — DONE

Migration 122 adds `mode VARCHAR(4) NOT NULL DEFAULT 'gate' CHECK (mode IN ('gate','warn'))` to the three
denylist tables. **Additive, and the default is the safe direction:** `gate` is what every existing row
means, so nothing changes effect, and a row inserted without naming a mode blocks.

**Two instruments, and they must not be each other.** A gate decides ACCESS; a warning is an annotation
on a VIEW. Only `gate` rows exclude — in the shared scene predicate, in the similarity candidate SQL, in
the `DeniedSceneIDs` batch and in the `ContentDenylistActive` guard — while warn rows are reported by
`Scene.warnings`. The failure this prevents is symmetrical and both halves are asserted: a warn row that
removes a scene, and a gate row that never becomes a reader-facing note.

**`warnings: [ContentWarning!]!` — an empty LIST, never null, and never the reason a scene is withheld.**
The non-null list is the contract: "no warnings to report" is a complete statement, so a client never has
to tell it apart from a server that does not report warnings at all. `ContentWarning` carries `source`
(`tag`/`studio`/`performer`) and `reason`, with an empty string rather than null when the operator wrote
none — the reader-facing half of the same distinction `content_denylist_*.reason` is nullable for.

**A WARNING IS WITHHELD FOR A SCENE THE READER CANNOT SEE, and that check is in the service, not the
resolver.** Two ways a scene is out of view — the instance denies it, or its tier is above the viewer's
ceiling — and both return the empty list: a warning is an annotation on a view, and "this content is
restricted" is itself a disclosure about content the reader cannot open. The resolver is the wiring;
putting the rule where every caller passes is the lesson the four tier paths taught.

**Probes, three, all killed:** the gate predicate made mode-blind (reddening "a WARNING must not remove
the scene" and the ceiling test), the ceiling check made unsatisfiable, and the batch denial lookup's
mode filter flipped in the generated statement. The second probe is the third occurrence of a harness
lesson worth stating once: **delete a check and its variables become unused, so the mutation does not
compile** — cut it by making it unsatisfiable (`row.Tier > visibleTier+99`) rather than by removing it.

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

### Slice 9a — the transition rule, and one source's check

**The rule is already documented in the database and has no implementation.** Migration 108's own
comment writes the machine: `live → intermittent` at 3+ consecutive failures, `intermittent → dead`
after 7+ days unreachable, and `any → dmca`, "terminal as far as serving is concerned". `RecordSourceCheck`
(migration 108) is the write and says in its comment that it takes `check_failures` as an argument
"rather than incremented, so the caller owns the transition rule ... and this statement stays a write and
not a policy". This slice is that policy.

**`internal/service/content/health.go`:**

* `FailuresToIntermittent = 3` and `UnreachableToDead = 7 * 24 * time.Hour`, named once, with the
  migration's comment quoted, because a second copy of a threshold is a second rule.
* `func NextStatus(current string, failures int, lastOK *time.Time, firstSeen time.Time, now time.Time, ok bool) string`
  — pure, no I/O, so every boundary is a unit test:
  * `dmca` and `archived` are TERMINAL: a successful check does not resurrect them. "Terminal as far as
    serving is concerned" is the migration's wording, and a sweep that flipped a takedown back to `live`
    on the first 200 would be the worst bug in the phase.
  * success → `live`, whatever the current status (including `dead`: that IS the resurrection, and the
    caller resets the counter with it).
  * failure → `failures+1`; `dead` when unreachable for 7+ days (measured from `last_ok`, falling back to
    `first_seen` for a source that has never been ok), else `intermittent` at 3+, else unchanged.
* `type Checker interface { Check(ctx context.Context, url string) error }` injected into the service, so
  the tests are hermetic. The production `HTTPChecker` is a HEAD request with the ingest drain's own
  20-second timeout (a second timeout constant would be a second policy).
* `func (s *Content) CheckSource(ctx context.Context, id uuid.UUID, now time.Time) (models.ContentSource, error)`
  — reads the row, checks, then writes through the EXISTING `RecordSourceCheck` with
  `last_ok = &now` only on success (the statement's `COALESCE` keeps the previous value otherwise, which
  is the whole reason it is written that way).

**The GraphQL status is an enum** (`ContentSourceStatus`, mirroring the CHECK's six values) -- DONE, in
this slice. The model's comment said the string was deliberate "because the transition rules ... are
Phase 9's health daemon, and an enum published before the rules exist would freeze a vocabulary nobody
has agreed to", and this slice is when the rules exist. It is force-resolved in `gqlgen.yml` rather than
bound, because the model carries the COLUMN's string: bound, gqlgen hands the column's value to the
enum's marshaller, which answers `""` for a name it does not know -- an empty status for every source,
in whichever direction the two vocabularies are out of step. The resolver maps explicitly and ERRORS on
a status with no name, rather than reporting `UNKNOWN`, which would be a lie ("nothing has checked this
URL" is not "this instance does not understand the answer"). `scene_sources_integration_test.go`'s three
wire assertions moved to the enum's spelling in the same commit: the change is a client-visible break,
and the test that notices is the point.

**Verification (both halves DONE):**
* `go test ./internal/service/content/` — the boundary table: 2 failures → live; 3 → intermittent; a
  success from intermittent → live with 0 failures; `last_ok` 6d23h ago + a failure → not dead; exactly
  7d → dead; never-ok + first_seen 8d ago → dead; `dmca` + success → still `dmca`; `archived` +
  failure → still `archived`.
* An integration test drives `CheckSource` with a fake checker and asserts the ROW: status, `last_ok`
  unchanged by a failure (the resurrection evidence), `check_failures` reset by a success, and the
  terminal statuses untouched.
* An httptest server for `HTTPChecker`: 200 → nil; 404 → an error; a hung handler → the timeout, not a
  hang.
* Probes: the terminal guard removed (a dmca source resurrected), the dead threshold off by an hour, the
  failure counter reset on failure instead of on success.

### Slice 9b — the sweep, and its cadence — DONE

**The cadence is a pure function** (`content.CheckInterval(failures, claimants)`): reliability LENGTHENS
the interval (doubling per consecutive failure, capped at four doublings = sixteen days) and popularity
SHORTENS it (halving per extra claimant, floored at three hours). Both halves are policy, so the whole
rule is a table test, and the cap is asserted as a claim about the worst case: a source nobody looks at
cannot come back, which is the failure mode this phase exists to prevent.

**The SELECTION and the CADENCE are deliberately in different places.** The query
(`FindContentSourcesDueForCheck`) answers "which rows are worth considering", most overdue first, with
each URL's claimant count; the caller skips a candidate whose interval has not elapsed. Due-ness cannot
be expressed in the WHERE clause without moving the policy into SQL, and putting it in Go is what makes
it testable -- but the two must agree, so the ORDERING is what makes skipping safe: oldest-checked
first means a candidate that is not due is only reached after every candidate that is, and skipping
cannot starve anything.

DEAD is excluded by the query on purpose (it is slice 9c's queue), and `content_sources_sweep_idx` is a
partial index over exactly the three statuses the query names -- the query and the index are the same
list. The job runs from `internal/cron` with its OWN semaphore (a slow site must not delay the edit
closer), bounded by `content.SweepBatch`, on `content_sweep_interval` (default 1h), registered
unconditionally like the ingest drain so that stopping edit processing does not stop the sweep.

**Verification (DONE):** the `CheckInterval` table (base, each doubling, the cap, the halvings, the
floor, and a claimants=0 row that is not special-cased); three integration tests -- the SET of urls
checked (three due sources, the recent one and the dead one untouched), the batch bound, and a single
recent source fetched and skipped -- each with a recording checker, because "three sources were checked"
is not the assertion worth making and "these three and not those two" is. Four probes, all killed with
their own messages: the due-check removed, DEAD swept, the bound ignored, the ordering reversed.

### Slice 9c — the resurrection queue — DONE

A `dead` source is excluded from the sweep's index on purpose (dead rows are the majority over time, and
a sweep that considered them would spend its bound on rows that will not answer). Coming back is its own
queue on its own cadence: `ResurrectionInterval(claimants)` = thirty ordinary intervals (a month for a
quiet source, sooner for a popular one).

**RELIABILITY IS DELIBERATELY NOT APPLIED HERE**, and that is the subtlety the slice exists around: a
dead source's failure count is saturated by definition -- death requires seven days of failures -- so
feeding it back through the sweep's backoff would multiply sixteen days by thirty and schedule the next
attempt a year out, and a source nobody asks is a source that never comes back.

**The evidence is read BEFORE the check, because the check destroys it.** A successful resurrection sets
`last_ok` to now -- correctly, it is alive now -- so "when was it last alive before this" has to be
captured first; `SweepResurrections` returns it alongside the URL (`Resurrection{URL, LastAliveAt}`),
and the cron tick logs the gap by name and by duration, because a resurrection is news and "which one,
gone since when" is the whole content of it. A dead source that is asked and still does not answer stays
dead, keeps its counter and keeps the date it was last alive.

**Verification (DONE):** the interval table including the bound claim; three integration tests (a
40-day-dead source resurrected with the pre-check evidence asserted, a two-day-dead source fetched and
left alone, a still-failing source staying dead with its `last_ok` intact). Three probes, all killed:
the saturated failure count fed back in, the queue considering everything but dead, and the gap read
after the check had overwritten it.

### Slice 9d — mirror resolution through the fingerprint cluster — DONE

**CORRECTION to this slice's original wording:** there is no "cluster table" and migration 63 is not
this. The machinery is `internal/service/fingerprint.Fingerprint.ClusterScenes` (`ClusterScenes(ctx,
seedScene, distance)`), which expands the seed scene's PHASH fingerprints through the bktree operator
into clusters of scenes (`fingerprints` + `scene_fingerprints`, migrations 01 and 34).

**The split is deliberate:** the fingerprint service answers "which scenes hold the same content" and
does not know what a source is; `content.Mirror` answers "is there a live URL to hand over, and is
handing over appropriate at all" and does not know what a cluster is. The resolver composes them, and
`MirrorDistance` is a CONTENT-side constant (4, tight on purpose: being generous costs a moderator a
glance on the identification board, where the distance is a client argument, and would cost a reader
the wrong scene here).

**Three rules, each with its own test:**
* A live source of the scene's own means nothing is handed over (`nil`).
* **A canonical source under DMCA or in the archive means NO mirror**, and this is the branch worth
  arguing for: a takedown is a decision the instance has made, not a hosting problem, and routing around
  it through a fingerprint cluster would be circumventing it by accident. It is the one branch where a
  mirror that exists is deliberately not offered.
* Otherwise the first LIVE source among the candidates wins. Live strictly: an `unknown` source has
  never been checked, and a mirror that is itself an unverified link is worth nothing.

**Verification (DONE):** four wire-level tests (the clustered live source is offered and the playable
scene gets nothing; a dmca source is not routed around; an unrelated live scene is NOT offered; an
unchecked source is not offered), each written against the wire rather than the resolver method, because
`mirror` is force-resolved and calling `Query().FindScene` would return the field unpopulated and every
assertion would pass for the wrong reason. Three probes killed: an unchecked source counting as a
mirror, the takedown branch removed, and the same-content requirement dropped (the last one surfaces in
the neighbouring test, because with the entity filter gone a leftover live source from another test
becomes an eligible "mirror" -- the same invariant, observed one assertion over).

### Slice 9e — Wayback submission — DONE

`archived_url`/`archived_at` exist as a pair with a CHECK (`(archived_url IS NULL) = (archived_at IS
NULL)`), and `SetContentSourceArchive` is the only statement that writes them, for that reason: a
half-written archive is refused by the database rather than by whoever reviews the next change to the
file. The probe that writes the pair half at a time is KILLED BY THE CONSTRAINT, which is the CHECK
earning its place rather than being documentation.

**Dead and intermittent only.** An archive is worth making when a URL is at risk of vanishing;
`dmca` and `archived` are absent deliberately, because sending a URL the instance has decided not to
serve to a third party for permanent storage would be passing a takedown around -- the same reasoning
that stops a mirror in 9d. An already-archived source is never resubmitted: the Internet Archive is
somebody else's service.

**A refusal is a RESULT, not an error.** One host refusing to archive says nothing about the next, so
`ArchiveSources` records the failure on that attempt (`ArchiveAttempt{URL, ArchivedURL, Err}`) and
carries on; only a database failure stops the run. And `Wayback.Archive` returns an error when a
submission is accepted but names no copy: a 200 without `Content-Location` is a success that produced
nothing this instance can record, and treating it as one would write a row whose time is real and whose
address is empty.

**Verification (DONE):** `Wayback` against an httptest server whose transport is rewritten so the
production URL stays in the code under test (the path form is made absolute, an absolute form is taken
as it comes, a missing header is an error, a 5xx is an error); four integration tests (a dead source
preserved with BOTH columns asserted, an archived source not resubmitted, a takedown left alone, a
refusal reported while the batch continues). Four probes, all killed -- one of them only after the
mutation script was fixed: `str.replace` with a needle that does not match is a silent no-op, so a
probe can report SURVIVED for a cut that was never made. The script now asserts the text changed.

### Slice 9f — `expected_totals` integration (PARTLY BUILT: the read path)

#### 9f-1 — the read path, built

**What was actually missing was bigger than the slice assumed, and smaller than it feared.** Phase 3a
built `expected_totals`, its constraints and the queries (`GetExpectedTotals`, `GetObservedCount`,
`ListStudioCompleteness`), and NOTHING read them: the only Go references were the sqlc struct and two
schema tests. So `internal/service/completeness` is new, and its first method (`StudioTotals`) is the
read path the earlier phase left open: each denominator with what the instance holds against it
(`Expected`/`Observed`/`Missing`, floored at zero because over-count is real and intended), the claim's
own `source_url` as the curator wrote it, and the health record for that page.

**TWO DISCOVERIES, both now encoded:**

1. **`content_sources` cannot hold a studio's page.** Its `entity_type` CHECK allows `'scene'` and
   `'content_item'` and nothing else, so a lookup filtered to the total's own entity type could NEVER
   match -- the first version of this code would have returned "no record" for every studio total on the
   instance, forever, while looking correct. The lookup is therefore per PAGE and ACROSS entity types
   (`FindContentSourcesForURL`), and ranks an alive page above a dead one, because a page one scene has
   given up on is still a page another is serving. What this means in practice: the field fires when the
   claim's source page is a URL the instance aggregates (a scene or content-item page), and stays
   honestly absent otherwise.
2. **"No record" is the ORDINARY case, not a lost source.** Most denominators come from pages nothing
   aggregates, so `Unverifiable()` is true only for `dead`, `dmca` and `archived` -- statuses the
   instance actually observed. An absent record and an `unknown` one are both "no evidence either way",
   and flagging them would cry wolf about every total on the instance.

**Verification (9f-1, DONE):** a unit table over all seven statuses plus the nil record; three
integration tests (a canonicalised resolution with BOTH columns asserted and the numbers UNCHANGED
after the page dies -- the assertion that proves dropping the denominator would be wrong; an untracked
page staying ordinary; the gap flooring at zero on over-count). Four probes, all killed, with the
mutation script asserting each cut landed (a silent no-op replace reports a false SURVIVED, which cost a
probe in 9e).

#### 9f-2 — the cap and the quest (DECIDED, still not built)

Migration 97's totals carry `(source_entity_type, source_url)` and its own comment says a total is "a
claim about the world" whose source must be real. Migration 108 then made `content_sources` the health
record for exactly those URLs. This slice joins the two.

**Resolve, do not copy.** The link is computed at READ time from the URL (`ingest.NormalizeURL`, the
same canonicalizer the ingest path writes `content_sources.url` with), via a query like
`FindExpectedTotalSources(entityType, entityID)`. No FK and no cached health column on the total: a
total's source is a claim, and copying a health row's id or status into it would freeze a stale answer
next to a live one -- the same reason slice 9e's columns are written by one statement.

**Report.** The total's GraphQL type gains `source: ContentSource`, so a client can say "this
denominator came from a page that is gone" rather than showing a number with no provenance.

**Score: CAP the entity, do not drop the denominator.** A total whose source is `dead`, `dmca` or
`archived` is UNVERIFIABLE. Dropping it is the tempting move and it is the exact failure migration 97
exists to prevent: a studio with 3 of 400 scenes catalogued would score 100% the moment its source page
died, rewarding the loss of evidence. So the denominator stands, and the entity's completion score is
capped at `100 - UnverifiablePenalty` (10) -- ONCE per entity, not once per total, because what is
compromised is the record's provenance rather than each individual number. This is a DECISION (the
spec section §7.24.2 does not address a source that dies, and the phase's acceptance criterion says only
that the score drops), recorded here as one so the next reader can argue with it rather than guess at it.

**Quest.** `quest.Generate(ctx, entityType, field, target)` raises quests per missing FIELD; an
unverifiable total is not a missing field, so it gets its own entry point (`GenerateUnverifiedSource`)
built from the same `Wording` machinery, so the wording convention stays in one place. The quest names
the URL: "the page this total came from is gone".

**Still to build, and why it is separate:** the cap needs a completion score that consults totals at
all, and today's `completion.Service` computes its fields without ever reading `expected_totals` -- the
score/totals link is itself phase 3's leftover, not this slice's. `Unverifiable()` is the input that
cap needs, and it now exists and is tested, so the remaining work is composition rather than discovery.

**Verification (9f-2, to build):**
* A studio whose total's source is live: the score is unchanged and `source.status` is `LIVE`.
* The same row after the source is marked dead: the score drops by the penalty, the quest exists and
  NAMES the URL, and the denominator still counts -- asserted directly, because "the score dropped" and
  "the denominator was dropped" are different outcomes and only the first is wanted.
* A source URL written in a non-canonical form still resolves (the canonicalizer's job).
* Probes: the penalty removed; the denominator dropped instead of capped (which reddens the 3-of-400
  assertion); the resolution done by exact string match (an unnormalized URL stops resolving).

### Slice 9g — performer profile snapshots

**Goal:** a performer's profile page as captured from an external site, stored as evidence of what the
instance knew and when, for the identification board.

**Verification:** a snapshot round-trips with its capture time, and a second capture does not overwrite
the first (a snapshot is a record, not a cache).

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
