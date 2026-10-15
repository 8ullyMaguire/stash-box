package cron

import (
	"context"
	"fmt"
	"time"

	"github.com/robfig/cron/v3"
	"go.opentelemetry.io/otel"
	"golang.org/x/sync/semaphore"

	"github.com/stashapp/stash-box/internal/autocert"
	"github.com/stashapp/stash-box/internal/config"
	"github.com/stashapp/stash-box/internal/service"
	"github.com/stashapp/stash-box/internal/service/content"
	"github.com/stashapp/stash-box/internal/tracing"
	"github.com/stashapp/stash-box/pkg/logger"
)

const tracerName = "github.com/stashapp/stash-box/internal/cron"

var sem = semaphore.NewWeighted(1)

// ingestSem is separate from sem on purpose: the ingest drain makes third-party HTTP
// requests with a 20-second timeout each, and sharing one semaphore with the edit closer
// would let one slow host delay edit processing by that much -- a coupling neither job
// wants and neither would ever attribute to the other.
// sweepSem is separate from the others for the same reason ingestSem is separate from sem: the
// health sweep makes its own third-party HTTP requests, and sharing a semaphore would let a slow
// site delay edit processing by however long it takes to answer.
var sweepSem = semaphore.NewWeighted(1)

var ingestSem = semaphore.NewWeighted(1)

type Cron struct {
	fac service.Factory
}

// processEdits runs at set intervals and closes edits where the voting period has ended,
// either by applying the edit if enough positive votes have been cast, or by rejecting it.
func (c Cron) processEdits() {
	if !sem.TryAcquire(1) {
		logger.Debug("Edit cronjob failed to start, already running.")
		return
	}
	defer sem.Release(1)

	ctx, span := otel.Tracer(tracerName).Start(context.Background(), "cron.processEdits")
	defer span.End()

	closedEdits, err := c.fac.Edit().CloseCompleted(ctx)
	tracing.RecordError(span, err)
	if err != nil {
		logger.Errorf("Error processing edits: %s", err)
	}

	// Trigger notifications for all closed edits
	for _, edit := range closedEdits {
		c.fac.Notification().OnApplyEdit(ctx, edit)
	}
}

// processIngestQueue drains the aggregation intake queue (docs/spec/feature-ingestion.md,
// plan phase 2): claim a bounded batch of submitted URLs, fetch each, and either record a
// source or record why not.
//
// BOUNDED, and the bound is the point: this shares the process with the edit closer and
// the cleanups, so a tick that drained an unbounded backlog would hold its own worker for
// as long as the backlog took -- while making third-party HTTP requests.
//
// Its OWN semaphore, not the shared `sem`: the ingest drain makes network calls with
// 20-second timeouts, and letting it exclude the edit closer would mean a slow host
// delaying edit processing by up to that long for no reason either of them cares about.
func (c Cron) processIngestQueue() {
	if !ingestSem.TryAcquire(1) {
		logger.Debug("Ingest cronjob failed to start, already running.")
		return
	}
	defer ingestSem.Release(1)

	ctx, span := otel.Tracer(tracerName).Start(context.Background(), "cron.processIngestQueue")
	defer span.End()

	processed, err := c.fac.Ingest().Drain(ctx, ingestDrainLimit)
	tracing.RecordError(span, err)
	if err != nil {
		logger.Errorf("Error draining the ingest queue: %s", err)
		return
	}
	if processed > 0 {
		logger.Infof("Ingest queue: processed %d job(s)", processed)
	}
}

// ingestDrainLimit is how many submitted URLs one tick handles. Twenty is a minute's work
// across a few hosts; it is a constant so the bound is visible in one place rather than as
// a literal at the call site.
const ingestDrainLimit = 20

func (c Cron) cleanDrafts() {
	ctx, span := otel.Tracer(tracerName).Start(context.Background(), "cron.cleanDrafts")
	defer span.End()

	err := c.fac.Draft().DeleteExpired(ctx)
	tracing.RecordError(span, err)
	if err != nil {
		logger.Errorf("Error cleaning drafts: %s", err)
	}
}

func (c Cron) cleanTokens() {
	ctx, span := otel.Tracer(tracerName).Start(context.Background(), "cron.cleanTokens")
	defer span.End()

	err := c.fac.UserToken().DestroyExpired(ctx)
	tracing.RecordError(span, err)
	if err != nil {
		logger.Errorf("Error cleaning user tokens: %s", err)
	}
}

func (c Cron) cleanInvites() {
	ctx, span := otel.Tracer(tracerName).Start(context.Background(), "cron.cleanInvites")
	defer span.End()

	err := c.fac.Invite().DestroyExpired(ctx)
	tracing.RecordError(span, err)
	if err != nil {
		logger.Errorf("Error cleaning invites: %s", err)
	}
}

func (c Cron) cleanNotifications() {
	ctx, span := otel.Tracer(tracerName).Start(context.Background(), "cron.cleanNotifications")
	defer span.End()

	err := c.fac.Notification().DestroyExpired(ctx)
	tracing.RecordError(span, err)
	if err != nil {
		logger.Errorf("Error cleaning notifications: %s", err)
	}
}

func (c Cron) refreshPopularityTrending() {
	ctx, span := otel.Tracer(tracerName).Start(context.Background(), "cron.refreshPopularityTrending")
	defer span.End()

	if err := c.fac.Scene().RefreshPopularityTrending(ctx); err != nil {
		tracing.RecordError(span, err)
		logger.Errorf("Error refreshing scene popularity trending: %s", err)
	}
}

// refreshSceneFreshness recomputes the freshness scores for the `FRESH` and `RISING` sorts.
//
// On the same hourly cadence as the popularity refresh it sits beside, and for the same reason:
// both are decayed sums over recent activity, so a feed that is a few minutes stale is the
// design. Slower would make "Rising" lag the activity it is supposed to show.
func (c Cron) refreshSceneFreshness() {
	ctx, span := otel.Tracer(tracerName).Start(context.Background(), "cron.refreshSceneFreshness")
	defer span.End()

	if err := c.fac.Scene().RefreshFreshness(ctx); err != nil {
		tracing.RecordError(span, err)
		logger.Errorf("Error refreshing scene freshness: %s", err)
	}
}

func (c Cron) refreshPopularityAllTime() {
	ctx, span := otel.Tracer(tracerName).Start(context.Background(), "cron.refreshPopularityAllTime")
	defer span.End()

	if err := c.fac.Scene().RefreshPopularityAllTime(ctx); err != nil {
		tracing.RecordError(span, err)
		logger.Errorf("Error refreshing scene popularity all_time: %s", err)
	}
	if err := c.fac.Performer().RefreshPopularityAllTime(ctx); err != nil {
		tracing.RecordError(span, err)
		logger.Errorf("Error refreshing performer popularity all_time: %s", err)
	}
}

func (c Cron) cleanImageAudits() {
	retentionDays := config.GetImageAuditRetentionDays()
	if retentionDays <= 0 {
		return
	}

	ctx, span := otel.Tracer(tracerName).Start(context.Background(), "cron.cleanImageAudits")
	defer span.End()

	err := c.fac.Image().DeleteExpiredAudits(ctx, retentionDays)
	tracing.RecordError(span, err)
	if err != nil {
		logger.Errorf("Error cleaning image audit logs: %s", err)
	}
}

func (c Cron) cleanModAudits() {
	retentionDays := config.GetModAuditRetentionDays()
	if retentionDays <= 0 {
		return
	}

	ctx, span := otel.Tracer(tracerName).Start(context.Background(), "cron.cleanModAudits")
	defer span.End()

	err := c.fac.ModAudit().DeleteExpired(ctx, retentionDays)
	tracing.RecordError(span, err)
	if err != nil {
		logger.Errorf("Error cleaning mod audit logs: %s", err)
	}
}

// sweepContentSources runs the aggregator's health sweep (docs/spec/feature-content-aggregator.md,
// plan phase 9 slice 9b): find the sources whose cadence says they are due, and check them.
//
// BOUNDED, and the bound is the point: every candidate costs a request to somebody else's site, and
// this shares the process with the edit closer and the ingest drain. A tick that considered an
// unbounded backlog would hold its worker for as long as the backlog took.
//
// The cadence is in `content.CheckInterval` -- reliability lengthens the interval, popularity
// shortens it -- and the query only decides which rows are worth considering at all.
func (c Cron) sweepContentSources() {
	if !sweepSem.TryAcquire(1) {
		logger.Debug("Content sweep cronjob failed to start, already running.")
		return
	}
	defer sweepSem.Release(1)

	ctx, span := otel.Tracer(tracerName).Start(context.Background(), "cron.sweepContentSources")
	defer span.End()

	now := time.Now()
	checker := content.NewHTTPChecker()

	checked, err := c.fac.Content().Sweep(ctx, now, content.SweepBatch, checker)
	tracing.RecordError(span, err)
	if err != nil {
		// Logged WITH the count: a run that checked nine sources and then failed is a different
		// situation from one that failed before it started, and only the count tells them apart.
		logger.Errorf("Error sweeping content sources after checking %d: %s", checked, err)
		return
	}

	if checked > 0 {
		logger.Debugf("Content sweep checked %d source(s)", checked)
	}

	// The resurrection queue (slice 9c) runs in the same tick and on its own cadence: a dead source
	// is asked about once a month, not once an hour, and the rows it considers are disjoint from the
	// sweep's (the sweep's query excludes dead, this one considers only dead).
	resurrected, err := c.fac.Content().SweepResurrections(ctx, now, content.SweepBatch, checker)
	tracing.RecordError(span, err)
	if err != nil {
		logger.Errorf("Error working the resurrection queue: %s", err)
		return
	}

	// The archive queue (slice 9e): sources that are gone or going, preserved before they vanish. It
	// runs AFTER the sweep so that a source the sweep has just found dead is archived in the same tick
	// rather than an hour later.
	attempts, err := c.fac.Content().ArchiveSources(ctx, content.SweepBatch, content.NewWayback())
	tracing.RecordError(span, err)
	if err != nil {
		logger.Errorf("Error archiving content sources: %s", err)
		return
	}
	for _, attempt := range attempts {
		if attempt.Err != nil {
			// A failure is a RESULT here: one host refusing to archive says nothing about the next.
			logger.Warnf("Could not archive %s: %s", attempt.URL, attempt.Err)
			continue
		}
		logger.Infof("Content source archived: %s -> %s", attempt.URL, attempt.ArchivedURL)
	}

	// Logged by NAME and by how long the gap was, not counted: a source this instance recorded as
	// dead is serving again, and "which one, gone since when" is the whole content of that news.
	for _, source := range resurrected {
		if source.LastAliveAt == nil {
			logger.Infof("Content source resurrected (never known alive before this): %s", source.URL)
			continue
		}
		logger.Infof("Content source resurrected after %s: %s", now.Sub(*source.LastAliveAt).Round(time.Hour), source.URL)
	}
}

// scheduledJob is one job this instance runs on a schedule, NAMED so that "which jobs run here" is a
// value a test can read rather than something only the scheduler knows.
type scheduledJob struct {
	Name string
	Spec string
	Run  func()
}

// contentPlaneJobs returns the jobs that make outbound HTTP requests to third-party content sources:
// the ingest queue's drain and the health sweep. EMPTY when content_fetch_sources is off, which is
// the default and the whole point of the 2026-10-15 respec
// (docs/spec/feature-content-aggregator.md's respec section).
//
// EMPTY RATHER THAN A PER-TICK CHECK, and the difference is the design: a job that is not registered
// cannot be called by anything -- not this file, not a future tick, not a refactor that loses the
// `if` -- whereas a job that runs and returns immediately is one edit away from making requests
// again. The absence is stated once, in Init, so an operator reading the log knows why the queue is
// not moving.
//
// This is a SECOND enforcement of the rule the services enforce (internal/service/content's
// RequireFetching), and the duplication is deliberate: the cron layer is where a request would come
// from with nobody watching, and the services are where one would come from a caller this file has
// never heard of. The condition is the same function, so the two cannot disagree.
//
// These are the only jobs in this file that touch a third party as a matter of posture. The vote
// interval still does not control them: an operator who sets vote_cron_interval to "" to stop edit
// processing does not stop anything here, and no longer means "stop fetching" either.
//
// The drain is bounded per tick, because a tick that drained an unbounded backlog would hold this
// process's single cron slot for as long as the backlog took -- and every other job in this file
// would wait behind it while fetching third-party pages.
func contentPlaneJobs(c Cron) []scheduledJob {
	if !config.GetContentFetchSources() {
		return nil
	}

	return []scheduledJob{
		{Name: "ingest-queue", Spec: "@every 1m", Run: c.processIngestQueue},
		{Name: "content-sweep", Spec: "@every " + config.GetContentSweepInterval(), Run: c.sweepContentSources},
	}
}

// register adds jobs to c and returns the names it registered, in order.
//
// Returning the names is what lets a test assert WHICH jobs an instance runs in either switch state
// without reaching into the scheduler for a function it cannot name or compare.
func register(c *cron.Cron, jobs []scheduledJob) ([]string, error) {
	registered := make([]string, 0, len(jobs))
	for _, job := range jobs {
		if _, err := c.AddFunc(job.Spec, job.Run); err != nil {
			return registered, fmt.Errorf("registering the %s job: %w", job.Name, err)
		}
		registered = append(registered, job.Name)
	}
	return registered, nil
}

func Init(fac service.Factory) {
	c := cron.New()
	cronJobs := Cron{fac}

	_, err := c.AddFunc("@every 5m", cronJobs.cleanDrafts)
	if err != nil {
		panic(err.Error())
	}

	_, err = c.AddFunc("@every 1m", cronJobs.cleanTokens)
	if err != nil {
		panic(err.Error())
	}

	_, err = c.AddFunc("@every 60m", cronJobs.cleanNotifications)
	if err != nil {
		panic(err.Error())
	}

	_, err = c.AddFunc("@every 60m", cronJobs.cleanInvites)
	if err != nil {
		panic(err.Error())
	}

	_, err = c.AddFunc("@every 12h", cronJobs.cleanModAudits)
	if err != nil {
		panic(err.Error())
	}

	_, err = c.AddFunc("@every 12h", cronJobs.cleanImageAudits)
	if err != nil {
		panic(err.Error())
	}

	_, err = c.AddFunc("@every 1h", cronJobs.refreshPopularityTrending)
	if err != nil {
		panic(err.Error())
	}

	_, err = c.AddFunc("@every 1h", cronJobs.refreshSceneFreshness)
	if err != nil {
		panic(err.Error())
	}

	_, err = c.AddFunc("@daily", cronJobs.refreshPopularityAllTime)
	if err != nil {
		panic(err.Error())
	}

	if config.GetAutocertConfig() != nil {
		_, err = c.AddFunc("@daily", autocert.CheckAndRenew)
		if err != nil {
			panic(err.Error())
		}
	}

	// The content plane's two outbound jobs (docs/spec/feature-content-aggregator.md's respec
	// section, 2026-10-15). They are registered ONLY when content_fetch_sources is on, and the
	// scheduler is told why they are missing when it is off -- an operator whose queue is not
	// moving should not have to read the source to find out that this is the reason.
	contentJobs := contentPlaneJobs(cronJobs)
	if len(contentJobs) == 0 {
		logger.Infof("content_fetch_sources is off: this instance makes no outbound requests to content sources, " +
			"so the ingest drain and the health sweep are NOT registered (see docs/spec/feature-content-aggregator.md)")
	} else {
		// THE ON CASE ANNOUNCES ITSELF TOO, and not for symmetry: the off line is something an
		// operator can notice by its absence, while the on posture had no line at all -- and
		// "which posture is this process running" is the first question anybody asks of an
		// instance, including the person reading a takedown notice. It also gives the
		// deployment's verification a POSITIVE line to grep: an absence assertion is not evidence,
		// whether it is a test or a log.
		logger.Infof("content_fetch_sources is ON: registering %d outbound job(s) -- the ingest drain and the "+
			"health sweep. They obey each host's robots.txt and one %s per-host pace, and a blocked URL or host "+
			"is refused at submission AND at the drain (see docs/spec/feature-content-aggregator.md)",
			len(contentJobs), config.GetContentFetchMinInterval())
	}
	if _, err := register(c, contentJobs); err != nil {
		panic(err.Error())
	}

	// Saved-search notifications (docs/spec/feature-discovery.md section 5). Every 15 minutes,
	// not every minute: this job runs one query per subscribed search, and "a standing question
	// got a new answer" is not a latency-sensitive event -- an hour-old match is still news. The
	// interval is a knob because the cost is proportional to how many searches are subscribed.
	_, err = c.AddFunc("@every "+config.GetSavedSearchNotifyInterval(), cronJobs.notifySavedSearches)
	if err != nil {
		panic(err.Error())
	}

	interval := config.GetVoteCronInterval()
	if interval != "" {
		_, err := c.AddFunc("@every "+config.GetVoteCronInterval(), cronJobs.processEdits)
		if err != nil {
			panic(err.Error())
		}

		logger.Debugf("Edit cronjob initialized to run every %s", interval)
	}

	// Started unconditionally, and NOT inside the interval block above.
	//
	// It used to live inside it, which coupled every job in this function -- nine of them,
	// including the edit closer -- to a value about voting intervals. With the default
	// (5m) that coupling is invisible; an operator who sets vote_cron_interval to empty
	// string to stop edit processing also silently stopped the invite cleanup, the
	// notification cleanup, the popularity refreshes and now the ingest drain. The jobs
	// are registered above and unconditional, so the scheduler that runs them is too.
	c.Start()
}
