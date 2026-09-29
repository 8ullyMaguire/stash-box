# stash-box fork — SPEC

**Status:** draft v0.2 — foundation, direction resolved (§6.1), vision amended (§7.17–§7.22)
**Repo:** `~/code-local/go/stash-box` (clone of `github.com/stashapp/stash-box`)
**Pinned upstream:** `b4b8aef2` ("Use setup-go native cache (#1250)", 2026-09-09)
**Written:** 2026-09-28 · **Amended:** 2026-09-29 (§7.17–§7.22, mesh architecture)

Every fact in §2 was measured on this host on the clone, not taken from the
README or `CLAUDE.md`. Where those two disagree with measurement, measurement
wins and the disagreement is recorded — two of their claims are false (§2.5).

---

## 1. What this document is

The owner asked to work on a fork of stash-box but did not state the product
direction. Rather than invent one, this spec does the part that is valuable
under any direction: it establishes the verified baseline, maps the
architecture, and names the decisions a fork has to make.

**§6 is the open question and it needs the owner's answer before code is
written.** Everything below it is settled.

---

## 2. Verified baseline (measured, not documented)

### 2.1 Clone and shape

| Fact | Value |
|---|---|
| Tracked files | 943 |
| Go | 249 files |
| TSX / TS | 231 / 153 |
| `.graphql` | **17** (not 133 — see note) |
| SQL files | 94 total; **78** of them migrations (75 `.up.sql`, 3 `.down.sql`) |
| SQL query sources | 16 files in `internal/queries/sql/` |
| GraphQL schema | 2044 lines across 17 files |
| gqlgen resolvers | 46 (`internal/api/resolver_*.go`) |
| License | MIT (21 lines) |
| History | long-lived, actively developed, 2026-09-09 tip |

Non-uniform by design: `internal/models/generated_exec.go` alone is 47 149 lines
(1.5 MB) of gqlgen output. **Hand-written production Go is ~30 000 lines**
(excluding `generated_*`, `*.sql.go`, `converter/`, `dataloader/`,
`queries/`); raw counts dominated by generated code overstate the project's size
by 2-3x.

### 2.2 The build DOES NOT WORK on a clean clone

This is the single most important practical fact in this spec, and it is not
mentioned in either the README or `CLAUDE.md`.

```
$ go build ./...
frontend/embed.go:5:12: pattern build: no matching files found
```

`frontend/embed.go` is:

```go
//go:embed build
var FS embed.FS
```

`frontend/build/` does not exist until the frontend is built. A
`//go:embed` on a missing directory is a **module-load** error, not a package
compile error, so the consequences cascade further than a normal failure:

- `go build ./...` fails
- `go test ./...` fails with `[setup failed]`
- **`go list ./...` returns ZERO packages** (41 with `-e`)

That last one is the sharp edge. Anything script-driven against `go list` sees
an empty package set, and `make generate` cannot bootstrap. Verified directly:

```
$ go list ./...   | wc -l
0
$ go list -e ./... | wc -l
41
```

**Workaround, verified:** create a real (non-dotfile) file —
`go:embed` ignores `.`-prefixed entries, so `touch .gitkeep` is *not*
sufficient — and the module loads:

```bash
mkdir -p frontend/build && echo placeholder > frontend/build/index.html
go list ./... | wc -l   # -> 41
```

**The placeholder moves the failure, it does not fix it.** With it in place the
embed error is gone (verified: zero occurrences of `pattern build: no matching
files`) and `go build ./...` proceeds past module load — only to fail at the cgo
link stage on the libvips/OpenEXR skew in §2.4. Still no binary, still exit 1.
The two blockers are sequential, not alternatives: the placeholder exists so
`go list`, `go test`, and codegen can run against a real package set while the
system-library problem is unresolved.

### 2.3 Unit tests pass with that one line

```
$ go test $(go list ./... | grep -vE 'internal/image$')
ok  github.com/stashapp/stash-box/internal/auth
ok  github.com/stashapp/stash-box/internal/models
ok  github.com/stashapp/stash-box/internal/service/edit
ok  github.com/stashapp/stash-box/internal/service/fingerprint
ok  github.com/stashapp/stash-box/internal/service/loadutil
ok  github.com/stashapp/stash-box/internal/service/query
ok  github.com/stashapp/stash-box/pkg/utils
```

### 2.4 The integration suite — the real gate — does NOT link

All 29 integration-tagged files live in `internal/api` alone. They fail to
link, on a host library conflict:

```
/usr/bin/ld: warning: libOpenEXR-3_5.so.34, needed by /usr/lib/libvips.so, not found
/usr/bin/ld: /usr/lib/libvips.so: undefined reference to `ImfCloseTiledInputFile'
   ... 14 more undefined Imf* references ...
```

Root cause, confirmed by inspection:

| | |
|---|---|
| Installed `openexr` | **3.4.15-1.1**, from repo `cachyos-extra-v3` |
| Available `openexr` | **3.5.0-2**, from repo `extra` |
| Installed `libvips` | 8.18.6-2 |
| `libvips.so.42.20.6` `NEEDED` | `libOpenEXR-3_5.so.34` |
| `/usr/lib/libOpenEXR-3_5*` | **absent** (only `3_4` present) |

CachyOS shipped `libvips` from `extra` (linked against OpenEXR 3.5) while
resolving `openexr` to `cachyos-extra-v3`'s 3.4. A packaging skew, not a
project defect. The fix is to install `extra`'s `openexr` 3.5.0-2.

**This has deliberately NOT been applied.** It replaces a system library
`appstream` also depends on, and changing the host's package set is a
costly, non-repo decision that belongs to the owner. Until it is resolved:

- the integration suite cannot run
- no image-processing code can be tested or run
- **a working local instance of the app cannot be started** (§2.6)

### 2.5 The project's own documentation is wrong in two places

Both errors are the kind that cost an implementer an hour:

1. **README** claims *"the integration tests run against a temporary sqlite3
   database by default."* **False.** `internal/database/testutil/testutil.go` is
   Postgres-only — it imports `github.com/jackc/pgx/v5/pgxpool` and connects with
   `pgxpool.New(ctx, "postgres://"+connString)`. There is no sqlite path in the
   tree. `CLAUDE.md` states the Postgres truth correctly; the README is stale.

2. **The integration database does not exist on this host.** No
   `stash-box-test` database, and `bktree` / `pg_search` are **not** in
   `pg_available_extensions` (only `pg_trgm` and `unaccent` are). Created for
   this investigation:

   ```bash
   PGPASSWORD=... psql -h 127.0.0.1 -U postgres -c 'CREATE DATABASE "stash-box-test";'
   ```

   Migration 56 (`56_paradedb_search.up.sql`) issues an **unguarded**
   `CREATE EXTENSION IF NOT EXISTS pg_search;` — unlike migrations 14/22/34/52,
   which guard with `IF EXISTS (SELECT 1 FROM pg_extension ...)` and so degrade
   gracefully. With `pg_search` unavailable, migration 56 will fail and take the
   whole migration chain with it. **Verify this against a real run before
   assuming the schema builds.**

### 2.6 No running instance is possible yet

Three independent blockers stack, and all three must clear before anyone can
click through a fork:

1. libvips/OpenEXR link failure (§2.4) — no cgo binary links
2. `pg_search` unavailable (§2.5) — schema migration chain likely fails
3. `frontend/build/` missing (§2.2) — `go:embed` fails module load

`pg_search` is only worth solving if the fork actually needs ParadeDB full-text
search; otherwise a local-only migration strategy is enough (see §5.2).

### 2.7 Codegen works, and is reproducible

All five generators are pinned as Go module tools — **no separate install, and
the `sqlc` / `golangci-lint` binaries being absent from `PATH` is a non-issue for
the first four**:

```
$ go tool
gqlgen, introspection, goverter, sqlc, dataloaden
```

Verified end to end:

```bash
go tool sqlc generate   # exit 0
go tool gqlgen generate # exit 0
git status --porcelain  # EMPTY — regeneration is byte-identical
```

**The generated code is committed and regeneration is a no-op.** An earlier
reading in this session guessed `internal/queries/queries.go` and
`internal/api/generated.go` were missing and uncommitted; that was wrong. sqlc
emits per-query files (`internal/queries/edit.sql.go`, 40 992 bytes, etc.) and
gqlgen writes to `internal/models/generated_*.go` — all tracked, all
reproducible. Codegen is therefore a *safe* operation in a fork, and
"did regeneration change anything?" is answerable with `git status`.

`make generate` additionally calls `cd frontend && pnpm generate`
(graphql-codegen), which is **not** covered by the above — frontend deps are not
installed on this host.

### 2.8 Toolchain status

| Tool | Status |
|---|---|
| go | 1.27.1 (repo needs 1.27.0) — OK |
| make | 4.4.1 — OK |
| node | v26.7.0 (needs `>=24`) — OK |
| pnpm | 12.4.1 (repo pins `pnpm@11.21.0`) — drift |
| libvips | 8.18.6 present but **unlinkable** (§2.4) |
| psql | 18.6 — OK |
| `golangci-lint` | **missing** — `make lint` cannot run |
| `sqlc` / `gqlgen` | missing from PATH, **available as `go tool`** (§2.7) |

---

## 3. Architecture

### 3.1 Request path

```
HTTP /graphql
  -> internal/api/          resolvers (46 files), directives, dataloader wiring
     -> internal/auth/      role checks, cached user projection
     -> internal/service/   business logic, one package per entity
        -> internal/queries/ sqlc-generated, pgx/v5
           -> Postgres
  <- internal/converter/    goverter: row structs <-> models
  <- internal/dataloader/   generated, prevents N+1
```

The layering is clean and worth preserving: resolvers hold no business logic,
services hold no SQL, and `internal/queries` is entirely generated.

### 3.2 Generated vs hand-written — know which is which

| Path | Nature |
|---|---|
| `internal/models/generated_exec.go` | gqlgen, 47 149 lines |
| `internal/models/generated_models.go` | gqlgen, 2 989 lines |
| `internal/queries/*.sql.go`, `querier.go`, `models.go` | sqlc |
| `internal/converter/*.go` | goverter |
| `internal/dataloader/*.go` | dataloaden |
| `internal/models/model_*.go`, `model_edit.go` | **hand-written** |

**`internal/models` is predominantly generated.** Do not hand-edit
`generated_*.go`; edit the source (`.graphql`, `sqlc.yaml`, SQL) and regenerate.
`model_edit.go` and `model_*.go` beside them are the real, editable surface.

### 3.3 The edit/voting core — the domain heart

`internal/service/edit` is the largest and most interesting service (4 480
lines; `service.go` alone 1 519). It is a MusicBrainz-style wiki: **no
contributor writes an entity directly — every change is an edit that must be
voted on.**

Roles, from `internal/models/generated_models.go:2262`:

```
READ, VOTE, EDIT, MODIFY, MODERATE, ADMIN, INVITE, MANAGE_INVITES
```

Note this is **eight** roles, not the five the README/CLAUDE.md list
(`READ, VOTE, EDIT, MODIFY, ADMIN`).

Lifecycle, from `internal/service/edit/service.go`:

| Stage | Entry point |
|---|---|
| propose (scene/studio/tag/performer) | `Create*Edit` — lines 684-953 |
| revise by creator | `Update*Edit` — lines 737-983, capped by `edit_update_limit` |
| vote | `CreateVote` — line 985 |
| apply (manual) | `Apply` 1097 / `ApplyEdit` 1142 |
| close | `CloseEdit` 1228, `CloseCompleted` 1409 (cron sweep) |
| amend (closed only) | `AmendEdit` 182, audit via `createAmendAudit` 259 |

**The decision function is a pure, shared function** — this is the best code in
the repo and the thing any fork must not fork-divergence:

```go
// Shared by vote casting and the cron sweep so the two can't disagree.
func decideEdit(tally editTally) models.VoteStatusEnum {
    threshold := config.GetVoteApplicationThreshold()
    if threshold > 0 && (tally.MinPeriodElapsed || !tally.Destructive) {
        if tally.Accept >= threshold && tally.Reject == 0 { return Accepted }
        if tally.Reject >= threshold && tally.Accept == 0 { return Rejected }
    }
    if tally.FullPeriodElapsed {
        if tally.Accept-tally.Reject >= netVoteThreshold(tally.Destructive) { return Accepted }
        return Rejected
    }
    return Pending
}
```

Governance invariants a fork inherits and must respect:

- **Destructive edits** (`Destroy`, `Merge`, and performer rename without
  `SetModifyAliases` — `model_edit.go:163`) face a minimum voting period
  *however* the votes fall, and need net `+1` to pass.
- **Votes are recomputed from rows, never tallied into a column.** `countVotes`
  (line 1320) reads `edit_votes` on every decision. The `Edit.VoteCount` field
  exists but `Passing()` consults it *alongside* the threshold — there is a
  latent drift hazard here worth auditing.
- **Amending restarts the voting period** — `editOpenedAt` (line 1334) returns
  `UpdatedAt` when set, else `CreatedAt`.
- **Error sentinels are declared at the top of `service.go`** (lines 25-35) and
  are part of the contract.

### 3.4 Configuration surface

YAML `stash-box-config.yml` + env overrides, documented in README. The
governance-relevant keys: `vote_application_threshold` (default 3),
`voting_period` (4 days), `min_destructive_voting_period` (2 days),
`vote_promotion_threshold`, `edit_update_limit`, `mod_audit_retention_days`.

---

## 4. Constraints a fork inherits

1. **Postgres-only.** No sqlite fallback exists. `bktree` (pHash distance) and
   `pg_search` (ParadeDB FTS) are optional *by comment* but migration 56's
   `CREATE EXTENSION` is unguarded.
2. **MIT, not copyleft** — forking, modifying, and self-hosting are all
   unencumbered. Contrast `~/code-local/go/stash`, which is AGPL-3.0.
3. **Downstream consumer is the Stash desktop app.** The GraphQL schema is a
   public compatibility surface; the client at `stashdb.org` is a separate
   deployment. A fork that changes query semantics silently can break a
   desktop app that cannot be updated in lockstep.
4. **Generated code is committed and reproducible** — good news. A fork can
   regenerate freely and use `git status` to detect drift.
5. **The 4-day voting period and 3-vote auto-apply are product decisions**, not
   implementation details. A fork that wants different governance should change
   *config*, not fork the code — the constants are all externalised.

---

## 5. Decisions already made in this spec

### 5.1 Clone location — settled

`~/code-local/go/stash` was already occupied by `github.com/stashapp/stash`
(the desktop app) with 7 uncommitted changes and a live worktree at
`~/code-local/worktrees/m6`. The owner chose to leave it untouched. stash-box
is at **`~/code-local/go/stash-box`**, full clone, `origin` =
`https://github.com/stashapp/stash-box.git` (the owner's own GitHub fork
should be added as a second remote — not done yet).

### 5.2 Placeholder over install, for now

The `frontend/build/` placeholder (§2.2) is a stopgap, not a solution: it makes
the Go module load but serves a broken UI. The real fix is `make pre-ui && make
ui`. The placeholder is recorded here only so the next session does not
rediscover the `go list` failure. **Decide per-milestone:** if work is
backend-only, keep it; if any UI is involved, build the frontend properly.

### 5.3 `docs/` as the spec home

`docs/` is **empty** in upstream stash-box. `CLAUDE.md` and `README.md` are the
only prose. So there is no established doc convention to conflict with, and
`docs/SPEC.md` is the natural entry point. A `docs/PLAN.md` should follow,
per the standing spec-before-code workflow, once §6 is answered.

---

## 6. The open question — fork direction

**No product direction has been specified.** This is the one thing §1 cannot
supply from measurement, and it determines everything downstream.

The relevant precedent: the sibling repo `~/code-local/go/stash` carries
**StashForge** work — a governance layer (proposals, ballots, reputation,
libraries, consent) built *on top of* stash's own models, motivated by
keeping the governance out of the upstream-mandated surface. stash-box *is* the
community database that StashForge's rules would apply to, and it already has
native voting that StashForge reimplemented. The plausible readings:

| Direction | What it means |
|---|---|
| **A. Port StashForge governance here** | Port the proposal/ballot/reputation model onto native `edit`/`edit_votes` instead of running a parallel system |
| **B. Fix/extend the fork's own behaviour** | Contributions back toward upstream: the `modbot.go` race (§7), the README's sqlite claim, the pnpm version pin |
| **C. Private/self-hosted instance** | Operational: clear the §2.6 blockers, harden config, run it |
| **D. Something else** | — |

A and C are the plausible pair: A is a large multi-milestone build, C is a
day of environment work plus a real design decision about data.

**Recommendation: C first, then A.** A fork cannot be designed against an
instance that does not start, and C produces the local dataset and the working
integration gate that A's plan would need. It also resolves §2.4/§2.5 as
prerequisites rather than leaving them as ambient risk.

### 6.1 Decision — taken 2026-09-29, by delegation

**The fork's direction is the federated discovery mesh (§7).** The owner
delegated this call rather than answering it directly, so here is the
reasoning, and here is what it rules out.

Direction A (port StashForge governance) is **rejected as the destination**.
StashForge reimplemented voting, reputation and proposals on top of stash's
models; stash-box already has native consensus (`edit` / `edit_votes`, 4 480
lines in `internal/service/edit`). Porting a parallel governance system onto
an archive that already has one would be the fork of a different product, and
it is not what the brief asks for.

Direction B (contributions back upstream) is **adopted as a standing practice,
not as the destination**. It costs nothing to keep upstreamable — small diffs,
generated code regenerated rather than hand-edited, and a bug fix that upstream
would accept is worth more than one that would not. Every issue fix in this
tracker is deliberately shaped that way. It is a constraint on how work is
done, not a goal.

Direction C (private/self-hosted instance) is **adopted as the prerequisite
the spec already recommended** — build, harden, run. It is now largely done:
the `vips`/`openexr`/`openjph` chain, the `pg_search` migration, the
`go:embed` frontend build, and a working integration gate are all resolved.

So: **C is a prerequisite, B is a constraint, and the destination is §7.** The
spec's own ordering ("C first, then A") was right about sequencing and wrong
about the endpoint, because at the time A and the mesh looked like
alternatives. They are not: the mesh needs the *capability* of StashForge's
governance, but it needs it federated and reputation-aware, which is what §7.6
(trust levels) and §7.12 (gamification) describe, and what A would deliver as
a single-instance system.

**Consequence for sequencing:** the remaining `help wanted` backlog still gets
finished first. It is cheap (bounded defects, each provable by a test), it
leaves the tree in a state worth building on, and it is the part most likely
to be upstreamed. §7 does not start until the bar is met.

**One decision deliberately left open:** whether the mesh's Phase 1 ships as
one instance or as a protocol. That is a genuine technical fork with real
cost either way, and it should be decided against the code rather than in
advance. It is recorded in `docs/PLAN.md` when Phase 1 is specced.

---

## 7. Product vision — the federated discovery mesh

The owner's premise, recorded here in full because it is the destination this
fork is being built toward. Nothing below is implemented yet; §6 (direction) is
still open and no code is written against this until it is answered.

**Tagline:** *Catalog everything. Curate together. Discover everywhere.
Preserve forever.*

Stash Box is a federated mesh of independently operated instances that together
form the world's most complete, community-curated archive and discovery engine
for adult content. It is metadata-first, discovery-first, and
preservation-first. Every instance is its own portal, community, and archive —
but instances see each other, peer with each other based on taste-profile
similarity, and automatically help each other preserve content.

### 7.1 Core vision

Each instance has its own operator, theme, community, trust thresholds,
content-access rules, storage budget, and replication policy. The mesh shares
metadata, Elo rankings, reviews, snapshots, identification boards, curation
quests, and preservation coordination.

Discovery is first-class: every performer, scene, studio, site, tag, list, and
instance gets recommendations, similar entities, and "because you liked…"
surfaces. Gravity pulls toward the instance theme — operators or a high-trust
subset of users set the instance's taste gravity, and recommendations, trending
lists, curation priorities, and preservation preferences bend toward it while
still respecting each user's personal taste.

Preservation is automatic by default: a scene should exist on at least three
instances unless configured otherwise. Each instance can opt out, set
minimum/maximum replication counts, and define which peers it trusts for
storage. Trust unlocks content: low-trust users see metadata and collages,
high-trust users who opt in can watch, stream, and manage content.

The result is a self-reinforcing archive — discovery brings users, users bring
curation, curation completes metadata, metadata improves discovery, trust unlocks
content, and federation keeps everything alive.

### 7.2 Federated mesh and taste-based peering

Every instance publishes a **taste profile** (from Elo votes, reviews, tags,
curation history, opt-in viewing signals, search behaviour, instance gravity)
and a **capability profile** (storage, bandwidth, uptime, content policies,
replication limits, trust thresholds, supported federation features).

Instances peer more closely with instances whose taste profiles are similar;
those peers share recommendations, cross-instance trending, joint curation
quests, and preservation priorities. Cross-mesh discovery ranks by **local
gravity + peer similarity + personal taste**, so a vintage-focused instance and
a VR-focused instance can both federate yet each see the mesh through its own
lens. Users may join multiple instances and carry a portable taste fingerprint.
Trust and reputation can be attested across instances while each operator keeps
local permissions.

### 7.3 Preservation by default

Every content object — scene, snapshot set, collage, metadata record — has a
preservation policy. The default is **a scene is hosted on at least three
instances**. Each instance configures minimum/maximum replicas it will host,
which scenes/studios/tags/performers it opts out of, preferred peers, and
storage/bandwidth/retention budgets.

The mesh schedules replication to trusted peers with capacity and similar taste
profiles. Health checks, manifest verification, and automatic repair keep
replicas alive; if an instance goes offline, peers detect missing replicas and
restore them. **Metadata is always replicated; content replication follows
per-instance policy.** Operators set global defaults, per-scene overrides, and
emergency rules. Users earn badges and reputation for contributing storage,
bandwidth, or curation that keeps rare content alive.

### 7.4 First-class discovery

Discovery is the default experience, not a search box. Recommendations for
everything; a personal taste fingerprint; an instance **gravity slider** that
lets a user bias results toward local theme or toward personal/mesh results; a
home feed of personalized recommendations, mesh trending, instance spotlight, new
curation quests, identification highlights, and awards.

Every entity page gains "similar to", "users like you also liked", "appears in",
"curated by", and "preservation status". Community-made discovery boards carry
lists, rankings, collections, and guides, and a **recommendation API** lets
developers build their own experiences on top.

### 7.5 Identification board — "Which Was That…?"

A dedicated community board for identifying actors, scenes, studios, sites, or
tags from half-remembered context. Users post a query with a description,
collage, snapshot, frame, quote, or context; the community suggests matches,
votes on candidates, and links solved queries to metadata. Solved
identifications become canonical links that improve search and recommendations.

Gamification: "Detective" reputation, solve streaks, badges, leaderboards, and
bounties for hard cases. It integrates with Elo, the directory, curation quests,
and preservation — identifying an orphan scene can trigger metadata creation
and replication. The board turns collective memory into structured archive data.

### 7.6 Trust and access model

Stash Box separates **metadata** from **media** across six levels:

|| Level | Grants |
||---|---|
|| 0 — Public / Anonymous | Browse directory, rankings, reviews, basic metadata, snapshot collages |
|| 1 — Registered | Vote in Elo matchups, submit edits, write reviews, flag duplicates, post on the identification board |
|| 2 — Contributor | Trusted edits auto-approve; expanded collages and storyboards; XP and badges |
|| 3 — Curator | Merge duplicates, approve edits, manage tags, claim curation quests, full snapshot sets |
|| 4 — Archivist | Opt-in content viewing, streaming, download, upload; host replicas for the mesh |
|| 5 — Steward | Governance, API keys, awards voting, instance gravity tuning, preservation policy input |

Trust is earned through approved edits, peer verification, voting consistency,
identification solves, curation quests, and preservation contributions.
High-trust users **explicitly opt in** to view content. Low-trust users still get
enough visual context — collages, storyboards, snapshots — to identify and
curate accurately.

**Mapping onto the existing role enum** (`internal/models`, eight roles: `READ`,
`VOTE`, `EDIT`, `MODIFY`, `MODERATE`, `ADMIN`, `INVITE`, `MANAGE_INVITES`): the
fork's trust ladder must be layered **on top of** these rather than replacing
them, or it breaks the public GraphQL compatibility surface the Stash desktop
app depends on (constraint 3 in §4). Concretely: Level 0 = anonymous + `READ`; Level 1 =
`READ`+`VOTE`+`EDIT`; Levels 2–3 = `MODIFY`/`MODERATE`; Levels 4–5 = new
capabilities gated on an opt-in flag, not on new roles.

### 7.7 Curation engine and completion

Every entity gets a **completion score** from missing metadata, snapshot
coverage, performer links, studio links, tag coverage, source links, review
coverage, and duplicate confidence.

- **Curation quests:** "Add missing birthdates for 5 performers", "Link 10
  unlinked scenes", "Verify studio catalog", "Resolve duplicate suspicion".
- **Bounties:** high-impact gaps earn bonus XP; rare performers, lost studios,
  and obscure scenes become community missions.
- **Federated quests:** instances with similar taste run joint campaigns.
- **Duplicate detection and merge:** perceptual hashing, metadata similarity, and
  community voting together.
- **Multi-user verification:** important edits need several trusted confirmations.
- **Progress bars everywhere:** every entity shows completion and missing fields.
- **Preservation quests:** "This scene has only 2 replicas. Help it reach 3."

### 7.8 Snapshot collages and scene identification

Every scene generates a **snapshot collage** — a storyboard of 12–24 evenly
spaced frames, hover-scrubbable, with optional silent previews.

Low-trust users see collages to identify scenes, match metadata, detect
duplicates, and curate accurately; high-trust users who opt in unlock playback,
streaming, and download. Collages power curation: vote "match"/"mismatch", flag
wrong covers, add tags, link performers, suggest merges. **Collages are
replicated across the mesh even when full content is not**, so identification and
curation work everywhere. Every collage interaction feeds the completion engine
and earns XP.

### 7.9 Elo ranking and taste profiles

Pairwise Elo voting — Glicko-2 or TrueSkill — ranks performers, scenes, studios,
sites, tags, lists, and even instances.

Performer battles present two performers side by side ("who do you prefer?").
Leaderboards exist overall and by genre, era, studio, country, instance, and
personal taste. Each user's votes form a taste fingerprint; each instance has a
taste vector shaped by its community and gravity. Rankings decay so they reflect
current relevance, and federated rankings show local, peer-mesh, and global
views. Gamification adds streaks, badges, "Taste Maker" reputation, daily
matchups, weekly tournaments, and bracket challenges, feeding annual Stash Box
Awards determined by community votes plus Elo plus reviews.

### 7.10 Directory, sites, studios and reviews

A community-curated directory in the spirit of ThePornDude, but open,
scalable, federated, and integrated with metadata: site and network profiles
(URL, description, categories, pricing, payment methods, features, pros/cons,
alternatives), studio profiles (history, owned sites, roster, notable scenes,
completion score), performer profiles (bio, aliases, scene credits, official
links, Elo rank, reviews), and structured user reviews with verified-usage flags.
Studios and performers can claim and confirm profiles to earn verified badges.
Search and filters cover niche, price, rating, features, ethical labels, and
payment methods; users publish lists. An **SEO engine** emits public pages for
every site, performer, studio, scene, and tag to bring mainstream traffic into
the mesh.

### 7.11 Ecosystem

Stash App integration (two-way sync: pull metadata, push edits, see rankings,
launch playback, contribute to preservation), a public API (GraphQL and REST,
SDKs for Python/JavaScript/Go, webhooks), a browser extension (overlay scores
and reviews on any site, one-click "Add to Stash Box", vote without leaving the
page), a mobile app (vote, quests, reviews, identification, notifications), a
developer sandbox, and a published **federation protocol** covering instance
peering, taste sharing, replication coordination, and cross-instance discovery.

### 7.12 Gamification and community

XP, levels, badges, and streaks for every contribution; leaderboards for top
curators, voters, reviewers, identifiers, and preservationists (local, mesh, and
global); guilds for niche- or studio-specific teams; "adopt a site/performer"
stewardship; mentorship; a public roadmap voted on by the community; annual
awards; and preservation badges for hosting replicas and seeding rare content.

### 7.13 Governance and operator controls

Each operator controls instance theme and gravity, trust thresholds for content
access, replication policy (minimum replicas, opt-outs, preferred peers, storage
budgets), federation agreements (which instances to peer with, which taste
profiles to trust), content access rules and opt-in requirements, local
moderation and curation priorities, and delegation of gravity tuning and policy
input to a high-trust subset. Users can move their taste fingerprint between
instances and see the mesh through different lenses.

### 7.14 The flywheel

More discovery → more users → more curation → better metadata → better
recommendations → more trust → more content access → more preservation → more
availability → more discovery. The mesh makes the archive resilient, personal,
and alive; every instance contributes to the whole and every user helps complete
the archive.

### 7.15 MVP roadmap

|| Phase | Scope |
||---|---|
|| 1 | Single-instance public metadata portal + snapshot collages + Elo voting + identification board |
|| 2 | Trust levels + opt-in content viewing + gamification + curation quests + completion scores |
|| 3 | Directory for sites/studios + user reviews + Stash app integration + public API + browser extension |
|| 4 | Federation protocol + taste-based peering + preservation replication + cross-instance discovery |
|| 5 | Mobile app + annual awards + advanced recommendation engine + mesh-wide curation campaigns |

### 7.17 Mesh architecture — roles, attestations, and peering (amended 2026-09-29)

Added from the owner's rewritten specification. §7.1–§7.16 are the earlier
revision of this vision and are unchanged; this section and §7.18 add to them
rather than restating them. The intake review, including every rejection and the
reason for it, is `docs/track/INTAKE-2026-09-29-federated-mesh-v2.md`.

**Two planes, and the draft's own text is what separates them.** Section 4.5 of
the *proposal* (not of this spec — this spec has no such section) says metadata syncs
over onion routing and that "full content never moves over onion routing — it
moves over the P2P layer." That is the proposal telling us
the two planes are separable, so the spec separates them: a **metadata plane**
(signed records, attestations, capability and taste profiles) and a **content
plane** (replication, storage, transfer). They have different trust models, different
anonymity properties, and different failure modes, and one implementation of each.

#### 7.17.1 Instance roles

A node may hold several roles. Roles are **capabilities this node offers**, not
permissions it claims, and a node's role never changes what it may read from the
metadata plane.

| Role | Offers | Notes |
|---|---|---|
| Hub | high uptime, large storage | operator- or community-funded |
| Community | themed archive (vintage, VR, indie) | moderate storage |
| Personal | a user's own node | contributes when it chooses |
| Read-only mirror | replicates metadata and snapshots | **never** accepts writes, never hosts content |
| Relay | metadata only | routing and sync, no content |

The read-only distinction is enforced by capability, not by trust: a mirror holds
no write credential, so "accepts no writes" is a property of its key rather than a
policy it could be asked to break.

#### 7.17.2 Peer reputation keys and signed attestations

Every user holds a **reputation keypair** signed by their home instance. Reputation
travels as a signed attestation: *"instance A attests that user U is level 4, has
contribution score 12,340, and holds active vanguard status."*

- An instance may **accept, weight, or reject** an attestation according to its
  trust in the attesting instance. Weighting is the mesh's only trust-inheritance
  mechanism, and it is per-instance policy.
- **Attestation weight decays** and requires re-signing, so a lapsed contributor's
  standing does not persist indefinitely.
- **Disagreement is a first-class outcome.** When instances disagree about an
  attestation, it is arbitrated by a steward — the same re-route §7.18 uses for
  metadata conflicts. The proposal does not specify this case and it is the case
  that decides whether the mechanism is safe, so the spec does.
- Sybil resistance: an attestation chain is only as strong as the weakest link the
  receiving instance chooses to honour, which is why honouring is **policy,
  per-receiver, and never automatic**.

**The attested level is a claim, never a copy — and the distinction is the whole
safety property of the mechanism.** An attestation says *"I believe this user is
level 4"*, not *"this user is level 4"*. A receiving instance that treats an
attestation as authoritative is running someone else's trust model with someone
else's thresholds, and a compromised or merely wrong home instance becomes a
privilege-escalation vector against every peer that believed it.

This is the same rule §3.3 already states for edit votes — **recomputed from rows,
never tallied into a column** — applied to a value that crosses an instance
boundary. Migration 76's `user_trust.level` is already a *cache with a defined
rebuild path* (`RebuildLevels`), not a source of truth, and an attestation carries
the recomputed claim plus the rows needed to re-derive it. A peer that stores the
level without the events has adopted a second source of truth it cannot audit.

#### 7.17.3 Peering and peering tiers

Instances peer on taste-vector similarity (cosine above an operator-set threshold),
capability compatibility, and mutual attestation. Operators may whitelist,
blacklist, or re-weight. Peer relationships are **signed and revocable by either
side at any time**.

| Tier | Metadata | Snapshots | Content | Quests | Ranked discovery |
|---|---|---|---|---|---|
| Full peer | yes | yes | yes | yes | yes |
| Metadata peer | yes | yes | no | yes | yes |
| Discovery peer | read | no | no | no | yes |
| Relay peer | no | no | no | no | no |

Tier is a property of the **signed relationship**, negotiated and recorded, not a
per-request decision — so a downgrade takes effect once and is auditable, and a
peer cannot be re-classified by a third instance.

#### 7.17.4 Onion-routed metadata sync

Metadata snapshots sync between peers over onion routing (Tor, I2P, or a
mesh-specific onion protocol) so peers do not learn each other's addresses. What
crosses the wire is **only**: signed metadata records, snapshot collage hashes,
reputation attestations, and peer capability/taste profiles. **Full content never
traverses this path** — it uses the content plane.

Cadence is configurable: hourly diffs by default, daily full reconciliation. An
instance may be **air-gapped**, syncing via signed export/import bundles instead;
an air-gapped instance is a supported topology, not a degraded one.

### 7.18 Federated curation, conflict resolution, and storage allocation (amended 2026-09-29)

#### 7.18.1 Change propagation and conflict resolution

Metadata changes are signed by the originating user's reputation key and then by
the instance. Peers apply them by rule:

- From a **high-trust user on a trusted peer** — applied immediately.
- From a **new user or an untrusted peer** — held in a **quorum queue** until a
  per-instance rule is satisfied (e.g. three trusted peers confirm).

Conflicts resolve in this order, and the order is the specification: **recency plus
trust weight**, then **community vote**, then **steward arbitration**, then
**fork**. Forking is legitimate and permanent: if two versions of truth are needed,
both are kept and shown with a **divergence marker**, and users can see which
instances hold which version. A fork is not a failure to be resolved later; it is
the outcome when resolution is genuinely ambiguous, and hiding it behind a last
write wins is the thing this ordering exists to prevent.

#### 7.18.2 Preservation alerts

A replica carries a **manifest hash** and a content hash. Peers verify each
other's replicas by random challenge; a failed verification triggers re-download
from a healthy peer. **If every replica of a scene disappears, the mesh raises a
preservation alert** and promotes that scene to the top of every matching
instance's allocation queue. This is the same scene §7.3 promises to restore — the
alert is what makes "if an instance goes offline" cover the stronger case of *all*
of them going offline.

#### 7.18.3 Storage allocation

When an instance has free storage it fills it with content its users would enjoy
most, scored by: instance taste vector, vanguard taste vectors (weighted highest),
high-trust user vectors (medium), general user vector (low), peer demand signals,
preservation urgency (rare content, endangered replicas), and curation completion
score — **more complete metadata is preferred, because an archived scene nobody
can search for has been preserved in the least useful sense.**

An **allocation log** is kept so operators can audit and tune what was placed and
why. Users may submit allocation preferences, which enter the scoring as an input
rather than an override.

#### 7.18.4 Auto-archiving by cross-instance enjoyment

Content that draws enjoyment signals on **at least two instances** becomes eligible
for auto-archiving by any peer with capacity and matching taste gravity. Signals
are: opt-in viewing sessions above a threshold, high vanguard/high-trust review
scores, Elo percentile, curation intensity, and repeated peer requests. Operators
may raise or lower the threshold or exclude tags.

Auto-archived content is **replicated, verified, and manifest-registered, never
silently mutated.** The threshold is an *instance default overridable in both
directions* — this is a starting policy, not a floor.

#### 7.18.5 Local-first and anonymity

A user may run a personal node with a full metadata database, a local library, and
optional federation, and may contribute storage to the mesh for reputation. Users
choose an anonymity level — **fully anonymous** (onion-routed metadata, no account),
**pseudonymous** (account plus signed reputation key), or **open**. The default for
a new user is pseudonymous with no content sharing until opt-in.

### 7.19 The P2P content layer — REJECTED, with the mechanism re-routed

Recorded as a rejection because a future draft will re-propose it, and because the
reasoning is the load-bearing part.

The proposal's §5.1 asks for BitTorrent, WebTorrent, DHT (mainline and custom),
eDonkey2000, Kad, IPFS, and a custom swarm protocol. **Not adopted as a product
surface.** Three independent grounds:

1. **It is a different product.** A swarm client has no metadata, no GraphQL, and
   none of the curation or trust machinery that is this repository's entire point.
   The proposal itself draws the seam (its section 4.5), and adopting its section 5 wholesale
   would build a filesharing daemon that a GraphQL server has to babysit.
2. **Licence exposure is unresolved.** The eMule-lineage reference implementations
   are GPL. This fork is MIT (constraint 2 in §4), and linking or embedding them is
   a distribution event. Adopting the vocabulary now puts a known-unanswered
   question inside the spec where it is inherited silently.
3. **It adds no GraphQL surface at all** — the first proposed subsystem with none.
   Constraint 3 in §4 ("Constraints a fork inherits") makes the GraphQL schema a
   public compatibility surface for the Stash desktop app, and a subsystem
   invisible to it cannot be governed by it.

**What is adopted instead:** the content plane is a single **instance-to-instance**
replication protocol (§7.18.2, §7.18.3), spec-agnostic and replacing whatever
transport is configured. If a public swarm protocol is ever wanted, it plugs in
behind the content plane's interface and adds no spec change — which is the test
for whether that addition was the right shape.

### 7.20 Vanguard — the draft's influence grant, corrected

The proposal grants vanguards "weighted influence on gravity tuning." **Adopted
except that clause**, and the correction is deliberate.

A vanguard is a user whose taste vector has high **resonance** with the instance's
gravity, who holds high trust, and who has a high contribution score. Resonance is
continuous and adoption-weighted, and **vanguard status is dynamic** — earned, lost,
and regained. Operators may appoint manually or let the algorithm decide (default:
algorithm, with manual override).

Vanguards receive early feature access, priority in curation quests, storage
allocation influence, awards nomination rights, and a visible badge. **They do not
get a weighted vote on instance gravity.** Gravity is an operator control, listed as
one in §7.13, and the proposal's own §2.2 provides the sanctioned influence path
when it says operators may appoint vanguards manually. Weighting gravity by user
resonance would let a small high-trust group steer every user's recommendations for
the whole instance — a governance change wearing a gamification costume. Priority
and nomination are influence; a vote on the theme is control.

### 7.21 Ecosystem delta

The only ecosystem change is a widened SDK list: **Rust** joins Python, JavaScript,
and Go (§7.11). Developer sandbox, webhooks, browser extension, and mobile app are
already specified in §7.11 and are unchanged.

### 7.22 §7.16 corrected — four of its five "No" rows are now stale

§7.16 was accurate when written and is **wrong now**, because this fork has since
built four of the five capabilities it declared absent. Correcting it here rather
than editing §7.16 in place, so the correction and its date are both visible.

|| Vision item | §7.16 said | Now |
||---|---|---|
|| Elo / Glicko ranking | No | **built** — `internal/service/elo`, Glicko-2 |
|| Snapshot collages | No | **built** — `internal/service/collage` |
|| Identification board | No | **built** — migration 79, `internal/service/identification` |
|| Completion scores | No | **built** — `internal/service/completion`, GraphQL-exposed |
|| Federation / replication | No | **still no** — and §7.17/§7.18 now say what it takes |

§7.16's reasoning about the existing `edit`/`edit_votes` machinery still holds and
is worth keeping: consensus on facts is a different problem from pairwise
preference ranking, and the Elo implementation is deliberately not built on the
edit system.

### 7.16 What the vision needs from the existing codebase

The three capabilities the fork must *build* rather than extend, because nothing
like them exists here today — verified by search, not assumed:

|| Vision item | Present in this repo? |
||---|---|
|| Elo / Glicko / TrueSkill ranking | **No.** No rating code anywhere outside test fixtures. The existing `edit`/`edit_votes` machinery is *consensus on facts*, which is a different problem from *pairwise preference ranking*. |
|| Snapshot collages / storyboards | **No.** Images exist as opaque `width`/`height` metadata; there is no frame-extraction, scrub, or storyboard concept. |
|| Identification board | **No.** Nothing in the schema, service layer, or `frontend/src/pages/` (18 page dirs, none of them an ID board). |
|| Completion scores | **No.** No per-entity completeness notion. |
|| Federation / replication | **No.** Single-instance only. |
|| Reviews, directories, XP/badges | **No.** |
|| Edit/vote consensus | **Yes** — `internal/service/edit`, 4 480 lines. The nearest reusable primitive. |
|| Perceptual hashing / fingerprinting | **Yes** — `internal/service/fingerprint`, and pHash clusters already exist (see issues #814, #1177). The substrate for duplicate detection (§7.7). |

---

## 8. Defects observed while reading (candidates, not findings)

Recorded because they were seen in passing and would be expensive to rediscover.
None is diagnosed to root cause; none is fixed.

1. **`internal/service/edit/modbot.go` — unsynchronised package-level cache.**
   `var modUserID *uuid.UUID` is lazily populated by `getModBot` with a plain
   `if modUserID == nil` read/write, no mutex, no `sync.Once`. Concurrent first
   use from two goroutines is a data race. Benign in practice (both write the
   same value, and the value is derived from a system user) but it is a real
   race the Go race detector would flag, and a fork that adds concurrency
   around edit application inherits it.

2. **`README.md:183` — stale sqlite3 claim.** See §2.5. One-line fix, and a
   genuinely useful first contribution to the fork.

3. **README/CLAUDE.md role list omits MODERATE, INVITE, MANAGE_INVITES.**
   Eight roles ship; five are documented.

4. **`VoteCount` drift hazard.** `Passing()` (line 1306) reads the stored
   `edit.VoteCount` while `decideEdit` recomputes from `edit_votes`. Two sources
   for the same fact. Worth auditing whether they can disagree, and whether
   `VoteCount` is ever authoritative.

5. **pnpm version drift.** `package.json` pins `packageManager` to
   `pnpm@11.21.0`; host has 12.4.1. Low severity, but codegen and lockfile
   resolution should use the pinned version.

6. **A piped `go build` reports the exit code of the pipe's LAST command, not
   the build's.** A verification harness written as

   ```bash
   go build ./... 2>&1 | tail -20; echo "BUILD_EXIT=$?"
   ```

   prints `BUILD_EXIT=0` on a build that failed, because `$?` is `tail`'s status.
   Measured on this tree: the same build gives `0` via `$?` and `1` via
   `${PIPESTATUS[0]}`. It is easy to mistake for a passing build in a scrollback
   and conclude §2.2 is wrong. Any command here that pipes a build or test into
   a pager must use `${PIPESTATUS[0]}`, or run unpiped.

---

## 9. Verification commands

Every claim in §2 is reproducible from the clone. The two that must be re-run
after the §2.4/§2.6 blockers clear:

```bash
cd ~/code-local/go/stash-box

# 1. module loads (expected: 41)
mkdir -p frontend/build && echo placeholder > frontend/build/index.html
go list ./... | wc -l

# 2. unit suite green (expected: 7x "ok")
go test $(go list ./... | grep -vE 'internal/image$')

# 3. codegen reproducible (expected: empty output)
go tool sqlc generate && go tool gqlgen generate
git status --porcelain

# 4. integration suite — BLOCKED until OpenEXR 3.5 is installed
POSTGRES_DB="postgres://postgres:<pw>@127.0.0.1:5432/stash-box-test?sslmode=disable" \
  go test -tags=integration -count=1 ./internal/api/

# 5. linter — BLOCKED, golangci-lint not installed
make lint
```

`make it` already passes `-count=1`; `-count=1` is not optional for a fork
where a cached `ok` proves nothing about a changed tree.
