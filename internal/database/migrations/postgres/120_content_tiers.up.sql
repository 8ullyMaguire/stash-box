-- Content tiers (plan phase 5, NexusXXX section 10).
--
-- DENYLIST-SHAPED, like migration 87's denylist and for the same reason: a tier
-- can only ever REMOVE visibility, never grant it. The effective tier of an
-- entity is the MAXIMUM over its tags, so a tag can only raise a thing out of a
-- reader's view; no arrangement of tags can lower an entity into someone's view
-- who has not opted in. An allowlist would need an entry for every tag an
-- operator has not classified yet, and a content plane that fails closed on an
-- unclassified tag goes dark the first time someone adds one.
--
-- Four tiers, 0 to 3. Zero is the default and means "unclassified", which is
-- also "visible to everyone" -- so every row that exists today is unaffected and
-- the column is additive. The ceiling is a CHECK rather than an enum type
-- because the tier is a number the UI compares (tier <= your ceiling), and a
-- Postgres enum would put that comparison behind a cast at every use.
ALTER TABLE "tags"
    ADD COLUMN "content_tier" SMALLINT NOT NULL DEFAULT 0
        CONSTRAINT "tags_content_tier_range" CHECK ("content_tier" >= 0 AND "content_tier" <= 3);

COMMENT ON COLUMN "tags"."content_tier" IS
    'Highest tier this tag places its entities in. 0 = unclassified/visible to everyone. A tier can only REMOVE visibility: an entity''s effective tier is the MAX over its tags.';

-- Distinguishes "this tag is tier 2" from "this tag needs an explicit opt-in
-- even at tier 0". Both refuse; they refuse for different reasons, and the
-- reason is what the user is told to act on.
ALTER TABLE "tags"
    ADD COLUMN "requires_opt_in" BOOLEAN NOT NULL DEFAULT FALSE;

COMMENT ON COLUMN "tags"."requires_opt_in" IS
    'Refuse this tag''s entities to any viewer who has not opted into content viewing, whatever the tier. Independent of content_tier so a tier-0 tag can still require the opt-in.';

-- The viewer''s own ceiling, in the same row as the existing content opt-in
-- (user_trust.content_viewing_opt_in) rather than in a second table: a user''s
-- consent to see content is ONE decision spread over two columns, and a second
-- home for half of it is a second source of truth able to disagree.
--
-- The array records WHICH tiers were consented to (that is what an audit asks
-- for -- "what did they agree to"), while the gate uses its MAXIMUM: opting into
-- tier 3 is opting into 1 and 2 as well, which is what a user means by it and
-- what the cumulative opt-in UI offers.
ALTER TABLE "user_trust"
    ADD COLUMN "visible_tiers" SMALLINT[] NOT NULL DEFAULT '{}'
        CONSTRAINT "user_trust_visible_tiers_range"
        CHECK ("visible_tiers" <@ ARRAY[0, 1, 2, 3]::SMALLINT[]);

COMMENT ON COLUMN "user_trust"."visible_tiers" IS
    'Tiers the user has explicitly opted into. The gate uses MAX(visible_tiers) as a ceiling; the array itself is the record of consent.';

-- The effective tier of an entity, for operators and for audit: "why is this
-- scene hidden from me" has to be answerable by looking, not by re-deriving the
-- MAX over tags in prose.
--
-- The READ paths do NOT use this view: they exclude with a NOT EXISTS against
-- the link table (see buildSceneQuery), because an aggregate over every scene
-- is the wrong shape for a filtered page. This view is the report.
--
-- LEFT JOINs so an entity with NO tags appears with tier 0 rather than not
-- appearing at all -- an entity missing from the report reads as "no tier",
-- which is a different claim from "tier 0".
CREATE VIEW "entity_effective_tier" AS
    SELECT 'scene'::TEXT AS "entity_type", s."id" AS "entity_id",
           COALESCE(MAX(t."content_tier"), 0)::SMALLINT AS "tier"
    FROM "scenes" s
    LEFT JOIN "scene_tags" st ON st."scene_id" = s."id"
    LEFT JOIN "tags" t ON t."id" = st."tag_id"
    GROUP BY s."id"
    UNION ALL
    SELECT 'content_item'::TEXT, ci."id", COALESCE(MAX(t."content_tier"), 0)::SMALLINT
    FROM "content_items" ci
    LEFT JOIN "content_item_tags" cit ON cit."content_item_id" = ci."id"
    LEFT JOIN "tags" t ON t."id" = cit."tag_id"
    GROUP BY ci."id";

COMMENT ON VIEW "entity_effective_tier" IS
    'Effective content tier per entity (MAX over its tags, 0 when untagged). A report: the read paths exclude with NOT EXISTS instead of aggregating.';
