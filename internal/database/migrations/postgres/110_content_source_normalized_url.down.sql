-- Teardown for 110. Nothing but the column and its index is created, and no data is
-- destroyed that the row's `url` cannot reconstruct: the normalised key is derivable
-- from `url` by the Go normaliser, which is exactly why it is safe to drop.
--
-- (That is not true of every derived column -- `scenes.origin` in 109 is not derivable
-- from anything, which is why its teardown says so.)
DROP INDEX IF EXISTS "content_sources_url_normalized_idx";

ALTER TABLE "content_sources" DROP COLUMN IF EXISTS "url_normalized";
