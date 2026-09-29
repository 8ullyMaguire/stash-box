-- Matchup candidate selection (SPEC §9: "two performers side by side, who do you
-- prefer, one click, next matchup").

-- name: QueryMatchupCandidates :many
-- Draw a pool of performers eligible to appear in a matchup.
--
-- Two eligibility rules, both load-bearing:
--
--   1. At least one vote already. A performer nobody has rated has a deviation of
--      350 (the default), and Glicko's whole signal-to-noise ratio collapses
--      against that: a matchup between two virgin entities produces two small
--      rating moves and no information about anyone. Requiring one vote means
--      every matchup involves at least one entity the system has a real opinion
--      about, so the user's choice lands on something meaningful.
--
--   2. Deleted performers are excluded explicitly, on the `deleted` BOOLEAN the
--      other performer queries use. elo_ratings has no foreign key and no
--      soft-delete awareness, so a deleted performer would otherwise still be
--      drawn and a user could spend a vote on a ghost.
--
-- Ordered by the ELO rating rather than randomly at the SQL level. Two reasons,
-- and the second is the important one: Postgres random() is not reproducible,
-- which would make this method untestable, and a purely random draw would show a
-- user the same obscure performers forever. Returning the best-rated window
-- first and letting the caller SAMPLE within it gives a user mostly-known
-- candidates with enough variety to be a choice.
--
-- The join is on elo_ratings, not on a rating column: the Glicko rating is not
-- stored on the performer at all, and querying for one is the first thing to
-- check when a query like this fails to validate.
SELECT p.*
FROM performers p
JOIN elo_ratings r ON r.entity_id = p.id AND r.entity_type = sqlc.arg(entity_type)
WHERE p.deleted = FALSE
  AND p.id <> ALL(sqlc.arg(exclude_ids)::UUID[])
  AND NOT EXISTS (
    -- Already offered to this user. The whole EXISTS is skipped when the caller
    -- asks for repeats, which is how streaks and "taste maker" challenges in
    -- SPEC §9 get their re-shows.
    SELECT 1 FROM elo_votes v
    WHERE sqlc.arg(exclude_offered)::BOOLEAN IS NOT TRUE
      AND v.user_id = sqlc.arg(user_id)
      AND (
        (v.winner_type = sqlc.arg(entity_type) AND v.winner_id = p.id)
        OR (v.loser_type = sqlc.arg(entity_type) AND v.loser_id = p.id)
      )
  )
ORDER BY r.rating DESC, p.name
LIMIT sqlc.arg(limit_count);

-- name: CountVotesBetweenEntities :one
-- How many times this user has already been shown this exact pair.
--
-- Backs EloMatchup.timesOffered. Repetition is not a bug: SPEC §9 wants streaks
-- and a daily reason to return, and a user who has seen a pair before and votes
-- the same way twice is the strongest signal the system can get. The client
-- labels a repeat matchup so the user knows the system is not pretending the
-- pair is new.
SELECT count(*)
FROM elo_votes
WHERE user_id = sqlc.arg(user_id)
  AND (
    (winner_type = sqlc.arg(entity_type)::TEXT
       AND winner_id = sqlc.arg(left_id)::UUID
       AND loser_id = sqlc.arg(right_id)::UUID)
    OR (winner_type = sqlc.arg(entity_type)::TEXT
       AND winner_id = sqlc.arg(right_id)::UUID
       AND loser_id = sqlc.arg(left_id)::UUID)
  );
