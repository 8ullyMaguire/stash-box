-- SPEC §7.24.1 verified-unknown markers (migration 96).
--
-- The storage was built and nothing read or wrote it: completion could not
-- suppress a gap, and no surface could assert one. These are the queries the two
-- halves need -- the write the edit path performs, and the read the completion
-- score performs.

-- name: CreateFieldVerificationState :one
-- One assertion per (entity, field), so a re-assertion REPLACES rather than
-- stacking: the unique index in 96 refuses a second row, and two rows would make
-- the suppression count double-count and let completion exceed what it should.
--
-- ON CONFLICT NAMES THE COLUMNS, NOT THE CONSTRAINT, and that is not a style choice: 96 declares
-- `field_verification_states_entity_field_key` as a UNIQUE INDEX, so `ON CONFLICT ON CONSTRAINT
-- <that name>` fails at runtime with `constraint ... does not exist (SQLSTATE 42704)` even though
-- the name reads like a constraint. Column inference matches the unique index, which is what
-- actually enforces the rule.
--
-- asserted_at is passed in rather than defaulted, because the caller is the edge
-- that knows whether this is an approval or an immediate application.
INSERT INTO field_verification_states
    (id, entity_type, entity_id, field, reason_code, asserted_by, asserted_at, citation_url, edit_id)
VALUES
    (gen_random_uuid(), sqlc.arg(entity_type), sqlc.arg(entity_id), sqlc.arg(field),
     sqlc.arg(reason_code), sqlc.arg(asserted_by), sqlc.arg(asserted_at),
     sqlc.narg(citation_url), sqlc.narg(edit_id))
ON CONFLICT (entity_type, entity_id, field)
DO UPDATE SET
    reason_code = EXCLUDED.reason_code,
    asserted_by = EXCLUDED.asserted_by,
    asserted_at = EXCLUDED.asserted_at,
    citation_url = EXCLUDED.citation_url,
    edit_id = EXCLUDED.edit_id
RETURNING *;

-- name: GetFieldVerificationStates :many
-- Every assertion about one entity, for the edit form and the entity page.
-- Joined to the registry so the caller reads the reason's MEANING (does it
-- suppress, may it self-affirm) without a second query and without the service
-- hard-coding which codes do what.
SELECT
    s.id, s.entity_type, s.entity_id, s.field, s.reason_code, s.asserted_by,
    s.asserted_at, s.citation_url, s.edit_id,
    r.suppresses_gap, r.allows_self_affirming
FROM field_verification_states s
JOIN field_verification_reasons r ON r.code = s.reason_code
WHERE s.entity_type = sqlc.arg(entity_type) AND s.entity_id = sqlc.arg(entity_id)
ORDER BY s.field;

-- name: GetSuppressingFieldVerificationStates :many
-- The (entity, field) pairs whose reason SUPPRESSES the completion gap, for one
-- entity. `not_yet_looked` is not one of them and that is the whole point: it
-- records a curator's belief and leaves the gap actionable.
--
-- Returns field names only. The completion score asks "is this gap answered",
-- which is a set-membership question about names -- a reason that does not
-- suppress must be indistinguishable here from no assertion at all.
SELECT s.field
FROM field_verification_states s
JOIN field_verification_reasons r ON r.code = s.reason_code
WHERE s.entity_type = sqlc.arg(entity_type)
  AND s.entity_id = sqlc.arg(entity_id)
  AND r.suppresses_gap;

-- name: GetFieldVerificationReasons :many
-- The reason registry, which is what the dropdown is built from and what the
-- service validates an incoming code against. Ordered so the list does not move
-- between requests: a dropdown whose options reorder is a dropdown that takes a
-- mis-click.
SELECT code, description, suppresses_gap, allows_self_affirming
FROM field_verification_reasons
ORDER BY code;

-- The author of the most recent OTHER edit of the scene an edit targets (SPEC §7.24.1's XP rule).
--
-- `edits` has no target id (migration 05's shape, kept; migration 118 widened target_type to
-- varchar(20) and left the id to the per-type tables), so the scene comes from scene_edits.
--
-- The current edit is excluded by ID rather than by comparing timestamps: it has ALREADY been
-- applied when this runs, so it is the entity's most recent edit, and `created_at < now` would be
-- a claim about clock resolution where `id <> edit_id` is a fact.
--
-- NULL user_id rows are skipped, so an anonymous edit is not "the previous editor" for anyone --
-- which matches selfAffirming's own treatment of an absent author.
-- name: GetPreviousSceneEditAuthor :one
SELECT e.user_id
FROM edits e
JOIN scene_edits se ON se.edit_id = e.id
WHERE se.scene_id = (SELECT se2.scene_id FROM scene_edits se2 WHERE se2.edit_id = sqlc.arg(edit_id))
  AND e.id <> sqlc.arg(edit_id)
  AND e.user_id IS NOT NULL
ORDER BY e.created_at DESC, e.id DESC
LIMIT 1;
