# R074 + D2 — handoff

**Status: the exit condition is MET. D2 is complete. 26 commits, 0 unpushed.**
Branch `r074-receiving-guard`, forked from `master` at `d3934900`.
Worktree `~/code-local/worktrees/stash-box-r074`. Profile `coding-2`.
The shared tree at `~/code-local/go/stash-box` was **never touched** — still
`master @ d3934900`, carrying only another session's untracked files.

This file replaces the session-by-session version. It is a handoff for whoever
picks this up, not a log.

---

## FIRST ACTION, verbatim

One thing remains that is not this worktree's to do, and it is a merge:

```bash
cd ~/code-local/worktrees/stash-box-r074
git log --oneline -1        # 311f3bd7, expect clean, 0 unpushed
```

**Read `9e794f9c` before merging.** It brought another session's untracked
`client.go` into this branch and fixed a dead guard in it. That file now exists in
two places, so the merge will collide on `internal/service/federation/client.go`.

**Resolve by taking this branch's copy** — it is the same file plus a live F1
guard and the dial-time check — and tell the other session, because their
uncommitted version has a guard that cannot fire.

Do not merge while that session is mid-task. It was idle at 21:00 with
`client.go` last written 19:08, but idle is not finished.

---

## Environment block

The suite needs Postgres with `pg_search` and `bktree`. The bare `postgres`
package on this host does **not** have them and migration 56 fails without them,
which looks like a broken build and is not.

```bash
sudo systemctl start docker.socket docker.service    # docker is disabled at boot
sudo docker build -f docker/production/postgres/Dockerfile \
  -t stashbox-pg-r074:latest docker/production/postgres
sudo docker run -d --name stashbox-pg-r074 \
  -e POSTGRES_PASSWORD=smoke_pw -e POSTGRES_USER=postgres \
  -e POSTGRES_DB='stash-box-test' \
  -p 127.0.0.1:5436:5432 stashbox-pg-r074:latest

cd ~/code-local/worktrees/stash-box-r074
export POSTGRES_DB='postgres:smoke_pw@127.0.0.1:5436/stash-box-test?sslmode=disable'
make it
```

`POSTGRES_DB` is **not a URL**. Do not give it a `postgres://` scheme — pgx then
parses it as `user=alvaro` and the suite dies in `pgDropAll` with
`lookup postgres: no such host`.

**Port 5436, not 5434.** Another session owns 5434, and two suites on one database
deadlock each other — which produced a failure I spent real time attributing to a
product defect before reading the error.

---

## Gates, with the output that matters

| Gate | Command | Result |
|---|---|---|
| build | `go build ./...` | exit 0 |
| vet | `go vet ./...` | clean |
| format | `gofmt -l ./internal/` | empty |
| suite | `make it` | `EXIT=0`, **30 packages**, 21 runs, green at every commit since run 17 |
| mutations | `mutate_r074.py <spec>` (see below) | **85 killed across ten harnesses** |

Suite runs, all 19 recorded, logs at
`~/.hermes/profiles/coding-2/cache/scratch/r074-full-N.log`:

```
run  1-14: ok=29 FAIL=0            before the notification fixes
run    15: ok=29 FAIL=3  ---FAIL=2 TestQueryNotifications{Pagination,TypeFilter}
run    16: ok=29 FAIL=3  ---FAIL=1 TestDownvoteNotificationSurvivesWhileOtherRejectsStand
run 17-21: ok=30 FAIL=0            green from the fix onward, incl. at every commit since
```

**Runs 15 and 16 were real failures, not flukes.** Do not re-run until green —
that is what a flaky suite invites, and both were fixed at the cause.

**85 mutations across ten specs, and they are committed** at
`docs/track/mutations/` so the claims above can be re-verified rather than
trusted:

```bash
python3 docs/track/mutations/mutate_r074.py "$PWD" docs/track/mutations/<spec>.json
```

| Spec | n | Guards | | Spec | n | Guards |
|---|---|---|---|---|---|---|
| `r074` | 13 | base-URL guard + scanner | | `dialguard` | 7 | dial-time half of rule 2 |
| `r074-wiring` | 7 | the guard being *called* | | `imageurl` | 9 | rule 3, incl. over-strict |
| `708` | 11 | edit-authority threshold | | `foreign-candidates` | 8 | F2 on the GraphQL type |
| `federation-surface` | 11 | operator surface reachability | | `client` | 8 | F1 guard + dial call site |
| `f2-boundary` | 8 | foreign candidate → vote path | | `issue9` | 3 | the NULL-update defect |

`mutations/README.md` explains the harness, what `SURVIVED` meant every time (a
test defect or dead code, never an unfixed hole), and why a spec reporting
`NOT APPLIED` is the harness refusing to lie rather than a pass.

---

## The four things to know before touching this code

### 1. An untracked file in another worktree hides defects

`client.go` sat untracked in the shared tree from 14:38 to 21:00. It contains:

```go
if err := error(nil); err != nil {   // always false
```

**The F1 content guard had never run.** Every question carrying a path, a URL or
an internal hostname was broadcast to every askable peer — the exact thing F1
exists to prevent. It compiled clean and returned no error, because a guard that
cannot fail is nothing `go build` has an opinion about.

Its own regression test, `TestBroadcastRefusesDirtyQuestion`, asserts exactly
this — and lived in the same untracked file. **The defect and the test that
catches it were in one place and neither was in the build.**

*An untracked file is unreachable from any import, so a test that has never been
compiled cannot catch anything, and neither can the guard it was written for.*

### 2. "Blocked on another session" was a reading of a file's location

I reported D2 as blocked for two sessions because `client.go` lived elsewhere. It
**compiles clean in this branch**, so it could simply be brought over. One
`go build` settles that class of question, and I ran it far too late.

### 3. A test asserting an absolute count after a cleanup step asserts test ORDER

Seven notification tests did `markNotificationsRead`, `time.Sleep(200ms)`, then
`assert.Equal(2, count)`. Notifications are raised from a bare `go`, so a
goroutine from an earlier test commits after the mark and the total comes back 3.
Those tests take 5.8–6.0s — the sleep was returning **before the work it waited
on had finished**, not merely racy.

Fixed with `drainNotifications()` (mark read *and poll until zero*), a sampled
baseline, and delta assertions. Four package runs went 104s / 110s / 89s / 96s —
**faster** than the broken 145–183s, because a poll returns when its condition
holds.

### 4. The survivor rule

Every mutation survivor across this work was a defect in a test or dead code —
**never an unfixed hole**:

> A surviving mutation means the guarded line is **dead or redundant**. Delete it;
> do not write a test to pin it.

Three that would otherwise have been "fixed" wrongly:

- `dialguard_test.go` drove a local **copy** of the rule, so two mutations gutting
  the real function were invisible. *A test of a copy proves the copy.*
- A fake resolver returned one fixed **public** address for every host, so
  `http://127.0.0.1` resolved outward and the guard allowed it. *A fake that
  ignores its argument is worse than no fake.*
- `imageurl.go` reused `IsSuspiciousValue`, the **question-text** predicate, which
  rejects any value containing `http://`. An image url is a url. *Share the marker
  LIST, never the PREDICATE.*

---

## What each of R074's three rules now has

| Rule | Target | State | Commit |
|---|---|---|---|
| 1 — no local-file reference in a URL | five stored columns | **scanner** reports; `images.url` also guarded at write time | `e4c92b29` |
| 2 — refuse private/loopback/link-local | `federation_peers.base_url` | **write-time guard, wired, reachable** | `23da1f6a`, `e2ee78b1`, `8338a797` |
| 2 — same, on the way OUT | dial time | **wired in `Client.askOne`** | `9e794f9c` |
| 3 — served URLs | `images.url` | **guarded at write time** | `e4c92b29` |

The seven address-shaped columns, re-measured word-bounded with optional quotes:

```
01_initial.up.sql:33                     "url" varchar     performer_urls
01_initial.up.sql:89                     "url" varchar     studio_urls
01_initial.up.sql:109                    "url" varchar     scene_urls
04_image_tables.up.sql:3                 url VARCHAR       images        <- UNQUOTED
21_site_urls.up.sql:5                    "url" TEXT        sites
84_add_webhooks.up.sql:27                "target_url"      webhook_endpoints  <- DIALS
89_identification_federation.up.sql:28   "base_url"        federation_peers    <- DIALS
```

The naive substring scan returns 8 (`03_misc`'s `director TEXT`, because `dir` ⊂
`director`) — that is the inflation `HANDOFF-SPLIT.md` §4 documents.

Both dialled columns are handled: `webhook_endpoints.target_url` already had
`webhook.ValidateTargetURL` (untouched, correctly), and `federation_peers.base_url`
had **nothing** — which is what this work built.

---

## Decisions, and why

**D5 obeyed: reuse `webhook.ValidateTargetURL`, do not write a second resolver.**
`ValidateBaseURL` delegates every address rule to `webhook.ValidateTarget(raw,
ips)` and adds exactly one thing it lacks: `checkNoPathComponent`, R074's "nothing
identifying becomes storable" rule. A second resolve-then-check-every-address
implementation is a second thing to keep in sync and to get subtly wrong.

**The path rule is marker-based, not "reject any path".** A webhook target
legitimately lives at `/hooks/abc123/deep/path`, and so does a peer at `/graphql`.
So the check rejects a path carrying an *identifying marker*.

**The resolver is an interface, not `net.DefaultResolver`.** The rebinding case
cannot be tested without one: proving the guard checks *every* resolved address
needs a resolver returning two addresses for one name.

**Fail-closed on all three "cannot tell" cases** — nil resolver, resolver error,
empty answer. A guard that passes when it cannot answer is the failure
`webhook.isPublicIP`'s own comment calls the worst possible outcome.

**A `CHECK` constraint cannot do this.** Postgres cannot resolve DNS in a
constraint, so "does this host resolve privately" is only enforceable in Go. The
schema-level backstop available instead is a scheme check, which would stop
`file://` at the database. Not done; recorded as the available option.

---

## What I deliberately did NOT do

1. **Did not fix issue #9.** Live since 2019: `*string` cannot distinguish absent
   from explicit null, so 25 fields cannot be set to Unknown. The fix is **not** to
   drop the nil-guards — absent must keep meaning absent, and the SQL beneath
   already writes every column unconditionally, so only the *type* cannot ask for
   NULL. That is a generated-model and API change across 94 input types. Recorded
   with tests (`af1254af`); not patched, because a one-line "fix" would break every
   partial update in the API.

2. **Did not guard the other four stored-URL columns.** They hold
   **operator-typed inbound references** — a human transcribing something they
   found — so a value that looks odd is a typo far more often than an attack, and a
   write-time guard destroys real user data. `images.url` differs in kind: the box
   *serves* it. This is a judgement call and the one place a reasonable person
   would choose differently.

3. **Did not merge into `master`.** Shared tree, another session's write.

4. **Did not weaken the per-page and per-type assertions** in the notification
   tests. A leaked row is not that test's to page through, and raising `perPage` to
   accommodate one destroys what the pagination test is for.

5. **Did not edit `HANDOFF-SPLIT.md` §4**, which still says the receiving half
   "does not exist". It now exists and is wired for `base_url`, and still does not
   exist for the five stored-URL columns. The stashforge worktree belongs to
   another profile.

---

## Traps, as rules

- **When a build contradicts the source, check the source before the compiler.** A
  `SugesterCount` vs `SuggesterCount` typo cost a long detour; I "verified" it with
  a `grep -c` that matched my own mistake rather than the field.
- **Run repeated measurements in ONE backgrounded loop.** A foreground command
  starts a second one, and two suites on one database deadlock.
- **Match processes by `comm`, never `pkill -f` on a command string** — the pattern
  matches the shell running the pkill and kills your own tool call.
- **A bare `go test` on an integration-tagged file reports `[no tests to run]`**, a
  green result that measured nothing. Only `make it` passes `-tags=integration`.
- **Strip comments before scanning a schema for forbidden names.** This repo's own
  prose documents that `ForeignCandidate` has no local id, and a naive substring
  scan reads that as a violation and fails a correct schema.
- **A comment in a migration is a claim about the schema, not the schema.**

---

## Open items

1. **The merge above.** Not blocked on a decision — on the other session.
2. **Issue #9** — a live defect, needs a tri-state input type.
3. **Rule 1 on four columns** — scanner only, by choice (above).
4. **125 `enhancement` issues** and the feature roadmap (17 RFCs, feature-01…05) —
   months.