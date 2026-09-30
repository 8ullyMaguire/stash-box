# Implementation plans

One plan per issue solved, and one per feature specified but not yet built. Each
is written to be executed by an LLM **with no prior context on this codebase**:
the defect stated, the root cause with `file:line` anchors, the change, the
tests, and the exact command that verifies each step.

**26 issue plans, 8 feature plans, 1 generator** — in `docs/plans/`.

There is a **second, hand-written** plan directory, `docs/plan/` (singular), and
the two are disjoint. Confusing them is easy and it has already cost a session:

| Directory | What it holds | Maintained by |
|---|---|---|
| `docs/plans/` | one generated plan per issue + the feature roadmap | `generate_plans.py` |
| `docs/plan/` | hand-written feature plans and the upstream PR port plan | by hand |

`docs/plan/feature-04-identification-federation.md` is **D2, and it is complete** —
six steps, all mutation-tested. The roadmap table below does not list it, and the
"none of these is implemented" line below does not describe it, because the two
directories are separate lineages of planning and the README had drifted to
describe only the first.

| | |
|---|---|
| `NNNN-<slug>.md` | one GitHub issue, numbered, named after the real issue title |
| `feature-0N-<slug>.md` | one phase of the federated mesh roadmap (`docs/SPEC.md` §7.15) |
| `generate_plans.py` | regenerates the issue plans from the fixing commits |
| [`publishing-plan.md`](publishing-plan.md) | how the two branches get published: `issue-fixes` and `main` |

## Roadmap phases

Start at `feature-00-roadmap.md` for the dependency order and sizing.

| Plan | Phase | Depends on |
|---|---|---|
| [`feature-01-portal-elo-identification.md`](feature-01-portal-elo-identification.md) | trust levels, public portal, snapshot collages, Elo, identification board, XP | — |
| [`feature-02-trust-quests-completion.md`](feature-02-trust-quests-completion.md) | content opt-in, curation quests and bounties, completion scores, gamification | 01 |
| [`feature-03-directory-ecosystem.md`](feature-03-directory-ecosystem.md) | reviews, site directory, public API and webhooks, Stash integration, browser extension | 01, 02 |
| [`feature-04-federation-preservation.md`](feature-04-federation-preservation.md) | federation protocol, taste-based peering, preservation replication, cross-instance discovery | 01–03 |
| [`feature-05-mobile-awards-recommendations.md`](feature-05-mobile-awards-recommendations.md) | recommendation engine, annual awards, curation campaigns, mobile app | 01–04 |
| [`feature-phase-2-curation-engine.md`](feature-phase-2-curation-engine.md) | curation engine, phase 2 | 01 |
| [`feature-phase-4-federation-mesh.md`](feature-phase-4-federation-mesh.md) | federation mesh, phase 4 | 01–03 |

**None of the seven roadmap phases above is implemented.** All of that lands on
the `main` branch; the issue fixes live on `issue-fixes`. The hand-written plans in
`docs/plan/` are the exception and carry their own status — read the plan, not
this file. See
[`publishing-plan.md`](publishing-plan.md). Phases 1 and 2 are each larger than all 26
issues combined, and Phase 4 is a protocol design that cannot be correct before
Elo produces stable taste vectors — taste is a peering input.

## Read the generator before trusting a plan

The issue plans are **derived from the fixing commit's own diff**, not written by
hand. They describe shipped work, so a plan goes stale the moment the commit it
was generated from changes. Verify:

```bash
python3 docs/plans/generate_plans.py --check    # exits non-zero if any plan is stale
```

This is the check CI should run: a fix committed without a matching plan should
fail the build.

**It is green, and getting it there found a real defect in the generator itself.**
`--check` compares each plan byte-for-byte against its regeneration, and
`diff_excerpt` embedded git's `index ` line verbatim. Git chooses the abbreviation
length from the repository's object count, so it grew from 7 to 8 characters as
this fork added commits — and **18 of 24 plans went red on that alone**, their
content perfectly correct. `--abbrev=7` on the `git show` makes the output
independent of repo size. A staleness check that depends on how big the repository
is not measuring staleness.

```bash
python3 docs/plans/generate_plans.py --check   # rc=0: all 24 up to date
```

This is the shape of trap the rest of these docs keep hitting: the check was
red, and the *plans* were the innocent party. Had someone "fixed" it by
regenerating, they would have committed 18 unrelated diff lines and learned
nothing.

```bash
python3 docs/plans/generate_plans.py             # rewrite every issue plan
```

Issue titles come from `gh issue list`. If `gh` is missing or unauthenticated the
generator falls back to the commit subject, so a network problem never aborts it.

## Status of each issue plan

Every plan states its own status in its first three lines:

- **`SOLVED in <commit>`** — code changed, tests mutation-checked.
- **`INVESTIGATED, DELIBERATELY NOT CHANGED`** — the reasoning is the deliverable.
  There are seven (#525, #583, #727, #778, #809, #879, #1177), and they exist so a
  closed question is not re-investigated from scratch.

**One is a decision rather than a record:**
`0583-password-length-limit-rejects-valid-long-passwords.md` records that the
64-**byte** limit is correct (bcrypt hashes a byte array) while the *message*
misleads. The message fix is specified there and is the one piece of outstanding
work from the issue pool.

## The rule the whole set encodes

Five issues in this set turned on the same lesson, and it is why a green run is
not evidence:

> A test that passes on unfixed code is worse than no test, because it reads as
> regression cover in review.

Every fix here was verified by reverting it, running the test, and requiring a
failure. Four shapes of the same mistake appeared:

| Issue | The trap |
|---|---|
| #525, #1177 | a test that passed on the *unfixed* code, so the "fix" was for a bug that does not exist |
| #605 | four tests covering a *detector*, all green when the line that *called* it was deleted |
| #1277 | an e2e regex matching `/cooldown\|wait/i`, which passed through the entire life of the bug it should have caught |
| #1007 | a destroy *edit* soft-deletes while a destroy *mutation* hard-deletes, so four tests asserted on rows that no longer existed |

## Verification commands used throughout

```bash
go build ./... && go vet ./...
export POSTGRES_DB="$STASHBOX_TEST_DSN"        # never commit a DSN with a password
go test -tags=integration -count=1 ./internal/api/
go test $(go list ./... | grep -vE 'internal/api$') -count=1
cd frontend && pnpm run test:run && cd ..
```

The frontend rebuild is `node node_modules/vite/bin/vite.js build`, **not**
`pnpm build` — the repo uses pnpm with the hoisted linker. A stale
`frontend/build` renders every route blank with HTTP 200, so rebuild before
browser-verifying anything.

Generated code (`internal/queries/*.sql.go`, `internal/models/generated_*.go`,
`graphql/generated.go`) is never hand-edited. Change the `.sql` / `.graphql`,
regenerate, then confirm idempotence by running the generator twice with no diff.

The test database is shared and not isolated, so any assertion about membership
in an unfiltered collection must pin `PerPage`; the default is 25 and other
tests' fixtures push yours off the end. This bit in both directions (#829,
#1007).

## Related documents

- `docs/track/WORKLOG.md` — the running session log, append-only
- `docs/SPEC.md` — product spec and fork direction
