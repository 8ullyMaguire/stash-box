-- Performer queries

-- name: CreatePerformer :one
INSERT INTO performers (
    id, name, disambiguation, gender, birthdate,
    ethnicity, country, eye_color, hair_color, height, weight, cup_size,
    band_size, hip_size, waist_size, breast_type, career_start_year,
    career_end_year, deathdate, genitals, penis_length, created_at, updated_at
)
VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13,
    $14, $15, $16, $17, $18, $19, $20, $21, now(), now()
)
RETURNING *;

-- name: UpdatePerformer :one
UPDATE performers
SET name = $2, disambiguation = $3, gender = $4, birthdate = $5,
    ethnicity = $6, country = $7, eye_color = $8, hair_color = $9,
    height = $10, weight = $11, cup_size = $12, band_size = $13, hip_size = $14,
    waist_size = $15, breast_type = $16, career_start_year = $17,
    career_end_year = $18, deathdate = $19, genitals = $20, penis_length = $21,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeletePerformer :exec
DELETE FROM performers WHERE id = $1;

-- name: SoftDeletePerformer :one
UPDATE performers SET deleted = true, updated_at = NOW() WHERE id = $1
RETURNING *;

-- name: FindPerformer :one
SELECT * FROM performers WHERE id = $1;

-- name: FindPerformerWithRedirect :many
SELECT P.* FROM performers P
WHERE P.id = $1 AND P.deleted = FALSE
UNION
SELECT T.* FROM performer_redirects R
JOIN performers T ON T.id = R.target_id
WHERE R.source_id = $1 AND T.deleted = FALSE;

-- name: FindPerformersByIds :many
SELECT * FROM performers WHERE id = ANY($1::UUID[]);

-- name: FindPerformerByName :one
SELECT * FROM performers WHERE UPPER(name) = UPPER($1) AND deleted = false;

-- name: FindExistingPerformers :many
SELECT * FROM performers
WHERE (
    (sqlc.narg('name')::text IS NOT NULL AND TRIM(LOWER(name)) = TRIM(LOWER(sqlc.narg('name'))) AND
     CASE
       WHEN sqlc.narg('disambiguation')::text IS NOT NULL
       THEN TRIM(LOWER(disambiguation)) = TRIM(LOWER(sqlc.narg('disambiguation')))
       ELSE (disambiguation IS NULL OR disambiguation = '')
     END)
    OR
    (sqlc.narg('urls')::text[] IS NOT NULL AND
     id IN (
       SELECT performer_id
       FROM performer_urls
       WHERE url = ANY(sqlc.narg('urls'))
       GROUP BY performer_id
     ))
);

-- name: FindPerformersByURL :many
SELECT P.*
FROM performers P
JOIN performer_urls PU ON PU.performer_id = P.id
WHERE LOWER(PU.url) = LOWER(sqlc.narg('url'))
LIMIT sqlc.arg('limit');

-- Keep the WHERE clause in sync across SearchPerformers, CountPerformerSearchMatches,
-- and GetPerformerSearchFacets so paging, counts, and facets stay consistent.

-- name: SearchPerformers :many
SELECT performer_id
FROM performer_search
WHERE performer_id @@@ paradedb.disjunction_max(disjuncts => ARRAY[
    paradedb.boolean(
        should => ARRAY[
            paradedb.disjunction_max(disjuncts => ARRAY[
                paradedb.boost(factor => 1.5, query => paradedb.match(field => 'name', value => sqlc.arg('term')::TEXT)),
                paradedb.boost(factor => 2.0, query => (jsonb_build_object(
                    'tokenized_phrase', jsonb_build_object('field', 'name', 'phrase', sqlc.arg('term')::TEXT)
                ))::paradedb.searchqueryinput)
            ]),
            paradedb.match(field => 'disambiguation', value => sqlc.arg('term')::TEXT)
        ]
    ),
    (jsonb_build_object(
        'tokenized_phrase', jsonb_build_object('field', 'aliases', 'phrase', sqlc.arg('term')::TEXT)
    ))::paradedb.searchqueryinput
])
AND (sqlc.narg('filter_gender')::TEXT IS NULL OR gender = sqlc.narg('filter_gender')::TEXT)
ORDER BY pdb.score(performer_id) DESC, performer_id
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountPerformerSearchMatches :one
SELECT pdb.agg('{"value_count": {"field": "performer_id"}}') AS total_count
FROM performer_search
WHERE performer_id @@@ paradedb.disjunction_max(disjuncts => ARRAY[
    paradedb.boolean(
        should => ARRAY[
            paradedb.disjunction_max(disjuncts => ARRAY[
                paradedb.boost(factor => 1.5, query => paradedb.match(field => 'name', value => sqlc.arg('term')::TEXT)),
                paradedb.boost(factor => 2.0, query => (jsonb_build_object(
                    'tokenized_phrase', jsonb_build_object('field', 'name', 'phrase', sqlc.arg('term')::TEXT)
                ))::paradedb.searchqueryinput)
            ]),
            paradedb.match(field => 'disambiguation', value => sqlc.arg('term')::TEXT)
        ]
    ),
    (jsonb_build_object(
        'tokenized_phrase', jsonb_build_object('field', 'aliases', 'phrase', sqlc.arg('term')::TEXT)
    ))::paradedb.searchqueryinput
])
AND (sqlc.narg('filter_gender')::TEXT IS NULL OR gender = sqlc.narg('filter_gender')::TEXT);

-- name: GetPerformerSearchFacets :one
SELECT pdb.agg('{"terms": {"field": "gender"}}') AS gender_facets
FROM performer_search
WHERE performer_id @@@ paradedb.disjunction_max(disjuncts => ARRAY[
    paradedb.boolean(
        should => ARRAY[
            paradedb.disjunction_max(disjuncts => ARRAY[
                paradedb.boost(factor => 1.5, query => paradedb.match(field => 'name', value => sqlc.arg('term')::TEXT)),
                paradedb.boost(factor => 2.0, query => (jsonb_build_object(
                    'tokenized_phrase', jsonb_build_object('field', 'name', 'phrase', sqlc.arg('term')::TEXT)
                ))::paradedb.searchqueryinput)
            ]),
            paradedb.match(field => 'disambiguation', value => sqlc.arg('term')::TEXT)
        ]
    ),
    (jsonb_build_object(
        'tokenized_phrase', jsonb_build_object('field', 'aliases', 'phrase', sqlc.arg('term')::TEXT)
    ))::paradedb.searchqueryinput
])
AND (sqlc.narg('filter_gender')::TEXT IS NULL OR gender = sqlc.narg('filter_gender')::TEXT);

-- Performer aliases

-- name: DeletePerformerAliases :exec
DELETE FROM performer_aliases WHERE performer_id = $1;

-- name: GetPerformerAliases :many
SELECT alias FROM performer_aliases WHERE performer_id = $1;

-- name: FindPerformerByAlias :one
SELECT p.* FROM performers p
JOIN performer_aliases pa ON p.id = pa.performer_id
WHERE UPPER(pa.alias) = UPPER($1) AND p.deleted = false;

-- Performer URLs

-- name: DeletePerformerURLs :exec
DELETE FROM performer_urls WHERE performer_id = $1;

-- name: GetPerformerURLs :many
SELECT url, site_id FROM performer_urls WHERE performer_id = $1;

-- Performer tattoos

-- name: DeletePerformerTattoos :exec
DELETE FROM performer_tattoos WHERE performer_id = $1;

-- name: GetPerformerTattoos :many
SELECT location, description FROM performer_tattoos WHERE performer_id = $1;

-- Performer piercings

-- name: DeletePerformerPiercings :exec
DELETE FROM performer_piercings WHERE performer_id = $1;

-- name: GetPerformerPiercings :many
SELECT location, description FROM performer_piercings WHERE performer_id = $1;

-- Performer redirects

-- name: CreatePerformerRedirect :exec
INSERT INTO performer_redirects (source_id, target_id) VALUES ($1, $2);

-- name: UpdatePerformerRedirects :exec
UPDATE performer_redirects SET target_id = @new_performer_id WHERE target_id = @old_performer_id;

-- Performer favorites

-- name: DeletePerformerFavorites :exec
DELETE FROM performer_favorites WHERE performer_id = $1;

-- name: ReassignPerformerFavorites :exec
UPDATE performer_favorites
   SET performer_id = @new_performer_id
   WHERE performer_favorites.performer_id = @old_performer_id
   AND user_id NOT IN (
    SELECT user_id
    FROM performer_favorites PF
    WHERE PF.performer_id = @new_performer_id
  );

-- name: CreatePerformerFavorite :exec
INSERT INTO performer_favorites (performer_id, user_id, created_at) VALUES ($1, $2, now())
ON CONFLICT (performer_id, user_id) DO NOTHING;

-- name: DeletePerformerFavorite :exec
DELETE FROM performer_favorites WHERE performer_id = $1 AND user_id = $2;

-- name: FindPerformerFavoritesByIds :many
-- Check favorite status for multiple performers for a specific user
SELECT performer_id, (performer_id IS NOT NULL)::BOOLEAN as is_favorite
FROM performer_favorites
WHERE performer_id = ANY(sqlc.arg(performer_ids)::UUID[]) AND user_id = sqlc.arg(user_id);

-- Performer images

-- name: GetPerformerImages :many
SELECT images.* FROM images
JOIN performer_images ON performer_images.image_id = images.id
WHERE performer_images.performer_id = $1;

-- name: DeletePerformerImages :exec
DELETE FROM performer_images WHERE performer_id = $1;

-- name: CreatePerformerImages :copyfrom
INSERT INTO performer_images (performer_id, image_id) VALUES ($1, $2);

-- name: CreatePerformerAliases :copyfrom
INSERT INTO performer_aliases (performer_id, alias) VALUES ($1, $2);

-- name: CreatePerformerTattoos :copyfrom
INSERT INTO performer_tattoos (performer_id, location, description) VALUES ($1, $2, $3);

-- name: CreatePerformerPiercings :copyfrom
INSERT INTO performer_piercings (performer_id, location, description) VALUES ($1, $2, $3);

-- name: CreatePerformerURLs :copyfrom
INSERT INTO performer_urls (performer_id, url, site_id) VALUES ($1, $2, $3);

-- name: SetScenePerformerAlias :exec
UPDATE scene_performers
SET "as" = $2
WHERE performer_id = $1
AND "as" IS NULL;

-- name: ClearScenePerformerAlias :exec
UPDATE scene_performers
SET "as" = NULL
WHERE performer_id = $1
AND "as" = $2;

-- name: ReassignPerformerAliases :exec
UPDATE scene_performers
SET performer_id = @new_performer_id
WHERE scene_performers.performer_id = @old_performer_id
AND scene_id NOT IN (SELECT scene_id from scene_performers sp WHERE sp.performer_id = @new_performer_id);

-- name: DeletePerformerScenes :exec
DELETE FROM scene_performers WHERE performer_id = $1;

-- name: FindMergeIDsByPerformerIds :many
-- Find merge target IDs for performers (for merges where these are sources)
SELECT source_id as performer_id, target_id as merge_id FROM performer_redirects WHERE source_id = ANY(sqlc.arg(performer_ids)::UUID[]);

-- name: FindMergeIDsBySourcePerformerIds :many
-- Find merge source IDs for performers (for merges where these are targets)
SELECT target_id as performer_id, source_id as merge_id FROM performer_redirects WHERE target_id = ANY(sqlc.arg(performer_ids)::UUID[]);

-- name: FindPerformerAliasesByIds :many
-- Get aliases for multiple performers
SELECT performer_id, alias FROM performer_aliases WHERE performer_id = ANY(sqlc.arg(performer_ids)::UUID[]);

-- name: FindPerformerTattoosByIds :many
-- Get tattoos for multiple performers
SELECT performer_id, location, description FROM performer_tattoos WHERE performer_id = ANY(sqlc.arg(performer_ids)::UUID[]);

-- name: FindPerformerPiercingsByIds :many
-- Get piercings for multiple performers
SELECT performer_id, location, description FROM performer_piercings WHERE performer_id = ANY(sqlc.arg(performer_ids)::UUID[]);

-- name: FindPerformerUrlsByIds :many
-- Get URLs for multiple performers
SELECT performer_id, url, site_id FROM performer_urls WHERE performer_id = ANY(sqlc.arg(performer_ids)::UUID[]);

-- name: PerformerChangelog :many
-- Keyset-paginated feed of performers changed since (since, after_id), including
-- tombstones. redirect_to is the surviving performer for merged-away performers.
SELECT P.id, P.updated_at, P.deleted, R.target_id AS redirect_to
FROM performers P
LEFT JOIN performer_redirects R ON R.source_id = P.id
WHERE (P.updated_at, P.id) > (sqlc.arg('since')::timestamp, sqlc.arg('after_id')::uuid)
ORDER BY P.updated_at, P.id
LIMIT sqlc.arg('limit');

-- Performer timeline (growth item 21).

-- name: FindPerformerTimeline :many
-- A performer's appearance history, bucketed by YEAR.
--
-- Year-bucketed because that is the strongest timeline this schema supports: `scenes.date`
-- is the only date attached to an appearance. `performers` has career_start_year and
-- career_end_year, but those are a declared range on the performer, not per-scene rows --
-- there is no per-appearance credit line to build a finer timeline from.
--
-- `scenes.date` is TEXT, not a date type. Migration 01 declares it as `date` on a
-- different table; this one is free text, and the live values are 'YYYY-MM-DD', '--'
-- (the placeholder for an unknown date) and ''. So EXTRACT(YEAR ...) does not exist here
-- at all -- it errors with "function pg_catalog.extract(unknown, text) does not exist" --
-- and the year has to be pulled out with a regex.
--
-- The regex is ANCHORED (`^`) and the year is then range-checked, because a substring
-- match on free text is a guess: '19th century' would yield 19, and an unanchored match
-- would find digits anywhere in the string. Requiring four leading digits AND a year in
-- 1880..2100 turns a nonsense value into NULL -- counted as undated -- rather than a
-- bucket labelled year 19.
--
-- The same expression appears in the WHERE and the GROUP BY because it cannot be
-- referenced by alias inside GROUP BY, and repeating it lets the planner evaluate it once
-- per row rather than per group.
WITH dated AS (
  SELECT CASE
           WHEN substring(S.date from '^([0-9]{4})') IS NULL THEN NULL
           WHEN substring(S.date from '^([0-9]{4})')::int BETWEEN 1880 AND 2100
             THEN substring(S.date from '^([0-9]{4})')::int
           ELSE NULL
         END::int AS year
    FROM scene_performers SP
    JOIN scenes S ON S.id = SP.scene_id
   WHERE SP.performer_id = $1
     AND S.deleted = false
)
SELECT year, COUNT(*) AS scene_count
  FROM dated
 WHERE year IS NOT NULL
 GROUP BY year
 ORDER BY year ASC;

-- name: CountUndatedScenesByPerformer :one
-- Appearances whose scene has NO date, so they land in no timeline bucket.
--
-- Counted separately and on purpose. `scenes.date` is NULLABLE, so an undated appearance
-- cannot appear in any bucket -- and without this number the timeline silently
-- under-reports: a performer with 50 scenes of which 3 are dated renders as "3" and looks
-- like a nearly-unknown performer rather than one whose dates are unrecorded. The two
-- numbers together let a client say which of those it is looking at.
-- Every appearance whose year cannot be determined. NOT merely `date IS NULL`: the
-- column is text, so it is also '--', '', or anything that fails the year regex. Counting
-- only NULLs would report 0 undated for a performer whose dates are all placeholders --
-- which reads as "every appearance is dated" and is the opposite of the truth.
WITH dated AS (
  SELECT CASE
           WHEN substring(S.date from '^([0-9]{4})') IS NULL THEN NULL
           WHEN substring(S.date from '^([0-9]{4})')::int BETWEEN 1880 AND 2100
             THEN substring(S.date from '^([0-9]{4})')::int
           ELSE NULL
         END::int AS year
    FROM scene_performers SP
    JOIN scenes S ON S.id = SP.scene_id
   WHERE SP.performer_id = $1
     AND S.deleted = false
)
SELECT COUNT(*) FROM dated WHERE year IS NULL;
