# Plan — Phase 4: federation, mesh architecture, and preservation (SPEC §7.17–§7.22)

**Spec:** `docs/SPEC.md` §7.17–§7.22 (amended 2026-09-29).
**Intake review:** `docs/track/INTAKE-2026-09-29-federated-mesh-v2.md`.
**Status:** written before any code, per the standing workflow. No migration in
this plan has been written.

**Supersedes `feature-04-federation-preservation.md`** (same phase, written
earlier and covering the federation half). That file's Steps 2–3 are folded into
Steps 2 and 5 below and its Step 4 is preserved verbatim as Step 7b, because
cross-instance discovery was the one thing it covered that this plan did not. Two
plans for one phase is how two implementations of one table get written from two
documents that disagree about column names; the earlier file is left in place as a
pointer rather than deleted, so a link to it still resolves.

**Scope note:** this is Phase 4 of a six-phase roadmap, and it is the first phase
whose *predecessors* are still unbuilt. Phase 2 step 3 (generated quests) is
next in line; nothing here is blocked by it, and nothing here blocks it. Phase 4 is
written as its own plan so the two can proceed in whichever order the owner
prefers.

**Hard rules for whoever executes this plan**

1. **Never store a value that is recomputed from rows.** §3.3 says this for edit
   votes and §7.17.2 extends it to attested trust levels. A stored level without
   its events is a second source of truth that cannot be audited.
2. **A read-only mirror holds no write credential.** §7.17.1 makes "accepts no
   writes" a property of its key, not a policy. Do not implement it as a role
   check — a role check is a request-time decision, and a request-time decision can
   be wrong.
3. **Do not build a public swarm protocol.** §7.19 rejected it with reasons. The
   content plane is instance-to-instance. If you find yourself reaching for DHT,
   BitTorrent, eDonkey, Kad, or IPFS, stop and re-read §7.19.
4. **Metadata sync is onion-routed; content is not.** §7.17.4. The two planes have
   different anonymity properties and putting content on the metadata path
   de-anonymises peers.

---

## Step 1 — Instance identity and federation keys

**Migration 83** (next free number; §7.16-era work took 79, and the
`elo`/`collage`/`identification`/`completion` slices took none because they are
service-only). Confirm the number against the live directory before writing:

```bash
ls internal/database/migrations/postgres/ | tail -3
```

```sql
CREATE TABLE instances (
    id UUID PRIMARY KEY,
    -- The instance's stable identity, distinct from its row id: an instance that
    -- moves to a new database keeps its identity, and federation references must
    -- survive a migration.
    fingerprint TEXT NOT NULL UNIQUE,

    name TEXT NOT NULL,
    -- A capability profile, per SPEC §1.2. NOT NULL with a default rather than
    -- nullable: an instance with no published profile cannot be peered, and
    -- "absent" and "publishes nothing" are the same state.
    capability_profile JSONB NOT NULL DEFAULT '{}'::jsonb,
    trust_thresholds JSONB NOT NULL DEFAULT '{}'::jsonb,

    -- Federation keys: signed public keys for metadata authentication (§1.2).
    -- An ARRAY because keys rotate, and a single key column cannot represent a
    -- rotation without either losing history or overwriting the past.
    federation_keys JSONB NOT NULL DEFAULT '[]'::jsonb,

    -- Roles this node offers (§7.17.1). Capabilities, not permissions claimed.
    roles TEXT[] NOT NULL DEFAULT '{}',

    public_key TEXT NOT NULL,
    private_key_encrypted TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- A taste vector is high-dimensional and instance-defined (§1.2), so it is NOT
-- a fixed-width float array. Stored as JSONB with a dimension recorded alongside,
-- because cosine similarity between vectors of different dimensions is undefined
-- and silently wrong rather than an error.
CREATE TABLE instance_taste_vectors (
    instance_id UUID PRIMARY KEY REFERENCES instances (id) ON DELETE CASCADE,
    dimensions INTEGER NOT NULL CHECK (dimensions > 0),
    vector JSONB NOT NULL,
    computed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

**Verify:**

```bash
/tmp/sqlc generate && go build ./...
psql "$POSTGRES_DB" -c '\d instances'
```

Expected: two new tables, `roles` and `federation_keys` non-null with defaults.

**Test:** `TestAnInstanceCannotPublishATasteVectorOfTheWrongDimension` — insert a
vector whose length differs from `dimensions` and assert the write is refused.
The failure without it is a cosine computed over a truncated vector, which returns
a plausible number.

---

## Step 2 — Peer relationships and peering tiers

```sql
CREATE TABLE peers (
    id UUID PRIMARY KEY,
    local_instance_id UUID NOT NULL REFERENCES instances (id) ON DELETE CASCADE,
    remote_instance_id UUID NOT NULL REFERENCES instances (id) ON DELETE CASCADE,
    -- Tier per §7.17.3: full | metadata | discovery | relay. A property of the
    -- SIGNED RELATIONSHIP, negotiated and recorded -- never a per-request
    -- decision, so a downgrade takes effect once and is auditable.
    tier TEXT NOT NULL CHECK (tier IN ('full','metadata','discovery','relay')),
    -- The signed handshake that established the tier. Without this a third
    -- instance can re-classify a peer relationship it is not party to.
    agreement_signature TEXT NOT NULL,
    established_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at TIMESTAMPTZ,
    UNIQUE (local_instance_id, remote_instance_id)
);
```

**Verify:** a revoked peer (`revoked_at` set) returns from no query. A peer
relationship is revocable by either side, so `revoked_at` is on the row and not a
per-side column — the row is the agreement.

**Test:** `TestARevokedPeerIsInvisibleToEveryQueryIncludingDiscovery` — revocation
must not be bypassed by the read-only discovery path, which is the path a
downgrade is most likely to survive in.

---

## Step 3 — Reputation attestations

**The design decision, stated before the migration** (§7.17.2): an attestation
carries a *claim* and the *rows to re-derive it*, never a bare level.

```sql
CREATE TABLE reputation_attestations (
    id UUID PRIMARY KEY,
    -- Who is making the claim.
    attesting_instance_id UUID NOT NULL REFERENCES instances (id) ON DELETE CASCADE,
    subject_user_id UUID NOT NULL,
    -- The claim. Signed, so it is attributable and revocable by the attestor.
    claim JSONB NOT NULL,
    -- The events the claim was computed from. An attested level WITHOUT these is
    -- a second source of truth the receiver cannot audit, which §7.17.2 forbids.
    supporting_event_ids UUID[] NOT NULL DEFAULT '{}',
    -- Weight decays and requires re-signing, so a lapsed contributor does not
    -- hold standing forever.
    issued_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    signature TEXT NOT NULL,
    revoked_at TIMESTAMPTZ
);
```

**Verify:** an expired attestation is not honoured; a revoked one is not honoured;
an attestation with an empty `supporting_event_ids` is refused at the service
layer, not merely down-weighted.

**Tests, one per property:**
- `TestAnExpiredAttestationIsNotHonoured`
- `TestARevokedAttestationIsNotHonoured`
- `TestAnAttestationWithoutSupportingEventsIsRefused` — the hard rule 1 test
- `TestAttestationWeightIsPerReceiverPolicy` — two instances with different trust
  in the same attestor produce different weights from the same attestation

---

## Step 4 — Change propagation, quorum, and divergence

```sql
CREATE TABLE metadata_changes (
    id UUID PRIMARY KEY,
    -- The change is signed by the originating user AND the instance (§7.18.1).
    origin_instance_id UUID NOT NULL REFERENCES instances (id) ON DELETE CASCADE,
    origin_user_key TEXT NOT NULL,
    instance_signature TEXT NOT NULL,
    entity_type TEXT NOT NULL,
    entity_id UUID NOT NULL,
    -- Quorum state: pending | applied | rejected | forked. 'forked' is a
    -- TERMINAL and legitimate outcome, not a failure awaiting resolution.
    state TEXT NOT NULL DEFAULT 'pending',
    -- Divergence marker: set when two instances hold irreconcilable versions and
    -- both are kept.
    diverged_from UUID REFERENCES metadata_changes (id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

**Verify:** the resolution order in §7.18.1 is implemented in that order —
recency + trust weight, then community vote, then steward arbitration, then fork.
A later pass must not let a cheaper step pre-empt a dearer one; the test is
`TestConflictResolutionFollowsTheSpecifiedOrder`.

**Test:** `TestAForkKeepsBothVersionsAndMarksThem` — and, critically,
`TestLastWriteNeverWinsSilently` — a conflict resolved by recency alone with no
divergence marker is the failure this ordering exists to prevent.

---

## Step 5 — Content plane: replicas, manifests, preservation alerts

```sql
CREATE TABLE content_replicas (
    id UUID PRIMARY KEY,
    scene_id UUID NOT NULL REFERENCES scenes (id) ON DELETE CASCADE,
    hosting_instance_id UUID NOT NULL REFERENCES instances (id) ON DELETE CASCADE,
    -- Two hashes, not one. The manifest hash covers the file list; the content
    -- hash covers the bytes. A single hash cannot distinguish "the file changed"
    -- from "the set of files changed", and repair needs to tell them apart.
    manifest_hash TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    -- Verification is by random challenge (§7.18.2), so the last check is
    -- recorded and the next one is derived from it rather than stored.
    last_verified_at TIMESTAMPTZ,
    verified_ok BOOLEAN NOT NULL DEFAULT TRUE,
    UNIQUE (scene_id, hosting_instance_id)
);

-- Default is 3 replicas per §7.3, as INSTANCE POLICY not a constant: §7.18.4
-- makes the threshold an overridable default in both directions.
CREATE TABLE preservation_policies (
    instance_id UUID PRIMARY KEY REFERENCES instances (id) ON DELETE CASCADE,
    min_replicas INTEGER NOT NULL DEFAULT 3 CHECK (min_replicas >= 1),
    max_replicas INTEGER,
    storage_budget_bytes BIGINT,
    bandwidth_budget_bytes BIGINT,
    retention_days INTEGER,  -- NULL is indefinite, which is the default
    -- Per-entity overrides ALWAYS take precedence (§4.1).
    overrides JSONB NOT NULL DEFAULT '{}'::jsonb
);
```

**Verify:** `min_replicas = 3` by default and overridable below it — §7.18.4 says
the threshold is a starting policy, not a floor, so a CHECK of `>= 3` anywhere is
a bug.

**Tests:**
- `TestReplicasBelowTheMinimumRaiseAPreservationAlert`
- `TestTheDefaultMinimumIsThreeAndIsOverridableInBothDirections` — a mutation
  raising the CHECK to `>= 3` must fail this
- `TestAPerSceneOverrideBeatsTheInstanceDefault` — §4.1's precedence rule
- `TestManifestAndContentHashesAreBothRecordedAndDistinct` — a single-column
  implementation must fail this

---

## Step 6 — Storage allocation and the allocation log

```sql
CREATE TABLE storage_allocations (
    id UUID PRIMARY KEY,
    instance_id UUID NOT NULL REFERENCES instances (id) ON DELETE CASCADE,
    scene_id UUID NOT NULL REFERENCES scenes (id) ON DELETE CASCADE,
    -- WHY it was placed, not just what. §7.18.3 makes the log the operator's
    -- audit surface; a log of placements without scores cannot be tuned, and
    -- tuning is the point of keeping it.
    score NUMERIC NOT NULL,
    score_breakdown JSONB NOT NULL,
    reason TEXT NOT NULL,
    allocated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

**Verify:** scoring inputs per §7.18.3 — instance taste vector, vanguard
(weighted highest), high-trust (medium), general (low), peer demand, preservation
urgency, and completion score. **More complete metadata is preferred**, and that
inversion is the one worth a test: `TestTheAllocationPrefersMoreCompleteMetadata
AmongEquallyEnjoyableContent`.

**Tests:**
- `TestVanguardSignalsOutweighGeneralUserSignals` — the weighting, not just the
  presence, of the inputs
- `TestEndangeredContentIsPromotedToTheTopOfTheQueue` — §7.18.2's alert
- `TestUserAllocationPreferencesAreAnInputNotAnOverride` — §7.18.3

---

## Step 7 — Onion-routed metadata sync

The transport. **No new product surface** (§7.19): this is the metadata plane's
sync, and it carries records, collage hashes, attestations, and profiles only.

**Verify by test, not by inspection** — the property that matters is what the
wire carries, so assert the payload:
- `TestTheSyncPayloadNeverContainsContentBytes` — the payload is records and
  hashes; a content path appearing here is a hard-rule-4 violation
- `TestAnAirGappedInstanceCanSyncViaExportImportBundles` — §7.17.4 makes
  air-gapped a supported topology, not a degraded one
- `TestAFullContentHashIsCarriedButNoContentPathIs` — the distinction between
  carrying a hash and carrying bytes

---

## Step 7b — Cross-instance discovery (carried from the prior plan)

Kept from `feature-04-federation-preservation.md`, which this plan supersedes.
Discovery is where the mesh is *felt*, so it is not the last item: the taste
vector, the peer tiers, and the gravity slider all exist to serve it, and a
discovery implementation that arrives before the vectors have been compared for
similarity has nothing to rank by.

### Cross-instance discovery — carried verbatim from the prior Phase 4 plan

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

## Step 8 — Personal nodes and anonymity levels

§7.18.5. A personal node is the same binary with a different role set, so this is
mostly configuration plus one table for the anonymity choice.

**Test:** `TestTheDefaultForANewUserIsPseudonymousWithNoContentSharing` — the
default is a safety property, and a default that drifts to "open" is invisible
without a test.

---

## Definition of done for Phase 4

- [ ] All 8 migrations apply in sequence from empty, and `sqlc generate` is
      idempotent afterwards
- [ ] `go build ./...` and `go vet ./...` clean; `go vet -tags=integration` clean
- [ ] Unit suite green, integration suite green
- [ ] `TestTheSyncPayloadNeverContainsContentBytes` and
      `TestTheAllocationPrefersMoreCompleteMetadata` pass — the two that encode
      decisions rather than mechanics
- [ ] No DHT/BitTorrent/eDonkey/Kad/IPFS dependency in `go.mod` (§7.19)
- [ ] Every `§` reference in the code comments resolves to a heading in `SPEC.md`
      (the citation audit, re-runnable)

## Explicitly not in this plan

- **A public swarm protocol** — rejected, §7.19, with reasons
- **Weighted influence on instance gravity** — rejected, §7.20
- **The annual awards and mobile app** — Phase 5, and they depend on mesh data
  this plan does not yet produce
- **Multi-user verification of edits** — deliberately out of scope for Phase 2 and
  not picked up here; it changes how edits are accepted, which is the most
  safety-critical code in the repository
