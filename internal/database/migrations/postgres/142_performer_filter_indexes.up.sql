-- Filter indexes for queryPerformers (upstream stashapp/stash-box#1284, ported 2026-10-16).
--
-- WHAT THESE SERVE. Each column below is a criterion `buildPerformerQuery` applies as a plain comparison on
-- the column itself: `ApplyIntCriterion` at height, band_size, waist_size, hip_size and the career years;
-- `ApplyEnumCriterion` at eye_color, hair_color and breast_type; `ApplyStringCriterion` at ethnicity, country
-- and gender; `ApplyDateCriterion` at birthdate and deathdate (internal/service/performer/query.go). Without
-- an index each is a sequential scan of every performer, and filtering is the one thing this query input
-- exists to do.
--
-- PARTIAL, ON `NOT deleted`, and that is not a smaller version of the same index: the queries read live rows,
-- so a partial index matches the predicate the planner is given and the full one does not.
--
-- WHAT WAS NOT TAKEN FROM THE PR, measured rather than judged. The PR also ships TWO cup_size expression
-- indexes -- `(UPPER(TRIM(cup_size)))` for equality, and a `CASE UPPER(TRIM(cup_size)) WHEN 'AAA' THEN 'A'
-- … END COLLATE "C"` expression for ordered comparison -- because upstream compares cup sizes with those
-- inline expressions. This fork does not: our cup_size filter goes through `queryhelper.ApplyStringCriterion`
-- (query.go:212), which emits a plain `cup_size = ?` or `cup_size ILIKE '%…%'`. An index on an expression
-- nothing queries is dead weight a later reader would take as evidence, and the ILIKE arm cannot use a btree
-- at all -- so the honest port is the fourteen plain columns, not fifteen and two expressions.
--
-- THE URL HALF OF THE PR NEEDS NO INDEX HERE. It turns the URL filter into a case-insensitive comparison
-- (`LOWER(performer_urls.url) = LOWER(?)`), and this fork already carries `performer_urls_url_lower_idx`
-- (migration 49), so that comparison is index-backed as it stands.

CREATE INDEX performers_gender_idx ON performers (gender) WHERE NOT deleted;
CREATE INDEX performers_ethnicity_idx ON performers (ethnicity) WHERE NOT deleted;
CREATE INDEX performers_country_idx ON performers (country) WHERE NOT deleted;

CREATE INDEX performers_eye_color_idx ON performers (eye_color) WHERE NOT deleted;
CREATE INDEX performers_hair_color_idx ON performers (hair_color) WHERE NOT deleted;
CREATE INDEX performers_breast_type_idx ON performers (breast_type) WHERE NOT deleted;

CREATE INDEX performers_height_idx ON performers (height) WHERE NOT deleted;
CREATE INDEX performers_band_size_idx ON performers (band_size) WHERE NOT deleted;
CREATE INDEX performers_waist_size_idx ON performers (waist_size) WHERE NOT deleted;
CREATE INDEX performers_hip_size_idx ON performers (hip_size) WHERE NOT deleted;

CREATE INDEX performers_career_start_year_idx ON performers (career_start_year) WHERE NOT deleted;
CREATE INDEX performers_career_end_year_idx ON performers (career_end_year) WHERE NOT deleted;

CREATE INDEX performers_birthdate_idx ON performers (birthdate) WHERE NOT deleted;
CREATE INDEX performers_deathdate_idx ON performers (deathdate) WHERE NOT deleted;

-- The existing unique index on this table starts with performer_id, which is the direction "who favourited
-- this performer" needs. Favourite FILTERING starts from the viewer, so it needs the reverse column order.
CREATE INDEX performer_favorites_user_performer_idx ON performer_favorites (user_id, performer_id);
