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

## Step 1 — migration: peer registry and foreign evidence

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

## Step 2 — peer registry service

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

## Step 3 — taste-based peer selection

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

## Step 4 — the broadcast payload type

**File:** `internal/service/federation/wire.go` (new),
`wire_test.go` (new).

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

## Step 5 — the client, and the local storage of answers

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

## Step 6 — wire it to a resolution, and to the operator surface

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

_(none yet)_
