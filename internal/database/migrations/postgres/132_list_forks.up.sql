-- Forking a list (plan phase 8 slice 8c: playlist extensions).
--
-- A FORK IS A COPY THAT REMEMBERS WHERE IT CAME FROM AND TRACKS NOTHING. That distinction is the
-- whole of this slice: a fork is independent the moment it exists, so later changes to the source do
-- not reach it. The alternative -- a live view of somebody else's list -- is a different feature (the
-- auto-updating list, which is slice 8d), and conflating them would mean a member's list changing
-- under them because a stranger edited theirs.
--
-- `ON DELETE SET NULL` rather than a cascade or a refusal: deleting a source must not delete the
-- copies members have built on, and it must not be blocked by them either. The fork survives its
-- parent and forgets it, which is the same shape the rest of this schema uses for provenance that is
-- not ownership.
ALTER TABLE "lists"
    ADD COLUMN "forked_from_id" UUID REFERENCES "lists"("id") ON DELETE SET NULL;

-- A list cannot be its own origin. The column is nullable and this refuses the one value that would
-- make the provenance a loop, since nothing else in the schema would catch it: a list whose parent is
-- itself reads as an ordinary fork to every query that follows the link.
ALTER TABLE "lists"
    ADD CONSTRAINT "lists_fork_is_not_itself" CHECK ("forked_from_id" IS DISTINCT FROM "id");

-- Reading a source's forks, and following a fork up to its source.
CREATE INDEX "lists_forked_from_idx" ON "lists" ("forked_from_id") WHERE "forked_from_id" IS NOT NULL;
