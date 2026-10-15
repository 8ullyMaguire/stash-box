-- The feed token goes away with the feature that uses it. Dropping the column drops the index with it.
ALTER TABLE saved_searches DROP COLUMN IF EXISTS feed_token_hash;
