-- Seed Elo state for live verification of the leaderboard.
--
-- elo_votes is NOT per-entity: it records PAIRWISE matchups (winner_id/loser_id,
-- picked_side, weight), verified with \d before writing this. The first draft of
-- this seed assumed a per-entity shape and would have failed on the first
-- insert, which is the cheaper way to learn it.
--
-- So: three performers, three ratings, and a vote COUNT that comes from real
-- matchup rows. The count is what the page exists to show -- a rating is a mean
-- over the votes cast, so a 3-vote rating and a 43-vote rating can differ by a
-- point while meaning very different things. Seeding one entry, or three with
-- equal counts, would not exercise that.
--
-- The well-observed performer wins most of its matchups; the barely-rated one
-- wins almost none, which is what produces a wide deviation and the visible
-- "this number is not well established" story.
--
-- Idempotent: re-running replaces the seeded rows.
BEGIN;

INSERT INTO performers (id, name, disambiguation, created_at, updated_at)
VALUES
  ('a1111111-1111-1111-1111-111111111111', 'Well Observed Performer', 'many votes', now(), now()),
  ('a2222222-2222-2222-2222-222222222222', 'Barely Rated Performer', 'three votes', now(), now()),
  ('a3333333-3333-3333-3333-333333333333', 'Mid Pack Performer', 'seventeen votes', now(), now())
ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name;

DELETE FROM elo_votes WHERE winner_id IN
  ('a1111111-1111-1111-1111-111111111111','a2222222-2222-2222-2222-222222222222','a3333333-3333-3333-3333-333333333333')
   OR loser_id IN
  ('a1111111-1111-1111-1111-111111111111','a2222222-2222-2222-2222-222222222222','a3333333-3333-3333-3333-333333333333');

-- 40 matchups the well-observed performer wins, 3 the barely-rated one wins.
INSERT INTO elo_votes (id, user_id, winner_id, loser_id, winner_type, loser_type, picked_side, weight, created_at)
SELECT gen_random_uuid(), u.id,
       CASE WHEN n <= 40 THEN 'a1111111-1111-1111-1111-111111111111'::uuid
            WHEN n <= 43 THEN 'a2222222-2222-2222-2222-222222222222'::uuid
            ELSE 'a3333333-3333-3333-3333-333333333333'::uuid END,
       CASE WHEN n <= 40 THEN 'a3333333-3333-3333-3333-333333333333'::uuid
            WHEN n <= 43 THEN 'a1111111-1111-1111-1111-111111111111'::uuid
            ELSE 'a1111111-1111-1111-1111-111111111111'::uuid END,
       'performer', 'performer', 1, 1.0, now() - (60 - n) * interval '1 hour'
FROM users u, generate_series(1, 60) AS g(n)
WHERE u.name = 'liveverify';

INSERT INTO elo_ratings (entity_type, entity_id, rating, deviation, volatility, last_rated_at)
VALUES
  ('performer', 'a1111111-1111-1111-1111-111111111111', 1580, 18,  0.02, now()),
  ('performer', 'a2222222-2222-2222-2222-222222222222', 1502, 240, 0.20, now()),
  ('performer', 'a3333333-3333-3333-3333-333333333333', 1512, 70,  0.06, now())
ON CONFLICT (entity_type, entity_id) DO UPDATE
  SET rating = EXCLUDED.rating,
      deviation = EXCLUDED.deviation,
      volatility = EXCLUDED.volatility;

COMMIT;
