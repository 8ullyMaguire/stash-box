-- Confirming an identification comment (plan phase 8 slice 8b).
--
-- A `performer_id` comment SUGGESTS that a performer belongs in a scene. Confirming one must not
-- write that relationship -- a comment is evidence and never authority, the same rule that makes a
-- vote on an edit a vote rather than an application -- so the confirmation creates an EDIT through
-- the pipeline every other scene change uses, and the edit is what links the performer once it is
-- approved.
--
-- These three columns are how the comment RECORDS ITS OUTCOME. Without them a moderator confirms
-- the same suggestion twice and nobody can tell: the second confirmation would create a second edit,
-- both would be approved, and the history would show two proposals where there was one.
ALTER TABLE "edit_comments"
    ADD COLUMN "confirmed_at" TIMESTAMP,
    ADD COLUMN "confirmed_edit_id" UUID REFERENCES "edits"("id") ON DELETE SET NULL,
    ADD COLUMN "confirmed_by_id" UUID REFERENCES "users"("id") ON DELETE SET NULL;

-- CONFIRMED AND UNCONFIRMED ARE ONE STATE, NOT FOUR. The equality of two booleans refuses both
-- directions at once, exactly as the structured-field rule does: a comment marked confirmed with no
-- edit to point at, and one that points at an edit without being marked confirmed.
ALTER TABLE "edit_comments"
    ADD CONSTRAINT "edit_comments_confirmation" CHECK (("confirmed_at" IS NULL) = ("confirmed_edit_id" IS NULL));

-- Only an identification can be confirmed. A discussion has nothing to confirm, and a comment whose
-- kind says it is a suggestion is the only one with an edit to create.
ALTER TABLE "edit_comments"
    ADD CONSTRAINT "edit_comments_confirmation_kind" CHECK ("confirmed_at" IS NULL OR "kind" = 'performer_id');

-- The moderator's own audit action, so a confirmation is answerable for by the same means every
-- other moderator action is.
ALTER TYPE mod_audit_action ADD VALUE IF NOT EXISTS 'PERFORMER_COMMENT_CONFIRM';

-- Reading the confirmations of one edit, which is what a moderator reviewing a scene's proposals wants.
CREATE INDEX "edit_comments_confirmed_idx" ON "edit_comments" ("confirmed_edit_id") WHERE "confirmed_edit_id" IS NOT NULL;
