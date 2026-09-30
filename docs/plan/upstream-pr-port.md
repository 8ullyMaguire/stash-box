# Upstream PR port — results

Attempted: 2026-09-29. 40 open PRs on `stashapp/stash-box`.

Note: this fork cannot merge to upstream (`push: false`). "Merge" here means
porting each PR into `8ullyMaguire/stash-box`.

## Merged and verified (26)

Build, `go vet`, 24 unit packages and 26 integration packages green after the
full sequence, not per-PR alone.

| # | Title | Note |
|---|---|---|
| 1242 | Lazy-load thumbnail images | |
| 1224 | fix(ui): remove global 1210px body min-width | |
| 1203 | Bump cel-go | |
| 1253 | Pre-size edit merge slices | |
| 1257 | Pre-alloc fingerprint filter | |
| 1254 | Cache roles at auth boundary | |
| 1252 | Pre-alloc draft slices | |
| 1260 | Memo list components | |
| 1263 | fix(query): recursive CTE ParentStudio filter | |
| 1255 | Avoid separate os.Stat in ReadFile | |
| 1262 | fix: Extend scenes.title to text | **renumbered** — see below |
| 1265 | Bump react-router-dom | |
| 1271 | Deduplicate ids in multi-ID includes-all | |
| 1274 | Bump otel/sdk | |
| 1223 | fix(ui): placeholder for deleted images in edit diffs | |
| 1219 | Require draft ownership on edit submission | |
| 1241 | Avoid duplicate tab search requests | |
| 1268 | Update github actions | |
| 1272 | Add an id criterion to SceneQueryInput | |
| 1249 | fix: high-risk n+1 query fields | |
| 1270 | Honour criterion modifier in scene fingerprints filter | |
| 1267 | Bump vitest | |
| 1212 | WIP Add scene merge form | |
| 1227 | Bump grpc to 1.83.1 | conflict resolved: kept our otel 1.45.0 |
| 1275 | Bump otlp/otlptrace | conflict resolved: kept our otel 1.45.0 |
| 1273 | Bump otlp/otlptracegrpc | conflict resolved: kept our otel 1.45.0 |

### The one merge that needed a manual fix

**#1262's migration was renumbered 76 → 88.** It shipped
`76_scene_title_text.up.sql`, but this fork already had
`76_add_user_trust.up.sql` for the trust rollup. golang-migrate keys on the
numeric prefix, so two files claiming version 76 is a hard startup failure
(`duplicate migration file`), and it took down every integration test that boots
a database. The per-PR check passed because each PR was green *before* this one
landed; only the accumulated tree failed. Verified no reachable database had
applied version 76, so the renumber needed no data reconciliation.

This is why the per-PR gate is necessary but not sufficient — a full run at the
end is the only thing that catches a collision between two individually-fine
ports.

## Not merged (14)

| # | Why not |
|---|---|
| 928 | **Destructive.** Downgrades the base image `postgres:18` → `postgres:16` and drops the `pg-spgist_hamming` build that pHash fingerprint matching depends on. Merging breaks the production image. |
| 1269 | **PORTED 2026-09-30** (`31edfd7b`). Was declined *for cause* — "pins schemaVersion 76 against our 88" — and that is still true of the patch but no longer of the outcome. Same two deviations as #1183, both forced. (1) Migration renumbered **76 → 91**: we already have TWO migrations numbered 76 (`76_add_user_trust`, `76_scene_title_text`), so a third would make the prefix ambiguous for golang-migrate. (2) The `schemaVersion = 76` pin **dropped**, not renumbered — the constant was deleted here deliberately and its comment records that it silently skipped migration 76 forever. One real union conflict: `resolver_model_performer_edit.go` had #1225's `[]*models.Image` widening colliding with #1269's new `Genitals` resolver, and taking either side drops the other feature. Coverage is genuinely good — create, find, edit, draft, converter both ways, enum resolution — but only after checking: an `-run Genital` probe matched nothing and looked untested, because those assertions live inside other test functions. Positive control: emptying the migration yields `column "genitals" of relation "performers" does not exist (42703)`. |
| 1086 | **PORTED 2026-09-30** (`82cf90ba`). Recorded as superseded, which is true of *upstream* — their master already merged it — and was false of *us*: our `EditCard.tsx` predated the change. Verified by fetching upstream's current file, which already carries both the `compact` prop and a `VoteBar` at line 79. The one-line patch would not apply because its hunk context expects `showVoteBar &&` where our line reads `!compact &&`; applying it mechanically would have **reverted compact support that both forks now share**. Kept the now-redundant `!compact` guard on its new home rather than diverging from upstream over a cosmetic change. |
| 1278 | Superseded. Wants a clearer cooldown error, but this fork already has a better one — `CooldownError` with `RetryAfter`, against upstream's bare `errors.New`. |
| 1227→ already merged; remaining below | |
| 1123 | Frontend conflict in two form components against #1212 and #1216-era changes. Needs a real UI decision about which pending-URL behaviour wins, not a mechanical merge. |
| 1076 | **SUPERSEDED — verified 2026-09-30, nothing to port.** The record said "touches criterion handling that #1270 and #1271 also changed", which is true but describes a *conflict*, not a *disposition*. Measured: the PR adds exactly **9** criterion applications (height, band_size, waist_size, hip_size, career_start_year, career_end_year, eye_color, hair_color, breast_type) and **all 9 are already in `internal/service/performer/query.go` on both branches**, at lines 204–229. Every column it filters on exists in our migrations. `gh` still reports CONFLICTING, which is now true and irrelevant — there is nothing left to apply. |
| 1225 | Conflicts in generated GraphQL code. Needs `make generate` plus a review of whether its 3 schema files duplicate #1216's work. |
| 1183 | **PORTED 2026-09-30** (`8c8160b5`), with two forced deviations. Four keyset-paginated *Changelog queries (scene/performer/studio/tag), genuinely absent here, + 220 lines of upstream tests. (1) Migration renumbered **73 -> 90**: we already own `73_scene_code_trgm_index.up.sql` and two files claiming 73 is a golang-migrate hard-startup failure. (2) The `schemaVersion = 73` pin **dropped, not renumbered** — `database.go` deleted that constant deliberately; its comment records that it was 75, so migration 76 was shipped and never applied, in every environment, silently. Applying the PR's hunk would reintroduce a bug this fork already fixed, in the file documenting the fix. *Added a test upstream did not have:* the PR's own tests pass with the migration deleted (Postgres will sequential-scan a small table), so the feature was covered and the performance claim was not. `TestChangelogIndexesExist` asserts each index exists **and** carries `INCLUDE (deleted)` — the pre-existing sort indexes are PARTIAL `WHERE deleted = false` and cannot serve a tombstone, so a name-only check would pass an index that still fails the migration's own stated requirement. Both controls fail as they should. |
| 1155 | Draft, upstream-conflicting, 13 conflicting files, 8 generated. User-mention notifications. |
| 878 | Draft, upstream-conflicting, 3 files. `findUpdatedScenes`. |
| 1248 | 6590-line dependency bump across 4 files; needs its own dependency review and lockfile audit, not a side effect of a port. |
| 1215 | +16.5k across 154 files, 5 generated/schema. Image labels for performers. |
| 1216 | +29k across 222 files, 5 generated/schema. Performer edit form cropping. #1215 and #1216 are 24 commits by the same author and likely want landing together. |
| 1266 | Conflicts in performer weight handling that overlaps this fork's `internal/service/elo` weight work. Needs a deliberate reconciliation of two independent weight implementations. |

The last group is not a failure to try — each is a feature port rather than a
merge, and four of them would need schema regeneration, UI validation and a
judgement call about overlapping existing work. They are the next unit of work,
not part of this one.

## Dispositioned 2026-09-30 (session: R074 guard) — 5 PRs, not 40

Re-measured rather than trusted. `gh pr list --state open` now returns **45**
open PRs, not the 40 this table was written against, and **five of them had no
recorded decision at all**. They are dispositioned below. Measured on
`stashapp/stash-box` at 2026-09-30 17:20.

| # | Decision | Why |
|---|---|---|
| 591 | **Decline** | Compose modernisation: drops `container_name`, drops `links:`, adds `image: stashapp/postgres:latest` and `POSTGRES_INITDB_ARGS`. Our `docker/production/docker-compose.yml` still has all of `container_name: postgres`, `links:`, so it applies — but **declined for cause**: adding `image:` alongside the existing `build: ./postgres` means a `docker compose pull` silently substitutes the published image for the locally built one. That image is what `pg-spgist_hamming` and `pg_search` are installed into (see `docker/production/postgres/Dockerfile`), so pulling the wrong one is the same class of breakage as declined PR #928. Worth doing, as a deliberate compose change with the image pinned — not as a port. |
| 708 | **Decline** | Lets an admin update another user's edit. Conflicting upstream, and the conflict is the point: our tree has no `validateUserOrAdmin`, and the ownership check lives in the service (`internal/service/edit/service.go:1214 validateEditUpdate`) rather than the resolver upstream moves it to. Porting it also weakens #1219 (already merged) which *required* draft ownership — 708's whole purpose is to admit admins past ownership. That is a product decision about who may edit moderation history, not a mechanical merge. **Needs an owner decision, not a port.** |
| 736 | **Decline** | Removes `url` from `ImageCreateInput`/`ImageUpdateInput`. Only a schema change with no resolver change, so it does not compile against our tree: `internal/api/scene_edit_integration_test.go:356` calls `ImageCreate(ctx, models.ImageCreateInput{URL: &imgURL})`. Removing the field breaks that test. And the field is *load-bearing for R074* — `images.url` is one of the seven address-shaped columns, and it is the served one. Removing it would change the R074 surface, which is a decision about the guard rather than a port. Do not re-litigate without deciding what R074 does about served URLs. |
| 761 | **Decline** | Adds `phash_duration_cutoff` config. 16 lines, upstream-conflicting, and it is a **product** choice, not a fix: the value silently DROPS fingerprints under a duration cutoff, with no user-visible effect. That is data loss dressed as a config option, and the README line documents it as "quietly dropped". Needs an owner decision; if taken, it belongs on `issue-fixes` as a port with a test that a short scene's fingerprint is dropped. |
| 871 | **Decline** | Draft, `mergeable=UNKNOWN`, 483 lines across 9 files including `graphql/schema/schema.graphql`, `generated_models.go`, `generated_exec.go` and `querybuilder_scene.go`. Same shape as already-declined #1183/#1155: mostly generated code, needs `make generate` plus a review of a 483-line feature. It is a draft upstream, so the conflict set will move again before it is ready. Not a mechanical merge. |

**Every open PR now has a recorded decision: 26 ported, 19 declined.** The five
above are the three that need an owner decision (708, 736, 761) and the two that
are genuinely mechanical-but-wrong (591's image/build collision, 871's draft).

**What I did NOT do, deliberately.** None of the five were ported. Porting 591,
708 or 736 would each break something (591 the postgres image, 708 #1219's
ownership guarantee, 736 an existing test), and 761 needs a product call. The
goal's own rule — "a ported PR may not remove a non-negotiable to be merged" —
applies to all three.

## Reusable

`scripts/port-upstream-prs.sh` runs the whole sequence: smallest blast radius
first, codegen for schema-touching PRs, then build + unit tests per PR, aborting
and recording any that fail. The summary line per PR is the record.

**The counting command, corrected.** The obvious one is
`gh pr list --repo stashapp/stash-box --state open`, which undercounts silently
as upstream PRs land. Compare against the numbers in the table rather than the
count in the goal document:

```bash
gh pr list --repo stashapp/stash-box --state open --limit 200 \
  --json number,title,isDraft,updatedAt
```
