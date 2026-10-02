-- SPEC §7.24.3: bounty pricing audit.
--
-- "Records what a suggested price was, and whether the author overrode it. Feeds §7.24.3's
-- calibration and is the evidence that the generator never wrote a bounty."
--
-- The second clause is the reason this table exists, and it is an unusual requirement to put in
-- a schema comment: the generator must be UNABLE to write a bounty, and this table is the
-- evidence. So it records authorship of the price as carefully as authorship of the quest --
-- `source` is a closed set, and the row that says "generated" is distinguishable from the row
-- that says "an author overrode this".
--
-- One row per priced quest, not per edit to the price. A re-pricing is itself interesting, so it
-- is recorded, but as a separate row keyed to the quest rather than by mutating this one: a
-- mutable "current price" column is a second source of truth next to authored_quests.bounty_points,
-- which already holds the answer. This table is the HISTORY, and history that overwrites itself
-- is not history.

CREATE TABLE "bounty_pricing_audit" (
    "id" UUID NOT NULL PRIMARY KEY DEFAULT gen_random_uuid(),

    "quest_id" UUID NOT NULL,
    -- CASCADE: the audit's purpose is to answer "how was this quest priced", and that question
    -- has no meaning once the quest is gone. Keeping the rows would preserve a claim about a
    -- quest that cannot be fetched, read, or appealed.
    FOREIGN KEY ("quest_id") REFERENCES "authored_quests" ("id") ON DELETE CASCADE,

    -- What the formula proposed, which is never authoritative. It is kept alongside the final
    -- price precisely so the difference is visible: an author who overrides every suggestion is
    -- making a statement about the calibration, and that statement is only legible if the
    -- suggestion is preserved.
    "suggested_points" INTEGER NOT NULL
        CHECK ("suggested_points" >= 0 AND "suggested_points" <= 10000),

    -- What the quest actually carries. Equal to suggested_points when the author accepted the
    -- formula, which is the common case and the one worth measuring against.
    "final_points" INTEGER NOT NULL
        CHECK ("final_points" >= 0 AND "final_points" <= 10000),

    -- Whether the author changed it. A stored boolean rather than inferring from the two points
    -- columns, because "overrode" is a fact about what HAPPENED, and it is the fact calibration
    -- needs: an override from 40 to 45 and an override from 40 to 5 are both overrides and they
    -- are not the same signal.
    "was_overridden" BOOLEAN NOT NULL,

    -- A closed set, not a free-text or a boolean. The distinction the spec asks for is between
    -- the generator proposing and a human deciding, and there is a third state that both
    -- collapse: a curator authored the quest themselves and priced it, and no suggestion was
    -- ever computed. `author_declared` is that case. Representing it as suggested_points = final_points
    -- would report it as "accepted the formula", which is a false claim about a decision nobody
    -- took by consulting a formula.
    "price_source" VARCHAR(24) NOT NULL
        CHECK ("price_source" IN ('generated', 'author_declared', 'author_overrode')),

    -- Set for every state, because §7.24.3's other rule -- "the generator never reads
    -- authored_quests" -- is only checkable if every row says who decided. NULL here would make
    -- "was this price decided by a person" unanswerable for exactly the rows that matter.
    "priced_by" UUID NOT NULL REFERENCES "users" ("id") ON DELETE RESTRICT,

    "reason" TEXT,

    "priced_at" TIMESTAMP NOT NULL DEFAULT now(),

    -- The three states are self-consistent, and this is what stops a row from claiming a
    -- generator wrote a price. Without it, `price_source = 'generated'` with
    -- `was_overridden = false` and a different final value is representable, and that is
    -- precisely the row §7.24.3 says must be impossible:
    --
    --   - author_declared => NOT overridden, and the points are the author's own, so there is
    --     no suggested value to have agreed or disagreed with
    --   - author_overrode => overridden
    --   - generated        => NOT overridden, because a generated price cannot be overridden
    --     without a human, which is author_overrode
    --
    -- So `generated` implies accepted-as-suggested, which is what makes "the generator never
    -- wrote a bounty" a constraint rather than a convention.
    CHECK (
        ("price_source" = 'author_declared' AND NOT "was_overridden")
     OR ("price_source" = 'author_overrode'  AND "was_overridden")
     OR ("price_source" = 'generated'        AND NOT "was_overridden"
                                AND "suggested_points" = "final_points")
    )
);

-- Calibration reads "every price an author overrode, most-recent first" to see whether the
-- formula is systematically wrong in one direction. Leading with quest_id is deliberate: that is
-- the per-quest history read, which is the more common query.
CREATE INDEX "bounty_pricing_audit_quest_idx"
    ON "bounty_pricing_audit" ("quest_id", "priced_at");

-- ...and the global read: overrides by a given author, to spot one curator fighting the formula on
-- everything. Partial on overrides, because the overwhelmingly common case is "accepted" and an
-- index over it grows with every quest on the instance.
CREATE INDEX "bounty_pricing_audit_overridden_idx"
    ON "bounty_pricing_audit" ("priced_by", "priced_at")
    WHERE "was_overridden";