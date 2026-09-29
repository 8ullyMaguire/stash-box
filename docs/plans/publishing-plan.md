# Publishing plan — issue-fixes branch vs features branch

Written at the request: "on next clean checkpoint I want to push to github with
issue solving fork as a branch and the one that not only solves the issues but
implements new features from the spec as main branch".

## EXECUTED 2026-09-29 — both branches now exist

The recommendation below was written on 2026-09-29 and **not acted on for 45
commits**. It is now real:

    issue-fixes   b6af8c80   48 issue fixes + 3 build fixes   (publishable upstream)
    master        d9b8d2be   issue-fixes + 44 feature commits (the roadmap)

`master` was rebased onto `issue-fixes` (44/44, one Makefile conflict resolved in
favour of `issue-fixes`). `issue-fixes` is an ancestor of `master`, so the merge
direction is one-way and the roadmap can never remove an upstream fix.

**The split was mechanical; verifying it was not.** Running `issue-fixes`'s own
suite — rather than assuming a branch frozen at commit 48 was green — found three
pre-existing upstream defects that master had been masking, including
`make it` running the integration packages in parallel against one database. Full
detail and reproduction in `docs/track/WORKLOG.md` session 43.

Both branches verified: `issue-fixes` 12/12 integration on three consecutive
runs, `master` 25/25.

## Current state, verified 2026-09-29

    local branch:   master
    remote origin:  https://github.com/stashapp/stash-box.git   (UPSTREAM, not a fork)
    merge base:     b4b8aef  2026-09-09  Use setup-go native cache (#1250)
    unpushed:       50 commits ahead, 0 behind
    gh account:     8ullyMaguire (token scopes include repo, workflow)
    fork of own:    NONE — `gh repo view` reports isFork:false on upstream

Two things this changes versus the request as stated:

1. **The branch is `master`, not `main`.** Renaming to `main` is a repo-wide
   change (upstream uses `master`) and is not needed to satisfy the request.
   Recommended: keep `master`, create ONE new feature branch.
2. **`origin` is upstream.** Pushing this work to `origin` would push a fork's
   work to the canonical repository. That is almost certainly not what is meant.
   A fork must exist first.

## One decision needed before any push

**Where do the two branches live?** This is the only genuinely ambiguous point,
and the answer changes what gets created on GitHub.

- **Option A (recommended) — fork, then two branches in it.**
  Create `8ullyMaguire/stash-box` as a fork, add it as a second remote, and push:
  - `issue-fixes` = the 50 commits of issue fixes, current `master` as-is.
  - `main`        = `master` + the spec features, merged in later.
  Upstream `stashapp/stash-box` stays untouched. The feature branch is a superset,
  so `main` is a descendant of `issue-fixes` and nothing is ever lost.

- **Option B — two repos.** Separate repos for fixes and for features. This
  breaks the superset relationship: the features repo would need `issue-fixes`
  merged into it anyway, and the history would diverge. Not recommended.

Proceeding on **Option A** unless told otherwise, because it is the only option
that keeps one history.

## The two branches

### `issue-fixes` — the 50 commits already made

This is the branch that solves GitHub issues. It is upstream-compatible: every
commit is a self-contained fix with a test, so it could be sent upstream as
individual pull requests.

    21 issue fixes + plans + worklog sessions

Publishable upstream as PRs. Worth saying plainly: the fixes are clean enough
that upstream PRs are realistic, which is presumably the point of keeping this
branch separate.

### `main` — issues PLUS spec features

Created by branching from `issue-fixes` and merging feature work into it. The
roadmap is `docs/plans/feature-00-roadmap.md` through
`feature-05-mobile-awards-recommendations.md`.

    main = issue-fixes + feature-01 ... feature-05

`main` is therefore always a superset of `issue-fixes`. Merge direction is
`issue-fixes` → `main`, never the reverse, and `main` is never rebased onto
`issue-fixes`.

## Procedure at the next clean checkpoint

"Clean checkpoint" means: `go build`, `go vet`, integration, unit and frontend
suites green, and `python3 docs/plans/generate_plans.py --check` clean.

```bash
cd ~/code-local/go/stash-box

# 0. Confirm clean. Nothing uncommitted, nothing half-done.
git status --porcelain            # must be empty
git log --oneline -1

# 1. Create the fork. Does NOT touch the local clone.
gh repo fork stashapp/stash-box --clone=false --remote=false

# 2. Add the fork as a second remote. Name it `fork`; `origin` stays upstream
#    so `git pull` keeps meaning "upstream changes".
git remote add fork https://github.com/8ullyMaguire/stash-box.git
git remote -v                     # confirm BOTH remotes, and which is which

# 3. Create issue-fixes at the current tip, then push it.
git branch issue-fixes master
git push -u fork issue-fixes

# 4. Create main from issue-fixes and push it. main starts identical; the
#    features get merged in as they are built.
git branch main issue-fixes
git push -u fork main

# 5. Optionally set the fork's default branch to main, since that is the
#    branch that will carry the product vision.
gh repo edit 8ullyMaguire/stash-box --default-branch main

# 6. Verify both branches exist and both are reachable.
git ls-remote --heads fork
git rev-list --left-right --count fork/issue-fixes...fork/main
```

Step 6's `rev-list` should print `0  0` immediately after setup, since the two
branches are identical at creation. It becomes non-zero as features land, and
that gap is exactly the feature work.

## Branching model once features start

    issue-fixes  ──────────────────────────────►  (frozen; PRs upstream)
         │
         └── main ──► feat/trust-levels ──► feat/elo ──► feat/federation
                       (merged one at a time, each gated on the full suite)

Rules:

- Feature work branches from `main`, never from `issue-fixes`.
- `issue-fixes` is **frozen** once published. New issue fixes go to `main` as
  well; the branch exists to give upstream a clean PR series, not to be a second
  line of development.
- Every merge to `main` re-runs the full gate from the "clean checkpoint" list.
- The worklog in `docs/track/WORKLOG.md` stays append-only across both branches.

## Why the fork matters, concretely

`origin` currently points at `stashapp/stash-box`. Pushing to it would either be
rejected (no write access) or, with a token that has `repo` scope on the wrong
account, land in the canonical repository. The fork plus a second remote removes
the ambiguity entirely: `origin` is always upstream, `fork` is always ours.

## Verification that this worked

```bash
git remote -v                                    # two remotes, origin=upstream
git ls-remote --heads fork                       # issue-fixes and main exist
gh repo view 8ullyMaguire/stash-box --json isFork,nameWithOwner
git log --oneline fork/issue-fixes -1            # the 50th fix commit
```

**No GitHub issues have been closed by any of this work.** The fixes are local
commits plus, later, PRs. Do not run `gh issue close` without being asked.
