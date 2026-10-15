package api

import (
	"context"
	"fmt"

	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/hydrus"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/pkg/logger"
)

// ExportScenes renders records as Hydrus import JSON (plan slice 13c).
//
// THE IDS ARE THE WHOLE INTERFACE, and that is the constraint rather than a simplification: an export is a
// rendering of records the caller ALREADY HAS, so there is no browsing, no query language and no
// "everything". A field that took a query would be a second read surface with its own paging, its own
// ceilings and its own rate limit to keep in step with the ones the instance already has.
const maxExportRecords = 100

func (r *queryResolver) ExportScenes(ctx context.Context, ids []uuid.UUID) (*models.HydrusExport, error) {
	if len(ids) == 0 {
		// An empty document, not an error: asking for nothing and getting nothing is a correct answer, and
		// a caller assembling a library from a list it computed may legitimately have an empty list.
		return r.renderExport(ctx, nil, nil)
	}
	if len(ids) > maxExportRecords {
		return nil, fmt.Errorf("an export is bounded to %d records at a time; this one asked for %d",
			maxExportRecords, len(ids))
	}

	// THE LOADER IS THE GATE. `LoadIds` is the single place scenes are loaded by id, and it applies the
	// instance's denylist there (internal/service/scene/denylist.go): a record that is taken down or blocked
	// comes back as the nil slot the loader already uses for a missing id. A denied record is therefore
	// ABSENT from this document by the same code that makes it absent from a search, rather than by a check
	// here that a future caller could forget.
	loaded, loadErrs := r.services.Scene().LoadIds(ctx, ids)
	for i, err := range loadErrs {
		if err != nil {
			// The whole export fails rather than shipping a document with a hole whose reason is unknown:
			// the caller is about to keep this, and a partial library that looks complete is worse.
			return nil, fmt.Errorf("could not read record %d of the export: %w", i, err)
		}
	}

	return r.renderExport(ctx, ids, loaded)
}

// renderExport does the loading that needs the whole batch, then assembles the document.
//
// The bulk loaders are used wherever one exists (appearances, performers, urls), because a hundred records
// must not cost four hundred queries. Tags have no bulk read in the scene service, so they are read per
// record -- bounded by the same hundred.
func (r *queryResolver) renderExport(ctx context.Context, ids []uuid.UUID, loaded []*models.Scene) (*models.HydrusExport, error) {
	scenes := make([]models.Scene, 0, len(loaded))
	present := make([]uuid.UUID, 0, len(loaded))
	for _, scene := range loaded {
		if scene == nil {
			continue
		}
		scenes = append(scenes, *scene)
		present = append(present, scene.ID)
	}

	cast := r.performerNamesByScene(ctx, present)
	urls := r.urlsByScene(ctx, present)
	studios := map[uuid.UUID]string{}

	document := hydrus.Document{}
	for _, scene := range scenes {
		record := hydrus.Record{
			ID:         scene.ID.String(),
			Performers: cast[scene.ID],
			URLs:       urls[scene.ID],
		}
		if scene.Title != nil {
			record.Title = *scene.Title
		}
		if scene.Details != nil {
			record.Details = *scene.Details
		}
		if scene.Code != nil {
			record.Code = *scene.Code
		}
		if scene.Date != nil {
			record.Date = *scene.Date
		}

		if scene.StudioID.Valid {
			if _, seen := studios[scene.StudioID.UUID]; !seen {
				studios[scene.StudioID.UUID] = r.studioName(ctx, scene.StudioID.UUID)
			}
			record.Studio = studios[scene.StudioID.UUID]
		}

		tags, err := r.services.Scene().GetTags(ctx, scene.ID)
		if err != nil {
			return nil, fmt.Errorf("could not read the tags of a record in the export: %w", err)
		}
		record.Tags = make([]string, 0, len(tags))
		for _, tag := range tags {
			record.Tags = append(record.Tags, tag.Name)
		}

		document[record.ID] = hydrus.SidecarFor(record)
	}

	body, err := hydrus.Render(document)
	if err != nil {
		return nil, err
	}

	// WHAT IS NOT THERE, in the order it was asked for: an id that names nothing and an id the instance
	// refuses to serve are one answer, which is why there is one list (internal/service/scene/denylist.go).
	omitted := make([]uuid.UUID, 0)
	for _, id := range ids {
		if _, ok := document[id.String()]; !ok {
			omitted = append(omitted, id)
		}
	}

	return &models.HydrusExport{
		JSON:        string(body),
		RecordCount: len(document),
		Omitted:     omitted,
	}, nil
}

// performerNamesByScene is the two-step bulk read: the appearances join, then the performers themselves,
// mapped back to the scene each appearance belongs to.
func (r *queryResolver) performerNamesByScene(ctx context.Context, ids []uuid.UUID) map[uuid.UUID][]string {
	names := map[uuid.UUID][]string{}
	if len(ids) == 0 {
		return names
	}

	appearances, errs := r.services.Scene().LoadAppearances(ctx, ids)
	performerIDs := make([]uuid.UUID, 0, len(appearances))
	for i := range appearances {
		if i < len(errs) && errs[i] != nil {
			logger.Errorf("could not read the cast of a record for an export: %v", errs[i])
			continue
		}
		for _, appearance := range appearances[i] {
			performerIDs = append(performerIDs, appearance.PerformerID)
		}
	}
	if len(performerIDs) == 0 {
		return names
	}

	performers, loadErrs := r.services.Performer().LoadIds(ctx, performerIDs)
	byID := make(map[uuid.UUID]string, len(performers))
	for i, performer := range performers {
		if i < len(loadErrs) && loadErrs[i] != nil {
			logger.Errorf("could not read a performer for an export: %v", loadErrs[i])
			continue
		}
		if performer != nil {
			byID[performer.ID] = performer.Name
		}
	}

	for i, id := range ids {
		if i >= len(appearances) {
			break
		}
		for _, appearance := range appearances[i] {
			if name, ok := byID[appearance.PerformerID]; ok {
				names[id] = append(names[id], name)
			}
		}
	}
	return names
}

func (r *queryResolver) urlsByScene(ctx context.Context, ids []uuid.UUID) map[uuid.UUID][]string {
	out := map[uuid.UUID][]string{}
	if len(ids) == 0 {
		return out
	}

	urls, errs := r.services.Scene().LoadURLs(ctx, ids)
	for i := range urls {
		if i < len(errs) && errs[i] != nil {
			logger.Errorf("could not read the addresses of a record for an export: %v", errs[i])
			continue
		}
		for _, url := range urls[i] {
			out[ids[i]] = append(out[ids[i]], url.URL)
		}
	}
	return out
}

func (r *queryResolver) studioName(ctx context.Context, id uuid.UUID) string {
	studio, err := r.services.Studio().FindByID(ctx, id)
	if err != nil || studio == nil {
		// A record whose studio cannot be read is still exported: the studio is the document's least
		// important part, and losing it must not cost a caller the record.
		logger.Errorf("could not read a studio for an export: %v", err)
		return ""
	}
	return studio.Name
}
