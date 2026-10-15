-- A feed URL is a BEARER of access (plan, slice 13b): a feed reader sends no headers, so the credential
-- has to be in the URL, and a URL ends up in a reader's config file, in a proxy's log and in the
-- `Referer` of every link a reader clicks out of it.
--
-- THREE DECISIONS LIVE IN THIS COLUMN'S SHAPE.
--
-- 1. ONLY THE HASH IS STORED, so a database dump does not hand out feeds, and "we cannot show you that
--    URL again" is a FACT rather than a policy the schema contradicts. The URL is returned once, by the
--    mutation that mints it.
-- 2. ONE TOKEN PER SAVED SEARCH, not the member's `users.api_key`. A leaked feed URL costs one search
--    rather than an account, and an API key is a header credential with the member's whole role
--    authority -- putting it in a URL hands a password to a log file.
-- 3. NULL MEANS NO FEED, and that is the whole switch: there is no separate boolean to disagree with it.
--    Disabling clears the hash, and the URL that named it is dead. Enabling again mints a NEW token, so
--    a member who pasted their feed into a chat has one move that ends the exposure.
ALTER TABLE saved_searches ADD COLUMN IF NOT EXISTS feed_token_hash text;

-- A feed request looks a token up and nothing else. UNIQUE because two searches sharing a token would
-- make the lookup ambiguous (which one did the reader ask for?), and PARTIAL because most rows have no
-- feed at all and a NULL is not a value anyone looks up -- a plain unique index would store one entry
-- per unfed search forever and buy nothing.
CREATE UNIQUE INDEX IF NOT EXISTS saved_searches_feed_token_hash_idx
  ON saved_searches (feed_token_hash) WHERE feed_token_hash IS NOT NULL;

COMMENT ON COLUMN saved_searches.feed_token_hash IS
  'SHA-256 (hex) of the token in this search''s feed URL, or NULL for no feed. Only the hash is stored: a dump of this table does not hand out feeds, and the URL is shown once when it is minted (plan slice 13b).';
