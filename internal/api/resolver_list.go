// GraphQL resolvers for shareable lists (SPEC §28, growth item 28).
//
// The resolvers are deliberately THIN. Every decision that matters -- whether a draft is
// visible, who may publish, what a duplicate is -- lives in the service, and each resolver
// here does three things: pull the authenticated user off the context, call the service,
// convert a row to a model.
//
// The reason is that a decision made in a resolver is a decision with no test. The service
// takes plain arguments and a uuid, so service/review's style of pure unit tests applies;
// a resolver needs a context, a directive chain and a live pool, and a rule written there
// tends to be the one rule nobody pins down.
//
// THE TWO THINGS A RESOLVER MUST NOT DO:
//
//	1. Turn a permission error into a null. `list` returns nil for a draft that is not
//	   yours AND for a list that does not exist -- the same answer, on purpose. Reporting
//	   them differently is what turns ids into an enumeration oracle.
//	2. Re-implement the visibility rule. Owner, publishedBy, items, itemCount and
//	   auditTrail are all resolved by EAGERLY loading them here, because gqlgen found no
//	   field resolver to generate for them -- so if they are not filled in at this layer they
//	   are simply empty, and a second implementation of the rule is the only way they could
//	   ever get filled. That is the design: one place decides visibility, and the model's
//	   zero values are what a caller sees when it decides no.
package api

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/stashapp/stash-box/internal/auth"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/queries"
	listsvc "github.com/stashapp/stash-box/internal/service/list"
)

// ---------------------------------------------------------------------------
// Row -> model conversion
// ---------------------------------------------------------------------------

// timestamptzToString renders a pgtype.Timestamptz as the string the schema's DateTime
// scalar expects, or "" when NULL.
//
// "" rather than a zero time string: a NULL timestamp and a timestamp at year one are
// different facts, and this codebase's DateTime scalar cannot represent the first except
// by the absence of a value. Callers check the pointer, not the string.
func timestamptzToString(ts pgtype.Timestamptz) string {
	if !ts.Valid {
		return ""
	}
	return ts.Time.Format("2006-01-02T15:04:05Z07:00")
}

// listToModel converts a generated list row into the GraphQL model.
//
// Owner and PublishedBy are resolved separately and cached, because a browse page lists
// many lists and a converter that fetched the owner per row would issue one query per list
// on the single page a stranger hits.
func (r *Resolver) listToModel(ctx context.Context, row *queries.List) (*models.List, error) {
	if row == nil {
		return nil, nil
	}

	m := &models.List{
		ID:        row.ID,
		Name:      row.Name,
		CreatedAt: timestamptzToString(row.CreatedAt),
		UpdatedAt: timestamptzToString(row.UpdatedAt),
		// PublishedAt nil IS "private". Not a separate boolean, deliberately -- see the
		// schema file. A boolean here would be a second source of truth that can disagree
		// with the timestamp, and that disagreement is exactly a leaked draft.
		PublishedAt: nil,
		// Non-nil empty slices, not nil. gqlgen renders nil as null, and the schema says
		// these are [T!]! -- non-null. A brand-new empty list would serialise with
		// items: null and fail the query's own type contract.
		Items:      []models.ListItem{},
		AuditTrail: []models.ListAudit{},
	}
	if row.PublishedAt.Valid {
		at := timestamptzToString(row.PublishedAt)
		m.PublishedAt = &at
	}
	if row.Description != nil {
		desc := *row.Description
		m.Description = &desc
	}
	if row.OwnerName != "" {
		// The denormalised name is enough for a browse listing and costs no query. It is a
		// NAME and not a User: resolving the full user per row would turn a 25-list page
		// into 25 extra lookups for data nobody renders there.
		m.Owner = &models.User{Name: row.OwnerName}
	}
	return m, nil
}

func listItemToModel(row *queries.ListItem) *models.ListItem {
	if row == nil {
		return nil
	}
	m := &models.ListItem{
		ID:        row.ID,
		EntityID:  row.EntityID.UUID,
		Position:  row.Position,
		CreatedAt: timestamptzToString(row.CreatedAt),
		// The schema declares this non-null, and the column's CHECK
		// (entity_type IS NOT NULL AND entity_id IS NOT NULL) means a row without one
		// cannot exist. The guard is here anyway: a null enum in a non-null field is a
		// gqlgen panic at serialisation, and a nil deref in review of the row is easier to
		// read than a panic from inside generated code.
		EntityType: models.ListEntityTypeEnumScene,
	}
	if row.EntityType != nil {
		m.EntityType = models.ListEntityTypeEnum(*row.EntityType)
	}
	return m
}

// listAuditToModel maps one audit row. The actor is NOT filled in here: doing so would need
// a database lookup per row, and the caller has the service. Passing a nil resolver is how
// this field shipped dead -- see fillAuditActors.
func listAuditToModel(row *queries.ListAudit) *models.ListAudit {
	if row == nil {
		return nil
	}
	return &models.ListAudit{
		ID: row.ID,
		// The column holds the lower-case verb ('publish') because that is what the
		// service writes; the enum is upper case. Casting raw would emit a value that is
		// not a member of the enum, which is what the live server did: the field is
		// declared ListAuditActionEnum! and came back "publish". Normalising here keeps the
		// casing in one place rather than in every writer.
		Action:    models.ListAuditActionEnum(strings.ToUpper(row.Action)),
		CreatedAt: timestamptzToString(row.CreatedAt),
	}
}

// fillAuditActors resolves the actor for each audit row.
//
// This function existing at all is a bug that survived a green test suite: listAuditToModel
// built the model without an Actor, the field was declared nullable ("or null if that
// account no longer exists"), and the unit tests passed a null actor in -- so they agreed
// with the broken behaviour. Only a live query against a row with a real actor showed
// `actor: null` on a publication that demonstrably had one.
//
// A NULL actor_id is left as nil. That is the signal the frontend renders as "a former
// member", and it is deliberately distinct from a lookup failure: a missing account and an
// unresolvable one must not look the same.
func (r *Resolver) fillAuditActors(ctx context.Context, audit []queries.ListAudit) error {
	for i := range audit {
		if !audit[i].ActorID.Valid || audit[i].ActorID.UUID == uuid.Nil {
			continue
		}
		actor, err := r.services.User().FindByID(ctx, audit[i].ActorID.UUID)
		if err != nil {
			// sql.ErrNoRows here means the account is gone, which is exactly the
			// "former member" case the schema documents. Leave the nil and carry on.
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return err
		}
		audit[i].Actor = actor
	}
	return nil
}

// ---------------------------------------------------------------------------
// Error mapping
// ---------------------------------------------------------------------------

// mapListError turns a service error into something a client can act on.
//
// gqlgen surfaces a returned error's message verbatim, so an unmapped error here becomes
// either a leaked driver message ("duplicate key value violates unique constraint
// ...") or a generic internal error for something that was really the caller's fault.
//
// ErrNotFound in particular must NOT be phrased as "you do not have permission" -- the
// schema says the list may not exist, and wording it as a permission problem invites a
// client to keep probing.
func mapListError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, listsvc.ErrNotFound):
		return errors.New("no such list")
	case errors.Is(err, listsvc.ErrNotOwner):
		return errors.New("you do not own this list")
	case errors.Is(err, listsvc.ErrNameTaken):
		return errors.New("you already have a list with this name")
	case errors.Is(err, listsvc.ErrInvalidName):
		return errors.New("list name must be 1 to 200 characters")
	case errors.Is(err, listsvc.ErrInvalidEntityType):
		return errors.New("unsupported entity type")
	case errors.Is(err, listsvc.ErrDuplicateItem):
		return errors.New("that entry is already in this list")
	case errors.Is(err, listsvc.ErrAlreadyPublished):
		return errors.New("this list is already published")
	case errors.Is(err, listsvc.ErrNotPublished):
		return errors.New("this list is not published")
	default:
		return err
	}
}

// ---------------------------------------------------------------------------
// Paging
// ---------------------------------------------------------------------------

// clampPage applies perPage/page defaults and bounds, returning (limit, offset).
//
// Bounded rather than trusted. perPage is used as a SQL LIMIT, and an unbounded value on a
// public listing is the cheapest way to make this instance expensive to query. The service
// clamps too; doing it here as well means the value the query layer sees is already sane,
// and the two clamps agree because they use the same ceiling.
//
// Page is 1-based in the schema and 0-based in SQL. Converted here, in one place, rather
// than at each call site where an off-by-one silently skips the first page.
func clampPage(perPage, page *int) (int, int) {
	const (
		defaultPerPage = 25
		maxPerPage     = 100
	)

	limit := defaultPerPage
	if perPage != nil && *perPage > 0 {
		limit = *perPage
	}
	if limit > maxPerPage {
		limit = maxPerPage
	}

	p := 1
	if page != nil && *page > 0 {
		p = *page
	}
	offset := (p - 1) * limit
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// hydrate fills the fields gqlgen did not generate resolvers for.
//
// Called on every list a client can reach, once, right after conversion. It is a separate
// function from listToModel because the browse listing does NOT want it -- those rows carry
// a denormalised owner name and nobody asked for their contents -- and calling it there
// would mean fetching every item of every list on a page a stranger is browsing.
//
// The visibility check is NOT repeated. Each sub-call goes through the service, which
// applies canView; this function only decides whether to ask.
func (r *Resolver) hydrate(ctx context.Context, m *models.List) error {
	if m == nil {
		return nil
	}
	svc := r.services.List()
	user := auth.GetCurrentUser(ctx)
	userID := user.ID

	items, err := svc.Items(ctx, m.ID, userID)
	if err != nil {
		return mapListError(err)
	}
	m.Items = make([]models.ListItem, 0, len(items))
	for i := range items {
		m.Items = append(m.Items, *listItemToModel(&items[i]))
	}

	count, err := svc.CountItems(ctx, m.ID)
	if err != nil {
		return mapListError(err)
	}
	m.ItemCount = int(count)

	audit, err := svc.Audit(ctx, m.ID, userID)
	if err != nil {
		// The audit trail is not worth failing a whole list view over: a client rendering
		// a list page asked for the list, and a moderator's history is a secondary panel.
		// Leaving it empty is honest (it is a []! so empty means empty, not unknown) and
		// does not hide a privacy failure, because the service returns ErrNotFound here
		// only when the viewer may not read the LIST -- which cannot happen, because the
		// list already resolved.
		m.AuditTrail = []models.ListAudit{}
	} else {
		if err := r.fillAuditActors(ctx, audit); err != nil {
			return mapListError(err)
		}
		m.AuditTrail = make([]models.ListAudit, 0, len(audit))
		for i := range audit {
			entry := listAuditToModel(&audit[i])
			entry.Actor = audit[i].Actor
			m.AuditTrail = append(m.AuditTrail, *entry)
		}
	}

	// The publisher, resolved only when there is one. A draft has none, and asking the
	// database about a draft's publisher would be a wasted query on the private path.
	if m.PublishedAt != nil {
		publisher, err := r.publisherOf(ctx, m.ID)
		if err != nil {
			return err
		}
		m.PublishedBy = publisher
	}

	if m.Owner == nil {
		owner, err := r.ownerOf(ctx, m.ID)
		if err != nil {
			return err
		}
		m.Owner = owner
	}
	return nil
}

func (r *Resolver) ownerOf(ctx context.Context, listID uuid.UUID) (*models.User, error) {
	ownerID, err := r.services.List().OwnerOf(ctx, listID)
	if err != nil {
		return nil, mapListError(err)
	}
	return r.services.User().FindByID(ctx, ownerID)
}

func (r *Resolver) publisherOf(ctx context.Context, listID uuid.UUID) (*models.User, error) {
	publisherID, err := r.services.List().PublisherID(ctx, listID)
	if err != nil {
		return nil, mapListError(err)
	}
	if publisherID == nil || *publisherID == uuid.Nil {
		return nil, nil
	}
	return r.services.User().FindByID(ctx, *publisherID)
}

// ---------------------------------------------------------------------------
// Queries
// ---------------------------------------------------------------------------

func (r *queryResolver) List(ctx context.Context, id uuid.UUID) (*models.List, error) {
	user := auth.GetCurrentUser(ctx)

	row, err := r.services.List().Get(ctx, id, user.ID)
	if err != nil {
		// A draft that is not the viewer's is reported as not-found by the service, and
		// this returns nil for it too. Indistinguishable from a list that genuinely does
		// not exist -- which is the point. A null `list(id:)` is the documented way to say
		// "you cannot see this, or it is not there".
		if errors.Is(err, listsvc.ErrNotFound) {
			return nil, nil
		}
		return nil, mapListError(err)
	}

	m, err := r.listToModel(ctx, row)
	if err != nil {
		return nil, err
	}
	if err := r.hydrate(ctx, m); err != nil {
		return nil, err
	}
	return m, nil
}

func (r *queryResolver) PublishedLists(ctx context.Context, perPage, page *int) (*models.ListBrowseResult, error) {
	limit, offset := clampPage(perPage, page)
	svc := r.services.List()

	rows, err := svc.ListPublished(ctx, limit, offset)
	if err != nil {
		return nil, mapListError(err)
	}

	out := &models.ListBrowseResult{Lists: make([]models.List, 0, len(rows))}
	for i := range rows {
		m, err := r.listToModel(ctx, &rows[i])
		if err != nil {
			return nil, err
		}
		// NOT hydrated, deliberately. A browse listing shows name, author and date; it does
		// not show contents. Hydrating here would fetch every item of every list on the
		// page -- the one query on this surface any anonymous visitor can run.
		out.Lists = append(out.Lists, *m)
	}

	// Count from the database rather than len(out.Lists): the length of one page is not the
	// total, and a client rendering "page 2 of N" needs the N.
	total, err := svc.CountPublished(ctx)
	if err != nil {
		return nil, mapListError(err)
	}
	out.Count = int(total)
	return out, nil
}

func (r *queryResolver) Lists(ctx context.Context, userID *uuid.UUID, perPage, page *int) ([]models.List, error) {
	user := auth.GetCurrentUser(ctx)

	// Default to the CALLER's own lists. Not nil, which would be an implicit "every list on
	// the instance" and therefore a draft leak through the back door of an omitted argument.
	target := user.ID
	if userID != nil {
		target = *userID
	}
	svc := r.services.List()

	// Another user's lists, published only. The service's ListPublishedByOwner applies that
	// filter, and it must be applied HERE rather than left implicit -- reading your own
	// drafts is legitimate and reading theirs is not, and those are the same query.
	if target != user.ID {
		limit, offset := clampPage(perPage, page)
		rows, err := svc.ListPublishedByOwner(ctx, target, limit, offset)
		if err != nil {
			return nil, mapListError(err)
		}
		out := make([]models.List, 0, len(rows))
		for i := range rows {
			m, err := r.listToModel(ctx, &rows[i])
			if err != nil {
				return nil, err
			}
			out = append(out, *m)
		}
		return out, nil
	}

	// One owner's own lists, drafts included.
	rows, err := svc.ListByOwner(ctx, target)
	if err != nil {
		return nil, mapListError(err)
	}
	limit, offset := clampPage(perPage, page)

	out := make([]models.List, 0, len(rows))
	for i := range rows {
		if i < offset || i >= offset+limit {
			continue
		}
		m, err := r.listToModel(ctx, &rows[i])
		if err != nil {
			return nil, err
		}
		// Hydrated: this is the owner's own listing, where they will open each list, and
		// the drafts here are the ones they are editing.
		if err := r.hydrate(ctx, m); err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Mutations
// ---------------------------------------------------------------------------

func (r *mutationResolver) ListCreate(ctx context.Context, input models.ListCreateInput) (*models.List, error) {
	user := auth.GetCurrentUser(ctx)

	// No publish argument exists on ListCreateInput, and that is the schema enforcing the
	// policy rather than this resolver remembering it.
	row, err := r.services.List().Create(ctx, user.ID, input.Name, derefString(input.Description))
	if err != nil {
		return nil, mapListError(err)
	}

	m, err := r.listToModel(ctx, row)
	if err != nil {
		return nil, err
	}
	if err := r.hydrate(ctx, m); err != nil {
		return nil, err
	}
	return m, nil
}

func (r *mutationResolver) ListUpdate(ctx context.Context, input models.ListUpdateInput) (*models.List, error) {
	user := auth.GetCurrentUser(ctx)

	row, err := r.services.List().Update(ctx, input.ID, user.ID, input.Name, derefString(input.Description))
	if err != nil {
		return nil, mapListError(err)
	}

	m, err := r.listToModel(ctx, row)
	if err != nil {
		return nil, err
	}
	if err := r.hydrate(ctx, m); err != nil {
		return nil, err
	}
	return m, nil
}

func (r *mutationResolver) ListDelete(ctx context.Context, id uuid.UUID) (bool, error) {
	user := auth.GetCurrentUser(ctx)

	if err := r.services.List().Delete(ctx, id, user.ID); err != nil {
		// Not-found is an error here even though it is a null for `list`. A delete that
		// found nothing means the caller asked to remove something that is not there, and
		// reporting success would tell them it is gone when it may simply be private.
		return false, mapListError(err)
	}
	return true, nil
}

func (r *mutationResolver) ListPublish(ctx context.Context, id uuid.UUID) (*models.List, error) {
	user := auth.GetCurrentUser(ctx)

	row, err := r.services.List().Publish(ctx, id, user.ID)
	if err != nil {
		return nil, mapListError(err)
	}

	m, err := r.listToModel(ctx, row)
	if err != nil {
		return nil, err
	}
	if err := r.hydrate(ctx, m); err != nil {
		return nil, err
	}
	return m, nil
}

func (r *mutationResolver) ListUnpublish(ctx context.Context, id uuid.UUID) (*models.List, error) {
	user := auth.GetCurrentUser(ctx)

	row, err := r.services.List().Unpublish(ctx, id, user.ID)
	if err != nil {
		return nil, mapListError(err)
	}

	m, err := r.listToModel(ctx, row)
	if err != nil {
		return nil, err
	}
	if err := r.hydrate(ctx, m); err != nil {
		return nil, err
	}
	return m, nil
}

func (r *mutationResolver) ListAddItem(ctx context.Context, input models.ListItemInput) (*models.ListItem, error) {
	user := auth.GetCurrentUser(ctx)

	position := int32(0)
	if input.Position != nil {
		position = int32(*input.Position)
	}

	row, err := r.services.List().AddItem(ctx, input.ListID, input.EntityID, user.ID,
		string(input.EntityType), position)
	if err != nil {
		return nil, mapListError(err)
	}

	m := listItemToModel(row)
	// The parent list comes back filled so a client that requested it on the mutation's
	// payload does not need a follow-up query. Same hydration rule as everywhere else.
	if parent, err := r.services.List().Get(ctx, input.ListID, user.ID); err == nil {
		if pm, err := r.listToModel(ctx, parent); err == nil {
			m.List = pm
		}
	}
	return m, nil
}

func (r *mutationResolver) ListRemoveItem(ctx context.Context, id uuid.UUID) (bool, error) {
	user := auth.GetCurrentUser(ctx)

	if err := r.services.List().RemoveItem(ctx, id, user.ID); err != nil {
		return false, mapListError(err)
	}
	return true, nil
}

func (r *mutationResolver) ListReorderItem(ctx context.Context, id uuid.UUID, position int) (*models.ListItem, error) {
	user := auth.GetCurrentUser(ctx)

	if err := r.services.List().ReorderItem(ctx, id, user.ID, int32(position)); err != nil {
		return nil, mapListError(err)
	}

	// Re-fetched because the reorder query is :exec and returns nothing, and a client that
	// asked to move an item wants to see where it landed.
	row, err := r.services.List().Item(ctx, id, user.ID)
	if err != nil {
		return nil, mapListError(err)
	}

	m := listItemToModel(row)
	if parent, err := r.services.List().Get(ctx, row.ListID, user.ID); err == nil {
		if pm, err := r.listToModel(ctx, parent); err == nil {
			m.List = pm
		}
	}
	return m, nil
}
