# HANDOFF — the content aggregator (NexusXXX parity), 2026-10-14

Repo `/home/alvaro/code-local/go/stash-box`, branch `main`, HEAD `ca019a91`, pushed to
`origin` and `forgejo`. Plan `docs/plan/feature-content-aggregator.md`, spec
`docs/spec/feature-content-aggregator.md`, state table `docs/track/TRACK-content-aggregator.md`.

## Where the programme is

`docs/goal-check.py` is the authority and says: **C6b PASS** (44 integration packages green),
**C7 PASS** (build, vet, gofmt clean), **C8 FAIL** — 12 of 17 phases not done: 5, 6, 8, 9, 10, 11, 12,
13, 14, 15, 16, 17. Phases 1, 2, 3, 4 and 7 are done. Three clauses are outstanding.

**Phase 5 (tiers, warnings and gates) is nearly closed.** Slices 5a-5h are done, committed and covered
by tests that were run in the full serial suite. Slice 5i's source is committed and building; its tests
are not. Slice 5g is designed, not built.

## The three things left in phase 5, in order

### 1. Finish the parked 5i tests, then mark 5i done

Parked at `~/secondbrain/10-Projects/stashforge/wip/2026-10-14-slice5i-tests-parked.go.txt`. It does not
compile, for three reasons named at the top of that file: `viewer.userID` and `viewer.queries` do not
exist on `testRunner` (five fields: `t`, `client`, `resolver`, `ctx`, `err` — reach the query through the
resolver's service or add the handles), and the file needs the `trust` import. Everything else in it is
believed right: the drift-guard assertions, the 17-with-99-rejections fixture (rejections are EXCLUDED
from the contribution total, and the assertion says so), the first-acceptance-stands check, and the
backdated-cooling check that proves `availableAt` reports the cooling rather than echoing a timestamp.

When they pass, phase 5's onboarding item is done and `TaskTheTierLadderReportsTheCooling` is the only
test in the package that needs the READ user's tiers, so CLEAR THEM IN A CLEANUP.

### 2. Slice 5g — the denylist on DIRECT access (designed in the plan, section "Slice 5g")

Slice 5f gates the four ENUMERATING paths; a direct fetch still serves a denylisted scene. The design
section measures the paths rather than guessing them: `FindByID`, the `SceneByID` dataloader that
`FindScenes(ids)` and every embedding entity go through, the fingerprint lookup, and the changelog. The
decision to make first is in the plan: **a denylisted scene reads as NOT FOUND** — "denied" and "does
not exist" must not be distinguishable to a reader. What must NOT be gated is also in the plan: the
operator report (`AccessRules` must still see the entries) and the edit/moderation paths, or the only
way to un-denylist something is to bypass the API.

Verification: a denylisted scene's id returns null on each named path, AND the edit path still works,
AND the operator report still lists the entry. Until it lands, the operator report's `Enforced: true` is
true of enumeration and not of direct access — the plan notes the field's contract leaves two honest
options: build it, or narrow the claim.

### 3. Then phase 5 is closed

The frontend onboarding wizard is named under phase 5's "Also" and has not been started; it consumes
`contentAccessSetup` and `userAcceptContentTerms`, both now on the schema.

## After phase 5

The TRACK's priority order (owner instruction: content aggregation and discovery first) is
1 → 2, then 4 → 7, then 3 → 9, then the supporting features 5 → 8 → 6 → 11 → 13 → 14 → 10 → 12 → 15 → 16,
with 17 threaded through every phase. Phases 1-4 and 7 are done, so **the next unfinished phase after 5
is 9 (preservation at scale, plan line ~1299)**, then 8 (social), then 6, 11, 13, 14, 10, 12, 15, 16.

Phase 7's own slice 7 (the social half and "where I left off", migrations 115/116) was recorded as
remaining in an earlier session — check whether closed before treating 9 as next.

## Traps this session paid for — read before writing code

- **sqlc parses an EXPLICIT migration list.** `sqlc.yaml`'s schema paths come from
  `scripts/gen-sqlc-schema-list.sh`; after adding a migration, run it, or sqlc fails with a confusing
  `column ... does not exist` (it did for migrations 121-123).
- **`go vet ./pkg/` exit 0 is NOT proof the test package compiles.** It reported 0 while this slice's
  tests did not compile at all. The compile check is `go test -tags integration -run TestAnything ./pkg/`.
- **gqlgen order: schema → generate → implement.** `go run github.com/99designs/gqlgen generate` (the
  module is `99designs/gqlgen`); the model only exists after the run, and a field resolver binds to
  VALUE slices, never pointers.
- **`cmd | head && echo OK` prints OK when the command failed** — a pipeline's status is the last
  command's. Use `${PIPESTATUS[0]}`, or check for output.
- **Cut a mutation by making it unsatisfiable, never by deleting it**: deleting a check leaves its
  variables unused, the package stops compiling, and no verdict was produced (`row.Tier > visibleTier+99`).
- **Fixtures that touch instance-wide state need `t.Cleanup` that deletes exactly their own row.** A
  leftover denylist row makes `ContentDenylistActive` true for every later test and silently changes which
  path the trending query takes.
- **The READ/EDIT/MODIFY users are SHARED across every test in `internal/api`.** Set your own state and
  clear it in a cleanup; a test that leaves `visible_tiers` set changes its neighbours.
- **`EvaluateContentAccess` still has NO production caller.** Enforcement of tiers and the denylist lives
  in the SQL predicates; the gate is a decision table the access-setup query is now asserted against.
  Wiring the gate into a per-entity read path is an open design question, not a bug.

## Verifying a session's work

```
cd ~/code-local/go/stash-box
export POSTGRES_DB='postgres:smoke_pw@127.0.0.1:55434/stash-box-test?sslmode=disable'
go test ./...                                                  # unit
go test -tags integration -count=1 -p 1 ./...                  # serial: packages share one DB
gofmt -l internal/ ; go vet ./...
python3 docs/goal-check.py
```

Integration tests MUST run with `-p 1`: every package's TestMain drops the public tables, so parallel
runs report bogus "relation already exists" failures in unrelated packages.
