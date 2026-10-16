# Plan — Phase 3a: curation completeness (spec §7.24.1–§7.24.10)

**Spec:** `docs/SPEC.md` §7.24 · **Intake review:** `docs/track/INTAKE-2026-10-01-curation-completeness.md`
**Written:** 2026-10-01 · **Amended:** 2026-10-05
Status: **in progress** — §7.24.1's SCHEMA and one RULE are built, and its write path is not; see
"the inert rule" below before believing any earlier claim about this section. (See the table below; this line
said "service and gates outstanding" until 2026-10-16, which its own table had already contradicted), and
`internal/service/lint` is built as of the same day, the four frontend surfaces are built, and the
§7.24.11 `cmd/backup` exception is built (2026-10-16).

> **This document is the source of truth for what is NOT built.** `docs/goal-check.py` clause
> C3 requires every plan document to read `Status: Built`, and it will keep failing until the
> phase is finished. Do not mark it Built to make the gate pass: C3 is a proxy for "the work is
> done", and setting it by hand is exactly the kind of gate that becomes decoration.

## Progress

| Item | State | Evidence |
|---|---|---|
| `96_field_verification_state` | **built** | applies cleanly after 01–95; 11 schema checks observed against a real database |
| `102_field_verification_cascade` | **built** | 2026-10-04; fixes a real defect in 96 — see below |
| §7.24.1 write path through `internal/service/edit` | **BUILT end to end, three defects found by the test that proves it** | Links 1-3 of the section below: the GraphQL input (`VerifiedUnknownInput` + `verified_unknowns` on `SceneEditDetailsInput`, mapped in `gqlgen.yml` because the edit-data model declares the type by hand), `internal/service/verification` (the registry, `Assert`, `SuppressingFields`), the materialisation at apply, and the XP rule wired at the funnel. Proven by `internal/api/verified_unknown_integration_test.go`: an assertion submitted through the API reaches `field_verification_states` with `entity_type = 'SCENE'` and the edit's id, `SuppressingFields` returns `[date]` and not `not_yet_looked`, a self-affirming assertion records NO trust event, and the positive control on another user's entity records one. |
| §7.24.2 `97_expected_totals` | **built** | `0e8b54e0`; 9 schema behaviours observed against a real database |
| §7.24.4 `98_lint_quest_definitions` | **built** | `15689da1`; 12 schema behaviours observed |
| §7.24.6 `99_fingerprint_corroboration` | **built** | `003d93b6`; 6 logic behaviours + a measured decision to add no index |
| §7.24.3 `100_bounty_pricing_audit` | **built** | `569fba05`; 12 schema behaviours observed |
| §7.24.8 `101_completion_field_weights` | **built (service)** | `b3676fe2`; `internal/service/completion/delta.go`, 11/11 mutations killed |
| §7.24.8 `101_completion_field_weights` (migration) | not started | weights are in Go today; see below |
| `internal/service/completion` | **extended, pre-existing** | `score.go` already existed (§7.7); `delta.go` adds §7.24.8 on top |
| `internal/service/lint` | **built** | `internal/service/lint/service.go` + `internal/queries/sql/lint.sql` (six named detectors) + `cmd/lintquests`; 12 unit tests, 8 integration tests against a real database, 7/7 mutations killed. See below for the detector set and its two deviations. |
| frontend: the lint board (`/curation/lint`) | **built** | `frontend/src/pages/curation/LintBoard.tsx` + `LintQuestBoard.tsx` + the `LintQuestBoard` query and hook; 7 vitest tests on the page's design constraints, 2 Playwright journeys (the board with a real finding; the VOTE gate), 5 Go integration tests on the resolver. |
| frontend: the detector registry (`/curation/lint/definitions`) | **built** | `LintDefinitions.tsx` + `LintQuestDefinitions.tsx`, the `lintQuestDefinitions` query (ADMIN) and an admin nav entry; 7 vitest tests on the three states (running / stranded / retired), 1 Playwright journey (an admin reads it, a curator is refused by NAME rather than by a fault), WCAG 2.1 AA clean in both palettes. `scripts/walk-3a-lint-registry-live.py` walks the state the page exists for: 8/8 verdicts with a planted stranded row -- the registry answers WHILE the board is refused, retiring the row recovers the board, and the retired row stays listed. |
| §7.24.4 the per-record arm: `lintFindingsFor(entityType, entityId)` | **built** | The same validation pass asked about ONE id -- the surface this plan listed as "the import-validation view", which turned out to be the same query as the record page's panel rather than a second view with no data behind it (an import batch is not a row anywhere in this schema; a record is, so the pass gets a second caller instead of a second rule). 5 Go integration tests: the whole finding and nothing else, an alias claiming this record's name, the refusal of a type no detector implements, the VOTE gate, and an empty answer for a clean record. 6/6 mutations killed -- the type refusal, the zero-count filter, the VOTE directive, each half of the whole-finding SQL, and the filter removed entirely. |
| frontend: the record findings panel + the candidate action | **built** | `frontend/src/components/recordFindings/` mounted on the performer page, and `?source=<id>` on the merge page. 11 vitest tests (the merge link carries BOTH ids; a finding whose fix is an edit offers no merge, with a second record present so the allow-list is distinguishable from the shape of the answer; a lone finding offers no merge; no record-changing control; truncated answers say so; a clean record says nothing, beside a flagged control; a fault is NAMED; a reader is not served it), 2 Playwright journeys (a flagged record's panel naming the other half and its action landing on a merge form carrying both records; a clean record and a reader beside a flagged control), WCAG 2.1 AA clean on the performer page in both palettes. 4/4 mutations killed on the panel's own rules, plus the merge-page prefill walked live. |
| §7.24.10 review-queue aging view | **built** | The data path needed nothing: `EditQueryInput` already carries `status`, `sort: CREATED_AT` and `direction`. What was missing was the one thing "aging" means — a pending edit never said how long it had waited — and a queue that opened on its NEWEST item. So: `formatElapsed` (a LENGTH of time, not `formatDistance`'s "3 days ago"), `renderWaiting` on the card (PENDING only), and `defaultDirection: ASC` for the queue page. 39 unit tests (4 for the formatter's units, 4 for the card's rule including its positive control, plus the existing suite); `e2e/tests/review-queue-aging.spec.ts` walks both claims in a browser against a live instance; `scripts/mutate-aging-order.py` flips the default to DESC, REBUILDS both the frontend and the binary (the build is embedded at compile time), and shows the ordering spec going red while the waiting spec stays green. |
| §7.24.11 exception: `cmd/backup` | **built** | An artifact with a manifest written last, a point-in-time `pg_dump` (dump, table counts, migration version and image rows all read in ONE exported snapshot), a copied image store whose every file is checked against the md5 the DATABASE records before the manifest is written, a strict `-verify` (missing, altered and unlisted files all fail), and a restore that refuses a non-empty target (exit 2) and then proves the restored migration version and every table's row count. 30 unit tests + 3 integration tests that dump and restore the real database; `scripts/backup-drill.sh` runs 13/13 against a live instance and then the integration suite against the RESTORED database; `scripts/mutate-backup.sh` kills 11 of the 12 mutation-able guards (the twelfth is named in the script). |

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

**The live walk found a defect the unit tests could not.** Planting an ENABLED registry row whose slug
this build does not implement, in the deployed database, gave: `-validate` refusing with exit 1 and a message
naming the slug — and the board running happily, returning five detectors instead of six. `Board` had been
written to SKIP such a row, on the reasoning that `Validate` is what refuses them and the instance does not
start without it. That reasoning covers the server and no other caller. `Board` now calls `Validate` first:
one rule, inherited by every caller, and the mutation that removes the call is killed by
`TestTheBoardRefusesARegistryThisBuildDoesNotImplement`. The walk is three steps and worth repeating after any
migration that adds a detector:

```
# plant (is_builtin true + authored_by NULL: the table's check constraint requires an author on non-builtin rows)
INSERT INTO lint_quest_definitions (slug, description, enabled, bounty_points, is_builtin, authored_by)
VALUES ('not_a_detector_this_build_has', 'planted', true, 1, true, NULL) ON CONFLICT (slug) DO UPDATE SET enabled = true;
lintquests -config <cfg> -validate   # must exit 1 and name the slug
lintquests -config <cfg> -limit 1     # must ALSO exit 1 now -- this is the step that failed the first time
UPDATE lint_quest_definitions SET enabled = false WHERE slug = 'not_a_detector_this_build_has';  # retired: must be fine
DELETE FROM lint_quest_definitions WHERE slug = 'not_a_detector_this_build_has';
```

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

### What walking the board found, which building it did not

Four defects and one of them was already shipped. Written down because each was
found by a different instrument, and none of them by looking at the new code:

| What | Found by | Why it mattered |
| --- | --- | --- |
| **`/curation/matchup` has never rendered its page.** The nested routes under `/curation/*` were declared with the ABSOLUTE route constants (`/curation/matchup`, and the new `/curation/lint`), which do not match inside a splat parent -- every `/curation/<anything>` URL fell through to the `/*` child and rendered the dashboard. | the new page did not appear when asked for it | a shipped page nobody could reach, and no test visited it, so nothing reported it. Fixed to React Router's relative form (the house convention three quarters of this frontend already uses: `path="/"`, `path="/:id/*"`). A `Link` takes the absolute constant and a `Route` takes the relative one -- the asymmetry between them is what hid this. |
| **23 TypeScript errors that were invisible.** `src/graphql/types.ts` was stale, and a stale union member makes `tsc` reason about a shape that does not exist. Regenerating the client (`pnpm generate`) surfaced them, all in the edit-detail union: `ContentItemEdit` was never a member the frontend handled, so `getEditDetailsName` read `.name` off a shape that has a `title`. | regenerating the client, for the new query | CI could not have caught it: the checked-in generated file was consistent with itself. The lesson is that a generated artefact which is never regenerated is a place errors go to hide. |
| **The empty board said the archive was clean.** With no detectors at all, `total === 0` is true, so the page said "no findings right now -- every detector below ran", which is the opposite of the truth and exactly what the service's own registry validation exists to prevent. | the vitest case that renders an EMPTY list | the component had two states where the domain has three (clean, findings, no-registry). Restoring a three-way branch is not something a reviewer sees; rendering an empty array is. |
| **The e2e `createSite` helper could not work.** `SiteCreateInput.highlighted` is `Boolean!`, the helper never sent it, and GraphQL refused the variable ("must be defined"). | the first e2e run of the new spec | every test that used the helper would have failed for a reason that names a fixture rather than a feature. The UI was never broken -- its form defaults `highlighted` to true and sends it. |

### What walking the record panel found, which building it did not

Three defects, every one of them in code that had already shipped:

| found by | defect | fix |
| --- | --- | --- |
| the CLI's own standard | `lintquests -entity studio -ids <uuid>` printed `validated 1 studio id(s)` and exited 0. The type went straight to `ValidateEntities`, which groups ids by type and skips any detector whose type has none, so an import could be reported clean without ever being looked at -- and the same pass already refuses an unreadable id for exactly that reason. | `parseRefs` validates the type against the two the service has detectors for; the service re-exports those constants so a caller cannot spell one nothing implements. 3 unit tests. |
| the record page's first live walk | the per-record answer carried only the record it was asked about, so a duplicate arrived with a partner the panel could not name: an action that would have led to a merge page knowing nothing about the finding. | the answer carries **every record in the findings that name the asked id**, and nothing from any other finding. Asserted in both directions -- the pair present, an unrelated pair absent -- because each direction alone is passable by a broken query. |
| the end-to-end journey | the merge page's source field stayed empty while the page's state carried the source: `PerformerSelect` seeds its own state from its `performers` prop **once, at mount**, so a value handed to it after mount is invisible. Continue was armed and the merge would have submitted the prefilled source, with the field on screen still reading "Search for performers to merge...". | the field is not rendered until the prefill has resolved, and it is mounted WITH the source. |

Two further things the sweep reported and this unit did **not** cause, kept here
rather than quietly fixed, because the measurement is the point: stashing this
unit's frontend changes, rebuilding from `HEAD` and re-running the light-palette
sweep reproduces both, byte for byte.

| route | violation | note |
| --- | --- | --- |
| `/edits` | `color-contrast x19`: `.btn-outline-danger` `#db3737` on the card-footer background `#b7b9ba` = **2.31:1** | an edit comment's delete button, in the light palette. A theme-level decision on a shared control, not this unit's; open. |
| `/audits` | `button-name x5`: an icon-only `.btn-link.btn-sm` in the first column of the audit table has no accessible name | open, same reason. |

The same run's two other a11y failures were load, not defects: the
tab-reachability journeys wait 15s for `main` to be visible on a database this
heavy, and with `--workers=1` they pass while exactly these two route sweeps fail.

### Two Bootstrap colours that a themed app cannot use

The registry's axe audit (added to the key-page gate with the page) failed the
first run: `color-contrast`, 11 nodes, in BOTH palettes. Both causes are the same
shape as the `.text-muted` defect the theme already documents -- a static colour
Bootstrap picked against a white page, in an app whose default palette is dark:

| Element | Bootstrap | Measured | Fixed with |
| --- | --- | --- | --- |
| `<code>` | `$code-color: #d63384` | 2.37:1 on the dark card surface, 4.3:1 on white | `--sb-code` in both palettes (dark `#f0a6c8` 5.6:1, light `#a3236b` 6.9:1), asserted against both backgrounds in `theme.test.ts` |
| `.badge.bg-*` | `.bg-*` mid-tone + white type | white on `$success` 3.0:1, on `$warning` 2.9:1 | each variant shaded 30% for badges only (`.badge.bg-*`), which keeps the hue and clears 4.5:1 everywhere |

Neither is the new page's fault and neither could be fixed on it: `<code>` appears
on the sites, audits and fingerprint pages, and `bg-success` badges on the
identification pages and the curation streak card -- and none of THOSE is audited,
which is why the shipped defect survived. The fix is in the theme, where the
`.text-muted` precedent already lives, and the gate now audits both lint pages in
both palettes: 16/16 key-page audits pass.

## The inert rule: what §7.24.1 actually has (measured 2026-10-16)

A feature whose tests pass and which no code path reaches. Every claim below is a grep over the
tree, run by name, and the claim it contradicts is quoted too so the next reader can see which
one was wrong.

WHAT EXISTS, and it is not nothing:

- Migration 96 (`field_verification_states` + `field_verification_reasons`) and 102 (the
  cascade), complete, with the reason registry carrying `suppresses_gap` and
  `allows_self_affirming`.
- `models/model_edit.go:338` — `VerifiedUnknowns []VerifiedUnknownInput` with the jsonb tag
  `verified_unknowns`, on the edit data, with a doc comment citing §7.24.1.
- `internal/service/edit/verified_unknown_xp.go` — `selfAffirming`, `editCarriesVerifiedUnknown`
  and `assertionEarnsTrust`, thoroughly reasoned: the file explains why the decision belongs at
  the funnel rather than at any one of the three call sites that reach it. Commit `b5b242d1`,
  7/7 mutations killed.
- `internal/queries/sql/field_verification.sql` — the four queries the two halves need
  (insert-or-replace on conflict, the per-entity list joined to the registry, the
  suppressing-fields-only read the score needs, and the registry itself for the dropdown).

WHAT DOES NOT, measured one link at a time:

- **No GraphQL input carries the field.** `grep -rn 'verified_unknown' graphql/` is empty. Run
  with the snake_case AND camelCase spellings: the jsonb key is `verified_unknowns`, so a
  camelCase-only sweep can miss a real field — the class of error that produced a wrong row here.
- **Nothing writes the table.** `field_verification_states` appears in exactly four places:
  migrations 96 and 102, the generated models, and the migration integration test.
- **Nothing reads it.** `internal/service/completion` never mentions verification;
  `internal/service/completeness/service.go` does not either. Note that `completeness`'s
  `Unverifiable()` is about a CONTENT SOURCE being dead or DMCA'd — the opposite meaning of the
  word from §7.24.1's "this field is unanswerable", which is why the two must not be conflated.
- **The rule has no production caller.** `assertionEarnsTrust` appears in its own definition and
  in `verified_unknown_xp_test.go`, and nowhere else in the tree.

THE REMAINING WORK, in dependency order, one link each:

1. ~~A GraphQL input field on the scene edit details input~~ **DONE** — `VerifiedUnknownInput` in
   `graphql/schema/types/field_verification.graphql`, the field on `SceneEditDetailsInput`, and an
   explicit `gqlgen.yml` mapping (the edit-data model declares the type by hand, so without the
   mapping gqlgen generates a SECOND one into `generated_models.go` and the schema binds to that
   copy rather than the one the edit path unmarshals).
2. ~~Materialisation at apply~~ **DONE** — `SceneEditProcessor.addVerifiedUnknownsFromEdit`, called
   from `ApplyEdit` beside the fingerprint write, with `asserted_at` = the APPLY time (a pending
   claim is not yet a fact) and `edit_id` filled in (this path is the norm). — write assertions into `field_verification_states` where the scene
   edit's fingerprints are written (`SceneEditProcessor.addFingerprintsFromEdit` is the template:
   extra data on an edit, written when the edit is applied).
3. ~~The funnel~~ **DONE** — `recordTrustEvent` asks the question, `assertionEarnsTrustFor` is the one
   ruling (the scene-typed and raw-jsonb entry points both reduce to it), and `previousEditorOf`
   resolves the entity's most recent OTHER edit.
4. Suppression in the completion score (gate 2), reading
   `GetSuppressingFieldVerificationStates` — where `not_yet_looked` deliberately does NOT appear.
5. The frontend control with the reason dropdown (gate 1's surface).
6. Gates 2 and 3 as tests plus mutations, with the XP battery re-aimed at the CALL SITE: 7/7
   mutants dying against a function nobody calls is the arithmetic this section exists to correct.

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

**Where this list stands, measured 2026-10-16 by grepping for each surface's
identifier rather than by reading the plan's own table:**

| surface | state |
| --- | --- |
| Lint quest board (`pages/curation/LintQuestBoard.tsx`) + the definitions registry (`LintQuestDefinitions.tsx`) | built |
| A record's own findings (`components/recordFindings/`) | built |
| Verification control on the edit form (§7.24.1) | **partly built, and the built part is inert** — the storage (96 + 102) is complete, `models/model_edit.go` declares `VerifiedUnknowns []VerifiedUnknownInput` on the EDIT DATA, and `internal/service/edit/verified_unknown_xp.go` implements the no-XP rule with 7/7 mutations killed. What is missing is every link between them: no GraphQL input carries the field, so no client can send one; nothing materialises an assertion into `field_verification_states` at apply, so the table stays empty; nothing reads it, so completion suppresses no gap; and `assertionEarnsTrust` has no production caller at all. `internal/service/completion` never mentions verification, and `completeness`'s `Unverifiable()` is about a CONTENT SOURCE being dead or DMCA'd — the opposite meaning of the word, which is why this row is spelled out rather than summarised. |
| Completion panel with the §7.24.8 delta preview (§7.24.8) | **not built** — `components/CompletionBar.tsx` shows the score; nothing shows the marginal-gain ranking or a before/after delta |
| Bounty authoring surface (§7.24.3) | **not built** — `bounty_points` is read (the registry displays it); nothing writes an authored bounty price |
| Fingerprint demand board (§7.24.5) | **built** — `fingerprintDemand` (query `GetFingerprintDemand`, service `internal/service/fingerprint/demand.go`, resolver `internal/api/resolver_query_fingerprint_demand.go`), the `/curation/fingerprint-demand` page, and a card on the curation dashboard. Aggregate by `(algorithm, hash)` over `scene_fingerprints`, `count(DISTINCT user_id)` as a LOWER BOUND, floor enforced in the query. See the SPEC correction: 89 does not provide the board to seed, and nothing in this schema records a miss. |
| Review-queue aging view (§7.24.10) | **built** — a pending edit carries `Waiting: <elapsed>` (`renderWaiting` in `components/editCard/EditCard.tsx`, `formatElapsed` in `utils/date.ts`), and the queue OPENS oldest-first (`pages/edits/Edits.tsx` passes `defaultDirection: ASC` through `EditList` to a new `defaultSort`/`defaultDirection` parameter on `useEditFilter`, which are defaults rather than fixed values, so the control and any URL parameter still win) |

The plan stays `in progress` because of this table: C3 in `docs/goal-check.py`
requires `Status: Built`, and five of the six frontend surfaces this plan calls
for do not exist yet. Marking it Built would make the clause report a door that
is not there.

- **Verification control** on the edit form: per-field "known unknown" with a
  reason-code dropdown. Not free text — §7.24.1's rule.
- **Completion panel**: score, the missing-field list ranked by marginal gain, and
  the §7.24.8 before/after delta preview.
- **Bounty authoring surface**: a price field pre-filled with the formula's
  suggestion, visibly marked as a suggestion, with the override recorded.
- **Fingerprint demand board**: hash + count only. **No scene, no user, no path
  column exists in this view and the API response must not carry one** — the
  temptation to add it later is the risk. BUILT: gate 6 below is the assertion, the
  response type is checked at the type level as well as the value level (so it holds
  for an empty board), and `DemandTable` asserts in the browser-side suite that no
  hash carries a link — the same firewall on the other side of the wire.
- **Lint quest board** and **review-queue aging view** (§7.24.10).
- **A record's own findings** (`src/components/recordFindings/`), mounted on the
  performer page: the same validation pass asked about one id, silent when the
  record is clean, absent for a reader who cannot vote, and naming a refusal
  rather than reporting a quiet record when the pass has stopped running. Every
  action on it is a link into a flow that already exists -- the merge form,
  preloaded with both halves of the finding, or the record's edit form -- and the
  panel itself can change nothing.

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

### Built 2026-10-16 — and the two facts that shaped it

**There is one database engine, and that is a fact about the code, not a choice
made here.** `database.Initialize` calls `pgxpool.ParseConfig("postgres://" +
path)`, so the configured `database` value is a DSN with its scheme stripped.
There is no second backend for this binary to detect: no engine switch, no
sqlite branch, no config key whose other arm is unreachable.

**The connection string never appears on a command line.** `pg_dump` and
`pg_restore` accept a connection string only as an argument, where it lands in
`ps` output and in another user's shell history — the same reason `sdbimport`
refuses the source password as a flag. So the command parses the DSN the way the
app does (`pgxpool.ParseConfig`, which is the same parser, not a second one) and
passes the parts as `PGHOST`/`PGPORT`/`PGUSER`/`PGPASSWORD`/`PGDATABASE`/`PGSSLMODE`
in the child's environment.

**The artifact** — `backup -config <file> -out <dir>`, written in this order:

    <dir>/db.dump           pg_dump -Fc: custom format, compressed, and restorable
                            with pg_restore. Not a plain .sql, which a failed
                            restore can half-apply.
    <dir>/images/           the image store tree, copied verbatim, when image_location
                            is set and holds files
    <dir>/README            how to restore, INSIDE the artifact
    <dir>/CHECKSUMS.sha256  sha256sum -c compatible, over every file above
    <dir>/MANIFEST.json     written LAST

The order is load-bearing: a run that dies before the manifest leaves a directory
no reader can mistake for a backup, because both `-verify` and `-restore` refuse
one that has no manifest.

**"Consistent" is two claims and only one of them is a transaction.** One
`pg_dump` is one point in time. The image store is not inside that snapshot, so
the pair is CHECKED instead of asserted — and the first version of that check was
wrong, in the way this plan keeps having to record.

*The rule was written from a guess about a column, and the guess was measured and
found false.* The check required a file for every row whose `images.organized` was
true, on the assumption that the column means "these bytes are on disk". It does
not: `organized` is a moderator's sign-off on an image's categorization
(`internal/service/image.SetOrganized`), and the instance this was measured against
holds **one organized row with no file and fourteen unorganized rows, ten of which
have files**. Nothing in the schema says a row must have local bytes, so the rule
as written would have refused every backup of every real instance.

The rule that survived measurement is the intersection: **every file present in the
store is checked against the checksum of the row that names its path, and a
MISMATCH fails the backup** — that is corruption, and it is the thing the check
exists to find. A row with no file is counted, and a file with no row is counted;
an instance with no `image_location` at all is the same case as one whose store
directory is missing. The manifest carries all three counts plus a `notes` entry
that says which numbers they are, so a reader comparing two backups can see which
number moved instead of inferring it. The column is md5 — that is what
`internal/service/image.calculateChecksum` computes; the artifact's own checksums
are sha256 and serve a different purpose.

**Restore is the most destructive thing the command does and it says so.**
`-restore -dir <dir> [-database <dsn>]` refuses a target database that already
contains tables unless `-force` is passed (**exit 2**, distinct from a failure's
exit 1, so a script can tell "refused" from "broken"), never modifies the
artifact, runs `pg_restore --clean --if-exists` so a re-restore is re-runnable,
and finally asserts the restored database's migration version equals the
manifest's.

**The manifest carries what a restore must be checked against**: the tool
version, the server version, `schema_migrations`' version and dirty flag, the row
count of EVERY table (read from `information_schema`, so it cannot go stale as
tables are added), the image count and byte total, and one entry per artifact
file with its sha256 and size.

**The steps, each with the command that proves it**

| # | step | command | expected |
| --- | --- | --- | --- |
| 1 | no manifest means not a backup | `rm <dir>/MANIFEST.json; backup -verify <dir>` | exit 1, names the missing manifest |
| 2 | a changed byte is caught | `printf x >> <dir>/db.dump; backup -verify <dir>` | exit 1, names db.dump |
| 3 | a deletion is caught | `rm <dir>/images/<one>; backup -verify <dir>` | exit 1, names that file |
| 4 | an addition is caught | `touch <dir>/stray; backup -verify <dir>` | exit 1, "not in the manifest" |
| 5 | a missing or altered image fails the BACKUP | delete/alter one file in a live instance's store, run `backup` | exit 1, names the image, and no manifest was written |
| 6 | the dump really restores | `backup -restore -dir <dir> -database <fresh>` then `select version, dirty from schema_migrations` | equals the manifest's |
| 7 | the rows came with it | every table's `count(*)` vs the manifest | equal, all of them |
| 8 | a destructive restore refuses | `backup -restore -dir <dir>` twice, no `-force` | exit 2 the second time |
| 9 | the drill, end to end | `scripts/backup-drill.sh -config <file>` | checks 1–8, then the integration suite green against the RESTORED database |

**The drill is the point.** `scripts/backup-drill.sh` backs the instance up,
verifies the artifact, creates a scratch database, restores into it, checks the
manifest's claims against what the restore produced, and runs the integration
suite with `POSTGRES_DB` pointed at the RESTORED database: a suite that passes
against a restore is the only evidence that the backup contains a working
instance. `scripts/mutate-backup.sh` kills the guards above, and
`.github/workflows/backup-drill.yml` runs the same script on every push using the
existing `.github/actions/postgres` composite action.

### What building the drill found, which reading the plan did not

| found by | defect | fix |
| --- | --- | --- |
| the first run against a real instance | the image check required a file for every row with `organized = true`. The column is a moderator's sign-off on categorization, not a statement about the disk: that instance has one organized row with no file and unorganized rows that have one. The rule would have refused every backup of every real instance. | the rule is the intersection: files that are there are checked against the md5 their row records, and a mismatch fails; rows without files and files without rows are COUNTED in the manifest, as is an instance with no `image_location` at all. |
| the first drill run | the artifact failed its own strict verification: `CHECKSUMS.sha256: present but not in the manifest`. The file list was collected before the checksum file was written, so the tool wrote a manifest that omitted a file it had just created. | the list is collected twice, the second time as the manifest's, and `WriteArtifactTail` exists as a function so this ordering is testable without a database. |
| running the drill | the `sha256sum -c` check `cd`-ed into the artifact and never came back, so the integration suite ran from the wrong directory (`go: cannot find main module, but found .git/config in /home/alvaro/.hermes`). | the check runs in a subshell and the suite runs from the repository root. |
| the mutation harness, first run | eleven of thirteen mutations did not compile: the marker was a bare `MUTANT` identifier. The harness reported them as broken rather than as survivors, which is the only reason the mistake was visible. | markers live in comments, every mutation compiles, and the harness counts broken separately from survived. |
| writing the CI job | §7.24.11's "a CI job that restores it and runs the suite" is not the claim it sounds like: the integration harness DROPS EVERY TABLE and re-runs all migrations before any integration package runs, so a suite passing against a restored database proves the restored database is one this build runs against — not that the restored data is intact. | the data claims are the restore's own migration-version and row-count comparison against the manifest, plus the drill's spot check of a recorded count; the drill's output says which claim it is making, and the CI job migrates with `POSTGRES_NODROP=1` so the database it backs up is not empty. |
| the live instance | the e2e instance's store holds ten files no row references and its database has fifteen rows with no file, so its image directory and its rows do not describe each other at all. | recorded rather than judged — it is exactly the state the counts exist to make visible, and exactly the state the first rule could not survive. |

---

## Gates from `docs/SPEC.md` §9

Unchanged and still binding:

- unit suite green (`go test $(go list ./... | grep -vE 'internal/image$')`)
- codegen reproducible and clean (`sqlc` + `gqlgen`, empty `git status`)
- integration suite requires `POSTGRES_DB=postgres://postgres:***@127.0.0.1:5432/stash-box-test?sslmode=disable` and the OpenEXR 3.5 install noted in §2.4
- `make lint` — currently unavailable (§2.8), so record that rather than skipping it

## Traps paid for: the three defects the write-path test found (2026-10-16)

Each one was invisible to every existing test, including 7/7 mutations against the XP rule, and each
was found by an assertion about a DIFFERENT layer than the one the defect was in.

1. **THE AUTHOR WAS A LITERAL `nil` ON THE ONLY BRANCH THAT MATTERS.** `applyModify` called
   `ApplyEdit(scene, false, data, nil)`. The `userID` parameter existed only for the create-only
   fingerprint write, so nothing on the modify path had ever read it -- and an assertion on a scene
   that already exists (the only kind there is) wrote nothing at all. The test could see the
   assertions in the stored edit data and saw them go no further; the fix passes the author through
   and is provably inert for fingerprints, which are still gated on `create`.
2. **THE CONVERSION DROPPED THE FIELD, SO AN ASSERTION-ONLY EDIT WAS REFUSED.** `SceneEditFromDiff`
   copies each field with an `IsNull()` guard and had no arm for `verified_unknowns`, so the diff came
   back empty and the edit was rejected as `edit contains no changes`. That is §7.24.1's ENTIRE USE
   CASE ("this field is confirmed absent") being refused as a no-op. It also means the field could
   never have reached the apply path even once the writer existed.
3. **A UNIQUE INDEX IS NOT A CONSTRAINT.** `ON CONFLICT ON CONSTRAINT
   field_verification_states_entity_field_key` failed at runtime with `constraint ... does not exist
   (SQLSTATE 42704)`, because migration 96 declares that name as a UNIQUE INDEX. Column inference is
   what matches it. Nothing had ever run that query, which is the same shape as the inert rule itself
   one layer down.

And the instrument that named defect 3 in one line was not a debugger but the funnel's own design: a
failed apply writes an edit comment carrying the error text, so the test read the reason out of
`edit_comments`.

THE TEST'S OWN PREMISE WAS WRONG ON THE FIRST RUN, and that is the fourth lesson. It asserted that a
self-affirming assertion records no trust event -- using a fixture scene, which `createTestScene`
writes DIRECTLY, with no create edit. So there was no previous editor, the rule could not fire, and
the test was asserting a rule about a situation that did not exist. The lookup was correct and the
fixture was not; the scene now gets a real create edit, which is also the scenario §7.24.1 describes.
Ask what the fixture leaves behind before asserting a rule that depends on history.

## Traps paid for

- **Do not edit an existing migration.** The harness re-runs all of them on entry
  to any integration package, so a rewritten migration silently changes the
  schema every later branch receives.
- **`cmd/sqlc` generated code is committed.** After changing `internal/queries/`,
  run both generators and commit the output, or the build breaks for whoever
  picks it up.
- **A nullable verification column is the design, not a shortcut.** Making it
  NOT NULL later means backfilling assertions nobody made.
- **A page size the server clamps is not a page size.** `internal/api`'s performer
  filter tests wanted the whole result set and set `PerPage: 10000`; the resolver
  clamps a page to `query.MaxPerPage = 100`, so the number did nothing and the
  assertions still depended on how much data other tests had left in the shared
  table. Measured on a fresh database with the whole suite run in order:
  `TestPerformerFilterEyeColorIsNull` failed with EXACTLY 100 ids and a message
  that named neither the page nor the archive; the same commit passes when that
  test runs alone. The queries are now bounded by a per-test fixture marker
  (`Names: pStr(marker + "-")`), which no archive size can truncate, and the
  helper REFUSES a query without one rather than quietly doing the wrong thing
  again. A mitigation that cannot be observed to act is a comment.
- **Run the integration suite against a database that holds nothing else.**
  `scripts/integration-tests.sh` creates one, points the suite at it with
  `-p 1`, and drops it afterwards. Two distinct failures came from getting this
  wrong: against a seeded instance the parser test above fails for a reason about
  page size, and `POSTGRES_DB` without the `postgres://` scheme that
  `internal/database/testutil` prepends parses to *no host at all* -- `pgxpool`
  accepts it, resolves the empty host to the unix socket, and the error names
  `/tmp/.s.PGSQL.5432` instead of the missing scheme.