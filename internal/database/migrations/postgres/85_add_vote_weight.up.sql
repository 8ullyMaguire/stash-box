-- Vanguard/trust-weighted Elo votes (SPEC §7.23 D1, plan D5).
--
-- The weight is SNAPSHOTTED on the row at cast time and never recomputed. A
-- vote's weight is a fact about WHEN it was cast: recomputing it at read time
-- means a user who gains (or loses) trust today silently re-weights every vote
-- they ever cast, changing historical rankings with no record that it happened.
-- An audit of a ranking must be able to answer "what was this user's weight
-- when they cast it", and only a stored value can answer that.
--
-- DOUBLE PRECISION, not SMALLINT: the weight is a multiplier that scales with
-- level bands and the contribution score, so it is continuous. Rounding it to
-- an integer would quantise the curve that makes the cap in
-- internal/service/elo/weight.go load-bearing.
--
-- NOT NULL DEFAULT 1.0 so every pre-existing row keeps its unweighted
-- semantics, and so an insert that forgets the column is a baseline vote
-- rather than a silent zero (which would make a vote count for nothing).
ALTER TABLE "elo_votes"
    ADD COLUMN "weight" DOUBLE PRECISION NOT NULL DEFAULT 1.0;

-- The audit query this column exists to serve is "which votes were cast by a
-- user who has since gained or lost trust".
--
-- The bare `elo_votes_user_idx (user_id)` that the plan proposed is NOT created
-- here: migration 77 already made that index as
-- `elo_votes_user_idx (user_id, created_at DESC)`, and a single-column index
-- on a composite's leading column is a redundant prefix that costs writes and
-- buys no query plan the composite does not already serve. Instead the existing
-- index is extended to carry the weight, so a per-user audit can read the
-- weights from the index without a heap fetch.
--
-- The column order keeps `user_id` first: every existing query filters on it, so
-- extending a live index's column list preserves all of them and adds the new
-- case rather than trading one for the other.
DROP INDEX "elo_votes_user_idx";

CREATE INDEX "elo_votes_user_idx" ON "elo_votes" ("user_id", "created_at" DESC, "weight");

-- A weight of zero would silently discard a vote: the row is stored, the
-- history is intact, and nothing in the rating maths ever uses it. A negative
-- weight is worse -- it inverts a vote, letting a user rank a matchup
-- backwards. Neither is reachable through VoterWeight (which clamps to a
-- documented floor), so the constraint is a backstop for direct SQL writes, not
-- a reachable state. The floor is 0.1, not 0, so "weighted almost nothing" stays
-- expressible and is not conflated with "did not count".
ALTER TABLE "elo_votes"
    ADD CONSTRAINT "elo_votes_weight_positive"
    CHECK ("weight" > 0);
