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
| 1269 | **Destructive.** Pins `schemaVersion = 76` in `internal/database/database.go`. This fork is at 88. Merging caps the schema and silently stops later migrations from being expected. |
| 1086 | Superseded. Moves a VoteBar that upstream has since removed; #1223 (merged) replaced that code. Its 1-line diff no longer applies. |
| 1278 | Superseded. Wants a clearer cooldown error, but this fork already has a better one — `CooldownError` with `RetryAfter`, against upstream's bare `errors.New`. |
| 1227→ already merged; remaining below | |
| 1123 | Frontend conflict in two form components against #1212 and #1216-era changes. Needs a real UI decision about which pending-URL behaviour wins, not a mechanical merge. |
| 1076 | Upstream already conflicts this one (their merge base moved). Touches criterion handling that #1270 and #1271, both merged, also changed. |
| 1225 | Conflicts in generated GraphQL code. Needs `make generate` plus a review of whether its 3 schema files duplicate #1216's work. |
| 1183 | Upstream-conflicting. 4 conflicting files, 3 of them generated. Bulk-update changelog queries. |
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

## Reusable

`scripts/port-upstream-prs.sh` runs the whole sequence: smallest blast radius
first, codegen for schema-touching PRs, then build + unit tests per PR, aborting
and recording any that fail. The summary line per PR is the record.
