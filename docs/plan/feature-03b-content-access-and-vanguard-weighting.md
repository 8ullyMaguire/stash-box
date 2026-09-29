# feature — content access gate + vanguard vote weighting

Plan file for SPEC §7.23 (second intake amendment, 2026-09-29).

Written to be executed by an LLM with no prior context. Every step names the
file to write, the code to write, and the command that proves it. Do not start
a step until the previous step's verification command has printed the expected
output.

## 0. Decisions to record first

These are settled. Do not re-open them; if one looks wrong while you work, write
the objection in this file's "Deviations" section and continue with the rest.

- **D1 — the access gate is a conjunction.** SPEC §7.23.3 W1. All five
  conditions must hold. The vanguard/admin alternative applies to the LEVEL
  check only. This is the single most important decision in the plan: the draft
  writes it as a disjunction, which makes the weakest control the effective one.
- **D2 — region and device-class rules are recorded, not enforced, in this
  repo.** §7.23.3 W2 and W4. They are passed to a proxy or used as an anomaly
  signal. An application-level check on a client-supplied header is a check the
  client controls.
- **D3 — MFA is a recorded requirement on the auth provider**, not a code path
  here. §7.23.3 W3.
- **D4 — reuse the existing trust cache.** `user_trust.level` is a rebuildable
  cache per §7.17.2, never a source of truth. Nothing in this plan writes it.
- **D5 — the elo weight column is on the vote row**, not computed at read time
  from current voter trust. A vote's weight is a fact about when it was cast.
  Re-weighting old votes retroactively silently changes historical rankings and
  is not defensible in an audit.

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
   `adminOverride` → denied, `"requires trust level 4"`. (D1: the disjunction
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
- level 3 + vanguard → **allowed** (the D1 disjunction, in the one place it
  applies).
- level 3 + admin → allowed.
- level 4 + vanguard + **`optedIn: false`** → **denied**
  `"content opt-in is not set"`. This row is the whole reason D1 exists: a
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
-- Vanguard/trust-weighted Elo votes (SPEC §7.23 D1).
ALTER TABLE elo_votes ADD COLUMN "weight" DOUBLE PRECISION NOT NULL DEFAULT 1.0;

-- The recompute path. A vote's weight is a fact about WHEN it was cast (D5),
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
// Snapshotted, NOT computed at read time (D5). Recomputing means a user's
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

## 3. The gravity slider formula

**File:** `internal/service/elo/gravity.go` (new)

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

## 4. Access-restriction rules (D2)

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

_(none yet)_
