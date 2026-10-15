-- name: ContentModerationReport :one
-- The transparency report's numbers, counted in ONE statement from the tables that hold them.
--
-- WHY THIS IS A QUERY AND NOT A DOCUMENT. `docs/goal-check.py` states the rule this phase is verified by:
-- "Never trust a count written in a document. Recompute it." A published count is a claim a reader cannot
-- check, and the whole point of a transparency report is that the instance's own records produce it -- so
-- every number here is a SELECT over `content_source_block`, `content_source_block_appeal`,
-- `content_sources` and `mod_audit`, and there is nowhere for a hand-written figure to live.
--
-- THE TWO HALVES ARE DELIBERATELY DIFFERENT KINDS OF NUMBER, and conflating them is how a transparency
-- report lies by accident:
--
--   * `IN FORCE NOW` numbers are STATE, read from the content tables with no window at all: what is refused
--     at this moment does not expire and is not a period's activity. A refusal recorded two years ago is
--     still in force today, and reporting it only inside a "last 30 days" window would understate it.
--   * `IN WINDOW` numbers are ACTIVITY, read from `mod_audit` with `created_at >= @since`. `mod_audit` has a
--     RETENTION -- `mod_audit_retention_days` deletes rows (DeleteExpiredModAudits) -- so an activity count
--     can never reach further back than the retention, and the report says which window it covers rather
--     than implying it covers everything.
--
-- THE RELEASE ARM IS WHY `data` IS READ RATHER THAN `action`. A release reuses the takedown's action with
-- `data.released = true` (see `internal/service/content/takedown.go`; migration 134's comment calls this
-- "one value per decision"), so counting rows by action alone would report every release as a NEW refusal --
-- the single number a reader of a transparency report is most likely to check against the notice they sent.
SELECT
    -- In force now: no window.
    (SELECT COUNT(*) FROM "content_source_block") AS blocks_in_force,
    (SELECT COUNT(*) FROM "content_source_block_appeal") AS appeals_filed,
    (SELECT COUNT(*) FROM "content_source_block_appeal" a WHERE a."decided_at" IS NULL) AS appeals_pending,
    (SELECT COUNT(*) FROM "content_source_block_appeal" a WHERE a."decided_at" IS NOT NULL) AS appeals_decided,
    (SELECT COUNT(*) FROM "content_source_block_appeal" a WHERE a."outcome" = 'granted') AS appeals_granted,
    (SELECT COUNT(*) FROM "content_source_block_appeal" a WHERE a."outcome" = 'denied') AS appeals_denied,
    (SELECT COUNT(*) FROM "content_sources" s WHERE s."status" = 'dmca') AS records_out_of_service,
    -- In window: activity, bounded by the audit retention.
    (SELECT COUNT(*) FROM "content_source_block" b WHERE b."created_at" >= @since) AS blocks_recorded_in_window,
    (SELECT COUNT(*) FROM "content_source_block_appeal" a WHERE a."filed_at" >= @since) AS appeals_filed_in_window,
    (SELECT COUNT(*) FROM "content_source_block_appeal" a
        WHERE a."decided_at" IS NOT NULL AND a."decided_at" >= @since) AS appeals_decided_in_window,
    (SELECT COUNT(*) FROM "mod_audit" m
        WHERE m."action" IN ('SOURCE_TAKEDOWN', 'SOURCE_BLOCK')
          AND (m."data" ->> 'released') IS DISTINCT FROM 'true'
          AND m."created_at" >= @since) AS refusals_in_window,
    -- RELEASES ARE ALSO RESTRICTED TO THE BLOCK-SIDE ACTIONS, and that is not tidiness. A GRANTED APPEAL
    -- writes TWO rows: the release it performs (through releaseKey, action SOURCE_TAKEDOWN/SOURCE_BLOCK,
    -- with released = true) and the decision it records (action SOURCE_APPEAL_DECISION, which also carries
    -- released = true). Counting every such row would therefore report one release as two -- in the exact
    -- case an appellant checks the figure against. The appeal's own outcome is already in the appeal figures
    -- above, so this column answers one question: how many refusals were taken out of service.
    (SELECT COUNT(*) FROM "mod_audit" m
        WHERE m."action" IN ('SOURCE_TAKEDOWN', 'SOURCE_BLOCK')
          AND (m."data" ->> 'released') = 'true'
          AND m."created_at" >= @since) AS releases_in_window;
