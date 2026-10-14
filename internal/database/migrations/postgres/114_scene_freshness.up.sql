-- Scene freshness (SPEC section 7, phase 7 slice 4).
--
-- WHAT THIS IS: the score behind "Trending Now" and "Rising" -- sum over the engagement
-- signals of weight * decay(age), where
--
--     decay(age_hours) = 1 / (1 + age_hours / 48) ^ 1.5
--
-- WHY A MATERIALIZED VIEW: the score is a pure function of the signal rows and the current
-- time, so it is derived data and never stored state -- the same shape, and the same
-- REFRESH MATERIALIZED VIEW CONCURRENTLY, as scene_popularity_trending (migration 60).
-- A table with a cron writer would be a second copy of the signal rows that can drift from
-- them; a view recomputed from them cannot.
--
-- WHY THE 90-DAY WINDOW: decay is asymptotic, so an unbounded sum would scan the whole
-- signal history on every refresh to add numbers that cannot change an order. At 90 days
-- (2160 hours) a signal contributes 1 / (1 + 45) ^ 1.5 = 0.0033 of its weight -- under a
-- third of one percent, below the resolution of a ranking whose top rows differ by whole
-- signals. The window is what keeps the refresh a bounded scan of recent rows.
--
-- THE SIGNALS AND THEIR WEIGHTS (spec section 7):
--
--   | signal                      | weight            | source                              |
--   |-----------------------------|-------------------|-------------------------------------|
--   | fingerprint vote             | 3 * vote          | scene_fingerprints.vote            |
--   | added to a list             | 3                 | list_items, entity_type = 'scene'   |
--   | review written              | rating - 3        | reviews, entity_type = 'scene'      |
--
-- The fingerprint signal is `3 * vote`, which is the draft's "+3 for a submission, -3 for a
-- withdrawal" in one expression: `scene_fingerprints.vote` is `SMALLINT NOT NULL CHECK (vote = -1
-- OR vote = 1)` since migration 40, and a client withdraws a fingerprint by submitting it with
-- `vote: REMOVE`, which the upsert stores as -1 rather than deleting the row. So a withdrawal is
-- a NEGATIVE SIGNAL with a timestamp, not an absence -- and the score is a sum, so it can go
-- below zero. A scene whose only fingerprint was voted down therefore ranks below a scene nobody
-- has touched at all, which is the right shape for a feed: a disputed claim does not promote.
--
-- A withdrawal keeps the ORIGINAL submission's `created_at` (the upsert updates duration and vote
-- only), so withdrawing an old fingerprint undoes an old signal -- the -3 decays with the claim it
-- retracts. That is a consequence of the upsert, not a choice made here, and it is why a
-- withdrawal is not a way to bury a scene.
--
-- The review weight is the draft's `2 * (rating - 3) / 2`, which is `rating - 3`: a 5-star
-- review is worth 2, a 1-star review -2, and a review with NO rating contributes nothing at all
-- (`rating IS NULL` is a review of a studio's ethics or a performer's aliases, which is not a
-- 1-to-5 quantity -- averaging it in as a 3 would invent a signal).
--
-- ONE TERM FROM THE DRAFT IS ABSENT: `votes_up - votes_down`, because there is no scene vote
-- table on this instance. It is recorded here rather than silently missing (spec section 14).
--
-- TIME ZONES: `scene_fingerprints.created_at` is a `timestamp` (no zone) stamped by this
-- database, so attaching UTC is what makes it comparable to `now()`; `list_items` and
-- `reviews` are `timestamptz` and need nothing. A `timestamp` compared without this reads as
-- the session's zone, which is the +02:00 mistake that made the saved-search cursor two hours
-- ahead of the rows it compared against.
--
-- A negative age (a row stamped in the future, e.g. by a process on a host with a fast clock)
-- is clamped to zero: without the clamp such a row would be worth MORE than a signal submitted
-- now, which inverts the ranking for a data anomaly nobody chose.
CREATE MATERIALIZED VIEW scene_freshness AS
WITH signals AS (
    SELECT
        sf.scene_id,
        (sf.created_at AT TIME ZONE 'UTC') AS occurred_at,
        (3 * sf.vote)::double precision AS weight
    FROM scene_fingerprints sf

    UNION ALL

    SELECT
        li.entity_id,
        li.created_at,
        3::double precision
    FROM list_items li
    WHERE li.entity_type = 'scene'

    UNION ALL

    SELECT
        r.entity_id,
        r.created_at,
        (r.rating - 3)::double precision
    FROM reviews r
    WHERE r.entity_type = 'scene'
      AND r.rating IS NOT NULL
),
aged AS (
    SELECT
        scene_id,
        weight,
        GREATEST(EXTRACT(EPOCH FROM (now() - occurred_at)) / 3600.0, 0) AS age_hours
    FROM signals
    WHERE occurred_at > now() - interval '90 days'
)
SELECT
    scene_id,
    SUM(weight / power(1.0 + age_hours / 48.0, 1.5)) AS score,
    COALESCE(SUM(weight / power(1.0 + age_hours / 48.0, 1.5))
        FILTER (WHERE age_hours <= 24), 0) AS recent_score,
    COALESCE(SUM(weight / power(1.0 + age_hours / 48.0, 1.5))
        FILTER (WHERE age_hours > 24 AND age_hours <= 48), 0) AS earlier_score,
    now() AS computed_at
FROM aged
GROUP BY scene_id;

-- Required for REFRESH MATERIALIZED VIEW CONCURRENTLY: it decides which rows changed by
-- matching this key, and refuses to run without one. It also makes the LEFT JOIN in the
-- sort a unique-key lookup rather than a scan.
CREATE UNIQUE INDEX scene_freshness_scene_id ON scene_freshness (scene_id);

-- The sorts read this view ordered by score (descending) with a LIMIT.
CREATE INDEX scene_freshness_score ON scene_freshness (score DESC);

-- The refresh aggregates list_items by entity_type, and until now the only index on that
-- table was (list_id, position) -- so every refresh was a sequential scan of the table.
-- Same for the review aggregate, which is already covered by reviews_entity_idx
-- (entity_type, entity_id, created_at DESC) from migration 82.
CREATE INDEX list_items_entity_created_at ON list_items (entity_type, created_at DESC);
