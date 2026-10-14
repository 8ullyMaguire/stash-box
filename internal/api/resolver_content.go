package api

import (
	"context"

	"github.com/gofrs/uuid"
	"github.com/stashapp/stash-box/internal/dataloader"
	"github.com/stashapp/stash-box/internal/models"
)

// The aggregator programme's read surface: a scene's URLs, with what is known about
// each one's health (docs/spec/feature-content-aggregator.md, phase 1).

// Sources resolves every URL a scene claims, canonical first.
//
// Batched through the dataloader like every other per-scene collection here, because
// the alternative is one query per scene on the browse page -- the page a browser hits
// first and the one with the most scenes on it.
//
// The list is read through the `scene_sources` view, so it is membership (owned by
// `scene_urls`, written by the edit path) LEFT JOINed against health (owned by
// `content_sources`). A URL the scene has stopped claiming is absent even though its
// health row survives; a URL whose health row is missing is present with `status:
// "unknown"`. Both of those are asserted in `scripts/verify-108.sh` at the SQL level,
// which is the only place they can be observed.
func (r *sceneResolver) Sources(ctx context.Context, obj *models.Scene) ([]models.ContentSource, error) {
	return dataloader.For(ctx).SceneSourcesByID.Load(obj.ID)
}

type contentSourceResolver struct{ *Resolver }

// ID is the source record's id, or nil when the scene claims a URL for which no health
// row exists yet.
//
// gqlgen asks for this as a resolver rather than binding the field, because the model
// carries it as `*models.ID` while the scalar's Go type is `uuid.UUID`; the conversion
// is here rather than in the model so the model does not have to know how the scalar is
// wired.
func (r *contentSourceResolver) ID(ctx context.Context, obj *models.ContentSource) (*uuid.UUID, error) {
	if obj.ID == nil {
		// Not an error and not a missing row: it is the "no health record yet" case the
		// schema documents. A nil id and a zero uuid would mean different things to a
		// client, so this returns nil rather than a zero value.
		return nil, nil
	}

	id := uuid.UUID(*obj.ID)
	return &id, nil
}

// Site resolves the directory entry for this URL's domain.
//
// Bulk-loaded, so a page showing twenty sources from the same tube site fetches that
// site once. A nil SiteID returns nil rather than an error: sources for a site the
// directory does not know are the normal case for freshly aggregated content, and a
// resolver error there would take the whole scene page down over a missing favicon.
func (r *contentSourceResolver) Site(ctx context.Context, obj *models.ContentSource) (*models.Site, error) {
	if obj.SiteID == uuid.Nil {
		return nil, nil
	}
	return dataloader.For(ctx).SiteByID.Load(obj.SiteID)
}
