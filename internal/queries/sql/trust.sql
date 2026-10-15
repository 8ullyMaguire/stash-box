-- Trust level queries (SPEC §6, migration 76).
--
-- trust_events is the source of truth; user_trust is a denormalised rollup of
-- it. These queries keep the rollup in step with the events, and the two
-- upserts below are the only places in the codebase allowed to write either
-- table.

-- name: GetUserTrust :one
SELECT * FROM user_trust WHERE user_id = $1;

-- name: GetUserTrustByUserIDs :many
SELECT * FROM user_trust WHERE user_id = ANY($1::UUID[]);

-- name: RecordTrustEvent :one
-- ON CONFLICT DO NOTHING makes a retried or duplicated event a no-op rather
-- than a silent double increment. The matching partial state is a real
-- concern: an edit can be applied twice by a retried request, and without this
-- the contributor's trust would grow twice for one contribution.
--
-- Returning the row means a duplicate insert reports no rows, which the
-- service treats as "already recorded" rather than as an error.
INSERT INTO trust_events (user_id, kind, entity_type, entity_id, delta)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT DO NOTHING
RETURNING *;

-- name: ApplyTrustEvent :one
-- Applies one event's effect to the rollup, creating the row if absent.
--
-- The totals are incremented rather than recomputed by replaying events, so
-- recording a trust event is O(1) regardless of how much history a user has.
-- The signed delta works for both directions: a rejected edit decrements the
-- same column an approval incremented.
--
-- ON CONFLICT (user_id) DO UPDATE is required because a user with no rollup row
-- yet (every new user) has to be created on first event.
-- The per-kind terms are the SIGNED DELTA, not a literal 1.
--
-- They used to be literal 1, which is why every caller passed Delta: 1 and the
-- column could only ever count whole contributions. A bounty needs its magnitude
-- to reach bonus_points, and a reversal needs -1 to come back out, so the
-- magnitude cannot live in the caller and be ignored here.
--
-- Rejected edits are already the sign-flipped term (-delta), which is the one
-- place the sign convention differs: 'edit_rejected' moves rejected_edits, and a
-- REVERSAL of a rejection moves it back.
INSERT INTO user_trust (user_id, approved_edits, rejected_edits, identification_solves, quests_completed, replicas_hosted, invites_activated, bonus_points, updated_at)
VALUES (
    $1,
    CASE WHEN sqlc.arg(event_kind)::TEXT = 'edit_approved' THEN sqlc.arg(event_delta)::INTEGER ELSE 0 END,
    CASE WHEN sqlc.arg(event_kind)::TEXT = 'edit_rejected' THEN sqlc.arg(event_delta)::INTEGER ELSE 0 END,
    CASE WHEN sqlc.arg(event_kind)::TEXT = 'identification_solved' THEN sqlc.arg(event_delta)::INTEGER ELSE 0 END,
    CASE WHEN sqlc.arg(event_kind)::TEXT = 'quest_completed' THEN sqlc.arg(event_delta)::INTEGER ELSE 0 END,
    CASE WHEN sqlc.arg(event_kind)::TEXT = 'replica_hosted' THEN sqlc.arg(event_delta)::INTEGER ELSE 0 END,
    -- invite_activated is a COUNTED kind (plan phase 11 slice 11b): the referral payment is
    -- count x weight like every other contribution, and the count is a real fact about the
    -- member, unlike the bounty below.
    CASE WHEN sqlc.arg(event_kind)::TEXT = 'invite_activated' THEN sqlc.arg(event_delta)::INTEGER ELSE 0 END,
    -- A points-ONLY kind: bounty_bonus moves bonus_points and no count at all.
    -- That is the whole reason it is a separate kind -- folding it into
    -- quests_completed would credit 500 completed quests for one bounty.
    CASE WHEN sqlc.arg(event_kind)::TEXT = 'bounty_bonus' THEN sqlc.arg(event_delta)::INTEGER ELSE 0 END,
    NOW()
)
ON CONFLICT (user_id) DO UPDATE SET
    approved_edits = user_trust.approved_edits + EXCLUDED.approved_edits,
    rejected_edits = user_trust.rejected_edits + EXCLUDED.rejected_edits,
    identification_solves = user_trust.identification_solves + EXCLUDED.identification_solves,
    quests_completed = user_trust.quests_completed + EXCLUDED.quests_completed,
    replicas_hosted = user_trust.replicas_hosted + EXCLUDED.replicas_hosted,
    invites_activated = user_trust.invites_activated + EXCLUDED.invites_activated,
    bonus_points = user_trust.bonus_points + EXCLUDED.bonus_points,
    updated_at = NOW()
RETURNING *;

-- name: RecomputeUserTrustTotals :one
-- Rebuilds the totals from the event log.
--
-- This is the recovery path, and the reason trust_events is append-only: if the
-- rollup ever drifts -- a threshold change, a bug in ApplyTrustEvent, a manual
-- database edit -- the truth is still replayable. Coalescing SUMs the signed
-- deltas per kind in one pass rather than replaying row by row.
INSERT INTO user_trust (user_id, approved_edits, rejected_edits, identification_solves, quests_completed, replicas_hosted, invites_activated, bonus_points, updated_at)
SELECT
    $1,
    COALESCE(SUM(CASE WHEN kind = 'edit_approved' THEN delta ELSE 0 END), 0),
    COALESCE(SUM(CASE WHEN kind = 'edit_rejected' THEN -delta ELSE 0 END), 0),
    COALESCE(SUM(CASE WHEN kind = 'identification_solved' THEN delta ELSE 0 END), 0),
    COALESCE(SUM(CASE WHEN kind = 'quest_completed' THEN delta ELSE 0 END), 0),
    COALESCE(SUM(CASE WHEN kind = 'replica_hosted' THEN delta ELSE 0 END), 0),
    COALESCE(SUM(CASE WHEN kind = 'invite_activated' THEN delta ELSE 0 END), 0),
    COALESCE(SUM(CASE WHEN kind = 'bounty_bonus' THEN delta ELSE 0 END), 0),
    NOW()
FROM trust_events
WHERE user_id = $1
-- No GROUP BY: an aggregate over an empty set still returns exactly one row,
-- with the COALESCE defaults above. GROUP BY made this return NOTHING for a
-- user with no events, which made it unusable for creating a zeroed rollup --
-- and that is exactly what SetContentViewingOptIn needs to do for a user
-- opting in before they are eligible.
ON CONFLICT (user_id) DO UPDATE SET
    approved_edits = EXCLUDED.approved_edits,
    rejected_edits = EXCLUDED.rejected_edits,
    identification_solves = EXCLUDED.identification_solves,
    quests_completed = EXCLUDED.quests_completed,
    replicas_hosted = EXCLUDED.replicas_hosted,
    invites_activated = EXCLUDED.invites_activated,
    bonus_points = EXCLUDED.bonus_points,
    updated_at = NOW()
RETURNING *;

-- name: ListUserTrustDecayPending :many
-- The sweep's working set: members with something in flight, and ONLY them.
--
-- A member enters it by having a shortfall or a demotion on record, which the
-- write path sets the moment an event puts them below their level -- so a member
-- whose totals fall can never be missed by the sweep, and a member whose totals
-- are steady is never examined. The partial indexes from migration 125 are on
-- exactly this predicate.
--
-- A row leaves it when the recovery window closes and the policy clears both
-- timestamps, which is why a sweep that has nothing to do examines a set that
-- empties itself.
SELECT * FROM user_trust
WHERE short_since IS NOT NULL OR demoted_at IS NOT NULL
ORDER BY user_id;

-- name: SetUserTrustState :one
-- The level is written by the service after deriving it from the thresholds,
-- never by the database. The curve is a product decision and belongs in Go,
-- where it can be changed without a migration.
--
-- The decay timestamps travel WITH the level in this one statement, and that is
-- why this replaces the level-only setter rather than joining it. A member's
-- level and the reason it is lower than their totals justify are one fact: a
-- level written without its grace deadline is a member who is demoted at the
-- next sweep with no grace left, and a deadline written without its level is a
-- shortfall with nothing to measure it against. Two setters would be two chances
-- to write one without the other, and the failure is silent in both directions.
UPDATE user_trust
SET level = $2,
    short_since = $3,
    demoted_at = $4,
    demoted_from_level = $5,
    updated_at = NOW()
WHERE user_id = $1
RETURNING *;

-- name: SetContentViewingOptIn :one
-- The one user-writable field in this table (SPEC §6: high-trust users
-- EXPLICITLY opt in to viewing content).
--
-- It is deliberately NOT gated on level here. Eligibility (level >= 4) is
-- derived at read time by the service, because a stored eligibility would go
-- stale the moment a threshold changed; a stored opt-in is just the user's
-- own choice and is safe to keep.
UPDATE user_trust
SET content_viewing_opt_in = $2, updated_at = NOW()
WHERE user_id = $1
RETURNING *;

-- name: ListUserTrustEvents :many
-- One user's history, newest first. Used by the audit view and by tests that
-- assert the event log is the source of truth.
SELECT * FROM trust_events
WHERE user_id = $1
ORDER BY created_at DESC, id DESC;

-- name: CountUserTrustEvents :one
SELECT count(*) FROM trust_events WHERE user_id = $1;

-- name: GetAllUserTrust :many
-- Every rollup row. Used when a threshold changes and every level must be
-- recomputed (see RebuildLevels in internal/service/trust).
SELECT * FROM user_trust;

-- name: UserVisibleTier :one
-- The viewer's ceiling: the MAXIMUM tier they have opted into, or 0 for a user
-- with no preference, no user_trust row at all, or an empty array. The LEFT JOIN
-- against a one-row default is what lets a member with no row answer 0 rather
-- than not answering at all, so "anonymous" and "never asked" stay the same
-- answer -- which is what the gate wants: neither has consented to anything.
--
-- THE T3 COOLING PERIOD IS APPLIED HERE AND NOWHERE ELSE (phase 5, migration 121).
-- Four read paths ask for this ceiling -- the scene query, similarity, the activity
-- feed and the gate -- and one of them calling a different query that forgot to cool
-- would be the whole rule gone. The period's LENGTH is a Go constant passed in
-- (`trust.T3CoolingHours`), so the policy number is not buried in SQL; NULL means the
-- consent predates the column and is not aged.
SELECT CASE
    WHEN v."visible_tiers" IS NULL THEN 0
    WHEN 3 = ANY (v."visible_tiers")
         AND v."t3_consented_at" IS NOT NULL
         AND NOW() < v."t3_consented_at" + make_interval(hours => sqlc.arg(cooling_hours)::int)
    THEN 2
    ELSE (SELECT COALESCE(MAX(x), 0) FROM unnest(v."visible_tiers") AS x)
END::SMALLINT AS "tier"
FROM (SELECT 1) AS one
LEFT JOIN "user_trust" v ON v."user_id" = sqlc.arg(user_id);

-- name: SetVisibleTiers :one
-- The record of consent. The gate uses its MAXIMUM; the array is what an audit
-- reads, so the value is stored as sent rather than collapsed to a number.
--
-- `t3_consented_at` is stamped on GAIN and cleared on LOSS, and left alone in every
-- other case -- the ELSE is the whole point: a member who re-sends the same numbers in
-- a different order has not re-consented, and stamp-on-every-write would let them (or
-- anyone replaying their request) restart the cooling clock at will.
UPDATE "user_trust"
SET "visible_tiers" = @visible_tiers,
    "t3_consented_at" = CASE
        WHEN 3 = ANY (@visible_tiers::smallint[]) AND NOT (3 = ANY ("visible_tiers")) THEN NOW()
        WHEN NOT (3 = ANY (@visible_tiers::smallint[])) THEN NULL
        ELSE "t3_consented_at"
    END,
    "updated_at" = NOW()
WHERE "user_id" = @user_id
RETURNING *;

-- name: SetTagContentTier :one
-- A moderator's classification of a tag. Both columns together, because the two
-- refusals ("tier too high" and "needs the opt-in") are set by the same decision.
UPDATE "tags"
SET "content_tier" = @content_tier, "requires_opt_in" = @requires_opt_in, "updated_at" = NOW()
WHERE "id" = @id AND "deleted" = FALSE
RETURNING *;

-- name: SceneEffectiveTiers :many
-- The effective tier of a BATCH of scenes, which is what the activity feed needs: it assembles a
-- page of items and then has to drop the ones above the viewer's ceiling. This is the case the
-- entity_effective_tier view IS for -- a known, small set of ids -- as opposed to a filtered page
-- over the whole table, which excludes with a NOT EXISTS instead.
SELECT "entity_id", "tier" FROM "entity_effective_tier"
WHERE "entity_type" = 'scene' AND "entity_id" = ANY(@scene_ids::uuid[]);

-- name: ContentDenylistActive :one
-- Whether this instance denylists anything at all. The scene query's optimized trending path LIMITs
-- its subquery before the filters run, so a page that must exclude denylisted scenes cannot take it
-- -- the same guard the tiers need, and it needs the same answer to "is there anything to exclude".
SELECT (
    EXISTS (SELECT 1 FROM "content_denylist_tag" WHERE "mode" = 'gate')
    OR EXISTS (SELECT 1 FROM "content_denylist_studio" WHERE "mode" = 'gate')
    OR EXISTS (SELECT 1 FROM "content_denylist_performer" WHERE "mode" = 'gate')
)::bool AS "active";

-- name: DeniedSceneIDs :many
-- Which of these scenes are denylisted, for the paths that assemble a page in Go (the activity feed)
-- rather than in one statement. A batch of ids is what this table shape is good for; a filtered page
-- over the whole table excludes with a NOT EXISTS (query.ApplySceneDenylist) instead.
SELECT DISTINCT s."id"::uuid AS "scene_id"
FROM unnest(@scene_ids::uuid[]) AS s("id")
WHERE EXISTS (SELECT 1 FROM "scene_tags" st JOIN "content_denylist_tag" dt ON dt."tag_id" = st."tag_id" AND dt."mode" = 'gate' WHERE st."scene_id" = s."id")
   OR EXISTS (SELECT 1 FROM "scene_performers" sp JOIN "content_denylist_performer" dp ON dp."performer_id" = sp."performer_id" AND dp."mode" = 'gate' WHERE sp."scene_id" = s."id")
   OR EXISTS (SELECT 1 FROM "scenes" sc JOIN "content_denylist_studio" ds ON ds."studio_id" = sc."studio_id" AND ds."mode" = 'gate' WHERE sc."id" = s."id");

-- name: SceneWarnings :many
-- The WARN-mode entries touching one scene, for `Scene.warnings`. Three arms UNIONed rather than one
-- polymorphic query, for the reason migration 87 gives for three tables: there is no query that needs
-- them together except this one, and here the `source` column is what a reader is shown.
--
-- An empty result is the normal answer and means "nothing to report", which is why the GraphQL field is
-- a non-null list: a client must not have to distinguish "no warnings" from "this surface is silent".
SELECT 'tag'::text AS "source", COALESCE(dt."reason", '')::text AS "reason"
FROM "scene_tags" st JOIN "content_denylist_tag" dt ON dt."tag_id" = st."tag_id"
WHERE st."scene_id" = @scene_id AND dt."mode" = 'warn'
UNION ALL
SELECT 'studio'::text, COALESCE(ds."reason", '')::text
FROM "scenes" sc JOIN "content_denylist_studio" ds ON ds."studio_id" = sc."studio_id"
WHERE sc."id" = @scene_id AND ds."mode" = 'warn'
UNION ALL
SELECT 'performer'::text, COALESCE(dp."reason", '')::text
FROM "scene_performers" sp JOIN "content_denylist_performer" dp ON dp."performer_id" = sp."performer_id"
WHERE sp."scene_id" = @scene_id AND dp."mode" = 'warn';

-- name: UserAccessSetup :one
-- Everything contentAccessSetup reports about ONE viewer, in one row, so the fields cannot disagree
-- with each other. The LEFT JOIN against a one-row default is UserVisibleTier's shape and for the same
-- reason: a member with no user_trust row must answer "nothing consented" rather than not answer.
--
-- THE CONTRIBUTION TOTAL IS A COPY OF elo.sql's EXPRESSION, deliberately and with a test holding it
-- there (TestAccessSetupAgreesWithTheGate compares this against the elo query for one fixture). Two
-- copies with an assertion that they agree is what makes it one definition; extracting a SQL function
-- would hide the terms in a third place, and the four terms are the definition, not the arithmetic.
--
-- NULLABLE t3_consented_at is returned rather than a computed "effective at" because the CALLER owns
-- the cooling period (trust.T3CoolingHours, the same constant UserVisibleTier is given), and a second
-- copy of that policy inside SQL is how the two would drift.
SELECT COALESCE(ut."content_viewing_opt_in", false)::bool AS "opted_in",
       COALESCE(ut."content_terms_accepted_at" IS NOT NULL, false)::bool AS "terms_accepted",
       (COALESCE(ut."approved_edits", 0)
        + COALESCE(ut."identification_solves", 0)
        + COALESCE(ut."quests_completed", 0)
        + COALESCE(ut."replicas_hosted", 0))::bigint AS "contribution_score",
       COALESCE(ut."visible_tiers", '{}')::int[] AS "visible_tiers",
       ut."t3_consented_at"
FROM "users" u
         LEFT JOIN "user_trust" ut ON ut."user_id" = u."id"
WHERE u."id" = @user_id;

-- name: AcceptContentTerms :exec
-- The FIRST acceptance stands: COALESCE makes a second call a no-op rather than a rewrite, and the
-- upsert covers a member who has no user_trust row yet (every column in that table has a default).
INSERT INTO "user_trust" ("user_id", "content_terms_accepted_at")
VALUES (@user_id, now())
ON CONFLICT ("user_id") DO UPDATE
    SET "content_terms_accepted_at" = COALESCE("user_trust"."content_terms_accepted_at", now());

-- name: SetLeaderboardListed :one
-- Records the member's own choice about appearing on the leaderboard.
--
-- Same shape as SetContentViewingOptIn, and for the same reason: this is a preference
-- rather than a contribution, so it must not touch the totals, the level, or the counters.
--
-- IT UPSERTS, because the first version did not and a brand-new member toggling the setting
-- got an error: there is no user_trust row until a member has contributed, and "I would
-- rather not be on the board" is a sentence a member with nothing to show should be able to
-- say. The row it creates is the zeroed rollup the read path already reports for a member
-- with none -- level 0, every counter zero, which is what they have.
UPDATE user_trust
SET leaderboard_listed = $2, updated_at = now()
WHERE user_id = $1
RETURNING *;

-- name: UpsertLeaderboardListed :one
-- The same write for a member who has no rollup row yet: it creates the zeroed row and sets
-- the preference on it in one statement, so there is no window in which a member exists with
-- a preference and no counters, or the other way round.
INSERT INTO user_trust (user_id, leaderboard_listed, updated_at)
VALUES ($1, $2, now())
ON CONFLICT (user_id) DO UPDATE
SET leaderboard_listed = EXCLUDED.leaderboard_listed, updated_at = now()
RETURNING *;

-- name: ListLeaderboard :many
-- The leaderboard: members ranked by how many trust events they recorded since a cutoff
-- (plan phase 6 slice 6f).
--
-- THE COUNT IS OF EVENTS AND NOT OF DELTAS, exactly as the heatmap's is, and for the same
-- reason: a rejected edit is a day of work by somebody who showed up, and a board that
-- counted only approvals would rank members by an outcome they do not entirely control.
-- One row per MEMBER, not per member-day: the score is the total over the window.
--
-- THE WINDOW IS TRUNCATED IN THE DATABASE'S ZONE, on both sides, so a board that says
-- "the last thirty days" cannot include a day the rest of the instance calls yesterday.
--
-- OPT-OUT IS A JOIN ON THE MEMBER'S OWN CHOICE, and it is a LEFT JOIN with a COALESCE
-- because a member with no user_trust row has never contributed and cannot have opted out:
-- an INNER JOIN would silently read "no consent recorded" as "declined", which is the one
-- mistake that would make a fresh member's absence mean something it does not.
--
-- THE LEVEL IS READ, NOT RECOMPUTED, AND THE FIRST VERSION OF THIS QUERY GOT THAT WRONG.
-- It carried the counters back so Go could run the level curve over them, and the test that
-- asserts "the board's level is the trust service's level" failed on the first run: a
-- member inside a recovery window has a level the POLICY is holding down, which is
-- deliberately NOT what their totals justify (slice 6b-1). Recomputing is a second
-- implementation of a number that is not a pure function of the totals in time, so the
-- board now reads the stored level -- the same number the badge shows -- and answers 0,
-- which is LevelPublic, for a member with no rollup row.
--
-- No level filter here: the caller narrows an already-ordered ranking, which keeps the
-- POSITIONS honest -- position 7 of a level-scoped board is the member's real standing
-- rather than their rank among the members who happen to share their level.
SELECT a.user_id, a.score, COALESCE(ut.level, 0) AS level
FROM (
    SELECT te.user_id, count(*) AS score
    FROM trust_events te
    WHERE date_trunc('day', te.created_at)::timestamp >= date_trunc('day', sqlc.arg(since)::timestamptz)::timestamp
    GROUP BY te.user_id
) a
LEFT JOIN user_trust ut ON ut.user_id = a.user_id
WHERE COALESCE(ut.leaderboard_listed, true)
ORDER BY a.score DESC, a.user_id
LIMIT sqlc.arg(limit_count);
