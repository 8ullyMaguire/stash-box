-- Streak freezes (SPEC §5.3, plan phase 6 slice 6c).
--
-- A freeze is a DAY ON RECORD, not a counter and not an allowance that expires.
--
-- The streak schema refuses a stored streak because "a streak you can LOSE is a
-- lever, not a measurement". A freeze is the closest thing to that lever, so the
-- shape here is chosen to keep the objection true: spending a freeze INSERTS A
-- FACT -- "this day was covered" -- and every number in the feature stays DERIVED
-- from the log. Nothing decays, nothing has to be scheduled, and nothing can
-- disagree with the log, which is the same property the streak itself has.
--
-- The primary key is (user_id, day) rather than a surrogate id: covering one day
-- twice is not a thing that happened, and the dedup belongs in the schema rather
-- than in a check the next caller has to remember.
--
-- day is a DATE, not a timestamp: it names the calendar day that was covered. The
-- streak's own day boundaries are computed in the session's timezone by
-- ListUserActivityDays, and a DATE column cannot drift away from that; a
-- timestamp could.
CREATE TABLE "streak_freezes" (
    "user_id" UUID NOT NULL REFERENCES "users" ("id") ON DELETE CASCADE,
    "day" DATE NOT NULL,
    "created_at" TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY ("user_id", "day")
);

-- The read path loads one user's covered days newest-first, alongside their
-- activity days. The primary key already serves that prefix.
