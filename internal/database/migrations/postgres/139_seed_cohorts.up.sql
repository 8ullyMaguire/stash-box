-- Seed cohorts and their campaigns (plan phase 11 slice 11d).
--
-- WHAT A COHORT IS AND IS NOT. A cohort is a NAMED SET OF MEMBERS -- a list an operator curates, with
-- nothing else attached to it. It grants nothing by existing, it is not a role (roles are permissions,
-- and a cohort is a group of people), and it is not a level (the level has exactly one implementation,
-- `LevelForTotals`, and a cohort that set one would be a second answer to "what level is this person").
--
-- WHY IT EXISTS: a seed campaign hands a curated group of early members a head start, and the head
-- start has to be paid through the ONE points path. So a campaign records a trust EVENT per member
-- carrying the campaign's own id, and trust_events' dedup index does the rest: running the same
-- campaign twice inserts nothing the second time. That is the whole design, and it is why this
-- migration adds no counter column anywhere.

CREATE TABLE "seed_cohorts" (
    "id" UUID NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY,

    -- UNIQUE because a cohort is referred to by name in conversation and in a campaign's own record;
    -- two cohorts with one name is an operator running the wrong one.
    "name" TEXT NOT NULL UNIQUE,
    "description" TEXT,
    "created_at" TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE "seed_cohort_members" (
    "cohort_id" UUID NOT NULL REFERENCES "seed_cohorts" ("id") ON DELETE CASCADE,
    "user_id" UUID NOT NULL REFERENCES "users" ("id") ON DELETE CASCADE,
    "added_at" TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- The pair is the identity: being in a cohort twice is the same fact as being in it once, so
    -- adding a member who is already there is a no-op rather than an error (the API's add is
    -- idempotent for that reason). ON DELETE CASCADE both ways: a cohort going away takes its
    -- membership with it, and a deleted account is not a member of anything.
    PRIMARY KEY ("cohort_id", "user_id")
);

-- The reverse question -- "which cohorts is this member in?" -- is the one a member's own page asks,
-- and the primary key above cannot answer it.
CREATE INDEX "seed_cohort_members_user_idx" ON "seed_cohort_members" ("user_id");

CREATE TABLE "seed_campaigns" (
    "id" UUID NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY,
    "cohort_id" UUID NOT NULL REFERENCES "seed_cohorts" ("id") ON DELETE CASCADE,
    "name" TEXT NOT NULL,

    -- The head start, per member, and it is applied ONCE per (member, campaign) by the dedup index on
    -- trust_events rather than by a flag here: the event log is what makes a re-run a no-op, and a
    -- "paid" boolean here would be a second answer that could disagree with it.
    --
    -- POSITIVE, and that is a decision rather than a constraint nobody thought about: a campaign is a
    -- head start. A negative delta would be a punishment campaign, which is a different feature with
    -- different questions (who consents to it, who is told, and why the ledger of it is not public),
    -- and it is not going to arrive by accident through this door.
    "points" INTEGER NOT NULL CHECK ("points" > 0),

    -- Who set it up. SET NULL rather than CASCADE: a departed operator's campaign is still a campaign
    -- that ran, and the events it paid are still in the log.
    "created_by" UUID REFERENCES "users" ("id") ON DELETE SET NULL,
    "created_at" TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- When it was last APPLIED, and by whom. NULL means never -- which is a different state from "ran
    -- and paid nobody", and an operator needs to tell those apart. Not a lock: the run is idempotent
    -- because of the event log, so a campaign run twice is safe and the second run reports that it
    -- paid nobody new.
    "last_run_at" TIMESTAMPTZ,
    "last_run_by" UUID REFERENCES "users" ("id") ON DELETE SET NULL
);

CREATE INDEX "seed_campaigns_cohort_idx" ON "seed_campaigns" ("cohort_id");
