-- The edit -> item join gets a primary key.
--
-- Migration 118 mirrored `tag_edits`, which has no key, and that leaves the item's edit
-- history able to list the same edit twice: `CreateJoin` is called for a modify or destroy,
-- the apply writes a join row for a create, and a re-submitted edit can reach both. A
-- duplicate in a HISTORY is worse than a duplicate elsewhere -- the page says an item was
-- changed twice when it was changed once.
--
-- The statement is `ON CONFLICT DO NOTHING`, so the key is what makes re-linking an
-- idempotent operation rather than an error.
ALTER TABLE "content_item_edits"
  ADD CONSTRAINT "content_item_edits_pkey" PRIMARY KEY ("edit_id", "content_item_id");
