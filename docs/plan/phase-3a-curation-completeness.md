# Plan — Phase 3a: curation completeness (spec §7.24.1–§7.24.10)

**Spec:** `docs/SPEC.md` §7.24 · **Intake review:** `docs/track/INTAKE-2026-10-01-curation-completeness.md`
**Written:** 2026-10-01 · **Amended:** 2026-10-05
**Status:** in progress — §7.24.1 schema built, §7.24.1 service and gates outstanding

> **This document is the source of truth for what is NOT built.** `docs/goal-check.py` clause
> C3 requires every plan document to read `Status: Built`, and it will keep failing until the
> phase is finished. Do not mark it Built to make the gate pass: C3 is a proxy for "the work is
> done", and setting it by hand is exactly the kind of gate that becomes decoration.

## Progress

| Item | State | Evidence |
|---|---|---|
| `96_field_verification_state` | **built** | applies cleanly after 01–95; 11 schema checks observed against a real database |
| §7.24.1 write path through `internal/service/edit` | **built** | `b5b242d1`; 7/7 mutations killed |
| §7.24.2 `97_expected_totals` | **built** | `0e8b54e0`; 9 schema behaviours observed against a real database |
| §7.24.4 `98_lint_quest_definitions` | **built** | `15689da1`; 12 schema behaviours observed |
| §7.24.6 `99_fingerprint_corroboration` | **built** | `003d93b6`; 6 logic behaviours + a measured decision to add no index |
| §7.24.3 `100_bounty_pricing_audit` | **built** | `569fba05`; 12 schema behaviours observed |
| §7.24.8 `101_completion_field_weights` | **built (service)** | `b3676fe2`; `internal/service/completion/delta.go`, 11/11 mutations killed |
| §7.24.8 `101_completion_field_weights` (migration) | not started | weights are in Go today; see below |
| `internal/service/completion` | **extended, pre-existing** | `score.go` already existed (§7.7); `delta.go` adds §7.24.8 on top |
| `internal/service/lint` | not started | — |
| frontend (4 surfaces) | not started | — |
| §7.24.11 exception: `cmd/backup` | not started | — |

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

### How this is verified, and why not by integration tests

`pg_search` is unavailable on this host, so `go test -tags=integration` cannot run at all —
the repo's own `TestEloMigrationApplied` fails identically, which is why `docs/goal-check.py`
reports C6b as UNKNOWN rather than PASS. Rather than assert the migrations are untested, there
are two committed harnesses:

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