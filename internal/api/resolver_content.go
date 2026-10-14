package api

import (
	"context"
	"fmt"

	"github.com/gofrs/uuid"
	"github.com/stashapp/stash-box/internal/dataloader"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/service/content"
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

// Status maps the database's lowercase status onto the enumerated vocabulary the API publishes.
//
// EXPLICIT, and not strings.ToUpper: the two vocabularies are the column's CHECK constraint and this
// enum, and a seventh status added to the column has to be an error here. Upper-casing would pass it
// through as a value no client has a name for, and the failure would surface in somebody's client
// months later instead of in this function.
//
// The zero value is deliberately NOT a valid answer (the generated type's zero value is ""), so a
// source whose status this function cannot name fails the read rather than reporting `UNKNOWN` --
// which would be a lie: UNKNOWN means "nothing has checked this URL", not "this instance does not
// understand the answer".
func (r *contentSourceResolver) Status(ctx context.Context, obj *models.ContentSource) (models.ContentSourceStatus, error) {
	switch obj.Status {
	case content.StatusUnknown:
		return models.ContentSourceStatusUnknown, nil
	case content.StatusLive:
		return models.ContentSourceStatusLive, nil
	case content.StatusIntermittent:
		return models.ContentSourceStatusIntermittent, nil
	case content.StatusDead:
		return models.ContentSourceStatusDead, nil
	case content.StatusDMCA:
		return models.ContentSourceStatusDmca, nil
	case content.StatusArchived:
		return models.ContentSourceStatusArchived, nil
	default:
		return "", fmt.Errorf("content source %s has status %q, which has no name in the API's vocabulary", obj.URL, obj.Status)
	}
}

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

// Mirror is where else this scene's content can be watched (slice 9d).
//
// The composition is here rather than in either service because the two halves are deliberately
// ignorant of each other: the fingerprint cluster answers "which scenes hold the same content", and
// content.Mirror answers "is there a live URL to hand over, and is handing over appropriate at all".
// The distance is a content-side constant, so that the meaning of "the same content" is one number in
// one place rather than a value a client happens to pass.
func (r *sceneResolver) Mirror(ctx context.Context, obj *models.Scene) (*models.ContentSource, error) {
	cluster, err := r.services.Fingerprint().ClusterScenes(ctx, obj.ID, content.MirrorDistance)
	if err != nil {
		return nil, err
	}

	var candidates []uuid.UUID
	for _, c := range cluster.Clusters {
		for _, member := range c.Members {
			for _, submission := range member.SceneSubmissions {
				// The hand-written cluster model carries the scene's ID; the SDL `scene` field
				// beside it is resolved elsewhere, and walking it here would load a scene per
				// candidate to read one uuid.
				if submission.SceneID != obj.ID {
					candidates = append(candidates, submission.SceneID)
				}
			}
		}
	}

	return r.services.Content().Mirror(ctx, obj.ID, candidates)
}
