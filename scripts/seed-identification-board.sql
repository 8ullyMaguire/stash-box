-- Seed one open identification query with two candidates, for live browser
-- verification of the board. Idempotent: re-running updates the same rows.
--
-- The candidate's entity is a performer that exists, so the board's entity link
-- can be checked live, and one candidate deliberately points at a scene id that
-- does not exist, so the "no longer exists" branch is exercised in the real app
-- rather than only in a test.
BEGIN;

INSERT INTO performers (id, name, disambiguation, created_at, updated_at)
VALUES ('11111111-1111-1111-1111-111111111111', 'Live Verify Performer', 'browser check', now(), now())
ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name;

INSERT INTO identification_queries
  (id, target_type, description, status, created_at, updated_at)
VALUES
  ('22222222-2222-2222-2222-222222222222', 'performer',
   'hotel room, rainy night, shot on a wide lens around 2016', 'open', now(), now())
ON CONFLICT (id) DO UPDATE SET description = EXCLUDED.description, status = 'open';

-- identification_candidates has no updated_at column (verified with \d), and
-- the unique constraint is (query_id, entity_type, entity_id), so ON CONFLICT
-- targets the pair rather than the id.
INSERT INTO identification_candidates
  (id, query_id, entity_type, entity_id, note, suggested_by, created_at)
VALUES
  ('33333333-3333-3333-3333-333333333333', '22222222-2222-2222-2222-222222222222',
   'performer', '11111111-1111-1111-1111-111111111111',
   'the studio watermark is visible in frame 3',
   (SELECT id FROM users ORDER BY id LIMIT 1), now()),
  ('44444444-4444-4444-4444-444444444444', '22222222-2222-2222-2222-222222222222',
   'scene', '99999999-9999-9999-9999-999999999999',
   'this is the one I think it is',
   (SELECT id FROM users ORDER BY id LIMIT 1), now())
ON CONFLICT (query_id, entity_type, entity_id) DO UPDATE SET note = EXCLUDED.note;

-- Give the first candidate three votes so the tally is not zero, which is the
-- case the board renders differently (singular vs plural).
INSERT INTO identification_candidate_votes (candidate_id, user_id, created_at)
SELECT '33333333-3333-3333-3333-333333333333', id, now() FROM users
ON CONFLICT DO NOTHING;

COMMIT;
