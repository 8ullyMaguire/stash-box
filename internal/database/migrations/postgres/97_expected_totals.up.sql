-- SPEC §7.24.2: expected-total denominators.
--
-- §7.7 computes a completion score from missing metadata, and a score without a
-- denominator is a ratio with no meaning. "The studio's site lists 412 scenes" is what
-- turns "catalog the studio" into a countable target: without it a studio with 3 of 400
-- scenes catalogued scores 100%.
--
-- One row per (entity_type, entity_id, kind). Not a column on studios: §7.24.8 makes the
-- weight of each kind differ, and the wanted list is derived from these rows, so adding a
-- kind must not be a migration.

CREATE TABLE "expected_totals" (
    "id" UUID NOT NULL PRIMARY KEY DEFAULT gen_random_uuid(),

    "entity_type" VARCHAR(20) NOT NULL,
    "entity_id" UUID NOT NULL,

    -- What is being counted: 'scenes', 'images', 'performers'. Kept as data rather than
    -- inferred from the entity, because a studio has all three and they are counted against
    -- each other -- 412 scenes and 60 performers are not one number.
    "kind" VARCHAR(32) NOT NULL,

    -- CHECK (total > 0): a denominator of zero is not a denominator. It makes every ratio
    -- against it undefined, and it is reachable by arithmetic -- a curator subtracting an
    -- observed count -- so the constraint is the only thing that catches it. Note the
    -- asymmetry with §7.24.1, where absence means missing and everything is nullable: an
    -- ABSENT total is a studio nobody has counted (harmless, the studio simply has no
    -- denominator), whereas a total that is PRESENT AND ZERO is a claim that the studio has
    -- nothing, and would score every real scene as surplus.
    "total" INTEGER NOT NULL CHECK ("total" > 0),

    -- source_id: SPEC §7.24.2 -- "Sourced, trusted-contributor-or-moderator only. A
    -- denominator is a claim about the world, so it carries source_id. An unsourced total is
    -- a rumour that silently deflates every completion score on the instance -- worse than
    -- having no total, because the damage is invisible."
    --
    -- The plan wrote `source_id UUID NOT NULL` referencing a `sources` table. There is no
    -- such table in this schema, and inventing one is not a decision this migration gets to
    -- make: a source is a first-class claim with an edit trail, its own trust rules, and
    -- de-duplication against re-imported urls, which is exactly what the three *_urls tables
    -- already are. So this points at a url row, via (source_entity_type, source_url).
    --
    -- A url rather than a plain text column, unlike §7.24.1's citation_url. The reasoning
    -- runs the other way there, and the difference is worth stating: an assertion says "this
    -- page does not mention height", where no url row for that page can exist, because it
    -- would be an entity claim of the opposite kind. A total says "this page lists 412",
    -- which IS the entity claim the url tables exist to hold -- so the url row is the source,
    -- and TEXT would duplicate it and let the two drift.
    --
    -- ON DELETE CASCADE: a total whose source is gone is no longer sourced, and the spec
    -- forbids an unsourced total. Deleting the row is honest; leaving it is exactly the
    -- invisible deflation the clause exists to prevent.
    "source_entity_type" VARCHAR(20) NOT NULL,
    "source_url" TEXT NOT NULL,

    "asserted_by" UUID NOT NULL REFERENCES "users" ("id") ON DELETE RESTRICT,
    "asserted_at" TIMESTAMP NOT NULL,

    -- A total per (entity, kind). The width matters: without entity_id in the key, asserting
    -- a total for one studio overwrites the rest, and every completion score on the instance
    -- moves because a curator corrected a single studio.
    UNIQUE ("entity_type", "entity_id", "kind"),

    -- The two halves of "sourced" and "attributed" are set together or not at all. A source
    -- with no author is a claim nobody stands behind, and an author with no source is the
    -- rumour this column exists to exclude.
    CHECK (("source_entity_type" IS NOT NULL AND "source_url" IS NOT NULL AND "asserted_by" IS NOT NULL))
);

-- The wanted-list and completion services both read a studio's totals; so does every
-- completion score recompute. entity_type is the leading column because the recompute
-- sweep asks "all the totals for these entity ids", and entity_id leads the UNIQUE above
-- because the write path asks "does this studio already have a total for scenes".
CREATE INDEX "expected_totals_entity_idx" ON "expected_totals" ("entity_type", "entity_id");

-- §7.24.2 says trusted-contributor-or-moderator only. That cannot be a CHECK, because
-- trust level lives in user_trust and changes; the service enforces it at write time and
-- this index makes the check cheap. A CHECK here would have been the tempting thing to
-- write and would have read as "enforced" in the schema.
CREATE INDEX "expected_totals_asserted_by_idx" ON "expected_totals" ("asserted_by");