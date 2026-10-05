// Package list implements shareable user lists (SPEC §28, growth item 28).
//
// A list is a named, ordered set of archive entities with an owner. The package exists
// because of one rule that a schema cannot express:
//
//	A LIST IS PRIVATE UNTIL ITS OWNER EXPLICITLY PUBLISHES IT.
//
// There is no implicit publication, no visibility enum, and no default-public. Every
// listing a user can discover is filtered on `published_at IS NOT NULL`, and the two
// places that are not -- a single-list read and the owner's own listing -- are the only
// ones that must do an explicit authorisation check instead.
//
// The distinction that governs the whole package:
//
//	READING a published list needs READ. EDITING a list needs WRITE on its owner,
//	regardless of whether it is published. PUBLICATION IS NOT AN EDIT -- it is the
//	trust-sensitive act, it is audited, and it is the one write a READER may not do
//	merely because they can see the list.
//
// The generated queries already refuse to make a draft public by accident:
// CreateList has no `published_at` column at all, and PublishList carries the actor in
// the same statement that sets the timestamp. What the service adds is the rule that
// decides WHO may call them.
package list

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/stashapp/stash-box/internal/queries"
)

// ErrNotFound is returned when a list id does not resolve.
var ErrNotFound = errors.New("list not found")

// ErrNotOwner is returned when someone tries to change a list that is not theirs.
//
// A distinct error from ErrNotFound and not a generic "unauthorized", for the same
// reason review.ErrNotAuthor is: the two mean different things to a client. Not-found
// says the list is gone; not-owner says it exists and is not yours. For a PRIVATE list
// both must be reported as not-found, so that a caller cannot enumerate private list ids
// by watching which error comes back -- see notFoundUnlessOwner.
var ErrNotOwner = errors.New("not the owner of this list")

// ErrNameTaken is returned when an owner already has a list with this name.
//
// Names are unique per owner, not globally: two people may both keep a list called
// "Favourites", and neither should be made to rename because of the other. The
// uniqueness is a database index; this error is what the service maps it to, because
// "duplicate key value violates unique constraint lists_unique_name_per_owner" is not
// something a GraphQL client should be shown.
var ErrNameTaken = errors.New("you already have a list with this name")

// ErrInvalidName is returned for a blank or overlong list name.
var ErrInvalidName = errors.New("list name must be 1 to 200 characters")

// ErrInvalidEntityType is returned for an entity type outside the supported set.
//
// The column is free text in the schema so that adding an entity type is a code change
// rather than a migration. The closed set is HERE, and that asymmetry is deliberate: the
// database cannot know which entity types the archive can actually resolve, and a typo
// would otherwise produce a list item pointing at nothing that any resolver will ever
// look for.
var ErrInvalidEntityType = errors.New("unsupported entity type")

// ErrListFull bounds one list, so a list cannot become an unbounded copy of the archive
// and no browse page has to order an unbounded result set.
const maxItems = 1000

// maxDescriptionLength bounds the description. Enforced here as well as by any column
// limit, so an overlong description is a clear error rather than a driver-level failure.
const maxDescriptionLength = 5000

// Entity types a list item may reference.
//
// Mirrors review's entity constants rather than importing them: a list can hold a
// performer or a studio, and a review cannot, and coupling the two would make one
// product decision force a migration in the other.
const (
	EntityPerformer = "PERFORMER"
	EntityScene     = "SCENE"
	EntityStudio    = "STUDIO"
	EntitySite      = "SITE"
)

// nullableString converts an empty string to SQL NULL.
//
// A nullable column set to '' and set to NULL are different states, and the difference is
// visible in the API: a list with no description should serialise as null so a client can
// tell "no description given" from "description is the empty string". The
// lists_description_blank_is_not_null CHECK forbids the blank case at the column level;
// this is the Go side of the same rule, applied before the write rather than caught after.
func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func validEntityType(t string) bool {
	switch t {
	case EntityPerformer, EntityScene, EntityStudio, EntitySite:
		return true
	}
	return false
}

// Service is the list service.
//
// withTxn is an injected transaction runner rather than a pool handle, matching every
// other service in this tree (service/draft, service/edit, service/image). Taking the
// pool directly would work and would be a second, differently-configured way to open a
// transaction in the same codebase.
type Service struct {
	queries *queries.Queries
	withTxn queries.WithTxnFunc
}

// NewService returns a list service.
func NewService(q *queries.Queries, withTxn queries.WithTxnFunc) *Service {
	return &Service{queries: q, withTxn: withTxn}
}

// normaliseName trims and bounds a list name, returning ErrInvalidName if unusable.
//
// Trimmed before validation rather than after: a name of "   " is not a name, and
// checking `len > 0` first would accept it. The stored value is the trimmed one so that
// "Favourites" and "Favourites " cannot both exist -- they would violate the unique index
// on (owner_id, name) at a byte level that no amount of client-side trimming prevents.
func normaliseName(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" || len(trimmed) > 200 {
		return "", ErrInvalidName
	}
	return trimmed, nil
}

// mapWriteError converts a driver error into the package's vocabulary.
//
// sqlc and pgx return *pgconn.PgError for constraint violations. A caller that receives
// the raw error learns nothing useful and, worse, may map it to a 500 and report a
// conflict as a server fault. Mapping here means every constraint this package relies on
// has a name the GraphQL layer can turn into a message.
func mapWriteError(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.ConstraintName {
		case "lists_unique_name_per_owner":
			return ErrNameTaken
		case "list_items_unique_entity":
			return fmt.Errorf("%w: that entry is already in this list", ErrDuplicateItem)
		case "list_items_entity_required", "lists_owner_name_required":
			return ErrInvalidName
		}
		// 23503 is foreign_key_violation: the list or the entity is gone. Reported as
		// not-found rather than a conflict, because from the caller's side the list they
		// named does not exist.
		if pgErr.Code == "23503" {
			return ErrNotFound
		}
	}
	return err
}

// ErrDuplicateItem is returned when a list item would duplicate an existing entry.
var ErrDuplicateItem = errors.New("that entry is already in this list")

// Create makes a new PRIVATE list.
//
// Private always, with no option to publish at creation: publication is a separate,
// audited act (SPEC §28), and a create that could publish would make the audited act
// skippable. ownerName is denormalised here because the browse listing shows the author's
// name and a JOIN per row on the public list is the one query on the hot path.
func (s *Service) Create(ctx context.Context, ownerID uuid.UUID, name, description string) (*queries.List, error) {
	clean, err := normaliseName(name)
	if err != nil {
		return nil, err
	}
	if len(description) > maxDescriptionLength {
		return nil, ErrInvalidName
	}

	// Find the owner's display name once, for the denormalised column. A missing name is
	// not an error: the users row exists (we hold its id) and the name column may be empty,
	// which is a display concern rather than a reason to refuse the create.
	var ownerName string
	if u, err := s.queries.FindUser(ctx, ownerID); err == nil {
		ownerName = u.Name
	}

	row, err := s.queries.CreateList(ctx, queries.CreateListParams{
		ID:          uuid.Must(uuid.NewV4()),
		OwnerID:     ownerID,
		Name:        clean,
		Description: nullableString(description),
		OwnerName:   ownerName,
	})
	if err != nil {
		return nil, mapWriteError(err)
	}
	return &row, nil
}

// Get returns a list by id for a reader.
//
// Visibility is decided HERE rather than in the query, because two callers need different
// answers for the same id: a reader may see a published list, and the owner may also see
// their own draft. A query-level filter could only pick one.
//
// The privacy of the not-found case: a draft belonging to someone else is reported as
// ErrNotFound, NOT ErrNotOwner. Reporting "exists but is not yours" would let a caller
// enumerate private list ids one uuid at a time.
func (s *Service) Get(ctx context.Context, id, viewerID uuid.UUID) (*queries.List, error) {
	row, err := s.queries.FindList(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if !canView(row, viewerID) {
		return nil, ErrNotFound
	}
	return &row, nil
}

// canView decides whether viewerID may read this list.
//
// The whole policy in four lines, kept pure so it can be tested without a database --
// this is the decision that decides whether a draft leaks, and burying it in a method
// that needs a live pool means it is the one rule in the package that never gets a unit
// test.
//
// The viewerID != uuid.Nil guard is not pedantry: an unauthenticated request arrives with
// the zero uuid, and a list whose owner_id happened to be the zero uuid would otherwise be
// readable by anyone. Owner ids are minted as v4 uuids so this cannot occur in practice,
// but a rule that depends on "cannot occur" is not a rule.
//
// ErrNotFound rather than ErrNotOwner for the invisible case, deliberately: returning
// "exists but is not yours" would let a caller enumerate private list ids one probe at a
// time, confirming which of a guessed uuid range are real.
func canView(row queries.List, viewerID uuid.UUID) bool {
	if row.PublishedAt.Valid {
		return true
	}
	return viewerID != uuid.Nil && viewerID == row.OwnerID
}

// ListPublished returns the public browse listing, newest publication first.
//
// The ONLY listing that needs no authorisation, because the query already excludes
// drafts. limit is clamped rather than trusted: an unbounded ORDER BY over a public table
// is the cheapest way to take this instance down, and a caller asking for 100000 is
// either a bug or an attempt.
func (s *Service) ListPublished(ctx context.Context, limit, offset int) ([]queries.List, error) {
	const maxPage = 100
	if limit <= 0 || limit > maxPage {
		limit = maxPage
	}
	if offset < 0 {
		offset = 0
	}
	return s.queries.FindPublishedLists(ctx, queries.FindPublishedListsParams{
		Limit:  int32(limit),
		Offset: int32(offset),
	})
}

// ListByOwner returns every list belonging to an owner, drafts included.
//
// Reads an owner's lists, so it needs the caller's identity to be checked by the caller;
// this method deliberately does not check that the viewer IS the owner, because the GraphQL
// layer's authz middleware already did. See FindByOwner for the version that does check.
func (s *Service) ListByOwner(ctx context.Context, ownerID uuid.UUID) ([]queries.List, error) {
	return s.queries.FindListsByOwner(ctx, ownerID)
}

// FindByOwner returns one owner's list by name, for the "you already have this" check.
func (s *Service) FindByOwner(ctx context.Context, ownerID uuid.UUID, name string) (*queries.List, error) {
	clean, err := normaliseName(name)
	if err != nil {
		return nil, err
	}
	row, err := s.queries.FindListByName(ctx, queries.FindListByNameParams{
		OwnerID: ownerID,
		Name:    clean,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &row, nil
}

// Update renames or re-describes a list.
//
// WRITE on the owner. NOT the publish path -- see the package comment. Publishing through
// this method would bypass the audit row.
func (s *Service) Update(ctx context.Context, id, actorID uuid.UUID, name, description string) (*queries.List, error) {
	clean, err := normaliseName(name)
	if err != nil {
		return nil, err
	}
	if len(description) > maxDescriptionLength {
		return nil, ErrInvalidName
	}

	// Resolve ownership first so a caller cannot use this to probe for the existence of
	// someone else's list: FindList-then-check is one query, and its error is not-found
	// for both "no such list" and "not yours".
	current, err := s.Get(ctx, id, actorID)
	if err != nil {
		return nil, err
	}
	if current.OwnerID != actorID {
		return nil, ErrNotOwner
	}

	row, err := s.queries.UpdateList(ctx, queries.UpdateListParams{
		ID:          id,
		Name:        clean,
		Description: nullableString(description),
	})
	if err != nil {
		return nil, mapWriteError(err)
	}
	return &row, nil
}

// Delete removes a list, its items and its audit trail.
//
// Cascade from the schema. Deleting an audit trail with the list is deliberate and differs
// from deleting a USER, which nulls the audit actor instead: the list's publication history
// belongs to the list, and once the list is gone the history is describing nothing. The
// user-level policy exists because a user's actions outlive their content.
func (s *Service) Delete(ctx context.Context, id, actorID uuid.UUID) error {
	current, err := s.Get(ctx, id, actorID)
	if err != nil {
		return err
	}
	if current.OwnerID != actorID {
		return ErrNotOwner
	}
	return s.queries.DeleteList(ctx, id)
}

// Publish makes a list public and records the act.
//
// Idempotent-by-refusal: PublishList is guarded on `published_at IS NULL`, so publishing
// an already-published list returns no rows and this reports ErrAlreadyPublished. Silently
// returning the existing row would let a client believe it had just published something
// it published a minute ago, and would write a second audit row describing an event that
// did not happen.
//
// The audit row is written inside a transaction with the publication. The two must agree,
// and the sqlc query already carries the actor into the same statement; the transaction
// additionally covers the separate list_audit INSERT.
func (s *Service) Publish(ctx context.Context, id, actorID uuid.UUID) (*queries.List, error) {
	current, err := s.Get(ctx, id, actorID)
	if err != nil {
		return nil, err
	}
	if current.OwnerID != actorID {
		return nil, ErrNotOwner
	}

	var row queries.List
	err = s.withTxn(func(tx *queries.Queries) error {
		published, err := tx.PublishList(ctx, queries.PublishListParams{
			ID:          id,
			PublishedBy: uuid.NullUUID{UUID: actorID, Valid: true},
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// The guard `AND published_at IS NULL` refused it: already public.
				return ErrAlreadyPublished
			}
			return mapWriteError(err)
		}
		row = published

		return tx.RecordListAudit(ctx, queries.RecordListAuditParams{
			ListID:  id,
			ActorID: uuid.NullUUID{UUID: actorID, Valid: true},
			Action:  ActionPublish,
		})
	})
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// ErrAlreadyPublished is returned when publishing a list that is already public.
var ErrAlreadyPublished = errors.New("this list is already published")

// Unpublish returns a list to private, keeping the audit history.
//
// NOT the inverse of Publish in one sense: the list_audit rows survive, because "this was
// published and then withdrawn" is the fact worth keeping, and a moderation system reading
// the history needs to know a list was once public.
func (s *Service) Unpublish(ctx context.Context, id, actorID uuid.UUID) (*queries.List, error) {
	current, err := s.Get(ctx, id, actorID)
	if err != nil {
		return nil, err
	}
	if current.OwnerID != actorID {
		return nil, ErrNotOwner
	}

	var row queries.List
	err = s.withTxn(func(tx *queries.Queries) error {
		withdrawn, err := tx.UnpublishList(ctx, id)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// Guard `AND published_at IS NOT NULL` refused it: already private.
				return ErrNotPublished
			}
			return mapWriteError(err)
		}
		row = withdrawn

		return tx.RecordListAudit(ctx, queries.RecordListAuditParams{
			ListID:  id,
			ActorID: uuid.NullUUID{UUID: actorID, Valid: true},
			Action:  ActionUnpublish,
		})
	})
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// ErrNotPublished is returned when unpublishing a list that is already private.
var ErrNotPublished = errors.New("this list is not published")

// Audit actions recorded in list_audit.
const (
	ActionPublish   = "publish"
	ActionUnpublish = "unpublish"
	ActionAddItem   = "add_item"
	ActionRemoveItem = "remove_item"
)

// Audit returns a list's publication history, newest first.
//
// Readable by anyone who can see the list. A published list's history is public because the
// publication itself is public -- "who published this and when" is not a secret about a
// public artefact. A DRAFT's history is readable only by the owner, because until it is
// published the fact that it was once public is itself private.
func (s *Service) Audit(ctx context.Context, id, viewerID uuid.UUID) ([]queries.ListAudit, error) {
	current, err := s.Get(ctx, id, viewerID)
	if err != nil {
		return nil, err
	}
	if !current.PublishedAt.Valid && current.OwnerID != viewerID {
		return nil, ErrNotFound
	}
	return s.queries.FindListAudit(ctx, id)
}

// AddItem appends an entity to a list.
//
// Rejects duplicates rather than silently no-op'ing. The database's unique index already
// refuses the second row, so a client that double-submits gets ErrDuplicateItem instead of
// a list whose displayed length disagrees with its contents.
//
// maxItems is checked with a count rather than trusted, and the count is advisory: a
// concurrent add can push the list one over the limit. That is acceptable because the
// limit exists to stop unbounded growth, not to be exact; the alternative -- a lock -- costs
// a serialised write on every add to make a bound exact by one row.
func (s *Service) AddItem(ctx context.Context, listID, entityID, actorID uuid.UUID, entityType string, position int32) (*queries.ListItem, error) {
	if !validEntityType(entityType) {
		return nil, ErrInvalidEntityType
	}

	current, err := s.Get(ctx, listID, actorID)
	if err != nil {
		return nil, err
	}
	if current.OwnerID != actorID {
		return nil, ErrNotOwner
	}

	count, err := s.queries.CountListItems(ctx, listID)
	if err != nil {
		return nil, err
	}
	if count >= maxItems {
		return nil, fmt.Errorf("a list may hold at most %d entries", maxItems)
	}

	// A negative position would sort an item ahead of every existing one and make
	// "add" order-dependent. Zero means "the end": the query returns the current length,
	// so appending twice appends twice, in order.
	if position < 0 {
		return nil, fmt.Errorf("position must not be negative")
	}
	if position == 0 {
		position = int32(count) + 1
	}

	row, err := s.queries.AddListItem(ctx, queries.AddListItemParams{
		ListID:     listID,
		EntityType: &entityType,
		EntityID:   uuid.NullUUID{UUID: entityID, Valid: true},
		Position:   int(position),
	})
	if err != nil {
		return nil, mapWriteError(err)
	}
	return &row, nil
}

// Items returns a list's contents in display order.
func (s *Service) Items(ctx context.Context, listID, viewerID uuid.UUID) ([]queries.ListItem, error) {
	if _, err := s.Get(ctx, listID, viewerID); err != nil {
		return nil, err
	}
	return s.queries.FindListItems(ctx, listID)
}

// RemoveItem removes one entry.
//
// Not audited. Adding is audited because it changes what a public list claims; removing is
// audited only if the item was in a published list, and that case is covered by the list's
// publication history remaining inspectable. A per-item trail would be noise at this scale.
func (s *Service) RemoveItem(ctx context.Context, itemID, actorID uuid.UUID) error {
	// My first version passed the ITEM id to FindListItems, which takes a LIST id, and
	// then compared the resulting list_id against actorID -- a list id against an actor id.
	// That would have read as "not the owner" and deleted nothing, silently, for every
	// caller. The bug was invisible because the function still compiled.
	item, err := s.queries.GetListItem(ctx, itemID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}

	current, err := s.Get(ctx, item.ListID, actorID)
	if err != nil {
		return err
	}
	if current.OwnerID != actorID {
		return ErrNotOwner
	}
	return s.queries.RemoveListItem(ctx, itemID)
}

// ReorderItem sets an item's display position.
//
// Owner only. Reordering a PUBLIC list is a content edit, and content edits of public
// artefacts are the same trust-sensitive act as publishing them.
func (s *Service) ReorderItem(ctx context.Context, itemID, actorID uuid.UUID, position int32) error {
	if position < 0 {
		return fmt.Errorf("position must not be negative")
	}

	// The sqlc query is keyed on the item id, so the list has to be resolved first. Using
	// FindListItems as a lookup by item id is a mistake -- it takes a LIST id -- so the
	// item is fetched through a dedicated lookup instead.
	item, err := s.queries.GetListItem(ctx, itemID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}

	current, err := s.Get(ctx, item.ListID, actorID)
	if err != nil {
		return err
	}
	if current.OwnerID != actorID {
		return ErrNotOwner
	}
	return s.queries.ReorderListItems(ctx, queries.ReorderListItemsParams{
		ID:       itemID,
		Position: int(position),
	})
}
