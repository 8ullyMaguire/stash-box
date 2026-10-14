// Package contentitem reads content items -- the five non-video kinds (aggregator
// phase 3, slice 3c).
//
// READ-ONLY ON PURPOSE. A content item is created by ingestion or by an edit, and
// this repository has exactly one write path for shared metadata: propose, vote,
// apply. A create mutation here would be a second writer that the vote machinery
// cannot see, which is the failure the edit system exists to prevent -- so the write
// side of phase 3 belongs with the edit types and phase 2's pipeline, not with a
// service that quietly bypasses them.
package contentitem

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/stashapp/stash-box/internal/queries"
)

// Kind is one of the six content kinds. The values are the COLUMN's spellings: the
// column is what the CHECK constraint accepts, and the mapping to the GraphQL enum is
// a named pair of functions in internal/api rather than a cast.
type Kind string

const (
	KindImage     Kind = "image"
	KindGallery   Kind = "gallery"
	KindAnimation Kind = "animation"
	KindText      Kind = "text"
	KindAudio     Kind = "audio"
	KindComic     Kind = "comic"
)

// AllKinds is every kind, in the order migration 117's CHECK lists them, so a reader
// comparing the two files is not made to wonder whether a difference means something.
var AllKinds = []Kind{KindImage, KindGallery, KindAnimation, KindText, KindAudio, KindComic}

// Valid reports whether k is one of the six.
func (k Kind) Valid() bool {
	for _, known := range AllKinds {
		if k == known {
			return true
		}
	}
	return false
}

// ErrUnknownKind is returned for a spelling the table would refuse.
//
// Refused here by NAME rather than left to the database, whose answer arrives as a
// constraint violation naming a constraint -- and a caller that asked for "imgs" needs
// to be told which word was wrong, not which CHECK rejected it.
var ErrUnknownKind = errors.New("unknown content kind")

// ErrNotFound is a missing or soft-deleted item.
var ErrNotFound = errors.New("content item not found")

// ParseKind converts a stored/individual spelling to a Kind.
func ParseKind(s string) (Kind, error) {
	k := Kind(s)
	if !k.Valid() {
		return "", fmt.Errorf("%w: %q", ErrUnknownKind, s)
	}
	return k, nil
}

// Item is a stored content item.
type Item struct {
	ID          uuid.UUID
	Kind        Kind
	Title       string
	Details     *string
	ReleaseDate *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Service reads content items.
type Service struct {
	queries *queries.Queries
}

func NewService(queries *queries.Queries) *Service {
	return &Service{queries: queries}
}

// DefaultPageSize is what a caller who expressed no preference gets.
const DefaultPageSize = 25

// MaxPageSize is the largest page this service will fetch.
//
// Above it is a REFUSAL, not a silent clamp: a caller that asked for 500 and received
// 100 has a truncated result that looks complete, which is the same reasoning
// ListIncomplete records in the completion service.
const MaxPageSize = 100

// Get returns one item, or ErrNotFound for a missing or soft-deleted one.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*Item, error) {
	row, err := s.queries.GetContentItem(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return fromRow(row), nil
}

// List returns the newest items, optionally of one kind.
func (s *Service) List(ctx context.Context, kind *Kind, limit int) ([]*Item, error) {
	if limit == 0 {
		limit = DefaultPageSize
	}
	if limit < 0 {
		return nil, fmt.Errorf("page size %d is negative", limit)
	}
	if limit > MaxPageSize {
		return nil, fmt.Errorf("page size %d exceeds the maximum of %d", limit, MaxPageSize)
	}
	if kind != nil && !kind.Valid() {
		return nil, fmt.Errorf("%w: %q", ErrUnknownKind, string(*kind))
	}

	var stored *string
	if kind != nil {
		v := string(*kind)
		stored = &v
	}

	rows, err := s.queries.ListContentItems(ctx, queries.ListContentItemsParams{
		Kind:     stored,
		RowLimit: int32(limit),
	})
	if err != nil {
		return nil, err
	}

	out := make([]*Item, 0, len(rows))
	for _, r := range rows {
		out = append(out, fromRow(r))
	}
	return out, nil
}

func fromRow(row queries.ContentItem) *Item {
	item := &Item{
		ID:        row.ID,
		Kind:      Kind(row.Kind),
		Title:     row.Title,
		Details:   row.Details,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}
	if row.ReleaseDate.Valid {
		t := row.ReleaseDate.Time
		item.ReleaseDate = &t
	}
	return item
}
