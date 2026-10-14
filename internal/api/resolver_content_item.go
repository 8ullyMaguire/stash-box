package api

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/service/contentitem"
)

// Content-item resolvers (aggregator phase 3, slice 3c).
//
// The one thing worth care here is the spelling gap between the GraphQL enum and the
// database column. Both spellings are lowercase (they agree today), but the two
// directions are still a named pair of functions rather than inline casts: gqlgen
// spells its Go constants `ContentItemKindImage` while the column spells `image`, and
// a cast in the wrong direction is how `visibilityEnum`/`storedVisibility` pairs earn
// their keep everywhere else in this package. Both directions are asserted, because a
// round trip through one request would only exercise one of them.

// storedKind maps the GraphQL enum member to the column's spelling.
func storedKind(kind models.ContentItemKind) (contentitem.Kind, error) {
	return contentitem.ParseKind(strings.ToLower(string(kind)))
}

// kindEnum maps the column's spelling back to an enum member.
func kindEnum(kind contentitem.Kind) (models.ContentItemKind, error) {
	// Refuses by name rather than emitting an invalid enum member: a value the schema
	// has no member for is a client-visible string that parses as nothing.
	candidate := models.ContentItemKind(strings.ToLower(string(kind)))
	if !candidate.IsValid() {
		return "", fmt.Errorf("%w: %q reached the schema from the database", contentitem.ErrUnknownKind, string(kind))
	}
	return candidate, nil
}

// contentItemToModel converts a service item to its GraphQL shape.
//
// Returns an error for a kind the schema cannot spell: the failure is loud at the
// boundary rather than a client receiving a member that does not exist.
func contentItemToModel(item *contentitem.Item) (models.ContentItem, error) {
	kind, err := kindEnum(item.Kind)
	if err != nil {
		return models.ContentItem{}, err
	}

	out := models.ContentItem{
		ID:        item.ID,
		Kind:      kind,
		Title:     item.Title,
		Details:   item.Details,
		CreatedAt: item.CreatedAt,
	}
	if item.ReleaseDate != nil {
		// The Date scalar is a *string of a calendar date, not a timestamp; the
		// database column is a real DATE, so the time-of-day half is not merely
		// dropped, it never existed.
		date := item.ReleaseDate.Format("2006-01-02")
		out.ReleaseDate = &date
	}
	return out, nil
}

func (r *queryResolver) ContentItem(ctx context.Context, id uuid.UUID) (*models.ContentItem, error) {
	item, err := r.services.ContentItem().Get(ctx, id)
	if err != nil {
		if errors.Is(err, contentitem.ErrNotFound) {
			// Null, not an error: a soft-deleted or never-existing item is a normal
			// outcome of following a stale link.
			return nil, nil
		}
		return nil, err
	}
	out, err := contentItemToModel(item)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (r *queryResolver) QueryContentItems(ctx context.Context, kind *models.ContentItemKind, limit *int) ([]models.ContentItem, error) {
	var filter *contentitem.Kind
	if kind != nil {
		parsed, err := storedKind(*kind)
		if err != nil {
			return nil, err
		}
		filter = &parsed
	}

	// The service owns the default and the ceiling; the resolver only forwards the
	// absence of a limit rather than inventing a second page size here.
	size := 0
	if limit != nil {
		size = *limit
	}

	items, err := r.services.ContentItem().List(ctx, filter, size)
	if err != nil {
		return nil, err
	}

	out := make([]models.ContentItem, 0, len(items))
	for _, item := range items {
		converted, err := contentItemToModel(item)
		if err != nil {
			return nil, err
		}
		out = append(out, converted)
	}
	return out, nil
}

// ContentItem is the per-type resolver accessor that models.ResolverRoot requires.
func (r *Resolver) ContentItem() models.ContentItemResolver {
	return &contentItemResolver{r}
}

// contentItemResolver is the field resolver for the ContentItem type.
type contentItemResolver struct{ *Resolver }

// Completion is how complete the item is, read on demand.
//
// The aggregator phase 3 slice 3b decision is what makes this line possible: the
// completion vocabulary knows `content_item`, so a client asking for the completeness
// of an item gets a real answer instead of an unmapped type that silently scores zero.
func (r *contentItemResolver) Completion(ctx context.Context, obj *models.ContentItem) (*models.Completion, error) {
	return completionOrValue(r.services.Completion().ContentItem(ctx, obj.ID))
}
