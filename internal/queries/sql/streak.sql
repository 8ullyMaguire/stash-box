-- Activity days and streaks (SPEC §12, phase 2 step 8).
--
-- Both are DERIVED from trust_events rather than stored, for the same reason
-- badges are: a stored streak disagrees with the log that justifies it, and
-- nothing reports the disagreement. Worse here, a stored streak is a NUMBER that
-- decays on its own -- a user who stops contributing must lose it, and that
-- decay has to be written, scheduled, and audited. Derived, "current streak" is a
-- function of today and the log, and yesterday's answer is simply a different
-- answer.
--
-- The one thing that cannot be derived is a day that has passed with no
-- contribution: absence leaves no row. That is what the window below is for.

-- name: ListUserActivityDays :many
-- The DISTINCT calendar days on which a user recorded a trust event, newest
-- first.
--
-- DISTINCT on a day, not on an event. A curator who approves forty edits in one
-- afternoon has ONE active day and not forty, and counting events here would give
-- a forty-day streak to someone who showed up once -- the exact opposite of what a
-- streak is supposed to measure.
--
-- The date_trunc is to the SESSION'S timezone deliberately, not UTC's: "did you
-- contribute today" is a question about the user's day, and a user in UTC+2 who
-- contributed at 00:30 local has not been idle for a day just because UTC calls
-- that yesterday. Computing in UTC splits that user's midnight contributions
-- across two days and can break a streak they did not break.
--
-- Deltas are NOT counted. A -1 event is a real contribution day -- the person
-- showed up and their edit was rejected, which is participation, not absence --
-- and filtering on a positive count would zero out a day for a user whose
-- activity is all rejections and quietly end their streak.
SELECT DISTINCT date_trunc('day', created_at)::timestamptz AS day
FROM trust_events
WHERE user_id = $1
ORDER BY day DESC;

-- name: CountUserActivityDays :one
-- How many distinct days a user has ever been active. Backs "active N days",
-- which is a lifetime total and does not decay, unlike the streak.
SELECT count(DISTINCT date_trunc('day', created_at))
FROM trust_events
WHERE user_id = $1;
