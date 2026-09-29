-- Vanguard status on user_trust (SPEC §7.23 D1).
--
-- elo.VoterWeight takes (level, isVanguard, contributionScore), and there was
-- nowhere for the vanguard half of that to come from: no column held it and no
-- query returned it. The weight function was fully tested against a bool that
-- no caller could ever supply, which is the shape of a feature that is green in
-- CI and inert in production.
--
-- BOOLEAN NOT NULL DEFAULT false, so every existing user is a non-vanguard and
-- keeps the unweighted semantics.
--
-- Deliberately NOT derived from `level`. Vanguard is a granted status, not a
-- threshold: a user can be made a vanguard below the level that would otherwise
-- qualify them, and the whole point of a separate status is that a curator can
-- confer it. Deriving it from level would make the flag unreachable as a
-- control and would silently re-classify anyone promoted or demoted for reasons
-- that have nothing to do with the mesh's rankings.
--
-- Adding an index is deliberately NOT done here. This column is read on the
-- vote-casting path, which already has the user_trust row loaded by primary key
-- (user_id), so a second index would be a write cost on every trust event for
-- no query plan.
ALTER TABLE "user_trust"
    ADD COLUMN "is_vanguard" BOOLEAN NOT NULL DEFAULT false;

COMMENT ON COLUMN "user_trust"."is_vanguard" IS
    'Granted status, not derived from level. Feeds elo.VoterWeight at vote-cast time.';
