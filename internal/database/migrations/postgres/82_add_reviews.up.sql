-- Reviews (SPEC §7.10, phase 3 step 1).
--
-- Reviews are the first user-generated content on the instance that is neither an
-- edit nor a vote, and that is why they need their own table rather than a reuse
-- of the edit log: a review is OPINION ABOUT a thing, not a correction to it, and
-- it has to be readable by an anonymous visitor, which an edit is not.

CREATE TABLE "reviews" (
    "id" UUID NOT NULL PRIMARY KEY,

    -- ON DELETE CASCADE, and that is the point: a GDPR erasure of a user must
    -- remove their reviews with them, and CASCADE is the only version of that
    -- which cannot be forgotten. A soft-deleted review outliving its author is a
    -- review attributed to a person who has asked to be forgotten.
    "author_id" UUID NOT NULL REFERENCES "users" ("id") ON DELETE CASCADE,

    -- Free text, not an enum, for the same reason trust_events.kind is: a review
    -- of a list or an instance is a plausible extension and a new value must not
    -- require a migration. Unknown types are rejected by the service, which knows
    -- the set, rather than by the schema, which would make adding one a release.
    "entity_type" TEXT NOT NULL,
    "entity_id" UUID NOT NULL,

    -- 1..5, NULLABLE, and nullability is load-bearing. Vision §10 asks for
    -- reviews of a studio's ethics and a performer's aliases, neither of which is
    -- a 1-to-5 quantity. Forcing a rating there produces a fake 3 that then gets
    -- AVERAGED into the site's score, so the constraint is a CHECK that permits
    -- NULL rather than a NOT NULL that would have to lie.
    "rating" INTEGER CHECK ("rating" IS NULL OR "rating" BETWEEN 1 AND 5),

    -- NOT NULL but not length-checked: a review of nothing is a misclick, and the
    -- service rejects it, while a length cap on the body belongs to a form
    -- concern (#660) rather than to the schema.
    "body" TEXT NOT NULL,

    -- "verified usage" from §10. FALSE by default and set by the service, never
    -- by the author: a self-asserted "I have used this" is worth nothing, and the
    -- whole value of the flag is that something checked it.
    "verified" BOOLEAN NOT NULL DEFAULT FALSE,

    -- 'published' | 'flagged' | 'removed'. A CHECK rather than an enum so the
    -- states are visible in the schema, and NOT a boolean because "flagged" and
    -- "removed" are different decisions with different consequences: flagged is
    -- still visible to its author and awaiting a moderator, removed is not shown
    -- to anyone but the author.
    "status" TEXT NOT NULL DEFAULT 'published'
        CHECK ("status" IN ('published', 'flagged', 'removed')),

    "created_at" TIMESTAMPTZ NOT NULL DEFAULT now(),
    "updated_at" TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- ONE REVIEW PER AUTHOR PER ENTITY, enforced here and not only in the service.
    -- The service check is a race: two concurrent submissions both read "no
    -- existing review" and both write. This is the same rule as the Phase 1
    -- identification suggestions, and the reason it is a constraint is that a
    -- duplicate review silently biases the average in the author's favour --
    -- one person rating a studio 5/5 three times outranks three people who were
    -- honest. The service returns the existing review for an update instead.
    CONSTRAINT "reviews_one_per_author_per_entity"
        UNIQUE ("author_id", "entity_type", "entity_id")
);

-- The listing read: one entity's reviews, newest first, published only. Covers
-- both the entity page and the rating average, which is the same scan.
CREATE INDEX "reviews_entity_idx"
    ON "reviews" ("entity_type", "entity_id", "created_at" DESC);

-- The author's own reviews, for a profile page. Without this it is a sequential
-- scan of every review ever written.
CREATE INDEX "reviews_author_idx"
    ON "reviews" ("author_id", "created_at" DESC);

-- Flagged-review moderation queue: status first, then oldest. The ordering is the
-- index, because a moderator's query is always "oldest flagged first" and an
-- unindexed created_at on a filtered query is a full scan of the review table.
CREATE INDEX "reviews_flagged_idx"
    ON "reviews" ("created_at") WHERE "status" = 'flagged';
