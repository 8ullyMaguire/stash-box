# Phase 4 — federation protocol, taste-based peering, preservation replication, cross-instance discovery

**Status: not started. Depends on Phases 1–3. The largest phase, and the one
most likely to be attempted too early.**

This is a **protocol design**, not a feature. It cannot be correct before the
taste profile (Phase 1 Elo) and the preservation policy (below) have real data
shapes, because both are inputs to peering and neither exists yet. Attempting
federation before Elo produces stable taste vectors means building peering on
vectors that change shape every week.

---

## Step 1 — write the protocol spec before any code

`docs/plan/federation-protocol.md`, covering:

- **Instance identity**: a stable public key per instance. Content-addressed, so
  two instances cannot claim the same id.
- **Capability profile** (vision §2): storage capacity, bandwidth, uptime,
  content policy, replication limits, trust thresholds, supported features.
  Published and fetchable.
- **Taste profile** (vision §2): derived from Elo. **Published as derived data
  and recomputable**, never as truth — same rule as the Phase 1 fingerprint.
- **Wire format**: versioned, with a capability negotiation handshake. An
  unversioned protocol cannot be evolved, and this one must be.
- **Trust attestation** (vision §2): "trust and reputation can be attested across
  instances, while each operator still controls local permissions." The
  attestation is *advisory*; no instance is obliged to honour it. Say that
  explicitly in the spec, because it is the difference between a mesh and a
  hierarchy.

**Stop here and get the spec reviewed before writing Go.** This is the one place
where writing code first is clearly wrong.

## Step 2 — instance registry and peering

```sql
CREATE TABLE peer_instances (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- Content-addressed public key, unique. Two instances cannot collide.
    instance_key TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    capability_profile JSONB NOT NULL,
    taste_profile JSONB,
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Operator-set. Trust thresholds on OUR side are ours to set.
    peering_enabled BOOLEAN NOT NULL DEFAULT FALSE
);
```

Similarity scoring for taste-based peering goes in one function with a unit test
using **hand-computed fixtures**, not a test that asserts two instances with
identical profiles score > 0. That assertion is satisfiable by returning a
constant.

```bash
go test ./internal/federation/ -count=1 -v
```

Required: symmetric (score(a,b) == score(b,a)); a profile against itself scores
1.0; an empty profile scores 0 rather than NaN. That last one is the guard, and
it is the same class of bug as the completion-score NaN in Phase 2 Step 3.

## Step 3 — preservation policy and replication

Vision §3: a scene should exist on at least three instances by default.

```sql
CREATE TABLE preservation_policies (
    -- 'global' or a specific entity.
    scope_type TEXT NOT NULL DEFAULT 'global',
    scope_id UUID,
    min_replicas INTEGER NOT NULL DEFAULT 3,
    max_replicas INTEGER,
    -- Opt-outs, per vision §3.
    opted_out BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE replicas (
    scene_id UUID NOT NULL,
    instance_key TEXT NOT NULL,
    -- 'verified' from a manifest check, per vision §3.
    status TEXT NOT NULL DEFAULT 'pending',
    last_verified_at TIMESTAMPTZ,
    PRIMARY KEY (scene_id, instance_key)
);
```

Three correctness requirements, each of which has bitten this codebase before in
a different form:

1. **The policy must be evaluated against verified replicas only.** A replica
   that was never checked is not a replica. This is the #525 lesson in a new
   place: the database said one thing and reality another.
2. **Degraded replica counts are computed, not stored.** A stored count drifts
   the instant a peer goes offline, and vision §3 requires detecting that.
3. **Replication must not create a second write path for metadata.** Per vision
   §3, "metadata is always replicated across the mesh" — that is a *replication of
   edits*, not a direct write. Route through the edit system as in Phase 1 Step
   5.2.

```bash
export POSTGRES_DB="postgres:[REDACTED]@127.0.0.1:5434/stash-box-test?sslmode=disable"
go test -tags=integration -count=1 -run TestPreservation ./internal/api/
```

Required: a scene with two verified replicas and min_replicas 3 is reported as
under-replicated; the same scene with two *pending* replicas is also reported
under-replicated; a peer going offline increases the under-replicated set without
any write. The third is the guard on 2.

## Step 4 — cross-instance discovery

Vision §4: search one instance, query the mesh, rank by local gravity + peer
similarity + personal taste.

The **timeout and partial-failure contract** is the part that decides whether
this feature is usable, so specify it before implementing:

- Total budget, e.g. 2s, with a per-peer share.
- A peer that times out contributes nothing and does not fail the request.
- The response says which peers answered, so the UI can say "3 of 7 instances".

A federated search that fails because one peer is slow is worse than a
single-instance search, because the user cannot tell which happened.

```bash
go test ./internal/federation/ -count=1 -v
```

Required: one slow peer does not fail the query; the response reports the
answering peers; results are ordered deterministically for the same inputs
(clock or map-iteration order leaking into ranking is a real and invisible bug).

---

## Standing rules for every phase

These are the traps that actually cost time across the 26 issues already closed.
They are not hypothetical; each one produced a wrong result that had to be undone.

1. **Mutation-check every test.** Delete the implementation, run the test, watch
   it fail, restore. A test that passes on unfixed code is worse than no test.
   Five issues turned on this: #525, #950, #1007, #1177, #605.
2. **A test that exercises a helper is not a test of the caller.** In #605, four
   tests covered a content-type detector and all four stayed green when the line
   that *called* it was deleted. Always assert the wiring.
3. **The shared integration database is not isolated.** Any assertion about
   membership in an unfiltered collection must pin `PerPage`; the default is 25
   and other tests' fixtures push yours off the end. Hit in both directions
   (#829, #1007).
4. **A destroy mutation hard-deletes; a destroy EDIT soft-deletes.** Any test
   about `deleted = true` must apply the edit, or it tests a row that no longer
   exists. This silently produced four vacuous tests in #1007.
5. **`approveEdit` calls `t.Errorf` on any error**, so it cannot assert an
   expected failure. Call `ApproveEdit` directly and assert the `Applied` flag or
   the modbot comment — apply failures become `Unknown Error: %v` on the comment
   and the edit is marked failed, never returned as an error.
6. **A regex that matches anything proves nothing.** The e2e cooldown assertion
   in #1277 matched `/cooldown|wait/i` and so passed through the entire life of
   the bug it should have caught.
7. **Regenerate, then verify idempotence.** `sqlc generate` and `gqlgen` must be
   run twice with no diff. A green test run after failed codegen is not a pass.
8. **`internal/image` must stay in the unit run.** It was excluded until #1205,
   which is how a missing PNG/JPEG decoder (#948) stayed hidden behind a
   build-tag split.
9. **Rebuild the frontend before browser-verifying anything.** A stale
   `frontend/build` renders every route blank with HTTP 200. Use
   `node node_modules/vite/bin/vite.js build`, not `pnpm build` (hoisted linker).
10. **Do not hand-edit generated files.** `internal/queries/*.sql.go`,
    `internal/models/generated_*.go` and `graphql/generated.go` come from sqlc and
    gqlgen. Change the `.sql` / `.graphql` and regenerate.
