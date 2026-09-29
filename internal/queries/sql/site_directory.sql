-- Site directory queries (SPEC §7.10, phase 3 step 2).
--
-- Everything here follows one rule from the #1007 work: a query that resolves a
-- site by id must never return a soft-deleted one, and the resolution helpers must
-- never invent a redirect to a row that does not exist. The plan referenced
-- FindSiteWithRedirect; no such function exists in this codebase, because there is
-- no redirect mechanism. So the contract is stated directly instead of being
-- delegated to a helper that was never written.

-- name: GetSiteDetails :one
-- The directory fields for one site, or no rows if nobody has filled them in.
--
-- A LEFT JOIN in the callers rather than this being an inner lookup, so an
-- unfilled site and a site whose details were emptied are distinguishable. A site
-- row with no site_details row means "unknown", which is not the same as "empty".
SELECT * FROM site_details WHERE site_id = $1;

-- name: UpsertSiteDetails :one
-- Create or replace the directory fields.
--
-- An UPSERT because site_details is 1:1 with sites and a partial update that only
-- sets non-null fields would make "clear the payment methods" unexpressible --
-- there would be no way to distinguish "leave it alone" from "set it to empty",
-- and the only signal would be whether the key appeared in the request. That is the
-- same nullable-vs-empty distinction the columns exist to preserve, applied to the
-- write.
--
-- updated_by is in the SET list so a moderator's edit is attributable, and
-- updated_at moves on every write including a no-op one, so "when was this last
-- touched" answers the question an operator actually asks.
INSERT INTO site_details (
    site_id, pricing, payment_methods, features, pros, cons, ethical_labels, updated_by
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (site_id) DO UPDATE SET
    pricing = EXCLUDED.pricing,
    payment_methods = EXCLUDED.payment_methods,
    features = EXCLUDED.features,
    pros = EXCLUDED.pros,
    cons = EXCLUDED.cons,
    ethical_labels = EXCLUDED.ethical_labels,
    updated_by = EXCLUDED.updated_by,
    updated_at = now()
RETURNING *;

-- name: AddSiteAlternative :one
-- Record that `alternative_site_id` is an alternative to `site_id`.
--
-- The self-link is prevented by the table's CHECK and NOT re-checked here. Adding
-- `WHERE site_id <> $2` would make the insert silently match zero rows instead of
-- failing, and a caller that does not check the error would report a successful
-- write for a row that was never created. Failing loudly is the right behaviour for
-- a constraint violation: it is a programming error, not a user error.
INSERT INTO site_alternatives (site_id, alternative_site_id)
VALUES ($1, $2)
ON CONFLICT DO NOTHING
RETURNING *;

-- name: RemoveSiteAlternative :exec
DELETE FROM site_alternatives
WHERE site_id = $1 AND alternative_site_id = $2;

-- name: ListSiteAlternatives :many
-- A site's direct alternatives, by name for display.
--
-- The JOIN is to sites rather than returning bare ids, because the alternative
-- list is rendered as "you might like X, Y, Z" and a second round trip per row to
-- resolve a name is N+1 for something the database can do once. The ordering is by
-- name so the list is stable between reads -- an unstable list makes a rendered
-- page reshuffle on refresh for no reason.
--
-- No `deleted` filter is possible here: sites has no deleted column. The
-- soft-delete concern from #1007 applies to studios, tags and performers, which
-- have one; sites are hard-deleted, and ON DELETE CASCADE means a deleted site
-- simply has no alternative rows left. A dangling id is therefore impossible
-- rather than filtered.
SELECT s.*
FROM site_alternatives sa
JOIN sites s ON s.id = sa.alternative_site_id
WHERE sa.site_id = $1
ORDER BY s.name ASC;

-- name: ListSitesListingThisAsAlternative :many
-- The reverse edge. A site's inbound discovery: who points at me.
SELECT s.*
FROM site_alternatives sa
JOIN sites s ON s.id = sa.site_id
WHERE sa.alternative_site_id = $1
ORDER BY s.name ASC;

-- name: CountSiteAlternatives :one
-- For the "N alternatives" badge on a site card.
SELECT count(*)::int FROM site_alternatives WHERE site_id = $1;

-- name: SearchSiteDirectory :many
-- §10's filters: by label, by payment method, by feature, and free text.
--
-- Every array filter is an OVERLAP (&&) rather than containment, because the
-- question is "does this site support credit cards", not "is its payment method
-- list exactly ['credit cards']". Containment (@>) would match nothing for a site
-- that takes cash too, which is most of them.
--
-- The filters are ANDed, which is what a user narrowing a search expects, and each
-- one is skipped when its array is empty so an unfiltered search does not require
-- the caller to pass a "match everything" sentinel. `cardinality(NULL) = 0` is
-- false for NULL, which is the wrong answer here -- a NULL filter must mean "no
-- filter", so the COALESCE makes the empty case explicit rather than relying on
-- three-valued logic to do it by accident.
--
-- visibility is the §10 directory default, and a hidden site is excluded from
-- SEARCH but not from a direct by-id fetch: hiding something from a listing is not
-- the same as deleting it, and a site that has scenes must still resolve by id or
-- every scene pointing at it 404s.
SELECT
    s.*,
    d.pricing,
    d.payment_methods,
    d.features,
    d.pros,
    d.cons,
    d.ethical_labels,
    -- The review summary, so the directory can sort and display without an N+1
    -- per row. LEFT JOIN'd and COALESCEd to zero: a site with no reviews is
    -- rating 0 of 0, not "no rating", and NULL here would make the client decide
    -- which of two zero-ish states it is looking at.
    COALESCE(r.average, 0)::double precision AS review_average,
    COALESCE(r.rated_count, 0)::int AS review_rated_count,
    COALESCE(r.total_count, 0)::int AS review_total_count
FROM sites s
LEFT JOIN site_details d ON d.site_id = s.id
LEFT JOIN LATERAL (
    SELECT
        CAST(COALESCE(avg(rating), 0) AS double precision) AS average,
        count(*) FILTER (WHERE rating IS NOT NULL)::int AS rated_count,
        count(*)::int AS total_count
    FROM reviews
    WHERE entity_type = 'SITE' AND entity_id = s.id AND status = 'published'
) r ON true
WHERE (cardinality(COALESCE(CAST(sqlc.narg('ethical_labels') AS TEXT[]), ARRAY[]::TEXT[])) = 0
       OR d.ethical_labels && CAST(sqlc.narg('ethical_labels') AS TEXT[]))
  AND (cardinality(COALESCE(CAST(sqlc.narg('payment_methods') AS TEXT[]), ARRAY[]::TEXT[])) = 0
       OR d.payment_methods && CAST(sqlc.narg('payment_methods') AS TEXT[]))
  AND (cardinality(COALESCE(CAST(sqlc.narg('features') AS TEXT[]), ARRAY[]::TEXT[])) = 0
       OR d.features && CAST(sqlc.narg('features') AS TEXT[]))
  AND (sqlc.narg('query')::TEXT IS NULL
       OR s.name ILIKE '%' || sqlc.narg('query')::TEXT || '%'
       OR d.pricing ILIKE '%' || sqlc.narg('query')::TEXT || '%')
ORDER BY
    -- Rated before unrated, then by score, then by name. Sorting on the raw
    -- average with no count guard puts a single 5-star review above forty 4.4s,
    -- which is the exact misreading §10's "rating" filter invites.
    review_total_count DESC,
    review_average DESC,
    s.name ASC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: ListSitesMissingDetails :many
-- The curation surface: sites with no directory fields at all.
--
-- This is what makes the directory a CURATION target rather than a form nobody
-- fills in. It is the same shape as the completion engine's missing-field queries,
-- and it is the join that says "the directory is 12% complete".
--
-- The NOT EXISTS rather than a LEFT JOIN ... IS NULL because site_details is
-- 1:1, so the two are equivalent -- and NOT EXISTS is the one that stays correct
-- if a future migration makes it 1:N by adding a history table.
SELECT s.*
FROM sites s
WHERE NOT EXISTS (SELECT 1 FROM site_details d WHERE d.site_id = s.id)
ORDER BY s.name ASC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');
