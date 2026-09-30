-- Federation peer registry and foreign identification evidence.
--
-- SPEC D2: "Identification board federates. A query broadcasts to peers with
-- matching taste vectors. Metadata plane only. Content never broadcasts."
-- Full reasoning in docs/spec/feature-04-identification-federation.md.
--
-- THE TWO TABLES ARE DELIBERATELY SEPARATE, and the separation is the design.
-- identification_candidates is a local suggestion the local community votes on.
-- identification_foreign_candidates is evidence with an origin. If they were
-- one table, a remote suggestion would sit in the local vote path and could
-- reach Resolve(), which writes a canonical link. The identification service's
-- own package doc says "a vote is EVIDENCE, not authority"; a peer's answer is
-- evidence squared, and evidence does not get a vote.

-- A peer this instance knows about.
--
-- OPERATOR-CONFIGURED, NOT DISCOVERED. Nothing extends this table by itself.
-- An open peer set is an SSRF and impersonation surface, and the instance_id is
-- what makes a reply attributable at all -- so who is allowed to be asked is an
-- operator decision, not a network effect.
CREATE TABLE "federation_peers" (
    "id" UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    "name" VARCHAR(255) NOT NULL,

    -- Base URL of the peer's API. NOT NULL: a peer row with no address is a row
    -- that can never be asked and should not be able to exist.
    "base_url" TEXT NOT NULL,

    -- The peer's self-declared instance identity, hex.
    --
    -- UNIQUE, and this is load-bearing rather than tidy. The same instance
    -- registered twice under two names would be asked twice, and its evidence
    -- would be counted twice -- a peer could buy double weight by being
    -- entered twice.
    "instance_id" VARCHAR(128) NOT NULL UNIQUE,

    -- Operator-set trust weight in (0, 1]. Multiplied into every piece of
    -- evidence this peer contributes, so a peer's word is worth strictly less
    -- than the local community's. Bounded above at 1 so a peer can never
    -- outrank a local vote, and bounded below away from 0 so a "disabled" peer
    -- is disabled by `enabled`, not by a weight that silently becomes 0 and
    -- makes the arithmetic ambiguous.
    "trust_weight" DOUBLE PRECISION NOT NULL DEFAULT 0.5
        CHECK ("trust_weight" > 0 AND "trust_weight" <= 1),

    "enabled" BOOLEAN NOT NULL DEFAULT TRUE,

    -- NULL means "never contacted", which is NOT the same as stale. Never
    -- contacted means we have no evidence this peer is alive; stale means we
    -- have positive evidence it has gone quiet. The service treats both as
    -- "do not ask", but the distinction is why the column is nullable.
    "last_seen_at" TIMESTAMPTZ,

    "created_at" TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    "updated_at" TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- A peer's answer to one of OUR queries.
--
-- "query_id" is deliberately a LOCAL id and the FK enforces it. A query id from
-- another instance is not a key in this database and must not be storable as
-- one: accepting a remote id here would let a peer address any local query,
-- including a private or abandoned one, by guessing a uuid.
CREATE TABLE "identification_foreign_candidates" (
    "id" UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    "query_id" UUID NOT NULL
        REFERENCES "identification_queries" ("id") ON DELETE CASCADE,

    "peer_id" UUID NOT NULL
        REFERENCES "federation_peers" ("id") ON DELETE CASCADE,

    "entity_type" VARCHAR(32) NOT NULL,

    -- The peer's id for the entity. NOT a local id and never resolvable as one:
    -- ids are per-instance, and treating a remote id as local would attach
    -- someone else's performer to a local scene.
    "remote_entity_id" VARCHAR(255) NOT NULL,

    -- Denormalised from the peer's answer so the operator surface can show what
    -- was proposed WITHOUT contacting the peer again, and so a peer that has
    -- since gone away still leaves a readable record of what it claimed.
    "remote_entity_name" VARCHAR(255) NOT NULL,

    -- How many of the peer's own users suggested this. Informational: it is the
    -- peer's number about the peer's community, and is deliberately not folded
    -- into any local tally (SPEC F2).
    "remote_vote_count" INTEGER NOT NULL DEFAULT 0,

    -- The peer's answer is only about THIS query, asked at THIS moment (SPEC
    -- F3). There is no entity_id column, so there is nowhere for a peer's
    -- opinion to attach to an entity and outlive the question.
    "fetched_at" TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Re-asking the same peer about the same query must not duplicate the row.
    UNIQUE ("query_id", "peer_id", "entity_type", "remote_entity_id")
);

CREATE INDEX "identification_foreign_candidates_query_idx"
    ON "identification_foreign_candidates" ("query_id");

-- The operator view: which peers are alive, and which have gone quiet. Sorted
-- by last_seen_at so a dashboard's "stale peers" list is a plain index scan
-- rather than a sort over the whole table.
CREATE INDEX "federation_peers_last_seen_idx"
    ON "federation_peers" ("last_seen_at");
