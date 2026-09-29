-- The per-entity content denylist (SPEC §7.23 D5).
--
-- The one access rule this instance actually ENFORCES, as opposed to the region,
-- device-class and MFA rules which are recorded and delegated elsewhere. The
-- distinction is structural: an entity id is a row in this database, so it can be
-- checked authoritatively, while a region or a User-Agent is a claim from the
-- client that the client controls.
--
-- Denylist, not allowlist. An allowlist would need an entry for every tag, studio
-- and performer an operator has not thought to list, and a content plane that
-- fails closed on an unlisted entity is a content plane that goes dark the first
-- time someone adds a studio. The asymmetry is intentional: this table can only
-- ever REMOVE access.
--
-- Three tables rather than one polymorphic table with a kind column: there is no
-- query that needs "all denied entities regardless of kind", and a kind column
-- would push that discrimination into every read for no gain. Foreign keys give
-- referential integrity for free, which a polymorphic table cannot.
CREATE TABLE "content_denylist_tag" (
    "tag_id" UUID NOT NULL REFERENCES "tags" ("id") ON DELETE CASCADE,
    "reason" TEXT,
    "created_at" TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY ("tag_id")
);

CREATE TABLE "content_denylist_studio" (
    "studio_id" UUID NOT NULL REFERENCES "studios" ("id") ON DELETE CASCADE,
    "reason" TEXT,
    "created_at" TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY ("studio_id")
);

CREATE TABLE "content_denylist_performer" (
    "performer_id" UUID NOT NULL REFERENCES "performers" ("id") ON DELETE CASCADE,
    "reason" TEXT,
    "created_at" TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY ("performer_id")
);

-- The reason column is nullable rather than NOT NULL because "no reason given"
-- is a legitimate operator state, and a NOT NULL with a sentinel empty string
-- would make "denied with no stated reason" and "denied with an empty reason"
-- indistinguishable in an audit.
COMMENT ON TABLE "content_denylist_tag" IS
    'Entities whose content access is denied by this instance. Can only remove access, never grant it.';
COMMENT ON COLUMN "content_denylist_tag"."reason" IS
    'Why this entity is denied. Nullable: an operator may deny without stating why.';
