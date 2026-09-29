package completion

import (
	"context"
	"errors"
	"fmt"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/stashapp/stash-box/internal/queries"
)

// Service reads completion scores.
//
// Deliberately thin: the arithmetic is in Score() in score.go and the facts are in
// completion.sql, so this file's whole job is to join the two and to convert
// sqlc's row types into the map the formula takes.
//
// # Why every boolean is read through asBool
//
// The COALESCE in the SQL is what makes the VALUES unambiguous -- a NULL never
// reaches the arithmetic. But sqlc still types a `COALESCE(expr, FALSE)` column as
// `interface{}` rather than `bool`, because it cannot prove the expression is a
// boolean. So the row arrives with a dynamic type and the conversion happens HERE.
//
// That placement is the point. A three-valued-logic bug in this query would be
// invisible if the conversion were in the SQL, because the SQL is what produced
// the NULL, and a query cannot catch its own three-valued logic. Reading it
// through one named function means the NULL case is a single decision, made once,
// in a place a test can reach.
type Service struct {
	queries *queries.Queries
}

// NewService creates a completion service.
func NewService(queries *queries.Queries) *Service {
	return &Service{queries: queries}
}

// ErrNotFound is returned for a missing or soft-deleted entity.
var ErrNotFound = errors.New("entity not found")

// asBool reads a sqlc boolean that may arrive as interface{}.
//
// A nil or absent value is FALSE, deliberately. The alternative -- treating an
// unknown as present -- would make an entity look more complete than it is, and
// the cost of each false "present" is a curator sent to fix something already
// fixed. An entity nobody can score is an entity with gaps.
//
// Anything that is not a bool and not nil is also false rather than an error: this
// runs on a read path for a value a client renders as a progress bar, and a
// scoring error must not become a 500. A malformed column shows up as a
// conservative score, which is the safe direction to be wrong in.
func asBool(v interface{}) bool {
	switch typed := v.(type) {
	case bool:
		return typed
	case *bool:
		if typed == nil {
			return false
		}
		return *typed
	default:
		return false
	}
}

// Performer scores a performer.
func (s *Service) Performer(ctx context.Context, id uuid.UUID) (Result, error) {
	row, err := s.queries.PerformerCompletionInputs(ctx, id)
	if err != nil {
		return Result{}, notFoundOr(err)
	}
	return Score(EntityPerformer, map[Field]bool{
		FieldName:         asBool(row.HasName),
		FieldAliases:      row.HasAliases,
		FieldBirthdate:    asBool(row.HasBirthdate),
		FieldGender:       asBool(row.HasGender),
		FieldEthnicity:    asBool(row.HasEthnicity),
		FieldCountry:      asBool(row.HasCountry),
		FieldEyeColor:     asBool(row.HasEyeColor),
		FieldHairColor:    asBool(row.HasHairColor),
		FieldHeight:       asBool(row.HasHeight),
		FieldMeasurements: asBool(row.HasMeasurements),
		FieldCareerDates:  asBool(row.HasCareerDates),
		FieldURLs:         row.HasUrls,
		FieldImage:        row.HasImage,
	})
}

// Scene scores a scene.
func (s *Service) Scene(ctx context.Context, id uuid.UUID) (Result, error) {
	row, err := s.queries.SceneCompletionInputs(ctx, id)
	if err != nil {
		return Result{}, notFoundOr(err)
	}
	return Score(EntityScene, map[Field]bool{
		FieldName:            asBool(row.HasName),
		FieldDuration:        asBool(row.HasDuration),
		FieldStudio:          asBool(row.HasStudio),
		FieldPerformers:      row.HasPerformers,
		FieldDate:            asBool(row.HasDate),
		FieldURLs:            row.HasUrls,
		FieldTags:            row.HasTags,
		FieldImage:           row.HasImage,
		FieldDetails:         asBool(row.HasDetails),
		FieldSnapshotCoverag: row.HasSnapshotCoverage,
	})
}

// Studio scores a studio.
func (s *Service) Studio(ctx context.Context, id uuid.UUID) (Result, error) {
	row, err := s.queries.StudioCompletionInputs(ctx, id)
	if err != nil {
		return Result{}, notFoundOr(err)
	}
	return Score(EntityStudio, map[Field]bool{
		FieldName:         asBool(row.HasName),
		FieldURLs:         row.HasUrls,
		FieldImage:        row.HasImage,
		FieldParentStudio: asBool(row.HasParent),
	})
}

// Site scores a site.
func (s *Service) Site(ctx context.Context, id uuid.UUID) (Result, error) {
	row, err := s.queries.SiteCompletionInputs(ctx, id)
	if err != nil {
		return Result{}, notFoundOr(err)
	}
	return Score(EntitySite, map[Field]bool{
		FieldName:    asBool(row.HasName),
		FieldURLs:    asBool(row.HasUrls),
		FieldDetails: asBool(row.HasDetails),
		FieldRegex:   asBool(row.HasRegex),
	})
}

// Tag scores a tag.
func (s *Service) Tag(ctx context.Context, id uuid.UUID) (Result, error) {
	row, err := s.queries.TagCompletionInputs(ctx, id)
	if err != nil {
		return Result{}, notFoundOr(err)
	}
	return Score(EntityTag, map[Field]bool{
		FieldName:    asBool(row.HasName),
		FieldDetails: asBool(row.HasDetails),
	})
}

// notFoundOr maps a missing row to ErrNotFound.
//
// A missing row and a soft-deleted row are the same answer here: the entity is
// not scoreable, and telling a caller which of the two it was would leak the
// existence of deleted records.
func notFoundOr(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: %s", ErrNotFound, err)
	}
	return err
}

// COVERAGE NOTE, written here because the unit tests in this package cannot reach
// the code above and a green suite would otherwise imply they do.
//
// `asBool` is fully mutation-checked by `asbool_test.go`. The five
// `Performer`/`Scene`/`Studio`/`Site`/`Tag` methods are NOT reachable from any test
// in this file: they take a context and a uuid and read the database, and this
// package has no database. Five mutations demonstrated that gap rather than
// asserting it -- every one of them SURVIVED this package's tests:
//
//   a scene's snapshot coverage read from has_image   -> SURVIVES
//   a scene's duration and studio swapped             -> SURVIVES
//   a performer's birthdate read from their country    -> SURVIVES
//   a performer's aliases read from their urls         -> SURVIVES
//   a scene's performers read from its tags            -> SURVIVES
//   a studio's parent read from its image              -> SURVIVES
//
// All six are real bugs, and none is visible from a pure unit test, because the
// FORMULA is correct and only the WIRING is wrong. The formula tests pass for
// exactly the wrong reason here: they hand the scorer a map of booleans it
// believes, and a scorer that maps the wrong column to the wrong field is
// perfectly self-consistent when handed a map.
//
// Three of the six needed a second pass even in the integration test, and all three
// were the same mistake: a column never set to TRUE cannot be distinguished from
// any other column never set to TRUE. The first version of the fixture left
// aliases, tags and studio-parents all false, so reading one from another changed
// nothing observable. The fix is a fixture that sets every column to a
// DISTINGUISHABLE value -- some present, some absent, never both-or-neither.
//
// So the mapping from row column to field is proved in
// `internal/api/completion_integration_test.go`, and all six mutations die there.
// This note is here so the gap is not mistaken for coverage when someone reads the
// unit mutation results in isolation.
