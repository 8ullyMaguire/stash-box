// Package extract turns a URL into whatever metadata can be learned about the content
// behind it.
//
// WHY AN INTERFACE WITH A REAL IMPLEMENTATION AND A FAKE. The pipeline's decision rules are
// the interesting part and they must be testable without a network: a test that needs a
// live tube site is a test that runs when someone remembers to. So the interface exists,
// `Fake` is what every test uses, and `HTTPExtractor` is what production uses -- a real
// implementation, not a stub, because a stub in the production slot is a feature that
// works in tests and does nothing in the deployment.
//
// WHAT THIS PACKAGE DELIBERATELY DOES NOT DO: fingerprint remote media. A hash of a video
// requires downloading the video, and the aggregator's download path is phase 9's. An
// HTTP extractor therefore returns no fingerprints, which means the pipeline's
// fingerprint-merge stage (spec §1 stage 5) only fires for URLs whose media was
// fingerprinted by something else. That is a real limitation, stated here rather than
// discovered later: in the first production cut, URL dedup is what merges.
package extract

import (
	"context"
	"errors"
)

// Fingerprint is one hash of a piece of media, in the form the fork already stores.
//
// Hash is an int64, and that is the fork's schema rather than a choice made here:
// `fingerprints.hash` is BIGINT and gqlgen's FingerprintHash scalar maps to Go int64
// (graphql/schema/types/misc.graphql). The algorithms the fork supports (OSSHASH, MD5,
// PHASH) are all reduced to a 64-bit value on the way in, so an extractor that produced a
// hex string would have to be the thing that reduces it -- and a second reduction that
// disagrees with the first produces hashes that never match anything. The extractor's job
// is to hand over a value in the stored form, not a representation of one.
type Fingerprint struct {
	Algorithm string
	Hash      int64
	// Duration is the media's length in seconds as the fingerprinter measured it, which
	// is not always the length the page claims. It travels with the hash because
	// scene_fingerprints stores the two together.
	Duration int
}

// Metadata is everything an extractor could learn. Every field is optional and the zero
// value means "not learned", because a page that states only a title is a normal page.
type Metadata struct {
	Title          string
	Studio         string
	PerformerNames []string
	// Date is a YYYY-MM-DD string, not a time.Time: the pages state a release date with
	// no time and no zone, and scenes.date is a text column for that reason (a scene's
	// production date is not an instant).
	Date            string
	DurationSeconds int
	ThumbnailURL    string
	Fingerprints    []Fingerprint
	// ResolvedURL is where the fetch actually landed -- the shortener's destination when
	// one was followed. Empty when it is the same URL or the extractor cannot say.
	ResolvedURL string
	// Confidence is the extractor's own 0..1 estimate, stored on the queue row and read
	// by the auto-approve rule (spec §4). It is not a verdict: a low number means "this is
	// thin evidence", not "this is wrong".
	Confidence float64
	// Source names the extractor that produced this, so a record can be traced to the
	// thing that made it when two extractors disagree later.
	Source string
}

// The three failure kinds, because the pipeline's response differs for each and a single
// error would force it to guess:
//
//   - ErrUnreachable: try again later (the host timed out, the connection was refused, the
//     server had a bad day). The queue retries with a backoff.
//   - ErrUnsupportedPage: do not try again -- a login wall, a paywall, a page this
//     extractor cannot read. Retrying cannot fix it, and the reason is recorded so a
//     person is not left re-queueing it.
//   - ErrGone: the content is not there (404/410). Permanent for the FETCH, and the
//     pipeline's job succeeds: the submission was handled correctly and the content is
//     what is missing. Conflating this with a failure makes the queue's failure count
//     meaningless (spec §5).
var (
	ErrUnreachable     = errors.New("the host could not be reached")
	ErrUnsupportedPage = errors.New("the page cannot be read by this extractor")
	ErrGone            = errors.New("the content is not there any more")
)

// Extractor is the pipeline's only network dependency.
type Extractor interface {
	Extract(ctx context.Context, url string) (Metadata, error)
}

// Fake is the test implementation: a table from URL to result.
//
// It is exported and lives in the package rather than in a test file because two packages'
// tests need it (this one and the service's integration test), and a second fake in the
// second package would drift from this one the first time a field is added.
type Fake struct {
	// ByURL maps a URL to what "the network" says about it.
	ByURL map[string]Metadata
	// ByURLError maps a URL to a failure, for the paths that test retry behaviour.
	ByURLError map[string]error
	// Calls records every URL asked for, in order, so a test can assert HOW MANY times an
	// extractor was consulted -- the count is what distinguishes "dedup worked" from
	// "the fetch was skipped", and only one of those is the claim.
	Calls []string
}

func NewFake(byURL map[string]Metadata) *Fake {
	return &Fake{ByURL: byURL, ByURLError: map[string]error{}}
}

func (f *Fake) Extract(ctx context.Context, url string) (Metadata, error) {
	f.Calls = append(f.Calls, url)
	if err, ok := f.ByURLError[url]; ok {
		return Metadata{}, err
	}
	meta, ok := f.ByURL[url]
	if !ok {
		// A URL the fake does not know is GONE, not unreachable: the test table is the
		// whole world, and a missing entry is "this URL has no content", which is a
		// terminal answer. Defaulting to unreachable would make every forgotten fixture
		// entry look like a network problem and retry forever.
		return Metadata{}, ErrGone
	}
	return meta, nil
}

var _ Extractor = (*Fake)(nil)
