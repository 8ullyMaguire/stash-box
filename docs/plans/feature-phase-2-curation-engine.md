# Phase 2 — Curation Engine, Completion Scores, Quests & Gamification

**Status:** planned 2026-09-29. Phase 1 complete (`99e5cb0` collages, `860c6c1`
identification board). Nothing in this document is implemented yet.

Covers SPEC §15 Phase 2: *trust levels + opt-in content viewing + gamification +
curation quests + completion scores.*

---

## 0. What is already built, and what this phase actually adds

Phase 2 is named for five things. Two already exist from Phase 1, and the plan
below says so rather than rebuilding them:

| Phase 2 item | Status | Where |
|---|---|---|
| trust levels | **done** | migration 76, `internal/service/trust` |
| opt-in content viewing | **done** | `user_trust.content_viewing_opt_in`, `Trust.CanViewContent` |
| gamification | **partial** — XP/levels/badges columns exist, nothing awards them except `trust.RecordEvent` | migration 76 |
| curation quests | **not started** | — |
| completion scores | **not started** | — |

So this phase is **three** pieces, and the first is the one everything else reads.

---

## 1. The ordering, and why it is this ordering

**Completion scores first.** Quests are *generated* from completion scores and
*verified* against them. Building quests first means writing a generator whose
input does not exist yet, and the temptation is to store the missing-field
information on the quest — which then goes stale the moment a real edit lands.

A completion score is a **DERIVED** value. It is not stored, and that is not a
performance decision, it is the correctness decision:

- The repo already has the rule. StashForge's `proposal_scores` is a VIEW
  precisely so a decision is reproducible from `(Policy, ballots)` alone. The same
  argument applies exactly here: a stored completion score is a second source of
  truth next to the columns it summarises, and the first time someone fixes a
  birthdate without a code change, every score in the database is wrong and
  nothing says so.
- Nothing in this codebase has a trigger that would keep a stored score current.
  Adding one is a much larger change than computing the number on read, and it
  introduces a write path on every edit that can fail independently of the edit.

So: **completion is a query.** §7.7's inputs are all columns and joins that
already exist, and the score is computed in SQL on read.

## 2. Completion scores (SPEC §7.7)

### The score

Seven inputs, each a boolean "is this present and trustworthy":

| Input | Performer | Scene | Studio | Site | Tag |
|---|---|---|---|---|---|
| name | always | always | always | always | always |
| disambiguation / alias | aliases | — | — | — | — |
| gender | yes | — | — | — | — |
| birthdate | yes | — | — | — | — |
| country | yes | — | — | — | — |
| height / measurements | yes | — | — | — | — |
| career years | yes | — | — | — | — |
| urls / links | yes | yes | yes | yes | — |
| image | yes | yes | yes | yes | — |
| **performer links** | — | yes | — | — | — |
| **studio link** | — | yes | — | — | — |
| **site link** | — | yes | — | — | — |
| **tag coverage** | — | yes | — | — | — |
| **snapshot coverage** | — | yes | — | — | — |
| **duration** | — | yes | — | — | — |
| **details** | yes | yes | yes | yes | yes |

**`birthdate_accuracy` participates, and this is the subtle part.** A performer
with `birthdate = 1990-01-01` and `birthdate_accuracy = 'unknown'` has an
inaccurate birthdate, which is worth *less* than no birthdate at all for
identification — it is a specific false claim, and a specific false claim is worse
than an absence because a curator trusting it will not fix it. So an uncertain
value scores as **absent**, not as present.

### Why not a single number

A single 0–100 score is what §7.7 asks for and it is what a progress bar wants,
but it is **not sufficient and on its own it is harmful**: it tells a curator
*that* something is missing and not *what*, and a quest phrased "improve this
performer" is not actionable. So the score is returned as:

- `score` — the 0–100 number, for the progress bar.
- `missing` — the list of named fields, for the quest and the tooltip.

The two are computed from the same pass, so they can never disagree. A stored
`score` with a computed `missing` is exactly the two-sources-of-truth bug this
decision exists to prevent.

**Weighting.** Fields are not equally valuable. A scene with no duration cannot
be identified; a scene with no tag can. So the score is a weighted fraction, and
the weights are **named constants in one place** rather than scattered through the
SQL, because a weight nobody can find is a weight nobody can argue with.

## 3. Curation quests (SPEC §7.7)

A quest is **generated, not authored**, for the bulk of them: "5 performers with no
birthdate" is a query over the completion scores, not a row someone wrote. Two
kinds exist:

- **Generated** — derived from missing-field counts. Bounded, regenerable, and
  **never stored**: a quest that is a pure function of the archive has no state
  to go stale.
- **Authored** — an operator's "rare performers need birthdates, this month",
  with a bounty. Stored, because it is a decision with a lifetime.

That distinction is the whole design. A stored generated quest accumulates claims
about the archive that were true when it was written, and a curator who claims it
is working from a list that may name performers who were already fixed an hour
ago.

### Claiming

Claiming is the anti-duplicate-work mechanism, and it is where the concurrency
lives: two curators claiming the last performer in a quest must not both get it.
Handled by a `claimed_by` column and a guarded UPDATE, **not** by a check-then-write.

**The quest is not consumed by claiming it.** Claiming marks work as *in progress*
so nobody duplicates it; the item is only removed when the underlying field is
actually filled. A quest that vanishes on claim loses work to a curator who is
still mid-edit when the claim expires.

### Bounties

`KindQuestCompleted` already exists in the trust enum at a value, so a completed
quest already has a trust meaning. Bounties multiply it. A bounty is an operator
decision, so it is **stored on the authored quest** and never on a generated one —
a bounty is a promise made by a person, and a generated quest must not be able to
manufacture one.

## 4. Gamification (SPEC §12)

XP already exists as `user_trust` rollup columns. What does not exist is the
**award path**: the mapping from an event to XP, and the badges.

- **XP awards go through `trust.RecordEvent`.** Not a parallel XP counter. Two
  counters for one contribution is the same two-sources-of-truth bug, and
  `RecordEvent`'s dedup key already prevents double-counting a replayed event.
- **Badges are DERIVED, not awarded.** A badge is a predicate over a user's
  counters: "solved 25 identifications", "100 approved edits", "a 30-day voting
  streak". Storing a `user_badges` table means a badge that disagrees with the
  counters that justify it, and the disagreement is invisible. Deriving them means
  a badge is *always* consistent with the record, and revoking is free.

**Streaks are the one thing that cannot be derived**, because "consecutive days"
needs a day boundary the counters do not carry. A `user_activity_day` table with a
unique `(user_id, day)` is the minimum honest representation. Computed streaks are
derived from it; the days themselves are facts.

## 5. Multi-user verification (SPEC §7.7)

"Important edits require multiple trusted confirmations" is **not built in this
phase** and the plan says so explicitly rather than leaving it implied: it is a
change to the existing edit/vote/apply path, which is the most safety-critical
code in the repository, and a curation engine bolted on beside it is not the way
to change how edits are accepted. It is Phase 2's *last* step, after the
completion engine is proven, and it is scoped in `docs/plans/` when it starts.

## 6. Verification, and what "green" is not

Every step below carries a mutation that must FAIL. A step whose verification is
"the tests pass" is not a step.

The recurring trap this phase must not fall into, having already hit it three
times in Phase 1: **a test that reads back its own return value tests nothing.**
The completion score is the sharpest version of that trap available — a test
asserting `score == expected` where `expected` is computed by the same function
under test is a tautology. So the completion tests assert against **hand-computed
values from hand-built fixtures**, with the arithmetic written out in the test, so
a change to the formula has to be a deliberate edit to a number someone can see.

## 7. Step order

| # | Piece | Depends on |
|---|---|---|
| 1 | completion scores — performer, studio, site, tag | — |
| 2 | completion scores — scene (joins, snapshot coverage) | 1 |
| 3 | GraphQL: expose the score and the missing list | 1, 2 |
| 4 | generated quests | 1, 2 |
| 5 | authored quests + bounties + claiming | 4 |
| 6 | XP award path through `RecordEvent` | 5 |
| 7 | derived badges | 6 |
| 8 | activity days + streaks | 6 |

Each step is a commit. Steps 1–3 are the load-bearing part; 4–8 are consumers of a
score that is already correct.
