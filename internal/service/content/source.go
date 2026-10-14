// Package content owns the aggregator's source records: where a piece of content
// lives, and whether it is still alive.
//
// It is not the scene service, and it deliberately does not write scenes. The one
// write path for a scene's URLs stays `scene_urls` (through the edit path), and
// migration 108 mirrors those writes into `content_sources` by trigger. This package
// reads through the `scene_sources` view, which is what keeps membership owned by
// `scene_urls` while health is owned here.
package content

import (
	"context"

	"github.com/gofrs/uuid"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/loadutil"
)

// Service reads content sources and, in later phases, owns their health state.
type Service struct {
	queries *queries.Queries
	withTxn queries.WithTxnFunc
}

// NewService creates a content Service.
func NewService(queries *queries.Queries, withTxn queries.WithTxnFunc) *Service {
	return &Service{
		queries: queries,
		withTxn: withTxn,
	}
}

// FindSceneSources returns the URLs a scene claims, canonical first.
//
// Read through the view, so a URL the scene has stopped claiming is absent even
// though its health row still exists -- the edit path replaces a scene's URL list by
// deleting every row and re-inserting it, and the health history deliberately
// survives that. Querying the table directly here would show URLs the scene no longer
// has, which is the wrong answer to the question this method is named for.
func (s *Service) FindSceneSources(ctx context.Context, sceneID uuid.UUID) ([]models.ContentSource, error) {
	rows, err := s.queries.FindSceneSources(ctx, sceneID)
	if err != nil {
		return nil, err
	}

	out := make([]models.ContentSource, 0, len(rows))
	for _, row := range rows {
		out = append(out, SceneSourceToModel(row))
	}
	return out, nil
}

// LoadSceneSources is the batched form, for the dataloader that serves Scene.sources
// when a page lists many scenes.
//
// A page of twenty scenes is one query here and was twenty without it. The rows come
// back ordered by scene_id, canonical first, and loadutil.Many preserves that order
// within each group, so the display order is decided by the query rather than by the
// order a map happens to iterate.
func (s *Service) LoadSceneSources(ctx context.Context, ids []uuid.UUID) ([][]models.ContentSource, []error) {
	return loadutil.Many(ids,
		func(ids []uuid.UUID) ([]queries.SceneSource, error) {
			return s.queries.FindSceneSourcesByIds(ctx, ids)
		},
		func(row queries.SceneSource) uuid.UUID { return row.SceneID },
		SceneSourceToModel,
	)
}

// SceneSourceToModel converts a row from the scene_sources view into the read model.
//
// Exported because the view is also read outside this package (the federated content
// path builds the same shape) and a second conversion would be a second place for the
// status vocabulary to drift.
func SceneSourceToModel(row queries.SceneSource) models.ContentSource {
	source := models.ContentSource{
		URL:           row.Url,
		SiteID:        row.SiteID,
		Platform:      row.Platform,
		Status:        row.Status,
		Canonical:     row.Canonical,
		CheckFailures: row.CheckFailures,
		ArchivedURL:   row.ArchivedUrl,
	}

	// The health row is optional: the view's LEFT JOIN means a scene_urls row whose
	// content_sources twin does not exist yet still appears, with the view's defaults.
	// `source_id` is the tell, because it is the only column the view does not
	// COALESCE.
	if row.SourceID.Valid {
		id := models.ID(row.SourceID.UUID)
		source.ID = &id
	}
	if row.FirstSeen.Valid {
		firstSeen := row.FirstSeen.Time
		source.FirstSeen = &firstSeen
	}
	if row.LastChecked.Valid {
		lastChecked := row.LastChecked.Time
		source.LastChecked = &lastChecked
	}
	if row.LastOk.Valid {
		lastOK := row.LastOk.Time
		source.LastOK = &lastOK
	}

	return source
}

// ContentSourceToModel converts the health ROW (the table, not the view).
//
// Separate from SceneSourceToModel because they answer different questions: the view's rows are
// "URLs this scene claims", where a missing health row is normal and the id is optional, and the
// table's rows are "health records", where the id is the whole point. The sweep works on the second,
// and reusing the view's converter would have made a real row look like an absent one.
func ContentSourceToModel(row queries.ContentSource) models.ContentSource {
	source := models.ContentSource{
		URL:           row.Url,
		Platform:      row.Platform,
		Status:        row.Status,
		Canonical:     row.Canonical,
		CheckFailures: row.CheckFailures,
		ArchivedURL:   row.ArchivedUrl,
	}

	id := models.ID(row.ID)
	source.ID = &id
	if row.SiteID.Valid {
		source.SiteID = row.SiteID.UUID
	}
	if row.FirstSeen.Valid {
		firstSeen := row.FirstSeen.Time
		source.FirstSeen = &firstSeen
	}
	if row.LastChecked.Valid {
		lastChecked := row.LastChecked.Time
		source.LastChecked = &lastChecked
	}
	if row.LastOk.Valid {
		lastOK := row.LastOk.Time
		source.LastOK = &lastOK
	}
	return source
}
