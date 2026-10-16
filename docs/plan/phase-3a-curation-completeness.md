# Plan — Phase 3a: curation completeness (spec §7.24.1–§7.24.10)

**Spec:** `docs/SPEC.md` §7.24 · **Intake review:** `docs/track/INTAKE-2026-10-01-curation-completeness.md`
**Written:** 2026-10-01 · **Amended:** 2026-10-05
Status: **in progress** — §7.24.1's schema, write path and gates are built (see the table below; this line
said "service and gates outstanding" until 2026-10-16, which its own table had already contradicted), and
`internal/service/lint` is built as of the same day. Still outstanding: the four frontend surfaces and the
§7.24.11 `cmd/backup` exception.

> **This document is the source of truth for what is NOT built.** `docs/goal-check.py` clause
> C3 requires every plan document to read `Status: Built`, and it will keep failing until the
> phase is finished. Do not mark it Built to make the gate pass: C3 is a proxy for "the work is
> done", and setting it by hand is exactly the kind of gate that becomes decoration.

## Progress

| Item | State | Evidence |
|---|---|---|
| `96_field_verification_state` | **built** | applies cleanly after 01–95; 11 schema checks observed against a real database |
| `102_field_verification_cascade` | **built** | 2026-10-04; fixes a real defect in 96 — see below |
| §7.24.1 write path through `internal/service/edit` | **built** | `b5b242d1`; 7/7 mutations killed |
| §7.24.2 `97_expected_totals` | **built** | `0e8b54e0`; 9 schema behaviours observed against a real database |
| §7.24.4 `98_lint_quest_definitions` | **built** | `15689da1`; 12 schema behaviours observed |
| §7.24.6 `99_fingerprint_corroboration` | **built** | `003d93b6`; 6 logic behaviours + a measured decision to add no index |
| §7.24.3 `100_bounty_pricing_audit` | **built** | `569fba05`; 12 schema behaviours observed |
| §7.24.8 `101_completion_field_weights` | **built (service)** | `b3676fe2`; `internal/service/completion/delta.go`, 11/11 mutations killed |
| §7.24.8 `101_completion_field_weights` (migration) | not started | weights are in Go today; see below |
| `internal/service/completion` | **extended, pre-existing** | `score.go` already existed (§7.7); `delta.go` adds §7.24.8 on top |
| `internal/service/lint` | **built** | `internal/service/lint/service.go` + `internal/queries/sql/lint.sql` (six named detectors) + `cmd/lintquests`; 12 unit tests, 8 integration tests against a real database, 7/7 mutations killed. See below for the detector set and its two deviations. |
| frontend (4 surfaces) | not started | — |
| §7.24.11 exception: `cmd/backup` | not started | — |

### `internal/service/lint` — what was built, and where it departs from §7.24.4's list

Built 2026-10-16. The registry is migration 98's (`slug`, `description`, `enabled`, `bounty_points`,
`is_builtin`); the SQL is six named queries in `internal/queries/sql/lint.sql`, which is what 98's comment
requires ("the SQL lives in `internal/queries/` as a named Go function"); `internal/service/lint` is the
mapping between the two, and **Validate refuses to start the instance** when an enabled row names a detector
this build does not implement — 98's stated contract, wired into `cmd/stash-box`'s startup beside
`CreateUserSystemUsers`.

**A candidate is never stored, and nothing is ever fixed.** A finding is re-derived from the tables on every
read (the same rule `internal/service/quest` states for generated quests, for the same reason: a stored "these
two scenes share a URL" is false the moment somebody fixes one of them), and §7.24.4's "emits a quest and never
auto-merges" is enforced by the package having no write path at all — it holds a `queries.Querier` and calls
only `SELECT`s.

**§7.24.4 names five detector families. Four are built, one is substituted, one is not built:**

| §7.24.4's family | What exists | Why |
|---|---|---|
| the duplicate-URL detector | `duplicate_performer_url`, `duplicate_scene_url` | as written, one per entity type: a duplicated performer URL is usually one person entered twice, a duplicated scene URL one scene catalogued twice by two sites, and a curator working one does not want them mixed. |
| alias collisions | `performer_alias_collision` | as written, **and the direction matters**: an alias claiming another performer's name or alias is the finding, while two performers merely *sharing a name* is legitimate in this schema (disambiguation is how the two are separated). No auto-merge. |
| covers below a minimum resolution | `low_resolution_performer_image` | as written, on the SHORTER side (a 4000x200 strip is not a usable performer picture) and only for images linked to a performer. |
| scene-dated-before-studio | **substituted**: `impossible_scene_date` | **the schema has no studio founding date.** `studios` carries `created_at`, which is when THIS INSTANCE recorded the studio — for an imported archive that is the import time, so the detector as named would flag every scene in the database. The family it belongs to ("a date that cannot be true") is expressible, so the detector covers a future date beyond a tunable slack window and a scene dated before one of its performers' birthdates. |
| conflicting tags | **not built** | migration 111 gives tags IMPLICATIONS (`tag_id` implies `implied_tag_id`), not conflicts: this schema has no way to say two tags cannot both apply. A "conflicting tags" detector would have to invent a conflict relation, and inventing one is a schema change, not a lint. Named here rather than silently dropped. |

**Two discoveries the SQL made, both recorded in the query comments:**

1. **`scenes.date` and `performers.birthdate` are TEXT, and a cast is the wrong instinct.** Migration 42
   dropped `date_accuracy` and folded the precision into the value, so a scene "sometime in 2015" is the string
   `'2015'`. Textual comparison is therefore CORRECT here (shorter is earlier in this format) and `s.date::date`
   is WRONG — it fails at runtime for exactly the partial dates the schema exists to hold. The first version
   compared a text date to `CURRENT_DATE` and the database answered `operator does not exist: text > date`,
   which is the recoverable version of that mistake.
2. **`images.width`/`height` are NOT NULL**, so "an image whose size was never measured" is not a state a row
   can be in. A comment claiming the detector handled it was deleted, and the test now asserts the CONSTRAINT
   instead — a detector written against an imaginary state is how a rule silently stops matching the data.

**Measured on the live archive, and the measurement changed the code twice.** The first board run
against thinkcentre's real data (112,391 performers, 181,896 aliases, 1,104,375 scenes) was unusable:

| detector | first run | after | what was wrong |
|---|---|---|---|
| `duplicate_performer_url` | 1.85s | 1.79s | fine |
| `duplicate_scene_url` | 0.67s | 0.09s | fine |
| `low_resolution_performer_image` | 0.08s | 0.06s | fine |
| `impossible_scene_date` | **9.28s** | **0.06s** | one `WHERE` asking `future OR before-a-birthdate` across a LEFT JOIN gave the planner a nested loop probing `scene_performers` once per scene — 1.1M probes. Split into a UNION of the two arms; each now gets the access path its own predicate deserves. |
| `performer_alias_collision` | **37.54s** | **1.05s** | the detector compared every alias against every name and every other alias on `lower()` of both sides. Migration 144 added the two expression indexes — the planner *did* use them and it was still 37.5s, because the plan probed the index once per performer. Rewritten to say what the rule is: narrow to the claims more than one live performer makes (`GROUP BY lower(claim) HAVING count(DISTINCT performer_id) > 1`, an aggregate the planner cannot reorder around), then join. The indexes stay; they are what makes the aggregate cheap. |

**Two lessons the archive taught that tests could not.** A planner's choice is not a property of the query
text: the first fix (indexes) was correct and insufficient, and only re-measuring said so. And an index
turns a probe cheap without changing the NUMBER of probes — when a detector is slow, ask how many times it
asks, not how expensive each answer is.

**A finding about the data, seen while measuring and not this feature's to fix:** the live archive has
1,104,375 scenes and an EMPTY `scene_performers`. The scene-side detectors' birthdate arm cannot fire until
the import that brings scenes also brings who is in them.

**The caller is `cmd/lintquests`** (board, single detector, `-ensure`, `-validate`, and `-entity`/`-ids` for
the import pass). The four frontend surfaces are still outstanding; until they land this is what makes the
detectors runnable against a real archive, and it is the instrument the verification used.

**Verified:** `go test ./...` green; 8 integration tests against a real database (each case builds two rows
that differ in one property and asserts BOTH the hit and the miss — the miss is what catches a join one
condition too loose); `scripts/mutate-lint-quests.py` **7/7 killed** with every restore green, after two
survivors turned out to be the TEST's fault rather than the mutation's (see that script's header: one probe
edited `.sql` while the compiler reads the generated Go, and one fixture used `DeletePerformer` — a HARD
delete — so the "deleted rows are not lint" rule had no subject to be wrong about).

### Two departures from the plan as written, both forced by the existing schema

1. **`citation_url` is a plain text column, not a `source_id` FK.** There is no `sources` table
   in this schema — citations live in `scene_urls`, `performer_urls`, `studio_urls`, each of
   which is a per-entity claim with its own edit trail. An assertion's citation is not that:
   it is a note about why a field is *unverifiable*, and the entity in question may have no url
   row for the page that says nothing about them. Putting it in those tables would make a
   "this page does not mention height" claim sit alongside rows that assert the opposite kind
   of thing. §7.24.2's expected totals are the opposite case and DO get `source_id NOT NULL`
   in `97` — an unsourced total silently deflates every completion score, whereas an unsourced
   unknown suppresses one gap and is a normal thing to record.

2. **`field_verification_reasons` is created BEFORE `field_verification_states`,** so
   `reason_code`'s FK has a target. The plan listed them as separate tables; the ordering is
   forced by the reference.

3. **`source_id` became `(source_entity_type, source_url)` pointing at a url row.** The plan
   wrote `source_id UUID NOT NULL` against a `sources` table that does not exist in this schema.
   A source is a first-class claim with its own edit trail and trust rules — which is what the
   three `*_urls` tables already are — so 97 points at a url row rather than inventing a table
   it has no standing to create.

   This is the **opposite** reasoning from departure 1, and the difference is the point: an
   assertion's citation describes a page that says nothing about the entity, so no url row for
   that page can exist (it would be an entity claim of the opposite kind). A total's source is
   an ordinary entity claim — "this page lists 412" — which is exactly what a url row is.

4. **`101_completion_field_weights` is NOT a table, and the plan's reason for it is the reason
   against.** The plan says "per-field marginal-gain weight, so 'next-highest-value missing
   field' (§7.24.8) and lint pricing share one source." The sharing is real and the weights are
   now in `internal/service/completion/score.go` — but they are in **Go**, not SQL, and moving
   them to a table would not create the second source the sentence is worried about. It would
   create a *second copy of the same constant*, one of which a curator could edit at runtime
   while the completion bar kept scoring with the compiled-in list. That is a worse failure than
   the one the plan is guarding against: it is the one `score.go`'s own header already refuses
   ("a stored score is a second source of truth sitting next to the columns it summarises").

   §7.24.8 itself asks only for a *read* of §7.7's score, and `delta.go` is that read. So the
   migration is deferred until something genuinely needs to query weights **in SQL** — which is
   the lint detectors' pricing, and that lands with `internal/service/lint`, not before it. The
   weights stay in Go until a second consumer exists in a different language.

### Third departure: 96's cascade did not exist, and the number 101 was already spoken for

**`102_field_verification_cascade` fixes a real defect, not a gap in the plan.** 96's header
states the intent:

> ON DELETE CASCADE, unlike most of the edit machinery. An assertion about a deleted entity
> is not a historical record anyone needs … keeping it would let a re-created entity of the
> same name inherit confidence nobody gave it.

`entity_id` is **polymorphic** — one `(entity_type, entity_id)` pair, not four nullable FKs —
so PostgreSQL cannot carry that cascade, and 96's only cascade is on `asserted_by`, which is
the author and not the subject. Measured on a real database:

```
insert performer P, insert assertion for P   -> assertion count 1
delete from performers where id = P          -> DELETE 1
assertion count                              -> 1        <-- the claim is false
```

An assertion outliving its entity suppresses a completion gap for an entity that no longer
exists, and hands that suppression to a future entity of the same name. This is the failure
§7.24.1 exists to prevent, arriving through the feature that implements it.

**Why 102 and not 101.** Departure 4 above defers `101_completion_field_weights`. Reserving a
number for work that is deliberately not being built is the only thing that number is for, so
the cascade migration takes the next free one rather than the next convenient one.

**Why a trigger and not four partial FKs.** `performer_id`/`scene_id`/… would cascade for
free, but they permit a row claiming a performer AND a scene at once, which is exactly the
objection 96's header raises against a four-column shape. The trigger keeps the entity-type
vocabulary in one place and **raises** on an unrecognised entity table rather than skipping —
an assertion nothing can ever clean up suppresses a gap forever.

The same gap exists in `76_add_user_trust` (`trust_events`) and 79's identification board, so
this is the shape this schema uses for polymorphic references rather than a mistake unique to
96. Those are **not fixed here** — a polymorphic reference with no cascade is a design
question about those two tables, not a bug fix to smuggle in alongside this one.

### How this is verified — corrected 2026-10-04, because the old answer was obsolete

The section this replaces read:

> `pg_search` is unavailable on this host, so `go test -tags=integration` cannot run at all …
> which is why `docs/goal-check.py` reports C6b as UNKNOWN rather than PASS.

**That is no longer true, and it was never the whole reason.** A container built from
`docker/production/postgres/Dockerfile` (PG 18 + pg_search 0.21.8 + bktree) runs here, and with
it the suite is **34 packages green with `-tags=integration`** and C6b PASSES. Getting there
needed two fixes to `goal-check.py` itself — it connected `psql` over the container's unix
socket instead of the port it had been told to use, and `CREATE DATABASE` never installed
pg_search/bktree into the database it created. Both made C6b report UNKNOWN on a host that
had a working database and a passing suite.

**The lesson, which is the reason this section exists at all:** for six days this plan
explained an UNKNOWN clause by blaming the environment, and the environment was fine. A clause
that cannot distinguish "no database" from "the gate cannot reach the database" will report
the second whenever the first happens, and the explanation will be believed. The suite being
run is a separate question from the gate being able to run it.

Run it:

```bash
sudo -n docker run -d --name sbx-pg-a --network host \
  -e POSTGRES_PASSWORD=smoke_pw -e POSTGRES_USER=postgres \
  -e POSTGRES_DB='stash-box-test' -e PGDATA=/tmp/pgdata -p 55434 \
  stashbox-pg:18
PGPASSWORD=smoke_pw psql -h 127.0.0.1 -p 55434 -U postgres -d 'stash-box-test' \
  -c 'CREATE EXTENSION IF NOT EXISTS pg_search; CREATE EXTENSION IF NOT EXISTS bktree;'
cd ~/code-local/go/stash-box
POSTGRES_DB='postgres:smoke_pw@127.0.0.1:55434/stash-box-test?sslmode=disable' make it
```

`--network host` rather than `-p`: on this host the bridge network did not assign an IP to a
newly created container, so published ports silently refused connections. `POSTGRES_DB` is a
**bare DSN** with no `postgres://` — see `internal/config/exec_mode_option_test.go`.

The migrations themselves are additionally checked against a real database by applying all of
them in numeric order to a scratch database. There are two committed harnesses:

```
PGPASSWORD=… python3 docs/verify-724-schema.py sb_check
```

- **`docs/apply-all-scratch.py`** applies every migration in order to a scratch database with
  the bm25 indexes shimmed out. **The shim is local to the script and is never written back to
  the repo** — migrations 56 and 58 need `CREATE EXTENSION pg_search`, and stripping only those
  statements is enough to prove a later migration applies against the schema its predecessors
  produce. It reports the highest migration NUMBER, not the file count, because **number 92 does
  not exist in this repo** and "applied 98 migrations" reads as though 92 were covered.
- **`docs/verify-724-schema.py`** rebuilds the database and checks 43 behaviours across
  migrations 96–99. The rebuild is mandatory: checking a dirty database produced 17 failures
  that read as schema regressions and were all stale rows meeting a UNIQUE key.

Both take `PGPASSWORD` from the environment and refuse to run without it. Each previously
carried its own default, which is how the parent and child scripts ended up disagreeing about
the same credential.

## Why this order

Not by the paste's "impact ÷ effort" ranking. By dependency: every item below
prices or measures a **gap**, and §7.24.1 and §7.24.2 are what a gap is measured
against. Building the work-item generator before the two things it counts would
mean generating quests against a denominator that does not exist.

---

## Migration

New migrations continue from `95_image_crops` (95 today). No existing migration is
edited — the fork's convention is append-only, and the harness re-runs every
migration on entry to any integration package, so a rewritten migration is a
different schema than the one the next branch will get.

| # | Adds | For |
|---|---|---|
| `96_field_verification_state` | per-(entity, field) verified-unknown state: `reason_code`, `asserted_by`, `asserted_at`, `source_id`. Nullable — absent means *missing*, present means *confirmed-absent*. | §7.24.1 |
| `97_expected_totals` | (entity_type, entity_id, total, source_id, asserted_by, asserted_at). CHECK total > 0. | §7.24.2 |
| `98_lint_quest_definitions` | named detector registry: `slug`, `description`, `enabled`. The SQL lives in `internal/queries/`, not in the row. | §7.24.4 |
| `99_fingerprint_corroboration` | view over `scene_fingerprints` — `scene_id`, `submission_count`, `algorithm_count`. Materialized on read; no table. | §7.24.6 |
| `100_bounty_pricing_audit` | records what a suggested price was, and whether the author overrode it. Feeds §7.24.3's calibration and is the evidence that the generator never wrote a bounty. | §7.24.3 |
| `101_completion_field_weights` | per-field marginal-gain weight, so "next-highest-value missing field" (§7.24.8) and lint pricing share one source. | §7.24.8 |

**Two schema rules that are not negotiable:**

1. **`96` is nullable and absence means missing.** A row is an *assertion*. If
   the column were NOT NULL, migrating an existing database would have to invent
   assertions, and every invented "unknown" would be a fact nobody verified.
2. **`97` requires `source_id` NOT NULL.** §7.24.2's whole rule is that an
   unsourced total silently deflates every completion score. Enforce it in the
   schema, not in a service that a future code path can forget.

---

## Services

All in `internal/service/`, all extending existing packages rather than adding new
ones. Named after what they touch.

| Package | Responsibility |
|---|---|
| `internal/service/edit` | the verified-unknown **write path**. An assertion is an edit, so it flows through the existing consensus machinery and earns trust like any other edit. This is the whole point of §7.24.1's adaptation. |
| `internal/service/completion` (new) | reads §7.7's completion factors, applies verified-unknown to suppress gaps, folds in `97` denominators when present. **One** definition of completion — §7.24.8's delta preview reads this and never recomputes. |
| `internal/service/lint` (new) | runs the `98` detectors, emits quest candidates. Also runs as a **validation pass over imports**, which is the higher-value half of §7.24.4. |
| `internal/service/fingerprint` (existing) | the §7.24.5 aggregation — hash + count only, thresholded — and the `99` corroboration view. |

**Explicitly not built in this phase:** a bounty generator. §7.24.3 makes the
formula a *suggestion to an authoring human*, and `100` records the override so the
"generator never reads `authored_quests`" invariant becomes checkable rather than
aspirational.

---

## Frontend

Pages under `frontend/src/pages/`, following the 18-page-dir convention already in
the tree.

- **Verification control** on the edit form: per-field "known unknown" with a
  reason-code dropdown. Not free text — §7.24.1's rule.
- **Completion panel**: score, the missing-field list ranked by marginal gain, and
  the §7.24.8 before/after delta preview.
- **Bounty authoring surface**: a price field pre-filled with the formula's
  suggestion, visibly marked as a suggestion, with the override recorded.
- **Fingerprint demand board**: hash + count only. **No scene, no user, no path
  column exists in this view and the API response must not carry one** — the
  temptation to add it later is the risk.
- **Lint quest board** and **review-queue aging view** (§7.24.10).

---

## Definition of done — behavioral gates

Each gate is a test, not a claim. Where a firewall is involved, the test is
written as an observation a reader can check.

1. **A verified-unknown edit flows through consensus.** Assert it via the normal
   edit path as a non-moderator, confirm it lands `pending`, vote it, confirm
   applied. If it applies without a vote, the wrong write path was used.
2. **A verified-unknown suppresses its gap in the completion score.** Take an
   entity at 62%, assert birthdate unknown with reason `not_publicly_knowable`,
   confirm the score does not fall and the gap leaves the missing list. Then
   assert with reason `not_yet_looked` and confirm the gap **stays** — the two
   reason codes are different facts and this is the test that proves it.
3. **No XP for a self-affirming assertion.** Same user asserts unknown on an
   entity they just edited; confirm zero XP. (Firewall: the cheapest-farm rule.)
4. **An unsourced expected total is rejected at the schema.** Attempt the insert
   without `source_id`; expect a constraint violation, not a service error.
5. **Lint alias collisions emit quests and never merge.** Run the alias-collision
   detector over a colliding pair; confirm a quest row and confirm both performers
   still exist separately. (Firewall: no auto-merge.)
6. **The fingerprint demand API returns no locator.** Call it, assert the response
   contains a hash and a count and **no** scene id, user id, or path — assert by
   marshalling the response and checking the field set, not by reading the UI.
7. **The bounty generator does not exist.** `100_bounty_pricing_audit` must have
   a row per authored bounty with a `suggested_price` and `final_price`; a row
   whose `authored_by` is NULL fails the build. (Firewall: a bounty needs a
   promisor.)
8. **Diminishing returns hold.** Two users complete the same (entity, field) gap;
   confirm the second payout is strictly less than the first.
9. **Review-queue XP pays for the vote, not the outcome.** Vote "no" on an old
   pending edit and confirm XP was still paid. (Firewall: §7.20's rule, third
   section.)
10. **Webhook events fire.** Complete an authored bounty; assert a
    `webhook_deliveries` row with the new `event_type` and a payload that carries
    no user content beyond the public entity name.

---

## The one exception to the phase split

`cmd/` contains only `sdbimport` and `stash-box`. **There is no backup command.**
§7.24.11's backup-and-restore drill is the only §7.24.11 item that does not wait
for the preservation phase, because every other preservation claim in that
section is untestable until a consistent snapshot can be taken and restored.

Scope it narrowly and start it early: a `backup` command producing a consistent
DB + image-store snapshot with checksums, plus a CI job that restores it and runs
the suite. Nothing else from §7.24.11 is in scope for this phase.

---

## Gates from `docs/SPEC.md` §9

Unchanged and still binding:

- unit suite green (`go test $(go list ./... | grep -vE 'internal/image$')`)
- codegen reproducible and clean (`sqlc` + `gqlgen`, empty `git status`)
- integration suite requires `POSTGRES_DB=postgres://postgres:***@127.0.0.1:5432/stash-box-test?sslmode=disable` and the OpenEXR 3.5 install noted in §2.4
- `make lint` — currently unavailable (§2.8), so record that rather than skipping it

## Traps paid for

- **Do not edit an existing migration.** The harness re-runs all of them on entry
  to any integration package, so a rewritten migration silently changes the
  schema every later branch receives.
- **`cmd/sqlc` generated code is committed.** After changing `internal/queries/`,
  run both generators and commit the output, or the build breaks for whoever
  picks it up.
- **A nullable verification column is the design, not a shortcut.** Making it
  NOT NULL later means backfilling assertions nobody made.