package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/auth"
	"github.com/stashapp/stash-box/internal/feed"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/service"
	"github.com/stashapp/stash-box/pkg/logger"
)

// feedRoutes serves a saved search as RSS 2.0: GET /feeds/saved-search/<token> (plan slice 13b).
//
// THE TOKEN IS THE CREDENTIAL, and it is in the PATH rather than a header because a feed reader cannot
// send one: the address is pasted into a program this instance does not control, and it ends up in that
// program's config file, in a proxy's log and in the `Referer` of every link clicked out of the feed. That
// is why the token is per search and only its hash is stored (internal/service/savedsearch/feed.go), and
// why the response below says `no-referrer`.
//
// EVERY MISS IS THE SAME 404. A token that was never minted, a token whose search was deleted, a token
// whose feed was disabled and a path that is not a token at all are one answer, because 401 would confirm
// that a search exists and 403 that it belongs to somebody else -- and an instance whose whole posture is
// one private catalogue must not be usable as a scanner for which searches exist.
type feedRoutes struct {
	fac service.Factory
}

// How many records a feed carries. A feed is a WINDOW on a standing question, not the question: a reader
// who wants all of it opens the search, and a feed that paged would be a client protocol nobody asked for.
const feedItemLimit = 50

func (rs feedRoutes) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/{token}", rs.feed)
	return r
}

func (rs feedRoutes) feed(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")

	search, err := rs.fac.SavedSearch().FindByFeedToken(r.Context(), token)
	if err != nil {
		// The token is NOT part of the message: it is a bearer, and a log is one of the places it must
		// not accumulate.
		logger.Errorf("could not resolve a feed token: %v", err)
		http.Error(w, "this feed could not be built", http.StatusInternalServerError)
		return
	}
	if search == nil {
		http.NotFound(w, r)
		return
	}

	// THE SEARCH RUNS AS ITS OWNER. The stored text goes through the same query path a signed-in request
	// takes, and that path reads the current user -- `favorites:` means the OWNER's favourites and the
	// rating ceilings are keyed on the reader -- so a run with nobody in the context is not merely
	// imprecise, it fails. Placing the owner in is what makes the feed carry exactly what this search
	// carries signed in, and nothing more.
	runCtx := context.WithValue(r.Context(), auth.ContextUser, search.Owner)

	query := search.Search.Query
	filter := models.SceneQueryInput{
		Query: &query,

		// The same values the search's own `results` field passes, spelled out for the same reason it
		// spells them out: the zero values are not "unset" -- `page: 0` is a negative offset and
		// `sort: ""` is no sort at all, so a feed that left them out would answer a different question.
		Page:      1,
		PerPage:   feedItemLimit,
		Sort:      models.SceneSortEnumDate,
		Direction: models.SortDirectionEnumDesc,
	}

	scenes, err := rs.fac.Scene().Query(runCtx, filter)
	if err != nil {
		logger.Errorf("could not run the search behind a feed: %v", err)
		http.Error(w, "this feed could not be built", http.StatusInternalServerError)
		return
	}

	// Two bulk loads for the whole page, never one per item: a feed of fifty records must not cost a
	// hundred and fifty queries. A failure here degrades the DESCRIPTION and not the feed -- a reader
	// without the names is still shown the records and their links.
	appearances := rs.performerNames(runCtx, scenes)
	studios := rs.studioNames(runCtx, scenes)

	base := baseURL(r.Context())
	doc := feed.NewDocument(search.Search.Name, base, rs.describeChannel(runCtx, search.Search, scenes, filter))

	for _, scene := range scenes {
		doc.Channel.Items = append(doc.Channel.Items, feed.NewItem(
			sceneTitle(scene),
			base+"/scenes/"+scene.ID.String(),
			scene.ID.String(),
			scene.CreatedAt,
			describeScene(scene, appearances[scene.ID], studios[scene.StudioID.UUID]),
		))
	}

	body, err := feed.Render(doc)
	if err != nil {
		logger.Errorf("could not render a feed: %v", err)
		http.Error(w, "this feed could not be built", http.StatusInternalServerError)
		return
	}

	// THE HEADERS ARE PART OF THE DESIGN, because the URL is the credential.
	//   noindex ..... a bearer on an indexed page is a bearer anybody can find.
	//   private ..... a SHARED cache holding this response would hand one member's search to the next
	//                 caller. The reader's own cache is the point of a feed, so it is not `no-store`.
	//   no-referrer . a reader clicking an item would otherwise send this URL -- token and all -- to
	//                 wherever the item points.
	w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Header().Set("Referrer-Policy", "no-referrer")
	_, _ = w.Write(body)
}

// describeChannel is what the feed says about itself: the question, and why it has no answers today.
//
// The query TEXT is the channel's description because the text is the standing question and the items are
// only one answer to it, and a reader shown only items cannot tell what they are being told about. When
// the run found nothing, the DIAGNOSTICS come with it: an empty feed that will not say why is
// indistinguishable from a broken one -- the same reasoning the search page's own `diagnostics` field
// carries out loud.
func (rs feedRoutes) describeChannel(ctx context.Context, search models.SavedSearch, scenes []models.Scene, filter models.SceneQueryInput) string {
	description := fmt.Sprintf("A saved search on this instance: %s", search.Query)

	if len(scenes) > 0 {
		return description
	}

	diagnostics, err := rs.fac.Scene().LanguageDiagnostics(ctx, filter)
	if err != nil {
		// Not fatal and not silent: the feed is still a valid feed, and this one says what it can.
		logger.Errorf("could not read a saved search's diagnostics: %v", err)
		return description
	}
	if len(diagnostics) == 0 {
		return description + " (no matches right now)"
	}

	reasons := make([]string, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		reasons = append(reasons, diagnostic.Message)
	}
	return description + " (no matches: " + strings.Join(reasons, "; ") + ")"
}

// describeScene is what a reader sees WITHOUT opening the record: who is in it, who made it, when it came
// out and how long it is. Plain text, and no framing beyond that -- a feed that rendered markup or carried
// a frame of the work would be the content plane with a polling schedule, which is the one thing this
// programme is built not to be (scripts/check-no-content-plane.py).
func describeScene(scene models.Scene, performers []string, studio string) string {
	parts := make([]string, 0, 4)

	if len(performers) > 0 {
		parts = append(parts, "With "+strings.Join(performers, ", "))
	}
	if studio != "" {
		parts = append(parts, studio)
	}
	if scene.Date != nil && *scene.Date != "" {
		parts = append(parts, *scene.Date)
	}
	if scene.Duration != nil && *scene.Duration > 0 {
		parts = append(parts, formatDuration(*scene.Duration))
	}

	return strings.Join(parts, " · ")
}

// performerNames loads the performers of a whole page in two queries (the appearances join, then the
// performers themselves) and returns them by scene id, in the order the records list them.
func (rs feedRoutes) performerNames(ctx context.Context, scenes []models.Scene) map[uuid.UUID][]string {
	names := make(map[uuid.UUID][]string, len(scenes))
	if len(scenes) == 0 {
		return names
	}

	ids := make([]uuid.UUID, 0, len(scenes))
	for _, scene := range scenes {
		ids = append(ids, scene.ID)
	}

	appearances, errs := rs.fac.Scene().LoadAppearances(ctx, ids)
	performerIDs := make([]uuid.UUID, 0, len(appearances))
	for _, group := range appearances {
		for _, appearance := range group {
			performerIDs = append(performerIDs, appearance.PerformerID)
		}
	}
	if len(performerIDs) == 0 {
		return names
	}

	performers, loadErrs := rs.fac.Performer().LoadIds(ctx, performerIDs)
	byID := make(map[uuid.UUID]string, len(performers))
	for index, performer := range performers {
		if index < len(loadErrs) && loadErrs[index] != nil {
			logger.Errorf("could not load a performer for a feed: %v", loadErrs[index])
			continue
		}
		if performer != nil {
			byID[performer.ID] = performer.Name
		}
	}

	for index, scene := range scenes {
		if index >= len(appearances) {
			break
		}
		for _, appearance := range appearances[index] {
			if name, ok := byID[appearance.PerformerID]; ok {
				names[scene.ID] = append(names[scene.ID], name)
			}
		}
		if index < len(errs) && errs[index] != nil {
			logger.Errorf("could not load the cast of a scene for a feed: %v", errs[index])
		}
	}

	return names
}

// studioNames resolves the DISTINCT studios of a page. The studio service has no bulk read by ids, so the
// lookup is memoised per id: a page of fifty records holds a handful of studios, not fifty.
func (rs feedRoutes) studioNames(ctx context.Context, scenes []models.Scene) map[uuid.UUID]string {
	names := make(map[uuid.UUID]string, len(scenes))

	for _, scene := range scenes {
		id := scene.StudioID.UUID
		if !scene.StudioID.Valid || id == uuid.Nil {
			continue
		}
		if _, seen := names[id]; seen {
			continue
		}

		studio, err := rs.fac.Studio().FindByID(ctx, id)
		if err != nil || studio == nil {
			// A record whose studio cannot be read still appears in the feed: the studio is the
			// description's least important part, and losing it must not cost a reader the record.
			logger.Errorf("could not load a studio for a feed: %v", err)
			names[id] = ""
			continue
		}
		names[id] = studio.Name
	}

	return names
}

// formatDuration renders seconds the way a reader reads them: 30:12, or 1:02:03.
func formatDuration(seconds int) string {
	hours := seconds / 3600
	minutes := (seconds % 3600) / 60
	secs := seconds % 60
	if hours > 0 {
		return fmt.Sprintf("%d:%02d:%02d", hours, minutes, secs)
	}
	return fmt.Sprintf("%d:%02d", minutes, secs)
}

// sceneTitle is what an item is called. A record with no title is not a record called "": the fallback
// chain is the title, then the studio's own code for the work, then the id -- an item whose title is blank
// is an item a reader cannot click with any confidence.
func sceneTitle(scene models.Scene) string {
	if scene.Title != nil && strings.TrimSpace(*scene.Title) != "" {
		return *scene.Title
	}
	if scene.Code != nil && strings.TrimSpace(*scene.Code) != "" {
		return *scene.Code
	}
	return "Scene " + scene.ID.String()[:8]
}
