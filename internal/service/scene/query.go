package scene

import (
	"context"
	"fmt"
	"strings"

	sq "github.com/Masterminds/squirrel"
	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/auth"
	"github.com/stashapp/stash-box/internal/models"
	queryhelper "github.com/stashapp/stash-box/internal/service/query"
	"github.com/stashapp/stash-box/internal/service/tagontology"
	"github.com/stashapp/stash-box/internal/service/trust"
)

func (s *Scene) Query(ctx context.Context, input models.SceneQueryInput) ([]models.Scene, error) {
	return s.QueryForPerformer(ctx, input, nil)
}

func (s *Scene) QueryForPerformer(ctx context.Context, input models.SceneQueryInput, performerID *uuid.UUID) ([]models.Scene, error) {
	user := auth.GetCurrentUser(ctx)

	filter, _, noMatch, err := s.lowerQueryLanguage(ctx, input)
	if err != nil {
		return nil, err
	}
	if noMatch {
		// A term in `query` asked for something the filter cannot apply. An empty LIST -- not
		// nil, the field is non-null -- is the honest answer; the reasons come from
		// LanguageDiagnostics, which computes the same list this call just did.
		return []models.Scene{}, nil
	}
	input = filter

	psql := sq.StatementBuilder.PlaceholderFormat(sq.Dollar)
	query, err := s.buildSceneQuery(ctx, psql, input, performerID, user.ID, false)
	if err != nil {
		return nil, err
	}

	ids, err := queryhelper.ExecuteIDQuery(ctx, query, s.queries.DB(), "QueryScenes")
	if err != nil {
		return nil, err
	}

	scenePtrs, loadErrs := s.LoadIds(ctx, ids)
	for _, loadErr := range loadErrs {
		if loadErr != nil {
			return nil, loadErr
		}
	}
	scenes := make([]models.Scene, 0, len(scenePtrs))
	for _, scene := range scenePtrs {
		if scene != nil {
			scenes = append(scenes, *scene)
		}
	}

	return scenes, nil
}

func (s *Scene) QueryCount(ctx context.Context, input models.SceneQueryInput) (int, error) {
	return s.QueryCountForPerformer(ctx, input, nil)
}

func (s *Scene) QueryCountForPerformer(ctx context.Context, input models.SceneQueryInput, performerID *uuid.UUID) (int, error) {
	user := auth.GetCurrentUser(ctx)

	filter, _, noMatch, err := s.lowerQueryLanguage(ctx, input)
	if err != nil {
		return 0, err
	}
	if noMatch {
		// Zero, because the query as written has no answers. The reasons are in
		// LanguageDiagnostics; a count that ignored the term it could not apply would be a
		// number for a different question (internal/service/scene/query_language.go).
		return 0, nil
	}
	input = filter

	psql := sq.StatementBuilder.PlaceholderFormat(sq.Dollar)

	innerQuery, err := s.buildSceneQuery(ctx, psql, input, performerID, user.ID, true)
	if err != nil {
		return 0, err
	}

	countQuery := psql.Select("COUNT(*)").FromSelect(innerQuery, "subquery")

	// Custom plan: an ILIKE filter makes the count's selectivity value-dependent, and a
	// generic plan is then computed over the wrong number of rows. Upstream PR #1280.
	return queryhelper.ExecuteCountCustomPlan(ctx, countQuery, s.queries.DB(), "QueryScenesCount")
}

func (s *Scene) buildSceneQuery(ctx context.Context, psql sq.StatementBuilderType, input models.SceneQueryInput, performerID *uuid.UUID, userID uuid.UUID, forCount bool) (sq.SelectBuilder, error) {
	query := psql.Select("scenes.id").From("scenes")

	// Scope to a single performer
	if performerID != nil {
		if err := queryhelper.ApplyMultiIDCriterion(&query, "scenes", "scene_performers", "scene_id", "performer_id", &models.MultiIDCriterionInput{
			Modifier: models.CriterionModifierIncludes,
			Value:    []uuid.UUID{*performerID},
		}); err != nil {
			return query, err
		}
	}

	// Filter by URL
	if input.URL != nil && *input.URL != "" {
		query = query.
			Join("scene_urls ON scenes.id = scene_urls.scene_id").
			Where(sq.Eq{"scene_urls.url": *input.URL})
	}

	// Filter by parent studio (recursive CTE for full hierarchy)
	if input.ParentStudio != nil {
		query = query.Where(sq.Expr("scenes.studio_id IN (WITH RECURSIVE studio_tree AS (SELECT id FROM studios WHERE id = ? UNION ALL SELECT s.id FROM studios s JOIN studio_tree st ON s.parent_studio_id = st.id) SELECT id FROM studio_tree)", *input.ParentStudio))
	}

	// Filter by performers
	if input.Performers != nil && len(input.Performers.Value) > 0 {
		if err := queryhelper.ApplyMultiIDCriterion(&query, "scenes", "scene_performers", "scene_id", "performer_id", input.Performers); err != nil {
			return query, err
		}
	}

	// Filter by tags
	if input.Tags != nil && len(input.Tags.Value) > 0 {
		// The tag ontology is a hierarchy, so a tag filter names what a reader MEANS and the
		// criterion matches anything that implies it: filtering by `woman_on_top` has to
		// return the things tagged `cowgirl_position`. One query expands the whole filter
		// (tagontology.Expand); an empty expansion would leave the criterion as itself, which
		// is what it did before the hierarchy existed.
		expansion, err := tagontology.New(s.queries).Expand(ctx, input.Tags.Value)
		if err != nil {
			return query, err
		}
		if err := queryhelper.ApplyMultiIDCriterionExpanded(&query, "scenes", "scene_tags", "scene_id", "tag_id", input.Tags, expansion); err != nil {
			return query, err
		}
	}

	// Filter by EXCLUDED tags. The expansion is the same one an include uses, and the criterion
	// builds NOT EXISTS over it: excluding `sex` has to exclude what IMPLIES `sex`, or the
	// hierarchy would be useless in the one direction where a reader needs it.
	//
	// The modifier is forced rather than read from the input: the FIELD's name is the modifier
	// ("exclude_tags"), and honouring an `INCLUDES` sent here would silently invert the filter.
	if input.ExcludeTags != nil && len(input.ExcludeTags.Value) > 0 {
		expansion, err := tagontology.New(s.queries).Expand(ctx, input.ExcludeTags.Value)
		if err != nil {
			return query, err
		}

		excludes := *input.ExcludeTags
		excludes.Modifier = models.CriterionModifierExcludes

		if err := queryhelper.ApplyMultiIDCriterionExpanded(&query, "scenes", "scene_tags", "scene_id", "tag_id", &excludes, expansion); err != nil {
			return query, err
		}
	}

	// Filter by duration in seconds. `scenes.duration` is the integer column the sort already
	// orders on; a comparison excludes NULL by SQL's own rules, which is the right reading of
	// "longer than ten minutes" for a scene whose duration nobody recorded.
	if input.Duration != nil {
		query = queryhelper.ApplyIntCriterion(query, "scenes.duration", input.Duration)
	}

	// Filter to what ARRIVED at this instance since an instant. `scenes.created_at`, not
	// `scenes.date`: the release date is when the scene was made, this is when it got here,
	// and a saved search's "anything new?" is a question about this instance.
	if input.CreatedSince != nil {
		query = query.Where(sq.GtOrEq{"scenes.created_at": *input.CreatedSince})
	}

	// Filter by fingerprints
	if input.Fingerprints != nil && len(input.Fingerprints.Value) > 0 {
		// Duplicate hashes are dropped so that the "includes all" count below
		// matches the number of hashes the subquery can actually return.
		seen := make(map[int64]struct{}, len(input.Fingerprints.Value))
		var args []any
		for _, hash := range input.Fingerprints.Value {
			h, err := models.UnmarshalFingerprintHash(hash)
			if err != nil {
				return query, fmt.Errorf("invalid fingerprint hash %q: %w", hash, err)
			}
			if _, dup := seen[h.Int64()]; dup {
				continue
			}
			seen[h.Int64()] = struct{}{}
			args = append(args, h.Int64())
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")

		matchingScene := fmt.Sprintf(`(
			SELECT 1
			FROM scene_fingerprints SFP
			JOIN fingerprints FP ON SFP.fingerprint_id = FP.id
			WHERE SFP.scene_id = scenes.id AND FP.hash IN (%s)
		)`, placeholders)

		// For a single value, "includes all" is identical to "includes".
		mod := input.Fingerprints.Modifier
		if mod == models.CriterionModifierIncludesAll && len(args) == 1 {
			mod = models.CriterionModifierIncludes
		}

		switch mod {
		case models.CriterionModifierIncludes:
			query = query.Where("EXISTS "+matchingScene, args...)
		case models.CriterionModifierExcludes:
			query = query.Where("NOT EXISTS "+matchingScene, args...)
		case models.CriterionModifierIncludesAll:
			// len > 1 only; "match all of these" has no semi-join equivalent.
			query = query.Join(fmt.Sprintf(`(
				SELECT SFP.scene_id
				FROM scene_fingerprints SFP
				JOIN fingerprints FP ON SFP.fingerprint_id = FP.id
				WHERE FP.hash IN (%s)
				GROUP BY SFP.scene_id
				HAVING COUNT(DISTINCT FP.hash) = %d
			) fingerprint_filter ON scenes.id = fingerprint_filter.scene_id`, placeholders, len(args)), args...)
		default:
			return query, fmt.Errorf("unsupported modifier %s for scene_fingerprints.hash", input.Fingerprints.Modifier)
		}
	}

	// Filter by has fingerprint submissions
	if input.HasFingerprintSubmissions != nil && *input.HasFingerprintSubmissions {
		query = query.Join(`(
			SELECT scene_id
			FROM scene_fingerprints
			WHERE user_id = ?
			GROUP BY scene_id
		) SFP ON scenes.id = SFP.scene_id`, userID)
	}

	// Deprecated free-text filter. Matched against the BM25 title index rather
	// than a substring scan, since callers pass whole titles.
	if input.Text != nil && *input.Text != "" {
		query = query.
			Join("scene_search ON scene_search.scene_id = scenes.id").
			Where(
				"scene_search.scene_id @@@ paradedb.match(field => 'scene_title', value => ?, conjunction_mode => true)",
				*input.Text,
			)
	}

	// Filter by title only
	if input.Title != nil && *input.Title != "" {
		searchTerm := "%" + *input.Title + "%"
		query = query.Where(sq.ILike{"scenes.title": searchTerm})
	}

	// Filter by code
	if input.Code != nil {
		query = queryhelper.ApplyStringCriterion(query, "scenes.code", input.Code)
	}

	// Filter by scene id
	if input.ID != nil && len(input.ID.Value) > 0 {
		switch input.ID.Modifier {
		case models.CriterionModifierEquals:
			query = query.Where(sq.Eq{"scenes.id": input.ID.Value[0]})
		case models.CriterionModifierNotEquals:
			query = query.Where(sq.NotEq{"scenes.id": input.ID.Value[0]})
		case models.CriterionModifierIncludes:
			query = query.Where(sq.Eq{"scenes.id": input.ID.Value})
		case models.CriterionModifierExcludes:
			query = query.Where(sq.NotEq{"scenes.id": input.ID.Value})
		default:
			return query, fmt.Errorf("unsupported modifier %s for scenes.id", input.ID.Modifier)
		}
	}

	// Filter by studios
	if input.Studios != nil && len(input.Studios.Value) > 0 {
		switch input.Studios.Modifier {
		case models.CriterionModifierEquals:
			query = query.Where(sq.Eq{"scenes.studio_id": input.Studios.Value[0]})
		case models.CriterionModifierNotEquals:
			query = query.Where(sq.NotEq{"scenes.studio_id": input.Studios.Value[0]})
		case models.CriterionModifierIsNull:
			query = query.Where("scenes.studio_id IS NULL")
		case models.CriterionModifierNotNull:
			query = query.Where("scenes.studio_id IS NOT NULL")
		case models.CriterionModifierIncludes:
			query = query.Where(sq.Eq{"scenes.studio_id": input.Studios.Value})
		case models.CriterionModifierExcludes:
			query = query.Where(sq.Or{sq.Expr("scenes.studio_id IS NULL"), sq.NotEq{"scenes.studio_id": input.Studios.Value}})
		default:
			return query, fmt.Errorf("unsupported modifier %s for scenes.studio_id", input.Studios.Modifier)
		}
	}

	// Filter by date
	if input.Date != nil {
		switch input.Date.Modifier {
		case models.CriterionModifierEquals:
			query = query.Where(sq.Eq{"scenes.date": input.Date.Value})
		case models.CriterionModifierNotEquals:
			query = query.Where(sq.NotEq{"scenes.date": input.Date.Value})
		case models.CriterionModifierGreaterThan:
			query = query.Where(sq.Gt{"scenes.date": input.Date.Value})
		case models.CriterionModifierLessThan:
			query = query.Where(sq.Lt{"scenes.date": input.Date.Value})
		case models.CriterionModifierIsNull:
			query = query.Where("scenes.date IS NULL")
		case models.CriterionModifierNotNull:
			query = query.Where("scenes.date IS NOT NULL")
		default:
			return query, fmt.Errorf("unsupported modifier %s for scenes.date", input.Date.Modifier)
		}
	}

	// Filter by favorites
	if input.Favorites != nil {
		var clauses []string
		var args []any

		if *input.Favorites == models.FavoriteFilterPerformer || *input.Favorites == models.FavoriteFilterAll {
			clauses = append(clauses, `(
				SELECT scene_id FROM performer_favorites PF
				JOIN scene_performers SP ON PF.performer_id = SP.performer_id
				WHERE PF.user_id = ?
			)`)
			args = append(args, userID)
		}
		if *input.Favorites == models.FavoriteFilterStudio || *input.Favorites == models.FavoriteFilterAll {
			clauses = append(clauses, `(
				SELECT S.id FROM studio_favorites SF
				JOIN scenes S ON SF.studio_id = S.studio_id
				WHERE SF.user_id = ?
			)`)
			args = append(args, userID)
		}

		if len(clauses) > 0 {
			query = query.Where(sq.Expr("scenes.id IN ("+strings.Join(clauses, " UNION ")+")", args...))
		}
	}

	// Only non-deleted scenes
	// Only non-deleted scenes. Inlined rather than `sq.Eq`, so the predicate is a literal
	// instead of a bind parameter -- upstream PR #1280.
	query = query.Where("scenes.deleted = false")

	// CONTENT TIERS (phase 5, migration 120).
	//
	// A scene's tier is the MAXIMUM over its tags, and a tier can only REMOVE visibility --
	// so this predicate excludes and never includes. It is a NOT EXISTS against the link
	// table rather than a join to the entity_effective_tier view: an aggregate over every
	// scene is the wrong shape for a filtered page, and this form uses the index on
	// (scene_id) and stops at the first tag above the ceiling.
	//
	// The comparison (> visibleTier) is the same one trust.EvaluateContentAccess makes for
	// a single entity. Two implementations of one rule is one too many; they are kept in
	// step by both being this comparison and by the tests that walk the same fixture
	// through the page path and the gate.
	//
	// Skipped entirely at the ceiling: a viewer who has opted into every tier has nothing
	// to exclude, and an unconsented viewer's visibleTier is 0, which still pays the
	// predicate -- the normal case must not be the cheap one.
	visibleTier, err := s.queries.UserVisibleTier(ctx, userID)
	if err != nil {
		return query, err
	}
	tierFiltered := visibleTier < trust.MaxContentTier
	if tierFiltered {
		query = query.Where(sq.Expr(
			`NOT EXISTS (SELECT 1 FROM scene_tags st JOIN tags t ON t.id = st.tag_id WHERE st.scene_id = scenes.id AND t.content_tier > ?)`,
			visibleTier))
	}

	// Apply sort and pagination
	switch input.Sort {
	case models.SceneSortEnumFresh, models.SceneSortEnumRising:
		// The freshness materialized view (migration 114). Like POPULARITY above, the LEFT JOIN
		// is what makes a scene with no signals still a row in the result: a scene nobody has
		// engaged with yet belongs at the END of a freshness feed, not missing from it.
		//
		// Read from the VIEW and never from scene_fingerprints/list_items/reviews directly: the
		// view is where the decay was evaluated, at the refresh's clock. Deciding the score in
		// the query instead would give two pages of one feed two different rankings.
		query = query.LeftJoin("scene_freshness ON scenes.id = scene_freshness.scene_id")

		if !forCount {
			sortDir := "DESC"
			if input.Direction != "" {
				sortDir = strings.ToUpper(input.Direction.String())
			}

			if input.Sort == models.SceneSortEnumRising {
				// Rising is a COMPARISON of two windows, not a bigger score: activity in the last
				// 24 hours minus the activity in the 24 before it. A scene with one signal today
				// and none yesterday outranks a scene with ten signals two days ago, which is the
				// entire difference between this sort and FRESH.
				query = query.OrderBy(fmt.Sprintf(
					"COALESCE(scene_freshness.recent_score, 0) - COALESCE(scene_freshness.earlier_score, 0) %s, scenes.id %s",
					sortDir, sortDir))
			} else {
				query = query.OrderBy(fmt.Sprintf("COALESCE(scene_freshness.score, 0) %s, scenes.id %s", sortDir, sortDir))
			}

			query = queryhelper.ApplyPagination(query, input.Page, input.PerPage)
		}
	case models.SceneSortEnumPopularity:
		query = query.LeftJoin("scene_popularity_all_time ON scenes.id = scene_popularity_all_time.scene_id")

		if !forCount {
			sortDir := "DESC"
			if input.Direction != "" {
				sortDir = strings.ToUpper(input.Direction.String())
			}
			query = query.OrderBy(fmt.Sprintf("COALESCE(scene_popularity_all_time.user_count, 0) %s, scenes.id %s", sortDir, sortDir))
			query = queryhelper.ApplyPagination(query, input.Page, input.PerPage)
		}
	case models.SceneSortEnumTrending:
		// Check if we can optimize by limiting the trending subquery
		// This is only safe when there are no other filters applied
		// The tier predicate is a filter like any other, and the optimized path LIMITS the
		// trending subquery BEFORE the filters run -- so a page that must exclude tiered
		// scenes cannot take it. Omitting tierFiltered here returns a page whose first
		// entry is a scene the reader must not see, which looks like a bug in the tier.
		hasOtherFilters := tierFiltered ||
			performerID != nil ||
			input.URL != nil || input.ParentStudio != nil ||
			(input.Performers != nil && len(input.Performers.Value) > 0) ||
			(input.Tags != nil && len(input.Tags.Value) > 0) ||
			(input.Fingerprints != nil && len(input.Fingerprints.Value) > 0) ||
			(input.HasFingerprintSubmissions != nil && *input.HasFingerprintSubmissions) ||
			(input.Text != nil && *input.Text != "") ||
			(input.Title != nil && *input.Title != "") ||
			(input.Studios != nil && len(input.Studios.Value) > 0) ||
			(input.ID != nil && len(input.ID.Value) > 0) ||
			input.Date != nil || input.Favorites != nil ||
			input.Code != nil ||
			(input.ExcludeTags != nil && len(input.ExcludeTags.Value) > 0) ||
			input.Duration != nil ||
			input.CreatedSince != nil
		// Everything the search language can lower into is a field listed above, and the
		// language variable itself is cleared before this function is reached -- so `input.Query`
		// would always be nil here and cannot stand in for its terms. A field the language
		// learns to lower in a later slice must be added to this list with it, or the optimized
		// trending path can be taken and the filter never applied (spec section 4, slice 2's
		// note in the plan).

		if !hasOtherFilters && !forCount {
			// Optimize: limit the trending subquery directly
			// Note: Use manual pagination here since we're limiting in the subquery
			p := queryhelper.Pagination(input.Page, input.PerPage)

			query = query.Join(fmt.Sprintf(`(
				SELECT scene_id, trending_count AS count
				FROM scene_popularity_trending
				ORDER BY trending_count DESC, scene_id DESC
				LIMIT %d OFFSET %d
			) TRENDING ON scenes.id = TRENDING.scene_id`, p.Limit, p.Offset))
			query = query.OrderBy("TRENDING.count DESC, TRENDING.scene_id DESC")
			// Don't apply pagination again below since we already limited in the subquery
		} else {
			// Standard trending query without optimization
			query = query.Join(`(
				SELECT scene_id, trending_count AS count
				FROM scene_popularity_trending
			) TRENDING ON scenes.id = TRENDING.scene_id`)

			if !forCount {
				query = query.OrderBy("TRENDING.count DESC, TRENDING.scene_id DESC")
				query = queryhelper.ApplyPagination(query, input.Page, input.PerPage)
			}
		}
	case models.SceneSortEnumRandom:
		// `scenes.random` is not a column, so RANDOM MUST have its own case: falling
		// through to the default branch lowercases the enum name and builds
		// `ORDER BY scenes.random`, which fails at runtime rather than at compile time.
		// An enum member that is not a column name needs to be handled, not trusted.
		if !forCount {
			query = query.OrderBy("RANDOM()")
			// Pagination still applies. It is not a window into a stable ordering -- it
			// bounds how many rows the random draw can produce, so LIMIT is doing real
			// work here even though the ORDER BY is redrawn per request.
			query = queryhelper.ApplyPagination(query, input.Page, input.PerPage)
		}
	default:
		if !forCount {
			// Only apply sorting for non-count queries
			sortField := "title"
			sortDir := "ASC"
			if input.Sort != "" {
				sortField = strings.ToLower(input.Sort.String())
			}
			if input.Direction != "" {
				sortDir = strings.ToUpper(input.Direction.String())
			}

			secondary := "title"
			if input.Sort != models.SceneSortEnumTitle {
				secondary = "id"
			}
			nullsClause := ""
			if input.Sort == models.SceneSortEnumDuration {
				nullsClause = " NULLS LAST"
			}
			query = query.OrderBy(fmt.Sprintf("scenes.%s %s%s, scenes.%s %s", sortField, sortDir, nullsClause, secondary, sortDir))
			query = queryhelper.ApplyPagination(query, input.Page, input.PerPage)
		}
	}

	return query, nil
}
