# stash-box fork — SPEC

**Status:** draft v0.1 — foundation, direction open
**Repo:** `~/code-local/go/stash-box` (clone of `github.com/stashapp/stash-box`)
**Pinned upstream:** `b4b8aef2` ("Use setup-go native cache (#1250)", 2026-09-09)
**Written:** 2026-09-28

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

**Nothing should be implemented until the owner picks.**

---

## 7. Defects observed while reading (candidates, not findings)

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

5. **`passing` without a body boundary.** `package.json` pins
   `packageManager` to `pnpm@11.21.0`; host has 12.4.1. Low severity, but
   codegen and lockfile resolution should use the pinned version.

---

## 8. Verification commands

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
