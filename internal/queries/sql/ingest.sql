-- The ingest queue's statements (migration 109, spec §3).
--
-- WHY THE CLAIM IS ONE STATEMENT. `SELECT ... FOR UPDATE SKIP LOCKED` followed by an
-- `UPDATE` is the shape that has two workers claim one row: the lock is released at the
-- end of the statement, so the window between the read and the write is exactly where the
-- second worker looks. The CTE below holds the lock INTO the update, so the race does not
-- exist rather than being made unlikely. scripts/verify-109.sh extracts this statement by
-- name and asserts the two-claim behaviour against the real database, so the text here is
-- not a claim about what some other copy does.

-- name: EnqueueIngest :one
INSERT INTO ingest_queue (url, url_normalized, submitted_by)
VALUES (@url, @url_normalized, @submitted_by)
-- The predicate is REQUIRED and it is the index's own: for a partial unique index,
-- `ON CONFLICT (url_normalized)` alone does not match any index and Postgres reports
-- "there is no unique or exclusion constraint matching the ON CONFLICT specification" --
-- an error that names the conflict clause rather than the predicate it is missing.
--
-- DO NOTHING plus RETURNING reports the conflict as NO ROWS RETURNED, not as an error, so
-- the caller distinguishes "queued" (a row) from "already queued" (pgx.ErrNoRows) by
-- absence. That is deliberate: a duplicate submission is not a failure and must not be
-- reported as one (spec §5), so this is the one place where ErrNoRows is a success.
ON CONFLICT (url_normalized) WHERE status IN ('pending', 'running', 'done') DO NOTHING
RETURNING *;

-- name: ClaimIngestJob :one
WITH claimed AS (
    SELECT id
    FROM ingest_queue
    WHERE status = 'pending'
      AND next_attempt_at <= now()
    ORDER BY next_attempt_at, created_at
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
UPDATE ingest_queue q
SET status     = 'running',
    claimed_at = now(),
    started_at = now(),
    attempts   = q.attempts + 1
FROM claimed
WHERE q.id = claimed.id
RETURNING q.*;

-- name: FinishIngestJob :one
-- The terminal success. `last_error` is cleared rather than left behind: a row that
-- succeeded after two failures must not carry the text of a failure it recovered from,
-- because every reader of last_error is asking "what is wrong with this row NOW".
UPDATE ingest_queue
SET status          = 'done',
    outcome         = @outcome,
    result_scene_id = @result_scene_id,
    confidence      = @confidence,
    finished_at     = now(),
    last_error      = NULL
WHERE id = @id
RETURNING *;

-- name: FailIngestJob :one
-- A failure, transient or permanent, and the column that says which.
--
-- A TRANSIENT failure returns the row to 'pending' with a future next_attempt_at instead
-- of burning it: a host that timed out is not a host that refused, and marking it failed
-- permanently is how a queue accumulates rows nobody will retry. The attempt count is the
-- guard against retrying forever; deciding that count's ceiling is phase 9's sweep, and
-- this statement only records the fact.
--
-- The cast on @permanent is not decoration: Postgres cannot infer a type for a parameter
-- used only as a CASE condition, and answers "could not determine data type of parameter"
-- rather than guessing boolean.
UPDATE ingest_queue
SET status          = CASE WHEN @permanent::boolean THEN 'failed' ELSE 'pending' END,
    last_error      = @last_error,
    reason          = @reason,
    next_attempt_at = now() + make_interval(secs => @backoff_seconds::int)
WHERE id = @id
RETURNING *;
