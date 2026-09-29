-- Site directory fields and alternatives (SPEC §7.10, phase 3 step 2).
--
-- Sites already exist as an entity (migration 21) with name/description/url/regex.
-- The directory adds the FIELDS §10 actually promises: pricing, payment methods,
-- features, pros/cons. Those are not on `sites` because they are directory
-- metadata rather than identity metadata, and putting them on the sites table
-- would make every existing SELECT sites.* carry a pricing tier nobody uses.
--
-- The site_alternatives self-relation is the part with a real hazard, addressed
-- below.

-- Directory fields. One table rather than columns on `sites` for the reason
-- above, and rather than a key/value table because every one of these is queried
-- (a filter on "free tier" is a directory requirement from §10, and a key/value
-- table makes that a join over text).
CREATE TABLE "site_details" (
    "site_id" UUID NOT NULL PRIMARY KEY REFERENCES "sites" ("id") ON DELETE CASCADE,

    -- NULLABLE, and the nullability is the semantics: §10 asks for pricing,
    -- payment methods, features, pros/cons and ethical labels, and most of them
    -- are UNKNOWN for most sites. An empty array would be a claim -- "this site
    -- has no payment methods" -- where NULL is the truth, "nobody has filled this
    -- in". A directory that renders the difference is honest; one that renders [] as
    -- "none" is lying about most of its own catalogue. Default NULL, not '{}'.
    --
    -- The one exception is pros/cons, which is also nullable for the same reason
    -- and NOT defaulted to '{}' for the same reason.
    "pricing" TEXT,
    "payment_methods" TEXT[],
    "features" TEXT[],
    "pros" TEXT[],
    "cons" TEXT[],

    -- §10's "ethical labels". Free text rather than a lookup table because the set
    -- is a community vocabulary that grows by discussion, and a reviewer adding a
    -- label should not have to open a migration.
    "ethical_labels" TEXT[],

    -- Who last filled this in, so a maintainer can ask. Nullable because a row
    -- can be created by an import or a seeding script with no user behind it.
    "updated_by" UUID REFERENCES "users" ("id") ON DELETE SET NULL,

    "updated_at" TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- §10's "alternatives" -- a many-to-many self-relation.
--
-- The CHECK is the load-bearing part of this table and the reason it is written
-- here rather than left to the service.
--
-- A site listing ITSELF as an alternative is otherwise reachable through the
-- GraphQL mutation, and a self-referential row is not a data error -- it is a
-- graph cycle. The UI that renders "alternatives" follows the relation to build a
-- navigation tree, and a one-node cycle in that tree is an infinite loop: the page
-- hangs, and it hangs for every visitor of that site, not for the person who
-- created the bad row. A service-side check races (two concurrent inserts both
-- read "no self-link" and both write) and is skipped entirely by any future
-- importer or admin script. The database is the only place that is always
-- consulted.
CREATE TABLE "site_alternatives" (
    "site_id" UUID NOT NULL REFERENCES "sites" ("id") ON DELETE CASCADE,
    "alternative_site_id" UUID NOT NULL REFERENCES "sites" ("id") ON DELETE CASCADE,

    PRIMARY KEY ("site_id", "alternative_site_id"),

    -- A site is not an alternative to itself. See above: the failure is a hung UI,
    -- not a rejected write.
    CONSTRAINT "site_alternatives_no_self" CHECK ("site_id" <> "alternative_site_id")
);

-- The listing direction: "what are A's alternatives", and the reverse ("what
-- lists A as an alternative") for a site's inbound discovery. Without the second
-- index the reverse lookup is a full scan, and the reverse is what a site's page
-- renders when nothing lists it yet.
CREATE INDEX "site_alternatives_reverse_idx"
    ON "site_alternatives" ("alternative_site_id");

-- Filtering the directory by label or payment method is a §10 requirement, and on
-- an array column the plain equality operator cannot answer it.
CREATE INDEX "site_details_payment_methods_idx"
    ON "site_details" USING GIN ("payment_methods");
CREATE INDEX "site_details_ethical_labels_idx"
    ON "site_details" USING GIN ("ethical_labels");
CREATE INDEX "site_details_features_idx"
    ON "site_details" USING GIN ("features");
