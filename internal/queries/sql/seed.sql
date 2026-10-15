-- Seed cohorts and their campaigns (plan phase 11 slice 11d).
--
-- The queries here are deliberately plain: a cohort is a list of people, a campaign is one row naming
-- its cohort and the head start it pays, and the only interesting thing about any of it is what
-- happens when a campaign RUNS (see the service). No counter column is read or written anywhere.

-- name: CreateSeedCohort :one
-- A cohort is created by name. The UNIQUE constraint is the error a caller gets for a name already in
-- use, so there is no check-then-insert here.
INSERT INTO seed_cohorts (name, description)
VALUES (sqlc.arg(name), sqlc.narg(description))
RETURNING *;

-- name: FindSeedCohort :one
SELECT * FROM seed_cohorts WHERE id = sqlc.arg(id);

-- name: ListSeedCohorts :many
-- Newest first: an operator looking for the cohort they just made finds it at the top.
SELECT * FROM seed_cohorts ORDER BY created_at DESC, name;

-- name: AddSeedCohortMember :exec
-- Adding somebody who is already in the cohort is a NO-OP rather than an error: the pair is the fact,
-- and "make sure this person is in the cohort" is the operation an operator actually wants. The
-- reported count of members added comes from the difference either side of the call.
INSERT INTO seed_cohort_members (cohort_id, user_id)
VALUES (sqlc.arg(cohort_id), sqlc.arg(user_id))
ON CONFLICT DO NOTHING;

-- name: RemoveSeedCohortMember :execrows
-- :execrows because "removed" and "was never in it" are different answers, and the second usually means
-- the operator is looking at the wrong cohort.
DELETE FROM seed_cohort_members
WHERE cohort_id = sqlc.arg(cohort_id) AND user_id = sqlc.arg(user_id);

-- name: ListSeedCohortMemberIDs :many
-- THE RUN'S MEMBER SET. Membership is read at RUN TIME and not cached anywhere, which is what makes
-- "a member who left the cohort before the campaign ran is not paid" true by construction rather than
-- by a check.
SELECT user_id FROM seed_cohort_members WHERE cohort_id = sqlc.arg(cohort_id) ORDER BY added_at, user_id;

-- name: ListSeedCohortMembers :many
-- The same set with names, for the operator's screen.
SELECT m.user_id, u.name, m.added_at
FROM seed_cohort_members m
JOIN users u ON u.id = m.user_id
WHERE m.cohort_id = sqlc.arg(cohort_id)
ORDER BY m.added_at, u.name;

-- name: CountSeedCohortMembers :one
SELECT COUNT(*)::int AS count FROM seed_cohort_members WHERE cohort_id = sqlc.arg(cohort_id);

-- name: CreateSeedCampaign :one
INSERT INTO seed_campaigns (cohort_id, name, points, created_by)
VALUES (sqlc.arg(cohort_id), sqlc.arg(name), sqlc.arg(points), sqlc.narg(created_by))
RETURNING *;

-- name: FindSeedCampaign :one
SELECT * FROM seed_campaigns WHERE id = sqlc.arg(id);

-- name: ListSeedCampaigns :many
SELECT * FROM seed_campaigns ORDER BY created_at DESC, name;

-- name: MarkSeedCampaignRun :one
-- Records that the campaign was APPLIED, and by whom. Deliberately a separate write from the paying of
-- it: the payment is idempotent (the event log decides), and this column is only what an operator reads
-- to see whether anybody has run it yet.
UPDATE seed_campaigns
SET last_run_at = NOW(), last_run_by = sqlc.narg(last_run_by)
WHERE id = sqlc.arg(id)
RETURNING *;
