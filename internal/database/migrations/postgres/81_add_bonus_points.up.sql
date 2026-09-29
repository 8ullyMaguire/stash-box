-- XP award path (SPEC §12), Phase 2 step 6
--
-- The event log already exists (migration 76) and two call sites already record
-- into it: an applied edit, and a solved identification. What does NOT exist is
-- the ability for one event to be worth more than its kind's standard value --
-- and a curation quest's BOUNTY is exactly that.
--
-- The conflict this migration resolves:
--
--   Points()  is  count x PointsPerKind[kind]
--   quests_completed  is a COUNT of quests
--
-- So a 500-point bounty on one quest cannot be recorded as delta=500: the rollup
-- would count 500 completed quests, and the curator would earn 500 x 20 = 10,000
-- points for finishing one. "How many quests did this person finish" and "how
-- much are those quests worth" are different questions, and one integer column
-- cannot answer both.
--
-- So bonus points are a separate COLUMN beside the counts, summed from the SAME
-- append-only event log. Not a parallel XP counter: it is derived from the same
-- events, by the same recompute, in the same transaction. Two columns written
-- together by one function, not two systems that can disagree.
--
-- Invariant, and the reason this is safe:
--     points = (sum of counts x PointsPerKind) + bonus_points
-- and BOTH halves come from trust_events. There is no third place a point can
-- come from, and no way for a rebuild to produce a different answer than an
-- incremental award.

ALTER TABLE "user_trust" ADD COLUMN "bonus_points" INTEGER NOT NULL DEFAULT 0;

-- Guard the column against the only shape that is actually dangerous: a negative
-- bonus, which would let a reversal or a bug take points away without any event
-- being able to explain it. The event log is signed and append-only, so a
-- negative BountyBonus event is legitimate for a reversal -- but the ROLLUP is a
-- running total, and a negative running total of bonus points is a score nobody
-- can reproduce from the log's positive contributions.
--
-- No upper bound: the ceiling that matters is the one in the authored quest
-- table (bounty_points <= 10000), and putting a second, different limit here is
-- how two limits drift apart.
ALTER TABLE "user_trust"
    ADD CONSTRAINT user_trust_bonus_points_nonnegative CHECK (bonus_points >= 0);

-- BountyBonus is a NEW kind, and it is a points-only kind: it moves bonus_points
-- and none of the counts. That asymmetry is the whole point, and it is why the
-- kind cannot be folded into quest_completed.
--
-- Recorded as a comment rather than a CHECK on trust_events.kind because the
-- column is deliberately free text (migration 76) so a new kind does not require
-- a migration -- and an unknown kind is recorded and ignored by the rollup, which
-- is the right behaviour for a kind a future version introduces.
--
-- The recompute query in internal/queries/sql/trust.sql gains a matching CASE
-- term in the same commit. The two must move together: a kind the query does not
-- sum is a kind whose points vanish on the next rebuild, and the rebuild is the
-- recovery path, so the loss would surface exactly when it is most needed.
