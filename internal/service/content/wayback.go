package content

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/stashapp/stash-box/internal/queries"
)

// Archiver submits a URL to an archive and returns the address of the copy it made.
//
// An interface because the tests must not touch the Internet Archive, and because a second archiver is
// a plausible thing to want: the submission itself is somebody else's service with somebody else's
// availability, and this instance's job is to record what came back.
type Archiver interface {
	Archive(ctx context.Context, url string) (string, error)
}

// ArchiveAttempt is one URL's trip to the archive: the address it was given, the copy's address when
// one came back, and why not when one did not.
//
// A failure is a RESULT, not an error: one host refusing to archive tells us nothing about the next,
// so the batch continues and reports it. An outer error from ArchiveSources means the database could
// not be read or written, which is a different kind of problem and does stop the run.
type ArchiveAttempt struct {
	URL         string
	ArchivedURL string
	Err         error
}

// Wayback is the production archiver: the Internet Archive's "Save Page Now".
type Wayback struct {
	Client *http.Client
}

// NewWayback builds the archiver the cron tick uses.
//
// The timeout is the longest of the three HTTP budgets in this package, because archiving is slow by
// nature: the archive has to fetch the page itself, and it is the one request here whose response we
// are not waiting on for a reader. Twenty seconds, like the health check, produced nothing but timeouts
// against a service that legitimately takes longer.
func NewWayback() Wayback {
	return Wayback{Client: &http.Client{Timeout: 90 * time.Second}}
}

// Archive submits a URL and returns the archived copy's absolute address.
//
// The endpoint answers a submission with a `Content-Location` header naming the copy (a path like
// `/web/20261015120000/https://example.invalid/scene`), and a submission that reports success WITHOUT
// one has not produced an archive this instance can record -- so a missing header is an error, not a
// success with an empty string. That is the whole reason this returns (string, error) rather than a
// bool.
//
// Endpoint details are exactly the kind of thing that changes without notice, so this is the one
// function to read when the column stops filling in.
func (w Wayback) Archive(ctx context.Context, target string) (string, error) {
	form := url.Values{"url": {target}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://web.archive.org/save", strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("building the archive submission for %s: %w", target, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := w.Client.Do(req)
	if err != nil {
		return "", fmt.Errorf("submitting %s to the archive: %w", target, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("the archive answered %d for %s", resp.StatusCode, target)
	}

	location := resp.Header.Get("Content-Location")
	if location == "" {
		return "", fmt.Errorf("the archive accepted %s but named no copy (no Content-Location)", target)
	}

	if strings.HasPrefix(location, "http://") || strings.HasPrefix(location, "https://") {
		return location, nil
	}
	return "https://web.archive.org" + location, nil
}

// ArchiveSources preserves the sources that are gone or going, at most `limit` of them.
//
// The pair of columns is written by the ONE statement that writes them, so a half-archive cannot happen
// even by mistake: the database refuses it.
func (s *Service) ArchiveSources(ctx context.Context, limit int, archiver Archiver) ([]ArchiveAttempt, error) {
	// Submitting a URL to a third party is an outbound request, so it is behind the content-plane
	// switch even though what it sends is an address and not a page. See policy.go.
	if err := RequireFetching(); err != nil {
		return nil, err
	}

	candidates, err := s.queries.FindSourcesToArchive(ctx, int32(limit))
	if err != nil {
		return nil, fmt.Errorf("reading the sources to archive: %w", err)
	}

	attempts := make([]ArchiveAttempt, 0, len(candidates))
	for _, candidate := range candidates {
		attempt := ArchiveAttempt{URL: candidate.Url}

		archived, err := archiver.Archive(ctx, candidate.Url)
		if err != nil {
			// Recorded and moved past: one host refusing to archive says nothing about the next.
			attempt.Err = err
			attempts = append(attempts, attempt)
			continue
		}

		row, err := s.queries.SetContentSourceArchive(ctx, queries.SetContentSourceArchiveParams{
			ID:          candidate.ID,
			ArchivedUrl: &archived,
		})
		if err != nil {
			return attempts, fmt.Errorf("recording the archive of %s: %w", candidate.Url, err)
		}

		attempt.ArchivedURL = archived
		_ = row
		attempts = append(attempts, attempt)
	}

	return attempts, nil
}
