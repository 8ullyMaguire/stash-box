-- Down for 113.
--
-- The enum value is NOT removed: Postgres has no `ALTER TYPE ... DROP VALUE`, and recreating
-- the type would mean rewriting every row of `notifications` that used it. An unused enum
-- value is inert; a half-rewritten notifications table is not. Said here so the asymmetry is
-- a decision rather than an omission.
DROP INDEX IF EXISTS saved_searches_notifying_idx;
DROP INDEX IF EXISTS saved_searches_user_updated_idx;
DROP TABLE IF EXISTS saved_searches;
