-- Follows and the activity feed (discovery SPEC section 10, aggregator phase 7 slice 7).
--
-- THE FEED IS CHRONOLOGICAL BY CONTRACT. The draft is explicit -- "no algorithmic reordering" --
-- so there is no score here, no decay, no diversity term and no permission for a later change to
-- add one: the order is `created_at DESC`, ties broken by kind and id so that two reads of the
-- same data return the same page. A feed that reorders itself is not a feed a reader can follow.
--
-- VISIBILITY IS EVALUATED IN SQL, IN ONE PLACE, because the alternative is three arms each
-- remembering to apply it and one of them forgetting. The `visible` CTE is the whole rule:
-- an actor's activity reaches this viewer if the actor granted THIS viewer 'followers' or
-- 'public', or if the actor granted 'public' to anyone (the broadcast -- see migration 115 for
-- why a per-follow column is where that value lives). A 'private' grant, which is the DEFAULT,
-- matches neither clause, so a feature that forgets to set the column shows nobody's activity
-- rather than everybody's.

-- name: UpsertFollow :execrows
-- Idempotent: ON CONFLICT DO NOTHING, so a double-clicked follow is one row rather than a
-- second one whose activity a reader would see twice. It does NOT touch the visibility, which is
-- the FOLLOWED user's grant: re-following must not reset someone else's choice.
INSERT INTO user_follows ("follower_id", "followed_id")
VALUES (sqlc.arg(follower_id), sqlc.arg(followed_id))
ON CONFLICT ("follower_id", "followed_id") DO NOTHING;

-- name: DeleteFollow :execrows
DELETE FROM user_follows
WHERE "follower_id" = sqlc.arg(follower_id) AND "followed_id" = sqlc.arg(followed_id);

-- name: SetFollowVisibility :execrows
-- The FOLLOWED user's grant to one follower. The service checks that the current user is the
-- followed side; this statement could not, because the actor is not a column of this table.
UPDATE user_follows
SET "activity_visibility" = sqlc.arg(activity_visibility)
WHERE "follower_id" = sqlc.arg(follower_id) AND "followed_id" = sqlc.arg(followed_id);

-- name: FindFollow :one
-- One follow row, for the follower's own view of it and for the visibility editor.
SELECT "follower_id", "followed_id", "activity_visibility", "created_at"
FROM user_follows
WHERE "follower_id" = sqlc.arg(follower_id) AND "followed_id" = sqlc.arg(followed_id);

-- name: FindFollowing :many
-- Who this user follows, newest first, with the visibility THEY were granted.
SELECT f."followed_id" AS user_id, u."name" AS user_name, f."activity_visibility", f."created_at"
FROM user_follows f
JOIN users u ON u."id" = f."followed_id"
WHERE f."follower_id" = sqlc.arg(user_id)
ORDER BY f."created_at" DESC, f."followed_id";

-- name: FindFollowers :many
-- Who follows this user, newest first, with the visibility each was granted. This is the read
-- the visibility editor is built on: the followed user needs to see the rows before changing
-- one.
SELECT f."follower_id" AS user_id, u."name" AS user_name, f."activity_visibility", f."created_at"
FROM user_follows f
JOIN users u ON u."id" = f."follower_id"
WHERE f."followed_id" = sqlc.arg(user_id)
ORDER BY f."created_at" DESC, f."follower_id";

-- THE FEED IS THREE QUERIES, ONE PER KIND OF ACTIVITY, MERGED IN GO.
--
-- A single UNION would be the tidier-looking choice and it is the one this file first had. sqlc
-- cannot resolve a CTE or a subquery alias built from a UNION of parenthesised arms with their own
-- ORDER BY / LIMIT -- "table alias f does not exist" -- and the shapes it does accept (an outer
-- ORDER BY over three unbounded arms) make the planner read every signal on the instance to return
-- twenty rows. Three bounded, indexed reads merged by timestamp is the same answer, each arm is
-- separately testable, and each arm's visibility rule is visible in the arm it belongs to.
--
-- EVERY ARM SHARES THREE RULES, and they are repeated rather than factored into a view because each
-- one is about THAT arm's tables:
--
--   * VISIBILITY. An actor's activity reaches this viewer if the actor granted THIS viewer
--     'followers' or 'public', or if the actor granted 'public' to anyone (the broadcast -- see
--     migration 115 for why a per-follow column is where that value lives). A 'private' grant,
--     which is the DEFAULT, matches neither clause: a feature that forgets to set the column shows
--     nobody's activity rather than everybody's.
--   * `before` IS NULLABLE, and an absent cursor means "from the top". It is a KEYSET cursor
--     (`created_at < before`), not an offset: a feed paged by OFFSET shows a reader the same row
--     twice when something is added between pages, which is exactly what happens on a feed.
--   * THE VIEWER'S OWN ACTIVITY IS EXCLUDED, in the service rather than here, because the three
--     arms return into one merge and doing it once is what makes it true of all of them.

-- name: FindFingerprintActivity :many
-- A fingerprint submission. `vote = 1` only: a WITHDRAWAL is a real signal but it is not the
-- activity "added this scene", and reporting it as one would put a scene in a feed on the strength
-- of someone saying they do NOT have it.
SELECT fp."created_at" AS created_at, fp."user_id" AS actor_id, u."name" AS actor_name,
       fp."scene_id" AS scene_id, NULL::uuid AS list_id, NULL::text AS list_name,
       NULL::int AS rating
FROM scene_fingerprints fp
JOIN users u ON u."id" = fp."user_id"
WHERE fp."vote" = 1
  AND (sqlc.narg(before)::timestamptz IS NULL OR fp."created_at" < sqlc.narg(before))
  AND (
      fp."user_id" = ANY(sqlc.arg(actor_ids)::uuid[])
      OR EXISTS (
          SELECT 1 FROM user_follows pub
          WHERE pub."followed_id" = fp."user_id" AND pub."activity_visibility" = 'public'
      )
  )
ORDER BY fp."created_at" DESC, fp."scene_id"
LIMIT sqlc.arg(row_limit)::int;

-- name: FindListActivity :many
-- A scene added to a list. THE LIST'S NAME IS REPORTED ONLY IF THE LIST IS PUBLISHED: the activity
-- is "someone filed this scene", which the visibility grant covers, while WHICH list they filed it
-- into is theirs until they publish it. A private list's name leaking through a follower's feed
-- would be the visibility column protecting one thing and this arm leaking another.
SELECT li."created_at" AS created_at, l."owner_id" AS actor_id, u."name" AS actor_name,
       li."entity_id" AS scene_id,
       l."id" AS list_id,
       l."name" AS list_name,
       -- Whether the list is published, so the ARM does not decide what to hide. Wrapping the
       -- two columns above in `CASE WHEN ... ELSE NULL END` reads well and lies to sqlc: the
       -- casts make the columns non-nullable, the rows contain NULLs anyway, and scanning one
       -- into a non-pointer field is a runtime error rather than a wrong value. The rule lives
       -- in the service, where a test can call it.
       (l."published_at" IS NOT NULL)::boolean AS list_published,
       NULL::int AS rating
FROM list_items li
JOIN lists l ON l."id" = li."list_id"
JOIN users u ON u."id" = l."owner_id"
WHERE li."entity_type" = 'scene'
  AND li."entity_id" IS NOT NULL
  AND (sqlc.narg(before)::timestamptz IS NULL OR li."created_at" < sqlc.narg(before))
  AND (
      l."owner_id" = ANY(sqlc.arg(actor_ids)::uuid[])
      OR EXISTS (
          SELECT 1 FROM user_follows pub
          WHERE pub."followed_id" = l."owner_id" AND pub."activity_visibility" = 'public'
      )
  )
ORDER BY li."created_at" DESC, li."entity_id"
LIMIT sqlc.arg(row_limit)::int;

-- name: FindReviewActivity :many
-- A review. PUBLISHED only, for the reason the taste service gives: a flagged or removed review is
-- a moderator's decision about whether the text may stand, and a feed showing it reports a review
-- the reader cannot open.
SELECT r."created_at" AS created_at, r."author_id" AS actor_id, u."name" AS actor_name,
       r."entity_id" AS scene_id, NULL::uuid AS list_id, NULL::text AS list_name,
       r."rating" AS rating
FROM reviews r
JOIN users u ON u."id" = r."author_id"
WHERE r."entity_type" = 'scene'
  AND r."status" = 'published'
  AND (sqlc.narg(before)::timestamptz IS NULL OR r."created_at" < sqlc.narg(before))
  AND (
      r."author_id" = ANY(sqlc.arg(actor_ids)::uuid[])
      OR EXISTS (
          SELECT 1 FROM user_follows pub
          WHERE pub."followed_id" = r."author_id" AND pub."activity_visibility" = 'public'
      )
  )
ORDER BY r."created_at" DESC, r."entity_id"
LIMIT sqlc.arg(row_limit)::int;

-- name: FindVisibleActivityActors :many
-- The actors whose activity this viewer may see: the ones who granted THIS viewer at least
-- 'followers', plus the ones who granted 'public' to anyone. Computed once per request and passed
-- to the three arms as an id array, so the rule is written down once in Go where a test can call it
-- directly.
SELECT DISTINCT uf."followed_id" AS actor_id
FROM user_follows uf
WHERE uf."follower_id" = sqlc.arg(viewer_id)
  AND uf."activity_visibility" IN ('followers', 'public');
