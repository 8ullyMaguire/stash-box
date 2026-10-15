package content

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/pkg/urlkey"
)

// The takedown path: the instance's answer to a notice, and the only way into the terminal status
// from outside the health machine (migration 134).
//
// WHY THIS IS A SERVICE METHOD AND NOT A MIGRATION OR A RUNBOOK. Every consequence of a takedown
// already existed and every one of them was tested -- with a raw `UPDATE` inside the test: the sweep
// cannot move a dmca row, the mirror will not offer one, the archiver will not submit one, the
// pipeline refuses to re-attach one. What did not exist was a way for the INSTANCE to be told. An
// operator holding a notice had psql. That is a mechanism without a product, and the difference
// between the two is exactly the difference between an instance that can answer a complaint and one
// that cannot.
//
// FOUR PROPERTIES, and they are the whole design:
//
//  1. ONE DECISION, ONE TRANSACTION. The rows are marked, the key is blocked and the audit row is
//     written together or not at all. Half-applied is the worst state available: sources removed
//     with no block means the URL can be re-submitted tomorrow and indexed, and a block with no
//     audit row means the instance cannot say why. The pipeline's own processing is deliberately
//     transactionless (see the package comment in internal/service/ingest) because each of its
//     writes is idempotent and the queue row is the record; this is not a job, it is one decision
//     by one person, and it is the case the pattern is for.
//  2. THE BLOCK IS WRITTEN EVEN WHEN NOTHING MATCHED. A notice about a URL this instance does not
//     have is the case the block exists for: without it, the notice is a no-op today and the URL
//     arrives tomorrow through a member's submission.
//  3. THE AUDIT IS PART OF THE DECISION, NOT A LOG LINE. `mod_audit` is where an instance answers
//     "what did you do, when, and on what" -- so the reference the notice carries is required input
//     rather than an optional note, and a takedown without one is refused.
//  4. ONE KEY, COMPUTED BY THE SAME FUNCTION THE PIPELINE USES. `urlkey` (pkg/urlkey) is imported
//     here and by the pipeline, so a block's key and a job's key are the same string by
//     construction. A second spelling of "the host" would store a block that matches nothing while
//     the pipeline keeps admitting the host, and nothing in the tree would notice.

// The kinds of key a block carries. These are the values migration 134's CHECK constraint allows.
const (
	// BlockKindURL blocks one normalized URL: what a copyright notice names.
	BlockKindURL = "url"
	// BlockKindHost blocks a whole host: what a report about a site names, and the instrument for
	// the case a single URL takedown cannot answer -- the same work re-uploaded under fifty URLs on
	// one tube site.
	BlockKindHost = "host"
)

// auditTargetSourceBlock is the `target_type` a takedown's audit row carries. The target is the
// BLOCK row rather than the sources it changed, because the block is what outlives the decision: the
// sources can be re-added, deleted or re-titled, and the question the audit answers is "what does
// this instance refuse, and why".
const auditTargetSourceBlock = "SOURCE_BLOCK"

var (
	// ErrNoReference is returned when a takedown arrives without the notice it acts on.
	//
	// Required, not defaulted: an audit trail whose entries say "takedown" and nothing else cannot
	// answer the only question anyone asks of it. An operator removing a link for their own reasons
	// writes that reason here, which is the point -- the field records a decision, and every
	// decision has one.
	ErrNoReference = errors.New("a takedown must record the notice or reason it acts on")
	// ErrNoHost is returned by BlockHost when the input has no host in it at all.
	ErrNoHost = errors.New("a host block needs a host (or a URL containing one)")
)

// Takedown is what a decision did: what it refuses from now on, and what it changed.
//
// One shape for URL takedowns and host blocks because the operator's question is the same for both
// -- "did it land, and what did it take out" -- and two shapes would make every caller that only
// wants to report it handle two cases.
type Takedown struct {
	// Block is the key this instance now refuses.
	Block models.ContentSourceBlock
	// Sources are the records the decision moved to the terminal status, in the order the statement
	// returned them. Empty is a normal, honest outcome: it means nothing here was serving the URL
	// (or the host) and the block is now what stops it arriving.
	Sources []models.ContentSource
	// AlreadyBlocked reports that this instance was refusing the key before this call.
	//
	// A SECOND NOTICE IS NOT AN ERROR. Complaints repeat, and they arrive about URLs an operator
	// already removed -- so the same call run twice has to be a success the second time with
	// "nothing changed, and here is the block that was already there". Reporting a unique-constraint
	// violation instead would answer a legal workflow that worked with a failure message, and the
	// operator's next move would be to escalate something already handled. This is the same rule the
	// ingest path applies to a duplicate submission: a duplicate is an ANSWER, not an error.
	AlreadyBlocked bool
}

// TakedownURL takes down every record of a URL and blocks the URL from entering again.
//
// The URL is normalized here rather than by the caller, so an operator pastes what the notice says
// and the key is the pipeline's key. A URL that cannot be normalized at all (no scheme, a
// non-http scheme, no host) is refused with the normalizer's own error: there is nothing to block
// and no row that could match a key nobody can compute.
func (s *Service) TakedownURL(ctx context.Context, rawURL string, actor uuid.UUID, reference string) (Takedown, error) {
	if strings.TrimSpace(reference) == "" {
		return Takedown{}, ErrNoReference
	}
	normalized, err := urlkey.Normalize(rawURL)
	if err != nil {
		return Takedown{}, fmt.Errorf("a takedown needs a URL this instance can key on: %w", err)
	}

	var out Takedown
	err = s.withTxn(func(tx *queries.Queries) error {
		rows, err := tx.TakedownContentSourcesByURL(ctx, &normalized)
		if err != nil {
			return err
		}

		block, already, err := blockKey(ctx, tx, BlockKindURL, normalized, reference, actor)
		if err != nil {
			return err
		}

		if err := createSourceAudit(ctx, tx, actor, queries.ModAuditActionSOURCETAKEDOWN, block, sourceDecisionAudit{
			Kind:        BlockKindURL,
			Value:       normalized,
			URL:         rawURL,
			Reference:   reference,
			Changed:     len(rows),
			ChangedURLs: sourceURLs(rows),
		}); err != nil {
			return err
		}

		out = Takedown{Block: ListBlockToModel(block), Sources: sourceModels(rows), AlreadyBlocked: already}
		return nil
	})
	if err != nil {
		return Takedown{}, err
	}
	return out, nil
}

// BlockHost refuses a whole host and takes down every source this instance already has on it.
//
// The host is parsed out of whatever the operator has: a URL from a report, or a bare host. Both
// go through `urlkey.SiteHost`, which is the same function the pipeline uses to decide which host a
// submitted URL belongs to, so the value stored here is comparable to the value the pipeline
// computes. Hosts are lowercased and a leading `www.` is dropped by that function; a port is not
// part of a host, so one block covers every port on it.
func (s *Service) BlockHost(ctx context.Context, hostInput string, actor uuid.UUID, reason string) (Takedown, error) {
	if strings.TrimSpace(reason) == "" {
		return Takedown{}, ErrNoReference
	}
	host := NormalizeHost(hostInput)
	if host == "" {
		return Takedown{}, ErrNoHost
	}

	var out Takedown
	err := s.withTxn(func(tx *queries.Queries) error {
		rows, err := tx.TakedownContentSourcesByHost(ctx, hostPattern(host))
		if err != nil {
			return err
		}

		block, already, err := blockKey(ctx, tx, BlockKindHost, host, reason, actor)
		if err != nil {
			return err
		}

		if err := createSourceAudit(ctx, tx, actor, queries.ModAuditActionSOURCEBLOCK, block, sourceDecisionAudit{
			Kind:        BlockKindHost,
			Value:       host,
			Reason:      reason,
			Changed:     len(rows),
			ChangedURLs: sourceURLs(rows),
		}); err != nil {
			return err
		}

		out = Takedown{Block: ListBlockToModel(block), Sources: sourceModels(rows), AlreadyBlocked: already}
		return nil
	})
	if err != nil {
		return Takedown{}, err
	}
	return out, nil
}

// blockKey writes the block row, or reuses the one that already refuses this key.
//
// The reuse is the whole reason this is a function: the mark statement and the audit row are the
// same either way, and the only difference between a first notice and the fifth is whether the block
// existed. `created_by_id` and `reason` stay as the FIRST decision wrote them, because they answer
// "what does this instance refuse and who said so" -- the later notices are in `mod_audit`, one row
// each, which is where a question about the notices themselves is answered.
func blockKey(ctx context.Context, tx *queries.Queries, kind, value, reason string, actor uuid.UUID) (queries.ContentSourceBlock, bool, error) {
	existing, err := tx.FindContentSourceBlock(ctx, queries.FindContentSourceBlockParams{Kind: kind, Value: value})
	if err == nil {
		return existing, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return queries.ContentSourceBlock{}, false, fmt.Errorf("could not read the block list for %s %q: %w", kind, value, err)
	}

	block, err := tx.CreateContentSourceBlock(ctx, queries.CreateContentSourceBlockParams{
		ID:          mustUUID(),
		Kind:        kind,
		Value:       value,
		Reason:      reason,
		CreatedByID: nullableUser(actor),
	})
	if err != nil {
		return queries.ContentSourceBlock{}, false, fmt.Errorf("failed to block %s %q: %w", kind, value, err)
	}
	return block, false, nil
}

// ReleaseBlock stops refusing a key. Reports whether there was a block to release.
//
// The row is deleted rather than flagged, so this table is the current policy and `mod_audit` is the
// history (migration 134 says why). Releasing does NOT put the sources back: a taken-down source
// stays down until something re-submits it, because un-removing content on the strength of a release
// would be asserting that the complaint was withdrawn, and a release usually means the opposite
// (the same material came back somewhere else, or the block was too broad).
func (s *Service) ReleaseBlock(ctx context.Context, kind, value string, actor uuid.UUID) (bool, error) {
	if kind != BlockKindURL && kind != BlockKindHost {
		return false, fmt.Errorf("unknown block kind %q", kind)
	}

	released := false
	err := s.withTxn(func(tx *queries.Queries) error {
		block, err := tx.FindContentSourceBlock(ctx, queries.FindContentSourceBlockParams{Kind: kind, Value: value})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// Not an error: an operator releasing a key that is not blocked wanted it unblocked
				// and it is. The audit row is written anyway, because "somebody tried to release
				// this and there was nothing to release" is a fact about the instance's state.
				block = queries.ContentSourceBlock{Kind: kind, Value: value, ID: mustUUID()}
			} else {
				return err
			}
		} else {
			released = true
		}

		n, err := tx.DeleteContentSourceBlock(ctx, queries.DeleteContentSourceBlockParams{Kind: kind, Value: value})
		if err != nil {
			return err
		}
		if n > 1 {
			// Unreachable while the unique constraint holds. If it ever fires, the instance has two
			// rows for one key and the audit below is recording a release that removed both.
			return fmt.Errorf("expected at most one block row for (%s, %s), deleted %d", kind, value, n)
		}

		action := queries.ModAuditActionSOURCEBLOCK
		if kind == BlockKindURL {
			action = queries.ModAuditActionSOURCETAKEDOWN
		}
		return createSourceAudit(ctx, tx, actor, action, block, sourceDecisionAudit{
			Kind:     kind,
			Value:    value,
			Released: true,
		})
	})
	if err != nil {
		return false, err
	}
	return released, nil
}

// Blocks lists what this instance currently refuses, newest first.
//
// The operator who has to answer "do we block that host?" cannot answer it from anywhere else, and
// the number is small enough that the whole list is the honest response.
func (s *Service) Blocks(ctx context.Context, limit, offset int32) ([]models.ContentSourceBlock, error) {
	rows, err := s.queries.ListContentSourceBlocks(ctx, queries.ListContentSourceBlocksParams{
		RowLimit:  limit,
		RowOffset: offset,
	})
	if err != nil {
		return nil, err
	}
	out := make([]models.ContentSourceBlock, 0, len(rows))
	for _, row := range rows {
		out = append(out, ListBlockToModel(row))
	}
	return out, nil
}

// Blocked reports whether a key is currently refused, with the row that says so.
//
// Used by the tests as the reader that proves a takedown LANDED, and by the resolver that answers
// "why can I not add this URL" without going through the pipeline.
func (s *Service) Blocked(ctx context.Context, kind, value string) (models.ContentSourceBlock, bool, error) {
	row, err := s.queries.FindContentSourceBlock(ctx, queries.FindContentSourceBlockParams{Kind: kind, Value: value})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.ContentSourceBlock{}, false, nil
		}
		return models.ContentSourceBlock{}, false, err
	}
	return ListBlockToModel(row), true, nil
}

// ListBlockToModel converts a block row for the API.
func ListBlockToModel(row queries.ContentSourceBlock) models.ContentSourceBlock {
	block := models.ContentSourceBlock{
		ID:        models.ID(row.ID),
		Kind:      row.Kind,
		Value:     row.Value,
		Reason:    row.Reason,
		CreatedAt: row.CreatedAt.Time,
	}
	if row.CreatedByID.Valid {
		block.CreatedByID = models.ID(row.CreatedByID.UUID)
	}
	return block
}

// NormalizeHost turns what an operator typed into the value a block stores.
//
// It accepts a URL or a bare host, because the report in the operator's hand may be either, and a
// block that silently failed to apply because they pasted the wrong shape is the worst outcome
// available. A bare host gets a scheme so it parses; `urlkey.SiteHost` then lowercases it, drops a
// leading `www.` and drops any port.
//
// A TRAILING DOT IS KEPT, deliberately. `example.com.` is the same host as `example.com` by DNS, and
// it would be defensible to fold them -- but `urlkey.SiteHost` does not fold them, and this function
// exists to produce a value that function would produce. Folding here only would make a block on
// `example.com.` match nothing the pipeline ever computes.
func NormalizeHost(input string) string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return ""
	}
	if !strings.Contains(trimmed, "://") {
		trimmed = "http://" + trimmed
	}
	return urlkey.SiteHost(trimmed)
}

// hostPattern builds the statement's regex for "every source on this host".
//
// Anchored at the authority component, with the host quoted, and optional userinfo, `www.` and port.
// The `www.` is allowed even though SiteHost strips it, because the column this matches holds the URL
// AS SUBMITTED: a row can read `https://www.example.com/x` while the block's value is `example.com`,
// and a pattern built only from the value would leave that row live. Verified against the trap cases
// (notexample.com, example.com.evil.com, sub.example.com) in the statement's test.
func hostPattern(host string) string {
	return `^[a-z][a-z0-9+.-]*://([^/?#@]*@)?(www\.)?` + regexp.QuoteMeta(host) + `(:[0-9]+)?([/?#].*)?$`
}

// sourceDecisionAudit is the `data` of a takedown's audit row: the decision plus the evidence that it
// was carried out.
//
// ChangedURLs is the part that matters later. "We took down 3 sources" is a claim; the three URLs are
// the record, and an instance asked "was <url> removed?" months later can answer it from the audit
// even after the source row has been deleted or re-added by a later submission.
type sourceDecisionAudit struct {
	Kind        string   `json:"kind"`
	Value       string   `json:"value"`
	URL         string   `json:"url,omitempty"`
	Reference   string   `json:"reference,omitempty"`
	Reason      string   `json:"reason,omitempty"`
	Changed     int      `json:"changed"`
	ChangedURLs []string `json:"changed_urls,omitempty"`
	Released    bool     `json:"released,omitempty"`
}

func createSourceAudit(ctx context.Context, tx *queries.Queries, actor uuid.UUID, action queries.ModAuditAction, block queries.ContentSourceBlock, data sourceDecisionAudit) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal audit data: %w", err)
	}

	reason := data.Reference
	if reason == "" {
		reason = data.Reason
	}

	_, err = tx.CreateModAudit(ctx, queries.CreateModAuditParams{
		ID:         mustUUID(),
		Action:     action,
		UserID:     nullableUser(actor),
		TargetID:   block.ID,
		TargetType: auditTargetSourceBlock,
		Data:       payload,
		Reason:     nullableString(reason),
	})
	if err != nil {
		return fmt.Errorf("failed to create audit record: %w", err)
	}
	return nil
}

func sourceURLs(rows []queries.ContentSource) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Url)
	}
	return out
}

func sourceModels(rows []queries.ContentSource) []models.ContentSource {
	out := make([]models.ContentSource, 0, len(rows))
	for _, row := range rows {
		out = append(out, ContentSourceToModel(row))
	}
	return out
}

// mustUUID generates a v7 id, which is what every other write in this repo uses for a new row.
//
// It panics rather than returning an error because the alternative is worse: a failed id generation
// would abort a takedown, and a takedown that does not happen because of a random-number failure is
// the same outcome as one that silently did nothing. The error is only possible if the process is
// out of entropy, in which case the instance has larger problems than this row.
func mustUUID() uuid.UUID {
	id, err := uuid.NewV7()
	if err != nil {
		panic(fmt.Sprintf("could not generate a uuid: %v", err))
	}
	return id
}

func nullableUser(id uuid.UUID) uuid.NullUUID {
	return uuid.NullUUID{UUID: id, Valid: id != uuid.Nil}
}

func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
