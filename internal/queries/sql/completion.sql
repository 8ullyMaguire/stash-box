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
-- How many entities of a type are under a completion threshold.
--
-- This is the number a generated quest is sized from, and it is why the score is
-- not stored: the count is recomputed from the same inputs the score is, so a
-- quest cannot claim there are 500 incomplete performers when 3 were fixed an
-- hour ago.
--
-- The expression is the per-type weighted sum, written out rather than reached
-- through a shared view, because each type's fields are different columns and a
-- view over five shapes would be harder to read than five statements. The
-- WEIGHTS must match `internal/service/completion`; a divergence here is the one
-- place the two copies can disagree, and it is why this count is a
-- "how many are incomplete" rather than a score.
SELECT count(*)::bigint
FROM scenes s
WHERE NOT s.deleted
  AND (
    (NOT (s.title IS NOT NULL AND s.title <> '')) * 0
    + (NOT (s.duration IS NOT NULL AND s.duration > 0)) * 20
    + (NOT (s.studio_id IS NOT NULL)) * 15
    + (NOT EXISTS (SELECT 1 FROM scene_performers sp WHERE sp.scene_id = s.id)) * 20
    + (NOT (s.date IS NOT NULL)) * 5
    + (NOT EXISTS (SELECT 1 FROM scene_urls su
                   WHERE su.scene_id = s.id AND su.type = 'SCENE')) * 10
    + (NOT EXISTS (SELECT 1 FROM scene_urls su WHERE su.scene_id = s.id)) * 8
    + (NOT EXISTS (SELECT 1 FROM scene_tags st WHERE st.scene_id = s.id)) * 7
    + (NOT EXISTS (SELECT 1 FROM scene_images si WHERE si.scene_id = s.id)) * 5
    + (NOT (s.details IS NOT NULL AND s.details <> '')) * 10
    + ((SELECT count(*) FROM scene_snapshots ss WHERE ss.scene_id = s.id) < 12) * 10
  ) >= $1;

