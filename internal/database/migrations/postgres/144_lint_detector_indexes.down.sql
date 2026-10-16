-- The two expression indexes from 144. Dropping them does not change any answer the detectors give --
-- it makes the alias-collision detector slow again (37s on an archive of 112k performers), which is
-- exactly what an operator rolling back should know before they do it.
DROP INDEX IF EXISTS "index_performer_aliases_on_lower_alias";
DROP INDEX IF EXISTS "index_active_performers_on_lower_name";
