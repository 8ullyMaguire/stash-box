-- Phase 14, slice 2 (spec §9, plan's slice 14.2): the appeal a DIFFERENT moderator hears.
--
-- WHY THIS TABLE EXISTS AT ALL. Slice 1 built the notice path: an operator can take a URL or a host out
-- of service (`content_source_block`), the submit path and the drain both consult it, and `mod_audit`
-- records who decided. What that path cannot express is a person disagreeing with it -- a takedown that
-- can only go one way is not a policy, it is a verdict. The appeal is the row that makes the decision
-- reversible by somebody the decision was not made by.
--
-- THE THREE DECISIONS THIS SCHEMA MAKES, each of them a rule that is in the shape rather than in a
-- procedure, because a rule in a procedure is a rule two code paths eventually answer differently:
--
-- 1. NO CASCADE FROM THE BLOCK, AND THE KEY DENORMALISED ONTO THE APPEAL. A release DELETES the block
--    row (`DeleteContentSourceBlock`; this table is the current policy and `mod_audit` is the history),
--    so a cascading reference would delete the appeal at exactly the moment it becomes the interesting
--    record: the one that says a decision was contested and what happened. `block_id` is therefore
--    nullable ON DELETE SET NULL, and `(kind, value)` is copied onto the appeal so the surviving row
--    still names WHAT was blocked. This is the invite-counter lesson of migration 138 one layer down:
--    a record that dies with its subject reports nothing about the one decision a person wants to read.
--
-- 2. HALF-STATES ARE UNREPRESENTABLE. `outcome`, `decided_by_id` and `decided_at` move together under two
--    CHECK constraints, so "decided with no outcome", "decided by nobody" and "granted at no time" cannot
--    be stored. The service writes all three in one UPDATE; the constraints are what keep a future writer
--    from writing two of them.
--
-- 3. WHAT A PERSON APPEALS AGAINST IS `(kind, value)`, NOT AN ID. The block list is an operator surface
--    (`contentSourceBlocks` is ADMIN-only), and a member who was refused knows the address they submitted,
--    not a row id. An appeal that can only be filed with an id nobody shows them is an appeal nobody
--    files -- so `kind`/`value` are the input, and they are also the column pair that survives the block.
CREATE TABLE "content_source_block_appeal" (
    "id" UUID NOT NULL PRIMARY KEY DEFAULT gen_random_uuid(),
    -- Nullable and non-cascading: see decision 1 above. A release is not the deletion of the appeal.
    "block_id" UUID REFERENCES "content_source_block" ("id") ON DELETE SET NULL,
    -- The block's key, DENORMALISED (decision 1). Same closed vocabulary as the block table, and the same
    -- normalisation applies: the value is what `ingest.NormalizeURL` / a lowercase host produce, so an
    -- appeal and the block it contests are compared as one string and not as two texts that look alike.
    "kind" VARCHAR(8) NOT NULL,
    "value" TEXT NOT NULL,
    -- Who appeals. Nullable ON DELETE SET NULL like every other actor reference in this schema: deleting a
    -- user must not delete the record of a decision they were part of.
    "filed_by_id" UUID REFERENCES "users" ("id") ON DELETE SET NULL,
    -- In the appellant's own words. NOT NULL, because an appeal with no statement is a request to review
    -- something with no reason given, and the moderator's queue is for reasons.
    "statement" TEXT NOT NULL,
    "filed_at" TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- WHO BLOCKED IT, recorded here at filing time rather than read back from the block row later.
    --
    -- This column exists because of the identity rule. "A different moderator decides" is a comparison
    -- against the operator who recorded the block, and a release DELETES that block row -- so a rule read
    -- from the block would be unenforceable exactly when it matters most: the appeal that is still pending
    -- after somebody released the block would then be decidable by the very operator whose decision is in
    -- question. The rule is therefore a property of the APPEAL row, which nothing deletes, and it is
    -- filled from the block at the only moment the block is guaranteed to exist (the insert below, in the
    -- same transaction that read it).
    "blocked_by_id" UUID REFERENCES "users" ("id") ON DELETE SET NULL,
    -- The decider. Deliberately NOT a foreign key to a role: the rule that the decider is a different
    -- person from the one who blocked is enforced by identity in the service, and a role check would pass
    -- for the same human holding a second role -- which is the failure this whole table exists to prevent.
    "decided_by_id" UUID REFERENCES "users" ("id") ON DELETE SET NULL,
    "outcome" VARCHAR(7),
    "decision_note" TEXT,
    "decided_at" TIMESTAMPTZ,
    CONSTRAINT "content_source_block_appeal_kind" CHECK ("kind" IN ('url', 'host')),
    CONSTRAINT "content_source_block_appeal_outcome"
        CHECK ("outcome" IS NULL OR "outcome" IN ('granted', 'denied')),
    -- Decision 2, one constraint per half. Both are total statements: they hold for undecided rows too.
    CONSTRAINT "content_source_block_appeal_decided_by"
        CHECK (("decided_by_id" IS NULL) = ("decided_at" IS NULL)),
    CONSTRAINT "content_source_block_appeal_decided_at"
        CHECK (("outcome" IS NULL) = ("decided_at" IS NULL))
);

COMMENT ON TABLE "content_source_block_appeal" IS
    'Contests against content_source_block rows, and their outcomes. Survives the block it names: an appeal outlives the policy it was about.';

-- One appeal per person per block, and NO constraint once the block is gone: there is nothing left to
-- appeal, and a unique index that spans released blocks would refuse a second appeal against a block that
-- was later re-created from a second notice -- which is a legitimate thing to want to contest.
CREATE UNIQUE INDEX "content_source_block_appeal_one_per_person"
    ON "content_source_block_appeal" ("block_id", "filed_by_id")
    WHERE "block_id" IS NOT NULL AND "filed_by_id" IS NOT NULL;

-- The moderator's queue: undecided rows, oldest first. Partial, because the queue never asks about
-- decided appeals and that is the only way this table is read in bulk.
CREATE INDEX "content_source_block_appeal_pending"
    ON "content_source_block_appeal" ("filed_at")
    WHERE "decided_at" IS NULL;

-- The audit actions. One value per DECISION, the convention migration 134's comment states: an operator
-- reading the trail wants "was this contested, and what happened" answered by a row, and the outcome is
-- in the row's `data`.
ALTER TYPE mod_audit_action ADD VALUE IF NOT EXISTS 'SOURCE_APPEAL';
ALTER TYPE mod_audit_action ADD VALUE IF NOT EXISTS 'SOURCE_APPEAL_DECISION';
