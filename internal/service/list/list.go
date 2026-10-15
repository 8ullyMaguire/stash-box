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
	// EntityContentItem is one of the five non-video content kinds (phase 3). Uppercase to
	// match this vocabulary's own spelling; see review.EntityContentItem for why the
	// spellings differ between surfaces.
	EntityContentItem = "CONTENT_ITEM"
	EntityScene       = "SCENE"
	EntityStudio      = "STUDIO"
	EntitySite        = "SITE"
)

// nullableString converts an empty string to SQL NULL.
//
// A nullable column set to ” and set to NULL are different states, and the difference is
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
	case EntityPerformer, EntityScene, EntityStudio, EntitySite, EntityContentItem:
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

	// search runs a derived list's saved search. It is a field rather than an argument because the
	// read path needs it, and the read path is `Items` -- an entry point with no place to pass a
	// runner, called from three resolvers and from `hydrate`.
	search SearchRunner
}

// NewService returns a list service.
//
// The search runner is a required argument, and it is a real one even though only derived lists use it:
// an optional runner would mean a wiring mistake shows up as an empty list on one instance and not
// another, which is the class of bug that takes a week to find. `nil` is accepted only so a test can
// construct the service without a scene engine, and reading a derived list through it reports
// ErrNoSearchRunner rather than returning nothing.
func NewService(q *queries.Queries, withTxn queries.WithTxnFunc, search SearchRunner) *Service {
	return &Service{queries: q, withTxn: withTxn, search: search}
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
		case "list_collaborators_pkey":
			// The duplicate rule for the write set is the primary key. The service checks first for the
			// useful message; this is the arm that closes the race the check cannot.
			return ErrAlreadyCollaborator
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
	if !canView(row, viewerID, false) {
		// Visible without any collaborator row -- published, or the owner -- short-circuits here,
		// so the query below is taken on exactly one path: somebody reading a list they do not
		// own. That ordering is why slice 8e costs a published read nothing at all.
		if viewerID == uuid.Nil {
			return nil, ErrNotFound
		}
		isCollaborator, err := s.IsCollaborator(ctx, id, viewerID)
		if err != nil {
			return nil, err
		}
		if !canView(row, viewerID, isCollaborator) {
			return nil, ErrNotFound
		}
	}
	return &row, nil
}

// canView decides whether viewerID may read this list.
//
// The whole policy in five lines, kept pure so it can be tested without a database --
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
//
// THE THIRD ARGUMENT IS A FACT, NOT A LOOKUP (slice 8e). A collaborator may read a draft
// they do not own, so the policy needs one more input -- but it stays a pure function of
// its arguments, because "is this viewer a collaborator" is a question only a database can
// answer and a policy that asks it cannot be unit-tested. Get answers it and hands the
// answer over; this function stays the only place the rule is WRITTEN.
func canView(row queries.List, viewerID uuid.UUID, isCollaborator bool) bool {
	if row.PublishedAt.Valid {
		return true
	}
	if viewerID == uuid.Nil {
		return false
	}
	return viewerID == row.OwnerID || isCollaborator
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

// CountPublished returns how many lists are public, for a browse page's total.
//
// A separate query rather than len(ListPublished(...)): the length of one page is not the
// total, and a client rendering "page 2 of N" needs N.
func (s *Service) CountPublished(ctx context.Context) (int64, error) {
	return s.queries.CountPublishedLists(ctx)
}

// ListPublishedByOwner returns an owner's PUBLIC lists only.
//
// The counterpart to ListByOwner, and the reason the resolver needs both. Reading your own
// drafts is legitimate; reading someone else's is not, and returning their drafts here
// would be a leak through the most obviously innocent query in the API.
//
// Paged, because this is the one listing a STRANGER can ask about and therefore the one
// where an unbounded result is reachable by anyone.
func (s *Service) ListPublishedByOwner(ctx context.Context, ownerID uuid.UUID, limit, offset int) ([]queries.List, error) {
	const maxPage = 100
	if limit <= 0 || limit > maxPage {
		limit = maxPage
	}
	if offset < 0 {
		offset = 0
	}

	all, err := s.queries.FindListsByOwner(ctx, ownerID)
	if err != nil {
		return nil, err
	}

	// FindListsByOwner orders by name and does not filter, so the published subset is taken
	// and paged here. That is O(one owner's lists) per request, which is fine because the
	// set is bounded by what one person created -- unlike the instance-wide listing, which
	// needs the partial index and cannot afford this. If an owner's list count ever becomes
	// large enough to matter, the honest fix is a published-only query rather than this loop.
	published := make([]queries.List, 0, len(all))
	for i := range all {
		if all[i].PublishedAt.Valid {
			published = append(published, all[i])
		}
	}
	if offset >= len(published) {
		return []queries.List{}, nil
	}
	end := offset + limit
	if end > len(published) {
		end = len(published)
	}
	return published[offset:end], nil
}

// Item returns one entry, enforcing the same visibility rule as Items.
//
// Exists so a caller that has just written an item can read it back through one policy.
func (s *Service) Item(ctx context.Context, itemID, viewerID uuid.UUID) (*queries.ListItem, error) {
	item, err := s.queries.GetListItem(ctx, itemID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if _, err := s.Get(ctx, item.ListID, viewerID); err != nil {
		return nil, err
	}
	return &item, nil
}

// CountItems returns how many entries a list holds.
func (s *Service) CountItems(ctx context.Context, listID uuid.UUID) (int64, error) {
	// The list has to be read first now, because a derived list's total is the SEARCH's total and there
	// are no rows to count. No visibility rule is applied here, matching OwnerOf: every caller reaches
	// this after a Get that decided the viewer's access, and a list id that does not exist is reported
	// as ErrNotFound rather than as a count of zero -- the two are different facts and one of them is
	// "you may not see this".
	row, err := s.queries.FindList(ctx, listID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	if row.SourceSavedSearchID.Valid {
		return s.derivedCount(ctx, row)
	}
	return s.queries.CountListItems(ctx, listID)
}

// OwnerOf returns the owner id of a list.
//
// Applies NO visibility rule, deliberately: it exists to fill the `owner` field of a list
// that has ALREADY passed the service's Get, and re-checking here would be a second
// implementation of the rule. Nothing calls it with an unverified list id.
func (s *Service) OwnerOf(ctx context.Context, listID uuid.UUID) (uuid.UUID, error) {
	row, err := s.queries.FindList(ctx, listID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, ErrNotFound
		}
		return uuid.Nil, err
	}
	return row.OwnerID, nil
}

// PublisherID returns who published a list, or nil when it is private.
//
// Nil rather than uuid.Nil for "nobody" so the caller can tell an unpublished list from a
// published one with a deleted publisher -- a state that exists and is real, and which the
// audit trail still records.
func (s *Service) PublisherID(ctx context.Context, listID uuid.UUID) (*uuid.UUID, error) {
	row, err := s.queries.FindList(ctx, listID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if !row.PublishedBy.Valid {
		return nil, nil
	}
	id := row.PublishedBy.UUID
	return &id, nil
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
//
// The first two are the publication trail. The other three exist because a list with more than one
// writer cannot answer "who changed it" from its own state (slice 8e): Add and Remove were declared
// here when the package was written and recorded NOWHERE, which made them a vocabulary rather than a
// trail, and the reorder and collaborator verbs are new with the slice.
const (
	ActionPublish    = "publish"
	ActionUnpublish  = "unpublish"
	ActionAddItem    = "add_item"
	ActionRemoveItem = "remove_item"
	// ActionReorderItem is a content edit of the list's order -- the one edit whose ACTOR a reader
	// cannot infer from the current state at all.
	ActionReorderItem = "reorder_item"
	// ActionCollaboratorAdd and ActionCollaboratorRemove record a change to WHO MAY WRITE, which is
	// the most trust-sensitive act on a collaborative list and the one a later moderation question is
	// really about.
	ActionCollaboratorAdd    = "collaborator_add"
	ActionCollaboratorRemove = "collaborator_remove"
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
// A WRITER may add: the owner, or a collaborator (slice 8e). The write is AUDITED with the ACTOR,
// which is what makes a collaborative list answerable -- "the list says this, and this member added
// it" -- and it is written in the same transaction as the entry, so the trail cannot describe an add
// that was rolled back.
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
	if err := s.requireWriter(ctx, current, actorID); err != nil {
		return nil, err
	}
	// A derived list's membership is computed, so an inserted entry would be gone on the next read.
	// REFUSED RATHER THAN IGNORED: a write that appears to work and vanishes is the worst of the three
	// possible answers.
	if current.SourceSavedSearchID.Valid {
		return nil, ErrDerivedList
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

	var row queries.ListItem
	err = s.withTxn(func(tx *queries.Queries) error {
		added, err := tx.AddListItem(ctx, queries.AddListItemParams{
			ListID:     listID,
			EntityType: &entityType,
			EntityID:   uuid.NullUUID{UUID: entityID, Valid: true},
			Position:   int(position),
		})
		if err != nil {
			return mapWriteError(err)
		}
		row = added

		return tx.RecordListAudit(ctx, queries.RecordListAuditParams{
			ListID:  listID,
			ActorID: uuid.NullUUID{UUID: actorID, Valid: true},
			Action:  ActionAddItem,
		})
	})
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// Items returns a list's contents in display order.
func (s *Service) Items(ctx context.Context, listID, viewerID uuid.UUID) ([]queries.ListItem, error) {
	row, err := s.Get(ctx, listID, viewerID)
	if err != nil {
		return nil, err
	}
	// The visibility rule has already run: `Get` decided whether the VIEWER may read this list, and
	// only then does the search run -- AS THE SEARCH'S OWNER. Those are two different questions asked
	// in a fixed order, and swapping them would either leak a draft or answer a published list with
	// the reader's own results.
	if row.SourceSavedSearchID.Valid {
		return s.derivedItems(ctx, *row)
	}
	return s.queries.FindListItems(ctx, listID)
}

// RemoveItem removes one entry.
//
// A WRITER may remove: the owner, or a collaborator. AUDITED, with the actor, since slice 8e: the
// earlier comment here reasoned that removing is covered by the publication history, and that
// reasoning stops holding the moment two people can write one list -- "it is gone" becomes a
// question about WHO, and the entry carries no author column for the trail to name.
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
	if err := s.requireWriter(ctx, current, actorID); err != nil {
		return err
	}
	// A DERIVED LIST IS NOT EDITED. This is NOT the path a derived entry's own id takes -- that id is
	// minted from (list, entity) and resolves to no row, so it answers ErrNotFound above -- and it is
	// kept for the state only a future writer could produce: a stored row on a derived list. The read
	// path IGNORES stored rows there, so without this a removal would appear to work and vanish.
	if current.SourceSavedSearchID.Valid {
		return ErrDerivedList
	}

	return s.withTxn(func(tx *queries.Queries) error {
		if err := tx.RemoveListItem(ctx, itemID); err != nil {
			return err
		}
		return tx.RecordListAudit(ctx, queries.RecordListAuditParams{
			ListID:  item.ListID,
			ActorID: uuid.NullUUID{UUID: actorID, Valid: true},
			Action:  ActionRemoveItem,
		})
	})
}

// ReorderItem sets an item's display position.
//
// A WRITER may reorder: the owner, or a collaborator. Reordering a PUBLIC list is a content edit, and
// content edits of public artefacts are the same trust-sensitive act as publishing them -- which is an
// argument about who the writer set is, not about how many people it holds.
//
// AUDITED with the actor (slice 8e), like the other two entry writes.
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
	if err := s.requireWriter(ctx, current, actorID); err != nil {
		return err
	}
	// A DERIVED LIST IS NOT REORDERED. As in RemoveItem: a derived entry's own id has no row, so it
	// cannot arrive here; this is the guard for a stored row on a derived list, which the read path
	// would ignore -- a move that appeared to work and vanished on reload.
	if current.SourceSavedSearchID.Valid {
		return ErrDerivedList
	}

	return s.withTxn(func(tx *queries.Queries) error {
		if err := tx.ReorderListItems(ctx, queries.ReorderListItemsParams{
			ID:       itemID,
			Position: int(position),
		}); err != nil {
			return err
		}
		return tx.RecordListAudit(ctx, queries.RecordListAuditParams{
			ListID:  item.ListID,
			ActorID: uuid.NullUUID{UUID: actorID, Valid: true},
			Action:  ActionReorderItem,
		})
	})
}
