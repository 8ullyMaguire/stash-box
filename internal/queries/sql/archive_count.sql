-- Archive-wide counts (SPEC §7.7, growth item 9 "State of the Archive").
--
-- One query, five rows. A page whose entire job is "how much is catalogued"
-- needs a total per entity type, and the schema had no field that gave one:
-- `findPerformers { count }` and its four siblings each apply their own filters
-- and their own soft-delete rules, so composing them into an archive total would
-- mean five round trips whose numbers could disagree with each other and with the
-- completion counts beside them.
--
-- The completion side is already a single call (`CountEntitiesWithCompletionBelow`
-- per type). Putting the denominators beside it in one query keeps the numerator
-- and the denominator from drifting, which is the same argument §7.7 makes for not
-- storing the score.
--
-- UNION ALL, not five scalar subqueries: one round trip, one consistent snapshot.
-- With separate subqueries Postgres may evaluate each at a different moment under
-- READ COMMITTED, and a total that disagrees with the count beside it is worse
-- than no page.

-- name: CountArchiveEntities :many
-- How many live entities of each scored type exist.
--
-- `NOT deleted` on every branch THAT HAS IT, matching the completion queries'
-- rule exactly. A total that counted soft-deleted entities while the incomplete
-- count excluded them would report a permanently incomplete archive, and the two
-- numbers come from the same table so they must use the same predicate.
--
-- `sites` is the exception and has no `deleted` column at all: it is a small
-- fixed table of scrapers, not curated content, so there is nothing to
-- soft-delete. The first version of this query applied `NOT deleted` uniformly
-- and the whole field failed with `column "deleted" does not exist` -- an error
-- naming a column rather than the table that lacks it. Verified with
-- information_schema before writing, which is the only reliable way to know.
--
-- The entity_type values are the GraphQL EntityType enum spellings, so the
-- resolver can pass the rows through without a second mapping table that could
-- drift from the enum.
SELECT 'performer'::varchar AS entity_type, count(*)::bigint AS count FROM performers WHERE NOT deleted
UNION ALL
SELECT 'scene', count(*)::bigint FROM scenes WHERE NOT deleted
UNION ALL
SELECT 'studio', count(*)::bigint FROM studios WHERE NOT deleted
UNION ALL
SELECT 'site', count(*)::bigint FROM sites
UNION ALL
SELECT 'tag', count(*)::bigint FROM tags WHERE NOT deleted
ORDER BY entity_type ASC;
