-- Seasonal events (SPEC §20.2 gamification, plan phase 6 slice 6g).
--
-- An event is a NAMED WINDOW with a THRESHOLD: "Winter Sprint, the first two weeks of
-- December, ten contributions". This table declares them and nothing else -- there is no
-- state column, no progress column, and no sweep, because everything a member sees about an
-- event is DERIVED: the window from these two timestamps and the clock, and the progress
-- from the same trust_events every other gamification surface already reads.
--
-- WHY THAT MATTERS MORE THAN IT SOUNDS. A stored "event progress" would need a row per
-- member per event before anybody had done anything, a scheduler to expire them, and an
-- answer to what happens when the log and the row disagree. Derived, the state of an event
-- is a function of the clock and the log: "active" is what today falls between, "reached" is
-- whether the window holds enough contributions, and yesterday's answer is simply a
-- different answer.
--
-- Events are declared by an operator, which is why this is a table rather than a config
-- file: an event is content, it has a title and a description a member reads, and the CRUD
-- surface for writing one is deliberately NOT part of this slice -- the slice is the
-- measurement. A row here is currently written by a migration or a hand-run INSERT.
CREATE TABLE "seasonal_events" (
    "id" UUID NOT NULL PRIMARY KEY,
    "slug" VARCHAR(64) NOT NULL UNIQUE,
    "title" VARCHAR(255) NOT NULL,
    "description" TEXT NOT NULL DEFAULT '',
    "starts_at" TIMESTAMPTZ NOT NULL,
    "ends_at" TIMESTAMPTZ NOT NULL,
    -- The contributions a member has to record inside the window to have "reached" it.
    "threshold" INTEGER NOT NULL,
    "created_at" TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- An event that ends before it starts is not an event, and an event with nothing to
    -- reach is one every member has already reached. Both are refused here rather than
    -- defended against in every reader.
    CONSTRAINT "seasonal_events_window" CHECK ("ends_at" > "starts_at"),
    CONSTRAINT "seasonal_events_threshold" CHECK ("threshold" > 0)
);

-- The list a member reads is ordered by when events start, and the walk over the log is
-- per-window, so the index is on the window.
CREATE INDEX "seasonal_events_window_idx" ON "seasonal_events" ("starts_at", "ends_at");
