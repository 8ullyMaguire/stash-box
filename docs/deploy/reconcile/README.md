# Reconciling a deployment whose database was migrated by another lineage

Written 2026-10-15 while moving the live instance from a pre-consolidation lineage onto this
repository's schema, keeping its 4.1M rows. The incident record is
`docs/track/HANDOFF-deployment-lineage-reconcile-2026-10-15.md`; this directory is the machinery, in
the order it ran, so the same operation can be repeated or audited.

Every script here talks to the deployment over `ssh thinkcentre` and to its database through
`docker exec stashbox-pg` (the container's own client — the host's `pg_dump` is 16 against a server
18 and refuses, which silently produces empty diffs if you do not notice).

| order | script | what it answers |
|---|---|---|
| 0 | (manual) | `go version -m <deployed binary> \| grep vcs.revision`, then `git cat-file -t <sha>` in the repo that is supposed to own it, then read the deployment's `schema_migrations`. This is the five-second check that decides whether any of this is necessary. |
| 1 | `measure-gap2.sh` | Snapshot. Table sets on both sides. Which tables a carry would move, and the row counts in them. |
| 2 | `measure-gap3.sh` | Are the deployment-only tables empty (i.e. is anything lost)? Column-level differences on the shared tables. |
| 3 | `blockers.sh` | The three things that decide whether a carry is mechanical: main-side NOT NULL columns without a default that a carried row cannot fill, values that would be dropped, and whether the role is a superuser (needed to suspend FK triggers). |
| 4 | `carry-plan.sh` | Per-table column intersection by NAME, and what main's own migrations have pre-seeded. |
| 5 | `decisions.sh` | Which seeded tables are identical (skip them) and which carry DIFFERENT ids for the same names (`users`, `user_roles`, `sites` — replace them, or every reference points at the wrong id). |
| 6 | `carry.sh` | The carry itself, then row-count comparison, then FK integrity with the triggers back on. |
| 7 | `switchover.sh` | Rename the databases (the name is preserved so the DSN keeps working), back up the config, append `content_fetch_sources: true`, swap the binary, start, and report. |
| 8 | `prove-switch.sh`, `prove-negative.sh` | Behavioural proof that outbound fetching is ON: one allowed URL fetched (robots read, page fetched, outcome classified), one blocked host REFUSED with no request made, plus an egress probe outside the app so a network failure is not read as a code failure. |
| 9 | `health.sh` | The instance's state afterwards: migration tip, site, data counts, job registration, errors, disks, and the preserved old database. |

## Two traps that cost a run each, both about `docker exec` and stdin

* **`\copy ... FROM STDIN` through `docker exec` needs `-i`.** Without it the container's stdin is
  attached to nothing: large tables die on SIGPIPE and small ones silently copy ZERO rows, so one run
  reports `MISMATCH` and `FAILED` side by side and looks like a data problem.
* **`-i` on every call, however, eats the `while read` loop's own input** — the loop reads the plan
  file on stdin, so the first `-i` call consumes the rest of it and the loop stops after one table.
  Only the receiving `\copy` gets `-i`; everything else gets `</dev/null`.

## The rule this operation exists to enforce

A migration NUMBER is not a promise about a FILE. Two lineages that reuse numbers 96/98/103 produce
two schemas that both claim version 103 and share almost nothing else. Applying one lineage's
migrations on top of the other's schema produces a third schema nobody designed, whose differences
are invisible until they are expensive. When the deployed binary's revision is not in the repository,
the only known-good target is a database that repository's own binary migrated from empty.
