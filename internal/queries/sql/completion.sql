-- Completion score inputs (SPEC §7.7).
--
-- These queries gather WHICH FIELDS ARE PRESENT. They deliberately do NOT compute
-- the score: the weighting and the arithmetic live in
-- `internal/service/completion`, as a pure function a test can exercise with
-- hand-built fixtures. Computing the fraction in SQL as well would be a second
-- copy of the formula, and the two would drift the first time a weight changed.
--
-- So each row here is a set of booleans, and the service turns them into a score.

-- name: PerformerCompletionInputs :one
-- Which performer fields are filled.
--
-- birthdate_accuracy is the subtle one, and it is why this is a query rather than
-- a struct scan. SPEC §7.7 counts "missing metadata", and a birthdate recorded as
-- 1990-01-01 with accuracy 'unknown' is a SPECIFIC FALSE CLAIM rather than an
-- absence. An absence is obviously worth fixing; a specific false claim reads as
-- an answer, so a curator who trusts it never goes looking for the real value.
-- So an uncertain birthdate is reported as ABSENT, not present.
--
-- measurements groups the four size columns: a curator fills them in as a block,
-- and a per-column score would make a performer with a cup size and no waist
-- look twice as complete as one with neither.
SELECT
    p.id,
    COALESCE(p.name IS NOT NULL, FALSE) AS has_name,
    EXISTS (SELECT 1 FROM performer_aliases pa WHERE pa.performer_id = p.id)
        AS has_aliases,
    COALESCE(
        -- The accuracy lives IN the value, not beside it. Migration 42 converted
        -- `birthdate date` + `birthdate_accuracy varchar` into a single TEXT
        -- column and dropped the accuracy column: '1990' is year-accurate,
        -- '1990-01' is month-accurate, '1990-01-01' is exact.
        --
        -- So the rule is a LENGTH test, and it is a real test rather than a
        -- convention -- '1990' is genuinely less information than '1990-01-01',
        -- and a performer whose birthdate is known only to the year is a
        -- performer a curator should still be asked about.
        --
        -- I had built this rule around a `birthdate_accuracy` column that I read
        -- in migration 01 and never checked against the live schema. It was
        -- dropped in migration 42. A column named in a comment in a migration
        -- from six revisions back is not a fact about the database.
        p.birthdate IS NOT NULL AND p.birthdate <> ''
            AND length(p.birthdate) >= 10,
        FALSE
    ) AS has_birthdate,
    COALESCE(p.gender IS NOT NULL AND p.gender <> '', FALSE) AS has_gender,
    COALESCE(p.ethnicity IS NOT NULL AND p.ethnicity <> '', FALSE) AS has_ethnicity,
    COALESCE(p.country IS NOT NULL AND p.country <> '', FALSE) AS has_country,
    COALESCE(p.eye_color IS NOT NULL AND p.eye_color <> '', FALSE) AS has_eye_color,
    COALESCE(p.hair_color IS NOT NULL AND p.hair_color <> '', FALSE) AS has_hair_color,
    COALESCE(p.height IS NOT NULL, FALSE) AS has_height,
    COALESCE(p.cup_size IS NOT NULL OR p.band_size IS NOT NULL
        OR p.hip_size IS NOT NULL OR p.waist_size IS NOT NULL, FALSE)
        AS has_measurements,
    COALESCE(p.career_start_year IS NOT NULL OR p.career_end_year IS NOT NULL, FALSE)
        AS has_career_dates,
    EXISTS (SELECT 1 FROM performer_urls pu WHERE pu.performer_id = p.id)
        AS has_urls,
    EXISTS (SELECT 1 FROM performer_images pi WHERE pi.performer_id = p.id)
        AS has_image
FROM performers p
WHERE p.id = $1;

-- name: SceneCompletionInputs :one
-- Which scene fields are filled.
--
-- duration is first because it is the most heavily weighted scene field and
-- because the snapshot collage and the identification board both need a time axis:
-- without a duration neither can render, and a scene with no duration is not
-- merely under-documented, it is unusable.
--
-- snapshot coverage is a THRESHOLD, not a count, and the threshold is 12 because
-- SPEC §8's minimum collage is 12 frames. A scene with 11 snapshots cannot
-- produce a compliant collage, so it scores exactly as a scene with none: a
-- half-finished collage is not a partial collage, it is no collage.
SELECT
    s.id,
    COALESCE(s.title IS NOT NULL AND s.title <> '', FALSE) AS has_name,
    COALESCE(s.duration IS NOT NULL AND s.duration > 0, FALSE) AS has_duration,
    COALESCE(s.studio_id IS NOT NULL, FALSE) AS has_studio,
    EXISTS (SELECT 1 FROM scene_performers sp WHERE sp.scene_id = s.id)
        AS has_performers,
    COALESCE(s.date IS NOT NULL, FALSE) AS has_date,
    -- ONE fact, not two. scene_urls is (scene_id, site_id, url): there is no
    -- `type` column, so a linked url IS a link to the site that url came from.
    -- Counting it twice would give a scene with a single URL credit for both
    -- "linked to its site" and "has a source url" and inflate every scene score
    -- by a field nobody can fill independently.
    EXISTS (SELECT 1 FROM scene_urls su WHERE su.scene_id = s.id) AS has_urls,
    EXISTS (SELECT 1 FROM scene_tags st WHERE st.scene_id = s.id) AS has_tags,
    EXISTS (SELECT 1 FROM scene_images si WHERE si.scene_id = s.id) AS has_image,
    COALESCE(s.details IS NOT NULL AND s.details <> '', FALSE) AS has_details,
    (SELECT count(*) FROM scene_snapshots ss WHERE ss.scene_id = s.id) >= 12
        AS has_snapshot_coverage
FROM scenes s
WHERE s.id = $1 AND NOT s.deleted;

-- name: StudioCompletionInputs :one
SELECT
    st.id,
    COALESCE(st.name IS NOT NULL AND st.name <> '', FALSE) AS has_name,
    EXISTS (SELECT 1 FROM studio_urls su WHERE su.studio_id = st.id) AS has_urls,
    EXISTS (SELECT 1 FROM studio_images si WHERE si.studio_id = st.id) AS has_image,
    COALESCE(st.parent_studio_id IS NOT NULL, FALSE) AS has_parent
FROM studios st
WHERE st.id = $1 AND NOT st.deleted;

-- name: SiteCompletionInputs :one
SELECT
    si.id,
    COALESCE(si.name IS NOT NULL AND si.name <> '', FALSE) AS has_name,
    COALESCE(si.url IS NOT NULL AND si.url <> '', FALSE) AS has_urls,
    COALESCE(si.description IS NOT NULL AND si.description <> '', FALSE)
        AS has_details,
    COALESCE(si.regex IS NOT NULL AND si.regex <> '', FALSE) AS has_regex
FROM sites si
WHERE si.id = $1;

-- name: TagCompletionInputs :one
SELECT
    t.id,
    COALESCE(t.name IS NOT NULL AND t.name <> '', FALSE) AS has_name,
    COALESCE(t.description IS NOT NULL AND t.description <> '', FALSE) AS has_details
FROM tags t
WHERE t.id = $1;

-- name: PerformersMissingField :many
-- Performers missing one field, for a generated quest.
--
-- Birthdate is the interesting filter and the reason this is a query rather than
-- a scan of PerformerCompletionInputs: the quest "add missing birthdates for 5
-- performers" is asking for entities where the birthdate is ABSENT **or
-- UNCERTAIN**, and the second half is the half that matters -- a performer with a
-- guessed birthdate looks complete to every query that only checks for NULL.
--
-- Ordered by the id for determinism, then bounded by the caller. An unbounded
-- list of every incomplete performer in the archive is not a quest, it is a
-- table scan with extra steps.
SELECT p.id
FROM performers p
WHERE NOT p.deleted
  AND (
    p.birthdate IS NULL
    OR p.birthdate = ''
    -- Shorter than 'YYYY-MM-DD', so year- or month-accurate rather than exact.
    OR length(p.birthdate) < 10
  )
ORDER BY p.created_at, p.id
LIMIT $1;

-- name: ScenesMissingField :many
-- Scenes missing one field, for a generated quest.
SELECT s.id
FROM scenes s
WHERE NOT s.deleted
  AND ($1::text = 'duration' AND (s.duration IS NULL OR s.duration <= 0)
    OR $1::text = 'studio' AND s.studio_id IS NULL
    OR $1::text = 'performers'
       AND NOT EXISTS (SELECT 1 FROM scene_performers sp WHERE sp.scene_id = s.id)
    OR $1::text = 'site'
       AND NOT EXISTS (SELECT 1 FROM scene_urls su WHERE su.scene_id = s.id)
    OR $1::text = 'tags'
       AND NOT EXISTS (SELECT 1 FROM scene_tags st WHERE st.scene_id = s.id)
  )
ORDER BY s.created_at, s.id
LIMIT $2;

-- name: CountEntitiesWithCompletionBelow :one
-- How many entities of a type have at least this much MISSING weight.
--
-- Expressed as missing weight rather than as a score, because the WEIGHTS live in
-- Go, in `internal/service/completion/score.go`, and this is a query. Three ways to
-- bridge that gap, and the other two are worse:
--
--   - Recompute the score in SQL. Then the formula exists twice and the copies
--     drift the first time a weight changes -- silently, because both still
--     return a plausible number.
--   - Store the score. Then it is a second source of truth beside the columns it
--     summarises, and the first edit that skips the refresh leaves it wrong.
--
-- So this asks the question the formula answers -- how much weight is MISSING --
-- and the comparison against the threshold happens in ONE place, the service.
-- The weights below are a real duplication, confined to constants rather than to a
-- formula, and `TestTheSQLWeightsMatchTheGoWeights` fails the build when the two
-- copies disagree.
--
-- EVERY TERM IS CAST: `(NOT (...))::int * weight`. PostgreSQL has no
-- boolean-times-integer operator, so `expr * N` is a type error, and a cast placed
-- INSIDE the NOT -- `NOT (...)::int` -- casts NOT's argument and is rejected the
-- same way. The first version of this query had never been executed by anything,
-- because `sqlc generate` type-checks the SQL's SYNTAX and not the expressions'
-- semantics; the error only appeared when a test finally ran it.
SELECT
    CASE sqlc.arg(entity_type)::text
        WHEN 'performer' THEN (
            SELECT count(*) FROM performers p
            WHERE NOT p.deleted AND (
                + (NOT (p.name IS NOT NULL AND p.name <> ''))::int * 0
                + (NOT (EXISTS (SELECT 1 FROM performer_aliases pa WHERE pa.performer_id = p.id)))::int * 10
                + (NOT (p.birthdate IS NOT NULL AND length(p.birthdate) >= 10))::int * 15
                + (NOT (p.gender IS NOT NULL AND p.gender <> ''))::int * 5
                + (NOT (p.ethnicity IS NOT NULL AND p.ethnicity <> ''))::int * 3
                + (NOT (p.country IS NOT NULL AND p.country <> ''))::int * 8
                + (NOT (p.eye_color IS NOT NULL AND p.eye_color <> ''))::int * 2
                + (NOT (p.hair_color IS NOT NULL AND p.hair_color <> ''))::int * 2
                + (NOT (p.height IS NOT NULL))::int * 5
                + (NOT (p.cup_size IS NOT NULL OR p.band_size IS NOT NULL
                       OR p.hip_size IS NOT NULL OR p.waist_size IS NOT NULL))::int * 5
                + (NOT (p.career_start_year IS NOT NULL OR p.career_end_year IS NOT NULL))::int * 5
                + (NOT (EXISTS (SELECT 1 FROM performer_urls pu WHERE pu.performer_id = p.id)))::int * 10
                + (NOT (EXISTS (SELECT 1 FROM performer_images pi WHERE pi.performer_id = p.id)))::int * 10
            ) >= sqlc.arg(min_missing)::int
        )
        WHEN 'scene' THEN (
            SELECT count(*) FROM scenes s
            WHERE NOT s.deleted AND (
                + (NOT (s.title IS NOT NULL AND s.title <> ''))::int * 0
                + (NOT (s.duration IS NOT NULL AND s.duration > 0))::int * 20
                + (NOT (s.studio_id IS NOT NULL))::int * 15
                + (NOT (EXISTS (SELECT 1 FROM scene_performers sp WHERE sp.scene_id = s.id)))::int * 20
                + (NOT (s.date IS NOT NULL))::int * 5
                + (NOT (EXISTS (SELECT 1 FROM scene_urls su WHERE su.scene_id = s.id)))::int * 18
                + (NOT (EXISTS (SELECT 1 FROM scene_tags st WHERE st.scene_id = s.id)))::int * 7
                + (NOT (EXISTS (SELECT 1 FROM scene_images si WHERE si.scene_id = s.id)))::int * 5
                + (NOT (s.details IS NOT NULL AND s.details <> ''))::int * 10
                + ((SELECT count(*) FROM scene_snapshots ss WHERE ss.scene_id = s.id) >= 12)::int * 10
            ) >= sqlc.arg(min_missing)::int
        )
        WHEN 'studio' THEN (
            SELECT count(*) FROM studios st
            WHERE NOT st.deleted AND (
                + (NOT (st.name IS NOT NULL AND st.name <> ''))::int * 0
                + (NOT (EXISTS (SELECT 1 FROM studio_urls su WHERE su.studio_id = st.id)))::int * 40
                + (NOT (EXISTS (SELECT 1 FROM studio_images si WHERE si.studio_id = st.id)))::int * 30
                + (NOT (st.parent_studio_id IS NOT NULL))::int * 30
            ) >= sqlc.arg(min_missing)::int
        )
        WHEN 'site' THEN (
            SELECT count(*) FROM sites si
            WHERE TRUE AND (
                + (NOT (si.name IS NOT NULL AND si.name <> ''))::int * 0
                + (NOT (si.url IS NOT NULL AND si.url <> ''))::int * 35
                + (NOT (si.description IS NOT NULL AND si.description <> ''))::int * 35
                + (NOT (si.regex IS NOT NULL AND si.regex <> ''))::int * 30
            ) >= sqlc.arg(min_missing)::int
        )
        WHEN 'tag' THEN (
            SELECT count(*) FROM tags t
            WHERE NOT t.deleted AND (
                + (NOT (t.name IS NOT NULL AND t.name <> ''))::int * 40
                + (NOT (t.description IS NOT NULL AND t.description <> ''))::int * 60
            ) >= sqlc.arg(min_missing)::int
        )
        ELSE 0
    END::bigint;

-- name: ListIncompleteEntities :many
-- Entities of a type with at least this much missing weight, for a generated quest.
--
-- A quest is a pure function of the archive, so it is never stored: this query and
-- the weights are the whole of it, and a quest recomputed now names only entities
-- that are incomplete NOW. A stored quest accumulates claims that were true when it
-- was written, and a curator working from it is chasing performers who were fixed
-- an hour ago.
--
-- Only performers and scenes are generated, and that is a scope decision recorded
-- rather than an oversight: those are the two types where "this field is missing"
-- is a specific, findable piece of work. A studio missing a parent studio is
-- NORMAL -- most studios genuinely have no parent -- so a quest for it would be an
-- unending list of items that are not actually gaps.
SELECT p.id
FROM performers p
WHERE sqlc.arg(entity_type)::text = 'performer' AND NOT p.deleted
  AND (
      + (NOT (p.name IS NOT NULL AND p.name <> ''))::int * 0
      + (NOT (EXISTS (SELECT 1 FROM performer_aliases pa WHERE pa.performer_id = p.id)))::int * 10
      + (NOT (p.birthdate IS NOT NULL AND length(p.birthdate) >= 10))::int * 15
      + (NOT (p.gender IS NOT NULL AND p.gender <> ''))::int * 5
      + (NOT (p.ethnicity IS NOT NULL AND p.ethnicity <> ''))::int * 3
      + (NOT (p.country IS NOT NULL AND p.country <> ''))::int * 8
      + (NOT (p.eye_color IS NOT NULL AND p.eye_color <> ''))::int * 2
      + (NOT (p.hair_color IS NOT NULL AND p.hair_color <> ''))::int * 2
      + (NOT (p.height IS NOT NULL))::int * 5
      + (NOT (p.cup_size IS NOT NULL OR p.band_size IS NOT NULL
                       OR p.hip_size IS NOT NULL OR p.waist_size IS NOT NULL))::int * 5
      + (NOT (p.career_start_year IS NOT NULL OR p.career_end_year IS NOT NULL))::int * 5
      + (NOT (EXISTS (SELECT 1 FROM performer_urls pu WHERE pu.performer_id = p.id)))::int * 10
      + (NOT (EXISTS (SELECT 1 FROM performer_images pi WHERE pi.performer_id = p.id)))::int * 10
  ) >= sqlc.arg(min_missing)::int
  AND p.id > COALESCE(sqlc.arg(after_id)::uuid,
                      '00000000-0000-0000-0000-000000000000'::uuid)

UNION ALL

SELECT s.id
FROM scenes s
WHERE sqlc.arg(entity_type)::text = 'scene' AND NOT s.deleted
  AND (
      + (NOT (s.title IS NOT NULL AND s.title <> ''))::int * 0
      + (NOT (s.duration IS NOT NULL AND s.duration > 0))::int * 20
      + (NOT (s.studio_id IS NOT NULL))::int * 15
      + (NOT (EXISTS (SELECT 1 FROM scene_performers sp WHERE sp.scene_id = s.id)))::int * 20
      + (NOT (s.date IS NOT NULL))::int * 5
      + (NOT (EXISTS (SELECT 1 FROM scene_urls su WHERE su.scene_id = s.id)))::int * 18
      + (NOT (EXISTS (SELECT 1 FROM scene_tags st WHERE st.scene_id = s.id)))::int * 7
      + (NOT (EXISTS (SELECT 1 FROM scene_images si WHERE si.scene_id = s.id)))::int * 5
      + (NOT (s.details IS NOT NULL AND s.details <> ''))::int * 10
      + ((SELECT count(*) FROM scene_snapshots ss WHERE ss.scene_id = s.id) >= 12)::int * 10
  ) >= sqlc.arg(min_missing)::int
  AND s.id > COALESCE(sqlc.arg(after_id)::uuid,
                      '00000000-0000-0000-0000-000000000000'::uuid)

-- Ordered and limited OUTSIDE the union, and that is not a style choice: ORDER BY
-- binds to the last SELECT of a set operation, so ordering the scene branch alone
-- would order the scenes and leave the performers in whatever order the planner
-- produced, interleaving the two.
ORDER BY id
LIMIT sqlc.arg(page_size)::int;
