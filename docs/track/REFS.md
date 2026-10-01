# Non-branch refs in this repository

`git branch` shows only `main`. Three namespaces exist anyway, and each is deliberate.
Deleting any of them loses something the remotes cannot supply.

## `refs/pr/*` — the upstream PR heads (40 refs)

One ref per upstream PR this fork has a recorded decision about, pinned to the exact
upstream commit the decision was made against. `docs/plan/upstream-pr-port.md` records
the decision; these refs record *what was decided about*. 28 of the 40 are ancestors of
`main` because the port landed and upstream's head came with it. The other 12 are not,
and are the only local copy:

| ref | disposition | why the ref is still needed |
|---|---|---|
| `pr/928` | Declined | Downgrades `postgres:18`→`16` and drops the `pg-spgist_hamming` build pHash matching needs. Nothing on `main` references it, so this is the only record of what was rejected. |
| `pr/1076` | Superseded | Adds 9 criterion applications already present via #1270/#1271. Nothing to port, but the measurement is only reproducible against its head. |
| `pr/1155` | Declined | Draft, 13 conflicting files. |
| `pr/878` | Declined | Draft, 3 conflicting files. `findUpdatedScenes`. |
| `pr/1278` | Superseded | Wants a clearer cooldown error; this fork has `CooldownError` with `RetryAfter`. |
| `pr/1086` `1123` `1183` `1225` `1248` `1266` `1269` | Ported | The port diverged — renumbering, conflict resolution, or a partial port — so upstream's head is not an ancestor of `main`. |

To refresh from upstream:

```sh
git fetch upstream 'refs/pull/*/head:refs/pr/*'
```

## `refs/preserved/*` — 28 superseded stash snapshots

From `git stash` during the 2026-09-28/29 sessions, before the branch restructure.
Every one is a `WIP on master:` / `On master:` / `untracked files on master:` commit.

These are **not** lost work, checked commit by commit against `main`:

- 28 unreachable commits; 16 touch files at all, and 4 of those are byte-identical to
  `main` already.
- The rest are earlier *drafts* of things `main` later did differently. Clearest
  example: `internal/service/federation/client.go` gains a `resolver Resolver`
  injection point so tests can bypass the R074 dial-time guard. `main` instead has
  `dialguard.go` and `client_test.go`. Re-applying the snapshot is a regression, not
  a rescue.

Pinned rather than deleted so the decision is auditable and reversible;
`refs/preserved/stashbox-pre-rename` is the tip. To drop them once satisfied:

```sh
git for-each-ref --format='%(refname)' refs/preserved/ | xargs -n1 git update-ref -d
git reflog expire --expire=now --all && git gc --prune=now
```

## `refs/remotes/upstream/*`

Left alone. `upstream/master` is `b4b8aef2`, the baseline every port is measured
against; `git log upstream/master..main` is how this fork's own work is counted.
`upstream/develop` and the `dependabot/*` refs are upstream's, not ours.

`refs/remotes/{origin,forgejo}/master` no longer exist — those remotes are on `main`.
`branch.main.merge` points at `refs/heads/main`; it was still `refs/heads/master`
after the rename, which would have made every pull fetch a ref the fork no longer has.