package content

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/pkg/urlkey"
)

// The appeal: a decision somebody disagrees with, and the moderator who did not make it.
//
// WHAT THIS FILE IS FOR. Slice 1 built the notice path -- an operator takes a URL or a host out of service
// and `mod_audit` records who decided. That path can only go one way, and a takedown that can only go one
// way is not a policy, it is a verdict. This is the other direction, and the plan's verification states the
// rule it turns on exactly: an appeal is heard by a DIFFERENT moderator, asserted by IDENTITY, not by role.
//
// WHY IDENTITY AND NOT A ROLE. A role check ("the decider holds MODERATE") passes for the same human
// holding a second role, which is precisely the failure this rule exists to prevent: the operator who
// decided to take something down does not get to hear the objection to their own decision. So the two
// comparisons below are between `users.id` values that live ON THE APPEAL ROW (`blocked_by_id`,
// `filed_by_id`), which is also why those columns exist there rather than being read back from the block:
// a release deletes the block, and the rule has to hold after that too (migration 141).
const (
	// AppealGranted releases the block: the objection was right, and the release happens through the SAME
	// path an operator's own release uses, in the SAME transaction as this decision.
	AppealGranted = "granted"
	// AppealDenied leaves the block in force: the decision stands, and it stands recorded as contested.
	AppealDenied = "denied"

	// auditTargetSourceAppeal is the `target_type` an appeal's audit rows carry, so "was this contested"
	// and "what did the moderator say" are answered by a row rather than by a support conversation.
	auditTargetSourceAppeal = "SOURCE_APPEAL"
)

var (
	// ErrNoSuchAppeal is returned when an appeal id matches nothing. The operator's queue is the only way
	// to obtain an id, so this says a stale queue was acted on rather than that a member did something
	// wrong.
	ErrNoSuchAppeal = errors.New("no such appeal")
	// ErrAppealEmptyStatement refuses an appeal that does not say what should be reviewed. A moderator's
	// queue is a list of reasons, and "please review" is not one.
	ErrAppealEmptyStatement = errors.New("an appeal must say what should be reviewed")
	// ErrAppealAlreadyFiled refuses a second appeal by the same person against the same live block
	// (the partial unique index of migration 141 is the backstop).
	ErrAppealAlreadyFiled = errors.New("this block has already been appealed by this member")
	// ErrAppealAlreadyDecided refuses a second decision, so the first outcome is the outcome.
	ErrAppealAlreadyDecided = errors.New("this appeal has already been decided")
	// ErrAppealSelfDecision is the rule this whole feature exists for: the operator who recorded the block
	// cannot decide the appeal against it, and the member who filed it cannot decide it either. The error
	// message names WHICH arm refused, because the remedy differs (a different moderator, or the appeal
	// being reassigned).
	ErrAppealSelfDecision = errors.New("an appeal is decided by a moderator other than the one who acted on it")
	// ErrAppealOutcome refuses an outcome that is not one of the two words the schema allows.
	ErrAppealOutcome = errors.New(`an appeal's outcome is "granted" or "denied"`)
	// ErrAppealUnkeyable refuses an appeal whose key cannot be normalized to what a block stores. An appeal
	// against a key nobody can compute would sit in the queue forever answering a question about a string
	// that does not exist.
	ErrAppealUnkeyable = errors.New("an appeal needs a URL or host this instance can key on")
	// ErrNoSuchBlock refuses an appeal against a key nothing is blocked under. It is the same answer the
	// release path gives a release that finds nothing -- "you wanted this unblocked and it is" -- except
	// that here the wanted state is a REVIEW, and there is nothing to review.
	ErrNoSuchBlock = errors.New("nothing is blocked under that key")
)

// appealAudit is the `data` of an appeal's audit row.
//
// It carries the KEY as well as the outcome because the appeal row itself can outlive the block and the
// moderator reading the trail months later is asking "what was this about, and what was decided" -- a
// question a bare outcome cannot answer.
type appealAudit struct {
	Kind     string `json:"kind"`
	Value    string `json:"value"`
	Outcome  string `json:"outcome,omitempty"`
	Released bool   `json:"released,omitempty"`
}

// FileAppeal records a contest against a refusal.
//
// The key is what a MEMBER has: the address they submitted and were refused, or the host. `(kind, value)`
// is therefore the input rather than an operator's row id -- the block list is ADMIN-only, and an appeal
// that can only be filed with an id nobody shows them is an appeal nobody files.
//
// Any authenticated member may file: an operator cannot know whose submission a URL was, and requiring the
// appellant to prove ownership would turn a complaint about a block into a claim about a person.
func (s *Service) FileAppeal(ctx context.Context, kind, value, statement string, filedBy uuid.UUID) (models.ContentSourceAppeal, error) {
	key, err := appealKey(kind, value)
	if err != nil {
		return models.ContentSourceAppeal{}, err
	}
	statement = strings.TrimSpace(statement)
	if statement == "" {
		return models.ContentSourceAppeal{}, ErrAppealEmptyStatement
	}

	var out models.ContentSourceAppeal
	err = s.withTxn(func(tx *queries.Queries) error {
		block, err := tx.FindContentSourceBlock(ctx, queries.FindContentSourceBlockParams{Kind: kind, Value: key})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// Named rather than generic: "nothing is blocked under that key" is a different answer
				// from "you already appealed this", and it is the answer that tells a member their URL
				// was refused for some other reason entirely.
				return fmt.Errorf("nothing is blocked under %s %q: %w", kind, key, ErrNoSuchBlock)
			}
			return fmt.Errorf("could not read the block list for %s %q: %w", kind, key, err)
		}

		row, err := tx.FileContentSourceAppeal(ctx, queries.FileContentSourceAppealParams{
			ID:          mustUUID(),
			BlockID:     uuid.NullUUID{UUID: block.ID, Valid: true},
			Kind:        kind,
			Value:       key,
			FiledByID:   nullableUser(filedBy),
			BlockedByID: block.CreatedByID,
			Statement:   statement,
		})
		if err != nil {
			if isUniqueViolation(err) {
				return ErrAppealAlreadyFiled
			}
			return fmt.Errorf("could not record the appeal: %w", err)
		}

		out = AppealToModel(row)
		if err := createAppealAudit(ctx, tx, filedBy, queries.ModAuditActionSOURCEAPPEAL, row.ID, statement,
			appealAudit{Kind: kind, Value: key}); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return models.ContentSourceAppeal{}, err
	}
	return out, nil
}

// DecideAppeal records a decision, ONCE, by somebody other than the people involved.
//
// The two identity checks and the release all happen in ONE transaction: the decision and its effect are
// one decision, and a release that landed after a failed decision write would be an effect with no cause in
// the trail.
//
// A GRANTED appeal releases the block through `releaseKey` -- the same function `ReleaseBlock` calls -- so
// there is one release path in the instance and no second DELETE that could come to mean something
// different. `Released` reports whether a live block was actually removed, which is a different fact from
// the outcome: a grant against a block somebody already released changes nothing, and claiming otherwise
// would put a false line in the audit trail.
func (s *Service) DecideAppeal(ctx context.Context, id uuid.UUID, outcome, note string, actor uuid.UUID) (models.ContentSourceAppeal, error) {
	if outcome != AppealGranted && outcome != AppealDenied {
		return models.ContentSourceAppeal{}, ErrAppealOutcome
	}

	var out models.ContentSourceAppeal
	err := s.withTxn(func(tx *queries.Queries) error {
		appeal, err := tx.FindContentSourceAppeal(ctx, id)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNoSuchAppeal
			}
			return fmt.Errorf("could not read the appeal: %w", err)
		}
		if appeal.DecidedAt.Valid {
			return ErrAppealAlreadyDecided
		}
		if err := appealDeciderAllowed(appeal, actor); err != nil {
			return err
		}

		released := false
		if outcome == AppealGranted {
			released, err = releaseKey(ctx, tx, appeal.Kind, appeal.Value, actor)
			if err != nil {
				return err
			}
		}

		decided, err := tx.DecideContentSourceAppeal(ctx, queries.DecideContentSourceAppealParams{
			DecidedByID:  nullableUser(actor),
			Outcome:      nullableString(outcome),
			DecisionNote: nullableString(strings.TrimSpace(note)),
			ID:           id,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// The UPDATE only matches an undecided row, so this is the concurrent case: another
				// moderator decided between our read and our write. The first decision is the decision.
				return ErrAppealAlreadyDecided
			}
			return fmt.Errorf("could not record the decision: %w", err)
		}

		out = AppealToModel(decided)
		out.Released = released
		return createAppealAudit(ctx, tx, actor, queries.ModAuditActionSOURCEAPPEALDECISION, id, note,
			appealAudit{Kind: appeal.Kind, Value: appeal.Value, Outcome: outcome, Released: released})
	})
	if err != nil {
		return models.ContentSourceAppeal{}, err
	}
	return out, nil
}

// Appeal reads one appeal, decided or not.
//
// Exists for the caller that has just acted on an id and wants to report what it did, and for the tests
// that prove the row SURVIVES the block it names (migration 141's decision 1) -- reading it back after a
// release is the only way that claim is checkable.
func (s *Service) Appeal(ctx context.Context, id uuid.UUID) (models.ContentSourceAppeal, error) {
	row, err := s.queries.FindContentSourceAppeal(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.ContentSourceAppeal{}, ErrNoSuchAppeal
		}
		return models.ContentSourceAppeal{}, err
	}
	return AppealToModel(row), nil
}

// Appeals lists appeals, oldest first -- a queue is answered in the order people waited.
//
// `pendingOnly` is the moderator's queue; the full list is the history an operator answering "has anybody
// contested our decisions, and how did we answer" reads. An appeal nobody can list is an appeal nobody
// answers, which is why this method exists rather than the mutation alone.
func (s *Service) Appeals(ctx context.Context, pendingOnly bool, limit, offset int32) ([]models.ContentSourceAppeal, error) {
	rows, err := s.queries.ListContentSourceAppeals(ctx, queries.ListContentSourceAppealsParams{
		PendingOnly: pendingOnly,
		RowLimit:    limit,
		RowOffset:   offset,
	})
	if err != nil {
		return nil, err
	}
	out := make([]models.ContentSourceAppeal, 0, len(rows))
	for _, row := range rows {
		out = append(out, AppealToModel(row))
	}
	return out, nil
}

// appealDeciderAllowed is the rule, in one place.
//
// Two arms, and BOTH are identity comparisons against values stored on the appeal row:
//
//  1. the operator who recorded the block -- they decided this already, and an appeal is the objection to
//     that decision;
//  2. the member who filed the appeal -- a person does not get to grant their own request.
//
// A DELETED ACCOUNT IS NOT A HOLE. `blocked_by_id` and `filed_by_id` are nullable (ON DELETE SET NULL), and
// a nil value matches nobody, which is right: there is no operator left to be the wrong decider, and no
// appellant left to decide their own appeal. The arms are checked separately so the refusal's message can
// name WHICH one fired -- "somebody else must decide" sends an operator looking for a colleague, and
// "you filed this" tells them the appeal is not theirs to rule on at all.
func appealDeciderAllowed(appeal queries.ContentSourceBlockAppeal, actor uuid.UUID) error {
	if actor == uuid.Nil {
		// Unreachable through the API (both mutations are authenticated), and refused anyway: an
		// unauthenticated decision would match neither arm and silently pass the rule.
		return fmt.Errorf("an appeal needs a decider: %w", ErrAppealSelfDecision)
	}
	if appeal.BlockedByID.Valid && appeal.BlockedByID.UUID == actor {
		return fmt.Errorf("you recorded the block this appeal contests, so another moderator must decide it: %w",
			ErrAppealSelfDecision)
	}
	if appeal.FiledByID.Valid && appeal.FiledByID.UUID == actor {
		return fmt.Errorf("you filed this appeal, so another moderator must decide it: %w", ErrAppealSelfDecision)
	}
	return nil
}

// appealKey normalizes what an appellant typed into the key a block stores.
//
// The same two spellings the block table accepts, produced by the same functions the pipeline uses
// (`urlkey.Normalize`, and NormalizeHost for a host): an appeal whose key is normalized differently from
// the block it contests matches nothing and sits in the queue forever.
func appealKey(kind, value string) (string, error) {
	switch kind {
	case BlockKindURL:
		normalized, err := urlkey.Normalize(strings.TrimSpace(value))
		if err != nil {
			return "", fmt.Errorf("%w: %v", ErrAppealUnkeyable, err)
		}
		return normalized, nil
	case BlockKindHost:
		host := NormalizeHost(value)
		if host == "" {
			return "", fmt.Errorf("%w: %q has no host in it", ErrAppealUnkeyable, value)
		}
		return host, nil
	default:
		return "", fmt.Errorf("%w: %q is not a block kind", ErrAppealUnkeyable, kind)
	}
}

// createAppealAudit writes the trail row for a filed or decided appeal.
//
// The TARGET is the appeal, not the block: the question `mod_audit` answers here is "what happened to this
// contest", and pointing the row at the block would make the trail unreadable the moment a release deletes
// it -- which is exactly the case an operator investigates.
func createAppealAudit(ctx context.Context, tx *queries.Queries, actor uuid.UUID, action queries.ModAuditAction,
	appealID uuid.UUID, reason string, data appealAudit) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal audit data: %w", err)
	}
	if _, err := tx.CreateModAudit(ctx, queries.CreateModAuditParams{
		ID:         mustUUID(),
		Action:     action,
		UserID:     nullableUser(actor),
		TargetID:   appealID,
		TargetType: auditTargetSourceAppeal,
		Data:       payload,
		Reason:     nullableString(strings.TrimSpace(reason)),
	}); err != nil {
		return fmt.Errorf("failed to record the appeal audit row: %w", err)
	}
	return nil
}

// AppealToModel converts an appeal row for the API.
func AppealToModel(row queries.ContentSourceBlockAppeal) models.ContentSourceAppeal {
	appeal := models.ContentSourceAppeal{
		ID:        models.ID(row.ID),
		Kind:      row.Kind,
		Value:     row.Value,
		Statement: row.Statement,
		FiledAt:   row.FiledAt.Time,
	}
	if row.BlockID.Valid {
		appeal.BlockID = models.ID(row.BlockID.UUID)
	}
	if row.FiledByID.Valid {
		appeal.FiledByID = models.ID(row.FiledByID.UUID)
	}
	if row.BlockedByID.Valid {
		appeal.BlockedByID = models.ID(row.BlockedByID.UUID)
	}
	if row.DecidedByID.Valid {
		appeal.DecidedByID = models.ID(row.DecidedByID.UUID)
	}
	// The three decision fields are copied as POINTERS, so a pending appeal serialises as null rather than
	// as an empty string and a year-1 timestamp. A copy rather than the row's own pointer, because the row
	// belongs to a scan buffer the caller may reuse.
	if row.Outcome != nil {
		outcome := *row.Outcome
		appeal.Outcome = &outcome
	}
	if row.DecisionNote != nil {
		note := *row.DecisionNote
		appeal.DecisionNote = &note
	}
	if row.DecidedAt.Valid {
		decidedAt := row.DecidedAt.Time
		appeal.DecidedAt = &decidedAt
	}
	return appeal
}

// isUniqueViolation reports whether an error is the database's unique_violation.
//
// Checked by SQLSTATE rather than by matching a message: the code is the contract Postgres actually offers,
// and the message is prose that changes between versions (the same reasoning `internal/service/collage`
// records for its own snapshot constraint).
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}
