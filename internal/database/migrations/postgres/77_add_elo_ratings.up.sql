-- Elo pairwise rankings (SPEC §9).
--
-- Replaces nothing and depends on nothing except 76, so the two feature slices
-- stay independently revertable.
--
-- Three tables, for the same reason trust_events/user_trust were split: the log
-- is the source of truth and the rating is a cache with a defined rebuild path.
--
--   elo_votes      append-only. One row per matchup a user has voted on. Never
--                  updated or deleted, so a rating can always be recomputed by
--                  replaying. This is also what makes a voter's own consistency
--                  (SPEC §6 "voting consistency") computable.
--   elo_ratings    the denormalised rating, per entity. Recomputed for the two
--                  participants of each vote.
--   taste_vectors  the per-user taste profile (SPEC §2). Computed from the
--                  user's own votes, and the input to everything downstream:
--                  recommendations (§4), peering similarity (§2), and the
--                  "because you liked" surfaces.
--
-- Glicko-2 rather than plain Elo, per SPEC §9. The deciding factor is not
-- accuracy but what it makes possible: Glicko tracks a per-entity RATING
-- DEVIATION, so the system knows which performers are genuinely well-observed
-- and which have three votes and a meaningless rating. Plain Elo cannot express
-- that, and a leaderboard that presents a 3-vote rating as confidently as a
-- 300-vote one is lying to the user. The deviation is stored rather than
-- recomputed because "how much do we trust this ranking" is needed on every
-- read.
--
-- `entity_type` + `entity_id` rather than a performer_id column: SPEC §9 ranks
-- performers, scenes, studios, sites, tags, lists AND instances. A per-entity
-- FK would need seven nullable columns and a CHECK with seven branches, and
-- could not express "instances" (a federation concept) at all until Phase 4
-- gives it a table. This is the one case where the loose typing is worth it,
-- and the FK is gone in exchange.

CREATE TABLE "elo_ratings" (
    "entity_type" VARCHAR(32) NOT NULL,
    "entity_id" UUID NOT NULL,
    -- Glicko rating on the standard scale, 1500 = average. Integer rather than
    -- double because the stored value is always a rating, never an intermediate;
    -- the arithmetic happens in float and is rounded once on write.
    "rating" INTEGER NOT NULL DEFAULT 1500,
    -- Glicko RD: the uncertainty around the rating. Falls as votes accumulate
    -- and rises when a player is idle, which is what makes the "needs more
    -- votes" badge and any confidence filter possible.
    --
    -- Capped by the min/max arguments of glicko.normalised, so a long-idle
    -- entity converges to a wide-but-bounded uncertainty rather than drifting to
    -- infinity. See the Glicko comments in internal/service/elo.
    "deviation" DOUBLE PRECISION NOT NULL DEFAULT 350,
    -- Glickman's sigma: how much this rating tends to swing. PERSISTENT state,
    -- not a per-update intermediate -- it is the player's demonstrated
    -- consistency, and the volatility update in Step 5 of the paper reads the
    -- previous value to decide how far sigma may move. Without this column the
    -- algorithm would restart every player at 0.06 on each vote, which silently
    -- discards the one signal that distinguishes a consistent performer from an
    -- erratic one.
    --
    -- 0.06 is Glickman's own starting value, distinct from the tau constraint
    -- (0.5) that bounds how fast sigma may change.
    "volatility" DOUBLE PRECISION NOT NULL DEFAULT 0.06,
    -- When the entity was last rated. Glicko is time-aware: RD grows during a
    -- gap, so this is an input, not just an audit column.
    "last_rated_at" TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY ("entity_type", "entity_id")
);

-- The default is created eagerly rather than lazily on first vote, because a
-- missing row and a default rating are different things: "nobody has voted" and
-- "everybody says 1500" give the same ranking but different confidence, and the
-- leaderboard needs to tell them apart.
INSERT INTO "elo_ratings" ("entity_type", "entity_id")
SELECT 'performer', id FROM "performers"
ON CONFLICT DO NOTHING;

CREATE TABLE "elo_votes" (
    "id" UUID PRIMARY KEY,
    "user_id" UUID NOT NULL REFERENCES "users" ("id") ON DELETE CASCADE,
    -- The two participants, in the order they were SHOWN. Order is not
    -- cosmetic: it is what makes the voter's position bias measurable, and
    -- position bias is the single most common way a pairwise vote gets gamed.
    "winner_id" UUID NOT NULL,
    "loser_id" UUID NOT NULL,

    "winner_type" VARCHAR(32) NOT NULL,
    "loser_type" VARCHAR(32) NOT NULL,

    -- Which side the voter picked. 0 = chose winner_id, 1 = chose loser_id.
    -- Stored rather than derivable, because the derivation depends on a
    -- comparison the database should not have to guess at, and an audit trail
    -- that depends on the current contents of two other columns is not an audit
    -- trail.
    "picked_side" SMALLINT NOT NULL,

    "created_at" TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- The two participants must differ. Without this a user could vote a
    -- performer against itself, which is a free +1 to the rating and a
    -- guaranteed way to reach the top of a leaderboard.
    CONSTRAINT "elo_votes_distinct_participants" CHECK ("winner_id" <> "loser_id"),

    -- picked_side is a two-valued choice, so anything else is a bug rather than
    -- a future extension point.
    CONSTRAINT "elo_votes_valid_side" CHECK ("picked_side" IN (0, 1)),

    -- A matchup is between two things of the same kind. Mixing a performer
    -- against a studio would make the rating meaningless -- there is no shared
    -- scale to update.
    CONSTRAINT "elo_votes_same_entity_type" CHECK ("winner_type" = "loser_type")
);

-- Serves the two access patterns: "this user's votes, newest first" (taste
-- vector, consistency) and "votes for this entity" (rebuild, delete-cascade
-- when a performer is merged away).
CREATE INDEX "elo_votes_user_idx" ON "elo_votes" ("user_id", "created_at" DESC);
CREATE INDEX "elo_votes_winner_idx" ON "elo_votes" ("winner_type", "winner_id");
CREATE INDEX "elo_votes_loser_idx" ON "elo_votes" ("loser_type", "loser_id");

-- Taste profile per user (SPEC §2).
--
-- One row per user, holding a sparse feature vector: a per-entity-kind preference
-- and the tag preference. Stored as JSONB rather than a wide table because the
-- feature set grows with each phase (Phase 3 adds site and era), and a wide
-- table would mean a migration per feature for what is a single additive blob.
--
-- A user with no votes has NO ROW here, and that is meaningful: "no taste data"
-- is different from "taste data that happens to be all zeros", and the
-- recommendation layer must be able to tell them apart. An empty vector row for
-- every new user would erase exactly that distinction.
CREATE TABLE "taste_vectors" (
    "user_id" UUID PRIMARY KEY REFERENCES "users" ("id") ON DELETE CASCADE,

    -- {"performer": 12.5, "scene": -3.0, ...} plus a "tag:<uuid>" entry per tag
    -- the user has shown a preference for. Null/absent means no opinion.
    "vector" JSONB NOT NULL DEFAULT '{}'::JSONB,

    -- How many votes this vector is built from. A vector from 3 votes is not
    -- evidence of a preference, and the recommender needs to know that -- hence
    -- a sample size stored alongside the vector rather than inferred from it.
    "vote_count" INTEGER NOT NULL DEFAULT 0,

    "updated_at" TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Rebuilding a vector scans one user's votes. Without this the taste profile
-- degrades to a full table scan of elo_votes on every recompute.
CREATE INDEX "taste_vectors_vote_count_idx" ON "taste_vectors" ("vote_count");
