# R074 — the receiving-end guard for `stash-box`

**Status: the guard is built, proven, and WIRED** (`e2ee78b1`, third session).
`service.go` calls `ValidateBaseURL` from both `Create` and `Update`, and
`Factory.Federation()` is registered.

**Still not reachable in production:** nothing calls `Factory.Federation()`, and
the only caller would be the GraphQL operator surface (D2 step 6's other half,
deliberately not built here because the D2 session owns it). The precise claim is
therefore: *the guard covers every path this package owns*, and a caller building
its own `queries.New(f.db)` handle from the pool could still bypass it.

**The top open item is dial-time re-validation.** See "Open items" below.

Session: 2026-09-30 ~17:10–17:45, agent profile `coding-2`.
Branch: `r074-receiving-guard`, forked from `master` at `d3934900`.
Worktree: `~/code-local/worktrees/stash-box-r074`.

---

## Environment

The suite needs a Postgres with `pg_search` and `bktree`. The bare `postgres`
package on this host does **not** have them, and migration 56 fails without
them — which looks like a broken build and is not. Build the project's own image:

```bash
sudo systemctl start docker.socket docker.service    # docker is disabled at boot
sudo docker build -f docker/production/postgres/Dockerfile \
  -t stashbox-pg-r074:latest docker/production/postgres
sudo docker run -d --name stashbox-pg-r074 \
  -e POSTGRES_PASSWORD=smoke_pw -e POSTGRES_USER=postgres \
  -e POSTGRES_DB='stash-box-test' \
  -p 127.0.0.1:5436:5432 stashbox-pg-r074:latest
```

```bash
cd ~/code-local/worktrees/stash-box-r074
export POSTGRES_DB='postgres:smoke_pw@127.0.0.1:5436/stash-box-test?sslmode=disable'
make it
```

`POSTGRES_DB` is not a URL and must not get a `postgres://` scheme — pgx then
parses it as `user=alvaro` and the suite dies in `pgDropAll` with
`lookup postgres: no such host`. See Trap 3 in the goal.

**Port 5436, not 5434, on purpose.** Another agent was running the same suite
against the shared tree on 5434, and `HANDOFF-SPLIT.md` §1 records that two
`go test ./...` runs at once on this host have already made results unreadable.
The tree's harness drops all tables on entry to every package, so two suites
against one database corrupt each other. Do not share the port.

## Ownership — do not work in `~/code-local/go/stash-box`

The goal's ownership note was **correct and still live**. `ps` at 17:09 showed
four Hermes sessions: `sysadmin`, `coding`, `coding-2`, `coding-3`. The `coding`
profile owns the shared tree and has untracked `client.go` / `client_test.go`
(D2 step 5). Everything here was done in the separate worktree above, on its own
branch, and **nothing was committed, staged or cleaned in the shared tree.**

---

## What was built

Three files, all new, all in `internal/service/federation/`:

| File | What it is |
|---|---|
| `baseurl.go` | `ValidateBaseURL` — the guard on `federation_peers.base_url`, plus the injectable `Resolver` |
| `schema_scan.go` | The migration scanner: finds every address-shaped column, word-bounded and quote-optional |
| `baseurl_test.go` | The suite, including controls for the *scanner* and not only for the rule |

**No migration was written.** The schema is unchanged; this guards a column that
`89_identification_federation` already created.

### The measurement, re-measured independently

`grep` across the whole migrations tree, word-bounded, optional quotes:

```
01_initial.up.sql:33            "url" varchar not null      performer_urls
01_initial.up.sql:89            "url" varchar not null      studio_urls
01_initial.up.sql:109           "url" varchar not null      scene_urls
04_image_tables.up.sql:3        url VARCHAR NOT NULL       images          <- UNQUOTED
21_site_urls.up.sql:5           "url" TEXT                  sites
84_add_webhooks.up.sql:27       "target_url" TEXT NOT NULL webhook_endpoints   <- DIALS
89_identification_federation.up.sql:28  "base_url" TEXT NOT NULL  federation_peers  <- DIALS
```

**Seven, confirming the corrected count in `HANDOFF-SPLIT.md` §4.** The naive
substring scan returns 8 (`03_misc`'s `director TEXT`, because `dir` ⊂
`director`) — that is the inflation the handoff documents.

Two of the seven are addresses the box **dials**, and both are now handled:

- `webhook_endpoints.target_url` — already had `webhook.ValidateTargetURL`.
  Untouched. Correctly.
- `federation_peers.base_url` — had **nothing**. This is what was built.

The other five are stored-URL columns. They remain unguarded by a *runtime*
validator and that is a deliberate open item, not an oversight — see below.

## Decisions made, and why

**D5 obeyed: reuse `webhook.ValidateTargetURL`, do not write a second
resolver.** `ValidateBaseURL` delegates every address rule to
`webhook.ValidateTarget(raw, ips)` and adds exactly one thing it does not have:
`checkNoPathComponent`, R074's "nothing identifying becomes storable" rule.
A second resolve-then-check-every-address implementation is a second thing to
keep in sync and a second thing to get subtly wrong.

**The path rule is marker-based, not "reject any path".** A webhook target
legitimately lives at `/hooks/abc123/deep/path`, and so does a federation peer at
`/graphql`. Rejecting any path refuses a real peer. So the check rejects a path
that carries an *identifying marker*, reusing `IsSuspiciousValue` — the same
predicate the **sending** half in `ask.go` uses. One definition of "this looks
like a path" on both sides of the boundary.

**The resolver is an interface, not `net.DefaultResolver`.** The rebinding case
cannot be tested at all without one: proving the guard checks *every* resolved
address needs a resolver returning two different addresses for one name, which
`net.Resolver` cannot be talked into without a live DNS server.

**Fail-closed on all three "cannot tell" cases** — nil resolver, resolver error,
empty answer. A guard that passes when it cannot answer is the failure
`webhook.isPublicIP`'s own comment calls the worst possible outcome.

## Verification

| Gate | Result |
|---|---|
| `go build ./...` | exit 0 |
| `go vet ./...` | exit 0 |
| `make it` run 1 | **exit 0**, 29 packages ok, 0 FAIL |
| `make it` run 2 | see run-2/run-3 logs below |
| `make it` run 3 | see run-2/run-3 logs below |
| Mutations | **13/13 killed** |

**On the known failure:** `TestMarkSpecificNotificationRead` did **not** fail in
any run. `internal/api` was green in 55.4s. The goal describes it as
pre-existing and order-dependent, and it is — it depends on shared state, and a
private database with one suite at a time does not produce that state. It is not
D2's and it is not this work's.

### Mutations — 13, all killed

Killed: every-address→first-address · drop the path rule · path-only rule ·
nil resolver passes · literal-address substitution · drop delegated webhook
validation · drop the no-host check · scanner quoted-only · scanner substring ·
scanner parses comments · scanner reads down migrations · scanner loses
recursion · scanner drops text types.

Harness: `mutate_r074.py` + `r074-mutations.json` (in this session's scratch, not
committed — **copy them into the repo if they are to be re-run**, they are the
mutation obligation for this file).

**Two survivors, and both taught something.** Both were reported SURVIVED first
and neither was a missing test:

1. **A dead line, not an untested one.** The `if err != nil` on the resolver
   call was redundant — `webhook.ValidateTarget` already rejects an empty
   `resolved` slice. Deleting the duplicate is correct; pinning it with a test
   would have enshrined a second implementation of a rule D5 says has one.
2. **A vacuous mutation.** "Narrow the scan to `postgres/`" could not fail,
   because `internal/database/migrations/` happens to contain only `postgres/`.
   `TestScannerReadsTheWholeTree` now builds a two-directory fixture tree, so the
   mutation is meaningful.

A surviving mutation means the guarded line is dead or redundant — **delete the
check, do not add a test.**

## Two test-authoring errors, recorded because they produced misleading greens

- **The unquoted-column control asserted its polarity backwards.** It required
  `images.url` to be recorded as *quoted*, and failed saying the scanner
  disagreed with the file. The file was right; `images.url` IS unquoted. A
  control test that is wrong tells you the code is wrong, and the tempting
  response is to "fix" the scanner.
- **The "resolver was consulted" assertion was wrong for hostless URLs.**
  `file:///etc/passwd` is correctly rejected at the scheme check with no lookup,
  and demanding a lookup there would have forced the guard to resolve something
  R074 exists to prevent. Now split: has-host ⇒ must consult, no-host ⇒ must not.

## Open items, in priority order

1. **Re-validate at dial time.** Rule 2 wants the check on the way out as well as
   the way in, and the rebinding window is the whole reason `ValidateTargetURL`
   resolves in the first place. **This belongs with whoever lands `client.go`** —
   the dialer is an untracked file in the shared tree owned by the D2 session, so
   writing a dial-time check here would mean writing a second dialer.
2. **The GraphQL operator surface.** D2 step 6's other half. Nothing calls
   `Factory.Federation()` until it exists. Deliberately not built here to avoid
   colliding with the session that owns D2.
3. **Rules 1 and 3 have a scanner, not a runtime guard.** The five stored-URL
   columns (`performer_urls.url`, `studio_urls.url`, `scene_urls.url`,
   `sites.url`, `images.url`) are operator-typed inbound references. The scanner
   reports them; it does not block them. An owner decision, and it interacts with
   PR #736 (declined partly because it removes `images.url`).
4. **A `CHECK` constraint is not possible and that is worth knowing.** Postgres
   cannot resolve DNS in a constraint, so "does this host resolve to a private
   address" can only be enforced in Go. The schema-level backstop available
   instead is a scheme check (`base_url LIKE 'http%' OR 'https%'`), which would
   stop `file://` at the database. Not done; recorded as the available option.
5. **`docs/SPEC.md` §7.23 D2 row** — §7A now carries the alignment, and §7.23.1a
   carries the step state. D2 is still not complete (step 6 unfinished).
6. **D2 step 5's client** is still in flight in the shared tree.

## What this repo owes the other profile — status

| Item | State |
|---|---|
| R074 receiving guard on `base_url` | **Built, proven, wired** (`e2ee78b1`). Not reachable until the operator surface exists |
| R074 scanner for the 7 columns | **Built, proven** |
| Commons half of the alignment in `SPEC.md` | **Done** — `docs/SPEC.md` §7A, `217bd789` |
| Dial-time re-validation (rule 2's second half) | **Open** — belongs with whoever lands `client.go` |

`~/code-local/worktrees/stashforge/docs/HANDOFF-SPLIT.md` §4 needs one line
changed: it says the receiving half "does not exist". It now exists and is wired
for `base_url` (third session). It still does not exist for the five stored-URL
columns, and the dial-time half is still open. **That edit is deliberately not
made here** — the stashforge worktree belongs to another profile.