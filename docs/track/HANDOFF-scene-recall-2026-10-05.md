# HANDOFF — scene recall: 200 ideas awaiting triage

**For:** an agent picking this up with no prior context on this repository.
**Date:** 2026-10-05. Repo state: `main` at `6fb5c0e8`, migrations newest `102`.
**Status:** nothing built. Two brainstorm documents and a merged index exist.
This handoff tells you what they are, what is true, what is false, and where to
start.

---

## 1. The task you are picking up

The originating problem: **someone remembers a scene they saw a long time ago
in very broad strokes** — an era, a vibe, half a performer name, a setting — and
a "tip of my tongue" community could not find it. Those communities fail
structurally: prose threads all the way down, no structured data to search.

This repo *has* structured data. The question is what to build around it.

Two agents brainstormed 100 ideas each, independently. Both landed as markdown.
They have been merged into one directory and **nothing has been implemented**,
specced, or committed.

### The files you need

| Path | What it is |
|---|---|
| `docs/ideas/scene-recall/README.md` | **Start here.** The merge record, the 18-lever overlap map, the corrections, and the recommended starting sequence. |
| `docs/ideas/scene-recall/SOURCE-A-tip-of-tongue-2026-10-05.md` | 100 ideas. Sections A–J, numbered 1–100. |
| `docs/ideas/scene-recall/SOURCE-B-tip-of-tongue-2026-10-05.md` | A different 100. Sections A–J, numbered 1–100. **Do not assume the numbering aligns between the two files.** Both have an "#16 structured RecallQuery" and both have an "#1 index scene.details", but idea #50 in A and idea #50 in B are unrelated. |

Both sources are preserved verbatim. Nothing was merged by rewriting; the
overlap is recorded in the README as a table instead.

---

## 2. Where this sits in the repo's planning conventions

Getting this wrong has already cost a session in this repo, so read this before
writing anything.

`docs/plans/README.md` documents **four** planning lineages. They are disjoint
and confusing them is expensive:

| Directory | Holds | Maintained by |
|---|---|---|
| `docs/SPEC.md` | **The spec.** Product vision, numbered sections (§1–§9, and the §7.x amendments). | Hand-written |
| `docs/plan/` (singular) | Hand-written feature plans. `feature-04-identification-federation.md` is complete. | By hand |
| `docs/plans/` (plural) | One generated plan per GitHub issue + the phase roadmap. 26 issue plans, 8 feature plans. | `generate_plans.py` |
| `docs/ideas/scene-recall/` (new) | **This.** Raw brainstorm input awaiting triage. | By hand |

`docs/track/` holds handoffs and intake reviews — `HANDOFF-R074.md`,
`INTAKE-2026-10-01-curation-completeness.md` — plus the mutation specs at
`docs/track/mutations/`. The `INTAKE-*` files are the closest precedent for what
you are about to do: an intake review that triages an AI-generated idea ranking
against the spec and the tree, recording adopted / adapted / already-built /
rejected with reasons. **That is the natural next artifact for these 200 ideas,
and it should probably be written to `docs/track/INTAKE-2026-10-05-scene-recall.md`.**

The repo's standing rule, from the owner's profile and every handoff in
`docs/track/`: **write the spec and the plan before implementing.** A plan here
must be detailed enough for an LLM with no context on this codebase to execute
it — exact file paths, complete copy-pasteable code, and per step the exact
verification command plus its expected output.

---

## 3. The environment, verified on this host today

Everything below was run on 2026-10-05, not recalled.

### The build

```
go version go1.27.1-X:nodwarf5 linux/amd64
go build ./...        → exit 0
```

### THE WORKING TREE IS CURRENTLY BROKEN — and it is not your doing

Two files are modified-but-uncommitted, and they break the **unit** suite:

```
 M internal/config/conn_max_lifetime_test.go
 M internal/database/testutil/testutil.go
```

`testutil.go` has had `func Factory() *service.Factory` deleted, so
**17 integration test files fail to compile** across three packages
(`internal/api` ×16, `internal/service/imagetype` ×1, plus `internal/service/image`),
each dying on `undefined: dbtest.Factory`. Note that `go vet` reports only *one*
error per package, so it under-reports this badly — it names two files and there
are seventeen. `conn_max_lifetime_test.go` separately has two `Errorf("%d")`
calls with no argument, which `go vet` rejects and which fails that package's
build.

Verified by contrast, in a detached worktree at clean `HEAD` (`6fb5c0e8`):

```
go vet ./internal/config/ ./internal/database/testutil/    → clean
go test ./internal/config/ ./internal/database/...          → ok
go test -tags=integration -run TestIdentification ./internal/api/  → ok, 6 tests PASS
```

**So: `git checkout -- internal/config/conn_max_lifetime_test.go
internal/database/testutil/testutil.go` before you do anything.** Then re-run
`go test ./...` and confirm it is green before you touch a line of your own. Do
not commit those two files' current state. Whether to *fix* them properly is a
separate decision for the owner — it is not part of this task, and both files
are green at HEAD.

### Postgres for the integration suite

There is **no local Postgres** (`pg_isready` → no response on 5432). Use the
running container:

```bash
sudo docker ps -a --format '{{.Names}}\t{{.Status}}'
# sbx-pg-a   Up 18 hours      stashbox-pg:18   NetworkMode=host, port 55434

export POSTGRES_DB='postgres:smoke_pw@127.0.0.1:55434/stash-box-test?sslmode=disable'
```

Verified in that database right now:

```
bktree | 1.0        — SP-GiST bktree opclass, PHASH Hamming search
pg_search | 0.21.8  — ParadeDB BM25
pg_trgm | 1.6
84 tables, schema_migrations = 102, image_types seeded with 27 rows
scene_search columns: scene_id, scene_title, scene_date, studio_name,
  network_name, studio_aliases, network_aliases, performer_names, scene_code
```

**`POSTGRES_DB` is not a URL.** Give it `postgres:smoke_pw@host:port/db?sslmode=disable`.
A `postgres://` scheme makes pgx parse it as `user=alvaro` and the suite dies in
`pgDropAll` with `lookup postgres: no such host` — which reads like a broken
build and is not. `docs/track/HANDOFF-R074.md` records this at length.

**Port 55434, and only one suite at a time.** `stashbox-pg-r074` (5436) is
exited and abandoned; `lh-pg-test` (55433) is lorehaven's. Two suites on one
database deadlock each other. `make it` already passes `-p 1` for this reason
(`Makefile:114`).

Note: `pg_spgist_hamming` is **not** compiled into the `stashbox-pg:18` image
that is running — only the `bktree` opclass is present in `pg_opclass`. The
Dockerfile at `docker/production/postgres/Dockerfile` builds it from
`InfiniteStash/pg-spgist_hamming`, but this running container was built from a
different image (`stashbox-pg:18`). PHASH search still works, via `bktree_ops`.
`docs/track/REFS.md` records that upstream PR 928 was declined partly because it
would drop that build. Worth confirming before you depend on it.

### Gates

| Gate | Command | Clean-HEAD result |
|---|---|---|
| build | `go build ./...` | exit 0 |
| vet | `go vet ./...` | clean |
| format | `gofmt -l ./internal/` | empty |
| unit | `go test ./...` | ok |
| integration | `make it` | ok (30 packages at last full run, `docs/track/HANDOFF-R074.md`) |
| mutations | `python3 docs/track/mutations/mutate_r074.py "$PWD" docs/track/mutations/<spec>.json` | 85 killed across 10 specs |

There is **no `gate_all.sh`** in this repo. If a handoff or spec mentions one,
it belongs to another project.

---

## 4. What is actually true about the codebase

Grep-verified 2026-10-05. This is the section to trust over anything either
source says.

### The search stack — the actual bottleneck

`scene_search` is a denormalized table maintained by triggers, with a ParadeDB
BM25 index. Its columns, confirmed from the live database:

```
scene_id, scene_title, scene_date, studio_name, network_name,
studio_aliases, network_aliases, performer_names, scene_code
```

**Not indexed: `scene.details`, `scene.director`, and scene tags.** Migrations
35 (tsvector) → 56 (ParadeDB) → 61 (tweak, added the alias arrays) each rebuilt
this table and none of them added those three. For broad-stroke recall that is
the whole gap: "hotel room, rainy night" and "Japanese studio, two performers,
anal" are exactly the memories, and none of them are in the index.

The query itself is `SearchScenes` at `internal/queries/scene.sql.go:664`. It is
already sophisticated: per-token `disjunction_max` across eight fields, a flat
+10000 coverage tier per matched token so more-matched-tokens always outranks
fewer, and a 2.0 boost on `performer_names`. **It uses `paradedb.match` only —
no `paradedb.fuzzy`, no `paradedb.parse`.** The performer search path *does* use
`paradedb.parse` and `tokenized_phrase` (`internal/queries/performer.sql.go:42`).
Scenes never got it.

**Trigram indexes exist and nothing uses them.** Migrations 73 and 75 create
GIN `gin_trgm_ops` on `scenes.code`, `scenes.title`, `performers.name`,
`performers.disambiguation`. Grepping the generated query layer for
`gin_trgm_ops` / `similarity(` returns nothing in the scene path. A fuzzy
fallback tier is available today at the cost of one query.

`SceneQueryInput` (`graphql/schema/types/scene.graphql:239`) has: text (deprecated),
title, url, code, date, production_date, id, studios, parentStudio, tags,
performers, alias, fingerprints, favorites, has_fingerprint_submissions. **No
duration, no date *range*, no performer count.**

### The identification board — exists, and is deliberately conservative

`internal/service/identification/service.go`, tables from migration 79
(`identification_queries`, `identification_candidates`,
`identification_candidate_votes`), federation from migration 89
(`federation_peers`, `identification_foreign_candidates`).

The governing rule is in the package doc and it is not negotiable:

> a query is a QUESTION and its candidates are SUGGESTIONS; neither becomes
> metadata until a human resolves the query. … A plurality vote is not a
> creation.

`Resolve(queryID, type, id, by, recordTrust)` is the single function that turns
a query into a canonical link, and it takes a `recordTrust` callback.

Schema (`graphql/schema/types/identification.graphql`): `IdentificationPostInput`
is `{targetType, targetId, description, snapshotId}`. **`description` is the only
required field and there is no other queryable clue field.**
`IdentificationSuggestInput` is `{queryId, entityType, entityId}` — and the
`IdentificationCandidate.note` field's own doc comment says verbatim:

> Not exposed as a mutation input yet: no caller in this version has a way to
> supply one.

`collage_id` is in the table and the Go struct (`service.go:141`, `convert.go:53`)
but **not in the GraphQL type** — one `collage` hit in the schema file, and it is
inside a comment.

Statuses are `open | solved | abandoned`. There is no "narrowing", no
"not in this database", no "unsolved for years".

### Collages

`internal/service/collage`. 12–24 frames, `MinFrames = 12`, `DefaultFrames = 16`,
sampled as **millisecond offsets** over the scene's duration. A collage is a
cache of a selection over `scene_snapshots`, regenerated not mutated. Errors are
distinct and deliberate: `ErrNotEnoughSnapshots`, `ErrNoDuration`, `ErrSceneNotFound`.

The design decision that matters for this task, quoted in the package doc: a
snapshot is **a timestamp, not a stored image**, because stash-box is a metadata
server. 24 timestamps replicate mesh-wide; a JPEG does not. **Any idea that
matches remembered *pixels* against collages requires images, and there are
none.** `scene_snapshots` has no hash column and `images` has none either.

### Fingerprints — the one working perceptual-hash path

`scene_fingerprints` + `fingerprints`, MD5/OSHASH/PHASH. Migration 52 makes the
PHASH a `bigint` and indexes it:

```sql
CREATE INDEX fingerprints_phash_idx ON fingerprints
  USING spgist (hash bktree_ops) WHERE algorithm = 'PHASH'
```

Confirmed live. The traversal works: `ExpandPhashNeighbors` in
`internal/queries/sql/fingerprint.sql` uses `FP.hash <@ (phash, $2::INTEGER)`
over an `UNNEST`, and `config.PHashDistance` (`internal/config/config.go:146`)
sets the radius. `internal/service/fingerprint/cluster.go` is 496 lines of
cluster logic on top of it.

So: **near-duplicate *video* is solved today. Near-duplicate *image* is not,
and cannot be until someone stores a hash per image.**

### Federation — the boundary rules a new feature must respect

`internal/service/federation/`. The constraints are documented as F1–F6 in
`docs/spec/feature-04-identification-federation.md`, and enforced in code:

- **F1** — the broadcast payload is a closed struct with no field capable of
  holding media. `Question.Validate` (`ask.go:199`) rejects empty target types,
  descriptions over `MaxDescriptionLen = 2000`, and suspicious values
  (`IsSuspiciousValue`). "Content never broadcasts" is enforced by the type.
- **F2** — a foreign candidate lands in `identification_foreign_candidates`,
  never in `identification_candidates`. It cannot be voted on locally, cannot
  win a plurality, cannot become a canonical link.
- **F3** — foreign evidence is scoped to one query and expires.
- **F5** — the peer registry is operator-configured. `baseurl.go` and
  `dialguard.go` fail closed; `imageurl.go` is the SSRF-safe URL handler any new
  image-fetching feature must route through.

`wire.go` has `Question{Description, TargetType, ...}` and `Answer`. A scene-recall
broadcast is a new question *kind*, not a new transport.

### Taste, Elo, quests, completion, notifications

- `internal/service/elo/taste.go` — taste keys are `"<prefix>:<entity uuid>"`,
  per-entity not per-kind, stored as JSONB with a `vote_count` beside it. The
  file's own comment is explicit that only the Elo-vote term has data behind it
  and the other five terms in SPEC §2's list are named and missing.
- `internal/service/federation/select.go` — `Cosine(a, b map[string]float64)`,
  peer selection ranked by taste similarity with a `vote_count` floor and an
  explicit zero-cosine-keeps-last rule. This is the **only** cosine in the repo.
- `internal/service/completion/score.go` — `Field` constants and per-entity-type
  weight lists. `sceneFields` (line 172) already scores `FieldDetails` at weight
  10 and `FieldTags` at 7. So "details" *is* a completion field; it is a
  *search* field it is missing.
- `internal/service/quest/quest.go` — `Generate(ctx, entityType, field, target)`
  and `GenerateAll`, built off completion. Quests follow for free from a
  completion change.
- `internal/service/notification/service.go`, `internal/service/streak/`,
  `internal/service/mod_audit/`, `internal/service/award/`,
  `internal/service/webhook/` all exist.

### What does not exist at all

Grep-verified. These are the claims both sources lean on hardest:

- **No ML or vector infrastructure.** No `clip`, `onnx`, `embedding`, `pgvector`,
  `qdrant`, `milvus`, `pinecone`, `tantivy`, `meilisearch` in `go.mod` or
  `internal/`. No CLIP model, no image embedding, no ANN index.
- **No "similar to".** `grep -r similarTo graphql/schema/` returns nothing.
  SPEC §7.4 promises "similar to" on every entity page; nothing provides it.
- **No date-range, duration-range, or performer-count filter** on
  `SceneQueryInput`. `DateCriterionInput` is one `Date!` plus a comparison
  modifier — `GREATER_THAN`, `LESS_THAN` — so a range is expressible only by
  passing one bound at a time.
- **No pHash on any image or snapshot.** Only video-file fingerprints.
- **No full-text index on `identification_queries.description`.** The board's
  own archive is unsearchable: nobody can find the question about the redhead.
- **No `collage_id` in the GraphQL identification types**, despite the column
  existing.

---

## 5. Corrections and caveats

**One factual error found in Source B, recorded so it is not repeated.**

Source B #17 asserts: *"there's no NOT in `SceneQueryInput` today (only
deleted-style exclusions would need adding)."*

**False.** `CriterionModifier` includes `EXCLUDES`, and
`internal/service/scene/query.go` honours it for `id` (line 145), `studios`
(line 203) and `tags` (line 223), with integration coverage at
`internal/api/scene_integration_test.go:561` and `:650`. Negative filters are a
UI and vocabulary problem, not a missing operator. The idea survives, weaker.

**The generalisable lesson.** An idea list that claims something does not exist
is more expensive than one that makes no claim, because a downstream agent sizes
the work from the claim and skips the check. **Re-verify every "X does not
exist" in both sources before turning it into a ticket.**

**On numbering.** Both sources number 1–100 in their own sections A–J. The
numbers do **not** correspond across files except where the README's overlap
table says so. Never refer to "idea #N" without naming the source.

**On SPEC section references.** Both sources cite "SPEC §5" for the
identification board and "§7.x" for various features. The Go code cites `§5`,
`§7.7`, `§8`, `§9` heavily, but `docs/SPEC.md` has since been restructured — its
headings are now `## 5. Decisions already made`, `## 8. Defects observed`, and
the identification board is `### 7.5`. **The code's comments and the SPEC's
numbering have drifted apart.** Either cite both, or verify before citing. This
is a known condition, not something you introduced.

---

## 6. What I would do next, in order

### 6.1 Unblock the tree (2 minutes)

```bash
cd ~/code-local/go/stash-box
git checkout -- internal/config/conn_max_lifetime_test.go \
                internal/database/testutil/testutil.go
go test ./...            # expect: all ok
go vet ./...             # expect: clean
```

### 6.2 Write the intake review, not a plan yet

`docs/track/INTAKE-2026-10-05-scene-recall.md`, following
`INTAKE-2026-10-01-curation-completeness.md` as the template: adopted /
adapted / already-built / rejected-with-reason, every existence claim checked
against the tree.

Expect the intake to land somewhere uncomfortable: that is the previous
intake's finding too — *"this draft is mostly already built, and its top-ranked
idea conflicts with the spec."* Two things in these 200 are already built or
partly built (the board, the quest generator), and at least one premise is
wrong ("ToMiT communities couldn't find it" is usually not a search-algorithm
failure — see 6.5).

### 6.3 Build the evaluation set before any feature

**150–200 remembered descriptions with known answers.** Drawn from the board's
own solved queries. Without it, every idea in both lists is unfalsifiable, and
the highest-ranked ones are the least measurable because they are
one-migration changes that "obviously help".

This is the single most valuable thing in the handoff, and both sources
independently gestured at it (A #77, B #100) without either making it step one.

### 6.4 Then the cheap, jointly-ranked-first lever

`scene_search` gains `scene_details`, `scene_director` and `tag_names`; the
`SearchScenes` disjunction_max gains those fields with tags weighted above
studio. One migration (103) and one query change. Both sources ranked this
first. It is worth doing **after** the eval set, so you can measure it.

Watch for the trigger-drift trap: `scene_search` is maintained by triggers, so
a scene whose trigger missed stays invisible forever, and that failure is
silent. A drift check belongs with this change.

### 6.5 Two honesty notes to carry into the intake

**The strongest technical answer is the weakest fit.** CLIP / face / appearance
matching is what actually solves "I remember the picture, not the words" — and
there is no ML stack, no image hashes, and per SPEC §7.17 the mesh replicates
questions and never content. It belongs after the text is searchable, not
before.

**The failure may not be a search failure at all.** "ToMiT communities couldn't
find it" has two very different causes, and the ideas address them differently:

- **Vocabulary failure** — the user cannot name what they remembers. Addressed
  by structured clues (A #51/#53, the grain-of-memory idea) and by learning
  the vocabulary from solved queries (A #54).
- **Coverage failure** — the scene was never catalogued. Addressed by
  `expected_totals` (migration 97, Source B #92): "this studio claims 400
  scenes, we have 120" tells an asker their memory may be fine and the *archive*
  is incomplete. If that is the real cause, no amount of better search finds it.

Distinguishing these two before building is the highest-value thing the intake
can do, and it needs data neither source has.

---

## 7. Constraints that apply to whatever you build

- **A vote is evidence, never authority.** Every idea in both lists routes
  through `resolveIdentificationQuery` or the edit path, by a named human.
  Neither source proposed creating metadata from a tally, and neither should
  you. The identification package doc's stated worst outcome is an archive
  filling itself with confidently wrong records.
- **New tables need a `.down.sql`.** Migration discipline is enforced; 102 is
  the field-verification cascade, and the bktree index is deliberately preserved
  across index swaps.
- **Keep SQL in the generated query layer.** `internal/queries/sql/*.sql` is
  sqlc's input; hand-written SQL in a service is a review finding.
- **Any image-URL handling routes through `federation/imageurl.go`.** SSRF-safe
  fetch, fail closed. Non-negotiable.
- **Any new vocabulary needs a governance answer.** Both `image_type_groups` and
  `site_details.ethical_labels` model this differently on purpose — one is a
  seeded table with conflicts, the other is free text. Pick deliberately and
  say why.
- **Respect the content-access gate.** SPEC §7.6 has six levels; metadata and
  collages are level 0, media URLs are not.

---

## 8. Files worth reading before you write anything

| Path | Why |
|---|---|
| `docs/ideas/scene-recall/README.md` | the merge record and overlap map |
| `docs/SPEC.md` §7.4–§7.14 | discovery, identification board, trust levels, curation, collages, Elo, directory, ecosystem, gamification, governance, flywheel |
| `docs/spec/feature-04-identification-federation.md` | F1–F6. The boundary every new federated idea must respect |
| `internal/service/identification/service.go` | the package doc that states the whole governing rule |
| `internal/database/migrations/postgres/79_add_identification_board.up.sql` | the board's schema, with its reasoning in comments |
| `internal/database/migrations/postgres/56_paradedb_search.up.sql` and `61_tweak_scene_search.up.sql` | the search table you are about to extend |
| `internal/database/migrations/postgres/78_add_scene_snapshots.up.sql` | why a snapshot is a timestamp and not an image |
| `internal/queries/sql/fingerprint.sql` | the `<@` bktree traversal you would reuse for frames |
| `internal/service/completion/score.go` | `sceneFields` at line 172 |
| `internal/service/elo/taste.go` | the taste-key design and its honest list of what is not wired up |
| `docs/track/HANDOFF-R074.md` | the environment traps, written down by whoever hit them |
| `docs/track/INTAKE-2026-10-01-curation-completeness.md` | the intake template, and a precedent for "mostly already built" |
| `CLAUDE.md` | build commands, architecture, the three fingerprint algorithms |

---

## 9. The one-sentence version

Two hundred grounded ideas exist in `docs/ideas/scene-recall/`, 18 of which two
agents reached independently; the search index provably omits `details`,
`director` and tags; the board that would catch what search misses already
exists and deliberately refuses to auto-resolve; **build the evaluation set
first, unblock the working tree, and write an intake review before a single
line of feature code.**