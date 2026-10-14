-- Warnings vs gates (aggregator phase 5, plan slice 5h; NexusXXX section 10).
--
-- Until now the instance had ONE instrument and it was all-or-nothing: an entity on the denylist was
-- not served, and there was no way to say "this is restricted" while letting a reader decide. A `warn`
-- entry is that second instrument: it is REPORTED and it changes nothing about what is served.
--
-- ADDITIVE, AND THE DEFAULT IS THE SAFE DIRECTION. `gate` is what every existing row means, so no row
-- changes its effect; a row inserted without naming a mode blocks. The CHECK is a closed set for the
-- same reason migration 115's visibility is: it may gain a level, and a typo must be refused rather
-- than silently behaving like one of the two.
--
-- A warning is NOT a second kind of tier. A tier removes a thing from a view; a gate decides access;
-- a warning is an annotation on a view, and it must never remove or grant visibility itself.
ALTER TABLE "content_denylist_tag"
    ADD COLUMN "mode" VARCHAR(4) NOT NULL DEFAULT 'gate' CHECK ("mode" IN ('gate', 'warn'));
ALTER TABLE "content_denylist_studio"
    ADD COLUMN "mode" VARCHAR(4) NOT NULL DEFAULT 'gate' CHECK ("mode" IN ('gate', 'warn'));
ALTER TABLE "content_denylist_performer"
    ADD COLUMN "mode" VARCHAR(4) NOT NULL DEFAULT 'gate' CHECK ("mode" IN ('gate', 'warn'));

COMMENT ON COLUMN "content_denylist_tag"."mode" IS
    'gate: the entity is not served. warn: the entity is served and the reason is reported to the reader. Warnings never change visibility.';
