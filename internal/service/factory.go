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
	"log"
	"strings"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stashapp/stash-box/internal/config"
	"github.com/stashapp/stash-box/internal/email"
	"github.com/stashapp/stash-box/internal/image/croptemplate"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/activity"
	"github.com/stashapp/stash-box/internal/service/authored"
	"github.com/stashapp/stash-box/internal/service/award"
	"github.com/stashapp/stash-box/internal/service/badge"
	"github.com/stashapp/stash-box/internal/service/collage"
	"github.com/stashapp/stash-box/internal/service/completeness"
	"github.com/stashapp/stash-box/internal/service/completion"
	"github.com/stashapp/stash-box/internal/service/content"
	"github.com/stashapp/stash-box/internal/service/contentitem"
	"github.com/stashapp/stash-box/internal/service/draft"
	"github.com/stashapp/stash-box/internal/service/edit"
	"github.com/stashapp/stash-box/internal/service/elo"
	"github.com/stashapp/stash-box/internal/service/federation"
	"github.com/stashapp/stash-box/internal/service/fingerprint"
	"github.com/stashapp/stash-box/internal/service/follow"
	"github.com/stashapp/stash-box/internal/service/identification"
	"github.com/stashapp/stash-box/internal/service/image"
	"github.com/stashapp/stash-box/internal/service/imagetype"
	"github.com/stashapp/stash-box/internal/service/ingest"
	"github.com/stashapp/stash-box/internal/service/ingest/extract"
	"github.com/stashapp/stash-box/internal/service/invite"
	"github.com/stashapp/stash-box/internal/service/leaderboard"
	listsvc "github.com/stashapp/stash-box/internal/service/list"
	"github.com/stashapp/stash-box/internal/service/mod_audit"
	"github.com/stashapp/stash-box/internal/service/notification"
	"github.com/stashapp/stash-box/internal/service/performer"
	"github.com/stashapp/stash-box/internal/service/quest"
	"github.com/stashapp/stash-box/internal/service/review"
	"github.com/stashapp/stash-box/internal/service/savedsearch"
	"github.com/stashapp/stash-box/internal/service/scene"
	"github.com/stashapp/stash-box/internal/service/sessionstate"
	"github.com/stashapp/stash-box/internal/service/similar"
	"github.com/stashapp/stash-box/internal/service/site"
	"github.com/stashapp/stash-box/internal/service/streak"
	"github.com/stashapp/stash-box/internal/service/studio"
	"github.com/stashapp/stash-box/internal/service/tag"
	"github.com/stashapp/stash-box/internal/service/tagontology"
	"github.com/stashapp/stash-box/internal/service/taste"
	"github.com/stashapp/stash-box/internal/service/trust"
	"github.com/stashapp/stash-box/internal/service/user"
	"github.com/stashapp/stash-box/internal/service/usertoken"
	"github.com/stashapp/stash-box/internal/service/vouch"
)

// Factory provides access to all services with centralized database connection management
type Factory struct {
	db       *pgxpool.Pool
	withTxn  queries.WithTxnFunc
	emailMgr *email.Manager

	cropTemplates *croptemplate.Loader

	// vectorStore is built ONCE, not per accessor call, because it is a STORE: this is
	// where embeddings live, and a store rebuilt on every request would be empty on every
	// request -- the vector half of scene similarity could never reorder anything, however
	// many vectors were put into the previous instance. It is a connection, and a
	// connection is not per-call state.
	vectorStore similar.VectorStore
}

// NewFactory creates a new service factory with the given database pool and email manager
func NewFactory(pool *pgxpool.Pool, emailMgr *email.Manager) *Factory {
	return &Factory{
		db:            pool,
		withTxn:       createWithTxnFunc(pool),
		emailMgr:      emailMgr,
		cropTemplates: croptemplate.NewLoader(),
		vectorStore:   newVectorStore(),
	}
}

// Completion returns a CompletionService instance
func (f *Factory) Completion() *completion.Service {
	return completion.NewService(queries.New(f.db))
}

// Similar returns a scene-similarity service.
//
// The vector store is built HERE and nowhere else, because this is the one place that knows
// both the configuration and the implementations: `similar_vector_store` names one, and an
// unknown name is a configuration error an operator must see rather than a silent fallback
// to the no-op store. The no-op store is the default and a legitimate choice -- no embedding
// model produces vectors on this instance yet -- but it has to be CHOSEN, not arrived at by
// a typo.
func (f *Factory) Similar() *similar.Service {
	return similar.New(queries.New(f.db), f.Scene(), f.vectorStore)
}

// Taste returns the viewer-affinity service behind `forYou`.
//
// No stored state and no store to build: affinity is derived per call from the viewer's own
// signals, so there is nothing here that two instances could disagree about and nothing that
// could be stale between calls.
func (f *Factory) Taste() *taste.Service {
	return taste.New(queries.New(f.db), f.Scene())
}

// Follow returns the follows service.
//
// Takes withTxn even though the writes here are single statements today: a follow has no second
// write to keep together, and passing it anyway is what makes adding one (an audit row, a
// notification) a change to the service rather than to its constructor.
func (f *Factory) Follow() *follow.Service {
	return follow.New(queries.New(f.db), f.withTxn)
}

// Activity returns the chronological activity feed.
func (f *Factory) Activity() *activity.Service {
	return activity.New(queries.New(f.db), f.Scene())
}

// SessionState returns the opt-in "where I left off" service.
//
// No store to build and no configuration: whether a cursor is stored is a per-request argument
// (the client's `storage`), not an instance-wide setting.
func (f *Factory) SessionState() *sessionstate.Service {
	return sessionstate.New(queries.New(f.db))
}

// newVectorStore turns the configured name into an implementation, once per factory.
func newVectorStore() similar.VectorStore {
	switch strings.ToLower(config.GetSimilarVectorStore()) {
	case "", "noop":
		return similar.Noop{}
	case "inmemory":
		return similar.NewInMemory(config.GetSimilarVectorDimensions())
	default:
		// Logged and defaulted rather than fatal: a bad value here must not stop a server
		// from serving scenes, and the healthy-looking alternative -- refusing to start --
		// turns a typo into an outage. The warning is what an operator needs.
		log.Printf("unknown similar_vector_store %q: using the no-op store, so scene similarity will not re-rank",
			config.GetSimilarVectorStore())
		return similar.Noop{}
	}
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

// ContentItem returns the content-item reader (aggregator phase 3).
//
// Read-only: creation belongs to ingestion and to the edit system, which is the only
// path that writes shared metadata.
func (f *Factory) ContentItem() *contentitem.Service {
	return contentitem.NewService(queries.New(f.db))
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

// TagOntology returns the tag ontology service: the implication graph and its versions.
func (f *Factory) TagOntology() *tagontology.Service {
	return tagontology.New(queries.New(f.db))
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

// Content returns the content source service: where aggregated content lives, and
// whether it is still alive (docs/spec/feature-content-aggregator.md, phases 1-2).
//
// It takes the transaction helper because the phases that write health state have to
// agree with the row they are reading; the read paths here do not use it yet.
func (f *Factory) Content() *content.Service {
	return content.NewService(queries.New(f.db), f.withTxn)
}

// Completeness returns the expected-total read service: what a source claims a studio lists,
// what the instance actually holds, and whether the page the claim came from is still alive
// (docs/spec/feature-content-aggregator.md, phase 9 slice 9f).
//
// No transaction helper: this is a read path, and every statement in it is a plain query.
func (f *Factory) Completeness() *completeness.Service {
	return completeness.NewService(queries.New(f.db))
}

// Ingest returns the aggregation pipeline (docs/spec/feature-ingestion.md, plan phase 2).
//
// The extractor is built here rather than injected, because the HTTP extractor is what
// production uses and a factory method that required one would make every caller decide.
// Tests that need a deterministic fetch construct ingest.NewService directly with
// extract.Fake -- which is exactly why the extractor is an interface and why there is no
// second factory method whose only purpose would be to serve a test.
func (f *Factory) Ingest() *ingest.Service {
	return ingest.NewService(queries.New(f.db), extract.NewHTTPExtractor())
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

// SavedSearch returns a SavedSearchService instance
func (f *Factory) SavedSearch() *savedsearch.SavedSearch {
	return savedsearch.NewSavedSearch(queries.New(f.db), f.withTxn)
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

// Leaderboard returns the contribution leaderboard service.
func (f *Factory) Leaderboard() *leaderboard.Service {
	return leaderboard.NewService(queries.New(f.db))
}

// Vouches returns the member-vouch service.
func (f *Factory) Vouches() *vouch.Service {
	return vouch.NewService(queries.New(f.db), f.withTxn, f.Trust())
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
