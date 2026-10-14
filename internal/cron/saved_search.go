package cron

import (
	"context"

	"go.opentelemetry.io/otel"
	"golang.org/x/sync/semaphore"

	"github.com/stashapp/stash-box/internal/tracing"
	"github.com/stashapp/stash-box/pkg/logger"
)

// savedSearchSem is a third semaphore, not a share of `sem` (the edit closer) or `ingestSem`.
// This job runs one query per subscribed search, so it is the job most likely to take a while,
// and sharing a slot with the edit closer would let it delay edit processing by however long it
// took -- a coupling nobody would attribute to the right job, which is what ingestSem exists to
// avoid. Three jobs that can be slow, three slots.
var savedSearchSem = semaphore.NewWeighted(1)

// notifySavedSearches is the schedule and nothing else: the loop, the cursor rule and the
// one-notification-per-run rule live in savedsearch.NotifyMatches, where they can be exercised
// against a real database without a scheduler (internal/api/saved_search_integration_test.go).
//
// The interval is a config knob because the cost is proportional to how many searches are
// subscribed; the default is 15 minutes, because "a standing question got a new answer" is not
// latency-sensitive -- an hour-old match is still news.
func (c Cron) notifySavedSearches() {
	if !savedSearchSem.TryAcquire(1) {
		logger.Debug("Saved search cronjob failed to start, already running.")
		return
	}
	defer savedSearchSem.Release(1)

	ctx, span := otel.Tracer(tracerName).Start(context.Background(), "cron.notifySavedSearches")
	defer span.End()

	written, err := c.fac.SavedSearch().NotifyMatches(ctx, c.fac.Scene())
	tracing.RecordError(span, err)
	if err != nil {
		logger.Errorf("Error notifying saved searches: %s", err)
		return
	}

	if written > 0 {
		logger.Debugf("Notified %d saved searches", written)
	}
}
