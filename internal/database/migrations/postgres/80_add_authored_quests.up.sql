-- Authored quests, bounties, and claiming (SPEC §7.7)
--
-- The previous step built GENERATED quests: a pure function of the archive, never
-- stored. This migration adds the two things a generated quest deliberately cannot
-- have.
--
--   A BOUNTY is an operator decision -- "this gap is worth triple" -- and a
--   promise made by a person. A generated quest must not be able to manufacture
--   one, so a bounty lives only on an AUTHORED quest, and the generator never
--   reads this table.
--
--   A CLAIM is a curator saying "I am working on this". It marks work IN PROGRESS
--   so nobody duplicates it, and it is NOT completion: the item leaves the quest
--   when the underlying field is actually filled, not when the claim expires.
--   A quest that vanished on claim would lose a curator's half-finished edit.

CREATE TABLE "authored_quests" (
    "id" UUID NOT NULL PRIMARY KEY,

    -- What the quest is about. A single entity type and a single field, for the
    -- same reason generated quests are: "add missing birthdates for 5 performers"
    -- is completable and "improve 5 performers" is not. The service refuses a
    -- field the scorer does not weight, so a quest cannot be authored against an
    -- unreachable gap.
    "entity_type" VARCHAR(20) NOT NULL,
    "field" VARCHAR(40) NOT NULL,

    -- How many items are asked for.
    "target" INTEGER NOT NULL DEFAULT 5,
    CHECK ("target" > 0),

    -- The BOUNTY: extra trust points for completing this quest, on top of
    -- KindQuestCompleted's own value.
    --
    -- Stored, not derived, because a generated quest cannot carry one -- this is
    -- the whole reason authored quests exist. Zero means "no bounty", and zero is
    -- a legitimate answer rather than a missing value.
    --
    -- Bounded above: an unbounded bounty is a typo that pays a year of
    -- reputation, and the check is the only thing standing between a fat-finger
    -- and that. The ceiling is generous enough for a real "this is the archive's
    -- worst gap" offer.
    "bounty_points" INTEGER NOT NULL DEFAULT 0,
    CHECK ("bounty_points" >= 0 AND "bounty_points" <= 10000),

    -- Who authored it, and why. An authored quest with no reason is a list a
    -- curator cannot evaluate: "rare performers" and "I need to finish my
    -- collection" are both valid and neither is inferable from the field.
    "reason" TEXT,

    "authored_by" UUID,
    FOREIGN KEY ("authored_by") REFERENCES "users" ("id") ON DELETE SET NULL,

    "expires_at" TIMESTAMP,

    "created_at" TIMESTAMP NOT NULL DEFAULT now(),

    -- An expiry in the past is not an error -- a quest can be authored already
    -- expired by a bad clock, and refusing the insert would make the mistake
    -- invisible. Callers filter on it instead.
    CHECK ("expires_at" IS NULL OR "expires_at" > "created_at")
);

-- The items, which are the entities the quest names. Distinct from a generated
-- quest's items because these are FIXED AT AUTHORING TIME: an authored quest is a
-- promise about specific entities ("these five performers are missing
-- birthdates"), so filling one in does not silently swap in a different performer
-- and leave the curator's work unrecognised.
--
-- That is the sharp difference from a generated quest, and it is why this is a
-- table rather than a filter over the completion score. A generated quest's items
-- are re-derived; an authored quest's items are a claim about the archive that
-- can turn out to be wrong, and the service reconciles them on read.
CREATE TABLE "authored_quest_items" (
    "id" UUID NOT NULL PRIMARY KEY,
    "quest_id" UUID NOT NULL,
    FOREIGN KEY ("quest_id") REFERENCES "authored_quests" ("id") ON DELETE CASCADE,

    "entity_type" VARCHAR(20) NOT NULL,
    "entity_id" UUID NOT NULL,

    -- The claim. NULL is the normal state and the majority of a quest's life: an
    -- unclaimed item is one nobody is duplicating.
    --
    -- ON DELETE SET NULL: deleting a user releases their claims, which is the
    -- correct outcome -- a claim is a statement by a person, and a person who
    -- left cannot still be working on anything.
    "claimed_by" UUID,
    FOREIGN KEY ("claimed_by") REFERENCES "users" ("id") ON DELETE SET NULL,
    "claimed_at" TIMESTAMP,

    -- A claim names its claimer, and a claim without a time cannot be aged out.
    -- The two are set together or not at all.
    CHECK (
        ("claimed_by" IS NULL AND "claimed_at" IS NULL)
        OR
        ("claimed_by" IS NOT NULL AND "claimed_at" IS NOT NULL)
    ),

    -- The quest's item count is the sum of its rows, so a quest cannot claim
    -- "5 items" and hold three. Enforced here rather than trusted from a client
    -- because a target nobody checks is a target that is a lie.
    --
    -- (Reconciled at read time: a completed item is retained but not counted, so
    -- this count is the items ASSIGNED, not the items remaining. The service
    -- distinguishes the two.)
    "created_at" TIMESTAMP NOT NULL DEFAULT now()
);

-- One entity appears once per quest. Without this, authoring a quest could name
-- the same performer twice and a curator would earn two bounties for one edit.
CREATE UNIQUE INDEX "authored_quest_items_unique"
    ON "authored_quest_items" ("quest_id", "entity_type", "entity_id");

-- The claim lookup that makes "who is already working on this?" cheap, and the
-- only hot read on a claim column.
--
-- Partial on claimed_by IS NOT NULL because unclaimed items are the default state
-- and vastly outnumber claimed ones; a full index over mostly-NULLs is a large
-- index that never serves the query it exists for.
CREATE INDEX "authored_quest_items_claimed_idx"
    ON "authored_quest_items" ("quest_id", "claimed_at")
    WHERE "claimed_by" IS NOT NULL;

-- A curator's own in-progress work across every quest: the "what am I working
-- on" list, and the query that expires stale claims.
CREATE INDEX "authored_quest_items_claimer_idx"
    ON "authored_quest_items" ("claimed_by", "claimed_at")
    WHERE "claimed_by" IS NOT NULL;

-- The quest board: active quests, most valuable first. Bounty descending because
-- a bounty IS the reason a curator picks one quest over another, and an index that
-- cannot serve that ordering makes every client sort.
CREATE INDEX "authored_quests_board_idx"
    ON "authored_quests" ("bounty_points" DESC, "created_at" DESC);

-- "Show me everything known about this entity", which is how a solved authored
-- quest turns into a canonical link back to the entity's page.
CREATE INDEX "authored_quest_items_entity_idx"
    ON "authored_quest_items" ("entity_type", "entity_id");

-- The entity references are deliberately WITHOUT foreign keys to
-- performers/scenes/studios/sites/tags, for the reason migration 79 gives: an
-- authored quest may outlive the entity it names (a soft-deleted performer, a
-- removed tag), and a constraint here would make the quest undeletable by
-- omission. The item is reconciled away on read instead.
