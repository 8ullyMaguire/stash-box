# HANDOFF — the deployment was running a binary from a lineage this repository does not contain

2026-10-15. Owner instruction: set `content_fetch_sources: true` in the deployment config and continue
developing stash-box to completion. Setting the switch turned out to require replacing the deployed
binary, and replacing the deployed binary turned out to require reconciling two schemas. This file is
the record of what was found, what was measured, what was done, and how to undo it.

## What was found

`content_fetch_sources` is a key this repository's code reads. The deployed binary (running since
2026-10-04) contains no such key. So the switch could not be set on the running instance; the new
binary had to be deployed. That is where it stopped being a one-line change, three ways:

1. **The deployed binary's commit is not in this repository.** `git cat-file -t 878b4bd0` → not a
   valid object. The binary reported `878b4bd0`, and no ref, branch, or unreferenced object here
   contains it. (The binary that WAS eventually deployed is `9c51857c`, "cron: say which posture this
   process is running -- the OFF line had no ON counterpart".)

2. **The two lineages re-use migration numbers for different objects.** The deployment's
   `schema_migrations` said 102; main's 102 is `field_verification_cascade`, and main's 103 is
   `scene_search_recall`. When the new binary was started against the live database it attempted 119,
   which means the marker meant something else entirely, and repair-on-repair produced the tell that
   settled it: the deployed chain's migration 103 **creates `field_verification_state`** (singular,
   one table) while this repository's 96 creates `field_verification_reasons` + `field_verification_states`
   (plural, two tables) and its 103 creates something else again. The table existed while the marker
   said 102 -- so the file the deployed chain applied is not the file that carries that number here.
   Numbers had been reused across a branch consolidation, and the deployment's database was migrated
   by the other lineage.

3. **Consequence:** an in-place "freshen the database" cannot end in a known state. Applying main's
   migrations on top of a foreign schema yields a third schema nobody designed, with differences that
   are invisible until something breaks. The only honest target is main's own schema.

## What was measured (all of it, before changing anything)

* The deployment's database: 110 tables, 4.1M rows, 2.4 GB. Main's fresh schema (a scratch database
  migrated by the new binary itself): 103 tables, 134 migrations clean.
* **30 tables exist only in the deployment** -- `instances`, `instance_theme`, `metadata_changes`,
  `quorum_rules`, `steward_arbitrations`, `reputation_attestations`, `attestation_policies`,
  `preservation_policies`, `peer_relationships`, `content_replicas`, `storage_allocations`,
  `anonymity_settings`, `user_hidden_tags`, `user_display_preferences`, `user_gravity_preference`,
  the singular `field_verification_state`, ... **Every one of them is EMPTY** (row counts measured,
  all zero). Nothing is lost by leaving that lineage's feature set behind.
* **80 tables exist in both**, and they hold all the data: `scenes` 1,104,375; `scene_urls`
  1,171,306; `performer_aliases` 181,622; `performer_urls` 136,573; `performers` 111,243;
  `performer_tattoos` 83,467; `performer_piercings` 31,019; `studio_urls` 16,497; `studios` 14,662;
  `tag_aliases` 8,875; `invite_keys` 19; `user_notifications` 6; `user_roles` 5; `users` 4;
  `sites` 361.
* **BLOCKER COUNT: ZERO.** No main-side NOT NULL column without a default is missing from a live
  table, so every carried row can be inserted. The only values dropped are three denormalised
  `normalized_url` columns (`scene_urls`, `performer_urls`, `studio_urls`), which main derives in code.
* `image_types`, `image_type_groups`, `image_type_conflicts` are seeded **identically** by main's own
  migrations (md5-compared), so they are skipped rather than duplicated.
* `users`, `user_roles`, `sites` are seeded by main's migrations with **different ids for the same
  names** (`root`, `StashBot`, `[deleted user]`, two placeholder sites), so main's rows are deleted
  and the live ones carried -- otherwise every reference into them would point at the wrong id. The
  live `users` include those same three names plus the real owner account.
* The role `stashbox` is a superuser, so `session_replication_role = replica` is available to suspend
  FK triggers for the load and to re-enable them for the verification.

## What was done

1. **Snapshot** (before anything): the deployment's own `pg_dump -Fc` to
   `~/stash-box-deploy/backups/stash-box-pre-rebuild-20261015-161250.dump` (371 MB).
2. **Fresh target**: `stash_box_main_scratch`, migrated by main's binary itself (so the schema is
   main's by construction, not by anyone's opinion of it), landing at 134, not dirty.
3. **Carry** 19 tables with rows, column-by-name intersection per table, `COPY (SELECT ...) TO STDOUT`
   piped straight into `COPY t (cols) FROM STDIN`, `session_replication_role = replica` on the
   receiving session. 4.1M rows in about two and a half minutes (`scenes` alone 95 s).
4. **Verify**: every table's row count compared live-vs-new (all equal), then FK integrity re-checked
   with the triggers back on: 0 orphan `scene_urls` → `scenes`, 0 orphan → `sites`.
5. **Switch over**: stop the unit; rename `stash-box` → `stash-box-pre-main-20261015-161621` and
   `stash_box_main_scratch` → `stash-box` (the name is preserved, so the DSN in the config still
   resolves and a rollback is one rename); back up the config; append `content_fetch_sources: true`
   with its reasoning; `bin/stash-box` + `bin/stash-box.main` → the deployed slot; start.

## Verification of the running instance (the point of all of it)

* `systemctl --user` active/running, **0 restarts**, `schema_migrations` = 134 clean, nginx `8011` →
  200, the frontend bundle served, **no errors or warnings in the log since the switchover**.
* The switch's own line: `content_fetch_sources is ON: registering 2 outbound job(s) -- the ingest
  drain and the health sweep...`
* **Behavioural, three ways, on the live instance** (a log line is a claim; a request that happened
  is evidence):
  * `https://example.org/page` queued → robots.txt read, page fetched, 404 → `status=failed
    outcome=reason=gone`. A complete outbound fetch cycle.
  * `https://example.com/` queued → its robots.txt could not be read (connection reset) →
    `reason=unreachable`. The same reset happens to `curl` from this host, so it is the network, not
    the app; and the guard behaved correctly by refusing to fetch without a robots.txt.
  * `example.net` put on the block list, then queued → `status=rejected`, `reason=this host is
    blocked`, **with no request made**. The takedown guard is enforced at the drain, not only at
    submission.
* Test rows and the block were removed afterwards; the queue and the block list are empty again.
* Data intact where the app reads it: 1,104,375 scenes, 1,171,306 scene_urls (all with a site),
  111,243 performers, 14,662 studios, 2,934 tags, 361 sites, 4 users.

## Rollback

Everything is reversible, in this order:

```bash
ssh thinkcentre
export XDG_RUNTIME_DIR=/run/user/1000
systemctl --user stop stash-box
docker exec stashbox-pg psql -U stashbox -d postgres -c 'ALTER DATABASE "stash-box" RENAME TO stash_box_main_failed'
docker exec stashbox-pg psql -U stashbox -d postgres -c 'ALTER DATABASE "stash-box-pre-main-20261015-161621" RENAME TO "stash-box"'
cd ~/stash-box-deploy
cp -a bin/stash-box.pre-main-20261015-161621 bin/stash-box
cp -a config/stash-box-config.yml.bak-before-main-20261015-161621 config/stash-box-config.yml
systemctl --user start stash-box
```

The old binary is a different lineage and its database is the one it was built for, so that pair comes
back exactly as it was. The carried database is not deleted by any of this: the failed name holds it.

## Notes for the next session

* **Never point a binary at a database you have not asked which lineage made it.** The check that
  would have caught this in five seconds: `go version -m <binary> | grep vcs.revision`, then
  `git cat-file -t <sha>` in the repository that is supposed to own it.
* The deployment's own database, converted to main's schema, is now the instance's database. The
  old lineage's 30 tables are gone from it; they were empty, and their features are not in this
  repository's programme.
* The programme's next row is **phase 8, slice 8e: collaborative lists** -- the only item left in
  phase 8, and the design decision it waits on is already written in
  `docs/plan/feature-content-aggregator.md` (a NAMED bounded collaborator set, the owner is not a row
  in it, `canView` gains exactly one arm, and the audit gains the `ActionAddItem`/`ActionRemoveItem`
  rows it promises but never wrote).
* The instance's `content_fetch_sources: true` means the ingest drain and the health sweep now make
  outbound requests on a schedule. That is the owner's decision of 2026-10-15, recorded with its
  guardrails in the config file itself.
