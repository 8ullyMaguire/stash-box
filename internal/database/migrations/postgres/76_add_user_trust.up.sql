-- Trust levels (SPEC §6).
--
-- Trust is NOT a role. models.RoleEnum is an authorization primitive (READ,
-- VOTE, EDIT, MODERATE, ADMIN) and is what the auth middleware checks. Trust is
-- a reputation score earned over time. Keeping them separate from this first
-- commit is deliberate: merging them is unrecoverable once data exists, because
-- "this user may not moderate" and "this user has not earned trust yet" are
-- different statements and conflating them makes the first unrepresentable.
--
-- Two tables, deliberately:
--
--   trust_events  the source of truth. Append-only, one row per thing that
--                 happened. Never updated or deleted, so a level can always be
--                 recomputed by replaying.
--   user_trust    a denormalised rollup, so the common question ("what level is
--                 this user?") does not require a scan of their history on every
--                 request. It is a cache with a defined rebuild path, not a
--                 second source of truth.
--
-- delta is signed rather than encoded in `kind`, so a reversed approval or a
-- revoked trust is an ordinary negative event and the audit trail stays
-- append-only in both directions.

CREATE TABLE "user_trust" (
    "user_id" UUID PRIMARY KEY REFERENCES "users" ("id") ON DELETE CASCADE,
    -- Cached level, derived from the totals below via the threshold table in
    -- internal/service/trust. Stored so listing users does not recompute, and
    -- recomputed by RebuildLevels whenever a threshold changes.
    "level" INTEGER NOT NULL DEFAULT 0,

    -- Running totals. These are the inputs to the threshold table, kept so a
    -- level can be re-derived without replaying trust_events.
    --
    -- Only the first two are populated in the first slice; the rest exist now so
    -- that later phases add a column rather than a second migration. Each is
    -- NOT NULL DEFAULT 0 so a partial row is never NULL-surprising.
    "approved_edits" INTEGER NOT NULL DEFAULT 0,
    "rejected_edits" INTEGER NOT NULL DEFAULT 0,
    "identification_solves" INTEGER NOT NULL DEFAULT 0,
    "quests_completed" INTEGER NOT NULL DEFAULT 0,
    "replicas_hosted" INTEGER NOT NULL DEFAULT 0,

    -- SPEC §6: high-trust users EXPLICITLY opt in to content viewing. This is
    -- the user's choice and is never set by the system. Eligibility (level >= 4)
    -- is derived at read time and deliberately NOT stored, because it changes
    -- when a threshold changes and a stored copy would silently go stale.
    "content_viewing_opt_in" BOOLEAN NOT NULL DEFAULT FALSE,

    "updated_at" TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE "trust_events" (
    "id" BIGSERIAL PRIMARY KEY,
    "user_id" UUID NOT NULL REFERENCES "users" ("id") ON DELETE CASCADE,

    -- e.g. 'edit_approved', 'edit_rejected', 'identification_solved',
    -- 'quest_completed', 'replica_hosted'. Free text rather than an enum: the
    -- set grows with each roadmap phase, and a new event kind should not require
    -- a migration. Unknown kinds are ignored by the rollup, not rejected.
    "kind" TEXT NOT NULL,

    -- What the event refers to, so a reversal can find the event it reverses and
    -- an operator can audit why a level changed. Both nullable: some events
    -- (a manual adjustment) are about the user and not an entity.
    "entity_type" TEXT,
    "entity_id" UUID,

    -- Signed. +1 for earning trust, -1 for losing it. A rejected edit and a
    -- reversed approval are both just -1, which keeps the audit trail
    -- append-only in both directions.
    "delta" INTEGER NOT NULL,

    "created_at" TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Reading a user's history, most recent first, is the only query the service
-- makes against this table, and rebuilding a level replays it in order.
CREATE INDEX "trust_events_user_idx" ON "trust_events" ("user_id", "created_at" DESC);

-- Guards the one invariant the rollup depends on: applying an event twice must
-- not double-count. The service records with ON CONFLICT DO NOTHING so a retried
-- or duplicated event is a no-op rather than a silent double increment.
--
-- NULLS NOT DISTINCT is load-bearing, not decoration. entity_type and entity_id
-- are nullable (a manual adjustment is about the user, not an entity), and in a
-- plain UNIQUE index Postgres treats NULL as distinct from NULL -- so every
-- entity-less event would be a *new* row on every retry, and the dedup would
-- silently do nothing for exactly the case most likely to be retried. This
-- requires PostgreSQL 15+; the project targets 18.
CREATE UNIQUE INDEX "trust_events_dedup_idx"
    ON "trust_events" ("user_id", "kind", "entity_type", "entity_id")
    NULLS NOT DISTINCT;
