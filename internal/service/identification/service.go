// Package identification implements the identification board from SPEC §5:
// "Which Was That…?"
//
// The design in one paragraph: a query is a QUESTION and its candidates are
// SUGGESTIONS; neither becomes metadata until a human resolves the query. The
// service records what people decided, never infers it from a tally.
//
// The rule running through every function here: a vote is EVIDENCE, not authority.
// SPEC §5 says identifying an orphan scene "can trigger metadata creation and
// replication" -- CAN, through the existing edit and draft paths, by a person. A
// plurality vote is not a creation. An archive that fills itself from votes fills
// itself with confidently wrong records, and every one of them then becomes a
// canonical link that search and recommendations point at.
package identification

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/stashapp/stash-box/internal/queries"
)

// TargetType is what a query is trying to identify.
//
// A closed set in Go for the same reason trust.KindEnum is: §5 names five kinds
// and a string that names a sixth is a string this code does not know what to do
// with. The database column stays unconstrained, so widening it later is a code
// change rather than a migration.
type TargetType string

const (
	TargetPerformer TargetType = "performer"
	TargetScene     TargetType = "scene"
	TargetStudio    TargetType = "studio"
	TargetSite      TargetType = "site"
	TargetTag       TargetType = "tag"
)

// AllTargetTypes is every type the service accepts.
var AllTargetTypes = []TargetType{
	TargetPerformer, TargetScene, TargetStudio, TargetSite, TargetTag,
}

// Valid reports whether a target type is one this version knows.
func (t TargetType) Valid() bool {
	for _, known := range AllTargetTypes {
		if t == known {
			return true
		}
	}
	return false
}

// String makes TargetType printable in error messages without a cast at every
// call site.
func (t TargetType) String() string { return string(t) }

// Status is a query's state.
type Status string

const (
	// StatusOpen is a question nobody has resolved yet. The only state anyone
	// queues from.
	StatusOpen Status = "open"
	// StatusSolved means a human accepted a resolution.
	StatusSolved Status = "solved"
	// StatusAbandoned means the community gave up. Distinct from open because
	// "nobody will ever solve this" and "nobody has solved this yet" want
	// different handling: an abandoned query re-surfacing in the queue forever is
	// how a board fills with questions nobody wants.
	StatusAbandoned Status = "abandoned"
)

// The service's errors, one per distinct consequence. They are separate types
// rather than one ErrValidation because the callers act differently on each: a
// not-found is a 404, a closed query is a conflict the user can do nothing about,
// and a category error is a bug in the client worth surfacing loudly.
var (
	// ErrQueryNotFound is returned for a missing query.
	ErrQueryNotFound = errors.New("identification query not found")
	// ErrCandidateNotFound is returned for a missing candidate. Separate from
	// ErrQueryNotFound because the two mean different things to a client: a
	// missing query is a stale link, and a missing candidate is a vote landing on
	// a suggestion someone else removed.
	ErrCandidateNotFound = errors.New("identification candidate not found")
	// ErrQueryClosed is returned when acting on a query that is no longer open.
	ErrQueryClosed = errors.New("identification query is no longer open")
	// ErrUnknownTargetType is returned for a type this version does not know.
	ErrUnknownTargetType = errors.New("unknown identification target type")
	// ErrEmptyDescription is returned when a query has no question in it.
	ErrEmptyDescription = errors.New("an identification query needs a description")
	// ErrMismatchedCandidateType is returned when a candidate's type differs from
	// the query's target type.
	ErrMismatchedCandidateType = errors.New(
		"candidate type must match the query's target type")
	// ErrMismatchedResolutionType is returned when a resolution names a different
	// type of entity than the query asked about.
	ErrMismatchedResolutionType = errors.New(
		"resolution type must match the query's target type")
	// ErrNoCandidates is returned when resolving a query with nothing suggested.
	ErrNoCandidates = errors.New("cannot resolve a query with no candidates")
)

// MaxDescriptionLength bounds a query's text.
//
// 4000 characters: long enough for a detailed recollection including a pasted
// quote, short enough that the board's queue renders. Enforced in Go rather than
// as a database CHECK so the caller gets a message naming the field rather than a
// constraint violation naming a column, and so a longer limit can be introduced
// per-instance later without a migration.
const MaxDescriptionLength = 4000

// maxCandidatesPerQuery bounds the candidate list.
//
// 50 rather than §5's implied open-endedness: a query with 200 candidates has
// stopped being a question with answers and become a fishing expedition, and the
// vote UI becomes unusable. The limit is on the SUGGESTION count, not the vote
// count, so a popular query is not capped.
const maxCandidatesPerQuery = 50

// maxCandidatesToReturn bounds one page of a candidate list.
const maxCandidatesToReturn = 200

// PostQuery is a new identification request.
type PostQuery struct {
	TargetType TargetType
	// TargetID is optional: a user often has the scene and wants the performer
	// identified within it, so the query is about a thing they can point at.
	TargetID *uuid.UUID
	// Description is what they remember. Required, and the only required field:
	// §5 lists description, collage, snapshot, frame and quote as alternatives,
	// not as a form with required inputs, and a user with a half-formed memory
	// should be able to post it and let the community ask for more.
	Description string
	// CollageID and SnapshotID are the visual evidence, when there is any.
	CollageID  *uuid.UUID
	SnapshotID *uuid.UUID
	CreatedBy  uuid.NullUUID
}

// Query is a stored identification request.
type Query struct {
	ID          uuid.UUID
	TargetType  TargetType
	TargetID    *uuid.UUID
	Description string
	CollageID   *uuid.UUID
	SnapshotID  *uuid.UUID
	CreatedBy   *uuid.UUID
	Status      Status
	// Resolved fields are set only when Status is StatusSolved. The database
	// CHECK enforces that pairing, so a non-nil ResolvedID with a non-solved
	// status is not representable.
	ResolvedType *TargetType
	ResolvedID   *uuid.UUID
	ResolvedBy   *uuid.UUID
	// ResolvedAt is the moment the query was solved. Distinct from UpdatedAt:
	// updating a solved query's description does not change when it was solved.
	ResolvedAt *time.Time
	// time.Time, not `any`. The column is NOT NULL, and the sqlc row already
	// types it as time.Time (internal/queries/models.go:337-338). Widening it to
	// `any` here meant the API converter could not assign it to the model's
	// time.Time, so the field was silently dropped -- and because the schema
	// declares `createdAt: Time!`, dropping it made the entire
	// listOpenIdentificationQueries query fail rather than render a blank date.
	// `any` is never the right type for a value a consumer must put in a
	// non-null field; it only defers the error to a place that cannot explain it.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Candidate is a suggested answer, with its tally.
type Candidate struct {
	ID          uuid.UUID
	QueryID     uuid.UUID
	EntityType  TargetType
	EntityID    uuid.UUID
	Note        *string
	SuggestedBy *uuid.UUID
	VoteCount   int
}

// Service manages the identification board.
type Service struct {
	queries *queries.Queries
	withTxn queries.WithTxnFunc
}

// NewService creates a new identification service.
func NewService(queries *queries.Queries, withTxn queries.WithTxnFunc) *Service {
	return &Service{queries: queries, withTxn: withTxn}
}

// WithTxn executes a function within a transaction.
func (s *Service) WithTxn(fn func(*queries.Queries) error) error {
	return s.withTxn(fn)
}

// Post creates an identification query.
func (s *Service) Post(ctx context.Context, post PostQuery) (*Query, error) {
	if !post.TargetType.Valid() {
		return nil, fmt.Errorf("%w: %q", ErrUnknownTargetType, post.TargetType)
	}
	// Trimmed for the EMPTINESS check only, so a description of three spaces is
	// caught. The stored value is the caller's original, untrimmed: a user who
	// pasted a quote with meaningful leading whitespace has quoted something, and
	// silently trimming it changes what they said.
	if strings.TrimSpace(post.Description) == "" {
		return nil, ErrEmptyDescription
	}
	if len(post.Description) > MaxDescriptionLength {
		return nil, fmt.Errorf("description is %d characters, over the %d limit",
			len(post.Description), MaxDescriptionLength)
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	row, err := s.queries.CreateIdentificationQuery(ctx, queries.CreateIdentificationQueryParams{
		ID:          id,
		TargetType:  string(post.TargetType),
		TargetID:    nullUUID(post.TargetID),
		Description: post.Description,
		CollageID:   nullUUID(post.CollageID),
		SnapshotID:  nullUUID(post.SnapshotID),
		CreatedBy:   post.CreatedBy,
	})
	if err != nil {
		return nil, err
	}
	return queryFromRow(row), nil
}

// Get reads one query.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*Query, error) {
	row, err := s.queries.GetIdentificationQuery(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: %s", ErrQueryNotFound, id)
		}
		return nil, err
	}
	return queryFromRow(row), nil
}

// ListOpen returns the board's queue: open queries, newest first.
func (s *Service) ListOpen(ctx context.Context, limit int) ([]*Query, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.queries.ListOpenIdentificationQueries(ctx, int32(limit))
	if err != nil {
		return nil, err
	}
	return queriesFromRows(rows), nil
}

// Suggest adds a candidate to an open query.
//
// The candidate's type must match the query's target type. Suggesting a performer
// for a scene query is a category error, and it is refused here rather than
// tolerated: the candidate list is rendered as "possible answers to this question",
// and a performer in a list of possible scenes is a bug the community would have
// to diagnose individually.
// The note column exists and is intentionally not exposed here: no caller in this
// version has a way to supply one, and a parameter nothing can reach is a
// parameter the code does not need. A later mutation exposes it.
func (s *Service) Suggest(ctx context.Context, queryID uuid.UUID, entityType TargetType, entityID uuid.UUID, by uuid.NullUUID) (*Candidate, error) {
	if !entityType.Valid() {
		return nil, fmt.Errorf("%w: %q", ErrUnknownTargetType, entityType)
	}

	query, err := s.Get(ctx, queryID)
	if err != nil {
		return nil, err
	}
	if query.Status != StatusOpen {
		return nil, fmt.Errorf("%w: %s is %s", ErrQueryClosed, queryID, query.Status)
	}
	if query.TargetType != entityType {
		return nil, fmt.Errorf("%w: query is about a %s, candidate is a %s",
			ErrMismatchedCandidateType, query.TargetType, entityType)
	}

	existing, err := s.queries.ListIdentificationCandidates(ctx, queries.ListIdentificationCandidatesParams{
		QueryID: queryID,
		Limit:   int32(maxCandidatesToReturn),
	})
	if err != nil {
		return nil, err
	}
	if len(existing) >= maxCandidatesPerQuery {
		return nil, fmt.Errorf("a query may have at most %d candidates", maxCandidatesPerQuery)
	}

	id, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	row, err := s.queries.AddIdentificationCandidate(ctx, queries.AddIdentificationCandidateParams{
		ID:          id,
		QueryID:     queryID,
		EntityType:  string(entityType),
		EntityID:    entityID,
		SuggestedBy: by,
	})
	if err != nil {
		return nil, err
	}
	return &Candidate{
		ID:          row.ID,
		QueryID:     row.QueryID,
		EntityType:  TargetType(row.EntityType),
		EntityID:    row.EntityID,
		Note:        row.Note,
		SuggestedBy: uuidPtr(row.SuggestedBy),
		VoteCount:   0,
	}, nil
}

// GetCandidate reads one candidate, for the path that re-reads a candidate after
// a vote changes its tally.
//
// Separate from ListCandidates because that one is scoped to a query and returns
// every row, which is the wrong shape for "give me this one back". A vote
// mutation needs exactly that one, and the vote count is already in the row.
func (s *Service) GetCandidate(ctx context.Context, candidateID uuid.UUID) (*Candidate, error) {
	row, err := s.queries.GetIdentificationCandidate(ctx, candidateID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: %s", ErrCandidateNotFound, candidateID)
		}
		return nil, err
	}
	// The candidate's own tally, which is what a vote mutation has to return.
	// The joined row does not carry it, so it is counted here rather than in the
	// query: this reads one candidate, not a query's list, and a count is cheaper
	// than rebuilding the grouped query for a single id.
	tally, err := s.queries.CountVotesForCandidate(ctx, candidateID)
	if err != nil {
		return nil, err
	}
	return &Candidate{
		ID:          row.ID,
		QueryID:     row.QueryID,
		EntityType:  TargetType(row.EntityType),
		EntityID:    row.EntityID,
		SuggestedBy: uuidPtr(row.SuggestedBy),
		VoteCount:   int(tally),
	}, nil
}

// Vote records a vote for a candidate.
//
// Voting on a query that is no longer open is refused rather than recorded. The
// vote would be counted for nothing -- nothing reads the tally of a resolved
// query -- and worse, it would be counted in the voter's "Detective" score, which
// is the one place a vote has lasting effect. A user who votes on a resolved query
// has been told their effort was wasted, which is worse than an error.
func (s *Service) Vote(ctx context.Context, candidateID uuid.UUID, userID uuid.UUID) error {
	row, err := s.queries.GetIdentificationCandidate(ctx, candidateID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("identification candidate %s not found", candidateID)
		}
		return err
	}
	if Status(row.QueryStatus) != StatusOpen {
		return fmt.Errorf("%w: the query behind this candidate is %s",
			ErrQueryClosed, row.QueryStatus)
	}
	return s.queries.VoteForIdentificationCandidate(ctx, queries.VoteForIdentificationCandidateParams{
		CandidateID: candidateID,
		UserID:      userID,
	})
}

// Unvote removes a vote.
//
// Provided because a user who votes and then changes their mind should be able
// to, and because an accidental tap should be undoable. The composite primary key
// makes a double-vote impossible, so this is the only way to lower a tally.
func (s *Service) Unvote(ctx context.Context, candidateID uuid.UUID, userID uuid.UUID) error {
	return s.queries.UnvoteIdentificationCandidate(ctx,
		queries.UnvoteIdentificationCandidateParams{
			CandidateID: candidateID,
			UserID:      userID,
		})
}

// HasVoted reports whether a user has voted for a candidate, so the UI can render
// a vote as a state rather than as an action that silently does nothing on a
// second tap.
func (s *Service) HasVoted(ctx context.Context, candidateID uuid.UUID, userID uuid.UUID) (bool, error) {
	return s.queries.HasVotedForCandidate(ctx,
		queries.HasVotedForCandidateParams{
			CandidateID: candidateID,
			UserID:      userID,
		})
}

// ListCandidates returns a query's candidates, most-voted first.
func (s *Service) ListCandidates(ctx context.Context, queryID uuid.UUID) ([]*Candidate, error) {
	rows, err := s.queries.ListIdentificationCandidates(ctx,
		queries.ListIdentificationCandidatesParams{
			QueryID: queryID,
			Limit:   int32(maxCandidatesToReturn),
		})
	if err != nil {
		return nil, err
	}
	out := make([]*Candidate, 0, len(rows))
	for _, row := range rows {
		out = append(out, &Candidate{
			ID:          row.ID,
			QueryID:     row.QueryID,
			EntityType:  TargetType(row.EntityType),
			EntityID:    row.EntityID,
			Note:        row.Note,
			SuggestedBy: uuidPtr(row.SuggestedBy),
			VoteCount:   int(row.VoteCount),
		})
	}
	return out, nil
}

// Resolve records what a HUMAN decided a query was.
//
// NOT a vote threshold, and the absence is deliberate. §5 wants gamification and
// the board is more useful with a consensus rule, but a threshold would mean a
// plurality silently creates metadata, and the whole package exists to prevent
// that. So this requires a named user and records them: the resolution is an act
// by a person, and resolvable_id/resolved_by are what §5's "detective" reputation
// and audit trail are built on.
//
// The resolution type must match the query's target type for the same reason a
// candidate's must: a scene query resolves to a scene. §5's example of "who is
// this performer" is a query whose TARGET is the performer, not a scene query
// with a performer answer, and conflating the two is how a query ends up resolved
// to something of the wrong kind.
//
// One transaction: the status change and the trust event for the solver are one
// fact, and a resolution that earned trust without being recorded would let a
// user farm the "Detective" level by solving queries that then vanished.
func (s *Service) Resolve(ctx context.Context, queryID uuid.UUID, resolvedType TargetType, resolvedID uuid.UUID, by uuid.UUID, recordTrust func(context.Context) error) (*Query, error) {
	if !resolvedType.Valid() {
		return nil, fmt.Errorf("%w: %q", ErrUnknownTargetType, resolvedType)
	}

	var resolved *Query
	err := s.withTxn(func(tx *queries.Queries) error {
		query, err := queryFromRowOrErr(tx.GetIdentificationQuery(ctx, queryID))
		if err != nil {
			return err
		}
		if query.Status != StatusOpen {
			return fmt.Errorf("%w: %s is %s", ErrQueryClosed, queryID, query.Status)
		}
		if query.TargetType != resolvedType {
			return fmt.Errorf("%w: query is about a %s, resolution is a %s",
				ErrMismatchedResolutionType, query.TargetType, resolvedType)
		}

		candidates, err := tx.ListIdentificationCandidates(ctx,
			queries.ListIdentificationCandidatesParams{
				QueryID: queryID,
				Limit:   int32(maxCandidatesToReturn),
			})
		if err != nil {
			return err
		}
		if len(candidates) == 0 {
			// Resolving a query nobody suggested anything for would record a
			// conclusion with no evidence behind it, which is the failure mode this
			// package exists to prevent. The poster can resolve their own query with
			// the resolution path in the metadata layer, where an edit is recorded.
			return ErrNoCandidates
		}

		// Whether the chosen entity was actually SUGGESTED, or is a fresh
		// identification the resolver is making themselves.
		wasSuggested := false
		for _, c := range candidates {
			if c.EntityID == resolvedID {
				wasSuggested = true
				break
			}
		}

		row, err := tx.ResolveIdentificationQuery(ctx, queries.ResolveIdentificationQueryParams{
			ID:           queryID,
			ResolvedType: strPtr(string(resolvedType)),
			ResolvedID:   uuid.NullUUID{UUID: resolvedID, Valid: true},
			ResolvedBy:   uuid.NullUUID{UUID: by, Valid: true},
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// The UPDATE is guarded on status='open', so no rows means someone
				// resolved it between the read above and here. A second resolution
				// must not overwrite the first.
				return fmt.Errorf("%w: %s was resolved concurrently", ErrQueryClosed, queryID)
			}
			return err
		}

		// Trust is recorded for the SOLVER, and only when the answer they gave was
		// one the community had already proposed. Resolving to something nobody
		// suggested is still useful -- the resolver did the identifying themselves
		// -- but it is a different act and §5's reputation is for reading the
		// community's suggestions and picking right.
		if recordTrust != nil && wasSuggested {
			if err := recordTrust(ctx); err != nil {
				return err
			}
		}

		resolved = queryFromRow(row)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return resolved, nil
}

// Abandon marks a query dead.
func (s *Service) Abandon(ctx context.Context, queryID uuid.UUID) error {
	row, err := s.queries.AbandonIdentificationQuery(ctx, queryID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: %s", ErrQueryNotFound, queryID)
		}
		return err
	}
	_ = row
	return nil
}

// ResolvedFor returns the solved queries about one entity.
//
// This is the canonical-link read from §5: the board's conclusions indexed
// against real metadata, so an entity page can show what the community worked out
// about it.
func (s *Service) ResolvedFor(ctx context.Context, entityType TargetType, entityID uuid.UUID, limit int) ([]*Query, error) {
	if !entityType.Valid() {
		return nil, fmt.Errorf("%w: %q", ErrUnknownTargetType, entityType)
	}
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	rows, err := s.queries.ListResolvedQueriesForEntity(ctx,
		queries.ListResolvedQueriesForEntityParams{
			ResolvedType: strPtr(string(entityType)),
			ResolvedID:   uuid.NullUUID{UUID: entityID, Valid: true},
			Limit:        int32(limit),
		})
	if err != nil {
		return nil, err
	}
	return queriesFromRows(rows), nil
}

// DetectiveScore is how many candidates a user has voted on across open queries.
//
// Backs §5's "Detective" leaderboard. Counted through the query table so a vote
// on a candidate whose query has since been resolved stops counting: the vote was
// evidence about a question that no longer exists, and a leaderboard that keeps
// counting it rewards voting on questions that were answered without them.
func (s *Service) DetectiveScore(ctx context.Context, userID uuid.UUID) (int, error) {
	count, err := s.queries.CountIdentificationVotesForUser(ctx, userID)
	if err != nil {
		return 0, err
	}
	return int(count), nil
}

// LeadingCandidate is the most-voted candidate for a query, if there is one.
//
// The point of this is the TIE and the absence. It returns nil when no candidate
// exists, and it does NOT return "the top one" when the top two are level: §5's
// gamification wants a leaderboard and this feeds one, but a leaderboard that
// silently picked a winner among equals would be reporting a consensus that does
// not exist. A tie is a tie, and a caller wanting a deterministic leaderboard row
// can sort by entity id itself.
func LeadingCandidate(candidates []Candidate) *Candidate {
	if len(candidates) == 0 {
		return nil
	}
	top := candidates[0]
	tied := false
	for _, c := range candidates[1:] {
		switch {
		case c.VoteCount > top.VoteCount:
			// A later candidate beats the current best, so whatever we had was
			// provisional and any tie seen so far is void.
			top = c
			tied = false
		case c.VoteCount == top.VoteCount:
			tied = true
		}
	}
	if tied {
		return nil
	}
	return &top
}
