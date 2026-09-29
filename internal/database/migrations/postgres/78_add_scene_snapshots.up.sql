-- Snapshot collages (SPEC §8).
--
-- The design decision that everything else follows from: a snapshot is a
-- TIMESTAMP into a video, not a stored image.
--
-- Stash Box is a metadata server. It does not host video and this migration adds
-- no image storage, no blobs and no S3 keys. A snapshot is "this scene, at 12:34,
-- is representative" -- a claim about content Stash Box does not have. The client
-- resolves a snapshot to a frame by seeking the video it already has, which is
-- what makes a collage work in a browser extension and in Stash App without either
-- of them uploading anything back.
--
-- The alternative -- storing a JPEG per snapshot -- was rejected because it makes
-- every instance a media host, which is a different product with a different cost
-- base, and because SPEC §3 says collages are replicated across the mesh "even
-- when full content is not". A timestamp replicates in 24 bytes; a JPEG does not.

CREATE TABLE "scene_snapshots" (
    "id" UUID NOT NULL PRIMARY KEY,

    -- The scene this snapshot is of.
    --
    -- ON DELETE CASCADE, unlike elo_ratings.entity_id. A snapshot is not a cache:
    -- it IS the claim, so a snapshot of a deleted scene is meaningless rather than
    -- merely orphaned. The CASCADE also means a merged scene's snapshots do not
    -- survive to be attached to whatever absorbed it, which is the conservative
    -- choice -- an operator or the merge UI can re-create them deliberately.
    "scene_id" UUID NOT NULL,
    FOREIGN KEY ("scene_id") REFERENCES "scenes" ("id") ON DELETE CASCADE,

    -- Position within the video, in milliseconds.
    --
    -- Milliseconds rather than seconds because a scene's most identifying frame is
    -- frequently a fraction of a second apart from its neighbour, and a
    -- second-granularity snapshot cannot distinguish "the opening shot" from "the
    -- shot one second later". The cost is nothing: 8 bytes of integer.
    --
    -- Not constrained against the scene's duration here, because the duration is
    -- user-submitted metadata that is routinely wrong and routinely revised. A
    -- CHECK against it would reject a legitimate snapshot the moment someone
    -- corrected a duration downwards. Validation against duration is the
    -- collages service's job, and it warns rather than refuses.
    "timestamp_ms" BIGINT NOT NULL,

    -- Which collage this snapshot belongs to.
    --
    -- NULL means "not yet assigned to a generated collage", which is the normal
    -- state for a snapshot added by hand or by the API before anyone asks for a
    -- collage. It is nullable rather than NOT NULL-with-a-default because a
    -- collage is a DERIVED artefact: SPEC §8 describes it as generated, so the
    -- snapshots are the source of truth and the collage row is a cache of a
    -- selection over them.
    "collage_id" UUID,

    -- Who added it.
    --
    -- ON DELETE SET NULL: a rating outlives its rater, and a snapshot should too.
    -- The snapshot's value to the archive does not depend on who submitted it, so
    -- deleting a user must not delete the community's visual index of a scene.
    "created_by" UUID,
    FOREIGN KEY ("created_by") REFERENCES "users" ("id") ON DELETE SET NULL,

    "created_at" TIMESTAMP NOT NULL DEFAULT now(),

    -- A scene cannot have two snapshots at the same instant: they would render as
    -- one indistinguishable frame and count twice toward a collage's frame budget.
    UNIQUE ("scene_id", "timestamp_ms")
);

-- The collage itself: the generated selection of snapshots for a scene.
--
-- Separate from scene_snapshots because SPEC §8's collages are REGENERATED (a
-- different frame count, a different sampling strategy, a re-roll after a bad
-- sample), and regeneration replaces a set of rows rather than mutating one. A
-- row per collage makes that a delete-and-insert inside one transaction instead of
-- an update cascade over every snapshot.
CREATE TABLE "collages" (
    "id" UUID NOT NULL PRIMARY KEY,
    "scene_id" UUID NOT NULL,
    FOREIGN KEY ("scene_id") REFERENCES "scenes" ("id") ON DELETE CASCADE,

    -- 12-24 per SPEC §8. Enforced here rather than in Go so a hand-written INSERT
    -- cannot produce a collage that violates the contract the UI renders against,
    -- and so the invariant has exactly one definition.
    "frame_count" INT NOT NULL,
    CHECK ("frame_count" BETWEEN 12 AND 24),

    -- The sampling strategy used, so a regenerated collage can be told apart from
    -- the previous one and so a future strategy can coexist with old rows.
    "strategy" VARCHAR(20) NOT NULL DEFAULT 'uniform',

    -- How the frames were chosen: the scene duration the sampler believed, and the
    -- scene duration the scene records now.
    --
    -- Recorded because they DIFFER, routinely. A collage generated against a
    -- duration that has since been corrected produces frames bunched at the end,
    -- and without these two numbers that is undiagnosable -- the collage just looks
    -- wrong. source_duration_ms is NULL when the scene had no duration at
    -- generation time, which is a real state and is why it is nullable.
    "source_duration_ms" BIGINT,
    "current_duration_ms" BIGINT,

    "generated_at" TIMESTAMP NOT NULL DEFAULT now(),

    -- One live collage per scene. A second generation replaces the first, so
    -- allowing two would mean every reader had to pick one and most would not.
    UNIQUE ("scene_id")
);

ALTER TABLE "scene_snapshots"
    ADD CONSTRAINT "scene_snapshots_collage_fkey"
    FOREIGN KEY ("collage_id") REFERENCES "collages" ("id") ON DELETE SET NULL;

-- The collage read path is "all snapshots for this collage, in order", so the index
-- covers both the equality and the ordering. Without it every collage render is a
-- sort of the scene's whole snapshot set.
CREATE INDEX "scene_snapshots_collage_order_idx"
    ON "scene_snapshots" ("collage_id", "timestamp_ms")
    WHERE "collage_id" IS NOT NULL;

-- Unassigned snapshots: the pool a regeneration samples from. Partial, because
-- this query is only ever "what is available to sample", never "everything".
CREATE INDEX "scene_snapshots_scene_unassigned_idx"
    ON "scene_snapshots" ("scene_id", "timestamp_ms")
    WHERE "collage_id" IS NULL;

-- §8: collages are replicated across the mesh even when full content is not, so
-- the replicas of a scene are worth finding on their own. This is the query the
-- federation layer will use and it does not exist until something asks for it.
CREATE INDEX "collages_generated_at_idx" ON "collages" ("generated_at");
