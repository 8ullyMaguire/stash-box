# The sync service — stashdb.org → this instance, running on its own

Status: **steps 1 and 2 are BUILT** (commit `a3ea8407`'s successor; migration 143, `watermark_store.go`,
7 integration tests, 8 mutation probes 8/8). **Steps 3–6 are PLAN.** Written 2026-10-16, after a live survey of
both ends (this instance at commit `a3ea8407`, deployed on thinkcentre 2026-10-16 12:28, and stashdb.org's
public API).

**The source's credential no longer works**, and that is a blocker for step 3 onward but not for 1 and 2: with
the credentials in `~/.sdb_auth.env` (unchanged since 2026-09-30, when they imported 1.1M scenes),
`queryPerformers`, `queryScenes` and `queryStudios` now answer **`not authorized`**. The same probe settled two
things the plan needed: the timestamp field is **`updated`** (not `updated_at`), and the query inputs have **no
`updated_at` filter** — so increments work by *sort-and-stop*, which is exactly what the existing `shouldStop`
plus its margin does, and the reason that function is not a bare comparison stands.

## What already exists, and the three things that do not

The survey came first, because three of the five things this feature needs are already here:

| Piece | State |
|---|---|
| `cmd/sdbimport` — a paged, resumable importer for tags, sites, studios, performers, scenes | **BUILT**, running, mutation-verified |
| `internal/sdbimport/syncdiff.go` + `syncwrite.go` — the reconcile: `diffPerformerFields`, `SyncWalk.Step`, `applyChanges`, the never-clear rule, `nonEmpty`, latest/upstream/local modes | **BUILT and tested — AND NOTHING CALLS IT** |
| `internal/storage` — image bytes: a sharded path under `image_location`, an S3 backend, write-verify-read | **BUILT** |
| A **caller for the sync** | **MISSING.** `grep -rn 'sdbimport\.\(Sync\|RunSync\)' cmd/` is empty: the reconcile is a library with no entry point, which is the "a thing nothing asks for" shape this project keeps finding |
| A **durable watermark** | **MISSING.** `Watermarks` is a Go map (`syncwrite.go:20`); "since last sync" does not survive the process |
| **Images / thumbnails** | **MISSING entirely.** `grep -n 'image\|photo' internal/sdbimport/*.go` is empty, and the live database confirms it: `performers=111243  scenes=1104375  images=0  performer_images=0` |
| A **scene idempotency key** | **MISSING.** `cmd/sdbimport`'s own doc: *"Scenes are NOT deduplicated… Re-running with `scenes` therefore duplicates them."* A repeatable workflow cannot have that property |

So this is not "write a sync". It is: **give the existing reconcile a caller, a memory, an image path and a
repeatable scene identity, then run it on a timer.**

## The three decisions, each with the alternative named

**1. A new table `sync_watermarks`, not a file.**
Alternative declined: a JSON file beside the config. A watermark is *derived state about a run* and the run
writes to Postgres in the same transaction as the records it changes; a file would be a second thing to keep
consistent across a crash, and the instance already has a database that survives one. The key is
`(entity, source)` — not `(entity)` — because the same instance may later sync two sources, and a watermark
shared between them would make one source's walk skip the other's records.

**2. Scene identity: `(source, source_scene_id)` stored in a new column, not `(title, date, studio)`.**
Alternative declined: matching on title+date+studio. `cmd/sdbimport` already explains why that is wrong — two
genuinely distinct scenes can share all three, and the failure is a *silent* merge of two records. Stashbox
scenes have no external id today, so there is nothing to key on: a nullable `scenes.source_scene_id` (plus a
unique index on `(source_scene_id)` where not null) is the smallest honest addition. **This is the one place
this plan adds a column rather than reusing something, and the reason is that nothing existing carries it** —
the audit is in the "what already exists" table, and `scenes` has no `url`, no `checksum` column of its own.
The same column, backfilled once from the existing rows by a separate one-shot pass, is what makes the first
incremental run able to tell "already imported" from "new".

**3. Thumbnails go through `internal/storage` via the image service — NOT through `mediaplane`.**
`internal/service/mediaplane` (the variant ladder, slices 10c-1/10c-2) stores *resized variants* keyed
`(id, width)` and has its own `media_dir`. `internal/storage` stores *the instance's own image bytes* keyed by
image id in `image_location`, and `internal/service/image` + the resize cache already serve them. **The sync
must use the second one**: it writes images the instance holds, which is exactly what `storage.Image()` is for,
and using the ladder would put a second image store beside the first for the same bytes. The ladder stays what
it is — a bounded thumbnail store for the bytes the instance serves — and the relationship (two directories,
two purposes, the overlap invariant) is already documented in the plan's slice 10c-1. **If the two are ever
pointed at the same directory the ladder refuses to start, which is the invariant doing its job.**

## Steps, each with its verification

All steps are implemented in `internal/sdbimport` + `cmd/sdbimport`, built on thinkcentre
(`go build -o /tmp/… ./cmd/sdbimport`), deployed beside the running instance, and verified against the live
database. Nothing here is verified by a unit test alone: the last step of every part is a real run.

### Step 1 — the watermark table (migration 143) — **BUILT**

```sql
CREATE TABLE sync_watermarks (
    entity   text        NOT NULL,
    source   text        NOT NULL,
    watermark timestamptz NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (entity, source)
);
```
`source` is the source's host (`stashdb.org`), written from the client's configured endpoint rather than
hard-coded, so a second source does not collide with this one's rows.

**Verification:** `docker exec stashbox-pg psql -U stashbox -d "stash-box" -tAc "\d sync_watermarks"` prints the
table; the migration is additive (no DROP, no ALTER of an existing table) — checked with the same
`grep -ilE '^\s*(DROP|TRUNCATE|DELETE FROM)'` audit the deploy uses, and the only match must be a `.down` file.

### Step 2 — watermarks loaded and stored, `internal/sdbimport/watermark_store.go` — **BUILT**

**Adjustment from the sketch above, made while building:** the writer is `SaveWatermark` (ONE entity), not
`SaveWatermarks` (a map). The per-entity call *is* the rule the section describes — a mark is saved once per
entity after that entity's walk returns — and a bulk writer would make it possible to save a map that contains
entities whose walks never finished, which is the first lie this store exists to prevent. `LoadWatermarks` stays
bulk because reading is not the dangerous direction.

```go
func LoadWatermarks(ctx context.Context, q *queries.Queries, source string) (Watermarks, error)
func SaveWatermarks(ctx context.Context, q *queries.Queries, source string, w Watermarks) error
```
`Watermarks` already has `Get`/`Set`; this gives it a store. Saving happens **once per entity, after that
entity's walk completed**, never mid-walk: an interrupted run must replay, not skip.

**Verification, as built.** `internal/sdbimport/watermark_store_test.go` (pure): the source key derived from
the endpoint host, including the two spellings of one source and the near-miss entity names. Plus seven
integration tests against a real Postgres through the same harness `internal/api` uses (`POSTGRES_DB`, migrations
at startup, tables dropped at teardown, `-p 1`): a mark survives and is read back; **an entity with no mark is
absent rather than zero** (the runner's "walk in full" cue); **two sources keep separate marks**; **a mark never
moves backwards** — with the forward direction asserted too, so this is not "refuse everything"; a near-miss
entity name is refused AND writes nothing (asserted by counting rows); a zero mark is refused; a blank source is
refused. Migration 143 applies cleanly through the harness, which is how the migration itself is verified before
it ever reaches the live database.

**MUTATIONS: `scripts/mutate-sync-watermark.py`, 8 probes, 8/8 killed, every restore green.** The plan
predicted three of them; the battery has eight, because building it turned up five more ways this table can lie
— a near-miss entity name given its own row, "never synced" reported as "synced to the beginning of time", a
walk that read nothing saving a position, a blank source becoming a usable key, the key being the whole endpoint
instead of its host, and the entity list losing the importer's order. **M1 (the source dropped from the read)
and M2 (the mark allowed to rewind) are the two the plan named**, and both die on the test written for them.

The probes run the INTEGRATION build, and the script refuses to start without `POSTGRES_DB`: two of the three
guarantees are SQL statements, and a fake with the same shape would assert what the test file already
believes.

### Step 3 — the runner, `internal/sdbimport/runner.go` + `cmd/sdbimport -sync`

Flags added to the existing command rather than a new binary: `-sync` (run the reconcile instead of the
import), `-sync-modes performer=latest-wins,scene=local-wins` (per-entity mode; the default is `latest-wins`,
the package's own default, and an unknown mode is a hard error as `ParseMode` already requires),
`-sync-full` (ignore the watermark and walk everything — the repair path), `-lock <path>`.

The run order is the importer's order (tags, sites, studios, performers, scenes), and the reason is the same:
a scene resolves its performers. Per entity the runner:

1. `LoadWatermarks` → the entity's watermark (zero on a first run, which means "no stop condition").
2. Walk source pages in `updated` order, stopping at the first record older than `watermark - margin`
   (`shouldStop`, 15 minutes; the margin's justification is already in the source: recency ordering on the
   source is *observed, not documented*).
3. For each record: look up locally, diff, and either create (an id the instance has never seen) or apply the
   winning fields (`planUpdate` + `applyChanges`, which already refuses to write an unset field over a
   curator's value).
4. `SaveWatermarks` for that entity. Then the next entity.

A **single-instance lock** (`flock` on the lock path, default `$XDG_RUNTIME_DIR/stash-box-sync.lock`) so a
timer run and a manual run cannot overlap: two concurrent walks would each diff against a database the other is
changing, and the report would be nonsense. A held lock makes the second run **exit 0 with `skipped: locked`**
rather than fail — a timer that fails because the previous run is still going is a timer that pages someone for
nothing.

**Report:** one line per entity on stdout, plus `-json` writing the same numbers as JSON to a file
(`~/stash-box-deploy/import-logs/sync-<timestamp>.json` in the service), so the timer's history is parseable
without a log parser.

**Verification:** `-sync -dry-run` against the live instance prints changes and writes **nothing** — asserted by
taking the watermark and the row counts before and after and asserting both are unchanged. Then a real
`-sync -limit-performer 50` (adapted from the existing limit flag) must update rows and move the watermark
forward by exactly the boundary it reports.

### Step 4 — thumbnails: `internal/sdbimport/images.go` + `-images <entities>`

For each entity, the source's GraphQL payload is extended with the image the source publishes for it
(`images { url width height }` on `Performer`/`Studio`/`Scene`). For each record with an image and no local
image:

1. **Fetch** through the instance's own politeness rules, reusing the content plane's decisions rather than
   inventing new ones: one request per host per `content_fetch_min_interval` (config, currently 2s), a
   `stash-box/<version> (+<instance url>)` User-Agent, a 10 MB byte cap, the host's `robots.txt` honoured, and
   a per-host refusal cache so a source that refuses to let one image be fetched does not cost a request per
   image for the rest of the run.
2. **Validate before storing**: `http.DetectContentType` must say `image/*` and the bytes must decode
   (`image.DecodeConfig`), because a 200 with an HTML error page is a real answer from real CDNs — the same
   rule slice 10c-2 put in the media plane, applied at the boundary where it belongs.
3. **Store** through the image service, so the row, the file and the checksum are written by the same code a
   user's upload goes through: create the `images` row, `storage.Image().WriteFile`, link it to the entity
   (`performer_images` / `studio_images` / `scene_images`), with `imagetype`'s "thumbnail ?" decision taken
   from the source's own dimensions rather than guessed.
4. **Idempotent**: an entity that already has an image is skipped before any HTTP request, so the second run
   costs one indexed query per record and no traffic.

**Verification (live, small, measurable):** `-images performers -limit-performer 25` on the host, then
`SELECT count(*) FROM images` must go from 0 to exactly 25 and `SELECT count(*) FROM performer_images` must
match; a second identical run must fetch nothing new (assert by the run's own `fetched 0` line and by the count
staying at 25); and a request for one of them through the running instance must return bytes — the end-to-end
check, not the row count. Failure arms: a record whose URL 404s, one whose URL returns HTML, one whose host is
robots-disallowed — each must be reported by name and skipped, and the run must not stop.

### Step 5 — the service: `stash-box-sync.service` + `stash-box-sync.timer`

Two user units on thinkcentre, beside `stash-box.service`:

```
# stash-box-sync.service
[Unit]
Description=stash-box metadata + image sync from stashdb.org
After=stash-box.service

[Service]
Type=oneshot
WorkingDirectory=%h/stash-box-deploy
EnvironmentFile=%h/.sdb_auth.env
ExecStart=%h/stash-box-deploy/bin/sdbimport -config %h/stash-box-deploy/config/stash-box-config.yml \
          -sync -images performers,studios,scenes -lock %t/stash-box-sync.lock \
          -json %h/stash-box-deploy/import-logs/sync-latest.json
TimeoutStartSec=infinity
StandardOutput=append:%h/stash-box-deploy/import-logs/sync.log
StandardError=append:%h/stash-box-deploy/import-logs/sync.log
```
`EnvironmentFile` on a oneshot service rather than a shell wrapper: the credentials come from
`~/.sdb_auth.env` (mode 600, already the house convention for `SDBSRC_PASS`), and systemd redacts an
`EnvironmentFile` in `systemctl show` output where an inline `Environment=` would not. **The password never
appears in `ExecStart`, in `ps`, or in this repository.**

```
# stash-box-sync.timer
[Timer]
OnBootSec=15min
OnUnitActiveSec=6h
Persistent=true
```
**Every six hours, and here is the reasoning rather than a number**: the source is a community database whose
`updated` timestamps move in bursts of a few hundred records a day; a six-hourly incremental run reads the
first page or two of each entity and writes only what changed. Daily would be fine for metadata and wrong for
images (a new scene's thumbnail would be a day late for a reader who just found the scene); hourly would spend
most of its time on the walk's own overhead. `Persistent=true` so a run missed while the host is down happens
after it comes back, and `OnBootSec` so a reboot does not trigger an immediate full walk at the same moment
as everything else starting.

`RandomizedDelaySec=10min` is added deliberately: the source is someone else's server, and a fixed schedule
across many instances is how a crawler becomes a load spike.

**Verification:** `systemctl --user start stash-box-sync.service` runs it once by hand and its log line and
JSON report are read; `systemctl --user list-timers stash-box-sync.timer` shows the next elapse; a second
manual start while the first is running must exit 0 with `skipped: locked` (assert by running two in
parallel and reading both exit codes and both report files).

### Step 6 — the repeatable full run, documented and scripted

`scripts/sync-thinkcentre.sh` in this repository: pushes nothing, builds on the host, installs `sdbimport`
beside the running instance, then runs one full pass with `-sync-full -images` and prints the report. It is the
"add anything missing" button the operator asked for, and it is the same code path the timer uses — a manual
run is not a second implementation.

## What this deliberately does NOT do

- **It does not mirror the source.** This instance holds its own records; the sync reconciles fields for
  records it has and creates records it does not. It is not a replica and does not try to be one.
- **It does not delete.** A record removed upstream is left alone: a deletion upstream is usually a merge, and
  the local row is the one a curator may have voted on. The never-clear rule has a second half and this is it.
- **It does not import full-resolution originals.** The source publishes thumbnails; the storage verdict of
  2026-10-16 (0.35–3 TB against 1.6 TB free) is about originals and is unchanged by this feature.
- **It does not run the sync inside the web process.** A timer running a oneshot binary means a crashed sync
  cannot take the API down with it, and the operator can run the same binary by hand.

## Order of work, and what "done" means

1. Step 1 + 2 (the watermark, with its probes) — self-contained, no source traffic.
2. Step 3 (the runner + `-sync`), exercised with `-dry-run` and a 50-record real run.
3. Step 4 (images), exercised with 25 performers and one served byte.
4. Step 5 (the units), exercised by starting them by hand.
5. Step 6 (the script) + `docs/POST-sync-service.md` with the numbers a real run produced.

Each step is a commit with the verification it claims, pushed to both remotes, and the deployed instance is
never left on a build that has not been run against the live database.
