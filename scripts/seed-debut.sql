-- Seed performers with distinct scene histories so sort ordering is observable.
--
-- Item 57 (debut tracking) is marked "no — min(date)" in the tracker, but
-- PerformerSortEnum.DEBUT already exists in the schema and has an integration
-- test. The claim is wrong; what it needs is verification that the ordering is
-- actually observable, which needs performers whose first scenes differ.
--
-- The debut spread is the point: debut_year is the MINIMUM of the performer's
-- scene dates, so three performers need three different minima to tell "sorted by
-- debut" from "sorted by anything else". One performer with no scenes at all
-- tests the NULL case, which is where a naive ORDER BY puts NULLs first and
-- claims an unknown debut is the earliest career.
--
-- Idempotent.
BEGIN;

-- Two studios for the scenes to hang off.
INSERT INTO studios (id, name, created_at, updated_at)
VALUES
  ('b1111111-1111-1111-1111-111111111111', 'Debut Studio A', now(), now()),
  ('b2222222-2222-2222-2222-222222222222', 'Debut Studio B', now(), now())
ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name;

-- Performers. birthdate is deliberately in a different order from career_start_year
-- so a DEBUT sort that secretly used birthdate would be visible.
INSERT INTO performers (id, name, disambiguation, birthdate, gender, career_start_year, created_at, updated_at)
VALUES
  -- Debuts in 2019: two scenes, the earlier in January.
  ('d1111111-1111-1111-1111-111111111111', 'Debut Ada',      NULL, '1985-03-02', 'FEMALE', 2019, now(), now()),
  -- Debut in 2021, but with an OLDER birthdate than Ada -- so birthdate order and
  -- debut order disagree, and a birthdate-based sort is detectable.
  ('d2222222-2222-2222-2222-222222222222', 'Debut Bo',       NULL, '1980-07-14', 'MALE',   2021, now(), now()),
  -- Debut in 2016, the earliest, and the most scenes.
  ('d3333333-3333-3333-3333-333333333333', 'Debut Cy',       NULL, '1992-11-30', 'FEMALE', 2016, now(), now()),
  -- No scenes at all: debut is UNKNOWN, not 2016.
  ('d4444444-4444-4444-4444-444444444444', 'Debut Nobody',   NULL, '1990-01-01', 'MALE',   2020, now(), now())
ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name;

-- Scenes. Dates are what debut_year aggregates over.
INSERT INTO scenes (id, title, studio_id, date, created_at, updated_at)
VALUES
  ('e1111111-1111-1111-1111-111111111111', 'Ada 2019-01-15', 'b1111111-1111-1111-1111-111111111111', '2019-01-15', now(), now()),
  ('e2222222-2222-2222-2222-222222222222', 'Ada 2019-09-02', 'b1111111-1111-1111-1111-111111111111', '2019-09-02', now(), now()),
  ('e3333333-3333-3333-3333-333333333333', 'Bo  2021-06-20', 'b2222222-2222-2222-2222-222222222222', '2021-06-20', now(), now()),
  ('e4444444-4444-4444-4444-444444444444', 'Cy  2016-04-01', 'b2222222-2222-2222-2222-222222222222', '2016-04-01', now(), now()),
  ('e5555555-5555-5555-5555-555555555555', 'Cy  2018-02-11', 'b2222222-2222-2222-2222-222222222222', '2018-02-11', now(), now()),
  ('e6666666-6666-6666-6666-666666666666', 'Cy  2022-08-08', 'b2222222-2222-2222-2222-222222222222', '2022-08-08', now(), now())
ON CONFLICT (id) DO UPDATE SET title = EXCLUDED.title;

-- The join that makes debut observable: performers reach scenes through
-- scene_performers, with the scene carrying the date.
INSERT INTO scene_performers (scene_id, performer_id)
VALUES
  ('e1111111-1111-1111-1111-111111111111', 'd1111111-1111-1111-1111-111111111111'),
  ('e2222222-2222-2222-2222-222222222222', 'd1111111-1111-1111-1111-111111111111'),
  ('e3333333-3333-3333-3333-333333333333', 'd2222222-2222-2222-2222-222222222222'),
  ('e4444444-4444-4444-4444-444444444444', 'd3333333-3333-3333-3333-333333333333'),
  ('e5555555-5555-5555-5555-555555555555', 'd3333333-3333-3333-3333-333333333333'),
  ('e6666666-6666-6666-6666-666666666666', 'd3333333-3333-3333-3333-333333333333')
ON CONFLICT DO NOTHING;

-- NOTE: there is no performer-to-studio relationship in this schema. No
-- studio_performers table, no performers.studio_id column -- checked
-- information_schema for every column named %studio% and none links the two. A
-- performer reaches a studio only INDIRECTLY, through the scenes they appear in
-- (scenes.studio_id -> scene_performers.performer_id). So "similar performers"
-- cannot lean on a shared studio, and neither can anything else here.

COMMIT;