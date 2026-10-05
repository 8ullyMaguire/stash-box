-- Seeds REAL co-occurrence into the live database, because "it works" is not the same
-- as "it can be seen working".
--
-- The live instance had 8 performers, 9 scenes and 6 scene_performers -- not one pair
-- of performers sharing a scene. So every one of the new fields would have returned
-- an empty list on the deployed server, and an empty list is indistinguishable from
-- the two bugs this milestone actually fixed: the inner join that dropped candidates,
-- and the unscoped count that counted the whole archive. A reviewer opening the live
-- site would have seen "no similar performers" and concluded the feature was broken.
--
-- The cast is arranged so the ranking has a checkable expected order:
--
--   BEN     shares 3 of ANA's 5 scenes, with 3 distinct third parties -> 0.96
--   CLEO    shares 2 of ANA's 5 scenes, with 1 third party            -> 0.48
--   DAVE    appears once, alone: shares nothing, so never returned
--
-- So the live instance must return BEN then CLEO for ANA, and must NOT return ANA
-- herself or DAVE. I first wrote this expecting CLEO to fall below the floor of 2;
-- she shares two scenes (one with BEN, one with ANA alone), so she legitimately
-- clears it and lands second. Asserted in scripts/verify-similar-live.sh.
--
-- Uses a fixed id range (...0050) disjoint from the fixture's, so this can be applied
-- alongside scripts/verify-similar.sql without either one deleting the other's rows.
\set ON_ERROR_STOP on

BEGIN;

DELETE FROM scene_performers
 WHERE scene_id::text LIKE 'f0000000-0000-0000-0000-00000000005%'
    OR performer_id::text LIKE 'f0000000-0000-0000-0000-00000000005%';
DELETE FROM scenes
 WHERE id::text LIKE 'f0000000-0000-0000-0000-00000000005%';
DELETE FROM performers
 WHERE id::text LIKE 'f0000000-0000-0000-0000-00000000005%';

INSERT INTO studios (id, name, created_at, updated_at)
VALUES ('f0000000-0000-0000-0000-000000000050', 'Live Similarity Studio', now(), now())
ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name;

INSERT INTO performers (id, name, gender, created_at, updated_at) VALUES
  ('f0000000-0000-0000-0000-000000000051', 'ANA',  'FEMALE', now(), now()),
  ('f0000000-0000-0000-0000-000000000052', 'BEN',  'MALE',   now(), now()),
  ('f0000000-0000-0000-0000-000000000053', 'CLEO', 'FEMALE', now(), now()),
  ('f0000000-0000-0000-0000-000000000054', 'DAVE', 'MALE',   now(), now()),
  ('f0000000-0000-0000-0000-000000000055', 'ELI',  'MALE',   now(), now()),
  ('f0000000-0000-0000-0000-000000000056', 'FIA',  'FEMALE', now(), now())
ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name;

-- ANA and BEN appear together in three scenes; each with a different third party, so
-- their co-performer count is 3 -- the recurring-circle case the score rewards.
INSERT INTO scenes (id, title, studio_id, date, created_at, updated_at) VALUES
  ('f0000000-0000-0000-0000-000000000501', 'ANA and BEN I',   'f0000000-0000-0000-0000-000000000050', '2021-01-05', now(), now()),
  ('f0000000-0000-0000-0000-000000000502', 'ANA and BEN II',  'f0000000-0000-0000-0000-000000000050', '2021-02-05', now(), now()),
  ('f0000000-0000-0000-0000-000000000503', 'ANA and BEN III', 'f0000000-0000-0000-0000-000000000050', '2021-03-05', now(), now())
ON CONFLICT (id) DO UPDATE SET title = EXCLUDED.title;

-- ANA's fourth scene, alone. It is what makes target_scenes 4 rather than 3, so the
-- score is 3/4 of the maximum rather than a perfect 1.0 -- a ranking of all-ones is
-- indistinguishable from a broken ranking.
INSERT INTO scenes (id, title, studio_id, date, created_at, updated_at) VALUES
  ('f0000000-0000-0000-0000-000000000504', 'ANA solo', 'f0000000-0000-0000-0000-000000000050', '2021-04-05', now(), now())
ON CONFLICT (id) DO UPDATE SET title = EXCLUDED.title;

-- CLEO meets ANA exactly once: one shared scene, below the floor of 2.
INSERT INTO scenes (id, title, studio_id, date, created_at, updated_at) VALUES
  ('f0000000-0000-0000-0000-000000000505', 'ANA meets CLEO', 'f0000000-0000-0000-0000-000000000050', '2021-05-05', now(), now()),
  ('f0000000-0000-0000-0000-000000000506', 'CLEO solo',      'f0000000-0000-0000-0000-000000000050', '2021-06-05', now(), now())
ON CONFLICT (id) DO UPDATE SET title = EXCLUDED.title;

-- DAVE appears once, with nobody. Shares nothing with anybody.
INSERT INTO scenes (id, title, studio_id, date, created_at, updated_at) VALUES
  ('f0000000-0000-0000-0000-000000000507', 'DAVE alone', 'f0000000-0000-0000-0000-000000000050', '2021-07-05', now(), now())
ON CONFLICT (id) DO UPDATE SET title = EXCLUDED.title;

-- The three shared scenes: ANA + BEN + one distinct third party each.
INSERT INTO scene_performers (scene_id, performer_id) VALUES
  ('f0000000-0000-0000-0000-000000000501', 'f0000000-0000-0000-0000-000000000051'),  -- ANA
  ('f0000000-0000-0000-0000-000000000501', 'f0000000-0000-0000-0000-000000000052'),  -- BEN
  ('f0000000-0000-0000-0000-000000000501', 'f0000000-0000-0000-0000-000000000055'),  -- ELI
  ('f0000000-0000-0000-0000-000000000502', 'f0000000-0000-0000-0000-000000000051'),
  ('f0000000-0000-0000-0000-000000000502', 'f0000000-0000-0000-0000-000000000052'),
  ('f0000000-0000-0000-0000-000000000502', 'f0000000-0000-0000-0000-000000000056'),  -- FIA
  ('f0000000-0000-0000-0000-000000000503', 'f0000000-0000-0000-0000-000000000051'),
  ('f0000000-0000-0000-0000-000000000503', 'f0000000-0000-0000-0000-000000000052'),
  ('f0000000-0000-0000-0000-000000000503', 'f0000000-0000-0000-0000-000000000053')   -- CLEO
ON CONFLICT DO NOTHING;

-- ANA alone, and the single ANA/CLEO meeting.
INSERT INTO scene_performers (scene_id, performer_id) VALUES
  ('f0000000-0000-0000-0000-000000000504', 'f0000000-0000-0000-0000-000000000051'),
  ('f0000000-0000-0000-0000-000000000505', 'f0000000-0000-0000-0000-000000000051'),
  ('f0000000-0000-0000-0000-000000000505', 'f0000000-0000-0000-0000-000000000053'),
  ('f0000000-0000-0000-0000-000000000506', 'f0000000-0000-0000-0000-000000000053'),
  ('f0000000-0000-0000-0000-000000000507', 'f0000000-0000-0000-0000-000000000054')
ON CONFLICT DO NOTHING;

COMMIT;