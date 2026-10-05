// Package service provides a centralized service factory for database operations.
//
// Usage:
//
//	// Initialize the factory with a database pool
//	pool, err := pgxpool.New(context.Background(), databaseURL)
//	if err != nil {
//		log.Fatal(err)
//	}
//	factory := service.NewFactory(pool)
//
//	// Each service call creates a fresh querier instance
//	tagService := factory.Tag()
//	tag, err := tagService.FindByID(ctx, tagID)
//
//	userService := factory.User()
//	user, err := userService.FindByID(ctx, userID)
package service

import (
	"context"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stashapp/stash-box/internal/email"
	"github.com/stashapp/stash-box/internal/image/croptemplate"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/authored"
	"github.com/stashapp/stash-box/internal/service/award"
	"github.com/stashapp/stash-box/internal/service/badge"
	"github.com/stashapp/stash-box/internal/service/collage"
	"github.com/stashapp/stash-box/internal/service/completion"
	"github.com/stashapp/stash-box/internal/service/draft"
	"github.com/stashapp/stash-box/internal/service/edit"
	"github.com/stashapp/stash-box/internal/service/elo"
	listsvc "github.com/stashapp/stash-box/internal/service/list"
	"github.com/stashapp/stash-box/internal/service/federation"
	"github.com/stashapp/stash-box/internal/service/fingerprint"
	"github.com/stashapp/stash-box/internal/service/identification"
	"github.com/stashapp/stash-box/internal/service/image"
	"github.com/stashapp/stash-box/internal/service/imagetype"
	"github.com/stashapp/stash-box/internal/service/invite"
	"github.com/stashapp/stash-box/internal/service/mod_audit"
	"github.com/stashapp/stash-box/internal/service/notification"
	"github.com/stashapp/stash-box/internal/service/performer"
	"github.com/stashapp/stash-box/internal/service/quest"
	"github.com/stashapp/stash-box/internal/service/review"
	"github.com/stashapp/stash-box/internal/service/scene"
	"github.com/stashapp/stash-box/internal/service/site"
	"github.com/stashapp/stash-box/internal/service/streak"
	"github.com/stashapp/stash-box/internal/service/studio"
	"github.com/stashapp/stash-box/internal/service/tag"
	"github.com/stashapp/stash-box/internal/service/trust"
	"github.com/stashapp/stash-box/internal/service/user"
	"github.com/stashapp/stash-box/internal/service/usertoken"
)

// Factory provides access to all services with centralized database connection management
type Factory struct {
	db       *pgxpool.Pool
	withTxn  queries.WithTxnFunc
	emailMgr *email.Manager

	cropTemplates *croptemplate.Loader
}

// NewFactory creates a new service factory with the given database pool and email manager
func NewFactory(pool *pgxpool.Pool, emailMgr *email.Manager) *Factory {
	return &Factory{
		db:            pool,
		withTxn:       createWithTxnFunc(pool),
		emailMgr:      emailMgr,
		cropTemplates: croptemplate.NewLoader(),
	}
}

// Completion returns a CompletionService instance
func (f *Factory) Completion() *completion.Service {
	return completion.NewService(queries.New(f.db))
}

// Quest returns a QuestService instance.
//
// The name resolver is injected rather than imported because the completion
// service deliberately does not know about names -- it scores fields, and pulling
// entity names into it would make every score query load a join it does not need.
// So the quest package asks through a closure, and the factory is the one place
// that knows how to resolve a name for each type.
func (f *Factory) Quest() *quest.Service {
	return quest.NewService(f.Completion(), f.resolveEntityName)
}

// Authored returns an AuthoredService instance.
func (f *Factory) Authored() *authored.Service {
	return authored.NewService(queries.New(f.db), f.withTxn, f.Completion())
}

// resolveEntityName is the name lookup a quest's item carries.
//
// A missing name is not an error the quest layer should see: the id is still
// actionable and the name is only a display convenience, so a lookup failure
// yields ("", nil) and the item renders without a name rather than the quest
// failing to generate. A deleted or renamed entity is the ordinary case, not an
// exception.
func (f *Factory) resolveEntityName(ctx context.Context, entityType quest.EntityType, id uuid.UUID) (string, error) {
	q := queries.New(f.db)
	switch entityType {
	case completion.EntityPerformer:
		row, err := q.FindPerformer(ctx, id)
		if err != nil {
			return "", nil
		}
		return row.Name, nil
	case completion.EntityScene:
		row, err := q.FindScene(ctx, id)
		if err != nil {
			return "", nil
		}
		// Title is nullable -- an unidentified scene is exactly the case the
		// identification board exists for -- so it becomes an empty name rather
		// than a dereference of NULL.
		if row.Title == nil {
			return "", nil
		}
		return *row.Title, nil
	case completion.EntityStudio:
		row, err := q.FindStudio(ctx, id)
		if err != nil {
			return "", nil
		}
		return row.Name, nil
	case completion.EntitySite:
		// Sites have no single-row finder by bare id: the lookup the rest of the
		// codebase uses goes through the name index. Rather than reach past the
		// service layer into a different query just to decorate a quest item, the
		// site case resolves to an empty name -- which is what the quest already
		// renders when a name is unavailable, and the id is still actionable.
		//
		// Recorded as a gap rather than hidden: a site quest's items will read
		// "unnamed site #4821" until this is filled, and filling it properly
		// means deciding whether to add a by-id finder or resolve sites through
		// the site service.
		return "", nil
	case completion.EntityTag:
		row, err := q.FindTag(ctx, id)
		if err != nil {
			return "", nil
		}
		return row.Name, nil
	default:
		return "", nil
	}
}

// Identification returns an IdentificationService instance
func (f *Factory) Identification() *identification.Service {
	return identification.NewService(queries.New(f.db), f.withTxn)
}

// Collage returns a CollageService instance
func (f *Factory) Collage() *collage.Service {
	return collage.NewService(queries.New(f.db), f.withTxn)
}

// Elo returns an EloService instance
func (f *Factory) Elo() *elo.Elo {
	return elo.NewElo(queries.New(f.db), f.withTxn)
}

// Tag returns a TagService instance
func (f *Factory) Tag() *tag.Tag {
	return tag.NewTag(queries.New(f.db), f.withTxn)
}

// Performer returns a PerformerService instance
func (f *Factory) Performer() *performer.Performer {
	return performer.NewPerformer(queries.New(f.db), f.withTxn)
}

// Scene returns a SceneService instance
func (f *Factory) Scene() *scene.Scene {
	return scene.NewScene(queries.New(f.db), f.withTxn)
}

// Studio returns a StudioService instance
func (f *Factory) Studio() *studio.Studio {
	return studio.NewStudio(queries.New(f.db), f.withTxn)
}

// User returns a UserService instance
func (f *Factory) User() *user.User {
	return user.NewUser(queries.New(f.db), f.withTxn, f.emailMgr)
}

// UserToken returns a UserTokenService instance
func (f *Factory) UserToken() *usertoken.UserToken {
	return usertoken.NewUserToken(queries.New(f.db), f.withTxn)
}

// Site returns a SiteService instance
func (f *Factory) Site() *site.Site {
	return site.NewSite(queries.New(f.db), f.withTxn)
}

// Edit returns an EditService instance
func (f *Factory) Edit() *edit.Edit {
	return edit.NewEdit(queries.New(f.db), f.withTxn, trust.NewTrust(queries.New(f.db), f.withTxn))
}

// Image returns an ImageService instance
func (f *Factory) Image() *image.Image {
	return image.NewImage(queries.New(f.db), f.withTxn)
}

// ImageType returns an ImageTypeService instance
func (f *Factory) ImageType() *imagetype.ImageType {
	return imagetype.NewImageType(queries.New(f.db), f.withTxn)
}

// Draft returns a DraftService instance
func (f *Factory) Draft() *draft.Draft {
	return draft.NewDraft(queries.New(f.db), f.withTxn)
}

// Notification returns a NotificationService instance
func (f *Factory) Notification() *notification.Notification {
	return notification.NewNotification(queries.New(f.db), f.withTxn)
}

func (f *Factory) Invite() *invite.Invite {
	return invite.NewInvite(queries.New(f.db), f.withTxn)
}

// Trust returns a TrustService instance
// Award returns an AwardService instance.
func (f *Factory) Award() *award.Service {
	return award.NewService(f.Trust())
}

// Review returns a review service instance.
func (f *Factory) Review() *review.Service {
	return review.NewService(queries.New(f.db))
}

// List returns a shareable-list service.
//
// Takes withTxn because publish and unpublish are two writes -- the state change and its
// audit row -- that must not come apart. Constructing the service per call is what every
// other accessor here does; the service holds only a *queries.Queries, so there is nothing
// to cache and nothing to keep in sync.
func (f *Factory) List() *listsvc.Service {
	return listsvc.NewService(queries.New(f.db), f.withTxn)
}

// Streak returns a user's activity streak service.
func (f *Factory) Streak() *streak.Service {
	return streak.NewService(queries.New(f.db))
}

// Badges returns a user's derived badges.
//
// Not a method on the badge package because Derive is a PURE function of the
// rollup and needs no service of its own -- it takes trust.Totals and returns
// badges. The factory method is here so a resolver has one way to reach it that
// does not involve constructing the rollup by hand, and so the trust service it
// reads is the same object the rest of the app uses.
func (f *Factory) Badges(ctx context.Context, userID uuid.UUID) ([]badge.Badge, error) {
	totals, err := f.Trust().TotalsFor(ctx, userID)
	if err != nil {
		return nil, err
	}
	return badge.Derive(totals), nil
}

func (f *Factory) Trust() *trust.Trust {
	return trust.NewTrust(queries.New(f.db), f.withTxn)
}

// ModAudit returns a ModAuditService instance
func (f *Factory) ModAudit() *mod_audit.ModAuditService {
	return mod_audit.NewModAuditService(queries.New(f.db))
}

// Fingerprint returns a Fingerprint clustering service instance
func (f *Factory) Fingerprint() *fingerprint.Fingerprint {
	return fingerprint.New(queries.New(f.db))
}

// Federation returns the peer registry service instance.
//
// The second argument is a nil Resolver, which NewService deliberately replaces
// with the default resolver. That is the right wiring here because this is
// production: the registry must resolve DNS to decide whether a peer is safe to
// dial, and the injectable resolver exists so the REBINDING case is testable,
// not so production can run without resolution.
//
// Passing nil rather than importing net.DefaultResolver at the call site keeps
// the one place that knows how a live resolver is built inside the federation
// package, where ValidateBaseURL and netResolver live together.
func (f *Factory) Federation() *federation.Service {
	return federation.NewService(queries.New(f.db), nil)
}

// CropTemplates returns the shared crop template loader.
func (f *Factory) CropTemplates() *croptemplate.Loader {
	return f.cropTemplates
}
