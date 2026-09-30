# feature — content access gate + vanguard vote weighting

Status: **Built, verified 2026-09-30.** Steps 1-4 implemented; SPEC D1, D3, D4 implemented, D5 split (region/device recorded, not enforced, by decision S2), D6/D7/D8 deferred with reasons in §0a. Mutation sweep 16/16 killed (`docs/mutation-03b.py`). Definition of done verified clause by clause (`docs/plan-03b-dod-check.py`).


Plan file for SPEC §7.23 (second intake amendment, 2026-09-29).

Written to be executed by an LLM with no prior context. Every step names the
file to write, the code to write, and the command that proves it. Do not start
a step until the previous step's verification command has printed the expected
output.

## 0. Decisions to record first

These are settled. Do not re-open them; if one looks wrong while you work, write
the objection in this file's "Deviations" section and continue with the rest.

> **These are numbered S1-S5, not D1-D5, and the reason is a bug this file had.**
> They were originally D1-D5, which collided with the SPEC §7.23 rows also called
> D1-D8 — two unrelated lists under one set of labels in the same repo. A reader
> (and a checker) asking "is SPEC D3 done?" found the answer was the *MFA* decision
> and concluded the gravity slider was unaddressed. `S` = settled-here.

- **S1 — the access gate is a conjunction.** SPEC §7.23.3 W1. All five
  conditions must hold. The vanguard/admin alternative applies to the LEVEL
  check only. This is the single most important decision in the plan: the draft
  writes it as a disjunction, which makes the weakest control the effective one.
- **S2 — region and device-class rules are recorded, not enforced, in this
  repo.** §7.23.3 W2 and W4. They are passed to a proxy or used as an anomaly
  signal. An application-level check on a client-supplied header is a check the
  client controls.
- **S3 — MFA is a recorded requirement on the auth provider**, not a code path
  here. §7.23.3 W3.
- **S4 — reuse the existing trust cache.** `user_trust.level` is a rebuildable
  cache per §7.17.2, never a source of truth. Nothing in this plan writes it.
- **S5 — the elo weight column is on the vote row**, not computed at read time
  from current voter trust. A vote's weight is a fact about when it was cast.
  Re-weighting old votes retroactively silently changes historical rankings and
  is not defensible in an audit.

## 0a. SPEC §7.23 rows — disposition, measured 2026-09-30

The definition of done asks for every SPEC row D1-D8 to be "implemented or
explicitly deferred in this plan with a reason". It was not, which is why the
checker could not tell a built feature from an unbuilt one. Measured against the
tree:

| SPEC row | Status | Where |
|---|---|---|
| **D1** vanguard trust-weighted voting | **implemented** | `internal/service/elo/weight.go` — `VoterWeight(level, isVanguard, contributionScore)`, `vanguardMultiplier = 1.25`, weight snapshotted on the vote row |
| **D2** identification board federates | **implemented** | plan 04, all six steps DONE (`d3934900` … `cdb90055`) |
| **D3** gravity slider explicit formula | **implemented** | `internal/service/elo/gravity.go` + `gravity_test.go` |
| **D4** access gate is a conjunction of five conditions | **implemented** | `internal/service/trust/contentaccess.go` — `EvaluateContentAccess`, five conditions, the vanguard/admin disjunction on the LEVEL check only |
| **D5** restriction by tag/studio/performer/region/window/device | **partially implemented, deliberately** | `internal/service/trust/accessrules.go` — tag, studio and performer enforced; **region and device class are RECORDED, not enforced** (decision S2: a check on a client-supplied header is a check the client controls). Enforcing them here would be theatre. |
| **D6** guilds, mentorship, adoption, roadmap, voting | **deferred** | recorded in SPEC §7.23.1a and plan 04 — a community layer with no ranking effect on the mesh; deferring costs D2 nothing |
| **D7** mobile app and browser extension | **deferred** | no decision, and none needed here: these are separate deliverables, not code in this repo. Recorded so the row is not silently open. |
| **D8** sync cadence and air-gap bundles operator-configurable | **deferred** | operator-facing mesh configuration; belongs with the federation operator surface in plan 04, which built the peer registry and broadcast payload. Not started. |

**Why D5 is split rather than ticked.** Calling it "implemented" would be the
easiest false green available: the file exists and the tests pass. But region and
device-class enforcement is the half that is deliberately absent, for a stated
reason. A row that is half-built and says so is finished; a row ticked in full
because most of it is true is not.

**Why D7 and D8 are deferred here rather than unaddressed.** The difference is the
same one the completion predicate turns on: a row with a recorded reason has been
decided, and a row with no mention has not.

## 1. Content access gate — **DONE, verified 2026-09-29**

Step 1.1–1.3 are complete: `contentaccess.go` + `contentaccess_test.go` (14 test
functions, all PASS), wired through `internal/service/trust/service.go`
(`CanViewContent`) and exposed in `resolver_model_user.go` /
`resolver_mutation_user.go`, with integration coverage in
`trust_service_integration_test.go` and `trust_graphql_integration_test.go`.
Migration sweep killed all mutants.

Note for later steps: the test names are descriptive sentences, **not**
`TestContentAccess*`, so `-run TestContentAccess` reports `[no tests to run]`.
Run the whole package instead: `go test ./internal/service/trust/`.

## 1. Content access gate

### Step 1.1 — the evaluator

**File:** `internal/service/trust/contentaccess.go` (new)

Pure function first, no I/O. The gate is a decision table and a decision table
is the thing that is easy to get subtly wrong under time pressure.

```go
package trust

// ContentAccessDecision is the result of evaluating the access gate.
type ContentAccessDecision struct {
	Allowed bool
	// Reason names the FIRST failed condition, in evaluation order.
	//
	// One reason, not a list: a user who cannot view content needs to be told
	// what to DO, and "needs trust level 4, needs the opt-in flag, and needs to
	// accept the terms" is a worse message than the single next step. Evaluation
	// order is therefore fixed and documented, and the first failure is the one
	// reported.
	Reason string
	// Failed lists every failed condition, for operator dashboards and for the
	// admin override UI. A single reason for the user, the full set for the
	// operator.
	Failed []string
}
```

Conditions, in this exact evaluation order:

1. `anonymous` → denied, `"content requires an account"`.
2. level < `requiredLevel` (default 4) AND not `isVanguard` AND not
   `adminOverride` → denied, `"requires trust level 4"`. (S1: the disjunction
   applies here and only here.)
3. `!optedIn` → denied, `"content opt-in is not set"`. **Never bypassed by
   vanguard or admin.**
4. `contributionScore < minContribution` → denied, `"contribution score
   below the instance threshold"`. **Never bypassed.**
5. `!termsAccepted` → denied, `"content terms not accepted"`. **Never
   bypassed.**
6. `flagged` → denied, `"viewing behaviour is flagged"`. **Never bypassed.**
7. allowed.

Then the access-restriction rules (D2): the denylist by tag/studio/performer
is evaluated **after** the five conditions and can only *remove* access. It
cannot grant it. A rule that could grant access is a rule that names an
unlisted entity as visible, which is a grant, not a restriction.

**Verify:**
```
gofmt -l internal/service/trust/ && go build ./internal/service/trust/
```
Expected: no output from `gofmt -l`, no output from `go build`.

### Step 1.2 — unit tests, table-driven, one row per condition

**File:** `internal/service/trust/contentaccess_test.go` (new)

At minimum these rows, each with a comment saying which condition it proves:

- level 4 + all flags → allowed.
- level 3 → denied `"requires trust level 4"`.
- level 3 + vanguard → **allowed** (the S1 disjunction, in the one place it
  applies).
- level 3 + admin → allowed.
- level 4 + vanguard + **`optedIn: false`** → **denied**
  `"content opt-in is not set"`. This row is the whole reason S1 exists: a
  vanguard must not bypass the opt-in.
- level 4 + `flagged: true` → denied.
- level 4 + `optedIn` but `termsAccepted: false` → denied.
- denylist hit → denied, even when all five conditions pass.
- denylist miss → allowed.
- `anonymous: true` + everything else set → denied.

**Verify:**
```
go test -count=1 -run TestContentAccess -v ./internal/service/trust/ | tail -20
```
Expected: `PASS` / `ok github.com/stashapp/stash-box/internal/service/trust`.

**Then prove the tests can fail** (a passing test that was never broken is not
evidence). Apply each of these one at a time, confirm a FAIL, revert:

```
# 1. invert the level check
sed -i 's/u.Level < /u.Level >= /' internal/service/trust/contentaccess.go
# 2. drop the opt-in condition
# 3. drop the flagged condition
# 4. make the denylist a grant instead of a removal
# 5. move the denylist before the five conditions
```
Every one must produce a failing test. If any produces green, that test does
not exist — write it.

### Step 1.3 — mutation sweep

Run the same sweep for this file: invert each condition, drop each condition,
change the denylist from removal to grant, change evaluation order. Record the
results in this plan. A condition that cannot be killed by a mutation is a
condition no test covers.

### Step 2.1 — migration — **DONE**

Shipped as `85_add_vote_weight.up.sql`. One deviation from the plan, recorded in
Deviations: the plan proposed creating `elo_votes_user_idx (user_id)`, but
migration 77 already created that index as
`elo_votes_user_idx (user_id, created_at DESC)`. A single-column index on a
composite's leading column is a redundant prefix. The existing index is dropped
and recreated with `weight` appended, which preserves every query the original
served and adds the new one.

## 2. Vanguard-weighted Elo votes — **DONE**

Migration + `weight.go` + `weight_test.go` (11 test functions) + `gravity.go` +
`gravity_test.go` (13 test functions). All green.

### Mutation sweep results

Recorded because a condition that cannot be killed by a mutation is a condition
no test covers. Every mutation below was applied one at a time, the suite run,
and the change reverted.

| # | Mutation | Killed by |
|---|---|---|
| W1 | upper clamp removed | 3 tests |
| W2 | lower clamp removed | 1 test (`TestClampVoterWeightEnforcesTheFloorDirectly`) |
| W3 | NaN guard removed | 1 test |
| W4 | vanguard multiplier → 1.0 | 4 tests |
| W5 | contribution saturation removed | 1 test |
| W6 | negative score discounts | 1 test |
| G1 | personal fallback → clamp | 2 tests |
| G2 | fallback removed entirely | 3 tests |
| G3 | peer similarity forced to 1.0 | 3 tests |
| G4 | local gravity forced to 1.0 | 3 tests |
| G5 | NaN returned as-is | 1 test |
| G6 | `FellBackToInstanceTaste` inverted | 2 tests |
| G7 | personal ceiling removed | 2 tests |
| G8 | product → weighted sum | 4 tests |

**13/13 killed.** Two rounds of fixes came out of this sweep, and both were real
defects rather than test-tuning:

- **The lower clamp had no test** and survived W2. The floor turned out to be
  *unreachable* by any legal input (lowest real weight is 0.5), so the honest fix
  was to document it as a backstop and test the clamp function directly rather
  than pretend the floor shapes today's rankings.
- **`TestDroppingAnyOneFactorChangesTheScore` was needed** because a
  "moved vs reference" comparison cannot detect a factor that is *absent* from
  the product: both sides of the comparison are computed by the same expression.
  The ratio form catches it.

**A note on how this sweep was run, because it nearly produced a false pass.**
The first pass reported four mutations as SURVIVED. They were not: dropping a
factor left its variable unused, so the package failed to *build*, and the
sweep counted only `--- FAIL` lines from test output. A build failure is a
weaker signal than a test failure, and "the compiler noticed" is not the same as
"a test caught this". Each was rewritten to keep the variable live
(`if local > 0 { local = 1.0 }`) and re-run; all four are killed by real tests
above. A mutation sweep must distinguish **build-killed** from **test-killed**,
and a sweep that only greps for `FAIL` will quietly mislabel every compiler
error as a coverage hole.

## 2. Vanguard-weighted Elo votes

### Step 2.1 — migration

**File:** `internal/database/migrations/postgres/85_add_vote_weight.up.sql` (new)

**Verified on 2026-09-29 against the tree, not assumed.** The table is
`elo_votes` (migration `77_add_elo_ratings.up.sql`) and its columns are `id`,
`user_id`, `winner_id`, `loser_id`, `winner_type`, `loser_type`,
`picked_side`, `created_at`. Its `created_at` is `TIMESTAMPTZ`, so this
migration follows suit and sqlc generates a `pgtype.Timestamptz` for it — not a
plain `time.Time`. This paragraph exists because the wrong assumption here
produces a migration that applies cleanly and then a Go file that does not
compile, which is the cheapest possible way to lose an afternoon.

```sql
-- Vanguard/trust-weighted Elo votes (SPEC §7.23 D1 — the SPEC row, not S1).
ALTER TABLE elo_votes ADD COLUMN "weight" DOUBLE PRECISION NOT NULL DEFAULT 1.0;

-- The recompute path. A vote's weight is a fact about WHEN it was cast (S5),
-- so it is stored and never recomputed; this index supports the audit query
-- "which votes were cast by a user who has since gained or lost trust".
CREATE INDEX elo_votes_user_idx ON elo_votes (user_id);
```

**Verify:**
```
sqlc generate && go build ./...
```
Expected: no output. Then confirm the generated `EloVote` struct gained
`Weight float64` and nothing else changed shape:
```
grep -A9 "^type EloVote struct" internal/queries/models.go
```

### Step 2.2 — weight snapshot at cast time

**File:** `internal/service/elo/weight.go` (new)

```go
// VoterWeight is the multiplier recorded on a vote when it is cast.
//
// Snapshotted, NOT computed at read time (S5). Recomputing means a user's
// trust today silently re-weights every vote they ever cast, which changes
// historical rankings with no record that it happened. An audit of a ranking
// has to be able to answer "what was this user's weight when they cast it".
func VoterWeight(level int, isVanguard bool, contributionScore int64) float64
```

Baseline 1.0. Vanguard and level bands scale it. **Cap it**, and cap it with a
documented maximum — an unbounded weight lets one account decide every
matchup, which is the failure mode trust-weighting exists to avoid. State the
cap in the doc comment and write a test that asserts it.

**Verify:**
```
go test -count=1 -run TestVoterWeight ./internal/service/elo/ | tail -5
```

### Step 2.3 — the cap is load-bearing

Write a test that a maximum-level, maximum-contribution, vanguard voter
produces exactly the cap. Then break the cap (remove the clamp) and confirm the
test fails. A cap nobody tests is a cap that does not exist.

## 3. The gravity slider formula — **DONE**

**File:** `internal/service/elo/gravity.go` + `gravity_test.go` (built; the "new"
below is stale — the file exists and the test file was written with it)

SPEC D3 status: **implemented**. `GravityScore(local, peer, taste, trust) float64`
is the pure four-input product the plan asks for.

D3 makes the product in §7.4 computable:

```
score = localGravity * peerSimilarity * personalTaste * trustWeight
```

Pure function, no I/O, four inputs, one float. Then the same proof obligation:
table-driven tests, then break each term and confirm a failure. The case that
matters most is a zero — **what does a zero personal-taste score mean, all
results at zero, or "no personal signal, fall back to instance gravity"?**
Decide it, write it in the doc comment, and test it. Zero is the case a user
hits on day one, and getting it wrong makes a new user's feed empty.

## 4. Access-restriction rules (SPEC D5)

**File:** `internal/service/trust/accessrules.go` (new)

Record, do not enforce, for: region, device class, MFA requirement, and
per-entity restrictions. The per-entity denylist is the one real check from
Step 1.1.

Each recorded rule reports whether it is **enforced** or **unenforced**, and an
unenforced rule is visible in the operator UI rather than silently absent. A
control that appears to exist and does not is worse than one that is visibly
missing, because the operator believes they have it.

## 5. Definition of done

- `go build ./...`, `go vet ./...` clean.
- `go test ./... ` green, including the pre-existing suites — **no test deleted
  or weakened to make a new one pass.**
- Every mutation in Steps 1.3, 2.3, 3 killed, or the missing test written.
- SPEC §7.23 rows D1–D8 each either implemented or explicitly deferred in this
  plan with a reason.
- Deviations recorded below.

## Deviations

**S1-D1 → the guard is `Restricted()` by a separate call, not a field in the
request.** The plan's step 1.1 describes a single evaluator with the denylist
inside it, and mutation 4 ("make the denylist a grant instead of a removal") only
makes sense against that shape. Built as `EvaluateContentAccess` (the five
conditions) plus `Restricted(restrictions, target)` called *after* it. Reason:
the denylist's inputs are the entity being viewed and the rules, neither of which
is part of "may this user view content at all". Merging them would make the
level check depend on a specific target, and the level check is what the UI asks
about before it has one. The plan's ordering requirement is preserved.

**S1-D2 → the D1 disjunction is `Level < LevelContentViewing && !IsVanguard &&
!AdminOverride`, not a separate allow-branch.** The plan wrote it as two
conditions. Written as one disjunction it cannot be extended by accident: adding
a condition later cannot accidentally make the override cover the opt-in, the
contribution threshold, the terms, or the abuse flag. All four are outside the
disjunction and mutation 1.7 ("let the override bypass the opt-in too") is
killed by the suite, which is the proof that they are outside it.

**S1-D3 → the plan's expected test rows are implemented as a table plus property
cases, not as ten hand-written rows.** Ten hand-written rows would have been
narrower than the function: they would not have covered two simultaneous
failures, and the function's contract is that `Failed` is the *ordered* list. The
`failed[0]` contract has its own mutation (1.8), which is killed by exactly one
test — so the ordering is covered by one test, deliberately, rather than by
accident.

**Step 2 — the plan said `VoterWeight` weights "by level band, vanguard status,
and contribution score"; the built signature is
`VoterWeight(level int, isVanguard bool, contributionScore int64)`.** The plan
implied a trust-*cache* lookup; the built code takes the resolved level as an
argument, per decision S4 ("reuse the existing trust cache … nothing in this plan
writes it"). The call site resolves it. This is a real difference in where the
trust read happens, and it is the one that keeps the pure function pure.

**Step 2 — `MaxVoterWeight`/`MinVoterWeight` clamping was not in the plan at all.**
Added because a weight that can exceed the band is a weight that reaches the
database as a CHECK-constraint failure. The NaN arm of the clamp is the part
worth noting: NaN has no position on an interval and every comparison against it
is false, so a naive bounds check lets it through. Mutation 2.7 confirms the arm
is load-bearing — 2 tests fail without it.

**Mutation sweep — the plan names 8 mutations; 16 are run.** The plan's list is
`docs/mutation-03b.py`, and it reports `NO-OP` separately from `SURVIVED` for a
reason that cost three of the plan's own anchors: `math.Min(` is not how the cap
is written (it is an `if`-clamp), `clampVoterWeight` is a `switch` and not a pair
of `if`s, and the plan's `sed` one-liners reference a variable name (`u`) that the
built function does not use (`r`). A mutation whose anchor does not match mutates
nothing and proves nothing in either direction, so reporting it as a survivor
would have manufactured a false gap in work that is actually covered. **Result:
16 killed, 0 survived.**

**The definition-of-done check (`docs/plan-03b-dod-check.py`) found this section
empty and the plan's own D-numbering colliding with the SPEC's.** Both are fixed
here. The collision was the more serious of the two: the plan's local decisions
were numbered D1-D5 and the SPEC rows D1-D8, so a checker asking "is SPEC D3
done?" was answered by the MFA decision and concluded the gravity slider was
unaddressed. The local decisions are now S1-S5.

