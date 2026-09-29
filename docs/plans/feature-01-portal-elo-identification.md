# Phase 1 — public metadata portal, snapshot collages, Elo voting, identification board

**Status: not started.** This is the first product phase and the largest single
piece of work in the vision. It is written to be executed by an LLM with no
prior context on this codebase.

Four deliverables: a public (unauthenticated) metadata portal, snapshot collages,
pairwise Elo voting, and an identification board. They share a prerequisite —
**trust levels** — which is nominally Phase 2 but cannot be deferred, because
"public" and "trusted" are not distinguishable without them. The split is
reconciled below.

---

## Step 0 — before any of it

### 0.1 The modbot race — DONE, do not repeat

`docs/SPEC.md` §8.1 recorded a race in the edit-apply path. It is fixed: the
bare `go func()` promoting user vote rights is now a synchronous call, covered by
`internal/api/edit_vote_promotion_integration_test.go`.

**Do not trust `go test -race ./internal/service/edit/` as verification of this
file.** It passes on the unfixed code, because the package has no concurrent
test reaching that path. If you re-verify, use the promotion test and confirm
it fails when the goroutine is restored.

### 0.2 Record the baseline

```bash
git rev-parse HEAD | tee docs/plan/.baseline
go build ./... && go vet ./...
export POSTGRES_DB="postgres:[REDACTED]@127.0.0.1:5434/stash-box-test?sslmode=disable"
go test -tags=integration -count=1 ./internal/api/
go test $(go list ./... | grep -vE 'internal/api$') -count=1
cd frontend && pnpm run test:run && cd ..
```

Write the results into `docs/track/WORKLOG.md`. Every later step is compared
against this.

### 0.3 Learn the edit system, because Phase 1 depends on it

Read, in this order:

1. `docs/SPEC.md` §3.3 — the edit/voting core.
2. `internal/service/edit/service.go` — `ApplyEdit` and the approve path
   (around line 1154). **Note that apply failures are swallowed into a modbot
   comment as `Unknown Error: %v` and the edit is marked failed.** Anything that
   asserts on an apply must assert on the comment or the `Applied` flag, never on
   the returned error.
3. `internal/service/edit/performer.go` — the `applyCreate` shape, including the
   duplicate check added for #950.

---

## Step 1 — trust levels (the prerequisite)

The vision defines six levels (0 Public through 5 Steward). The codebase has
`models.RoleEnum`. **Do not overload roles** — roles are an authorization
primitive (`admin`, `moderate`) and trust is a reputation score. Keep them
separate from the first commit, because merging them is unrecoverable once data
exists.

### 1.1 Migration

New file `internal/database/migrations/postgres/<next-number>_add_user_trust.sql`.
Find the next number:

```bash
ls internal/database/migrations/postgres/ | sort -V | tail -3
```

Schema:

```sql
CREATE TABLE user_trust (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    -- Denormalised rollup of the events below. Kept in sync by a trigger or by
    -- the service; the events are the source of truth.
    level INTEGER NOT NULL DEFAULT 0,
    -- Running totals, so a level can be recomputed without replaying events.
    approved_edits INTEGER NOT NULL DEFAULT 0,
    rejected_edits INTEGER NOT NULL DEFAULT 0,
    identification_solves INTEGER NOT NULL DEFAULT 0,
    quests_completed INTEGER NOT NULL DEFAULT 0,
    replicas_hosted INTEGER NOT NULL DEFAULT 0,
    -- Set when the user opts in to content viewing (vision §6, level 4).
    content_viewing_opt_in BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE trust_events (
    id SERIAL PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- e.g. 'edit_approved', 'edit_rejected', 'identification_solved',
    -- 'quest_completed', 'replica_hosted'
    kind TEXT NOT NULL,
    -- The entity the event refers to, for auditing and for undoing a reversal.
    entity_type TEXT,
    entity_id UUID,
    -- +1 or -1. Rejections and reversals are negative, so a revoked approval
    -- does not need its own event kind.
    delta INTEGER NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX trust_events_user_idx ON trust_events(user_id, created_at DESC);
```

**Verify the migration and only the migration:**

```bash
psql "$POSTGRES_DB" -c '\d user_trust'
psql "$POSTGRES_DB" -c '\d trust_events'
```

Expect the indexes and both foreign keys. If `sqlc generate` is wired to
migrations, run it and confirm the generated files change.

### 1.2 Queries and service

New file `internal/queries/sql/trust.sql`:

```sql
-- name: GetUserTrust :one
SELECT * FROM user_trust WHERE user_id = $1;

-- name: RecordTrustEvent :one
INSERT INTO trust_events (user_id, kind, entity_type, entity_id, delta)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ApplyTrustEventToTotals :one
INSERT INTO user_trust (user_id, approved_edits, rejected_edits, ...)
VALUES ($1, $2, $3, ...)
ON CONFLICT (user_id) DO UPDATE SET
    approved_edits = user_trust.approved_edits + EXCLUDED.approved_edits,
    rejected_edits = user_trust.rejected_edits + EXCLUDED.rejected_edits,
    updated_at = now()
RETURNING *;
```

```bash
sqlc generate && go build ./...
```

New file `internal/service/trust/service.go` with a `Level(userID)` function that
maps totals to a level. **Put the thresholds in one exported map and one
function**, with the numbers in a comment, because they are a product decision
and will be argued about:

```go
// trustThresholds maps a level to the minimum points required to reach it.
// The curve is deliberately steep at the top: level 4 (Archivist) is the point
// at which a user can host content, and it must be hard to reach by volume
// alone. Vision §6.
var trustThresholds = []struct {
	Level int
	Points int
}{
	{Level: 0, Points: 0},
	{Level: 1, Points: 10},      // registered
	{Level: 2, Points: 50},      // contributor
	{Level: 3, Points: 200},     // curator
	{Level: 4, Points: 1000},    // archivist
	{Level: 5, Points: 5000},    // steward
}
```

Verification, and the guard that matters:

```bash
go test ./internal/service/trust/ -count=1 -v
```

The level-0 case is the guard. A new user with zero activity must be level 0 and
must see only public data. Without that test, a thresholds bug makes an empty
account a curator.

**Mutation check:** change a threshold so level 0 requires 1 point, and confirm
the level-0 test fails. If it does not, the test is not testing the threshold.

### 1.3 GraphQL exposure

Add to `graphql/schema/types/user.graphql`:

```graphql
type UserTrust {
  level: Int!
  approvedEdits: Int!
  rejectedEdits: Int!
  identificationSolves: Int!
  questsCompleted: Int!
  replicasHosted: Int!
  contentViewingOptIn: Boolean!
}
```

and a field on `User`. **Read-only to the user for now** — the only mutation is
the opt-in:

```graphql
type Mutation {
  setContentViewingOptIn(enabled: Boolean!): UserTrust!
}
```

```bash
go build ./... && go run github.com/99designs/gqlgen generate
go build ./...
```

Do not hand-edit `internal/models/generated_*.go`.

### 1.4 Wire the event emitters

Exactly one place may call `RecordTrustEvent` for each kind. The obvious one is
`internal/service/edit/service.go` where an edit is applied:

- edit approved and applied → `edit_approved`, +points
- edit rejected → `edit_rejected`, −points

**Find the existing apply site before adding a new one.** #1060 and #941 both
changed notification triggers in this file; the pattern is there.

Verification:

```bash
export POSTGRES_DB="postgres:[REDACTED]@127.0.0.1:5434/stash-box-test?sslmode=disable"
go test -tags=integration -count=1 -run TestTrust ./internal/api/
```

The required test: a user who has an edit approved gains level, and a user whose
edit is rejected does not. Both directions, or the test only proves the happy
path.

---

## Step 2 — the public metadata portal

### 2.1 What "public" means here, precisely

Today every query requires authentication. "Public" must be a deliberate,
narrow set — not "remove auth" and not "everything at trust 0". The set:

- performer, scene, studio, site, tag **by id and by name**
- paginated lists of each, with filters
- image bytes
- Elo ratings (aggregate only — never an individual's votes)

Everything else — drafts, pending edits, notifications, private collections —
stays authenticated.

### 2.2 Middleware

New file `internal/api/middleware_public.go`. Read the existing middleware chain
first:

```bash
grep -rn "auth.Middleware\|Use(" internal/api/routes.go internal/api/*.go | head
```

Add a `requireTrust(level int)` middleware that resolves a level and compares.
**Anonymous is level 0, not "unauthenticated is rejected"** — that distinction is
the whole feature.

Verification:

```bash
go test -tags=integration -count=1 -run TestPublicAccess ./internal/api/
```

Four cases, all required: anonymous read succeeds; anonymous write fails;
level-1 write succeeds; level-1 cannot reach a level-3 field. The fourth is the
guard — without it an over-permissive middleware passes the first three.

### 2.3 Frontend routes

The frontend has a router. Find it before adding anything:

```bash
grep -rn "createBrowserRouter\|<Routes>\|<Route" frontend/src/ | head
```

Add a public shell that does not require the auth context, plus performer and
scene detail pages that render from the public queries.

**Verify in a real browser, not only in tests.** From memory: a stale
`frontend/build` makes every route render blank with HTTP 200, so rebuild first:

```bash
cd frontend
node node_modules/vite/bin/vite.js build     # NOT pnpm build: hoisted linker
cd ..
```

Then load the page and confirm it renders. A passing test suite does not prove a
route renders.

---

## Step 3 — snapshot collages

### 3.1 What a collage is

12–24 evenly spaced frames from a scene, hover-scrubbable, and — per vision §6 —
visible to low-trust users, with full playback gated on the level-4 opt-in.

**The hard constraint: this fork does not have the media.** The most recent issue
fixed in this area was #738/#948, and the whole image pipeline is stills. A
collage of *video* frames therefore requires either an ingest step that does not
exist or a source of pre-extracted frames.

**Decide this before writing code**, and record the decision in the worklog. The
two options:

- **(a) Store frames as images.** Reuses the existing `images` table and the
  `storage` backend unchanged. Cost: no motion, and 24 images per scene is a
  large storage multiplier — a scene with 5 images becomes 29.
- **(b) Store a single sprite sheet** and slice client-side. One image per scene,
  one extra HTTP request, no server-side slicing. **Prefer this.** It keeps the
  storage multiplier at 2× rather than 24×, which matters because preservation
  replication in Phase 4 multiplies storage again.

### 3.2 Schema (option b)

```sql
CREATE TABLE scene_collages (
    scene_id UUID PRIMARY KEY REFERENCES scenes(id) ON DELETE CASCADE,
    image_id UUID NOT NULL REFERENCES images(id) ON DELETE CASCADE,
    frame_count INTEGER NOT NULL,
    frame_width INTEGER NOT NULL,
    frame_height INTEGER NOT NULL
);
```

### 3.3 Generation

Generation is a job, not a request-time path. Put it behind the existing
background/cron mechanism if there is one:

```bash
grep -rn "cron\|scheduler\|Ticker" internal/ --include="*.go" | grep -v _test | head
```

If there is no scheduler, **this step is blocked** and the decision goes in the
worklog before continuing. Do not fake it with a synchronous hook; a collage
generated in the request path will time out on large scenes.

Verification:

```bash
go test -tags=integration -count=1 -run TestCollage ./internal/api/
```

Required: a scene with N frames produces `frame_count` slices that reassemble to
the original sprite sheet. Byte-exact reassembly is the assertion that matters —
a collage that is subtly wrong is worse than none, because it is used to identify
scenes.

---

## Step 4 — Elo voting

### 4.1 Use an existing Glicko-2 or TrueSkill library

Do not implement either. Check what the module graph already has:

```bash
grep -iE "glicko|trueskill|elo" go.sum | head
```

If nothing, add one. TrueSkill is the better fit for this data: it handles
teams and uncertainty natively, and Glicko-2's rating-deviation model assumes
more regular play than a vote every few weeks.

### 4.2 Schema

```sql
CREATE TABLE elo_ratings (
    entity_type TEXT NOT NULL,     -- 'performer' | 'scene' | 'studio' | 'site' | 'tag'
    entity_id UUID NOT NULL,
    -- Entity-level rating, aggregated from its voters.
    mu REAL NOT NULL,
    sigma REAL NOT NULL,
    match_count INTEGER NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (entity_type, entity_id)
);

CREATE TABLE elo_votes (
    id SERIAL PRIMARY KEY,
    voter_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    entity_type TEXT NOT NULL,
    entity_id UUID NOT NULL,
    -- The pairwise matchup, exactly as presented.
    winner_type TEXT NOT NULL, winner_id UUID NOT NULL,
    loser_type TEXT NOT NULL,  loser_id UUID NOT NULL,
    -- One vote per (voter, winner) per day, to bound stuffing.
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX elo_votes_voter_idx ON elo_votes(voter_id, created_at DESC);
```

The daily bound is not optional. Without it, Elo is trivially farmable and the
leaderboard is worthless — which matters because the vision makes the
leaderboard the retention mechanic.

### 4.3 The rating update must be in the same transaction as the vote

A vote recorded without its rating update is a lost vote; a rating updated
without its vote is a phantom rating. `internal/queries.WithTxnFunc` exists for
this.

**The two orders are both wrong, and this is worth stating:** recomputing both
entities from all votes is O(n) per vote and will not scale; updating a global
counter is not a rating algorithm. The correct shape is the standard online
update — apply the matchup to both entities' `(mu, sigma)` in the transaction,
and never recompute from history.

### 4.4 Personal taste fingerprint

Per vision §9, a user's votes produce a taste fingerprint. Store it as derived
data, not as a separate truth:

```sql
CREATE TABLE taste_fingerprints (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    -- Serialised sparse vector over entity ids. Rebuildable from elo_votes.
    vector BYTEA NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

**Rebuildable is the requirement.** If it cannot be rebuilt from `elo_votes`, it
will drift and nobody will know.

Verification:

```bash
go test ./internal/service/elo/ -count=1 -v
```

Required tests, in this order:

1. A matchup updates both entities' `sigma` as well as `mu`. A test that only
   checks `mu` passes with a broken implementation that never becomes confident.
2. The order of a matchup's two entities does not change the outcome. TrueSkill
   updates are order-dependent in their internals; a test that does not check this
   will pass with an asymmetric bug.
3. `match_count` increments exactly once per vote.
4. **The daily bound:** a second vote for the same pair in the same day is
   rejected. This is the guard on the whole leaderboard.

Mutation check each one by deleting the corresponding line.

---

## Step 5 — the identification board

### 5.1 Data model

```sql
CREATE TABLE identifications (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    author_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- The half-remembered thing. Either an entity (linked) or free text.
    linked_entity_type TEXT,
    linked_entity_id UUID,
    description TEXT NOT NULL,
    -- Collage or snapshot the poster attached, if any.
    image_id UUID REFERENCES images(id) ON DELETE SET NULL,
    status TEXT NOT NULL DEFAULT 'open',   -- 'open' | 'solved' | 'rejected'
    -- Set when solved: what it was actually identified as.
    solved_entity_type TEXT,
    solved_entity_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    solved_at TIMESTAMPTZ
);

CREATE TABLE identification_suggestions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    identification_id UUID NOT NULL REFERENCES identifications(id) ON DELETE CASCADE,
    suggester_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    entity_type TEXT NOT NULL,
    entity_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- One suggestion per (identification, suggester, entity).
    UNIQUE (identification_id, suggester_id, entity_type, entity_id)
);
```

The `UNIQUE` constraint is the anti-spam mechanism. Do not also enforce it in
the service; the database is the authority.

### 5.2 Solves become archive links

Per vision §5, a solved identification becomes a canonical link. On `status` →
`solved`, write an **edit** for the linked entity, not a direct update. Vision
§5 explicitly wants identification to "trigger metadata creation and
replication", and the edit system is what carries that into replication.

This is the step where a second write path is most tempting. Do not take it.

```bash
grep -n "func (m \*PerformerEditProcessor) createEdit" internal/service/edit/performer.go
```

Follow that shape.

### 5.3 Verification

```bash
export POSTGRES_DB="postgres:[REDACTED]@127.0.0.1:5434/stash-box-test?sslmode=disable"
go test -tags=integration -count=1 -run TestIdentification ./internal/api/
```

Required: a solved identification creates an edit that appears in the normal edit
queue and can be voted on. The assertion is on the **edit**, not on a directly
mutated entity — that is the difference between this design and a parallel write
path.

Also required: a duplicate suggestion is rejected by the `UNIQUE` constraint, not
only by the service. Test it by inserting twice at the SQL level.

---

## Step 6 — gamification for phase 1 only

XP and badges for the four things Phase 1 actually has: voting, submitting
suggestions, solving identifications, and submitting edits.

```sql
CREATE TABLE user_xp (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    xp INTEGER NOT NULL DEFAULT 0,
    level INTEGER NOT NULL DEFAULT 1
);
```

**Derive `level` from `xp`, do not store it.** A stored level drifts the moment
the curve changes, and every curve change becomes a migration. Same rule as the
taste fingerprint.

Badges as a definition table plus a grant table, so a badge can be granted
retroactively when its criteria are met by historical data:

```sql
CREATE TABLE badge_definitions (
    code TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT NOT NULL,
    -- The predicate, evaluated by Go. See badge.go.
    criteria JSONB NOT NULL
);
CREATE TABLE user_badges (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    badge_code TEXT NOT NULL REFERENCES badge_definitions(code),
    awarded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, badge_code)
);
```

Verification: the guard is that XP is a pure function of events. A test that
deletes an event and asserts XP *decreases* is the one that catches drift.

---

## Definition of done for Phase 1

- [ ] `go build ./...` and `go vet ./...` clean
- [ ] full integration suite green, `-count=1`, twice
- [ ] unit suite green including `internal/image` (see #1205 — do not exclude it)
- [ ] `sqlc generate` and `gqlgen` idempotent: run twice, no diff
- [ ] frontend `pnpm run test:run` green, and `pnpm run validate` has no *new*
      errors (one pre-existing `lint/style/noNonNullAssertion` in
      `TagForm.test.tsx:216` is documented and tolerated)
- [ ] `go test -race ./internal/service/edit/` clean (the §8.1 race, fixed in 0.1)
- [ ] every new test mutation-checked: delete the fix, watch it fail, restore
- [ ] every decision recorded in `docs/track/WORKLOG.md`, including the
      collage-storage decision from 3.1
