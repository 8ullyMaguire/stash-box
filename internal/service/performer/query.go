package performer

import (
	"context"
	"fmt"
	"strings"

	sq "github.com/Masterminds/squirrel"
	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/auth"
	"github.com/stashapp/stash-box/internal/models"
	queryhelper "github.com/stashapp/stash-box/internal/service/query"
)

func (s *Performer) Query(ctx context.Context, input models.PerformerQueryInput) ([]models.Performer, error) {
	user := auth.GetCurrentUser(ctx)

	psql := sq.StatementBuilder.PlaceholderFormat(sq.Dollar)
	query := s.buildPerformerQuery(psql, input, user.ID, false)

	// Apply sort
	query = s.applyPerformerSort(query, input)

	// Apply pagination
	query = queryhelper.ApplyPagination(query, input.Page, input.PerPage)

	ids, err := queryhelper.ExecuteIDQuery(ctx, query, s.queries.DB(), "QueryPerformers")
	if err != nil {
		return nil, err
	}

	performerPtrs, loadErrs := s.LoadIds(ctx, ids)
	for _, loadErr := range loadErrs {
		if loadErr != nil {
			return nil, loadErr
		}
	}
	performers := make([]models.Performer, 0, len(performerPtrs))
	for _, performer := range performerPtrs {
		if performer != nil {
			performers = append(performers, *performer)
		}
	}

	return performers, nil
}

func (s *Performer) QueryCount(ctx context.Context, input models.PerformerQueryInput) (int, error) {
	user := auth.GetCurrentUser(ctx)

	psql := sq.StatementBuilder.PlaceholderFormat(sq.Dollar)
	query := s.buildPerformerQuery(psql, input, user.ID, true)

	// Custom plan: the performer count's selectivity depends on the filter values, and a
	// generic plan is then computed over the wrong number of rows. Upstream PR #1280.
	return queryhelper.ExecuteCountCustomPlan(ctx, query, s.queries.DB(), "QueryPerformersCount")
}

func (s *Performer) buildPerformerQuery(psql sq.StatementBuilderType, input models.PerformerQueryInput, userID uuid.UUID, forCount bool) sq.SelectBuilder {
	var query sq.SelectBuilder
	needsStudioJoin := input.StudioID != nil

	// A network/parent studio holds no scenes of its own -- its content lives
	// on its sub-studios. Matching studio_id exactly therefore returns nothing
	// for a network page (#974), while the sibling "All Scenes" tab does show
	// content because it filters on studios.parent_studio_id.
	//
	// So the studio filter covers the studio itself AND its direct children,
	// mirroring the scene query's ParentStudio filter exactly, so the two tabs
	// on a network page cannot disagree about what belongs to the network.
	//
	// Deliberately one level, matching that scene filter. A recursive walk
	// would make the two paths disagree in the other direction, and the data
	// model treats networks as a single level (search triggers join
	// TP ON T.parent_studio_id = TP.id).
	//
	// scenes holds studio_id; studios holds its own id and parent_studio_id.
	// Both are bound to the same studio id.
	studioFilter := "(scenes.studio_id = ? OR studios.parent_studio_id = ?)"

	// Build base query with studio join if needed
	if forCount {
		if needsStudioJoin {
			query = psql.Select("COUNT(DISTINCT performers.id)").From("performers").
				Join(`(
					SELECT performer_id, MIN(date) as debut, MAX(date) AS last_scene, COUNT(*) as scene_count
					FROM scene_performers
					JOIN scenes ON scene_id = id
					JOIN studios ON scenes.studio_id = studios.id
					WHERE `+studioFilter+`
					GROUP BY performer_id
				) D ON performers.id = D.performer_id`, input.StudioID, input.StudioID)
		} else {
			query = psql.Select("COUNT(*)").From("performers")
		}
	} else {
		if needsStudioJoin {
			query = psql.Select("performers.id").From("performers").
				Join(`(
					SELECT performer_id, MIN(date) as debut, MAX(date) AS last_scene, COUNT(*) as scene_count
					FROM scene_performers
					JOIN scenes ON scene_id = id
					JOIN studios ON scenes.studio_id = studios.id
					WHERE `+studioFilter+`
					GROUP BY performer_id
				) D ON performers.id = D.performer_id`, input.StudioID, input.StudioID)
		} else {
			query = psql.Select("performers.id").From("performers")
		}
	}

	// Filter by URL
	if input.URL != nil && *input.URL != "" {
		query = query.
			Join("performer_urls ON performers.id = performer_urls.performer_id").
			Where(sq.Eq{"performer_urls.url": *input.URL})
	}

	// Filter by name only
	if input.Name != nil && *input.Name != "" {
		searchTerm := "%" + *input.Name + "%"
		query = query.Where(sq.ILike{"performers.name": searchTerm})
	}

	// Filter by names (searches name and disambiguation)
	if input.Names != nil && *input.Names != "" {
		searchTerm := "%" + *input.Names + "%"
		query = query.Where(sq.Or{
			sq.ILike{"performers.name": searchTerm},
			sq.ILike{"performers.disambiguation": searchTerm},
		})
	}

	// Filter by birth year
	if input.BirthYear != nil {
		query = queryhelper.ApplyIntCriterion(query, "EXTRACT(YEAR FROM to_date(performers.birthdate, 'YYYY-MM-DD'))::int", input.BirthYear)
	}

	// Filter by birthdate
	if input.Birthdate != nil {
		query = queryhelper.ApplyDateCriterion(query, "performers.birthdate", input.Birthdate)
	}

	// Filter by deathdate
	if input.Deathdate != nil {
		query = queryhelper.ApplyDateCriterion(query, "performers.deathdate", input.Deathdate)
	}

	// Filter by age
	if input.Age != nil {
		ageExpr := "EXTRACT(YEAR FROM AGE(COALESCE(to_date(performers.deathdate, 'YYYY-MM-DD'), CURRENT_DATE), to_date(performers.birthdate, 'YYYY-MM-DD')))::int"
		query = queryhelper.ApplyIntCriterion(query, ageExpr, input.Age)
	}

	// Filter by weight
	if input.Weight != nil {
		query = queryhelper.ApplyIntCriterion(query, "performers.weight", input.Weight)
	}

	// Filter by gender
	if input.Gender != nil && *input.Gender != "" {
		if *input.Gender == models.GenderFilterEnumUnknown {
			query = query.Where("performers.gender IS NULL")
		} else {
			query = query.Where(sq.Eq{"performers.gender": input.Gender.String()})
		}
	}

	// Filter by ethnicity
	if input.Ethnicity != nil && *input.Ethnicity != "" {
		if *input.Ethnicity == models.EthnicityFilterEnumUnknown {
			query = query.Where("performers.ethnicity IS NULL")
		} else {
			query = query.Where(sq.Eq{"performers.ethnicity": input.Ethnicity.String()})
		}
	}

	// Filter by favorite status
	if input.IsFavorite != nil {
		if *input.IsFavorite {
			query = query.
				Join("performer_favorites F ON performers.id = F.performer_id").
				Where(sq.Eq{"F.user_id": userID})
		} else {
			query = query.
				LeftJoin("performer_favorites F ON performers.id = F.performer_id AND F.user_id = ?", userID).
				Where("F.performer_id IS NULL")
		}
	}

	// Filter by performed with
	if input.PerformedWith != nil {
		subquery := `
			performers.id IN (
				SELECT SP.performer_id FROM scene_performers SP
				JOIN scene_performers SPP ON SP.scene_id = SPP.scene_id
				WHERE SPP.performer_id = ? AND SP.performer_id != ?
				GROUP BY SP.performer_id
			)`
		query = query.Where(sq.Expr(subquery, input.PerformedWith, input.PerformedWith))
	}

	// String criteria
	if input.Disambiguation != nil {
		query = queryhelper.ApplyStringCriterion(query, "disambiguation", input.Disambiguation)
	}
	if input.Country != nil {
		query = queryhelper.ApplyStringCriterion(query, "country", input.Country)
	}
	if input.CupSize != nil {
		query = queryhelper.ApplyStringCriterion(query, "performers.cup_size", input.CupSize)
	}

	// Enum criteria. Nullable columns, so IS NULL is a real query (#829): these
	// were accepted by the schema and then silently dropped, so a query setting
	// them returned every performer instead of the filtered set.
	if input.EyeColor != nil {
		query = queryhelper.ApplyEnumCriterion(query, "performers.eye_color", input.EyeColor.Value, input.EyeColor.Modifier)
	}
	if input.HairColor != nil {
		query = queryhelper.ApplyEnumCriterion(query, "performers.hair_color", input.HairColor.Value, input.HairColor.Modifier)
	}
	if input.BreastType != nil {
		query = queryhelper.ApplyEnumCriterion(query, "performers.breast_type", input.BreastType.Value, input.BreastType.Modifier)
	}

	// Int criteria
	if input.Height != nil {
		query = queryhelper.ApplyIntCriterion(query, "performers.height", input.Height)
	}
	if input.BandSize != nil {
		query = queryhelper.ApplyIntCriterion(query, "performers.band_size", input.BandSize)
	}
	if input.WaistSize != nil {
		query = queryhelper.ApplyIntCriterion(query, "performers.waist_size", input.WaistSize)
	}
	if input.HipSize != nil {
		query = queryhelper.ApplyIntCriterion(query, "performers.hip_size", input.HipSize)
	}
	if input.CareerStartYear != nil {
		query = queryhelper.ApplyIntCriterion(query, "performers.career_start_year", input.CareerStartYear)
	}
	if input.CareerEndYear != nil {
		query = queryhelper.ApplyIntCriterion(query, "performers.career_end_year", input.CareerEndYear)
	}

	// Body modifications (tattoos, piercings) are relational tables.
	if input.Tattoos != nil {
		query = queryhelper.ApplyBodyModificationCriterion(query, "performer_tattoos", "performer_id", input.Tattoos)
	}
	if input.Piercings != nil {
		query = queryhelper.ApplyBodyModificationCriterion(query, "performer_piercings", "performer_id", input.Piercings)
	}

	// Only non-deleted performers
	// Only non-deleted performers. Inlined rather than `sq.Eq`, so the predicate is a
	// literal instead of a bind parameter -- upstream PR #1280.
	query = query.Where("deleted = false")

	return query
}

func (s *Performer) applyPerformerSort(query sq.SelectBuilder, input models.PerformerQueryInput) sq.SelectBuilder {
	sortField := "name"
	sortDir := "ASC"
	if input.Direction != "" {
		sortDir = strings.ToUpper(input.Direction.String())
	}

	needsStudioJoin := input.StudioID != nil

	switch input.Sort {
	case models.PerformerSortEnumDebut:
		if !needsStudioJoin {
			query = query.LeftJoin(`(
				SELECT performer_id, MIN(date) as debut
				FROM scene_performers
				JOIN scenes ON scene_id = id
				GROUP BY performer_id
			) D ON performers.id = D.performer_id`)
		}
		return query.OrderBy(fmt.Sprintf("debut %s NULLS LAST, name %s", sortDir, sortDir))
	case models.PerformerSortEnumLastScene:
		if !needsStudioJoin {
			query = query.LeftJoin(`(
				SELECT performer_id, MAX(date) as last_scene
				FROM scene_performers
				JOIN scenes ON scene_id = id
				GROUP BY performer_id
			) D ON performers.id = D.performer_id`)
		}
		return query.OrderBy(fmt.Sprintf("last_scene %s NULLS LAST, name %s", sortDir, sortDir))
	case models.PerformerSortEnumSharedSceneCount:
		if input.PerformedWith != nil {
			query = query.LeftJoin(`(
				SELECT SP.performer_id, COUNT(*) as shared_scene_count
				FROM scene_performers SP
				JOIN scene_performers SPP ON SPP.scene_id = SP.scene_id AND SPP.performer_id = ?
				JOIN scenes ON scenes.id = SP.scene_id AND scenes.deleted = false
				GROUP BY SP.performer_id
			) SS ON performers.id = SS.performer_id`, input.PerformedWith)
			return query.OrderBy(fmt.Sprintf("COALESCE(shared_scene_count, 0) %s, name %s", sortDir, sortDir))
		}
		fallthrough
	case models.PerformerSortEnumSceneCount:
		if !needsStudioJoin {
			query = query.LeftJoin(`(
				SELECT performer_id, COUNT(*) as scene_count
				FROM scene_performers
				GROUP BY performer_id
			) D ON performers.id = D.performer_id`)
		}
		return query.OrderBy(fmt.Sprintf("COALESCE(scene_count, 0) %s, name %s", sortDir, sortDir))
	case models.PerformerSortEnumPopularity:
		query = query.LeftJoin("performer_popularity_all_time ON performers.id = performer_popularity_all_time.performer_id")
		return query.OrderBy(fmt.Sprintf("COALESCE(performer_popularity_all_time.user_count, 0) %s, name %s", sortDir, sortDir))
	default:
		if input.Sort != "" {
			sortField = strings.ToLower(input.Sort.String())
		}
		return query.OrderBy(fmt.Sprintf("%s %s", sortField, sortDir))
	}
}
