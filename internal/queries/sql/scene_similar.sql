-- Scene similarity (feature-discovery SPEC section 8, aggregator phase 7 slice 5).
--
-- THE EVIDENCE IS THE QUERY, THE SCORE IS THE SERVICE.
--
-- These three queries answer "which scenes has this one got anything to do with, and what
-- exactly do they have in common". They deliberately do NOT answer "how similar, on a scale
-- of nought to one": the score weights the evidence in ONE place --
-- `internal/service/similar/scene.go` -- so that the number and the reasons printed beside it
-- cannot disagree. A SQL score would be a second opinion the labels could contradict.
--
-- What SQL does own is the CANDIDATE SET, which is the part that has to be a query: the axes
-- are three different joins and a closure walk over the tag DAG, and the set is ranked by a
-- count of shared facts so the caller can ask for a bounded page.

-- name: FindSimilarSceneCandidates :many
-- Candidates to consider, plus the two subject-side counts the score needs as a denominator.
--
-- THE PRE-RANK IS A FILTER, NOT THE SCORE, and it is deliberately UNWEIGHTED: `shared_facts`
-- counts shared tags (in either direction of the ontology), shared performers and a shared
-- studio, each as one. A weighted pre-rank would be a second copy of the scorer's weights
-- living in a file the scorer does not read, and the two would drift. Counting facts cannot
-- drift from anything -- it is not an opinion about importance -- and its only job is to
-- choose which rows the exact scorer gets to see. A candidate that loses on tags can still
-- outrank a tag-heavy one in the final order, which is why `candidate_limit` is wider than
-- the page the caller asked for (see `candidateSlack` in the service).
--
-- EXCLUDES the subject itself (every scene is trivially identical to itself), soft-deleted
-- scenes (a recommendation that links to a deleted scene is worse than one fewer
-- suggestion), and scenes that share NOTHING -- the candidate predicate is the same axes as
-- the rank, so a scene with no tag, performer or studio in common is not a candidate at all
-- rather than a candidate with a score of zero.
--
-- TAGS ARE READ THROUGH THE ONTOLOGY, both ways, because migration 111 does not write an
-- implied tag to `scene_tags`: a scene tagged `cowgirl_position` carries one row, and a
-- reader asking for `woman_on_top` finds it by walking the DAG. Similarity has to walk the
-- same graph or it will call two scenes unrelated because nobody typed the general tag
-- twice. Direction matters and is scored differently downstream -- a candidate carrying a
-- tag that SATISFIES one of the subject's tags is a specific instance of a claim the subject
-- makes (a strong match), while a candidate carrying a tag the subject's tag IMPLIES is a
-- family relative (a weak one) -- so the evidence query reports the direction.
--
-- The walks are depth-bounded, not size-bounded, and they are TOP-LEVEL rather than nested
-- inside another CTE: a recursive CTE may not reference itself from a subquery, and sqlc
-- resolves a nested `WITH RECURSIVE` as a missing relation rather than as invalid SQL.
WITH RECURSIVE subject AS (
    SELECT s.id, s.studio_id, s.duration
    FROM scenes s
    WHERE s.id = sqlc.arg(subject_id)::uuid
      AND NOT s.deleted
),
subject_tags AS (
    SELECT st.tag_id
    FROM scene_tags st
    WHERE st.scene_id = sqlc.arg(subject_id)::uuid
),
subject_performers AS (
    SELECT sp.performer_id
    FROM scene_performers sp
    JOIN performers p ON p.id = sp.performer_id AND NOT p.deleted
    WHERE sp.scene_id = sqlc.arg(subject_id)::uuid
),
-- A tag the subject's tags SATISFY: a specific instance of what the subject claims.
-- `cowgirl_position` implies `woman_on_top`, so a subject tagged `woman_on_top` is satisfied
-- by a candidate tagged `cowgirl_position`. The same walk, and the same depth bound, as
-- ExpandTagIDsByImplications -- there is one DAG and one way to read it. A tag reachable from
-- two of the subject's tags appears twice, which is harmless: every use below is a membership
-- test (`IN`), which cannot double-count.
reach_down(root_id, tag_id, depth) AS (
    SELECT tag_id, tag_id, 0 FROM subject_tags
    UNION
    SELECT r.root_id, ti.tag_id, r.depth + 1
    FROM reach_down r
    JOIN tag_implications ti ON ti.implied_tag_id = r.tag_id
    WHERE r.depth < 64
),
-- A tag the subject's tags IMPLY: a broader claim the subject belongs to.
reach_up(root_id, tag_id, depth) AS (
    SELECT tag_id, tag_id, 0 FROM subject_tags
    UNION
    SELECT r.root_id, ti.implied_tag_id, r.depth + 1
    FROM reach_up r
    JOIN tag_implications ti ON ti.tag_id = r.tag_id
    WHERE r.depth < 64
),
subject_size AS (
    SELECT
        (SELECT count(*)::int FROM subject_tags) AS subject_tags,
        (SELECT count(*)::int FROM subject_performers) AS subject_performers,
        (SELECT count(*)::int FROM subject) AS subject_exists
)
SELECT
    c.id AS scene_id,
    -- The denominator, repeated on every row because it is a fact about the SUBJECT and the
    -- caller needs it even when the candidate list is empty. Sent from here rather than
    -- queried again so that the page and its denominator come from one statement: a separate
    -- count could see a different snapshot.
    ss.subject_tags,
    ss.subject_performers,
    (
        -- Every shared fact counts once, in whichever direction the ontology found it.
        (SELECT count(*) FROM scene_tags cst WHERE cst.scene_id = c.id AND cst.tag_id IN (SELECT tag_id FROM reach_down))
      + (SELECT count(*) FROM scene_tags cst WHERE cst.scene_id = c.id AND cst.tag_id IN (SELECT tag_id FROM reach_up))
      + (SELECT count(*) FROM scene_performers csp WHERE csp.scene_id = c.id AND csp.performer_id IN (SELECT performer_id FROM subject_performers))
      + CASE WHEN c.studio_id IS NOT NULL AND c.studio_id = (SELECT studio_id FROM subject) THEN 1 ELSE 0 END
    ) AS shared_facts
FROM scenes c
CROSS JOIN subject_size ss
WHERE c.id <> sqlc.arg(subject_id)::uuid
  AND NOT c.deleted
  AND ss.subject_exists = 1
  AND (
        EXISTS (SELECT 1 FROM scene_tags cst WHERE cst.scene_id = c.id AND cst.tag_id IN (SELECT tag_id FROM reach_down))
     OR EXISTS (SELECT 1 FROM scene_tags cst WHERE cst.scene_id = c.id AND cst.tag_id IN (SELECT tag_id FROM reach_up))
     OR EXISTS (SELECT 1 FROM scene_performers csp WHERE csp.scene_id = c.id AND csp.performer_id IN (SELECT performer_id FROM subject_performers))
     OR (c.studio_id IS NOT NULL AND c.studio_id = (SELECT studio_id FROM subject))
  )
ORDER BY shared_facts DESC, c.id ASC
LIMIT sqlc.arg(candidate_limit)::int;

-- name: FindSimilarSceneTagEvidence :many
-- Which tags two scenes have in common, through the ontology, and HOW they are in common.
--
-- One row per (candidate, a tag the candidate carries, the subject tag it relates to). The
-- service turns these into reasons, so the label a client renders names something a reader
-- can open -- which is the whole point of an explained recommendation.
--
-- BOTH DIRECTIONS, as one union, with `depth` and `direction` kept so the service can tell an
-- exact match (depth 0, 'down') from an instance (depth >= 1, 'down') from a family relative
-- ('up'). `up` starts at depth 1 on purpose: a tag must not arrive twice with two different
-- explanations, and depth 0 is the subject's own tag, which the `down` arm already reports.
-- A cycle cannot occur -- migration 111 refuses one at the database -- so a tag cannot be both
-- directions of the same pair.
WITH RECURSIVE subject_tags AS (
    SELECT st.tag_id
    FROM scene_tags st
    WHERE st.scene_id = sqlc.arg(subject_id)::uuid
),
down(root_id, tag_id, depth) AS (
    SELECT tag_id, tag_id, 0 FROM subject_tags
    UNION
    SELECT d.root_id, ti.tag_id, d.depth + 1
    FROM down d
    JOIN tag_implications ti ON ti.implied_tag_id = d.tag_id
    WHERE d.depth < 64
),
up(root_id, tag_id, depth) AS (
    SELECT ti.tag_id, ti.implied_tag_id, 1
    FROM tag_implications ti
    JOIN subject_tags st ON st.tag_id = ti.tag_id
    UNION
    SELECT u.root_id, ti.implied_tag_id, u.depth + 1
    FROM up u
    JOIN tag_implications ti ON ti.tag_id = u.tag_id
    WHERE u.depth < 64
),
evidence AS (
    SELECT st.scene_id, d.tag_id, d.root_id, 'down'::text AS direction, d.depth
    FROM down d
    JOIN scene_tags st ON st.tag_id = d.tag_id
    WHERE st.scene_id = ANY(sqlc.arg(candidate_ids)::uuid[])
    UNION ALL
    SELECT st.scene_id, u.tag_id, u.root_id, 'up'::text AS direction, u.depth
    FROM up u
    JOIN scene_tags st ON st.tag_id = u.tag_id
    WHERE st.scene_id = ANY(sqlc.arg(candidate_ids)::uuid[])
)
SELECT
    e.scene_id,
    e.tag_id,
    t.name AS tag_name,
    e.root_id,
    rt.name AS root_name,
    e.direction,
    e.depth
FROM evidence e
JOIN tags t ON t.id = e.tag_id
JOIN tags rt ON rt.id = e.root_id
-- Deterministic, so a reason list is stable between two identical reads: the service picks the
-- shallowest explanation for a tag, and ties are broken by direction and then by name.
ORDER BY e.scene_id, e.depth ASC, e.direction ASC, rt.name ASC, t.name ASC;

-- name: FindSimilarSceneSharedPerformers :many
-- Performers who appear in both scenes, by name, so the reason can name someone.
--
-- Soft-deleted performers are excluded on BOTH sides -- here and in the candidate counts --
-- because two counts that disagree is the bug this schema keeps producing: the reason list
-- would name four people while the count said five, and a client showing both is showing a
-- contradiction.
SELECT
    sp.scene_id,
    sp.performer_id,
    p.name AS performer_name
FROM scene_performers sp
JOIN performers p ON p.id = sp.performer_id AND NOT p.deleted
WHERE sp.scene_id = ANY(sqlc.arg(candidate_ids)::uuid[])
  AND sp.performer_id IN (
      SELECT subject_sp.performer_id
      FROM scene_performers subject_sp
      JOIN performers sp2 ON sp2.id = subject_sp.performer_id AND NOT sp2.deleted
      WHERE subject_sp.scene_id = sqlc.arg(subject_id)::uuid
  )
ORDER BY sp.scene_id, p.name ASC;
