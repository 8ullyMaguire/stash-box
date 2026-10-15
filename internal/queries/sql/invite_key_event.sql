-- The invite key's journal: what a member's invitations did (plan phase 11 slice 11c).
--
-- Every statement here is a COUNT or an append: nothing is ever updated, because an event that has
-- happened cannot be un-happened, and a counter that could be edited is a counter nobody can trust.

-- name: RecordInviteKeyEvent :exec
INSERT INTO invite_key_events (invite_key_id, generated_by, kind, user_id)
VALUES ($1, $2, $3, $4);

-- name: CountInviteKeyEventsForUser :many
-- The member's counters, grouped by kind. A kind with no rows is ABSENT rather than zero, which is
-- why the caller maps over the kinds it knows rather than printing what the database returned.
SELECT kind, count(*)::int AS count
FROM invite_key_events
WHERE generated_by = $1
GROUP BY kind;

-- name: CountInviteKeyEventsForKey :many
-- One key's counters. The key may be gone (invite_key_id is set to NULL on delete), in which case
-- this returns nothing while the member's own counters still hold the events.
SELECT kind, count(*)::int AS count
FROM invite_key_events
WHERE invite_key_id = $1
GROUP BY kind;
