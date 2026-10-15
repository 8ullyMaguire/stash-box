-- Reversing this slice removes the cohorts and the campaigns, and it deliberately does NOT touch the
-- trust events a campaign paid: trust_events is append-only, and points that were earned by a real
-- member are that member's history. Un-picking them is a trust adjustment (with its own recorded kind
-- and its own audit), not a schema rollback -- and a migration that silently took points off people
-- would be the worst possible way to undo a campaign.
DROP TABLE IF EXISTS "seed_campaigns";
DROP TABLE IF EXISTS "seed_cohort_members";
DROP TABLE IF EXISTS "seed_cohorts";
