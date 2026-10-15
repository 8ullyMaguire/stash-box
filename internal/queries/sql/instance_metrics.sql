-- name: InstanceMetrics :one
--
-- The instance metrics (spec 16.2): every figure DERIVED from the rows the features already write.
--
-- WHY ONE STATEMENT RATHER THAN ONE QUERY PER METRIC. These are read by an operator surface and by the live
-- walk, and a walk that recomputed them one at a time would be reading a moving table. One statement is one
-- snapshot, which is what makes relations like "members_active_window is never larger than members_total" true
-- by construction rather than by luck.
--
-- THE DEFINITIONS ARE NOT HERE, deliberately. Each figure is served with its own sentence, and that sentence
-- lives beside the accessor that reads the column (internal/service/metrics/metrics.go) -- so a metric cannot
-- be added without wiring its column, and cannot be served without its definition. Two readers of a bare
-- "scenes_total" who disagree about deleted scenes are reading two different numbers, and nothing in the
-- response would tell them.
--
-- A figure that counts MEMBERS excludes the instance's own bots (user_roles.role = 'BOT') and the deleted-user
-- placeholder row, because neither is a person using the catalogue. A figure that counts SCENES excludes
-- deleted ones. Every figure that is WINDOWED is bound to the same @since, so the window is one window.
SELECT
    -- Members.
    (SELECT COUNT(*) FROM users u
        WHERE u.name <> '[deleted user]'
          AND NOT EXISTS (SELECT 1 FROM user_roles r WHERE r.user_id = u.id AND r.role = 'BOT')) AS members_total,
    (SELECT COUNT(*) FROM users u
        WHERE u.name <> '[deleted user]'
          AND NOT EXISTS (SELECT 1 FROM user_roles r WHERE r.user_id = u.id AND r.role = 'BOT')
          AND u.last_api_call >= @since) AS members_active_window,
    (SELECT COUNT(*) FROM users u
        WHERE u.name <> '[deleted user]'
          AND NOT EXISTS (SELECT 1 FROM user_roles r WHERE r.user_id = u.id AND r.role = 'BOT')
          AND u.created_at >= @since) AS members_created_window,
    (SELECT COUNT(*) FROM users u
        WHERE u.name <> '[deleted user]'
          AND NOT EXISTS (SELECT 1 FROM user_roles r WHERE r.user_id = u.id AND r.role = 'BOT')
          AND u.invited_by IS NOT NULL) AS members_invited,

    -- The catalogue.
    (SELECT COUNT(*) FROM scenes s WHERE NOT s.deleted) AS scenes_total,
    (SELECT COUNT(*) FROM scenes s WHERE NOT s.deleted AND s.created_at >= @since) AS scenes_created_window,
    (SELECT COUNT(*) FROM scenes s
        WHERE NOT s.deleted
          AND EXISTS (SELECT 1 FROM content_sources cs
                        WHERE cs.entity_type = 'scene' AND cs.entity_id = s.id)) AS scenes_with_sources,
    (SELECT COUNT(*) FROM scenes s
        WHERE NOT s.deleted
          AND EXISTS (SELECT 1 FROM scene_fingerprints f WHERE f.scene_id = s.id)) AS scenes_with_fingerprint,
    (SELECT COUNT(*) FROM scene_fingerprints) AS fingerprints_total,
    (SELECT COUNT(*) FROM tags) AS tags_total,
    (SELECT COUNT(*) FROM tag_implications) AS tag_implications_total,

    -- Source health: the sweep's coverage, which is a different question from the catalogue's size.
    (SELECT COUNT(*) FROM content_sources) AS sources_total,
    (SELECT COUNT(*) FROM content_sources cs WHERE cs.last_checked >= @since) AS sources_checked_window,
    (SELECT COUNT(*) FROM content_sources cs WHERE cs.status = 'unknown') AS sources_unknown,

    -- The community's throughput and queues.
    (SELECT COUNT(*) FROM edits e WHERE e.status = 'PENDING') AS edits_pending,
    (SELECT COUNT(*) FROM edits e
        WHERE e.closed_at IS NOT NULL AND e.closed_at >= @since) AS edits_decided_window,
    (SELECT COUNT(*) FROM identification_candidates) AS identification_candidates_pending,
    (SELECT COUNT(*) FROM invite_keys) AS invites_issued,
    (SELECT COUNT(*) FROM invite_keys k WHERE k.uses = 0) AS invites_unused;
