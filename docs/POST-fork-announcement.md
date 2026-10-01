<!--
Post for the fork announcement. Title is the H1 below; body is everything after it.
Baseline: b4b8aef2 (clone of stashapp/stash-box, 2026-09-28).
Head: 1a7a7c38. Figures measured from the repo, not recalled.
-->

# Forking stash-box: 16 new features, 35 upstream PRs ported, 177 issues dispositioned

| | |
|---|---|
| commits | 268 |
| backend Go files | 289 (119 of them `_test.go`) |
| frontend files | 132 |
| migrations | 20 files (16 new fork-authored migrations) |
| docs | 70 |
| test files of any kind | 159 (`_test.go` + `__tests__` + `.spec.`) |
| schema | 94 migrations, one per number, no two sharing a statement |
| suite | 60 packages green (untagged), 33 green with `-tags=integration`, 142 Go test files |
| frontend tests | 54 files / 683 tests |

---

## New features (this fork's own spec)

Sixteen new migrations carry the fork's features, numbered 79–95 so they sit above
every upstream migration:

| # | Migration | Feature |
|---|---|---|
| 79 | `add_identification_board` | identification board: multi-performer scenes and their volunteers |
| 80 | `add_authored_quests` | authored quests, scored independently of votes |
| 81 | `add_bonus_points` | bonus points alongside the existing reputation |
| 82 | `add_reviews` | written reviews, separate from ratings |
| 83 | `add_site_directory` | directory of sites, their metadata and reachability |
| 84 | `add_webhooks` | outbound webhooks for federation and moderation events |
| 85 | `add_vote_weight` | Vanguard/trust-weighted Elo |
| 86 | `add_vanguard` | Vanguard roles and permissions |
| 87 | `content_denylist` | content denylist for whole sites or tags |
| 88 | `scene_title_text` | titles as `text`, not a bounded string |
| 89 | `identification_federation` | identification federated to peers |
| 90 | `changelog_indexes` | keyset-paginated changelog queries, four of them |
| 91 | `performer_genitals` | genital attributes on performers |
| 93 | `performer_weight` | physical weight on performers |
| 94 | `image_types` | curator-defined image type vocabulary with per-viewer ordering |
| 95 | `image_crops` | cropping on upload, pre-crop original retained, recrop from the original |

Two full plan documents are written, built and verified:

- `docs/plan/feature-03b-content-access-and-vanguard-weighting.md` — content access and Vanguard weighting (D1–D8; D5 deliberately split — region/device recorded, not enforced, by decision S2)
- `docs/plan/feature-04-identification-federation.md` — identification federation, with 93 lines of deviations recorded

## Upstream PRs: 35 of 45 ported

Twenty-six ported before this stretch; nine here, each needing structural
reconciliation rather than a patch application:

| PR | What it brought | What it cost |
|---|---|---|
| #1183 | four keyset-paginated changelog queries + upstream tests | migration 73 → 90; `schemaVersion` pin dropped |
| #1269 | performer genital attributes | migration 76 → 91; same refusal |
| #1225 | `imageList` returns nullable elements | generated code regenerated, not patched |
| #1086 | vote bar below comments | patch would have reverted `compact` support |
| #1123 | typed-URLs actually submitted | fixes silent data loss |
| #1215 | image type labels | migration 76 → 94; `imageList` signature kept; `storeFile` early-return declined |
| #1216 | image crops + recrop | migration 77 → 95; `storeFile` declined again |
| #1248 | dependency bump | pgx held at 5.9.2 (5.10.0 breaks six integration packages) |
| #1266 | performer physical weight | reconciled with `85_add_vote_weight` — same word, different unit |

Ten deliberately not ported, each with a recorded reason in
`docs/plan/upstream-pr-port.md`:

- **#928** destructive — downgrades `postgres:18` → `postgres:16` and drops the
  `pg-spgist_hamming` build that pHash fingerprint matching depends on
- **#591 #708 #736 #761 #871** declined on written policy
- **#1076 #1278** superseded — verified their content already present, not assumed
- **#1155 #878** upstream drafts, `mergeable=UNKNOWN`, conflicting against a tree
  this fork diverged from months ago

## Issues: 177 dispositioned, 23 solved

| Disposition | Count |
|---|---|
| fixed | 22 |
| partially-fixed | 1 |
| declined (with reasons) | 16 |
| feature-request (kept as backlog) | 138 |
| **outstanding** (`bug` / `suspect-bug` / `spec-collision`) | **0** |

The 138 feature-requests each cite a written policy in `docs/SPEC.md` section 0,
so the ledger is uniform application rather than bulk reclassification. The
partially-fixed row is #583: the 64-byte bcrypt cap is deliberate, and the
misleading error message was the actual defect.

## Verification

`docs/goal-check.py` — ten clauses, all PASS:

```
C1 PRs decided          all 45 open PRs decided
C2 reasons present      138 rows cite a written policy, 12 distinct reasons
C2 issues dispositioned 177 rows, zero outstanding
C3 spec/plan built      all 2 plan documents Built
C4 narrow merged        issue-fixes is an ancestor of master
C5 migrations coherent  one migration per number, none shared
C6 suite                60 packages accounted for (baseline 60)
C6b integration suite   33 packages green with -tags=integration
C7 build clean          build, vet and gofmt all clean
```

## Bugs found in upstream code while porting

Not padding — each was caught by running the suite, not by reading the diff, and
each would have shipped broken:

1. **`storeFile` ordering** (upstream #1215, #1216). Routing `Create` through it
   reinstates a write order that predates this fork's #738 and #948, orphaning files
   the reaper cannot find. Declined both times.
2. **A short `Read` yields a truncated image** (upstream #1216) whose dimensions and
   checksum describe bytes that were never stored. Upstream's `ReadFull` fix taken.
3. **Four upstream test bugs** (#1215): a `Count` vs page-length equality that only
   holds under 100 fixtures; an ASC page-scan for a performer that ASC sorts off page
   one; an unguarded `toTypedImages(performer.images)` that crashed the whole form on
   merge-source edits while line 221 of the same file already guarded it; and a bare
   `<img alt="Deleted">` replacing this fork's `<DeletedImage>`.
4. **pgx 5.10.0** (#1248) panics six integration packages at `initPostgres`. Upstream
   master is still on 5.9.2, so upstream CI green meant nothing about it.

The recurring lesson: **upstream's green checks describe upstream's fixture count,
not the code.** Every one of these passes in a smaller suite.

## Two verification bugs this fork had in its own tooling

Worth recording because both had the same shape — a number produced by one code path
and consumed by another:

1. **`goal-check.py`'s C6 floor was decorative.** The clause counted `ok` AND
   `?  [no test files]`; `--update-baseline` counted `ok` only, so the command meant to
   prevent drift wrote 31 against a measured 60. A suite losing 29 of 60 packages would
   still have reported PASS. Both now share one `count_packages()`, verified by
   control: floor 200 → FAIL, floor 60 → PASS. A check that cannot fail is not a check.
2. **`testTimeout: 15000` was too tight** for a suite grown to 54 files / 683 tests.
   `user.type` and `user.click` are real timers, so a test killed mid-interaction
   leaves the submit callback half-called and the next assertion sees it called twice —
   "expected 1, got 2" reads as a logic bug and is not one. Raised to 60000; the same
   tests pass 55/55 in isolation and 683/683 in the suite.
