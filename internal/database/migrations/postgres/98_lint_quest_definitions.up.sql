-- SPEC §7.24.4: data-lint quests.
--
-- "Each detector is a named SQL query emitting quest candidates, against tables that already
-- exist." So this table is a REGISTRY of detectors, not a store of them: the SQL lives in
-- internal/queries/ as a named Go function, and this row says which detectors exist, are
-- enabled, and cost what.
--
-- The plan's wording is "the SQL lives in internal/queries/, not in the row", and that is the
-- load-bearing decision. A detector whose SQL is a row would make every instance able to
-- rewrite its own work-item generator through an ordinary edit, and the two halves would then
-- disagree: the registry would claim a detector exists that no code implements. A slug that
-- names no Go function is a broken quest, and the registry refuses it at startup instead.

CREATE TABLE "lint_quest_definitions" (
    "id" UUID NOT NULL PRIMARY KEY DEFAULT gen_random_uuid(),

    -- The slug is the contract between the row and the Go function. snake_case, stable, and
    -- never renamed: a rename orphans every candidate row and every operator's saved filter,
    -- and the failure is silent because the detector simply stops firing.
    "slug" VARCHAR(64) NOT NULL UNIQUE CHECK ("slug" ~ '^[a-z][a-z0-9_]*$'),

    -- Shown to a curator deciding whether to work the quest, so it says what is WRONG, not
    -- what the query does. NOT NULL: a detector a curator cannot read is one they will not
    -- work, and an empty string would look like a rendering bug.
    "description" TEXT NOT NULL CHECK (length(trim("description")) > 0),

    -- Whether the detector runs when generating. Default TRUE rather than FALSE: a detector
    -- that must be switched on after seeding never is, and an operator who wants fewer
    -- quests can disable one.
    "enabled" BOOLEAN NOT NULL DEFAULT TRUE,

    -- What each candidate is worth, in the same trust points §7.7 uses. Stored per detector
    -- because the detectors are NOT equally worth the curator's time, and one global constant
    -- would price "resolve this duplicate identity" the same as "add one missing birthdate".
    --
    -- NOT NULL with a floor, and it is INTERNAL points, never money: §7.24.3's audit row
    -- records that no currency column exists anywhere in this schema, and adding one here
    -- would be the only such column in the database.
    "bounty_points" INTEGER NOT NULL DEFAULT 5
        CHECK ("bounty_points" >= 0 AND "bounty_points" <= 10000),

    -- Seeded or curator-authored. A seeded detector is part of the product; an authored one
    -- is a local rule. The distinction is why `slug` is UNIQUE rather than the pair: a
    -- curator must not be able to shadow a shipped detector, because the shipped one would
    -- then stop firing on this instance and nobody would notice.
    "is_builtin" BOOLEAN NOT NULL DEFAULT FALSE,

    -- NULL only for a builtin that predates the column. For an authored row, a missing source
    -- is a claim nobody stands behind -- the same rule §7.24.2's expected_totals enforces.
    "authored_by" UUID REFERENCES "users" ("id") ON DELETE SET NULL,
    "created_at" TIMESTAMP NOT NULL DEFAULT now(),

    -- A builtin is nobody's; an authored detector has an author. ENFORCED, because a seeded
    -- row with no author and an authored row with one are the whole distinction between "part
    -- of the product" and "this instance's own rule", and a CHECK is the only thing that
    -- keeps a later INSERT from blurring it.
    CHECK (("is_builtin" AND "authored_by" IS NULL) OR (NOT "is_builtin" AND "authored_by" IS NOT NULL))
);

-- The generation sweep reads enabled rows on every pass, so this is the index that keeps
-- quest generation off the critical path. Partial, because disabled detectors are the ones
-- nobody reads and an index over them is dead weight that grows every time an operator
-- experiments.
CREATE INDEX "lint_quest_definitions_enabled_idx"
    ON "lint_quest_definitions" ("slug") WHERE "enabled";

-- Operators list detectors by author ("which rules did this curator add?"). Partial on
-- authored rows, which are the only ones that can have one.
CREATE INDEX "lint_quest_definitions_authored_by_idx"
    ON "lint_quest_definitions" ("authored_by") WHERE "authored_by" IS NOT NULL;

-- §7.24.4: "The same SQL doubles as a validation pass over imports." A detector needs to know
-- whether an import introduced a problem, and "recently" is a time filter over candidates --
-- so the detector registry is joined by candidate rows, and both sides want a timestamp.
CREATE INDEX "lint_quest_definitions_created_at_idx" ON "lint_quest_definitions" ("created_at");