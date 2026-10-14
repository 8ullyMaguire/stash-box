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
INSERT INTO ingest_queue (url, url_normalized, submitted_by, target_entity_id)
VALUES (@url, @url_normalized, @submitted_by, @target_entity_id)
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

-- name: FindScenesByFingerprint :many
-- Stage 5: "is this content already here under another URL?" (spec §1).
--
-- Fingerprints are per (scene, fingerprint, USER): the same scene can carry the same hash
-- from several submitters, and `vote` records whether they corroborated it. So a match is
-- any row at all, deduplicated by scene, and deliberately NOT filtered on `vote = 1`: a
-- single submission of a hash is evidence the content is there, which is all dedup needs,
-- and requiring corroboration would make the merge path depend on a second person having
-- submitted the same file.
--
-- A NEGATIVE vote is excluded, because that is the one value that means "this hash is NOT
-- that scene". That is the fork's own corroboration rule (the same distinction
-- SubmittedHashExists draws in the other direction).
--
-- Ordered by scene_id so the caller's pick of a merge target is deterministic rather than
-- whichever row the planner returned first -- an unordered LIMIT 1 makes the same
-- submission land on different scenes on different runs, which is unreproducible in a bug
-- report and visible in production as a source that moves.
SELECT DISTINCT sf.scene_id
FROM scene_fingerprints sf
JOIN fingerprints f ON sf.fingerprint_id = f.id
WHERE f.hash = @hash
  AND f.algorithm = @algorithm
  AND (sf.vote IS NULL OR sf.vote >= 0)
ORDER BY sf.scene_id
LIMIT 20;

-- name: CreateAggregateScene :one
-- Stage 6: a record for content this instance points at and does not host.
--
-- `origin = 'aggregate'` is the whole point of the row and is hardcoded here rather than
-- passed in: this statement exists to create aggregated content, and a caller able to
-- write 'library' through it would be creating a scene that claims a member uploaded
-- something they did not.
--
-- studio_id is left NULL on purpose, and it is the decision in this statement worth
-- stating. The extractor reads a studio NAME off a page; `studio_id` is a foreign key into
-- a directory that a human curates. Populating it would mean creating studios on the
-- strength of a machine's parse -- the fork's own rule that a guess is evidence and not
-- authority -- and the name is not lost: it goes in `details`, where a curator can promote
-- it deliberately (or an edit can, through the normal path).
--
-- No fingerprint is attached here either; the pipeline adds one only when the extractor
-- produced a real hash, and only attributed to a submitting user, because
-- scene_fingerprints.user_id is NOT NULL and "extracted by a machine" is not a user.
INSERT INTO scenes (
    id, title, details, date, production_date, duration, origin, created_at, updated_at
) VALUES (
    @id, @title, @details, @date, @production_date, @duration, 'aggregate', now(), now()
)
RETURNING *;

-- name: RejectIngestJob :one
-- A refusal, which is not a failure and gets its own status because of what the two mean.
--
-- A `rejected` row says the pipeline DECLINED the URL before asking the network anything: a
-- takedown, a host with no site. Nothing went wrong and there is nothing to retry -- asking
-- again would ask a question that has already been answered. A `failed` row is a fetch
-- outcome: the attempt was made and did not succeed, which is why FailIngestJob computes a
-- backoff and why the transient arm returns the row to 'pending'.
--
-- The distinction is the one the spec argues for at §3. Lumping them meant "how many of the
-- last hundred submissions were refused rather than broken" was a string comparison over
-- `reason` -- on a table that has a column built to answer it.
--
-- `finished_at` is set, because a refusal IS finished work. No outcome and no backoff: a
-- rejected row ran nothing to completion (`ingest_queue_done_has_outcome` keeps those two
-- facts from drifting), and its retry is a person's decision, not a timer.
UPDATE ingest_queue
SET status      = 'rejected',
    last_error  = @last_error,
    reason      = @reason,
    finished_at = now()
WHERE id = @id
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
