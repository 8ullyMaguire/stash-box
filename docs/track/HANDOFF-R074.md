# R074 — the receiving-end guard for `stash-box`

**Status: the guard is built, proven, WIRED, and REACHABLE.**
`service.go` calls `ValidateBaseURL` from both `Create` and `Update`
(`e2ee78b1`), and the GraphQL operator surface calls those
(`8338a797` — `federation_peer_create` / `_update` / `_delete`, all ADMIN).

An integration test now asserts the rule in operator terms rather than as a unit
test on a validator: an admin cannot register a peer at `169.254.169.254`, and
cannot repoint an existing one there either.

**Rule 2's second half has landed** as `federation.DialGuard` (`97d00b44`), which
the client calls once `client.go` is merged — see "Ordering" below, because that
is the whole constraint.

**Rule 3 landed for `images.url`** (`e4c92b29`), the one stored-URL column this
box *serves* rather than stores.

**D2 step 6's read path landed**: `federation_foreign_candidates`, ADMIN,
read-only, and the GraphQL type carries no local identity by construction.

**The top open item is now the merge order, not the code.** See "Open items".

Sessions: 2026-09-30, agent profile `coding-2`.
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
2. ~~The GraphQL operator surface~~ — **done, `8338a797`**. One part of step 6 is
   still owed: `federation.queryForeignCandidates`, the read-only query path. It
   does not touch the write path or the guard, so it is independent work.
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
| R074 receiving guard on `base_url` | **Built, proven, wired, reachable** (`e2ee78b1`, `8338a797`) |
| R074 scanner for the 7 columns | **Built, proven** |
| Commons half of the alignment in `SPEC.md` | **Done** — `docs/SPEC.md` §7A, `217bd789` |
| Dial-time re-validation (rule 2's second half) | **Open** — belongs with whoever lands `client.go` |

`~/code-local/worktrees/stashforge/docs/HANDOFF-SPLIT.md` §4 needs one line
changed: it says the receiving half "does not exist". It now exists and is wired
for `base_url` (third session). It still does not exist for the five stored-URL
columns, and the dial-time half is still open. **That edit is deliberately not
made here** — the stashforge worktree belongs to another profile.

## Ordering — the one thing that actually blocks a merge

`client.go` is an **untracked file in the shared tree**, owned by another session,
and it is where the peer is dialled. `baseurl.go` — which holds
`ValidateBaseURL` — exists only on this branch. So:

```
1. merge this branch into master            (gets baseurl.go, service.go, the resolvers)
2. merge the other session's client.go
3. in Client.askOne, call DialGuard before building the request
```

Step 3 is a one-line change and `DialGuard` is written and tested (`97d00b44`), so
nothing about the guard is still open — only the ordering is.

**This was measured, not assumed.** Editing their `client.go` to call
`ValidateBaseURL` does not compile on their branch, because that function is not
there. The attempt was reverted. That is what establishes the merge order; without
it the natural assumption is the opposite one.

## What each of R074's three rules now has

| Rule | Target | State | Commit |
|---|---|---|---|
| 1 — no local-file reference in a URL | all five columns | **scanner** reports; `images.url` also guarded at write time | `e4c92b29` |
| 2 — resolve and refuse private/loopback/link-local | `federation_peers.base_url` | **write-time guard, wired, reachable** | `23da1f6a`, `e2ee78b1`, `8338a797` |
| 2 — same, on the way OUT | dial time | **`DialGuard` written and tested; awaiting the merge above** | `97d00b44` |
| 3 — served URLs | `images.url` | **guarded at write time** | `e4c92b29` |

The four operator-typed columns (`performer_urls`, `studio_urls`, `scene_urls`,
`sites`) deliberately stay on the scanner: a human is transcribing something they
found, so a value that looks odd is a typo far more often than an attack, and a
write-time guard destroys their data. That is a judgement call, and it is the one
place a reasonable person would choose differently.

## Open items

1. **Merge order above.** Not blocked on a decision — blocked on the other session.
2. **Rule 1 on the other four columns** — scanner only, by choice.
3. **#583** is a partial fix with no dedicated test; recorded as such.
4. The feature roadmap (17 RFCs, feature-01…05) is untouched.

## A stale note in the goal file, and the trap behind it

The goal described `TestMarkSpecificNotificationRead` as a known pre-existing
order-dependent failure — "fails in the suite, passes in isolation". **It has not
failed in 14 consecutive full-suite runs**, and it now passes inside the full
435-test `internal/api` package run (0.51s).

The reason it looked absent is worth more than the fact that it passes:

```
$ go test ./internal/api/ -run TestMarkSpecificNotificationRead
ok  ... [no tests to run]
```

The file is `//go:build integration`, so a bare `go test` does not run it at all.
Only `make it` does, because it passes `-tags=integration`. **A test that reports
`[no tests to run]` is a green result that measured nothing** — and it is the same
shape as the "a test that matches zero things passes" trap the goal warns about,
one level up. If you are checking an integration-tagged test, the tag is part of
the command or the check is void.

Its fixture polls for the real condition (`awaitUnreadCountsAbove`, requiring both
`Total` and `Urgent` to rise) rather than sleeping, so the fixed-latency race the
note described was designed out rather than left latent.

**Do not "fix" it again by loosening the assertion.** If it starts failing, the
cause is a new fixture problem, not the old sleep.
