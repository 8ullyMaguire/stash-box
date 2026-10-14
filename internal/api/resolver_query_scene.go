package api

import (
	"context"
	"errors"
	"strings"

	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/dataloader"
	"github.com/stashapp/stash-box/internal/models"
)

func (r *queryResolver) FindScene(ctx context.Context, id uuid.UUID) (*models.Scene, error) {
	return r.services.Scene().FindByID(ctx, id)
}

func (r *queryResolver) FindScenes(ctx context.Context, ids []uuid.UUID) ([]*models.Scene, error) {
	return loadByIDs(ids, dataloader.For(ctx).SceneByID.LoadAll)
}

func (r *queryResolver) QueryScenes(ctx context.Context, input models.SceneQueryInput) (*models.SceneQuery, error) {
	return &models.SceneQuery{
		Filter: input,
	}, nil
}

func (r *queryResolver) FindScenesBySceneFingerprints(ctx context.Context, sceneFingerprints [][]models.FingerprintQueryInput) ([][]*models.Scene, error) {
	if len(sceneFingerprints) > 40 {
		return nil, errors.New("too many scenes")
	}

	sceneFingerprints = filterMD5FingerprintQueryInputs(sceneFingerprints)
	return r.services.Scene().FindScenesBySceneFingerprints(ctx, sceneFingerprints)
}

type querySceneResolver struct{ *Resolver }

func (r *querySceneResolver) Count(ctx context.Context, obj *models.SceneQuery) (int, error) {
	if obj.SearchResults != nil {
		return obj.SearchResults.Count, nil
	}
	return r.services.Scene().QueryCountForPerformer(ctx, obj.Filter, obj.PerformerID)
}

func (r *querySceneResolver) Scenes(ctx context.Context, obj *models.SceneQuery) ([]models.Scene, error) {
	if obj.SearchResults != nil {
		return obj.SearchResults.Scenes, nil
	}
	return r.services.Scene().QueryForPerformer(ctx, obj.Filter, obj.PerformerID)
}

// Diagnostics is what a `query` asked for that the filter could not apply. It is a field of its
// own so that a client renders it BESIDE the results, which are empty by construction whenever
// it is not: a term nobody could apply was not dropped, it made the answer empty, and this is
// the reason (internal/service/scene/query_language.go).
func (r *querySceneResolver) Diagnostics(ctx context.Context, obj *models.SceneQuery) ([]models.SearchDiagnostic, error) {
	diagnostics, err := r.services.Scene().LanguageDiagnostics(ctx, obj.Filter)
	if err != nil {
		return nil, err
	}

	out := make([]models.SearchDiagnostic, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		out = append(out, models.SearchDiagnostic{
			Code:    string(diagnostic.Code),
			Term:    diagnostic.Term,
			Offset:  diagnostic.Offset,
			Message: diagnostic.Message,
		})
	}

	return out, nil
}

func (r *queryResolver) QueryExistingScene(ctx context.Context, input models.QueryExistingSceneInput) (*models.QueryExistingSceneResult, error) {
	input.Fingerprints = filterMD5FingerprintInputs(input.Fingerprints)
	return &models.QueryExistingSceneResult{
		Input: input,
	}, nil
}

type queryExistingSceneResolver struct{ *Resolver }

func (r *queryExistingSceneResolver) Edits(ctx context.Context, obj *models.QueryExistingSceneResult) ([]models.Edit, error) {
	return r.services.Edit().FindPendingSceneCreation(ctx, obj.Input)
}

func (r *queryExistingSceneResolver) Scenes(ctx context.Context, obj *models.QueryExistingSceneResult) ([]models.Scene, error) {
	return r.services.Scene().FindExistingScenes(ctx, obj.Input)
}

// Deprecated: Use SearchScenes instead
func (r *queryResolver) SearchScene(ctx context.Context, term string, limit *int) ([]models.Scene, error) {
	result, err := r.searchScenes(ctx, term, limit, nil, nil)
	if err != nil {
		return nil, err
	}
	if result.SearchResults != nil {
		return result.SearchResults.Scenes, nil
	}
	return nil, nil
}

func (r *queryResolver) SearchScenes(ctx context.Context, term string, limit *int, page *int, perPage *int) (*models.SceneQuery, error) {
	return r.searchScenes(ctx, term, limit, page, perPage)
}

func (r *queryResolver) searchScenes(ctx context.Context, term string, limit *int, page *int, perPage *int) (*models.SceneQuery, error) {
	trimmedQuery := strings.TrimSpace(term)
	sceneID, err := uuid.FromString(trimmedQuery)
	if err == nil {
		var scenes []models.Scene
		scene, err := r.services.Scene().FindByID(ctx, sceneID)
		if scene != nil {
			scenes = append(scenes, *scene)
		}
		return &models.SceneQuery{
			SearchResults: &models.SceneSearchResults{
				Scenes: scenes,
				Count:  len(scenes),
			},
		}, err
	}

	searchLimit := 10
	searchOffset := 0

	if perPage != nil && *perPage > 0 {
		searchLimit = *perPage
	} else if limit != nil && *limit > 0 {
		searchLimit = *limit
	}

	if page != nil && *page > 1 {
		searchOffset = (*page - 1) * searchLimit
	}

	if strings.HasPrefix(trimmedQuery, "https://") || strings.HasPrefix(trimmedQuery, "http://") {
		scenes, err := r.services.Scene().FindByURL(ctx, trimmedQuery, searchLimit)
		return &models.SceneQuery{
			SearchResults: &models.SceneSearchResults{
				Scenes: scenes,
				Count:  len(scenes),
			},
		}, err
	}

	return r.services.Scene().SearchScenesWithCount(ctx, trimmedQuery, searchLimit, searchOffset)
}
