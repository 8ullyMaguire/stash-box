-- Fixture for the similar-performers query (growth item 27).
--
-- The scoring is the whole of this feature, and a scoring function that is merely
-- plausible is worse than none: it recommends confidently and wrongly. So this builds
-- a fixture whose CORRECT order is derivable by hand, and the runner asserts the
-- query produces it.
--
-- Written as SQL rather than as concatenated psql calls because six bugs came from
-- doing it that way:
--
--   * a LATERAL with no scope filter, which attached the subject to every OTHER
--     seed's scenes;
--   * a `WHERE` wedged between `FROM` and `CROSS JOIN`, truncating the join;
--   * `gen_random_uuid()` + `ON CONFLICT DO NOTHING`, which can never conflict and so
--     accumulated ten scenes per run;
--   * `round(double precision, integer)`, which does not exist;
--   * a psql call piped to /dev/null without checking its exit status, so a fixture
--     that failed to apply still reported success;
--   * and OVERLAPPING ID RANGES: the shared block and the three single-scene cases
--     were both built on the ...0001 series, so a prefix matching one matched the
--     other. Four casts per special scene instead of three, and a subject denominator
--     of 22 instead of 13.
--
-- Every one of those produced a *plausible* ranking, which is why reading the output
-- was not enough. Only an explicit isolation assertion caught the last one.
--
-- ID RANGES, disjoint by construction:
--   ...0010 01..10  ten shared scenes: SUBJECT + CLOSE + CROWD
--   ...0030 01..03  three single-scene cases, each shaped differently
--   ...0020 01..20  OUTSIDER's scenes, sharing nothing with SUBJECT
--
-- SUBJECT therefore has 13 scenes: 10 shared + loose + solo + ghost.
\set ON_ERROR_STOP on

BEGIN;

DELETE FROM scene_performers
 WHERE scene_id::text LIKE 'f0000000-0000-0000-0000-000000000%'
    OR performer_id::text LIKE 'f0000000-0000-0000-0000-000000000%';
DELETE FROM scenes    WHERE id::text LIKE 'f0000000-0000-0000-0000-000000000%';
DELETE FROM performers WHERE id::text LIKE 'f0000000-0000-0000-0000-000000000%';

INSERT INTO studios (id, name, created_at, updated_at)
VALUES ('f0000000-0000-0000-0000-000000000001', 'Similarity Fixture', now(), now())
ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name;

INSERT INTO performers (id, name, gender, birthdate, created_at, updated_at) VALUES
  ('f0000000-0000-0000-0000-00000000000a', 'SUBJECT',  'FEMALE', '1990-01-01', now(), now()),
  ('f0000000-0000-0000-0000-00000000000b', 'CLOSE',    'FEMALE', '1991-01-01', now(), now()),
  ('f0000000-0000-0000-0000-000000000010', 'CROWD',    'FEMALE', '1980-01-01', now(), now()),
  ('f0000000-0000-0000-0000-00000000000c', 'LOOSE',    'MALE',   '1992-01-01', now(), now()),
  ('f0000000-0000-0000-0000-00000000000e', 'SOLO',     'FEMALE', '1993-01-01', now(), now()),
  ('f0000000-0000-0000-0000-00000000000f', 'GHOST',    'MALE',   '1988-01-01', now(), now()),
  ('f0000000-0000-0000-0000-000000000020', 'OUTSIDER', 'MALE',   '1975-01-01', now(), now())
ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name;

-- GHOST is soft-deleted, which is the entire point of it: it co-appears with
-- SUBJECT in a scene and must still never be recommended, because recommending it
-- would break the list's own links.
UPDATE performers SET deleted = true WHERE id = 'f0000000-0000-0000-0000-00000000000f';

-- The ten shared scenes. Deterministic ids, so re-runs are exact.
INSERT INTO scenes (id, title, studio_id, date, created_at, updated_at)
SELECT ('f0000000-0000-0000-0000-0000000001' || lpad(g::text, 2, '0'))::uuid,
       'Shared ' || g,
       'f0000000-0000-0000-0000-000000000001', '2020-01-01', now(), now()
FROM generate_series(1, 10) AS g
ON CONFLICT (id) DO UPDATE SET title = EXCLUDED.title;

-- The three single-scene cases, in their own disjoint id series.
INSERT INTO scenes (id, title, studio_id, date, created_at, updated_at) VALUES
  ('f0000000-0000-0000-0000-000000000301', 'Loose Scene', 'f0000000-0000-0000-0000-000000000001', '2020-06-01', now(), now()),
  ('f0000000-0000-0000-0000-000000000302', 'Solo Scene',  'f0000000-0000-0000-0000-000000000001', '2020-07-01', now(), now()),
  ('f0000000-0000-0000-0000-000000000303', 'Ghost Scene', 'f0000000-0000-0000-0000-000000000001', '2020-08-01', now(), now())
ON CONFLICT (id) DO UPDATE SET title = EXCLUDED.title;

-- OUTSIDER's twenty scenes: prolific, and sharing nothing with SUBJECT. A performer
-- who shares no scenes with the subject must never be recommended however many
-- scenes they have -- otherwise the list is just "the prolific".
INSERT INTO scenes (id, title, studio_id, date, created_at, updated_at)
SELECT ('f0000000-0000-0000-0000-0000000002' || lpad(g::text, 2, '0'))::uuid,
       'Outsider ' || g,
       'f0000000-0000-0000-0000-000000000001', '2019-01-01', now(), now()
FROM generate_series(1, 20) AS g
ON CONFLICT (id) DO UPDATE SET title = EXCLUDED.title;

-- The shared block only, matched on its own ...0010 series. Because the three
-- single-scene cases live in ...0030, this prefix cannot reach them.
INSERT INTO scene_performers (scene_id, performer_id)
SELECT s.id, v.pid
FROM scenes s
CROSS JOIN LATERAL (VALUES
  ('f0000000-0000-0000-0000-00000000000a'::uuid),   -- SUBJECT
  ('f0000000-0000-0000-0000-000000000010'::uuid),   -- CROWD
  ('f0000000-0000-0000-0000-00000000000b'::uuid)    -- CLOSE
) AS v(pid)
WHERE s.id::text LIKE 'f0000000-0000-0000-0000-0000000001%'
ON CONFLICT DO NOTHING;

-- Loose: SUBJECT + LOOSE + CROWD. One shared scene, one co-performer.
INSERT INTO scene_performers (scene_id, performer_id) VALUES
  ('f0000000-0000-0000-0000-000000000301', 'f0000000-0000-0000-0000-00000000000a'),
  ('f0000000-0000-0000-0000-000000000301', 'f0000000-0000-0000-0000-00000000000c'),
  ('f0000000-0000-0000-0000-000000000301', 'f0000000-0000-0000-0000-000000000010')
ON CONFLICT DO NOTHING;

-- Solo: SUBJECT + SOLO alone, no third party at all.
INSERT INTO scene_performers (scene_id, performer_id) VALUES
  ('f0000000-0000-0000-0000-000000000302', 'f0000000-0000-0000-0000-00000000000a'),
  ('f0000000-0000-0000-0000-000000000302', 'f0000000-0000-0000-0000-00000000000e')
ON CONFLICT DO NOTHING;

-- Ghost: SUBJECT + GHOST + CROWD, but GHOST is soft-deleted.
INSERT INTO scene_performers (scene_id, performer_id) VALUES
  ('f0000000-0000-0000-0000-000000000303', 'f0000000-0000-0000-0000-00000000000a'),
  ('f0000000-0000-0000-0000-000000000303', 'f0000000-0000-0000-0000-00000000000f'),
  ('f0000000-0000-0000-0000-000000000303', 'f0000000-0000-0000-0000-000000000010')
ON CONFLICT DO NOTHING;

-- OUTSIDER alone in each of theirs.
INSERT INTO scene_performers (scene_id, performer_id)
SELECT s.id, 'f0000000-0000-0000-0000-000000000020'::uuid
FROM scenes s
WHERE s.id::text LIKE 'f0000000-0000-0000-0000-0000000002%'
ON CONFLICT DO NOTHING;

COMMIT;