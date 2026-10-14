package cron

import (
	"context"
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

	checked, err := c.fac.Content().Sweep(ctx, time.Now(), content.SweepBatch, content.NewHTTPChecker())
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

	// The aggregation intake queue's drain (docs/spec/feature-ingestion.md, plan phase 2).
	//
	// Bounded per tick, because a tick that drained an unbounded backlog would hold this
	// process's single cron slot for as long as the backlog took -- and every other job in
	// this file would wait behind it while fetching third-party pages.
	//
	// Errors are logged and swallowed, like every other job here: a failed drain is one
	// minute of no progress, and the rows stay 'pending' (or back off) for the next tick.
	_, err = c.AddFunc("@every 1m", cronJobs.processIngestQueue)
	if err != nil {
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

	// The health sweep. Registered unconditionally, like the ingest drain, and for the reason the
	// comment below the vote block gives: an operator who sets vote_cron_interval to "" to stop edit
	// processing must not silently stop the sweep as well.
	_, err = c.AddFunc("@every "+config.GetContentSweepInterval(), cronJobs.sweepContentSources)
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
