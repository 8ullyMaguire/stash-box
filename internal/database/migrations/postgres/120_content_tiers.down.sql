DROP VIEW IF EXISTS "entity_effective_tier";
ALTER TABLE "user_trust" DROP COLUMN IF EXISTS "visible_tiers";
ALTER TABLE "tags" DROP COLUMN IF EXISTS "requires_opt_in";
ALTER TABLE "tags" DROP COLUMN IF EXISTS "content_tier";
