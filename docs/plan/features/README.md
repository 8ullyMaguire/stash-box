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

The `modbot.go` race recorded in `docs/SPEC.md` §8.1 is still untouched and is
not part of any phase below. It should be fixed before Phase 1, because Phase 1
puts more load on the notification path that races.
