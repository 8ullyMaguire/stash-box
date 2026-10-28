-- Dropping this table drops the history of every recorded run. That is acceptable only
-- because the numbers are reproducible from the sha and seed in the row -- but once the
-- rows are gone, the shas are gone with them, which is why a rollback of this slice is a
-- decision and not a tidy-up.
DROP INDEX IF EXISTS "eval_runs_suite_created_idx";
DROP TABLE IF EXISTS "eval_runs";
