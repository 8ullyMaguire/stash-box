package cron

import (
	"testing"

	"github.com/robfig/cron/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/config"
)

// withFetching sets the content-plane switch for one test and restores it, because `config.C` is a
// package global and a test that left it set would change what later tests in this binary exercise.
func withFetching(t *testing.T, enabled bool) {
	t.Helper()
	previous := config.C.ContentFetchSources
	config.C.ContentFetchSources = enabled
	t.Cleanup(func() { config.C.ContentFetchSources = previous })
}

// withSweepInterval sets the sweep's interval for one test and restores it.
func withSweepInterval(t *testing.T, interval string) {
	t.Helper()
	previous := config.C.ContentSweepInterval
	config.C.ContentSweepInterval = interval
	t.Cleanup(func() { config.C.ContentSweepInterval = previous })
}

// The property this file exists for: with the switch off, the jobs that make outbound requests are not
// in the scheduler AT ALL -- so there is no tick to skip and no `if` a later edit can drop
// (respec, 2026-10-15).
//
// Both halves are asserted, and the second one matters as much as the first: a switch that produced an
// empty list in both states would pass a one-sided test and disable the sweep for everybody.
func TestTheContentPlaneJobsAreAbsentUnlessFetchingIsOn(t *testing.T) {
	svc := Cron{}

	withFetching(t, false)
	require.Empty(t, contentPlaneJobs(svc),
		"the ingest drain and the sweep must not be offered to the scheduler at all when fetching is off")

	off := cron.New()
	names, err := register(off, contentPlaneJobs(svc))
	require.NoError(t, err)
	assert.Empty(t, names)
	assert.Empty(t, off.Entries(), "nothing may be registered: a registered job is a job that can run")

	withFetching(t, true)
	jobs := contentPlaneJobs(svc)
	require.Len(t, jobs, 2)

	on := cron.New()
	names, err = register(on, jobs)
	require.NoError(t, err)
	assert.Equal(t, []string{"ingest-queue", "content-sweep"}, names)
	assert.Len(t, on.Entries(), 2, "register must add exactly the jobs it names")
}

// The sweep's schedule comes from the configured interval, and a bad value is a loud failure that names
// the job. A silent fallback here would mean an operator's typo turned into either a flood of requests
// or a sweep that never runs, depending on which way the fallback went.
func TestTheSweepJobCarriesTheConfiguredInterval(t *testing.T) {
	withFetching(t, true)
	withSweepInterval(t, "not-a-duration")

	names, err := register(cron.New(), contentPlaneJobs(Cron{}))
	require.Error(t, err, "an unparseable interval must not be swallowed")
	assert.Contains(t, err.Error(), "content-sweep", "the failure must name the job it happened in")
	assert.Equal(t, []string{"ingest-queue"}, names,
		"the ingest job was registered before the failure and the sweep was not: registering in order is "+
			"what makes that state reportable")
}

// The drain's interval is fixed rather than configurable, and this is the assertion that keeps it that
// way by accident: it is the one job whose cadence is a property of the design (a URL a member just
// pasted should not wait an hour) rather than a cost knob.
func TestTheIngestDrainRunsEveryMinute(t *testing.T) {
	withFetching(t, true)

	jobs := contentPlaneJobs(Cron{})
	require.Len(t, jobs, 2)
	assert.Equal(t, "@every 1m", jobs[0].Spec)
}
