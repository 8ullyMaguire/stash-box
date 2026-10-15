package ingest

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/content"
	"github.com/stashapp/stash-box/internal/service/ingest/extract"
	"github.com/stashapp/stash-box/pkg/logger"
)

// The pipeline: a submitted URL becomes a content record, or a recorded reason why not
// (spec docs/spec/feature-ingestion.md, plan phase 2).
//
// ONE JOB AT A TIME, NO TRANSACTION. The design is that every write is idempotent and the
// QUEUE ROW is the record of what happened: the fetch, the decide and the write run in
// whatever order the database allows, and a worker that dies halfway leaves a 'running' row
// the sweep reclaims (phase 9) rather than a half-written record nobody can find. Attaching
// is `ON CONFLICT DO NOTHING`, finishing is an UPDATE of the row the claim already owns, so
// running a job twice is wrong at worst about a counter and is never wrong about the data.
// A transaction here would buy atomicity between two statements that are each already
// safe to repeat, and cost the property that matters more: a job that can be resumed.
//
// WHEN THE NETWORK IS TOUCHED, and it is less often than it looks: only the path that might
// CREATE a record fetches the page. A URL that is refused, already claimed, or destined for
// a named scene is settled from the database alone (see process, where the ordering is
// argued). The consequence worth stating: `confidence` is NULL on those rows rather than
// 0.0, and a test that asserts on a confidence for an attach is asserting about a fetch
// that should not have happened.

// ErrInvalidURL is returned by Submit for a URL the pipeline refuses to queue. It is a
// callER error (the submitter can fix it), not a callEE failure, so the API layer turns it
// into a message rather than a 500.
var ErrInvalidURL = errors.New("the URL cannot be ingested")

// What Submit reports back. Two outcomes, and neither is an error: a submission that is a
// duplicate was handled, and telling a submitter that duplicates fail is how a queue
// collects rows nobody looks at (spec §5).
const (
	StatusQueued    = "queued"
	StatusDuplicate = "duplicate"
)

type Submission struct {
	Status string
	// QueueID is the queue row, absent for a duplicate (which by definition did not create
	// one).
	QueueID *uuid.UUID
	// NormalizedURL is what the submitter's URL was reduced to -- the key dedup runs on.
	// Returned because a submitter who sent `?utm_source=nl` and got "duplicate" needs to
	// see that we considered it the same page they submitted last week.
	NormalizedURL string
	// Reason is the human-readable explanation for a duplicate. Empty otherwise.
	Reason string
}

type Service struct {
	q         *queries.Queries
	extractor extract.Extractor
}

func NewService(q *queries.Queries, extractor extract.Extractor) *Service {
	return &Service{q: q, extractor: extractor}
}

// Submit normalises a URL and puts it in the queue.
//
// target is the scene a reader submitted the URL FROM (a scene page's "add a source" form),
// or nil for a bare URL submission. See migration 110 for why the row carries it.
func (s *Service) Submit(ctx context.Context, rawURL string, target, submittedBy *uuid.UUID) (Submission, error) {
	// The content-plane switch, asked FIRST -- before the URL is even normalised. The order
	// matters and it is the same order the sweep uses: "this instance does not fetch" is a
	// property of the server rather than a complaint about the URL, so a malformed URL submitted
	// to a non-fetching instance is refused as a posture and not as a bad input. See
	// internal/service/content/policy.go (respec, 2026-10-15).
	if err := content.RequireFetching(); err != nil {
		return Submission{}, err
	}

	normalized, err := NormalizeURL(rawURL)
	if err != nil {
		return Submission{}, fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}

	row, err := s.q.EnqueueIngest(ctx, queries.EnqueueIngestParams{
		Url:            rawURL,
		UrlNormalized:  normalized,
		SubmittedBy:    nullableUUID(submittedBy),
		TargetEntityID: nullableUUID(target),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// The partial unique index refused it: this URL is queued, in flight, or already
		// ingested. Not an error -- see the constant's comment.
		return Submission{
			Status:        StatusDuplicate,
			NormalizedURL: normalized,
			Reason:        "already queued or already ingested",
		}, nil
	}
	if err != nil {
		return Submission{}, err
	}

	id := row.ID
	return Submission{Status: StatusQueued, QueueID: &id, NormalizedURL: normalized}, nil
}

// Drain claims and processes up to max jobs, newest-claimable first.
//
// It does NOT loop forever: the caller is a cron tick, and a tick that processes an
// unbounded backlog blocks every other job in the same process. The bound is a parameter
// rather than a constant so a backfill can pass a larger one deliberately.
func (s *Service) Drain(ctx context.Context, max int) (int, error) {
	processed := 0
	for i := 0; i < max; i++ {
		job, err := s.q.ClaimIngestJob(ctx)
		if errors.Is(err, pgx.ErrNoRows) {
			// Nothing claimable. The ordinary case when the queue is empty and the normal
			// case when every pending row is backing off.
			break
		}
		if err != nil {
			return processed, err
		}

		if err := s.process(ctx, job); err != nil {
			// The job's own outcome is already recorded (process records it before
			// returning); an error reaching here means the RECORDING failed, which is a
			// database problem and not a per-job problem, so the drain stops rather than
			// burning through the queue against a broken connection.
			logger.Errorf("ingest: could not record the outcome of job %s: %s", job.ID, err)
			return processed, err
		}
		processed++
	}
	return processed, nil
}

func (s *Service) process(ctx context.Context, job queries.IngestQueue) error {
	// The content-plane switch, and the reason it is HERE rather than only in Submit: a row can
	// exist from a time when fetching was on, or from a direct insert, and the function that
	// fetches is the one that must refuse. Submit only stops new work arriving.
	//
	// THE REFUSAL IS RECORDED AND NOT RETURNED, which is this pipeline's own rule -- the queue row
	// is the record of what happened. Returning it instead would leave the claimed row in
	// 'running' forever and make Drain log "could not record the outcome", which would be a lie:
	// the recording is what failed to happen, not the writing. Recorded as a TRANSIENT failure, so
	// the row goes back to 'pending' with a backoff rather than being burned: the URL is fine and
	// the operator's posture may change, and FailIngestJob's transient arm is exactly the shape for
	// "the attempt did not succeed and will be made again" (its own comment: a host that timed out
	// is not a host that refused -- here, an instance that does not fetch is not a URL that is gone).
	if err := content.RequireFetching(); err != nil {
		return s.recordExtractFailure(ctx, job, err)
	}

	// ---- The URL-level questions, asked BEFORE the network is touched ----------------
	//
	// The order is a policy decision and not a convenience, and implementing the takedown
	// path is what made it visible: the first version of this function fetched every
	// submitted URL and only THEN consulted what was already known, which meant a URL this
	// instance had been told to take down was requested again -- and a URL we already had
	// was re-fetched to learn something no one was going to use. Both are costs with no
	// answer attached: the takedown is a human's judgement and re-requesting is how the
	// instance keeps asking a question it has already been answered, and the second fetch
	// buys metadata for a record that already exists.
	//
	// So the three questions that need nothing from the page -- is this refused, is it
	// already claimed, does the submitter name a destination -- are answered first, and
	// the network is touched only by the one path that might have to CREATE a record.
	known, err := s.gatherURLFacts(ctx, job)
	if err != nil {
		return err
	}
	if decision := Decide(known); decision != DecisionCreate {
		switch decision {
		case DecisionReject:
			return s.recordRejection(ctx, job, known.DenyReason)
		default:
			target := known.Target()
			if target == nil {
				// Unreachable through Decide's own rules (both Attach arms require a
				// target) and handled rather than assumed: a nil dereference here would
				// take the drain down for every other job.
				return s.recordRejection(ctx, job, "no destination for an attach")
			}
			// No metadata, because there was no fetch. The source is recorded with what is
			// known -- the URL, its site, the entity -- and its health is the checker's
			// business (phase 9), not something this row invents.
			return s.attach(ctx, job, *target, nil, job.Url, job.UrlNormalized, string(decision))
		}
	}

	// ---- A record might be created, so the page has to be read ----------------------
	//
	// The RAW url is fetched, not the normalised one: normalisation is a claim about which
	// URLs are the same PAGE, and it is not a claim that the sorted/trimmed form is a URL
	// this host serves. The raw form is what the submitter saw work.
	meta, extractErr := s.extractor.Extract(ctx, job.Url)
	if extractErr != nil {
		return s.recordExtractFailure(ctx, job, extractErr)
	}

	// A shortened URL's destination replaces the submitted one: that is where the content
	// is, and it is what a reader should be sent to. The queue's key stays as submitted,
	// because that is what a second submission of the same short link will match.
	sourceURL := job.Url
	if meta.ResolvedURL != "" {
		sourceURL = meta.ResolvedURL
	}
	normalized, err := NormalizeURL(sourceURL)
	if err != nil {
		// The extractor followed a redirect to something the normaliser refuses (a
		// javascript: redirect, a non-http scheme). Keep the submitted key: the row is
		// still processed, and losing the submission over a redirect we did not like would
		// be the tail wagging the dog.
		normalized = job.UrlNormalized
	}

	// The fingerprint is the one fact that cannot be known without fetching, so it is
	// added here and Decide is consulted a second time. That second call can only return
	// Merge or Create -- the first call already answered Reject and Attach -- and calling
	// the same rule table rather than re-implementing the two remaining arms is what keeps
	// the order in one place.
	if err := s.addFingerprintMatch(ctx, &known, meta); err != nil {
		return err
	}
	switch Decide(known) {
	case DecisionReject, DecisionAttach:
		// Impossible after the first call, and handled rather than assumed for the same
		// reason as the nil target above.
		return s.recordRejection(ctx, job, "a URL fact changed between the two decisions")
	case DecisionMerge:
		target := known.Target()
		if target == nil {
			return s.recordRejection(ctx, job, "no destination for a merge")
		}
		return s.attach(ctx, job, *target, &meta, sourceURL, normalized, string(DecisionMerge))
	default:
		return s.create(ctx, job, meta, sourceURL, normalized)
	}
}

// gatherURLFacts collects what Decide can rule on without reading the page: the named
// target, whether anything already claims this URL, and whether it was taken down.
func (s *Service) gatherURLFacts(ctx context.Context, job queries.IngestQueue) (Known, error) {
	known := Known{}
	if job.TargetEntityID.Valid {
		id := job.TargetEntityID.UUID
		known.EntityID = &id
	}

	rows, err := s.q.FindContentSourcesByNormalizedURL(ctx, queries.FindContentSourcesByNormalizedURLParams{
		EntityType:    "scene",
		UrlNormalized: &job.UrlNormalized,
	})
	if err != nil {
		return known, err
	}
	for _, row := range rows {
		if row.Status == "dmca" {
			// A URL this instance has already been told to take down. A DENIAL, not an
			// attach: re-attaching a taken-down URL because it was submitted again would
			// make the takedown depend on nobody re-submitting.
			known.Denied = true
			known.DenyReason = "a source with this URL was taken down"
		}
		known.EntityIDs = append(known.EntityIDs, row.EntityID)
		// Note: `status = 'dead'` is deliberately NOT a denial. Dead content can come back
		// (sites restore pages, hosts rotate), and refusing to re-record a live URL because
		// it was dead a year ago is the kind of permanence nobody asked for. The health
		// sweep owns that judgement; the ingest path only refuses what a human refused.
	}
	return known, nil
}

// addFingerprintMatch looks up the hashes the extractor produced and records the first
// scene that already has one.
//
// Only consulted when the URL-level facts left nothing to decide, which is why it is
// separate from gatherURLFacts: this is the lookup that costs a fetch to be able to run at
// all, and folding it in would suggest it can be answered without one.
func (s *Service) addFingerprintMatch(ctx context.Context, known *Known, meta extract.Metadata) error {
	if known.Denied || len(known.EntityIDs) > 0 || known.EntityID != nil {
		// Already decided. Skipping the lookup is not just an optimisation: a Merge here
		// would be a second, contradicting answer to a question the first pass settled.
		return nil
	}
	for _, fp := range meta.Fingerprints {
		scenes, err := s.q.FindScenesByFingerprint(ctx, queries.FindScenesByFingerprintParams{
			Hash: fp.Hash, Algorithm: fp.Algorithm,
		})
		if err != nil {
			return err
		}
		if len(scenes) > 0 {
			// The query is ORDER BY scene_id, so this is the lowest id among the matches
			// rather than whichever row the planner happened to return: the same
			// submission must land on the same scene on every run, or a bug report about a
			// source appearing on the wrong scene is unreproducible.
			match := scenes[0]
			known.FingerprintMatch = &match
			return nil
		}
	}
	return nil
}

// attach records that target is reachable at this URL, then finishes the job.
//
// meta is nil for the paths that decided without a fetch. That is not "unknown metadata"
// being conflated with an empty record: the confidence column stays NULL, which is the
// honest statement that no page was read, rather than 0.0, which would say the extractor
// looked and found nothing.
//
// The order matters less than it looks: the insert is idempotent (a duplicate is a
// no-op) and the finish is the only statement that moves the row out of 'running', so a
// crash between them leaves a reclaimable job rather than a lost one.
func (s *Service) attach(ctx context.Context, job queries.IngestQueue, target uuid.UUID, meta *extract.Metadata, sourceURL, normalized, outcome string) error {
	siteID, _, ok, err := s.requireSite(ctx, job, sourceURL)
	if err != nil || !ok {
		return err
	}
	if err := s.recordSource(ctx, target, sourceURL, normalized, siteID, job.SubmittedBy); err != nil {
		return err
	}

	var confidence *float64
	if meta != nil {
		c := meta.Confidence
		confidence = &c
	}
	return s.finish(ctx, job, outcome, &target, confidence)
}

// requireSite resolves a source's directory entry, or records the job as refused.
//
// The nil case is unreachable after NormalizeURL (which refuses a URL with no host), and it
// is handled by RECORDING a refusal rather than by returning an error, because of what an
// error out of process means: Drain stops on one (see its own comment), so a single
// unlabellable URL would halt every job behind it rather than failing alone.
//
// `ok` is returned rather than an error so the two callers cannot forget to check it.
func (s *Service) requireSite(ctx context.Context, job queries.IngestQueue, url string) (uuid.UUID, string, bool, error) {
	siteID, siteName, err := s.resolveSite(ctx, url)
	if err != nil {
		return uuid.UUID{}, "", false, err
	}
	if !siteID.Valid {
		return uuid.UUID{}, "", false, s.recordRejection(ctx, job, "no site could be resolved for this URL")
	}
	return siteID.UUID, siteName, true, nil
}

// recordSource writes both halves of "this scene is reachable at this URL".
//
// TWO TABLES, and phase 2's own integration test is what proved both are needed:
// content_sources owns the URL's health (status, canonical, last_checked), scene_urls owns
// the claim, and Scene.sources reads the scene_sources view, which JOINs the two (migration
// 108). Writing only the health row produced a source that existed, was queryable by hand,
// and appeared on no page -- and every assertion made at the table level passed.
//
// THE ORDER IS NOT COSMETIC: health row first, membership second. The scene_urls mirror
// trigger would otherwise insert the health row itself WITHOUT the normalised key (the
// mirror is SQL, normalisation is Go -- migration 110 argues why it has to be), and this
// path's own ON CONFLICT DO NOTHING would then leave the key NULL on the one set of rows
// where it is available.
func (s *Service) recordSource(ctx context.Context, entityID uuid.UUID, url, normalized string, siteID uuid.UUID, createdBy uuid.NullUUID) error {
	// siteID is a plain UUID here, not a nullable one, and that asymmetry with the column
	// is the point: content_sources.site_id is nullable and scene_urls.site_id is NOT NULL,
	// so "a source with no site" can exist in one table and cannot be a membership. Callers
	// go through requireSite, which guarantees a site -- and `scene_urls` is what makes the
	// source visible at all, so a path that could reach here without one has no business
	// writing either row.
	_, err := s.q.InsertContentSource(ctx, queries.InsertContentSourceParams{
		EntityType:    "scene",
		EntityID:      entityID,
		Url:           url,
		UrlNormalized: &normalized,
		SiteID:        uuid.NullUUID{UUID: siteID, Valid: true},
		Platform:      nil,
		CreatedBy:     createdBy,
	})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		// ErrNoRows is the ON CONFLICT: this entity already has this URL. The ordinary
		// outcome of a duplicate submission.
		return err
	}

	return s.q.AttachSceneURL(ctx, queries.AttachSceneURLParams{
		SceneID: entityID,
		Url:     url,
		SiteID:  siteID,
	})
}

func (s *Service) create(ctx context.Context, job queries.IngestQueue, meta extract.Metadata, sourceURL, normalized string) error {
	sceneID := uuid.Must(uuid.NewV7())
	siteID, siteName, ok, err := s.requireSite(ctx, job, sourceURL)
	if err != nil || !ok {
		return err
	}

	// A record with no title is unusable in every list and search surface, so the URL
	// stands in when the page stated nothing. That is not a good title -- it is a title
	// that says where the content is, which an edit can replace and a blank cannot.
	title := strings.TrimSpace(meta.Title)
	if title == "" {
		title = sourceURL
	}

	_, err = s.q.CreateAggregateScene(ctx, queries.CreateAggregateSceneParams{
		ID:       sceneID,
		Title:    &title,
		Details:  sceneDetails(meta, siteName),
		Date:     strPtr(meta.Date),
		Duration: intPtr(meta.DurationSeconds),
	})
	if err != nil {
		return err
	}

	if err := s.recordSource(ctx, sceneID, sourceURL, normalized, siteID, job.SubmittedBy); err != nil {
		return err
	}

	s.attachExtractedFingerprints(ctx, sceneID, job, meta)

	confidence := meta.Confidence
	return s.finish(ctx, job, string(DecisionCreate), &sceneID, &confidence)
}

// attachExtractedFingerprints stores the hashes the extractor produced, attributed to the
// submitting user.
//
// ONLY when there is a submitting user, and that is a schema constraint rather than a
// preference: scene_fingerprints.user_id is NOT NULL, the fork's fingerprints are
// per-submitter evidence (that is what the vote and the corroboration rules are built on),
// and there is no such thing as a machine's user. A crawler-submitted URL therefore yields
// a scene with no fingerprint, which is the honest record: nobody here has vouched for a
// hash.
//
// A failure here is logged and does not fail the job. The record exists and is findable;
// losing the pipeline's whole result over a hash that could not be stored would trade a
// complete record for a fingerprint nobody was promised.
func (s *Service) attachExtractedFingerprints(ctx context.Context, sceneID uuid.UUID, job queries.IngestQueue, meta extract.Metadata) {
	if len(meta.Fingerprints) == 0 || !job.SubmittedBy.Valid {
		return
	}
	for _, fp := range meta.Fingerprints {
		row, err := s.q.CreateFingerprint(ctx, queries.CreateFingerprintParams{
			Hash: fp.Hash, Algorithm: fp.Algorithm,
		})
		if err != nil {
			logger.Errorf("ingest: could not store fingerprint %s/%d: %s", fp.Algorithm, fp.Hash, err)
			continue
		}
		_, err = s.q.CreateSceneFingerprints(ctx, []queries.CreateSceneFingerprintsParams{{
			FingerprintID: row.ID,
			SceneID:       sceneID,
			UserID:        job.SubmittedBy.UUID,
			Duration:      fp.Duration,
		}})
		if err != nil {
			logger.Errorf("ingest: could not attach fingerprint %s/%d to scene %s: %s",
				fp.Algorithm, fp.Hash, sceneID, err)
		}
	}
}

// recordExtractFailure turns an extractor error into a queue outcome.
//
// THREE ANSWERS, because a retry policy that treats them alike is either useless or
// harmful:
//
//   - Gone (404/410) is PERMANENT and lands 'failed'. The content is not there and asking
//     again will not change that. It is deliberately NOT 'done': nothing was recorded, and
//     an outcome of 'created'/'attached'/'merged' would be a claim about a record that does
//     not exist. (The spec's §5 table said "the source is marked dead and the row is done";
//     that is wrong for this path and the spec was corrected -- there is no source row to
//     mark dead when nothing claims the URL, and where something does claim it, writing
//     health is the sweep's job, not the ingest path's.)
//   - Unsupported (a login wall, a paywall, a non-HTML body) is PERMANENT. A retry cannot
//     fix it, and the reason is recorded so a person is not left re-queueing it.
//   - Unreachable is TRANSIENT: back to 'pending' with a backoff. A host that timed out is
//     not a host that refused.
func (s *Service) recordExtractFailure(ctx context.Context, job queries.IngestQueue, extractErr error) error {
	permanent := errors.Is(extractErr, extract.ErrGone) || errors.Is(extractErr, extract.ErrUnsupportedPage)

	reason := "unreachable"
	switch {
	case errors.Is(extractErr, extract.ErrGone):
		reason = "gone"
	case errors.Is(extractErr, extract.ErrUnsupportedPage):
		reason = "unsupported_page"
	}

	msg := extractErr.Error()
	_, err := s.q.FailIngestJob(ctx, queries.FailIngestJobParams{
		Permanent:      permanent,
		LastError:      &msg,
		Reason:         &reason,
		BackoffSeconds: BackoffSeconds(job.Attempts),
		ID:             job.ID,
	})
	return err
}

// recordRejection writes a refusal, which is not a failure.
//
// It goes through RejectIngestJob rather than FailIngestJob's permanent arm, and the
// difference is what each status means: `rejected` is the pipeline declining a URL before
// it asks the network anything (a takedown, a host with no site), and `failed` is a fetch
// outcome. Recording a policy decision as a failure makes "how many of the last hundred
// submissions were refused rather than broken" a string comparison over `reason`, which is
// the argument the spec makes for having one column for the state and one for the decision.
func (s *Service) recordRejection(ctx context.Context, job queries.IngestQueue, reason string) error {
	if reason == "" {
		reason = "refused"
	}
	msg := reason
	_, err := s.q.RejectIngestJob(ctx, queries.RejectIngestJobParams{
		LastError: &msg,
		Reason:    &reason,
		ID:        job.ID,
	})
	return err
}

func (s *Service) finish(ctx context.Context, job queries.IngestQueue, outcome string, resultSceneID *uuid.UUID, confidence *float64) error {
	params := queries.FinishIngestJobParams{
		Outcome:       &outcome,
		ResultSceneID: nullableUUID(resultSceneID),
		ID:            job.ID,
	}
	if confidence != nil {
		params.Confidence = pgtype.Float4{Float32: float32(*confidence), Valid: true}
	}
	_, err := s.q.FinishIngestJob(ctx, params)
	return err
}

// resolveSite finds, or creates, the directory entry a source belongs to.
//
// FINDING IS THE ORDINARY CASE and creating is the aggregator's arrival: the directory is
// curated by people, and the hosts it does not have are the ones this programme exists to
// find. The creation is argued at EnsureIngestSite's own comment; the short version is that
// scene_urls.site_id is NOT NULL, so without a site row there is no membership, and without
// a membership the source exists and appears nowhere.
func (s *Service) resolveSite(ctx context.Context, url string) (uuid.NullUUID, string, error) {
	row, err := s.q.FindSiteForURL(ctx, &url)
	switch {
	case err == nil:
		return uuid.NullUUID{UUID: row.ID, Valid: true}, row.Name, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return uuid.NullUUID{}, "", err
	}
	return s.ensureSite(ctx, url)
}

// ingestSiteValidTypes is the role observed for a host a scene page was read from. One
// value, not all three: a site claiming SCENE, PERFORMER and STUDIO would be the directory
// asserting a host serves everything, which is a claim nobody made.
var ingestSiteValidTypes = []string{"SCENE"}

// ensureSite creates the directory entry for a URL's host, or returns the one that exists.
//
// The regex is the host with its metacharacters quoted, which is what makes this run once
// per host rather than once per URL: FindSiteForURL matches on regex, and the pattern is
// deliberately unanchored because that is the directory's own convention (`@url ~ regex`).
func (s *Service) ensureSite(ctx context.Context, rawURL string) (uuid.NullUUID, string, error) {
	host := SiteHost(rawURL)
	if host == "" {
		// No host, so no site, and the caller handles a nil site: the source is still
		// recorded in content_sources. Reaching here means NormalizeURL accepted a URL
		// whose host SiteHost cannot see, and refusing the whole ingestion over that would
		// trade a complete record for a missing label.
		return uuid.NullUUID{}, "", nil
	}

	id, err := uuid.NewV7()
	if err != nil {
		return uuid.NullUUID{}, "", err
	}

	scheme := "https"
	if parsed, parseErr := url.Parse(rawURL); parseErr == nil && parsed.Scheme != "" {
		scheme = parsed.Scheme
	}
	homepage := scheme + "://" + host
	pattern := regexp.QuoteMeta(host)

	row, err := s.q.EnsureIngestSite(ctx, queries.EnsureIngestSiteParams{
		ID:          id,
		Name:        host,
		Url:         &homepage,
		Regex:       &pattern,
		ValidTypes:  ingestSiteValidTypes,
		Highlighted: false,
	})
	if err != nil {
		return uuid.NullUUID{}, "", err
	}
	return uuid.NullUUID{UUID: row.ID, Valid: true}, row.Name, nil
}

// sceneDetails writes what the extractor learned that has no column of its own.
//
// `details` is free text and this is deliberately boring text: a studio NAME is not a
// studio_id (a directory entry a human curates), performer names are not links (phase 2's
// rule that a machine's guess is evidence, not authority), and the extractor's own name is
// recorded so a wrong record can be traced to the thing that made it.
func sceneDetails(meta extract.Metadata, siteName string) *string {
	var parts []string
	if meta.Studio != "" {
		parts = append(parts, "Studio: "+meta.Studio)
	}
	if len(meta.PerformerNames) > 0 {
		parts = append(parts, "Performers: "+strings.Join(meta.PerformerNames, ", "))
	}
	if siteName != "" {
		parts = append(parts, "Site: "+siteName)
	}
	if meta.Source != "" {
		parts = append(parts, "Metadata from: "+meta.Source)
	}
	if len(parts) == 0 {
		return nil
	}
	details := strings.Join(parts, "\n")
	return &details
}

func nullableUUID(id *uuid.UUID) uuid.NullUUID {
	if id == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *id, Valid: true}
}

func strPtr(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

func intPtr(v int) *int {
	if v == 0 {
		return nil
	}
	return &v
}
