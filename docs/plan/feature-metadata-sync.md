# Metadata sync from stashdb.org — incremental, with a conflict policy

Status: **Built, verified 2026-10-01.** Performers only, as scoped. All four
tests named in Verification below exist and pass, and the two claims the design
rests on are mutation-verified rather than merely asserted:

- `shouldStop` with the margin removed, and with it inflated a thousandfold, are
  both KILLED by `TestStopCondition` -- the safety margin is load-bearing.
- `diffPerformerFields` with the absent-is-not-a-change rule dropped, inverted, or
  with change detection short-circuited, are all KILLED by
  `TestUpdatePreservesUnsetFields`. That rule is what stops an absent upstream
  value from clearing a curator's height.

Scope is performers, per the document's own Scope section; studios/tags/scenes are
explicitly deferred to a later step.

## Problem

An instance's metadata drifts from stashdb.org. Import tooling creates records;
nothing reconciles them. A correction made upstream — a fixed name, a corrected
birthdate, a studio that turned out to be a duplicate — never reaches an instance
that already has that performer.

Today's only option is a full re-import, which is insert-only: the `sdbimport`
package has **zero** `Update*` calls. A re-run walks every page of the source,
creates nothing, and reports `skipped 1103442` at ~9.5 hours of API traffic.

## What the API already gives us

Verified against the live API with a real session cookie, 2026-10-01:

- `PerformerSortEnum` includes `UPDATED_AT`
- `Performer` exposes `updated`
- Sorted by `UPDATED_AT DESC`, pages are **monotonically decreasing** across the
  page boundary (p1 oldest `2026-09-30T14:54:26`, p2 newest `14:54:11` — strictly
  older).

So incremental sync needs **no server change**: sort by recency, walk until a
record is older than the watermark, stop.

What is missing is an `updated_at` **filter**. `PerformerQueryInput` has no date
criterion, so the client pages down and stops itself rather than asking for a
range.

## Scope

**Performers only**, in this step. They are the richest metadata surface and the
clearest case. Studios/tags/scenes follow the same shape once the seam is proven.

## Design

### Watermark

A single row per (entity, source) holding the highest `updated` successfully
processed. Stored locally, not derived: deriving it from local rows would be
wrong the moment a run is interrupted or a mode other than `upstream-wins` writes.

### Stop condition, with a safety margin

Stop when a record's `updated` is older than `watermark - margin`. The margin
exists because recency ordering is *observed*, not documented — three pages is not
a specification. A run that re-scans a small window is cheap; a run that silently
skips a boundary row is not.

### Three modes

| Mode | Behaviour |
|---|---|
| `latest-wins` (default) | Most recently written value survives, whichever side wrote it. |
| `upstream-wins` | stashdb.org overwrites local. |
| `local-wins` | Local edits preserved; upstream fills only empty fields. |

### Provenance

`latest-wins` is undecidable without knowing when each side last wrote a field.
A `metadata_sync_provenance` table records `(entity_type, entity_id, field, source,
written_at)`.

**This is the expensive part and it is why the modes differ in cost:**
- `upstream-wins` needs no provenance at all — it is a straight overwrite.
- `local-wins` needs no provenance either — "is the local field empty?" is
  answerable from the row.
- `latest-wins` needs provenance on **both** sides, and the upstream side is not
  ours to write.

That asymmetry is the honest cost of the default, and it is why the default is not
simply "overwrite". The implementation records provenance for local writes and, for
upstream, compares against the upstream `updated` it already fetched — a good
approximation, not a guarantee, and documented as such.

## Decisions

**D1 — Performers only.** Smallest complete slice. The seam (`syncOne`) is the
deliverable; other entities are more of the same.

**D2 — Watermark is stored, not derived.** A derived watermark is wrong after an
interrupted run.

**D3 — Safety margin of 1 hour on the stop condition.** Re-scanning an hour of
upstream changes is cheap; missing a boundary row is not.

**D4 — Dry-run is the default mode.** A first run against 110k curated records
must not write. `-dry-run` reports what *would* change, per field, per entity.

**D5 — Provenance records local writes only.** Upstream's write time is the
`updated` we already fetched. Stated as an approximation in the code, not hidden.

**D6 — No auto-scheduling.** The job is built and documented; whether it runs on a
timer is the operator's call, because it writes to curated data.

## Risks

- **Scenes are not deduplicated** by the existing importer (title+date is ambiguous).
  Excluded here; performers are deduplicated by name.
- **`Performer.Update` takes a full input.** A partial update may clear fields the
  caller did not set. The sync must send a **complete** input assembled from the
  fetched upstream record, never a sparse one. This is the single most dangerous
  line in the implementation and gets its own test.
- **Aliases and URLs are lists.** "Changed" means the set differs, not the order.

## Verification

| Check | Command | Expected |
|---|---|---|
| Build | `go build ./...` | exit 0 |
| Stop condition | `go test ./internal/sdbimport/ -run TestStopCondition` | PASS |
| Update does not clear unset fields | `go test ./internal/sdbimport/ -run TestUpdatePreservesUnsetFields` | PASS |
| Mode semantics | `go test ./internal/sdbimport/ -run TestModes` | PASS |
| Idempotent re-run | `go test ./internal/sdbimport/ -run TestIdempotent` | PASS |
| Full suite | `make it` | all packages green |

The `TestUpdatePreservesUnsetFields` test is the one that earns its keep: it fails
if a sparse update clears a field, which is the failure mode that would silently
destroy a curator's `height`.
