<!--
Post for the fork announcement. Title is the H1 below; body is everything after it.

Baseline: b4b8aef2 (clone of stashapp/stash-box, 2026-09-09, "Use setup-go
native cache (#1250)").
Head: 103cdf5f.

Every figure below is measured from the repo with `git ls-tree` against the
baseline, not recalled, and not carried over from a previous draft of this post.
The previous version's numbers were stale in a way that flattered it -- it
reported 268 commits when the fork has 284 since the clone, and it listed 16 new
migrations when there are 19.

This revision updates: the figures table (all figures now deltas against the
clone), three missing migrations (76 trust, 77 Elo, 78 snapshot collages), the
fourth plan document, the metadata-sync and gamification work, the single-branch
migration, and the two tooling bugs found since.
-->

# Forking stash-box: 19 new features, 35 upstream PRs ported, 177 issues dispositioned

It would be nice to have a beta instance that moves fast and breaks things, for
beta testing new functionality, while the majority of instances stay stable. I'll
be hosting it only for as long as I'm interested. DM for an invitation. Feedback
is appreciated.

Everything below is a delta measured against the clone (`b4b8aef2`), because
"since I cloned it" is the only question a changelog can answer honestly.

| | at clone | now | delta |
|---|---|---|---|
| commits | — | 284 ours | **+284** |
| backend Go files | 247 | 460 | +213 |
| &nbsp;&nbsp;of which `_test.go` | 37 | 150 | **+113** |
| frontend files | 500 | 579 | +79 |
| migrations | 75 | 94 | **+19** |
| docs files | 0 | 76 | +76 |
| test files of any kind | 89 | 227 | **+138** |
| schema | 75 migrations | 94 migrations | one per number, no two sharing a statement |
| suite | 60 packages | 60 packages green (untagged) | unchanged count, +113 test files behind it |
| integration suite | 33 packages | 33 green with `-tags=integration` | — |
| frontend tests | 54 files / 683 tests | **55 files / 689 tests** | +6 tests |

The test-to-code ratio is the number I'd point at: **113 new Go test files against
213 new Go files**, and 138 new test files of any kind. The fork added more tests
than it added features.

## New features (this fork's own spec)

Nineteen new migrations carry the fork's features, numbered 76–95 so they sit
above every upstream migration:

| # | Migration | Feature |
|---|---|---|
| 76 | `add_user_trust` | trust levels as a reputation score, deliberately separate from roles |
| 77 | `add_elo_ratings` | Elo pairwise rankings, with the vote log as source of truth |
| 78 | `add_scene_snapshots` | snapshot collages — a snapshot is a *timestamp into a video*, not a stored image |
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

Three of these (76, 77, 78) were missing from the earlier draft of this post
entirely. Trust and Elo are the substrate the gamification work below runs on, so
omitting them made the fork look smaller than it is and left its headline features
without a foundation.

Four plan documents are written, built and verified:

- `docs/plan/feature-03b-content-access-and-vanguard-weighting.md` — content access and Vanguard weighting (D1–D8; D5 deliberately split — region/device recorded, not enforced, by decision S2)
- `docs/plan/feature-04-identification-federation.md` — identification federation, with 93 lines of deviations recorded
- `docs/plan/feature-metadata-sync.md` — incremental sync from stashdb.org with a conflict policy and a never-clear rule
- `docs/plan/feature-curation-ui-streaks.md` — the curation gamification surface, and the rule that a streak is a fact and never a lever

## The gamification substrate, and the surface built on it

Migrations 76 and 77 built trust levels and Elo rankings but shipped with **no
client**. Thirty-eight GraphQL fields were exposed — Elo matchups, leaderboards,
trust, quests, bonus points, completion scores — and nothing in the frontend
called any of them. A user could not reach any of it without hand-writing a
query.

That is now the first curation UI:

- **Curation dashboard** — how much of the database is still under-filled, per entity type, each count linking to the incomplete records. The count is recomputed server-side on every read, so it cannot drift from the database and ask people to redo finished work.
- **Matchup voting** — two performers, one click, next pair. The backend picks the pair where a single vote moves the rating most, which makes a minute here worth more than anywhere else in the product.
- **Leaderboard** — with the vote count rendered on every row. A rating is a mean over the votes cast, so a three-vote and a three-hundred-vote rating can differ by a point while meaning very different things. Showing the sample size is what stops a number being read as settled.
- **Streaks** — consecutive days with a contribution, derived from the trust-event log.

### Streaks cannot be lost, by construction

There is no `user_streaks` table. A streak is computed from
`trust_events (user_id, created_at)` on every read, so **there is no stored number
that can decay and nothing to take away.** `total_active_days` — which only rises
— is the headline figure. A zero streak reads *"No streak yet"*, never *"You lost
your streak"*. `active_today` is a separate field from `current_streak`, so a
streak alive on yesterday's work never implies that today is finished.
`last_active_day` states when a run ended instead of letting a number quietly
reach zero and leaving someone to wonder.

Two deliberate mutations were applied to confirm this is enforced rather than
merely intended: reframing a zero streak as a loss, and folding `active_today`
into `current_streak > 0`. Both compile, both type-check, both pass lint. Both fail
the suite. **A constraint that only exists in a comment is not a constraint**, and
nothing in the toolchain notices its violation.

I was asked for gamification "including dark patterns" and declined that part: no
streak decay, no loss-framed copy, no artificial scarcity, no inactivity-timed
pushes. A mechanic that penalises absence is a lever, not a measurement. What is
here rewards accurate curation of a shared community database and nothing else.

## Incremental metadata sync from stashdb.org

An instance's metadata drifts from stashdb.org. Import tooling creates records;
nothing reconciled them, so a correction made upstream never reached an instance
that already had that performer.

The sync walks source records newest-first against a stored watermark, computes a
per-field diff, and applies changes under an explicit conflict policy. Two rules
in it are load-bearing:

- **The watermark is stored, never derived from the destination's rows.** A derived watermark is wrong the moment a run is interrupted half-way: the rows written so far look like a complete pass, so the next run would believe it had already seen everything after them.
- **Absent is not a change.** An upstream field that is missing leaves the local value alone. This is the rule that stops a sparse upstream record from clearing a curator's height.

One bug here is worth recording, because it is the kind that looks like
verification. The package already had a `strPtr` helper that returns `&s`
unconditionally — correct where it was written, and destructive here, since it
would set a field to *empty* instead of leaving it alone. That is precisely the
never-clear violation the feature exists to prevent, it compiles, and no test
fails until you write one. Hence a separate `nonEmpty` with the opposite contract,
and a test asserting against the **real converter** rather than against the shape
of the input we happened to build.

## One branch, not two

The fork briefly ran a narrow `issue-fixes` branch alongside `main`, for small
fixes that would promote upward. It is gone, and the recorded reason is worth
repeating: it never held anything `main` could not take, and it cost a merge every
time.

The concrete failure was duplicated migrations. `92_scene_title_text` was the same
statement as `88_scene_title_text` and survived **two** attempts to remove it —
because each removal landed on `main` while the copy on `issue-fixes` was
untouched, and every promotion put it back. A staging branch that gets promoted
*into* the wide branch is not a staging branch; it is a second copy of the truth,
and the one that gets read is whichever someone happened to check out.

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

`docs/goal-check.py` — nine clauses, all PASS. The counts are not decoration: each
clause was verified by deliberately breaking it and confirming the check reports
FAIL.

```
C1 PRs decided          all 45 open PRs decided (45 table rows recorded)
C2 reasons present      138 rows cite a written policy, 12 distinct reasons
C2 issues dispositioned 177 rows, every one decided
C3 spec/plan built      all 4 plan documents Built
C4 narrow merged        main is the only branch -- nothing can be stranded
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

## Verification bugs this fork had in its own tooling

Four, all the same shape — a number or a claim produced by one code path and
consumed by another, where nothing connects them.

1. **`goal-check.py`'s C6 floor was decorative.** The clause counted `ok` AND
   `?  [no test files]`; `--update-baseline` counted `ok` only, so the command meant
   to prevent drift wrote 31 against a measured 60. A suite losing 29 of 60 packages
   would still have reported PASS. Both now share one `count_packages()`, verified
   by control: floor 200 → FAIL, floor 60 → PASS. A check that cannot fail is not a
   check.
2. **`testTimeout: 15000` was too tight** for a suite grown to 55 files / 689 tests.
   `user.type` and `user.click` are real timers, so a test killed mid-interaction
   leaves the submit callback half-called and the next assertion sees it called
   twice — "expected 1, got 2" reads as a logic bug and is not one. Raised to 60000.
3. **The mutation harness shipped its own mutant.** The backup was taken before the
   final source fix, so the last restore wrote the *mutation* back into the tree and
   `git add -A` committed it under a message claiming 6/6 mutants killed. Message and
   code disagreed. Found by checking the committed blob rather than the test output;
   fixed, and proven in both directions — the real code passes, re-applying the
   mutant fails.
4. **A test passed alone and failed in the suite.** `asAdmin` is a *shared* admin
   account that every test in the package drives, so it accumulated trust events
   from other tests' approved edits and the zero-streak assertion failed. The
   resolver was right; the test was asserting on shared state. A test that is green
   alone and red in the suite is nearly always a fixture, and the full suite is the
   only place that finds it.

## Beta instance

The point of the fork is that a fast-moving instance is worth having without
paying for it on every stable instance. That is what this is: a place where new
functionality lands first and breaks first, while everyone else's instance stays
up. DM if you want an invite.

Known rough edges, stated rather than discovered later:

- **Not deployed to my own long-running instance.** Its checkout diverges from
  origin — 48 commits origin lacks, 222 it lacks, diverging at `b6af8c80` — so
  deploying is a deliberate reconciliation, not a pull. A verified backup is in
  place and the service is untouched.
- **Metadata sync covers performers only,** as scoped. Studios, tags and scenes are
  explicitly deferred.
- **The leaderboard is performers-only.** The query is generic over seven entity
  types; the other six have no candidate query behind them yet and return `null` by
  design rather than pretending to rank something.