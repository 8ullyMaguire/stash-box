-- The T3 cooling period (aggregator phase 5, plan slice 5e; NexusXXX section 10).
--
-- T0-T2 are consented to immediately and T3 is not: the most extreme tier an instance offers is not
-- something a member should fall into within the same minute they read about it. The rule itself is
-- one comparison, and it lives in the ceiling query (`UserVisibleTier`) rather than in each of the
-- four read paths, so nothing can consult the ceiling without passing it.
--
-- ADDITIVE AND SILENT FOR EVERY EXISTING CONSENT: NULL means "not cooling", so a member whose array
-- already contains 3 keeps exactly the access they had. (The alternative -- backfilling now() --
-- would have removed access from existing members for a day, which is the opposite of what a
-- denylist-shaped rule is allowed to do.)
--
-- The column is stamped on GAIN and cleared on LOSS by SetVisibleTiers, never on every write: a
-- member who merely re-orders their array has not changed their consent, and a stamp-on-write
-- version would let anyone reset the clock by re-sending the same numbers in a different order.
ALTER TABLE "user_trust" ADD COLUMN "t3_consented_at" TIMESTAMPTZ;

COMMENT ON COLUMN "user_trust"."t3_consented_at" IS
    'When the member''s consent first included tier 3, or NULL if it never has. The gate uses a ceiling of 2 until T3CoolingHours have passed; NULL means no cooling (a consent that predates this column cannot be aged, and is not aged).';
