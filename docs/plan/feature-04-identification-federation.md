# PLAN — feature 04: identification board federation (SPEC D2)

Implements `docs/spec/feature-04-identification-federation.md`. Read that first;
this file assumes its decisions F1–F6 and does not restate them.

Sequenced so each step is independently verifiable and nothing is built against
an API that does not exist yet.

**The order is forced by two dependencies, and both are load-bearing:**

1. **Migration before service.** The peer registry and the foreign-evidence table
   must exist before any code that queries them, or the first thing a test does
   is fail on a missing relation. This mirrors the 03b plan's Step 2.1-before-2.2
   shape.
2. **Taste matching before broadcast.** A broadcast that goes to every
   configured peer is not federation, it is fan-out, and it is the shape the
   `vote_count` floor exists to prevent. Selecting peers has to be built and
   tested before anything can be sent, so there is never a version of this
   feature that broadcasts unconditionally.

---

## Step 1 — migration: peer registry and foreign evidence — DONE (`d3934900`)

**File:** `internal/database/migrations/postgres/89_identification_federation.up.sql` (new)
and `89_identification_federation.down.sql` (new).

**Why 89:** the tree's highest applied migration is 88
(`88_scene_title_text.up.sql`, the renumbered upstream #1262 port). Verify
before writing, because a collision is a hard startup failure that kills every
integration test:

```bash
ls internal/database/migrations/postgres/*.up.sql | sed 's#.*\///' | cut -d_ -f1 | sort | uniq -d
```

**Empty output means no collision.**

```sql
-- A peer this instance knows about. Operator-configured (F5): nothing here is
-- discovered, and nothing here extends itself.
CREATE TABLE "federation_peers" (
    "id" UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    "name" VARCHAR(255) NOT NULL,
    -- Base URL of the peer's API. NOT NULL because a peer row with no address
    -- is a row that can never be asked and should not exist.
    "base_url" TEXT NOT NULL,
    -- The peer's self-declared instance identity, hex. Unique so the same
    -- instance cannot be registered twice under two names and be asked twice,
    -- which would double-count its evidence.
    "instance_id" VARCHAR(128) NOT NULL UNIQUE,
    -- Operator-set trust weight in (0,1]. Multiplied into every piece of
    -- evidence this peer contributes, so a peer's word is worth less than the
    -- local community's (F5).
    "trust_weight" DOUBLE PRECISION NOT NULL DEFAULT 0.5
        CHECK ("trust_weight" > 0 AND "trust_weight" <= 1),
    "enabled" BOOLEAN NOT NULL DEFAULT TRUE,
    "last_seen_at" TIMESTAMPTZ,
    "created_at" TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    "updated_at" TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- A peer's answer to one of our queries.
--
-- NOT "identification_candidates", deliberately (F2). This table is evidence
-- with an origin; the local table is a suggestion the local community can vote
-- on. Keeping them apart is what stops a remote suggestion from entering the
-- local vote path and becoming a canonical link.
CREATE TABLE "identification_foreign_candidates" (
    "id" UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- The LOCAL query this answers. Never a remote id: a query id from another
    -- instance is not a key here and must not be trusted as one.
    "query_id" UUID NOT NULL REFERENCES "identification_queries" ("id") ON DELETE CASCADE,
    "peer_id" UUID NOT NULL REFERENCES "federation_peers" ("id") ON DELETE CASCADE,
    "entity_type" VARCHAR(32) NOT NULL,
    -- The peer's id for the entity. NOT a local id and never resolvable as one.
    "remote_entity_id" VARCHAR(255) NOT NULL,
    -- Denormalised so the operator UI can show what was proposed without
    -- contacting the peer again, and so a dead peer still leaves a readable
    -- record of what it claimed.
    "remote_entity_name" VARCHAR(255) NOT NULL,
    "remote_vote_count" INTEGER NOT NULL DEFAULT 0,
    -- The peer's answer is only about THIS query (F3).
    "fetched_at" TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE ("query_id", "peer_id", "entity_type", "remote_entity_id")
);

CREATE INDEX "identification_foreign_candidates_query_idx"
    ON "identification_foreign_candidates" ("query_id");
```

The `down.sql` drops foreign candidates first, then peers — the reverse of
creation order, so the FK never dangles.

**Verify:**

```bash
go build ./... && go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.29.0 generate
go build ./...
```

Both must be silent. Then confirm the migration applies, and that the unique
constraint is real rather than believed:

```bash
go test -tags=integration -count=1 ./internal/database/... 2>&1 | tail -3
```

## Step 2 — peer registry service — DONE (`23da1f6a`, `e2ee78b1`)

**File:** `internal/service/federation/peer.go` (new),
`internal/service/federation/peer_test.go` (new).

Pure CRUD plus the staleness rule. No network I/O in this file — the
registry's whole job is to be the thing the network code asks, not to do the
asking.

The staleness rule (F3) is the part that needs a test: a peer not seen inside
the window is not asked. Window constant:

```go
// PeerStaleness is how long a peer's last successful contact stays fresh.
// Past it the peer is not asked, because F3 scopes a peer's answer to the
// moment it was given: a two-week-old answer presented as current is worse
// than no answer.
const PeerStaleness = 7 * 24 * time.Hour
```

**Tests, table-driven, one row per condition:**

| Case | Assert |
|---|---|
| peer never seen (`last_seen_at` NULL) | not fresh — an unknown peer is not asked |
| seen 1 hour ago | fresh |
| seen exactly `PeerStaleness` ago | fresh (boundary is inclusive) |
| seen `PeerStaleness + 1ns` ago | not fresh |
| `enabled = false` | never asked, regardless of freshness |

**Verify:**

```bash
go test ./internal/service/federation/ -run TestPeerFresh -v
```

Expect every case to pass. Then **break it** — this is the plan's mutation
obligation. Change the boundary comparison from `<` to `<=` and confirm
`seen exactly PeerStaleness ago` goes red naming that case. A boundary test
that has never been inverted is a comment.

## Step 3 — taste-based peer selection — DONE (`f5601107`)

**File:** `internal/service/federation/select.go` (new),
`internal/service/federation/select_test.go` (new).

Pure function, no I/O, so it is table-testable without a database:

```go
// SelectPeers ranks candidates by taste similarity to the asker and returns
// the top n. F4: a peer whose taste vector was built from fewer than
// MinTasteVotes is not evidence of a preference and is excluded outright
// rather than ranked last, because "ranked last" still means "asked" at a
// large n, and at n=5 out of 6 peers it is asked.
func SelectPeers(asker TasteVector, candidates []PeerTaste, n int) []PeerTaste
```

`TasteVector` is `{Scores map[string]float64, VoteCount int}`.

**The zero cases, decided and written into the doc comment, not left implicit:**

- **Asker has no vector** (a user who has never voted). Cosine against nothing
  is undefined. Decision: return the asker's own instance peers' defaults —
  no. Decision: return **empty**. A user with no taste has no basis for
  preferring one peer over another, and returning an arbitrary order would make
  the federation's choice look considered. The caller falls back to no broadcast
  and the query stays local.
- **A candidate peer has no vector.** Excluded, same reasoning as the floor.
- **Cosine is zero or negative** (disjoint tastes). Kept, ranked last. Zero
  similarity is a real answer — "this peer disagrees with you" — and dropping it
  would make the selection silently prefer peers that share nothing.
- **n <= 0.** Returns empty. Not "all": a caller that computes `n` wrongly gets
  a no-op and a log line, not a broadcast to every peer.

**Tests:** one row per case above, plus a cosine correctness case with
hand-computed values, plus one proving `n=1` on a 5-peer input returns exactly
one peer.

**Verify:**

```bash
go test ./internal/service/federation/ -run TestSelectPeers -v
```

**Mutation obligation — each of these must fail:**

1. Remove the `MinTasteVotes` filter → the low-vote-exclusion case goes red.
2. Change `n <= 0` to `n < 0` → the `n == 0` case goes red.
3. Drop negative similarities from the sort → the disjoint-taste case goes red.

## Step 4 — the broadcast payload type — DONE (`7704600c`)

**File:** `internal/service/federation/wire.go` (new),
`wire_test.go` (new). Plus `ask.go` / `ask_test.go` / `addr_drift_test.go`,
added because the reflection test alone does not make F1 true — see the note at
the end of this section.

**This step exists to make F1 checkable.** "Content never broadcasts" is only
meaningful if the payload cannot carry content, so the type is defined first and
reflected over in a test:

```go
// Question is what this instance asks a peer. F1: every field is text or a
// scalar. There is no field a snapshot, a collage, an image url or a media
// reference can be put into, and wire_test.go reflects over this struct to
// prove the set of field types is closed.
type Question struct {
    QueryID        uuid.UUID
    TargetType     string
    Description    string
    CandidateNames []string
}

// Answer is a peer's reply: the candidates it already has, with how many of its
// own users suggested each. F2: it carries the peer's ids and names, and this
// instance resolves NOTHING from them.
type Answer struct {
    PeerInstanceID string
    Candidates     []RemoteCandidate
}
```

**Tests:**

- Reflect over `Question` and `Answer`; assert every field's kind is one of
  string, `[]string`, `uuid.UUID`, int, or `[]RemoteCandidate`. Any other kind —
  especially `[]byte`, `any`, or a struct with a URL field — fails the test by
  name.
- A round-trip encode/decode of a full `Question` and `Answer`, asserting the
  decoded value is deep-equal. A wire format that drops a field is a bug that
  only shows up against a real peer.

**Verify:**

```bash
go test ./internal/service/federation/ -run TestWire -v
```

### Step 4 notes — what the plan did not say

Implemented as planned, with two additions the tests forced and one deliberate
narrowing.

**Added: `ask.go` (`Question.Validate` + the value-level content check).** The
plan's Step 4 only asked for the payload type and a reflection test. That proves
the field *types* cannot hold content — but every field in `Question` is a
`string`, and a string holds `/etc/passwd`. The structural half is necessary and
not sufficient, so `Validate` runs on the way out and rejects (never truncates)
suspicious values. Three defects it found, all recorded in the commit:

- a bare `"://"` substring check rejected the legitimate names `AC/DC` and any
  description containing "the http:// era" → now scheme-aware;
- a bare IP as a candidate name passed everything → now parsed and classified;
- `metadata.google.internal` passed everything → now a closed hostname list,
  because a hostname has nothing to parse.

**Added: `addr_drift_test.go`.** `federation.isInternalIP` duplicates
`webhook.isPublicIP` rather than importing it. The test exists only to make that
duplication fail loudly if one side moves.

**Narrowed: the length caps reject rather than truncate.** The plan did not say
which. Truncation returns answers to a *different* question and two operators
comparing answers cannot tell, so `Validate` returns an error naming both the
actual and maximum length, and leaves the question unmodified.

**Deviation on the drift test's mechanism:** `webhook.isPublicIP` is unexported,
so the comparison goes through `webhook.ValidateTarget`, comparing two
implementations that both answer "may the box reach this address?". This is
weaker than comparing the predicates directly and is noted here so nobody reads
the green as a stronger claim than it is.

## Step 5 — the client, and the local storage of answers — DONE (`d3934900`, `f07cc48d`, `9e794f9c`)

**File:** `internal/service/federation/client.go` (new),
`store.go` (new).

`Broadcast(ctx, peers []Peer, q Question) ([]Answer, error)` — one goroutine
per peer, a per-peer timeout, and a context that cancels the rest when the
overall budget expires. A single slow peer must not hold a query open.

**`store.go` writes `identification_foreign_candidates` and nothing else.** This
is the F2 boundary, and it is enforced structurally: the only INSERT statement
in the federation package targets the foreign table. There is no code path from
an `Answer` to `identification.Service.Suggest`, `Vote`, or `Resolve`.

**Tests:**

- A store test that records an answer, then asserts `ListCandidates` on the
  local identification service **does not** contain the foreign candidate.
- An attempt to resolve a query using only foreign candidates, asserting it
  errors.
- A client test against an `httptest.Server` that returns one `Answer`, asserting
  exactly one row lands in the foreign table and zero in the local one.

**Verify:**

```bash
go test -tags=integration -count=1 ./internal/service/federation/ -v 2>&1 | tail -20
```

## Step 6 — wire it to a resolution, and to the operator surface — DONE (`8338a797`, `cdb90055`)

**File:** `internal/service/federation/service.go` (new, the Factory entry),
`graphql/schema/types/federation.graphql` (new).

Adds `federation.peerCreate/peerList/peerDelete` and
`federation.queryForeignCandidates`. Read-only for the query path: there is no
mutation that turns foreign evidence into a local candidate, because F2 forbids
it and an operator override would be the same hole with a smaller door.

**Verify:**

```bash
go build ./... && go vet ./...
go test -tags=integration -count=1 ./internal/api/... 2>&1 | tail -5
```

Then the manual check the automated tests cannot do — the operator surface has
to render, because a GraphQL field that validates is not a feature someone can
use:

```bash
curl -s -X POST http://127.0.0.1:9998/graphql -H 'Content-Type: application/json' \
  -d '{"query":"{ federationPeers { name baseUrl trustWeight enabled } }"}'
```

Expect `{"data":{"federationPeers":[]}}` on a fresh instance, not an error.

## Definition of done

- `go build ./...`, `go vet ./...` clean.
- `go test ./...` green **including the pre-existing suites — no test deleted or
  weakened to make a new one pass.**
- Every mutation in Steps 2, 3 killed, or the missing test written.
- The `wire_test.go` reflection test passes, proving `Question`/`Answer` cannot
  carry content (F1).
- The store test proves foreign evidence cannot reach the local vote path (F2).
- SPEC §7.23 D2 marked implemented in `docs/SPEC.md`; D6's deferral recorded
  with its reason.
- This plan's own "Deviations" section updated.

## Deviations

**Step 1 (2026-09-30, `d3934900`) — the F2 boundary is enforced by the schema, and
the plan asked for a service-layer discipline instead.** The plan says `store.go`
"writes `identification_foreign_candidates` and nothing else" and calls that
"enforced structurally". It is enforced more strongly than that: the evidence
table has **no column that can hold a local entity id**, so no INSERT is
reachable that could write one — an assertion, not a convention. The test the
definition of done asked for was missing from step 1 and is `f07cc48d`.

**Step 2 (2026-09-30, `23da1f6a`, `e2ee78b1`) — the guard was written before
anything could call it, and that is why the plan's step 2 read as complete a day
before it was.** The validator `ValidateBaseURL` landed first, with the write path
arriving in a second commit. In between, the repository was in a state where a
complete, mutation-tested guard existed and **nothing in the program called it** —
including its own test, which exercised the function directly. The plan's step 2
should have been marked done only at `e2ee78b1`.

**Step 5 (2026-09-30, `9e794f9c`) — the plan's step 5 was already written and
sitting untracked in another worktree, and the plan's own definition of done was
therefore unmeetable for two sessions.** This is the one that matters, so it is
written at length.

The plan specifies `client.go` as a file to be **created**. It already existed, in
another session's working tree, containing:

```go
if err := error(nil); err != nil {   // always false
```

**The F1 content guard this plan's step 4 built the validator for had never run.**
Any question carrying a path, a URL or an internal hostname was broadcast to every
askable peer — the precise thing F1 exists to prevent. It compiled clean and
returned no error, because a guard that cannot fire is nothing `go build` has an
opinion about.

Its own regression test, `TestBroadcastRefusesDirtyQuestion`, asserts exactly
this, and lived in the same untracked file. **The defect and the test that catches
it were in one place and neither was in the build** — an untracked file is
unreachable from any import, so a test that has never been compiled cannot catch
anything, and neither can the guard it was written for.

The plan is silent on all of this because it could not be: the plan assumed a file
that did not exist, and the file that did exist was not the plan's. Deviating from
the plan was therefore not a choice — the plan could not be followed as written
until someone noticed that its premise was false.

*Two consequences for whoever runs plans like this one. First: a step naming a
file to be created should begin by checking whether that file already exists,
somewhere, untracked — the compile-then-copy path is not only legal, it is usually
faster than writing it again. Second: the plan's own tests are part of its
premises. When they travel with the file they belong to, they certify nothing.*

**Step 6 (2026-09-30, `8338a797`, `cdb90055`) — the plan asked for one wiring
step; the guard was unreachable from two directions, and each needed its own
commit.** Rule 2's validator was not callable from outside the package, and there
was no operator surface at all, so "wired to a resolution and to the operator
surface" was two distinct pieces of work with different failure modes. The write
path (`e2ee78b1`, from step 2) and the read path (`cdb90055`) are also separate:
the read path is what makes a peer-sourced candidate **visible to an operator**,
which is the only way the F2 boundary can be observed rather than merely asserted.
The plan says "wire it to a resolution" and does not say to what — recording that
as a deviation because the plan's single step hid two unreachable surfaces.

**Step 4 (2026-09-30, `7704600c`) — three, all recorded above.** Added a
value-level content guard the plan did not ask for, because the plan's own F1
requirement is not met by a reflection test alone; added a drift test for a
deliberate duplication; and chose rejection over truncation for the length caps,
which the plan did not specify either way.

**Step 3 (2026-09-30, `f5601107`) — the `n<=0` guard is asserted white-box.** Its
behaviour is identical with and without the guard, because the final truncation
produces the same empty result. Two attempts to kill the `n<0` mutant through
the return value failed. `TestNGuardIsExplicit` reads the source instead, so a
later edit cannot delete the guard as redundant — which is how it looks from the
outside.

**Step 3 — three of `Cosine`'s guards are individually removable and every
removal survives the suite.** Measured over 81 adversarial pairs, not reasoned
about (reasoning gave the wrong answer twice): a zero-magnitude-only guard leaves
31 pairs returning NaN, while the non-finite-denominator and non-finite-result
guards are each sufficient alone. The leading NaN/Inf input scan was therefore
deleted rather than left as untestable insurance, and the two surviving guards
are kept because they cover two different *steps* of the arithmetic.
