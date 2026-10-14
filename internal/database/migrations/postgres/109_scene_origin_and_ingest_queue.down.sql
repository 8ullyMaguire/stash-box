-- Teardown for 109. Read the second half before running this anywhere real.

-- The queue is transient work, so dropping it loses queued items and nothing else: no
-- completed ingestion depends on its own queue row (`result_scene_id` points the other
-- way, from the queue to the scene).
DROP TABLE IF EXISTS "ingest_queue";

DROP INDEX IF EXISTS "scenes_origin_aggregate_idx";

ALTER TABLE "scenes" DROP CONSTRAINT IF EXISTS "scenes_origin_check";

-- DERIVABLE FROM NOTHING. Every other fact about an aggregated scene survives this
-- teardown -- its sources are in content_sources, its rows are in scenes -- but WHICH
-- SCENES WERE AGGREGATED does not, and cannot be re-derived from them: a member's scene
-- with a tube URL looks identical afterwards. Record it before running this on any
-- database that holds aggregated content:
--
--     SELECT id FROM scenes WHERE origin = 'aggregate';
--
ALTER TABLE "scenes" DROP COLUMN IF EXISTS "origin";
