-- Typed edit comments (plan phase 8 slice 8a, NexusXXX section 9).
--
-- A comment was free text with a UUID in it. It is now TYPED: the kind says what the writer is
-- doing -- discussing, offering a source, identifying a performer, pointing at a timestamp,
-- proposing a correction, asking for something, reviewing -- and the kind decides whether the
-- comment carries a STRUCTURED FIELD rather than making the reader parse prose.
--
-- The vocabulary lives in a CHECK rather than in the application, by the same rule the content
-- status column follows: a value nobody named must be refused by the database, not stored and
-- rendered as blank by every reader that meets it.
ALTER TABLE "edit_comments"
    ADD COLUMN "kind" VARCHAR(20) NOT NULL DEFAULT 'discussion',
    ADD COLUMN "performer_id" UUID REFERENCES "performers"("id") ON DELETE SET NULL;

ALTER TABLE "edit_comments"
    ADD CONSTRAINT "edit_comments_kind" CHECK ("kind" IN (
        'discussion', 'source_info', 'performer_id', 'timestamp', 'correction', 'request', 'review'
    ));

-- THE STRUCTURED FIELD BELONGS TO ITS KIND, and this is the whole of slice 8a's schema:
-- `performer_id` is the field for the `performer_id` kind and for nothing else. The equality of
-- two booleans refuses both directions at once -- a comment that names a performer without being
-- of the kind that means it, and one of that kind that names nobody -- so there is no state in
-- which a reader has to guess which of the two the writer meant.
ALTER TABLE "edit_comments"
    ADD CONSTRAINT "edit_comments_kind_field" CHECK (("kind" = 'performer_id') = ("performer_id" IS NOT NULL));

-- Reading a member's own identification comments, which is how 8b will find them.
CREATE INDEX "edit_comments_performer_idx" ON "edit_comments" ("performer_id") WHERE "performer_id" IS NOT NULL;
