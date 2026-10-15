-- name: FileContentSourceAppeal :one
-- Records a contest against a block, in the appellant's words.
--
-- NO ON CONFLICT, for the reason migration 141's own comment gives: the caller READ the block first (so
-- the appeal names a row that exists at the time it is filed), and a conflict on
-- (block_id, filed_by_id) means this person has already contested this block -- which is a refusal the
-- service reports BY NAME (ErrAppealAlreadyFiled) rather than a silent second row. The partial unique
-- index is the backstop, not the mechanism.
--
-- `block_id` is written here and is the ONLY place it is ever set: it goes NULL when a release deletes the
-- block, which is decision 1 of migration 141 -- the appeal keeps `kind`/`value` and loses only the
-- reference.
--
-- `blocked_by_id` is copied from the block IN THE SAME STATEMENT's transaction as it is read, because that
-- is the last moment the block is guaranteed to exist. It is what makes the "a different moderator decides"
-- rule enforceable on a pending appeal after a release has deleted the block row: the rule reads this
-- appeal, never the block.
INSERT INTO content_source_block_appeal (id, block_id, kind, value, filed_by_id, blocked_by_id, statement)
VALUES (@id, @block_id, @kind, @value, @filed_by_id, @blocked_by_id, @statement)
RETURNING *;

-- name: FindContentSourceAppeal :one
-- One appeal by id, decided or not. The decision path reads the row it is about to rule on, because the
-- two rules it enforces (not already decided, decider is neither the blocker nor the appellant) are
-- properties of THIS row.
SELECT * FROM content_source_block_appeal
WHERE id = @id;

-- name: ListContentSourceAppeals :many
-- The moderator's queue: undecided appeals, oldest first, because a queue is answered in the order people
-- waited. `@pending_only` is a column-free boolean rather than two queries, so the ordered listing and the
-- pending listing cannot drift apart in their ordering or their limit handling.
--
-- An appeal nobody can list is an appeal nobody answers, which is why this query exists at all: the appeal
-- is only a policy if the queue is a surface.
SELECT * FROM content_source_block_appeal
WHERE (NOT @pending_only::boolean OR decided_at IS NULL)
ORDER BY filed_at ASC, id ASC
LIMIT @row_limit OFFSET @row_offset;

-- name: DecideContentSourceAppeal :one
-- Decides an appeal, ONCE.
--
-- `WHERE decided_at IS NULL` in the UPDATE is the whole guard: a second decision matches no row, so this
-- statement returns pgx.ErrNoRows and the service reports ErrAppealAlreadyDecided instead of overwriting
-- the first outcome with the second. The service also reads the row first (for the identity rule), so this
-- is the concurrency backstop -- two operators deciding at the same moment cannot both win.
--
-- The three decision columns are written together, which is what the table's two CHECK constraints
-- require; setting any two of them is a constraint violation rather than a half-decided row.
UPDATE content_source_block_appeal
SET decided_by_id = @decided_by_id,
    outcome = @outcome,
    decision_note = @decision_note,
    decided_at = NOW()
WHERE id = @id AND decided_at IS NULL
RETURNING *;
