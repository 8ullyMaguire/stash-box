package api

import (
	"context"
	"errors"

	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/auth"
	"github.com/stashapp/stash-box/internal/dataloader"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/service/identification"
	"github.com/stashapp/stash-box/internal/service/trust"
)

// Identification board resolvers (SPEC §5).
//
// The rule these enforce at the API boundary: a query is a QUESTION and a
// candidate is a SUGGESTION. Nothing here writes to scenes, performers, studios,
// sites or tags, and the absence of such a mutation is deliberate rather than
// pending. SPEC §5 says an identification "can trigger metadata creation" -- CAN,
// through the existing edit path, by a person. A vote is evidence; treating a
// plurality as authority is how an archive fills with confidently wrong records
// that then become canonical links search points at.

// identificationTargetType converts the schema enum to the service's.
//
// The two are separate types rather than one shared string so the service's
// closed set stays closed: the schema accepts whatever its enum allows, and this
// is where an unrecognised value is refused by name rather than stored and matched
// against no candidates ever.
func identificationTargetType(t models.IdentificationTargetType) identification.TargetType {
	return identification.TargetType(t)
}

// toModelQuery converts a stored query to its GraphQL shape.
//
// Candidates, ResolvedBy and ResolvedAt are filled in by the caller rather than
// here, because all three need a database read and this function is called from
// list queries that would otherwise turn one read into N+1.
func toModelQuery(q *identification.Query) *models.IdentificationQuery {
	if q == nil {
		return nil
	}
	out := &models.IdentificationQuery{
		ID:          q.ID,
		TargetType:  models.IdentificationTargetType(q.TargetType),
		TargetID:    q.TargetID,
		Description: q.Description,
		SnapshotID:  q.SnapshotID,
		Status:      models.IdentificationStatus(q.Status),
		// CreatedAt is `Time!` in the schema (identification.graphql:72), so leaving
		// it zero is not a cosmetic omission: gqlgen rejects the whole field and
		// `listOpenIdentificationQueries` returns
		//   "the requested element is null which the schema does not allow"
		// with data:null -- the board renders as an error page rather than as a
		// half-empty list.
		//
		// Found by driving the real app, not by a test. Every unit test mocked the
		// query result, so the mapping was never exercised against a row that
		// actually came from the database.
		CreatedAt: q.CreatedAt,
	}
	if q.ResolvedType != nil {
		t := models.IdentificationTargetType(*q.ResolvedType)
		out.ResolvedType = &t
	}
	out.ResolvedID = q.ResolvedID
	return out
}

// toModelQueries converts a list.
func toModelQueries(list []*identification.Query) []models.IdentificationQuery {
	out := make([]models.IdentificationQuery, 0, len(list))
	for _, q := range list {
		if converted := toModelQuery(q); converted != nil {
			out = append(out, *converted)
		}
	}
	return out
}

// queryToModelWithResolution converts a query and fills the fields that need a
// read of their own.
//
// ResolvedBy and ResolvedAt come from a second call rather than being carried on
// the stored struct, and the reason is that they are not the same shape of data:
// ResolvedBy is a USER reference that has to be loaded through the dataloader, and
// ResolvedAt is a timestamp the sqlc row types as an untyped value. Fetching the
// row again is one indexed read by primary key, and it keeps a single conversion
// point rather than two that can drift.
func queryToModelWithResolution(ctx context.Context, s *identification.Service, q *identification.Query) (*models.IdentificationQuery, error) {
	out := toModelQuery(q)
	if out == nil {
		return nil, nil
	}
	if q.ResolvedBy != nil {
		user, err := dataloader.For(ctx).UserByID.Load(*q.ResolvedBy)
		if err != nil {
			return nil, err
		}
		out.ResolvedBy = user
	}
	// `resolvedAt` is the column named resolved_at, not updated_at. The old code
	// asserted q.UpdatedAt to time.Time and published THAT as the resolution
	// time, so a solved query reported the moment its row was last touched as
	// the moment it was solved. `ResolvedAt` was added to the service Query
	// precisely so this could be read from the right place.
	//
	// The type assertion was also what hid the first bug: with UpdatedAt typed
	// `any`, `q.UpdatedAt.(time.Time)` compiled and the whole `if` was a silent
	// no-op whenever the assertion failed, which is why `resolvedAt` was always
	// null in the live app while every service test still passed.
	if q.ResolvedAt != nil {
		ts := *q.ResolvedAt
		out.ResolvedAt = &ts
	}
	return out, nil
}

// candidateToModel converts one candidate, loading its suggester.
func candidateToModel(ctx context.Context, s *identification.Service, c *identification.Candidate, viewer uuid.UUID) (*models.IdentificationCandidate, error) {
	out := &models.IdentificationCandidate{
		ID:         c.ID,
		QueryID:    c.QueryID,
		EntityType: models.IdentificationTargetType(c.EntityType),
		EntityID:   c.EntityID,
		Note:       c.Note,
		// Without this line the tally is silently zero on every response. The
		// field is declared non-null in the schema, so GraphQL accepts the zero
		// and every service-level test still passes -- the count is in the
		// service's struct and simply never crossed the boundary.
		VoteCount: c.VoteCount,
	}
	// Only performers have a dataloader in this version, matching how the elo
	// resolver handles the same problem: the schema accepts five target types and
	// this phase wires one. Null rather than an error, so a client renders an
	// unresolved link instead of failing the whole board over one suggestion.
	if c.EntityType == identification.TargetPerformer {
		performer, err := dataloader.For(ctx).PerformerByID.Load(c.EntityID)
		if err != nil {
			return nil, err
		}
		out.Entity = performer
	}
	if c.SuggestedBy != nil {
		user, err := dataloader.For(ctx).UserByID.Load(*c.SuggestedBy)
		if err != nil {
			return nil, err
		}
		out.SuggestedBy = user
	}
	voted, err := s.HasVoted(ctx, c.ID, viewer)
	if err != nil {
		return nil, err
	}
	out.VotedByMe = voted
	return out, nil
}

// withCandidates attaches candidates to a query, for a single-query read.
//
// A list query does NOT do this: fifty queries each with candidates is fifty
// extra reads for a queue view that renders descriptions, and the field is
// resolved per-query by the client asking for it.
func withCandidates(ctx context.Context, s *identification.Service, m *models.IdentificationQuery, viewer uuid.UUID) error {
	if m == nil {
		return nil
	}
	list, err := s.ListCandidates(ctx, m.ID)
	if err != nil {
		return err
	}
	m.Candidates = make([]models.IdentificationCandidate, 0, len(list))
	for _, c := range list {
		converted, err := candidateToModel(ctx, s, c, viewer)
		if err != nil {
			return err
		}
		if converted != nil {
			m.Candidates = append(m.Candidates, *converted)
		}
	}
	return nil
}

// orDefaultInt returns a value or a fallback, for an optional bounded limit.
//
// A nil limit is the client not saying; a limit of 0 or 10000 is the client
// saying something the service has a bound for. Both land on the service's own
// bound, which is the service's decision rather than this layer's.
func orDefaultInt(limit *int, fallback int) int {
	if limit == nil {
		return fallback
	}
	return *limit
}

// ListOpenIdentificationQueries returns the board's queue.
func (r *queryResolver) ListOpenIdentificationQueries(ctx context.Context, limit *int) ([]models.IdentificationQuery, error) {
	list, err := r.services.Identification().ListOpen(ctx, orDefaultInt(limit, 50))
	if err != nil {
		return nil, err
	}
	return toModelQueries(list), nil
}

// IdentificationQuery reads one query, with its candidates.
func (r *queryResolver) IdentificationQuery(ctx context.Context, id uuid.UUID) (*models.IdentificationQuery, error) {
	s := r.services.Identification()
	q, err := s.Get(ctx, id)
	if err != nil {
		if errors.Is(err, identification.ErrQueryNotFound) {
			// null, not an error: a deleted or never-existing query is a normal
			// outcome of following a stale link, and every client already renders
			// an empty state for a null.
			return nil, nil
		}
		return nil, err
	}
	out, err := queryToModelWithResolution(ctx, s, q)
	if err != nil {
		return nil, err
	}
	if err := withCandidates(ctx, s, out, auth.GetCurrentUser(ctx).ID); err != nil {
		return nil, err
	}
	return out, nil
}

// ResolvedIdentificationQueries returns solved queries about one entity.
func (r *queryResolver) ResolvedIdentificationQueries(ctx context.Context, entityType models.IdentificationTargetType, entityID uuid.UUID, limit *int) ([]models.IdentificationQuery, error) {
	list, err := r.services.Identification().ResolvedFor(ctx,
		identificationTargetType(entityType), entityID, orDefaultInt(limit, 20))
	if err != nil {
		return nil, err
	}
	return toModelQueries(list), nil
}

// MyIdentificationDetectiveScore returns the viewer's own Detective score.
func (r *queryResolver) MyIdentificationDetectiveScore(ctx context.Context) (*models.IdentificationDetective, error) {
	user := auth.GetCurrentUser(ctx)
	score, err := r.services.Identification().DetectiveScore(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	current, err := dataloader.For(ctx).UserByID.Load(user.ID)
	if err != nil {
		return nil, err
	}
	return &models.IdentificationDetective{
		User:  current,
		Score: score,
	}, nil
}

// PostIdentificationQuery posts a question.
func (r *mutationResolver) PostIdentificationQuery(ctx context.Context, input models.IdentificationPostInput) (*models.IdentificationQuery, error) {
	user := auth.GetCurrentUser(ctx)

	q, err := r.services.Identification().Post(ctx, identification.PostQuery{
		TargetType:  identificationTargetType(input.TargetType),
		TargetID:    input.TargetID,
		Description: input.Description,
		SnapshotID:  input.SnapshotID,
		CreatedBy:   uuid.NullUUID{UUID: user.ID, Valid: true},
	})
	if err != nil {
		return nil, err
	}
	return toModelQuery(q), nil
}

// SuggestIdentificationCandidate proposes an answer.
func (r *mutationResolver) SuggestIdentificationCandidate(ctx context.Context, input models.IdentificationSuggestInput) (*models.IdentificationCandidate, error) {
	user := auth.GetCurrentUser(ctx)
	s := r.services.Identification()

	c, err := s.Suggest(ctx, input.QueryID,
		identificationTargetType(input.EntityType), input.EntityID,
		uuid.NullUUID{UUID: user.ID, Valid: true})
	if err != nil {
		return nil, err
	}
	return candidateToModel(ctx, s, c, user.ID)
}

// VoteIdentificationCandidate records a vote.
func (r *mutationResolver) VoteIdentificationCandidate(ctx context.Context, candidateID uuid.UUID) (*models.IdentificationCandidate, error) {
	user := auth.GetCurrentUser(ctx)
	if err := r.services.Identification().Vote(ctx, candidateID, user.ID); err != nil {
		return nil, err
	}
	return r.reloadCandidate(ctx, candidateID, user.ID)
}

// UnvoteIdentificationCandidate withdraws a vote.
func (r *mutationResolver) UnvoteIdentificationCandidate(ctx context.Context, candidateID uuid.UUID) (*models.IdentificationCandidate, error) {
	user := auth.GetCurrentUser(ctx)
	if err := r.services.Identification().Unvote(ctx, candidateID, user.ID); err != nil {
		return nil, err
	}
	return r.reloadCandidate(ctx, candidateID, user.ID)
}

// reloadCandidate re-reads a candidate after a vote change.
//
// The vote and the returned tally are separate operations, and returning the
// candidate the caller already held would report a tally nobody has recounted.
// Re-reading is what makes the mutation's return value mean "this is the state
// now" rather than "this call succeeded", and it is why votedByMe is correct in
// the response: it is read in the same pass as the tally.
//
// A candidate deleted between the vote and the re-read is not a failure of the
// vote, so it returns null rather than an error.
func (r *mutationResolver) reloadCandidate(ctx context.Context, candidateID, userID uuid.UUID) (*models.IdentificationCandidate, error) {
	s := r.services.Identification()
	c, err := s.GetCandidate(ctx, candidateID)
	if err != nil {
		if errors.Is(err, identification.ErrCandidateNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return candidateToModel(ctx, s, c, userID)
}

// ResolveIdentificationQuery records what a query turned out to be.
func (r *mutationResolver) ResolveIdentificationQuery(ctx context.Context, input models.IdentificationResolveInput) (*models.IdentificationQuery, error) {
	user := auth.GetCurrentUser(ctx)
	s := r.services.Identification()

	// The trust event is recorded in the SAME transaction as the resolution.
	//
	// Passed as a callback rather than called after Resolve returns, because a
	// resolution that earned trust and then failed to persist would let a user
	// farm the "Detective" level by solving queries that then vanished. The
	// callback is what makes the two one fact.
	q, err := s.Resolve(ctx, input.QueryID,
		identificationTargetType(input.ResolvedType), input.ResolvedID, user.ID,
		func(ctx context.Context) error {
			// The delta is +1, not left to a default: RecordEvent refuses a zero
			// delta rather than inserting a row that moves no counter, and an
			// omitted Delta would be exactly that.
			_, err := r.services.Trust().RecordEvent(ctx, trust.Event{
				UserID:     user.ID,
				Kind:       trust.KindIdentificationSolved,
				Delta:      1,
				EntityType: string(identificationTargetType(input.ResolvedType)),
				EntityID:   &input.ResolvedID,
			})
			return err
		})
	if err != nil {
		return nil, err
	}
	return queryToModelWithResolution(ctx, s, q)
}

// AbandonIdentificationQuery marks a query dead.
func (r *mutationResolver) AbandonIdentificationQuery(ctx context.Context, id uuid.UUID) (*models.IdentificationQuery, error) {
	s := r.services.Identification()
	if err := s.Abandon(ctx, id); err != nil {
		return nil, err
	}
	q, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return toModelQuery(q), nil
}
