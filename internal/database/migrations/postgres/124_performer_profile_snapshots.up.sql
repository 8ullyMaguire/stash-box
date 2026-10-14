-- Performer profile snapshots (SPEC §7.4 "performer archive/snapshots", plan phase 9 slice 9g).
--
-- WHAT THIS IS: what an external page said about a performer, and WHEN it said it. The
-- identification board's hard case is not "who is this" but "the page that proved it is gone" --
-- a retired performer's agency profile, a studio page that has since been rewritten, a site that
-- closed. A LINK is not evidence: it points at evidence somebody else controls the lifetime of.
-- A snapshot is the claim itself, recorded at a moment, and it does not move when the source does.
--
-- NO BLOBS, following migration 78. That migration refused to store a JPEG per scene snapshot
-- because "Stash Box is a metadata server"; this one refuses the page's bytes for the same reason
-- and one more: the page is not ours to keep. What is stored is the STRUCTURED claim the extractor
-- read -- title, studio, performer names, date, thumbnail address -- plus a hash of it, so a later
-- capture can be compared with this one without either being rewritten.
--
-- A SECOND CAPTURE DOES NOT OVERWRITE THE FIRST. The unique key is
-- (performer_id, url_normalized, content_hash): the same page saying the same thing twice is ONE
-- record, and the same page saying something DIFFERENT is a new row BESIDE the old one. That is
-- what makes this a record rather than a cache -- a cache keeps the newest answer, and the
-- interesting question here is what the page said BEFORE it changed.
--
-- The unique key is keyed on url_normalized, not source_url: a curator pasting the same page with
-- a tracking parameter or in a different case has captured one page, not two. source_url keeps the
-- string they actually used, because that is the evidence of where it came from.
--
-- captured_at DEFAULTS TO now() rather than being passed in: the column is a wall clock with no
-- zone, and the writer must be the same clock as every other row it will be compared with. A Go
-- process on a host two hours ahead of the database would stamp captures in the future.
CREATE TABLE "performer_profile_snapshots" (
    "id" UUID NOT NULL PRIMARY KEY,

    -- ON DELETE CASCADE, like scene_snapshots: a snapshot is not a cache, it IS the claim, and a
    -- claim about a performer who no longer exists is meaningless rather than merely orphaned.
    "performer_id" UUID NOT NULL,
    FOREIGN KEY ("performer_id") REFERENCES "performers" ("id") ON DELETE CASCADE,

    -- The page, as the curator gave it.
    "source_url" TEXT NOT NULL,

    -- The same page in canonical form, which is what decides whether two captures are the same
    -- page. See the header.
    "url_normalized" TEXT NOT NULL,

    -- When this content was seen. Never updated: see the header.
    "captured_at" TIMESTAMP NOT NULL DEFAULT now(),

    -- The identity of what was captured, so a later capture can be compared without storing it
    -- twice and without either copy being rewritten.
    "content_hash" TEXT NOT NULL,

    -- The extractor's own reading of the page, as it read it.
    "content" JSONB NOT NULL,

    CONSTRAINT "performer_profile_snapshots_unique"
        UNIQUE ("performer_id", "url_normalized", "content_hash"),

    -- A snapshot of nothing is not a snapshot. Both of these are needed to identify a capture, so
    -- an empty one makes the record uncomparable rather than merely odd.
    CONSTRAINT "performer_profile_snapshots_source_url_check" CHECK ("source_url" <> ''),
    CONSTRAINT "performer_profile_snapshots_hash_check" CHECK ("content_hash" <> '')
);

-- The read a profile page makes: this performer's captures, newest first.
CREATE INDEX "performer_profile_snapshots_performer_idx"
    ON "performer_profile_snapshots" ("performer_id", "captured_at" DESC);
