-- name: CreatePerformerProfileSnapshot :one
-- Record what a page said about a performer, at this instant.
--
-- ON CONFLICT DO UPDATE rather than DO NOTHING on purpose: DO NOTHING returns NO ROW, so the caller
-- would have to re-select, and the re-select is the race this statement exists to close. The update
-- is a no-op (it writes back the hash it collided on) and captured_at is untouched -- a second
-- capture of the same content IS the same record, and moving its timestamp would turn a record into
-- a cache with a fresh-looking date.
INSERT INTO performer_profile_snapshots (
    id, performer_id, source_url, url_normalized, content_hash, content
) VALUES (
    @id, @performer_id, @source_url, @url_normalized, @content_hash, @content
)
ON CONFLICT (performer_id, url_normalized, content_hash)
DO UPDATE SET content_hash = EXCLUDED.content_hash
RETURNING *;

-- name: ListPerformerProfileSnapshots :many
-- A performer's captures, newest first. `id` breaks ties so two captures in the same second have a
-- stable order: a page that changes twice in one second is unlikely and an unstable page is not.
SELECT * FROM performer_profile_snapshots
WHERE performer_id = @performer_id
ORDER BY captured_at DESC, id DESC;
