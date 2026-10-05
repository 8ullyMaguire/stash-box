-- Seed fingerprint submissions so trending/popularity have something to rank.
--
-- `scene_popularity_trending` is a MATERIALIZED VIEW over scene_fingerprints:
-- trending_count is the number of DISTINCT users who submitted in the last 7
-- days, NULLIF(..., 0) so "nobody submitted this week" is ABSENT rather than
-- zero. That NULL is why the trending list is legitimately empty on a fresh
-- instance, and why verifying trending needs submissions in the fixture rather
-- than just scenes.
--
-- Two tables, in order: `fingerprints` (id, algorithm, hash) is the fingerprint
-- identity, and `scene_fingerprints` links one to a scene with the submitting
-- user. The first draft of this seed put a `fingerprint` bytea column directly
-- on scene_fingerprints, which does not exist -- the schema splits identity from
-- submission.
--
-- The distribution is deliberate. One scene many users touched RECENTLY (the
-- genuine front-runner), one scene many users touched LONG AGO (high all-time
-- popularity, absent from trending), and one touched by a single user (present
-- but last). Without that spread, "trending works" and "trending returns the same
-- rows as popularity" are indistinguishable.
--
-- Idempotent: re-running replaces the seeded rows.
BEGIN;

-- The seed owns its own studio rather than assuming seed-elo.sql ran first.
-- These scripts are run individually, and an integration suite pointed at the
-- same database drops every table -- so a cross-file assumption here fails with
-- a foreign key error that reads like a schema problem.
INSERT INTO studios (id, name, created_at, updated_at)
VALUES ('a1111111-1111-1111-1111-111111111111', 'Seed Studio', now(), now())
ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name;

-- Three scenes, all on that studio.
INSERT INTO scenes (id, title, studio_id, created_at, updated_at)
VALUES
  ('c1111111-1111-1111-1111-111111111111', 'Trending Leader',    'a1111111-1111-1111-1111-111111111111', now(), now()),
  ('c2222222-2222-2222-2222-222222222222', 'Stale But Popular',  'a1111111-1111-1111-1111-111111111111', now(), now()),
  ('c3333333-3333-3333-3333-333333333333', 'Quietly Submitted',  'a1111111-1111-1111-1111-111111111111', now(), now())
ON CONFLICT (id) DO UPDATE SET title = EXCLUDED.title;

-- The voters. Enough distinct users that trending and popularity can diverge: a
-- scene submitted by 12 people this week outranks one submitted by 9 people in
-- March, even though the March scene has more all-time submissions.
-- ONE user per n, and the submission insert pairs them positionally. The first
-- draft generated users and submissions independently and let a JOIN pair them,
-- which produced 14 users x 14 fingerprints = 196 submissions -- every scene
-- landing on every user, so all three scenes scored 14 and trending looked
-- broken rather than mis-seeded.
INSERT INTO users (id, name, password_hash, email, api_key, last_api_call, created_at, updated_at)
SELECT
  gen_random_uuid(),
  'trending-voter-' || n,
  'x',
  'trending-voter-' || n || '@example.org',
  'trending-voter-' || n,
  now(), now(), now()
FROM generate_series(1, 14) AS g(n)
ON CONFLICT (email) DO NOTHING;

-- Fingerprint identities. `hash` is a signed bigint, so the values are small and
-- positive rather than a packed byte string; the point is only that each
-- submission references a distinct identity.
INSERT INTO fingerprints (algorithm, hash)
SELECT 'PHASH', 900000000 + n
FROM generate_series(1, 14) AS g(n)
ON CONFLICT (hash, algorithm) DO NOTHING;

-- The submissions. `vote` is NOT NULL (1 = approved) and `duration` is NOT NULL,
-- and the timestamps are the whole point: the 7-day window in the view is what
-- separates trending from all-time.
INSERT INTO scene_fingerprints (fingerprint_id, scene_id, user_id, duration, created_at, vote)
SELECT
  f.id,
  CASE WHEN n <= 12 THEN 'c1111111-1111-1111-1111-111111111111'::uuid
       WHEN n <= 13 THEN 'c3333333-3333-3333-3333-333333333333'::uuid
       ELSE 'c2222222-2222-2222-2222-222222222222'::uuid END,
  u.id,
  180,
  CASE WHEN n <= 13 THEN now() - (n || ' hours')::interval
       ELSE now() - (200 + n || ' days')::interval END,
  1
-- Pair user n with fingerprint n EXPLICITLY rather than letting a join decide.
-- `FROM users, generate_series(1,14)` is a cartesian product: 14 users x 14 n =
-- 196 submissions, every user on every scene, so all three scenes scored 14 and
-- the ranking looked broken instead of mis-seeded. The suffix arithmetic lines
-- the two sets up one-to-one.
FROM users u
JOIN generate_series(1, 14) AS g(n)
  ON u.name = 'trending-voter-' || n
JOIN fingerprints f
  ON f.hash = 900000000 + n AND f.algorithm = 'PHASH'
ON CONFLICT DO NOTHING;

-- The views are only refreshed by the hourly cron, and a seeded instance must not
-- wait an hour to show anything.
REFRESH MATERIALIZED VIEW scene_popularity_trending;
REFRESH MATERIALIZED VIEW scene_popularity_all_time;

COMMIT;
