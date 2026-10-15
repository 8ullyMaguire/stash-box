-- Collaborator queries (plan phase 8 slice 8e).
--
-- Four statements, and the shape of the set is visible in all of them: the OWNER is not a row, so
-- "may this person write this list" is never answered here alone -- see service/list's requireWriter,
-- which is the only place that question is decided.

-- name: AddListCollaborator :exec
INSERT INTO list_collaborators (list_id, user_id, added_by)
VALUES (@list_id, @user_id, @added_by);

-- name: RemoveListCollaborator :execrows
-- execrows rather than exec: "removed" and "was not a collaborator" are different answers, and the
-- service refuses the second by name instead of reporting a success that changed nothing.
DELETE FROM list_collaborators
WHERE list_id = @list_id AND user_id = @user_id;

-- name: FindListCollaborator :one
SELECT * FROM list_collaborators
WHERE list_id = @list_id AND user_id = @user_id;

-- name: FindListCollaborators :many
-- Ordered by insertion so the set is stable across reads; user_id breaks the tie for rows written
-- inside one statement's timestamp, which is what a test doing two adds in a row produces.
SELECT * FROM list_collaborators
WHERE list_id = @list_id
ORDER BY created_at, user_id;

-- name: CountListCollaborators :one
SELECT count(*) FROM list_collaborators
WHERE list_id = @list_id;
