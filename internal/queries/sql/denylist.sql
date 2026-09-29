-- name: ListDeniedTagIDs :many
-- The tag half of the content denylist, for the access rule that IS enforced.
--
-- Id only: the resolver maps a UUID straight into the rule's id set, and
-- selecting `reason` would mean every read drags a column no caller uses. An
-- operator audit reads the reason through a per-entity query, which is the
-- right shape for "why is this one entity denied" anyway.
SELECT "tag_id" FROM "content_denylist_tag";

-- name: ListDeniedStudioIDs :many
SELECT "studio_id" FROM "content_denylist_studio";

-- name: ListDeniedPerformerIDs :many
SELECT "performer_id" FROM "content_denylist_performer";
