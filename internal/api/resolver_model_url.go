package api

import (
	"context"
	"strings"

	"github.com/stashapp/stash-box/internal/dataloader"
	"github.com/stashapp/stash-box/internal/models"
)

type urlResolver struct{ *Resolver }

func (r *urlResolver) URL(ctx context.Context, obj *models.URL) (string, error) {
	return obj.URL, nil
}

func (r *urlResolver) Site(ctx context.Context, obj *models.URL) (*models.Site, error) {
	return dataloader.For(ctx).SiteByID.Load(obj.SiteID)
}

func (r *urlResolver) Type(ctx context.Context, obj *models.URL) (string, error) {
	site, err := dataloader.For(ctx).SiteByID.Load(obj.SiteID)
	if err != nil {
		return "", err
	}

	// A missing site must not panic. Site is declared non-null, so a URL with a
	// dangling site_id should already be filtered out upstream
	// (GetMergedURLsForEdit joins sites), but this resolver is reachable from
	// any model carrying a URL and the schema's own guarantee is not something
	// to dereference blindly: a nil here is a panic that takes the request
	// handler with it, which is strictly worse than an empty string on a
	// deprecated field.
	if site == nil {
		return "", nil
	}

	return strings.ToUpper(site.Name), nil
}
