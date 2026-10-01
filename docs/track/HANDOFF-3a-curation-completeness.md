# HANDOFF — Phase 3a: curation completeness (spec §7.24.1–§7.24.10)

**Status:** waiting for instruction. Nothing in this phase is implemented.
**Spec:** `docs/SPEC.md` §7.24 · **Plan:** `docs/plan/phase-3a-curation-completeness.md`
**Intake review (read this first):** `docs/track/INTAKE-2026-10-01-curation-completeness.md`

---

## Read before touching anything

Four things, in this order.

1. **§7.24's two framing findings.** The paste that produced this phase ranked a
   bounty board #4 on the premise that bounties are "only a one-liner" in the
   spec. They are not — `80_add_authored_quests` and `81_add_bonus_points` exist.
   And the paste's auto-pricing idea is refused by a comment in the schema it
   would change. If you have not read those two, you will re-litigate a settled
   decision.

2. **A bounty is a promise, so it needs a promisor.** That is the reason for
   §7.24.3, and it is not a style preference. The schema comment says the quest
   generator *never reads* `authored_quests`. Do not add a generator. What you
   build is a **suggested price shown to an authoring human**.

3. **A verified-unknown is an edit, not a moderator field.** If you implement it
   as a direct column write, trust levels (§7.6) and reputation stop meaning
   anything, because the cheapest XP farm in the system becomes available to
   anyone. Route it through `internal/service/edit`.

4. **The fingerprint demand board must never grow a locator column.** Hash and
   count only. A miss record is evidence a copy exists; the moment it carries a
   scene id, user id, or path it is a cross-instance pointer, and §7A's rule is
   the reason.

---

## Start here

Migrations continue from `96_field_verification_state`. The plan names each one and
why it exists; the two rules that matter most:

- `96` is **nullable, and absent means missing**. A row is an assertion. NOT NULL
  would force a backfill of assertions nobody made.
- `97` requires **`source_id` NOT NULL** — §7.24.2's entire rule is that an
  unsourced total silently deflates every completion score on the instance.
  Enforce it in the schema, where a forgotten service cannot bypass it.

Then, in the plan's order: `98`, `99`, `100`, `101`.

---

## HARD RULES

- **Do not edit an existing migration.** The integration harness drops all tables
  and re-runs every migration on entry to any package, so a rewritten migration
  is a different schema than every later branch will receive. 96 and up only.
- **No bounty generator, ever, in this phase.** `100_bounty_pricing_audit` exists
  so the invariant is checkable: a row with `authored_by IS NULL` fails the build.
- **No money.** `bounty_points` is `INTEGER` and stays that way. Real-money
  bounties are rejected in §7.24.14.
- **No auto-merge.** Alias-collision lint emits a quest; the merge path stays the
  existing consensus flow in `internal/service/edit`. Same for cross-entity
  inference suggestions: pre-filled drafts for humans to vote on, never applied.
- **Vote XP pays for the vote, never the outcome.** A reward keyed to the outcome
  is a reward for a position — §7.20's refusal, applied a third time.
- **No XP for a self-affirming verified-unknown.**
- **Two reason codes that are not interchangeable.** `not_publicly_knowable`
  suppresses a gap; `not_yet_looked` does not. Gate 2 in the plan fails if they
  behave the same.
- **Commit `sqlc` and `gqlgen` output** after touching `internal/queries/`. The
  generated code is committed in this repo.
- **Do not start §7.24.11** except the `backup` command, which is the sole
  exception and is scoped narrowly in the plan.

---

## Definition of done

The plan's ten behavioral gates. Two of them are the ones that matter most, and
both are stated as observations rather than intentions:

- **Gate 2** — assert `not_publicly_knowable`, the gap leaves the missing list;
  assert `not_yet_looked`, the gap stays. This is the test that proves the reason
  code is load-bearing rather than decorative.
- **Gate 7** — `authored_quests` rows have a non-null `authored_by`. A bounty
  without a promisor is the failure §7.24.3 exists to prevent.

Gates 3, 5, 6 and 9 are firewall tests: self-affirming XP, no auto-merge, no
locator in the demand response, and vote XP independent of outcome. Write them
even though they look like edge cases. Each one is a decision this repo has
already had to defend once.

---

## Gates from SPEC §9

- `go test $(go list ./... | grep -vE 'internal/image$')` — green
- `go tool sqlc generate && go tool gqlgen generate` — empty `git status`
- integration: `POSTGRES_DB=postgres://postgres:***@127.0.0.1:5432/stash-box-test?sslmode=disable go test -tags=integration -count=1 ./internal/api/`
  — blocked until OpenEXR 3.5 is installed (§2.4). The password is not optional;
  without it the failure reads like a broken build rather than a missing password.
- `make lint` — **unavailable** (§2.8, golangci-lint not installed). Record that,
  do not skip it silently.

---

## Deliberately not done

- **§7.24.11 preservation** — endangered-copy notice, wanted list, nightly dump,
  link-rot checker, release variants. Deferred to their phase. Their data
  dependencies (denominators, holder lower bound) are specified now so the phase
  does not have to re-derive them.
- **§7.24.12 deferred-in-principle items** — freshness quests, source citations,
  inference suggestions, gold-set calibration, state-of-archive page, keyboard
  triage, campaigns, read-only mirror, bot import. All adopted in the spec; none
  scheduled. The bot pipeline's precondition — fix the modbot race (§8.1) first —
  is accepted without reservation.
- **A second completion definition.** §7.24.8's delta preview reads
  `internal/service/completion`. If you find yourself computing a score in a
  resolver or a page, that is the bug this phase is meant to prevent.