package verification

import (
	"context"
	"fmt"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/queries"
)

// The entity types an assertion may name (SPEC §7.24.1, migration 96's entity_type).
//
// THESE ARE UPPERCASE BECAUSE THEY MATCH `edits.target_type`, NOT BECAUSE IT IS PRETTIER.
// This schema spells the same idea two ways: `edits.target_type` holds 'SCENE' (varchar(10),
// widened to 20 by migration 118), while `content_sources.entity_type` holds 'scene'. Migration
// 96 put NO CHECK on field_verification_states.entity_type, so a value in the wrong case is
// accepted at write time and then matches nothing at read time -- an assertion that silently
// suppresses no gap, in a feature whose whole purpose is to suppress a gap.
//
// The edit-side spelling wins because an assertion RIDES an edit (7.24.1: it is an edit, not a
// moderator field), so the entity it names is the entity an edit targets. Both halves of the
// feature -- the write in Assert and the read in SuppressingFields -- take the value from these
// constants, which is the only reason the choice is safe to make at all.
const (
	EntityScene       = "SCENE"
	EntityStudio      = "STUDIO"
	EntityPerformer   = "PERFORMER"
	EntityTag         = "TAG"
	EntityContentItem = "CONTENT_ITEM"
)

// Service owns SPEC §7.24.1's storage: the reason registry, the assertions, and the read the
// completion score uses to decide that a gap is answered.
type Service struct {
	queries *queries.Queries
}

func New(q *queries.Queries) *Service {
	return &Service{queries: q}
}

// nullUUID converts the optional edit id. A moderator applying an assertion by hand has no edit
// row, so absence is a normal state rather than an error -- but it is also not something to
// collapse into the zero UUID, which would point at nothing while claiming to point at something.
func nullUUID(id *uuid.UUID) uuid.NullUUID {
	if id == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *id, Valid: true}
}

// Reasons returns the registry, for the edit form's dropdown and for validating an incoming code.
func (s *Service) Reasons(ctx context.Context) ([]queries.FieldVerificationReason, error) {
	return s.queries.GetFieldVerificationReasons(ctx)
}

// Assert records one or more verified-unknown claims about an entity.
//
// ONE READ OF THE REGISTRY, NOT ONE PER ASSERTION. An edit may carry several claims, and the
// validation below is the reason the codes are checked here at all: the FK in 96 would refuse an
// unknown code too, with "violates foreign key constraint", which names the constraint rather
// than the mistake. A curator who typed `not_publically_knowable` should be told that.
//
// assertedBy is the edit's author. It is NOT NULL in the schema and this method does not default
// it: an assertion whose author is unknown cannot be weighed against trust, which is the entire
// reason 7.24.1 routes these through the edit machinery.
//
// editID is nullable because a moderator may apply an assertion directly, and 7.24.1 requires the
// edit path to be the norm rather than the only path.
func (s *Service) Assert(
	ctx context.Context,
	entityType string,
	entityID uuid.UUID,
	assertions []models.VerifiedUnknownInput,
	assertedBy uuid.UUID,
	editID *uuid.UUID,
	assertedAt time.Time,
) error {
	if len(assertions) == 0 {
		return nil
	}

	known, err := s.Reasons(ctx)
	if err != nil {
		return fmt.Errorf("reading the verified-unknown reason registry: %w", err)
	}
	byCode := make(map[string]struct{}, len(known))
	for _, r := range known {
		byCode[r.Code] = struct{}{}
	}

	for _, a := range assertions {
		if a.Field == "" {
			return fmt.Errorf("a verified-unknown assertion names no field")
		}
		if _, ok := byCode[a.ReasonCode]; !ok {
			return fmt.Errorf(
				"unknown verified-unknown reason code %q; the registry holds %d code(s) and a reason is never free text (SPEC 7.24.1)",
				a.ReasonCode, len(known))
		}
		// One row per (entity, field), so a re-assertion REPLACES. Two rows would make the
		// suppression count double-count and let completion exceed what it should.
		if _, err := s.queries.CreateFieldVerificationState(ctx, queries.CreateFieldVerificationStateParams{
			EntityType:  entityType,
			EntityID:    entityID,
			Field:       a.Field,
			ReasonCode:  a.ReasonCode,
			AssertedBy:  assertedBy,
			AssertedAt:  pgtype.Timestamptz{Time: assertedAt, Valid: true},
			CitationUrl: a.CitationURL,
			EditID:      nullUUID(editID),
		}); err != nil {
			return fmt.Errorf("recording a verified-unknown for %s.%s: %w", entityType, a.Field, err)
		}
	}
	return nil
}

// SuppressingFields returns the fields of one entity whose reason CLOSES the completion gap.
//
// `not_yet_looked` is deliberately absent from that answer, and that is the feature rather than a
// detail: it records a curator's belief while leaving the gap actionable, which is what stops an
// unanswerable question from being closed by assertion. The caller compares against a set of field
// names, so a reason that does not suppress is indistinguishable from no assertion at all -- which
// is exactly right, because it changes no score.
func (s *Service) SuppressingFields(ctx context.Context, entityType string, entityID uuid.UUID) ([]string, error) {
	return s.queries.GetSuppressingFieldVerificationStates(ctx, queries.GetSuppressingFieldVerificationStatesParams{
		EntityType: entityType,
		EntityID:   entityID,
	})
}
