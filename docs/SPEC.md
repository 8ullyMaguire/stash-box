# stash-box fork — SPEC

**Status:** draft v0.3 — foundation, direction resolved (§6.1), vision amended (§7.17–§7.22, §7.24)
**Repo:** `~/code-local/go/stash-box` (clone of `github.com/stashapp/stash-box`)
**Pinned upstream:** `b4b8aef2` ("Use setup-go native cache (#1250)", 2026-09-09)
**Written:** 2026-09-28 · **Amended:** 2026-09-29 (§7.17–§7.22, mesh architecture) · 2026-10-01 (§7.24, curation completeness and preservation)

Every fact in §2 was measured on this host on the clone, not taken from the
README or `CLAUDE.md`. Where those two disagree with measurement, measurement
wins and the disagreement is recorded — two of their claims are false (§2.5).

---

## 0. Scope policy — what this fork takes from upstream, and what it does not

**Recorded 2026-09-30, and it should have been here from the start.**

`docs/ISSUES.md` dispositioned 138 upstream issues as `feature-request`, every one
of them with the same reason:

> upstream feature request; the fork's policy is upstream bug fixes and
> spec-driven work, and no spec row asks for this

That policy was true, and it was asserted 138 times without appearing in a single
document. A rule that exists only inside the reason column is not a rule a reader
can check their disagreement against — and the goal-checker caught exactly that:
its `C2 reasons present` clause fails when more than a dozen rows share one
identical reason, because bulk reclassification produces exactly that signature and
138 real decisions do not. The clause was right and the document was missing.

### The policy

This fork takes from upstream:

1. **Upstream bug fixes.** A PR that fixes something upstream considers broken is
   in scope, because the bug is real regardless of who discovered it.
2. **Upstream security fixes.** Non-negotiable, and they skip the normal gates:
   take them even when the patch is invasive.
3. **Work this fork's own spec asks for.** If `docs/plan/` names it, it is in scope
   whatever upstream thinks of it — that is what a spec is for.

And does not take:

4. **Upstream feature requests.** These are product decisions belonging to the
   upstream maintainers. Taking them would make this fork track upstream's roadmap
   rather than its own, at the cost of every merge conflict those features create.
   Where a feature request overlaps something this fork is already building, the
   fork's own spec wins and the upstream PR is ported on the fork's terms.

### What this means for a reader

If you think a `feature-request` row above was mis-dispositioned, the question to
ask is whether **your** spec row asks for it. If you want it, add the spec row
first — that is the whole point of the ordering, and it turns a policy dispute
into a one-line change. Do not add the feature by way of the issue ledger.

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

> The following block is §7.23, inserted before the §7.16 that follows it. It
> sits here rather than at end-of-file because §7.16 through §7.22 form one
> contiguous amendment series and §7.23 amends that series; §7.16's position
> after §7.22 is pre-existing and is not reordered.

---

### 7.23 Second intake amendment — what the 2026-09-29 rewrite adds (amended 2026-09-29)

The owner's specification was re-pasted in fuller form. Most of it is the
revision already captured in §7.17–§7.22; this section records only the
**eight deltas that are new**, and the four places where the draft would be
**wrong as written** if implemented literally.

Verified against the tree rather than assumed; each "already present" claim
below was checked by search on 2026-09-29.

#### 7.23.1 Adopted — genuinely new

| # | Delta | Note |
|---|---|---|
| D1 | **Vanguard trust-weighted voting.** Elo votes weight by voter trust, vanguard status, and contribution score. | Not a new mechanism — a weight column on a vote row. Fits `elo`. |
| D2 | **Identification board federates.** A query broadcasts to peers with matching taste vectors. | Metadata plane only (§7.17.1). Content never broadcasts. |
| D3 | **Gravity slider ranks by an explicit formula**: local gravity × peer similarity × personal taste × trust weight. | The product from §7.4 becomes a computable expression. |
| D4 | **Content access is a conjunction of five conditions**, not a level check. | The one that changes how the gate is written — see §7.23.3. |
| D5 | **Access can be restricted by tag, studio, performer, region, time window, device class.** | A denylist evaluated after the five conditions, never instead of them. |
| D6 | **Guilds, mentorship, adoption, public roadmap, voting.** | Community layer. No ranking effect on the mesh. |
| D7 | **Mobile app and browser extension are named first-class deliverables.** | Already in §7.11; promoted from implied to explicit. |
| D8 | **Sync cadence and air-gap bundles are operator-configurable.** | Already in §7.17.4; this paste states it more firmly. |

#### 7.23.1a Implementation status, re-measured 2026-09-30 22:10

**D2 IS COMPLETE and is marked implemented.** All six steps are in one branch,
mutation-tested, with build/vet/gofmt clean and the suite green at the commit that
records it.

The condition that held this open for two sessions — "step 5's client is an
untracked file in another session's worktree" — was **never a real dependency.**
The file compiles clean in this branch, so it was brought over (`9e794f9c`). The
authoring session turned out to have been idle for seven hours: the "blocked on
another session" was inferred from a file's mtime and never tested against
`/proc`. Recorded here because SPEC is what the next session reads first, and a
false blocker in it is expensive.

**Start at `docs/track/HANDOFF-R074.md`.** It carries the environment block, every
gate with its output, the four things to know before touching this code, what was
deliberately *not* done and why, and the traps paid for as rules. The mutation
obligation is committed beside it at `docs/track/mutations/` — 85 mutations across
ten specs, runnable — so the claims here can be re-verified rather than trusted.

**D2 is on `master`, green and pushed** (2026-09-30 23:05, `422a2f28`): 28 commits
fast-forwarded from `d3934900`, then `make it` run **in `master` itself** rather
than in the worktree it was built in — 30 packages, no failures. The one line still
outstanding in that handoff is `git stash drop`, which is optional.

The verdict sat unreadable while the blocker was real, which was the right call
then and is worth recording now: a row saying "done" against a half-landed branch
is the kind of claim this repo keeps retracting. What changed is that the branch
is no longer half-landed.

| D2 step | State | Commit |
|---|---|---|
| 1 — peer registry + foreign evidence tables | done | `c4f8fcdc` |
| 2 — freshness rule | done | `1d64e70a` |
| 3 — taste-based peer selection | done | `f5601107` |
| 4 — broadcast payload + F1 content guard | done | `7704600c` |
| 5 — client + local storage of answers | **DONE** — store `d3934900`, F2 test `f07cc48d`, client + dial-time guard `9e794f9c` | see commits |
| 6 — wiring to a resolution + operator surface | **DONE, both halves** — write path `e2ee78b1`/`8338a797`, read path `federation_foreign_candidates` | see commits |

**D2 IS NOW COMPLETE, and the blocker that held it for two sessions is gone.**

The blocker was always described as "step 5's `client.go` is an untracked file in
another session's worktree, so the branch cannot be merged". That was half the
truth. The file **compiles clean in this branch**, so it could simply be brought
over — `9e794f9c` — which resolves the ordering instead of waiting on it. The
shared tree was never touched; the work moved.

That move immediately exposed a defect that could not have been found before,
because the file had never been compiled by anything:

```go
if err := error(nil); err != nil {   // always false
```

**The F1 content guard had never run.** Any question carrying a path, a URL or an
internal hostname was broadcast to every askable peer. Its own regression test
lived in the same untracked file, so the defect and the test that catches it were
in one place and neither was in the build.

*An untracked file in another worktree is unreachable from any import — so a test
that has never been compiled cannot catch anything, and neither can the guard it
was written for.*

### Against D2's own definition of done

Six items, checked rather than asserted:

| Requirement | State |
|---|---|
| `go build ./...`, `go vet ./...` clean | **met** |
| `go test ./...` green, no test deleted or weakened | **met** — 30 packages. 21 full-suite runs; runs 15–16 were the notification failures described below, fixed rather than retried away |
| Every mutation in the plan killed, or the missing test written | **met** — **89 killed across ten harnesses** (13 guard, 7 wiring, 11 #708, 11 surface, 8 F2, 7 dial-time, 9 image url, 8 foreign-candidates, 3 issue-9, 8 client). Every survivor was a TEST defect or DEAD CODE, never an unfixed hole — see the records below, and the two survivors in `9e794f9c` that were real gaps in the tests |
| Broadcast payload has no field capable of holding media, proven by reflection | **met** — `TestWireFieldTypesAreClosed`, `TestWireHasNoURLOrPathField` (`7704600c`) |
| A foreign candidate provably cannot reach the local vote path, **proven by a test that attempts it and expects a rejection** | **met, and it was NOT before** — see below |
| SPEC §7.23 D2 marked implemented; D6's deferral recorded | **MET** — this section is that marking, and D6's deferral is recorded below. All six steps are in ONE branch, build/vet/gofmt clean, suite green |

### Two survivor records, because both point the same way

Across nine mutation harnesses this branch has produced **28 survivors**, and not
one of them was a hole in the shipped code. Every one was a defect in a test or in
the guard's own shape. Three are worth naming because they are the kind of mistake
that produces a green suite and no protection:

**1. A test of a copy.** The dial-time tests originally called `ValidateBaseURL`
through a local helper that reimplemented the rule, so two mutations that gutted
`DialGuard` itself were invisible. The function the client actually calls was
untested. Fixed by making the rule take an injected resolver and pointing the tests
at the real entry point.

**2. A resolver fake that quietly disables the rule under test.** The fake returned
one fixed *public* address for every host, so `http://127.0.0.1` resolved to a
public address and the guard allowed it — three subtests passed a loopback,
link-local and private-range URL. A fake that ignores the host is worse than no
fake. The existing `baseurl_test.go` fake was already sound because it has a
`perHost` map.

**3. A guard that reused the wrong predicate.** The image-url guard called
`IsSuspiciousValue`, the F1 *question-text* predicate, which rejects any value
containing `http://`. An image url is a url, so the guard refused
`https://cdn.example.com/pic.jpg` and every ordinary image on the column. The
marker LIST is shared; the PREDICATE is not.

And the inverse case, which is the rule the survivors keep teaching: when a
mutation survives because the guarded line is **redundant**, delete the line rather
than write a test to pin it. The image guard had three checks where one was doing
the job — `url.Parse` gives both an empty string and a relative url an empty
scheme, and the allowlist refuses it. Nine mutations now, and the three
over-strict ones (must not refuse a dotted host, must not use the question
predicate, must not reject a host that looks unreachable from here) are the ones
that protect existing user data.

**The F2 item was unmet until this session, and the reason is worth recording.**
`store.go` — the F2 boundary itself — was committed as `d3934900` with **no test
at all**. The two things that stood in for one both fall short of "attempts it
and expects a rejection": `TestRegistryCannotWriteForeignEvidence` asserts the
registry exposes no `Suggest`/`Vote`/`Resolve` method, which is a claim about
names and a rename would defeat it; and migration 89's comments argue the case,
which is a claim about the schema and not the schema.

`f2_boundary_integration_test.go` now attempts the crossing for real: a peer's id
is spelled as an existing local performer's uuid, recorded as an answer, and the
test shows no local performer was created or attached, that
`identification_foreign_candidates` has no column capable of pointing at a local
entity, and that the peer's 500 suggestions bought no local weight. 8 mutations,
8 killed, including adding the `entity_id` column and resolving a remote id
against a real performer.

Two of those 8 survived the first version of the test, and both were the test's
fault: `assert.Error` was satisfied by the **foreign key** rejecting `uuid.Nil`
once the guard was removed, so the test passed while the guard was gone. The
assertion is now on the guard's own error message, which is what distinguishes
"refused deliberately" from "refused by the database by accident".

**D6 is deferred, with its reason.** Guilds, mentorship, adoption, a public
roadmap and voting are a community layer, and nothing in the identification
federation work needs them. Deferring D6 costs D2 nothing, and building D6 before
D2's read path exists would mean a ranking and gamification surface over evidence
no operator can yet query. It stays deferred until D2 step 6 is closed.

**R074's receiving guard is built, wired and REACHABLE** (`r074-receiving-guard`).
`internal/service/federation/baseurl.go` validates `federation_peers.base_url`,
`service.go` calls it from both `Create` and `Update`, `schema_scan.go` watches
the seven address-shaped columns, and the operator surface calls those
(`e2ee78b1`, `8338a797`). An integration test asserts the rule in operator terms:
an ADMIN cannot register or repoint a peer at `169.254.169.254`. Full note:
`docs/track/HANDOFF-R074.md`.

#### 7.23.2 Already present — recorded so the plan does not rebuild them

The draft re-proposes these as if new. They exist; building them again would
be a fork's first real mistake.

| Draft item | Reality in this tree |
|---|---|
| Perceptual hashing, duplicate clustering | **built** — `internal/service/fingerprint`, `cluster.go`, oshash |
| Duplicate detection, multi-user merge | **built** — `internal/service/fingerprint/cluster.go`; issue #943 merge/redirect path already works |
| Merge history for audit | **built** — `mod_audit` table + `internal/service/mod_audit`; `performer_redirects` retains the mapping |
| Trust levels, opt-in content flags | **built** — `internal/service/trust`; `user_trust.level` is a rebuildable cache (§7.17.2) |
| Directory, site profiles, reviews | **built** — `internal/service/site`, `internal/service/review` |
| Completion scores, curation quests | **built** — `internal/service/completion`, `internal/service/quest` |
| Identification board | **built** — `internal/service/identification` |
| Collages | **built** — `internal/service/collage` |
| Elo / Glicko | **built** — `internal/service/elo` |

#### 7.23.3 The draft is wrong in four places

**W1 — "Trust level ≥ 4 **or** vanguard **or** selected by admin" makes the
gate a disjunction.** The draft's own §2.3 lists five conditions and then opens
with an *or*, so a vanguard passes the trust check regardless of their
contribution score or whether they accepted the content terms. A disjunction
means the weakest of five controls is the actual control. **Adopted as a
conjunction**: all five must hold, and the vanguard exemption exists only as an
alternative to the *level* check, never as a bypass of the opt-in flag, the
contribution threshold, or the terms acceptance. Every extra exemption the draft
adds after the list is a hole, and each is recorded below with the reason it
would be a hole.

**W2 — "Restrict access by geographic region" is untestable as written.** A
box has no reliable client geography; every HTTP client presents whatever its
proxy says, so a region rule keyed on IP or `X-Forwarded-For` is trivially
spoofed and gives the appearance of a control. **Adopted as a policy input to
be passed to the CDN/proxy in front of the instance**, where it is enforceable,
rather than an application-level check. If the instance has no such proxy, the
rule is reported as unenforced rather than silently ignored.

**W3 — "Require multi-factor identity binding" is specified at the wrong
layer.** Binding an identity to a person is not something a metadata server
does; it is what the instance's authentication provider does. **Adopted as a
requirement on the instance's auth configuration**, recorded and displayed, not
implemented as a code path in this repository.

**W4 — "Device class" is a client claim, not a server fact.** Same failure as
W2: a header the client sets. **Adopted as a soft signal** — a factor in an
anomaly score, never a hard gate on its own, because a hard gate on a
client-supplied value is a check the client controls.

The common thread in W2–W4: **a control keyed on a value the client supplies
is not a control.** That is the review rule for every access rule this fork
accepts, and it is why the content plane (§7.19) does not trust client-supplied
provenance either.

#### 7.23.4 What did not change

§7.19's rejection of the P2P content layer **stands and is now firmer**, because
this paste re-proposes it at greater length (its §5.1, §5.2, §5.3, §5.4) and the
additional grounds are the same: no GraphQL surface, unresolved licence
exposure, and a different product. The re-proposal is recorded as a
re-proposal, not as a new decision, and §7.19's re-routed content plane is
unchanged.

§7.20's rejection of the vanguard gravity vote **stands.** This paste grants
vanguards "weighted influence on gravity tuning" again, verbatim. The reason in
§7.20 is unchanged and is not a reading of the draft: gravity is an operator
control, priority and nomination are influence, a vote on the theme is control.


### 7.24 Third intake amendment — curation completeness, archival, preservation (amended 2026-10-01)

An AI-generated ranking of 31 completeness/archival/preservation ideas arrived
titled "ranked by impact ÷ effort", self-described as unverified against the tree.
It was verified here. Verdicts, adaptations and rejections with reasons are in
`docs/track/INTAKE-2026-10-01-curation-completeness.md`; this section is what
adopted.

**Two findings reframed the whole paste before any of it was adopted.**

**F1 — the premise is stale.** The draft ranks a bounty board #4 on the grounds
that "the spec has them only as a one-liner". `80_add_authored_quests` and
`81_add_bonus_points` already exist: `authored_quests` carries a bounded
`bounty_points` (`0..10000`) plus a `reason` and an `authored_by`, and quest
completion pays bonus XP. The pricing layer is largely present.

**F2 — the top-ranked new idea is forbidden by the code's own comment.** The
draft proposes auto-generating bounties from completion gaps, priced by marginal
gain × rarity × age. `80_add_authored_quests` says, in the schema comment:

> A BOUNTY is an operator decision -- "this gap is worth triple" -- and a promise
> made by a person. A generated quest must not be able to manufacture one, so a
> bounty lives only on an AUTHORED quest, and the generator never reads this
> table.

That is a decision with a stated reason — **a bounty is a promise, so it needs a
promisor** — and the draft's version deletes the promisor. Rejected as written;
the formula is adopted in 7.24.3 as a *suggested price shown to the author*.

#### 7.24.1 Verified-unknown markers — the prerequisite for everything else here

**The highest-value item in the paste, and adopted as the prerequisite it is.**
Nothing in 95 migrations can express "this field is confirmed-absent", so
completion can never legitimately reach 100%, gaps that are unanswerable get
bountied forever, and quests recycle.

Adapted deliberately:

- **It is an edit, not a moderator field.** It attaches to the existing
  edit/consensus machinery (`internal/service/edit`), not a parallel write path.
  A curator with trust earns auto-approval; "not publicly knowable" is a low-risk,
  high-value edit that should be earnable by exactly that logic. Making it a
  moderator-only field would hollow out §7.6's trust levels and §7.7's reputation.
- **It carries a reason code, not free text.** "Birthdate is not publicly
  knowable" and "birthdate exists but nobody has looked" are different facts, and
  the second must stay farmable. A free-text reason collapses them.
- **No XP for asserting one on an entity you just edited.** Otherwise
  verified-unknown becomes the cheapest XP-per-minute farm in the system — the
  same shape §7.20 refused for vanguards, applied here for the same reason.

#### 7.24.2 Expected-total denominators

Adopted. §7.7 computes a completion score from missing metadata; a score without a
denominator is a ratio with no meaning, and "the studio's site lists 412 scenes"
is the only thing that turns "catalog the studio" into a countable target.

**Sourced, trusted-contributor-or-moderator only.** A denominator is a claim about
the world, so it carries `source_id`. An unsourced total is a rumour that silently
deflates every completion score on the instance — worse than having no total,
because the damage is invisible.

#### 7.24.3 Bounty pricing — re-routed to the authored model

The draft's five pricing traps are adopted; four of the five are already satisfied
by migration 80 and are verified rather than re-adopted.

| Trap | State | Evidence |
|---|---|---|
| Pay on edit applied, not on submit | already true | 80: the item leaves the quest "when the underlying field is actually filled, not when the claim expires" |
| Claw back if later reverted | partly true | `KindQuestCompleted` bonus is the mechanism; explicit claw-back is not |
| No payout for confirming your own edit | **new rule** | absent today |
| Diminishing returns per entity | **new rule** | absent today |
| XP/reputation only, never money | already true | `bounty_points` is `INTEGER`; no currency column exists anywhere |

**The re-route:** an operator — or a curator whose trust level permits authoring —
authors the quest, and the formula computes a *suggested* price the author may
override. The generator never reads `authored_quests`. 7.24.3 therefore adds
pricing assistance to the authoring surface and **does not** add a bounty
generator.

#### 7.24.4 Data-lint quests — the cheapest real work-item generator

Adopted, and it needs no new subsystem. §7.7 already promises these in spirit
("Resolve duplicate suspicion", "Link 10 unlinked scenes"); this names the source.

Each detector is a **named SQL query emitting quest candidates**, against tables
that already exist: `scene_urls` / `performer_urls` / `sites.url` for the duplicate
-URL detector; `studios` + `scenes` for scene-dated-before-studio; alias
collisions against the performer alias table; conflicting tags; covers below a
minimum resolution.

**Alias collisions emit quests and never auto-merge.** The merge path is the
existing consensus flow in `internal/service/edit`, which already writes all four
redirect tables (7.24.1's sibling — see the audit row below).

The same SQL doubles as a **validation pass over imports**, which is the more
valuable half of this item and is recorded in the plan phase.

#### 7.24.5 Unmatched-fingerprint demand board, and the holder lower bound

Adopted. `18_fingerprint_user` gives `scene_fingerprints.user_id` `NOT NULL` with
an index on `(user_id, algorithm, hash)`, so the aggregation exists;
`89_identification_federation` already provides the board to seed.

**The boundary, now load-bearing:** aggregate by **hash and count only** — never
store or expose the path, the user, or the scene. A miss is evidence that a copy
exists somewhere; the miss record must not become a pointer to it. This is §7A's
rule applied to a table that would otherwise be a quiet cross-instance locator.

**Holder count is a lower bound, never a count.** Distinct fingerprint submitters
bounds replicas from below, because one user holding three copies contributes the
same as one holding one. It is exposed **thresholded**, and the endangered-copy
notice (7.24.11, deferred) is built on it.

#### 7.24.6 Fingerprint corroboration as a completion factor

Adopted, and it is nearly free: a scene with one submission, or only one hash
algorithm, gets a "needs a second independent submission" flag. The data is already
there (`algorithm`, `user_id`, and the unique constraint on
`(scene_id, fingerprint_id, user_id)`). This is a query plus one factor in §7.7's
existing completion score — not a subsystem.

#### 7.24.7 Bulk vandalism rollback

Adopted. `mod_audit` exists and §7.13's operator surface already reads it, so this
is one audited action: revert every edit by one user, or since one checkpoint, in a
single transaction.

**The revert must write the same audit shape as a normal edit**, or the audit
trail acquires two formats and neither is readable.

#### 7.24.8 Completion-delta preview

Adopted. The edit form shows "this edit takes the scene from 62% to 71%", then
suggests the next-highest-value missing field for that entity. It is a read of
§7.7's existing completion score plus a ranking of missing fields by marginal
gain — not a second, competing definition of completion.

#### 7.24.9 Webhook events for bounties and preservation notices

Adopted as wiring. `84_add_webhooks` already has `webhook_endpoints` and
`webhook_deliveries` with `event_type` and a JSONB payload; this adds two event
types and nothing else.

#### 7.24.10 Review-queue bounties and easy-tier onboarding — one loop

Adopted together, because they are one loop: pending edits nobody votes on block
the multi-user verification §7.7 depends on. XP is paid for voting on the oldest
pending items, and the same board is the source of a "first five edits" funnel.

**The payout is for the vote, never for its outcome.** A reward keyed to the
outcome is a reward for a position, which is §7.20's refusal — the same rule, the
same reason, a third section.

#### 7.24.11 Preservation and archival — adopted, deferred to its phase

Recorded here so the ideas are not lost, and so their data dependencies are
specified before the phase starts:

- **Endangered-copy notice** — a scene with only one known holder tells that user
  "you may hold the only known copy; consider backing up". Metadata plane only;
  no path crosses the boundary (§7A). Built on 7.24.5's thresholded lower bound.
- **Wanted list** — scenes known to exist, from denominators (7.24.2) or
  fingerprint misses (7.24.5), with zero known holders. The "lost media" board.
- **Backup and restore drill** — `cmd/` contains only `sdbimport` and `stash-box`;
  **there is no backup command.** Adopted as a `backup` command producing a
  consistent DB + image-store snapshot with checksums, plus a CI job that restores
  it and runs the suite. This is the one item in the whole paste that should not
  wait for its phase: preserving the archive starts with preserving the instance,
  and the restore drill is what makes every other preservation claim testable.
- **Nightly signed metadata dump** — self-describing, versioned (JSONL + schema +
  hash list + README). The cold-storage artifact, the first draft of §7.17.4's
  air-gap bundle, and the seed for read-only mirrors.
- **Link-rot checker and archival submission** — periodically check
  `scene_urls`, `performer_urls` and `sites.url`, flag dead ones, optionally
  submit live ones to the Wayback Machine, store the archived link. Dead links
  become "find an archive" quests. **One correction to the draft:** it says to
  reuse "the webhook validator's resolve-then-check". The right thing is the
  discipline, not that function — `internal/webhook/target.go` and
  `internal/service/federation/baseurl.go` hold the SSRF resolution, and the
  checker is itself an outbound fetch, so it must resolve-then-check exactly as
  they do (§7A.3 rule 2). It is also operator-opt-in, because it submits URLs to
  an external service.
- **Release variants and reference copy** — encodes of one scene as variants, with
  the highest-quality known one marked. One addition the draft omits: the variant
  row needs a **re-checkable fingerprint set**, not a boolean, or "diverging
  hashes reveal corruption" is not actually checkable.
- **Cover and image upgrade quests** — low-resolution or hash-duplicate covers
  trigger a quest; `94_image_types` / `95_image_crops` exist and the image service
  already answers the duplicate half.

#### 7.24.12 Deferred, adopted in principle

- **Freshness and re-verification quests** — "last verified" on facts that rot
  (studio active? site alive?). One more completion factor, following §7.17.2's
  freshness pattern.
- **Per-edit source citations** — a structured source link on high-impact fields,
  so completion separates sourced from unsourced and confidence scoring has an
  input. §7.7 counts source links already; this makes one a first-class field.
- **Cross-entity inference suggestions** — co-star graph gaps, studio-network tag
  inheritance, cluster members with differing performers, emitted as **pre-filled
  draft edits for humans to vote on**. Never auto-applied, for the same reason
  alias collisions are not (7.24.4).
- **Gold-set reviewer calibration** — seed known-answer edits, measure vote
  accuracy. `85_add_vote_weight` exists, so the mechanism is present; this is the
  measurement that says whether the weights are right, which makes §7.6's "voting
  consistency" trust input objective instead of asserted.
- **Public state-of-archive page** — a completion heatmap by studio, generated from
  §7.7's scores. It must aggregate rather than expose entities, so it cannot leak
  anything a per-entity view would hide.
- **Keyboard-driven triage mode** — fast match/mismatch and merge-review on
  collages. It lowers per-contribution effort, which raises throughput against
  every other item in this amendment.
- **Campaign weekends** — time-boxed themed sprints. Thin, and §7.7 already has
  federated joint campaigns, so this is a scoped instance of an existing mechanism.
- **Early read-only metadata mirror** — §7.17.1's read-only role, fed by the
  nightly dump. Carries no trust-model risk, which is why it precedes full
  federation.
- **Bot import pipeline** — the largest single completeness lever and the most
  expensive. Batch drafts at low trust in a quorum queue (§7.18.1) so humans vote
  rather than type. **Its stated precondition is accepted without reservation:
  fix the modbot race (§8.1) first.** Importing at scale before that is fixed
  amplifies it.

#### 7.24.13 Audit row — redirect permanence needs an end-to-end test, not a feature

The paste lists redirect-permanence as new work. It is **substantially built**:
`06_deletion_and_redirects` creates all four redirect tables, and all four merge
paths write them — `internal/service/edit/scene.go:648`, `studio.go:418`,
`tag.go:310`, `performer.go:571`, each calling `Update*Redirects` — with
read-time resolution in `FindStudioWithRedirect`.

So this is not a spec row. It is **one row in the operator verification surface**:
an end-to-end assertion that a stored Stash-library ID survives merge *and*
delete. §4.3's compatibility claim depends on it and nothing tests it today, which
is the only reason it appears here at all.

#### 7.24.14 Rejected

- **Auto-generated bounties from completion gaps** (7.24.3) — rejected as
  written, on the reason quoted verbatim from `80_add_authored_quests`: a bounty
  is a human promise, so it lives only on an authored quest, and **the generator
  never reads that table**. The pricing formula is adopted as assistance to the
  author; the generator is not adopted. *(A future draft will re-propose this.
  This is the reason.)*
- **Federated bounty exchange** — deferred to Phase 4 with the rest of
  federation, and dependent on cross-instance trust machinery §7.17 does not yet
  have.
- **Holder count as an exact "how many copies exist"** — rejected as stated; it
  is a lower bound (7.24.5) and one user with three copies counts once.
- **Real-money bounties, swarm/torrent mirroring, a vanguard vote on what gets
  prioritized, and any rule keyed on client-supplied region or device** — all
  excluded by the paste itself, and all already barred: `bounty_points` is
  `INTEGER`, §7.19 refuses the content layer, §7.20 refuses the influence vote,
  and §7.23 D5 makes region/device a denylist evaluated *after* the five access
  conditions, never instead of them.

#### 7.24.15 Order of work

By dependency, not by the paste's ranking:

1. 7.24.1 verified-unknown · 7.24.2 denominators — everything below prices or
   measures a gap, and needs both
2. 7.24.4 data-lint quests · 7.24.5 fingerprint demand + holder bound
3. 7.24.6 corroboration · 7.24.3 authored bounty pricing
4. 7.24.7 rollback · 7.24.8 delta preview · 7.24.9 webhook events
5. 7.24.10 review-queue bounties + onboarding
6. 7.24.11 preservation (backup drill **first**, inside it)

The paste's own "best cheap wedge" agrees with this order, and is right for the
wrong reason: those items are cheap, but they are also *first*, because 7.24.1
and 7.24.2 are what everything downstream prices against.

---

### 7.25 Fourth intake amendment — recall: finding a scene from broad strokes (amended 2026-10-05)

Two AI-generated lists of 100 ideas each arrived for one problem: **someone
remembers a scene they saw a long time ago in broad strokes — an era, a vibe,
half a performer name, a setting — and a "tip of my tongue" community could not
find it.** Verbatim sources and the merged index are in
`docs/ideas/scene-recall/`; this section is what was adopted, and
`docs/plan/feature-scene-recall-index.md` is the plan.

**The premise, and the two failures hiding inside it.** Tip-of-tongue
communities fail structurally — prose threads, no structured data to search.
But "could not find it" has two very different causes, and this section treats
them as separate problems because they need opposite responses:

- **A vocabulary failure.** The user cannot *name* what they remember. The
  answer is structured clues and a vocabulary learned from solved queries
  (7.25.3).
- **A coverage failure.** The scene was never catalogued at all. The answer is
  ingestion, not search, and the honest surface for it is `expected_totals`
  (7.25.5) — "this studio claims 400 scenes, we have 120".

Nothing in this section can tell the two apart on its own, and that is a
limitation of the design rather than a gap to be closed later: **an absent scene
is indistinguishable from an undescribed one.** 7.25.5 is the only thing in the
set that makes the difference visible to a user, and it is deliberately early
in the order of work for exactly that reason.

**What was verified before anything was adopted, and it changed the plan.**
`scene_search` is a denormalized table maintained by triggers, with a ParadeDB
BM25 index. Measured on a freshly migrated database, its columns are
`scene_id, scene_title, scene_date, studio_name, network_name, studio_aliases,
network_aliases, performer_names, scene_code`. **`details`, `director` and tags
are absent** — migrations 35, 56 and 61 each rebuilt the table and none added
them. Separately, and worse than the missing columns: **`scene_tags` carries no
trigger at all.** `tags` fires `trg_tag_search_on_tag` into `upsert_tag_search`,
`scenes` fires `trg_scene_search_on_scene` into `upsert_scene_search`, and the
`scene_performers` row triggers exist for insert and delete — so the performer
path is maintained and the tag path has no maintenance mechanism whatsoever. A
tag column added to `scene_search` without a trigger on `scene_tags` would be
an index that silently reports whatever the last unrelated scene write happened
to recompute. That is the single most important finding in this amendment and
it is not visible from either source list.

#### 7.25.1 The recall index — `details`, `director`, and tags become searchable

The three fields the vision's §7.4 "discovery is the default experience" assumes
are searchable, and which are not. `scenes.details` is free text and is where
broad strokes live by definition ("hotel room, rainy night"); `scenes.director`
is a half-remembered-name cue; tags are the most common broad stroke of all.

Adopted as **one migration and one query change**, because they are the same
change: three columns on `scene_search`, the same denormalizing `INSERT` that
already builds `performer_names`, the matching fields in the BM25 index, and
three new `paradedb.match` disjuncts in `SearchScenes`.

**Two decisions, and what each rules out.**

**R1 — tags are weighted above studio in `disjunction_max`.** For a vague query,
what happened in the scene matters more than who released it. The ranker
already carries per-field boosts (`performer_names` at 2.0), so this is a
weighting change and not a new mechanism. It rules out treating the tag field as
just another equal disjunct.

**R2 — a `scene_tags` statement-level trigger, not a row trigger.** The
performer path's insert/delete triggers use `REFERENCING NEW TABLE` and are
therefore one statement each regardless of batch size. A row-level trigger on
`scene_tags` would fire `upsert_scene_search` once per tag per scene, which on a
bulk tag import is the difference between one rewrite and thousands. The
statement-level shape is the one already proven in this schema, so it is the one
reused.

#### 7.25.2 A drift check, because a trigger-maintained index fails silently

`scene_search` has no way to report a row it missed. A scene whose trigger did
not fire is invisible to every search, forever, and nothing in the product
notices — the same class of fault as §7.24.1's verified-unknown markers, where
*absence* could not be distinguished from *empty*. Without this, 7.25.1's
correctness rests on a trigger nobody can observe failing.

Adopted as a **query, not a watch**: `count(*)` of live non-deleted scenes
against `count(*)` of `scene_search` rows, plus the scene-ids in the first set
and not the second. A count difference is the alarm; the id set is the
diagnosis. It ships in the same migration as 7.25.1 because a drift check added
later is a drift check nobody adds.

#### 7.25.3 The board is the fallback, so it needs to be findable and structured

The identification board already exists (§7.5, migration 79, and it is complete
— §7.16's table saying otherwise is stale). It is deliberately conservative: a
vote is evidence, never authority, and nothing in it can create metadata.

What it is *not* is searchable or structured. `IdentificationPostInput` is
`{targetType, targetId, description, snapshotId}` and **`description` is the
only queryable clue**; `collage_id` exists in the table and in the Go struct and
is absent from the GraphQL type; `IdentificationCandidate.note` has a doc
comment saying verbatim that it is not exposed as a mutation input because no
caller supplies one. The board's own archive — every solved question — is
unsearchable, so the answer to a new question cannot be found by asking the
archive.

Adopted here, and each is small:

- **A BM25 index on `identification_queries.description`.** The cheapest item in
  the set and the one that makes every solved query reusable.
- **Structured clue fields on the post input** — era band, approximate duration,
  performer count, setting. Free text stays, and stays required; a user with a
  half-formed memory must still be able to post it (the board's own migration
  comment insists on this).
- **`note` exposed on the candidate mutation.** "The studio watermark is visible
  in frame 3" is the difference between a suggestion and a guess, and the field
  already exists.
- **`collage_id` exposed on the query type.** The column and the Go struct are
  both already there; only the schema omits it.
- **Solved queries linked back from the resolved scene's page.** One index and
  one join, already possible through the
  `identification_queries_resolved_idx` partial index.

**Not adopted here: auto-resolving anything.** Structured fields may
*pre-populate a candidate list for a human to confirm*; they may never settle a
query. The board's rule is not a default this section is permitted to relax.

#### 7.25.4 Filters the schema cannot express, which are the most-remembered clues

`SceneQueryInput` has no duration, no date *range*, and no performer count.
`DateCriterionInput` is one `Date!` plus a comparison modifier, so a range is
expressible only by passing one bound at a time. Meanwhile "it was around 2016",
"it was short" and "it was one of those two-person scenes" are three of the
commonest broad strokes there are.

Adopted: **date range, duration band, performer count** as first-class inputs,
each composing into the existing `queryScenes` surface rather than adding a
parallel one.

**One thing already exists and is mistaken for missing.** `CriterionModifier`
includes `EXCLUDES` and `internal/service/scene/query.go` honours it for `id`,
`studios` and `tags`. Negative filters are a **UI and vocabulary** problem, not
a missing operator, and no backend work is needed for them.

#### 7.25.5 Coverage failure has to be visible, or the whole premise is unfalsifiable

`expected_totals` (migration 97) records a sourced claim about how many scenes
a studio has. Nothing surfaces it. So a user searching for a scene that was never
catalogued gets an empty page and concludes their memory is at fault.

Adopted: **surface the ratio at the studio level**, as a plain count comparison
in the directory. "This studio claims 400 scenes, we have 120" is the honest
answer to a failed recall, and it points the effort at ingestion instead of
search. It is the only item in this amendment that can distinguish the two
failures in §7.25's premise, which is why it is not deferred.

#### 7.25.6 Order of work

By dependency, and the first item is not a feature:

1. **The evaluation set** — 150–200 remembered descriptions with known answers,
   drawn from the board's solved queries. Every claim in this amendment is
   unfalsifiable without it, including the ones in the two source lists that
   were ranked first. A one-migration change that "obviously helps" is exactly
   the kind of claim that needs a measurement rather than an assurance.
2. **7.25.1 + 7.25.2** — the index fields and the drift check, together.
3. **7.25.3** — the board's findability and structure.
4. **7.25.4 + 7.25.5** — the missing filters, and the coverage surface.

#### 7.25.7 Explicitly not shipping, and why

- **CLIP / face / appearance matching**, and every idea depending on image hashes
  (frame-upload matching, reverse-collage search, duplicate-collage detection,
  colour-palette fingerprints). There is no ML stack in this repository, no
  image hash on any table, and §7.19 refuses the content plane outright. The
  strongest *technical* answer to "I remember the picture, not the words" is the
  worst *fit* for this codebase, and it stays out until the text is searchable.
- **Scene vector embeddings and ANN search.** Same reason, one phase later.
- **Auto-creating or auto-updating metadata from a query, a clue, or a vote.**
  Barred by §7.5's own doctrine, restated in 7.25.3.
- **Federated recall broadcast.** The machinery is real (§7.17, F1–F6, and D2 is
  complete), but a new *question kind* across the mesh is a Phase 4 item and this
  amendment is a Phase 1/2 item. It is listed in
  `docs/ideas/scene-recall/` and deliberately not adopted here.
- **A "similar to" block on every scene page**, despite §7.4 promising it. It is
  adjacent, it is not on the recall path, and adding an unrequested discovery
  surface is how a focused amendment becomes an unbounded one.

---

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

## 7A. The commons half of the cross-repo alignment

**This section is the part of `ALIGNMENT.md` that belongs to *this* repo.** The
`stash` side wrote the contract in
`~/code-local/worktrees/stashforge/docs/ALIGNMENT.md` §3–§4 and the debt it
accrued in `HANDOFF-SPLIT.md` §4; this is where those rules land on the receiving
end. Written 2026-09-30, `r074-receiving-guard`.

`ALIGNMENT.md` §3 is one sentence and one rule:

> **The commons owns identity. A node owns bytes.** No path, filename,
> hostname, IP address, or account detail crosses from a node to the commons, **in
> either direction, ever.**

**"In either direction" is the whole difficulty, and it is why this half was
missing.** `stash` enforces the **sending** half today, in its exporter's path
guard, with a positive control. A one-sided guard is half a guard: the exporter
can be perfect and the commons can still acquire a path from a peer.

### 7A.1 The one rule, as this repo implements it

| Half | Where | State |
|---|---|---|
| **Sending** — a question carries no content, no path, no address | `internal/service/federation/wire.go` `Question` + `ask.go` `Validate` | **done**, `7704600c` |
| **Receiving** — a `base_url` the box dials is validated | `baseurl.go` `ValidateBaseURL`, called from `service.go` `Create`/`Update` | **built, wired and reachable**, `23da1f6a` + `e2ee78b1` + `8338a797` |
| **Schema** — nothing that can hold an address is unguarded | `internal/service/federation/schema_scan.go` | **built, scanning only** |

**The middle row is now fully closed.** `Factory.Federation()` has a caller: the
GraphQL operator surface (`8338a797`, D2 step 6). `federation_peer_create` and
`federation_peer_update` go through `Service.Create`/`Service.Update`, so both
validate, and an integration test asserts the outcome in operator terms — an
ADMIN cannot register a peer at `169.254.169.254`, and cannot repoint an existing
one there either. 42 mutations killed across four harnesses.

Two residual notes, neither closable in code:

- A caller building its own `queries.New(f.db)` handle from the pool can still
  write `federation_peers` without the guard. That is a property of Go, not of
  this package, and the operator surface is now the intended way in.
- A `CHECK` constraint cannot encode the rule, because Postgres cannot resolve
  DNS in a constraint. The available schema-level backstop is a scheme check.

See §7A.3 and §7A.5.

### 7A.2 The seven address-shaped columns, measured

A word-bounded grep across the whole migrations tree, with the quotes optional,
returns **seven** — not one, and not six. Two of the seven are addresses the box
**dials**, which is the distinction that matters and the one a storage-only search
misses:

| Migration | Column | Direction | Guarded by |
|---|---|---|---|
| `01_initial:33` | `performer_urls.url` | stored, operator-typed | — |
| `01_initial:89` | `studio_urls.url` | stored, operator-typed | — |
| `01_initial:109` | `scene_urls.url` | stored, operator-typed | — |
| `04_image_tables:3` | `images.url` | **served** | — |
| `21_site_urls:5` | `sites.url` | stored, operator-typed | — |
| `84_add_webhooks:27` | `webhook_endpoints.target_url` | **DIALED** | `webhook.ValidateTargetURL` |
| `89_identification_federation:28` | `federation_peers.base_url` | **DIALED** | `ValidateBaseURL` (**unwired**) |

Two measurement errors are recorded because R074's own guard is a source scanner
and would have inherited both:

1. **Substring matching inflates.** `03_misc`'s `director TEXT` matches on `dir`
   ⊂ `director`. A guard written that way demands the commons stop storing a
   person's director, and the tempting response is to weaken the guard.
2. **Requiring the quotes hides the worst case.** `images.url` is written
   **unquoted** (`url VARCHAR NOT NULL`) while every other address column is
   `"url" varchar`. A quoted-name regex passes while the one column the original
   measurement found goes unwatched.

**A guard that matches zero things passes.** Every one of those controls has a
test: `TestScannerDoesNotCountSubstrings` for (1),
`TestPathScannerMatchesUnquotedColumn` for (2), and `TestScannerFindsTheSeven`
for the dial columns specifically.

### 7A.3 R074's three rules, not one rule seven times

`HANDOFF-SPLIT.md` §4 states the guard as three rules because they are **not the
same rule applied seven times**:

1. **A path, hostname or IP must not be *storable*** — not in the `*_urls.url`
   columns, not in `sites.url`.
2. **An address the box *dials*** (`target_url`, `base_url`) must be validated by
   resolve-then-check-every-address, **at write time AND at dial time** — a URL
   that validated on insert can resolve differently later, which
   `84_add_webhooks`' own comment already says.
3. **A *served* URL** (`images.url`) must not become a proxy for one: `file://`,
   `\\host\share`, and a bare `/etc/passwd` are all accepted by a string column,
   and all three are the attack.

**Rule 2's "and at dial time" is not yet satisfied.** `ValidateBaseURL` is a
write-time guard. Nothing re-checks at dial time, so the rebinding window stays
open: a peer row inserted today whose hostname resolves publicly now and privately
in a week will be dialled in a week. **This is the single most important open
item in this section.**

**Rule 3 has a scanner and no runtime guard.** `images.url` is a plain string
column and `IsSuspiciousValue` covers the three attack values, but nothing calls
it on the write path. Related: upstream PR #736 removes `images.url` entirely,
and is declined in `docs/plan/upstream-pr-port.md` partly *because* it changes
this surface.

### 7A.4 The three boundaries from `ALIGNMENT.md` §4, as this repo answers them

- **§4.1 Trust is not access.** `internal/service/elo/weight.go` derives vote
  weight from trust **level** bands (`baseWeightForLevel`) and vanguard status;
  it does not read the access store, and no file under `internal/service/elo`
  references an `AccessRule` at all — verified by search, not assumed.
  `internal/service/trust/accessrules.go` keeps the gate a conjunction and
  reports `Enforced` per rule rather than claiming enforcement it does not have:
  `region` and `device_class` are `Enforced: false` because both are
  client-supplied claims (`ALIGNMENT.md` §4.3), `mfa` is `Enforced: true` only
  when the **auth provider** actually has it configured, and
  `PerEntityRule` — the denylist by tag/studio/performer — is `Enforced: true`.
  Two of the four are enforced here, two are deliberately not, and the file says
  which and why.
- **§4.2 Gravity is an operator control, not a vote.** Recorded here because the
  owner proposed the vote twice and rejected it twice, in both specs, for the
  same reason. A third proposal should find a decision rather than an argument.
- **§4.3 A control keyed on a value the client supplies is not a control.**
  Region and device class are reported unenforced by design, per §7A.4's first
  bullet. The content-plane version of the same rule — peer-supplied filename,
  capability claim and manifest hash are **claims** — is why
  `identification_foreign_candidates` is named for a *remote* value
  (`remote_entity_id`, `remote_entity_name`, `remote_vote_count`) and carries a
  local `query_id` and `peer_id` that no remote value can select.

### 7A.5 What this repo still owes

1. ~~Wire `ValidateBaseURL` at write time~~ — **done, `e2ee78b1`**.
   ~~Make it reachable~~ — **done, `8338a797`**.
2. **Re-validate at dial time.** Rule 2's second half, and the rebinding window
   is the reason the webhook validator exists in the shape it does. **This is now
   the top open item.** It cannot be done in this branch: the dialer
   (`client.go`, D2 step 5's second half) exists only as an **untracked file in
   the shared tree**, owned by the session working on D2. Writing a dial-time
   check here would mean writing a second dialer, so this item belongs with
   whoever lands `client.go` — it should call `ValidateBaseURL` on the way out,
   not only on the way in.
3. **Decide whether the five stored-URL columns need write-time value checks.**
   An owner decision, and it interacts with PR #736.
4. **Stash side:** `HANDOFF-SPLIT.md` §4 still says the receiving half "does not
   exist". It now exists for `base_url` and is unwired. That edit is not made
   here — the stashforge worktree belongs to another profile.

Full session note: `docs/track/HANDOFF-R074.md`.

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
mkdir -p frontend/build && echo placeholder > frontend/build/index.html

# INTEGRATION suite. The password is not optional: testutil.defaultTestDB is
# `postgres@localhost/stash-box-test?sslmode=disable` with no credentials, and
# falling back to it fails as `SASL auth: password authentication failed for
# user "postgres"` -- a panic in pgDropAll before any test runs, which reads like
# a broken build rather than a missing password. The harness drops all tables and
# re-runs migrations on entry to every package, so ANY package passing is proof
# the newest migration applied; there is no separate migration check to remember.
export POSTGRES_DB='postgres:smoke_pw@127.0.0.1:5434/stash-box-test?sslmode=disable'
make it

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
