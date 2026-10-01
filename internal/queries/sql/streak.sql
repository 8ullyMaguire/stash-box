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
-- Cast to `timestamp`, explicitly, and NOT to timestamptz.
--
-- The cast was the bug: date_trunc returns midnight in the DATABASE's zone, and
-- ::timestamptz re-reads that wall-clock time as an instant, so the driver renders
-- it in the HOST's zone. The two disagree whenever the host's calendar date and the
-- database's differ -- at 00:05 local on 2026-10-01 against 22:05 UTC on 09-30, the
-- same day came back as YearDay 274 against 273 and ActiveToday went false for an
-- event created a minute earlier. Returning the truncated value uncast keeps it a
-- timestamp WITHOUT time zone, so there is no instant for the driver to convert
-- and every comparison stays in the database's frame of reference.
--
-- The cast target is load-bearing for a second reason: bare `date_trunc` is
-- inferred by sqlc as an INTERVAL, which is wrong -- truncating a timestamp does
-- not produce a duration -- and it silently changed this function's return type
-- from []time.Time to []pgtype.Interval. `timestamp` is the type the value
-- actually has.
SELECT DISTINCT date_trunc('day', created_at)::timestamp AS day
FROM trust_events
WHERE user_id = $1
ORDER BY day DESC;

-- name: CountUserActivityDays :one
-- How many distinct days a user has ever been active. Backs "active N days",
-- which is a lifetime total and does not decay, unlike the streak.
SELECT count(DISTINCT date_trunc('day', created_at))
FROM trust_events
WHERE user_id = $1;

-- name: DatabaseNow :one
-- The database's own clock, in the database's own timezone.
--
-- Two things are deliberate and both were bugs once.
--
-- AT TIME ZONE current_setting('TIMEZONE') keeps the value in the DATABASE's
-- zone instead of letting the driver convert it to the host's. Streak days come
-- back already truncated to the database's calendar, so `today` has to be in
-- that same frame or the two disagree at midnight -- an event created a minute
-- ago landing on "yesterday" and reporting a broken streak.
--
-- It is NOT time.Now() on the Go side. Events are stamped by PostgreSQL, so a
-- host whose clock is a minute fast would report "not active today" for an
-- event that landed two seconds ago, and the failure would look like a streak
-- bug rather than a clock bug.
-- Cast to timestamp, not timestamptz, for the same reason as ListUserActivityDays:
-- timestamptz would hand the driver an instant to re-render in the HOST's zone, undoing
-- the whole point of AT TIME ZONE.
SELECT (now() AT TIME ZONE current_setting('TIMEZONE'))::timestamp AS now;
