-- Decay state (SPEC §5.3, plan phase 6 slice 6b-2).
--
-- Two timestamps, because the level no longer follows the totals by itself: the
-- policy in internal/service/trust/decay.go needs to know WHEN a shortfall was
-- first seen and WHEN a demotion landed, and neither can be recovered from the
-- totals or from trust_events without replaying a user's whole history on every
-- request.
--
-- NULL rather than a sentinel instant, deliberately. The zero time is a real
-- instant in Go (year 1) and a real value in Postgres, and a column that uses it
-- as "not happening" makes a bug in the policy indistinguishable from a member
-- who was demoted in the year 1. NULL is what "there is no shortfall on record"
-- means, and both columns are nullable because a member who has never been
-- short of their level is the ORDINARY case, not an edge one.
--
-- Nothing here is derived at read time and cached: these are the inputs to the
-- decision, and the decision writes back a level. Rebuilding them from history is
-- possible (trust_events is append-only) but is not a path the service takes --
-- a member whose decay columns were lost is a member who gets a fresh grace
-- period, which is the safe direction to fail in.
ALTER TABLE "user_trust"
    ADD COLUMN "short_since" TIMESTAMPTZ,
    ADD COLUMN "demoted_at" TIMESTAMPTZ,
    -- The level the member was dropped FROM. The recovery window is a hold on THAT level and on
    -- nothing else: a member whose totals come back has not regained anything early, while a member
    -- who EARNS a standing they never had has earned it, and holding them at the level they were
    -- dropped to would be punishing them for doing better. Without this column the two cases are
    -- indistinguishable, and the test in decay_integration_test.go that promotes a demoted member
    -- past their old standing is the one that noticed.
    ADD COLUMN "demoted_from_level" INTEGER;

-- The sweep reads every rollup with something outstanding to do, which is the
-- minority of rows. Partial indexes so that work stays proportional to the
-- members actually in a grace period or a recovery window rather than to the
-- instance's size.
CREATE INDEX "user_trust_short_since_idx" ON "user_trust" ("short_since") WHERE "short_since" IS NOT NULL;
CREATE INDEX "user_trust_demoted_at_idx" ON "user_trust" ("demoted_at") WHERE "demoted_at" IS NOT NULL;
