# Plan — Phase 3a: curation completeness (spec §7.24.1–§7.24.10)

**Spec:** `docs/SPEC.md` §7.24 · **Intake review:** `docs/track/INTAKE-2026-10-01-curation-completeness.md`
**Written:** 2026-10-01 · **Status:** specified, not started

Scope here is §7.24.1 through §7.24.10 — the items that are cheap and
unblocking. §7.24.11 (preservation) is a separate phase and deliberately not
started, with one exception noted at the end.

---

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