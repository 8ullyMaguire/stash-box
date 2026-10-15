-- name: FindContentSourceBlock :one
-- "May this URL, or anything on this host, enter the database at all?"
--
-- The pipeline asks this BEFORE it records anything, which is the whole point of the table: a
-- takedown that only marked existing rows would leave the sequence "notice about a URL we do not
-- have yet, member submits it, pipeline indexes it" ending with the instance serving the link it was
-- told to remove (migration 134).
--
-- One row, not a list: (kind, value) is unique, and a second block for the same key would be a row
-- nobody reads.
SELECT * FROM content_source_block
WHERE kind = @kind AND value = @value;

-- name: ListContentSourceBlocks :many
-- What this instance currently refuses, for the operator who has to be able to answer that.
--
-- Newest first, because the question "did the block I added yesterday land?" is asked more often
-- than "what has this instance accumulated since 2026".
SELECT * FROM content_source_block
ORDER BY created_at DESC, id
LIMIT @row_limit OFFSET @row_offset;

-- name: CreateContentSourceBlock :one
-- Blocks a key. Called inside the same transaction as the takedown it belongs to, so an operator
-- cannot end up with the sources marked and the block missing -- the half-applied state in which a
-- removed URL can be re-submitted and indexed the next day.
--
-- NO ON CONFLICT, and the reason is that a conflict here is not an error and not a no-op either:
-- the caller READ this key first and only reaches this statement when there was nothing to find, so
-- a conflict means two takedowns raced. `ON CONFLICT DO NOTHING` would return no row, and the caller
-- needs a row to report what the instance refuses; `ON CONFLICT DO UPDATE` would rewrite the first
-- decision's reason with the second notice's, and the block row's reason answers "who first decided
-- this", not "what was the last letter we received". The unique constraint stays as the backstop.
INSERT INTO content_source_block (id, kind, value, reason, created_by_id)
VALUES (@id, @kind, @value, @reason, @created_by_id)
RETURNING *;

-- name: DeleteContentSourceBlock :execrows
-- Releases a key. Deleting rather than flagging keeps this table the CURRENT policy: two histories
-- would eventually disagree, and this one's disagreement would be about whether a URL is blocked.
-- The audit row written in the same transaction is the history (migration 134).
DELETE FROM content_source_block
WHERE kind = @kind AND value = @value;
