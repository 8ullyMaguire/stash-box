package scene

import (
	"context"
	"fmt"
	"strings"

	sq "github.com/Masterminds/squirrel"
	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/auth"
	"github.com/stashapp/stash-box/internal/converter"
	"github.com/stashapp/stash-box/internal/models"
)

// Facets answers "what else is in here" for a query: for each tag its result set carries, how
// many of its scenes a filter on that tag would return (docs/spec/feature-discovery.md section
// 6).
//
// THE COUNTS COME THROUGH THE IMPLICATION CLOSURE, in the same direction a filter walks it. A
// scene tagged only `cowgirl_position` counts for `woman_on_top`, because filtering by
// `woman_on_top` finds it. Counting direct rows instead is the bug this shape exists to avoid:
// on an ontology where most items carry the specific tag, the general tags -- the ones a reader
// browses by -- would report nearly nothing, and the number beside a facet would disagree with
// the number a reader gets by clicking it. A figure a reader can check and find wrong is worse
// than no figure.
//
// THE SEED IS buildSceneQuery, so the counts are scoped by exactly the filter the results were
// scoped by -- through the same code, not a copy of it. That is why the seed appears here as a
// subquery rather than as a hand-written WHERE clause, and why this statement is hand-built
// rather than sqlc-generated: the seed is dynamic.
//
// The tags the filter ALREADY applies are left out: a facet list offers what a reader has not
// decided yet, and "the tag you filtered by" is not a suggestion. A tag that only IMPLIES a
// filtered tag stays in, and is usually the most useful row -- filtering `woman_on_top` and
// being told `cowgirl_position` is what people actually tagged answers the question the
// hierarchy creates. EXCLUDED tags stay in too: "the thing you just ruled out is how 40% of
// these are tagged" is a fact, and hiding it would make the list quietly editorial.
func (s *Scene) Facets(ctx context.Context, input models.SceneQueryInput, limit int, minCount int) (*models.TagFacets, error) {
	user := auth.GetCurrentUser(ctx)

	filter, _, noMatch, err := s.lowerQueryLanguage(ctx, input)
	if err != nil {
		return nil, err
	}
	if noMatch {
		// A term nobody could apply empties the results, and an empty result set has no facets.
		// Counting the pre-lowering filter would describe a different query.
		return &models.TagFacets{Tags: []models.TagFacet{}}, nil
	}

	psql := sq.StatementBuilder.PlaceholderFormat(sq.Dollar)

	seed, err := s.buildSceneQuery(ctx, psql, filter, nil, user.ID, true)
	if err != nil {
		return nil, err
	}

	seedSQL, seedArgs, err := seed.ToSql()
	if err != nil {
		return nil, err
	}

	// The seed's own arguments are kept: the "how much is tagged at all" count below uses the
	// same seed and none of this statement's trailing arguments.
	args := append([]any{}, seedArgs...)

	// The tags the filter requires, so they are not offered back.
	var applied []uuid.UUID
	if filter.Tags != nil {
		applied = filter.Tags.Value
	}

	args = append(args, minCount)
	minPlaceholder := fmt.Sprintf("$%d", len(args))

	args = append(args, limit)
	limitPlaceholder := fmt.Sprintf("$%d", len(args))

	exclusion := ""
	if len(applied) > 0 {
		args = append(args, applied)
		exclusion = fmt.Sprintf("\n  AND NOT (c.tag_id = ANY($%d::uuid[]))", len(args))
	}

	// `result_scenes` is referenced once, so the seed's placeholders lead the arguments and the
	// three appended above follow in the order they appear in the text.
	statement := fmt.Sprintf(`-- name: SceneTagFacets
WITH RECURSIVE result_scenes AS (
%s
),
carried AS (
    SELECT DISTINCT st.scene_id, st.tag_id
    FROM scene_tags st
    JOIN result_scenes rs ON rs.id = st.scene_id
),
closure AS (
    SELECT scene_id, tag_id FROM carried

    UNION

    SELECT c.scene_id, ti.implied_tag_id
    FROM closure c
    JOIN tag_implications ti ON ti.tag_id = c.tag_id
)
SELECT c.tag_id, COUNT(DISTINCT c.scene_id)::bigint AS count
FROM closure c
JOIN tags t ON t.id = c.tag_id AND t.deleted = false
WHERE 1 = 1%s
GROUP BY c.tag_id
HAVING COUNT(DISTINCT c.scene_id) >= %s
ORDER BY count DESC, c.tag_id
LIMIT %s`, indent(seedSQL), exclusion, minPlaceholder, limitPlaceholder)

	// Scanned directly rather than through queryhelper.ExecuteQuery: that helper takes a
	// SelectBuilder, and a recursive CTE whose seed is a generated subquery is not expressible as
	// one (squirrel v1.5 has no With). The statement carries its own `-- name:` so traces still
	// label it.
	rows, err := s.queries.DB().Query(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	order := make([]uuid.UUID, 0, limit)
	counts := make(map[uuid.UUID]int, limit)
	for rows.Next() {
		var tagID uuid.UUID
		var count int64
		if err := rows.Scan(&tagID, &count); err != nil {
			return nil, err
		}
		order = append(order, tagID)
		counts[tagID] = int(count)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(order) == 0 {
		return &models.TagFacets{Tags: []models.TagFacet{}}, nil
	}

	// How much of the result set is tagged AT ALL. A client rendering "40% of these" needs the
	// denominator, and summing the facet counts would count a scene once per tag it carries.
	var tagged int
	countStatement := fmt.Sprintf(`-- name: SceneTaggedCount
WITH RECURSIVE result_scenes AS (
%s
)
SELECT COUNT(DISTINCT st.scene_id)::int AS count
FROM scene_tags st
JOIN result_scenes rs ON rs.id = st.scene_id`, indent(seedSQL))
	if err := s.queries.DB().QueryRow(ctx, countStatement, seedArgs...).Scan(&tagged); err != nil {
		return nil, err
	}

	tags, err := s.queries.FindTagsByIds(ctx, order)
	if err != nil {
		return nil, err
	}

	byID := make(map[uuid.UUID]models.Tag, len(tags))
	for _, tag := range tags {
		byID[tag.ID] = converter.TagToModel(tag)
	}

	// The denominator of `strength`: every scene in the INSTANCE a filter on each of these tags
	// would return. One query for the whole list, not one per facet.
	instanceCounts, err := s.queries.CountScenesForTagsThroughImplications(ctx, order)
	if err != nil {
		return nil, err
	}

	total := make(map[uuid.UUID]int64, len(instanceCounts))
	for _, row := range instanceCounts {
		total[row.TagID] = row.Count
	}

	facets := make([]models.TagFacet, 0, len(order))
	for _, id := range order {
		tag, ok := byID[id]
		if !ok {
			// A tag deleted between the two statements is left out rather than rendered with an
			// empty name.
			continue
		}

		facet := models.TagFacet{
			Tag:   tag,
			Count: counts[id],
		}
		if total[id] > 0 {
			facet.Strength = float64(counts[id]) / float64(total[id])
		}

		facets = append(facets, facet)
	}

	return &models.TagFacets{Tags: facets, TaggedCount: tagged}, nil
}

// indent keeps the generated seed readable inside the CTE in a log or a failure message.
func indent(sql string) string {
	lines := strings.Split(sql, "\n")
	for i, line := range lines {
		lines[i] = "    " + line
	}
	return strings.Join(lines, "\n")
}
