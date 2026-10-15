-- Collaborative lists (plan phase 8 slice 8e): the named write set.
--
-- WHAT THIS TABLE IS. `list_collaborators` names the members who may write a list's CONTENTS --
-- add, remove and reorder entries -- without owning it. That is the whole of the membership: every
-- act that changes what the list IS, or who may write it, stays the owner's (rename, re-describe,
-- delete, publish, unpublish, follow a saved search, add a collaborator). The object of the feature
-- is a list several people can fill; it is not a list several people can hand out.
--
-- THE OWNER IS NOT A ROW HERE, deliberately. `lists.owner_id` already answers "may this person write
-- this list", and storing the owner a second time is two answers to one question -- which is how one
-- of them comes to be wrong. The consequence is visible in every query that asks: a writer is
-- `owner_id = $1 OR EXISTS (SELECT 1 FROM list_collaborators WHERE ...)`.
--
-- A NAMED SET, NOT A VISIBILITY RULE. The cheap alternative -- "whoever can see the list may write
-- it" -- makes every published list publicly writable, which is a moderation surface handed to
-- strangers by a rename. Nothing here derives from `published_at`.
--
-- CASCADE ON BOTH KEYS, unlike list_audit.actor_id which is SET NULL: a collaborator row has no
-- meaning without the list or the member, whereas the record of what somebody did outlives them.
-- Note what this table does NOT carry: entries have no author column, so removing a collaborator
-- cannot and does not remove what they added. Attribution of an entry is the audit trail's job.
CREATE TABLE list_collaborators (
  list_id uuid NOT NULL REFERENCES lists (id) ON DELETE CASCADE,
  user_id uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  -- Who added them. SET NULL for the same reason as list_audit.actor_id.
  added_by uuid REFERENCES users (id) ON DELETE SET NULL,
  created_at timestamp with time zone NOT NULL DEFAULT now(),

  -- The primary key IS the duplicate rule: the same member cannot be added to the same list twice,
  -- so the service maps one constraint rather than checking first and racing.
  PRIMARY KEY (list_id, user_id)
);

-- The read the draft path takes is the primary key lookup ("is this viewer a collaborator of this
-- list"). This index answers the other direction -- "which lists am I a collaborator on" -- which is
-- what a member's own listing needs and which the primary key cannot serve.
CREATE INDEX list_collaborators_user ON list_collaborators (user_id);
