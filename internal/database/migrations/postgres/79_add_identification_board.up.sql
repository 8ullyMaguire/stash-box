-- Identification board (SPEC §5): "Which Was That…?"
--
-- The board turns collective memory into structured archive data. A user posts
-- something half-remembered, the community proposes candidates and votes, and a
-- solved query becomes a canonical link to real metadata.
--
-- The design decision that shapes the schema: a query is a QUESTION and its
-- candidates are SUGGESTIONS, and neither becomes metadata until a human resolves
-- it. There is deliberately no automatic path from "a suggestion won a vote" to
-- "create the scene". SPEC §5 says identifying an orphan scene "can trigger
-- metadata creation and replication" -- CAN, through the existing edit/draft path,
-- by a person. A vote is evidence, and treating a plurality vote as authority is
-- how an archive fills with confidently wrong records.

CREATE TABLE "identification_queries" (
    "id" UUID NOT NULL PRIMARY KEY,

    -- What is being identified. Exactly one of these is set: a query is about a
    -- performer, a scene, a studio, a site or a tag, never two at once, because
    -- the candidate suggestions and the resolution are all typed to one kind and
    -- a query spanning two would need both sets at once for no use.
    "target_type" VARCHAR(20) NOT NULL,
    "target_id" UUID,

    -- The question itself. Always present even when target_id is set: a user who
    -- has a candidate scene in hand still wants to confirm the PERFORMER in a
    -- frame, and the free text is what they remember.
    "description" TEXT NOT NULL,

    -- The evidence the poster has. Nullable individually, required as a set of
    -- zero or more: a text-only question is legitimate ("am I remembering the
    -- studio or the site?"), and a collage-only question is legitimate too.
    --
    -- No CHECK requiring at least one. §5 lists description, collage, snapshot,
    -- frame, quote and context as alternatives, not as a form with required
    -- fields, and a user with a half-formed memory should be able to post it and
    -- let the community ask for more.
    "collage_id" UUID,
    "snapshot_id" UUID,

    -- Who posted it. ON DELETE SET NULL: a solved identification outlives the
    -- person who asked. The question's value to the archive does not depend on
    -- who asked it, and deleting a user must not delete the collective memory
    -- that question represents.
    "created_by" UUID,
    FOREIGN KEY ("created_by") REFERENCES "users" ("id") ON DELETE SET NULL,

    -- OPEN until resolved or abandoned. ABANDONED is distinct from OPEN because
    -- "nobody will ever solve this" and "nobody has solved this yet" want
    -- different handling: an abandoned query is one the community voted down, and
    -- treating it as still-open would keep re-surfacing a dead question.
    "status" VARCHAR(20) NOT NULL DEFAULT 'open',
    CHECK ("status" IN ('open', 'solved', 'abandoned')),

    -- The resolution. Set only when status = 'solved'.
    --
    -- The entity the query turned out to be about, which is NOT necessarily the
    -- target: a user asking "who is this performer" with target_type=scene
    -- resolves to a performer. So this is a separate typed reference rather than
    -- reusing target_id, and the service refuses a resolution whose type differs
    -- from what the candidates were typed as.
    "resolved_type" VARCHAR(20),
    "resolved_id" UUID,

    -- Who accepted the resolution, and when.
    "resolved_by" UUID,
    FOREIGN KEY ("resolved_by") REFERENCES "users" ("id") ON DELETE SET NULL,
    "resolved_at" TIMESTAMP,

    "created_at" TIMESTAMP NOT NULL DEFAULT now(),
    "updated_at" TIMESTAMP NOT NULL DEFAULT now(),

    -- A resolved query must name what it resolved to, and an unresolved one must
    -- not. Without this, a query can be marked solved with nothing attached and
    -- every consumer has to re-check it.
    CHECK (
        (status = 'solved' AND resolved_type IS NOT NULL AND resolved_id IS NOT NULL
             AND resolved_at IS NOT NULL)
        OR
        (status <> 'solved' AND resolved_type IS NULL AND resolved_id IS NULL)
    )
);

-- The candidate suggestions, and their votes.
--
-- Separate from the query rather than a JSON array because a vote is a row:
-- SPEC §5 wants community voting, streaks and leaderboards, and "how many people
-- voted for this candidate" is the most-queried number on a solved query.
CREATE TABLE "identification_candidates" (
    "id" UUID NOT NULL PRIMARY KEY,
    "query_id" UUID NOT NULL,
    FOREIGN KEY ("query_id") REFERENCES "identification_queries" ("id") ON DELETE CASCADE,

    -- What is being suggested. Typed to match the query's target_type: suggesting
    -- a performer for a scene query is a category error, and the service enforces
    -- it rather than trusting the client.
    "entity_type" VARCHAR(20) NOT NULL,
    "entity_id" UUID NOT NULL,

    -- Why this was suggested. Free text, optional. A candidate with a reason
    -- ("the studio watermark is visible in frame 3") is far more useful to
    -- someone reading the thread than the bare entity, and it is the difference
    -- between a suggestion and a guess.
    "note" TEXT,

    "suggested_by" UUID,
    FOREIGN KEY ("suggested_by") REFERENCES "users" ("id") ON DELETE SET NULL,

    "created_at" TIMESTAMP NOT NULL DEFAULT now(),

    -- One user suggests a given entity for a given query once. Re-suggesting is
    -- not more signal, and allowing it would let one person weight the vote.
    UNIQUE ("query_id", "entity_type", "entity_id")
);

CREATE TABLE "identification_candidate_votes" (
    "candidate_id" UUID NOT NULL,
    FOREIGN KEY ("candidate_id") REFERENCES "identification_candidates" ("id") ON DELETE CASCADE,

    "user_id" UUID NOT NULL,
    FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON DELETE CASCADE,

    "created_at" TIMESTAMP NOT NULL DEFAULT now(),

    -- One vote per user per candidate. The composite primary key IS the rule, so
    -- a second vote is a constraint violation rather than a silently doubled
    -- tally.
    PRIMARY KEY ("candidate_id", "user_id")
);

-- The queue. §5 is a board, so a query is something you look at, and the query
-- every client needs is "the open queries, newest first".
--
-- Partial on status='open' because that is the only state anyone queues from, and
-- a full index over solved and abandoned queries -- which accumulate forever --
-- would make the hot read slower for every row that has already been answered.
CREATE INDEX "identification_queries_open_idx"
    ON "identification_queries" ("created_at" DESC, "id")
    WHERE status = 'open';

-- "Show me everything known about this entity", which is the read that turns a
-- solved query into a canonical link.
CREATE INDEX "identification_queries_resolved_idx"
    ON "identification_queries" ("resolved_type", "resolved_id")
    WHERE resolved_id IS NOT NULL;

-- The vote tally for a query's candidates, without a join per candidate.
CREATE INDEX "identification_candidate_votes_candidate_idx"
    ON "identification_candidate_votes" ("candidate_id");

-- The evidence fields point at tables this migration does not create ordering for,
-- and they are declared WITHOUT foreign keys on purpose: a snapshot is a claim
-- about a scene, and a query may legitimately reference a snapshot the user has
-- since deleted while the question remains answerable from its description. A
-- foreign key here would make the question undeletable-by-omission, which is the
-- wrong failure mode for a question.

-- The evidence attachments resolve through a join at read time rather than a
-- constraint at write time, as above.
