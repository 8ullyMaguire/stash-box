package content

import (
	"errors"

	"github.com/stashapp/stash-box/internal/config"
)

// The content plane's one posture switch (respec, 2026-10-15).
//
// WHAT IT IS FOR. Three behaviours in this programme make an outbound HTTP request to a third
// party: the health sweep's HEAD (health.go), Wayback submission (wayback.go) and the ingest
// pipeline's page fetch (internal/service/ingest). Each one is the act of READING SOMEBODY ELSE'S
// PAGE, which is what makes an instance a scraper -- and a scraper that serves what it read is a
// host, which is the category of site that is DMCA'd out of existence. The programme builds a
// catalogue of references instead: claims about works and the addresses of pages. So those three
// behaviours are off unless an operator turns content_fetch_sources on deliberately.
//
// WHY THE RULE LIVES HERE AND NOT AT EACH CALL SITE. Six call sites can issue a request (four in
// this package, two in the pipeline). A policy restated six times is six chances for one of them to
// drift, and the failure mode is silent: the instance keeps working and keeps fetching. So the
// decision is one function, EVERY caller asks it, and the refusal is one error with one message.
// The cron layer enforces the same rule a second time and from the other end -- the jobs are not
// registered at all when the switch is off (internal/cron) -- because a job that is not registered
// cannot be called, and a guard a later edit deletes is not a guard.
//
// WHAT IS NOT GATED, named so that the absence of a guard is a decision and not an oversight:
// internal/storage/favicon.go (upstream's member-triggered site-icon fetch), internal/webhook (a URL
// an operator configured), internal/service/federation/client.go (peers the operator peered with)
// and internal/sdbimport (the directory importer). This switch is about CONTENT SOURCES.

// ErrFetchingDisabled is what a caller returns when this instance does not fetch content sources.
//
// The message is written for the two readers it has: an operator, who is told which key to set, and
// a member whose submitted URL was refused, who is told the instance does not fetch rather than that
// their input was malformed -- the difference between a server's posture and a bad request.
var ErrFetchingDisabled = errors.New("this instance does not fetch content sources (content_fetch_sources is off)")

// FetchingEnabled reports whether this instance may make outbound requests to content sources.
func FetchingEnabled() bool {
	return config.GetContentFetchSources()
}

// RequireFetching returns ErrFetchingDisabled unless the operator has enabled content fetching.
//
// It returns an error rather than a bool so it can be the FIRST statement of a function that issues
// a request: an unused bool is invisible to the compiler, while an unchecked error is not, and the
// guard then reads as the precondition it is.
func RequireFetching() error {
	if !FetchingEnabled() {
		return ErrFetchingDisabled
	}
	return nil
}
