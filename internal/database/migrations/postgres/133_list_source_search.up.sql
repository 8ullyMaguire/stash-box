-- A list whose entries come from a saved search (plan phase 8 slice 8d: the auto-updating list).
--
-- A DERIVED LIST STORES NO ENTRIES. Membership is computed from the search on every read, so a scene
-- that matches appears in the list without anything being written and without a sweep job to keep the
-- two in step. The alternative -- a snapshot refreshed on a schedule -- has a window in which the list
-- and its search disagree, and that window is the whole feature: a reader who saves "unwatched anal"
-- and then opens the list expects the list to be what the search says now.
--
-- THE FILTER IS NOT STORED HERE, and this column is not a second copy of it. It names the search whose
-- TEXT stays canonical (docs/spec/feature-discovery.md section 3.2): the text is re-parsed on every
-- run, so a tag merged away since the search was written still resolves through the redirect fallback
-- rather than being frozen into this row.
--
-- ON DELETE RESTRICT, NOT CASCADE, and not SET NULL:
--
--   * CASCADE would delete a member's list because they deleted a saved search. Nobody asked for that,
--     and the list is the artefact they curated.
--   * SET NULL would leave a list that LOOKS like an ordinary empty list. The membership would vanish
--     with no trace, and "my list is empty" is exactly what the owner would report as a bug -- the
--     derived state is destroyed silently, which is the failure mode the whole feature is written to
--     avoid.
--   * RESTRICT refuses, and the error names the list. The owner deletes the list, or clears its source
--     first, and both of those are decisions they get to make.
ALTER TABLE "lists"
    ADD COLUMN "source_saved_search_id" UUID REFERENCES "saved_searches"("id") ON DELETE RESTRICT;

-- Following a list up to its search. Partial, because almost every list is an ordinary one and a full
-- index would be mostly dead weight -- the same reasoning as saved_searches_notifying_idx.
CREATE INDEX "lists_source_saved_search_idx"
    ON "lists" ("source_saved_search_id") WHERE "source_saved_search_id" IS NOT NULL;

COMMENT ON COLUMN "lists"."source_saved_search_id" IS
    'The saved search this list derives its entries from, or NULL for an ordinary list. A derived list stores no list_items rows; deleting a search a list follows is RESTRICTed.';
