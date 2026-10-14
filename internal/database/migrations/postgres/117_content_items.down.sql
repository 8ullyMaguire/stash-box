-- Reverses 117: the content-item tables and nothing else.
--
-- Child tables first, because their foreign keys reference content_items. There is no data
-- migration to reverse: this migration created tables and populated none (phase 10 fills
-- content_item_media, and nothing writes content_items until phase 3's own service lands).
DROP TABLE IF EXISTS "content_item_tags";
DROP TABLE IF EXISTS "content_item_media";
DROP TABLE IF EXISTS "content_item_chapters";
DROP TABLE IF EXISTS "content_items";
