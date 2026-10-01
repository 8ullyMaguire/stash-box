# Feature plans — the federated discovery mesh

One file per roadmap phase from the product vision (`docs/SPEC.md` §7.15). Each
is written to be executable by an LLM with no prior context: exact files, the
schema and resolver shape, the verification command per step, and the traps that
cost time in the 26 issues already closed.

**Nothing here is implemented.** The issue work is done (26 of 48 `help wanted`
triaged, 21 code fixes); these are the product phases, and they are far larger
than the issues. They are specced here rather than in `docs/PLAN.md` because
`docs/PLAN.md` is deliberately unwritten until Phase 1 is specced.

## Read these before writing any of it

1. **The edit/voting core is the domain heart, not a detail.** `internal/service/edit/`
   is where an entity actually changes. Every phase below that adds user-facing
   data must decide how that data enters the archive: directly, or as an edit
   that needs votes. Writing around the edit system produces a second, parallel
   write path that will diverge. `docs/SPEC.md` §3.3 describes the flow.
2. **Generated vs hand-written is not a matter of taste.** `internal/queries/*.sql.go`
   comes from `sqlc`, `internal/models/generated_*.go` and `graphql/generated.go`
   come from `gqlgen`. Never hand-edit a generated file; change the `.sql` or the
   `.graphql` and regenerate. A green test run after *failed* codegen is not a
   pass.
3. **Migrations are append-only and shared.** `internal/database/migrations/postgres/`
   is numbered and never edited after merge. A migration that changes a unique
   index (the mesh phases need several) must be a new numbered file.
4. **The trap that has cost the most time, stated once.** A test that passes on
   unfixed code, or that fails for the wrong reason, is worse than no test. For
   every behaviour in these phases: write the test, delete the implementation, run
   it, watch it fail, restore. Five of the 26 closed issues turned on this
   (#525, #950, #1007, #1177, #605).

## The five phases

| Phase | File | Depends on |
|---|---|---|
| 1. Public metadata portal, snapshot collages, Elo voting, identification board | [phase-1-portal-elo-identification.md](phase-1-portal-elo-identification.md) | — |
| 2. Trust levels, opt-in viewing, gamification, curation quests, completion scores | [phase-2-trust-quests-completion.md](phase-2-trust-quests-completion.md) | Phase 1 |
| 3. Site/studio directory, reviews, Stash integration, public API, browser extension | [phase-3-directory-ecosystem.md](phase-3-directory-ecosystem.md) | Phase 1, 2 |
| 4. Federation protocol, taste-based peering, preservation replication, cross-instance discovery | [phase-4-federation-preservation.md](phase-4-federation-preservation.md) | Phase 1–3 |
| 5. Mobile app, awards, advanced recommendations, mesh-wide curation campaigns | [phase-5-mobile-awards-recommendations.md](phase-5-mobile-awards-recommendations.md) | Phase 1–4 |

## Sizing, honestly

Phases 1 and 2 are each larger than all 26 issues combined. Phase 4 is the largest
— a federation protocol is a protocol design, not a feature, and it cannot be
correct before the taste and preservation models in phases 1–3 have real data
shapes. Attempting them in order is right; attempting them quickly is not.

## Where this work lands

| Branch | Contents |
|---|---|
| `main` | Everything: the issues solved so far, the pulls of upstream PRs, and every phase below. The product vision lives here. |

There is one branch. Work lands on a topic branch and is merged into `main` as it
is finished, then the topic branch is deleted — so `main` is the only thing that
needs to stay current, and no branch can hold finished work `main` lacks. Upstream
fixes are no longer separated onto a branch of their own; they are ordinary commits
on `main`, identified by their commits and by the port table in
[`../plan/upstream-pr-port.md`](../plan/upstream-pr-port.md) rather than by which
branch they sit on.

The two-branch model this replaced is recorded in
[`../publishing-plan.md`](../publishing-plan.md), including why it was dropped.

## The modbot.go race — FIXED

Recorded in `docs/SPEC.md` §8.1 and previously a Phase 1 blocker. It is now
fixed: the bare `go func()` in `internal/service/edit/service.go` that promoted
user vote rights after an edit apply is now a synchronous call.

The bare goroutine did two things wrong. It outlived the request, so any caller
of `ApplyEdit` that then read the author's roles was racing — flaky, not
failing, which is why it survived. And it detached from the request context
entirely, so nothing could ever cancel it.

Covered by `internal/api/edit_vote_promotion_integration_test.go` (3 tests).
The primary one asserts the promotion is **complete by the time `ApplyEdit`
returns** — no sleep, no retry, no `Eventually`. That is what makes it
deterministic: with the goroutine it fails every run, which was verified by
restoring the original code.

Two traps found while writing it, both worth knowing before writing another test
against this package:

- **Roles go through a per-request dataloader cache.** `userResolver.Roles`
  reads via `dataloader.For(ctx).UserRolesByID`, so a test that reads roles
  through the resolver sees a cached value no matter what the code did — it
  would pass for the wrong reason *and* miss the race. The tests read the roles
  table directly through `dbtest.Factory().User().GetRoles`.
- **`createTestUser` only copies its `roles` argument into the input when the
  input is nil.** Passing a non-nil input without `Roles` silently creates a
  user with no roles at all. And a nil `roles` argument defaults to **Admin**,
  which implies Vote, so an "author who cannot vote" built that way is promoted
  trivially and the test proves nothing.

**`go test -race ./internal/service/edit/` passed on the unfixed code** and still
does. That package has no concurrent test reaching this path, so a clean race run
was never evidence of anything here.
