package api

import (
	"context"

	"github.com/stashapp/stash-box/internal/models"
)

// The tag ontology as a query surface: the whole graph, and the versions over it.
//
// `ontologyVersion` is what lets a client say which ontology produced an answer it is
// showing, and `findOntologyVersion` is what lets a reviewer read the graph as it stood
// before a change -- the two halves of "the ontology is a versioned document".

func (r *queryResolver) TagImplications(ctx context.Context) ([]models.TagImplication, error) {
	return r.services.TagOntology().List(ctx)
}

func (r *queryResolver) OntologyVersion(ctx context.Context) (int, error) {
	return r.services.TagOntology().CurrentVersion(ctx)
}

func (r *queryResolver) OntologyVersions(ctx context.Context, limit int) ([]models.OntologyVersion, error) {
	if limit <= 0 {
		return nil, nil
	}

	return r.services.TagOntology().Versions(ctx, limit)
}

func (r *queryResolver) FindOntologyVersion(ctx context.Context, version int) (*models.OntologyVersion, error) {
	return r.services.TagOntology().Version(ctx, version)
}
