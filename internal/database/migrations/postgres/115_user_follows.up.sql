-- User follows, and the visibility of the activity they produce (discovery SPEC section 10,
-- aggregator phase 7 slice 7).
--
-- WHAT THIS IS: who follows whom, and -- per follow -- how much of the FOLLOWED user's activity
-- the follower may see. The activity feed (section 10) is assembled from the rows this table
-- makes visible: fingerprint submissions, list additions and reviews by people you follow.
--
-- WHY THE VISIBILITY IS ON THE FOLLOW ROW AND NOT ON `users`: it is what the draft specifies
-- (NexusXXX section 10 lists the enum on `user_follows`), and a per-follow grant is strictly
-- more expressive than a per-user one -- a user can publish to one follower and not another.
-- The cost is that "public" cannot be read off a user row, so its effect is defined below.
--
-- THE DEFAULT IS PRIVATE AND THAT IS THE POINT. The safer default is the one that cannot leak:
-- following someone is a statement about who the follower watches, NOT a grant of access to the
-- followed user's activity. A new row grants nothing, so a feature that forgets to set the
-- column fails CLOSED -- nobody sees the activity -- rather than open. This is the same rule as
-- migration 82's `reviews.verified` defaulting FALSE: the value that means "something checked
-- this" is never the value you get for free.
--
-- THE THREE LEVELS, operationally, because a name is not a semantics:
--
--   private    (DEFAULT) the followed user's activity is visible to nobody but themselves.
--   followers  visible to THIS follower. Not to their other followers: the grant is per row.
--   public     visible to this follower AND to every reader of the instance. The broadcast
--              exists because the draft's `public` is a level about the ACTIVITY, not about the
--              relationship, and a per-follow row has no other way to express "anyone".
--
-- WHO MAY CHANGE IT: the FOLLOWED user, and only the followed user. A follower setting their own
-- access to someone else's activity would make the column a claim rather than a permission --
-- every follower could grant themselves `public` and the default would be decoration. The
-- service enforces that the current user is the followed side; the schema cannot, because the
-- actor is not a column here.
--
-- NOT MODELLED: a follow REQUEST. Following is unilateral and immediate, which is what the
-- draft describes; `activity_visibility` covers the privacy question that a request flow would
-- otherwise be asked to solve, and adding requests later does not need this table to change.

CREATE TABLE "user_follows" (
    "follower_id" UUID NOT NULL REFERENCES "users" ("id") ON DELETE CASCADE,

    -- ON DELETE CASCADE on both sides, and for the same reason migration 82 gives for reviews:
    -- a GDPR erasure of a user must remove the rows that name them, in both directions. A
    -- follow row outliving its follower is a subscription held by nobody; outliving the
    -- followed user it is an edge pointing at a person who asked to be forgotten.
    "followed_id" UUID NOT NULL REFERENCES "users" ("id") ON DELETE CASCADE,

    "created_at" TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- NOT an enum type and NOT a boolean: 'private' | 'followers' | 'public' is a closed set
    -- that may gain a level (the draft mentions "lists only"), and a CHECK makes the states
    -- visible in the schema while a new value stays a one-line migration.
    "activity_visibility" VARCHAR(20) NOT NULL DEFAULT 'private'
        CHECK ("activity_visibility" IN ('private', 'followers', 'public')),

    -- ONE ROW PER (follower, followed). The service upserts, so a double-click is idempotent
    -- rather than two rows whose activity the reader would see twice.
    PRIMARY KEY ("follower_id", "followed_id"),

    -- Following yourself is not a relationship, and it would put your own activity in your
    -- feed twice over (as an actor you follow and as yourself). A constraint and not only a
    -- service check, for migration 82's reason: the service check is a race.
    CONSTRAINT "user_follows_not_self" CHECK ("follower_id" <> "followed_id")
);

-- "Who follows me", which is the read the visibility editor and the follower count need.
CREATE INDEX "user_follows_followed_idx" ON "user_follows" ("followed_id", "activity_visibility");

-- The two indexes the ACTIVITY FEED needs, and they are added here because the feed is what
-- this migration makes possible. Each feed arm is `... WHERE actor = $user ORDER BY created_at
-- DESC LIMIT n`, so the (actor, created_at) pair is the whole of it.
--
-- `scene_fingerprints` has (user_id) and (created_at) separately (migrations 18 and 34), but
-- neither serves "this user's most recent": the planner picks one and sorts the rest.
CREATE INDEX "scene_fingerprints_user_created_at_idx"
    ON "scene_fingerprints" ("user_id", "created_at" DESC);

-- `list_items` has (entity_type, created_at DESC) from migration 114, which is the
-- "what was added to lists lately" read. The feed's arm is per LIST OWNER and reaches list_items
-- through `lists`, so it needs (list_id, created_at DESC).
CREATE INDEX "list_items_list_created_at_idx" ON "list_items" ("list_id", "created_at" DESC);
