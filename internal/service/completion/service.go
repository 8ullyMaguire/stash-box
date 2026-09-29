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
		FieldName:             asBool(row.HasName),
		FieldDuration:         asBool(row.HasDuration),
		FieldStudio:           asBool(row.HasStudio),
		FieldPerformers:       row.HasPerformers,
		FieldDate:             asBool(row.HasDate),
		FieldURLs:             row.HasUrls,
		FieldTags:             row.HasTags,
		FieldImage:            row.HasImage,
		FieldDetails:          asBool(row.HasDetails),
		FieldSnapshotCoverage: row.HasSnapshotCoverage,
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

// CountIncomplete counts entities of a type with at least this much missing
// weight.
//
// The threshold is MISSING weight rather than a score, and that is the whole
// point of the query's shape: the weights live here, in Go, and the query
// duplicates them as SQL constants. Expressing the question as missing weight lets
// the comparison happen in one place -- this function -- so there is one number
// that decides, even though two places hold the arithmetic.
//
// `below` is a score in the caller's terms (0-100) and is INVERTED here, because
// a caller asking "how many are below 50" is thinking in scores and a query
// counting missing weight is thinking in the other direction. The inversion is
// here rather than at each call site so a caller cannot get the sense wrong: an
// inverted threshold counts the entities you did not ask for, and the number looks
// entirely plausible.
//
// Out-of-range thresholds are clamped rather than refused. `below = 0` means "every
// entity" and `below = 100` means "none", and both are legitimate -- "how complete
// is this archive overall" is the question an operator most wants answered. A
// refusal would make the most useful question the one you cannot ask.
func (s *Service) CountIncomplete(ctx context.Context, entityType EntityType, below int) (int, error) {
	total, err := TotalWeight(entityType)
	if err != nil {
		return 0, err
	}
	if below < 0 {
		below = 0
	}
	if below > 100 {
		below = 100
	}
	// An entity is incomplete iff score < below, i.e. missing > total*(100-below)/100.
	// The query tests `missing >= minMissing`, so minMissing must be the SMALLEST
	// missing weight that is strictly above the boundary: floor(boundary) + 1.
	//
	// The +1 is the whole subtlety and my first version got it wrong. With
	// `(total*(100-below)+99)/100` a threshold of 50 gives minMissing 45, and an
	// entity missing exactly 45 of 90 scores exactly 50 -- which is not below 50,
	// and would be counted. Every entity sitting exactly on the boundary was
	// reported as needing work.
	//
	// The two degenerate cases are the ones that prove it, because they are the
	// ones a caller actually asks:
	//
	//   below = 0   -> boundary = total, minMissing = total+1. Nothing can be
	//                  missing that much, so the count is 0. Correct: no entity
	//                  scores below 0.
	//   below = 100 -> boundary = 0, minMissing = 1. An entity missing at least
	//                  one weight-unit counts, which is every incomplete one.
	//                  Correct: only a complete entity scores 100.
	//
	// I had these two the wrong way round in the test, which is how the bug
	// surfaced: "below 0 counts everything" is false, and no amount of staring at
	// the formula would have shown it as clearly as writing the two extremes down.
	minMissing := minMissingWeight(total, below)

	count, err := s.queries.CountEntitiesWithCompletionBelow(ctx,
		queries.CountEntitiesWithCompletionBelowParams{
			EntityType: string(entityType),
			MinMissing: minMissing,
		})
	if err != nil {
		return 0, err
	}
	return int(count), nil
}

// ListIncomplete returns incomplete entity ids for a generated quest, one page at
// a time.
//
// Paged by `afterID` rather than by OFFSET, because OFFSET re-scans every skipped
// row and a quest queue is read repeatedly -- the fifth page of a quest list would
// be five times the work of the first. Keyset pagination also does not skip or
// repeat rows when an entity is fixed mid-walk, which OFFSET does: fixing three
// performers while paging shifts everything after them by three, and the caller
// silently sees two entities twice.
//
// The returned ids carry no field information: a quest asks "these performers are
// missing something" and the caller scores each one to find out what. Returning the
// missing fields here would mean a second read of every entity, and the fields are
// available from the per-entity score the client already fetches.
func (s *Service) ListIncomplete(ctx context.Context, entityType EntityType, minMissing int, afterID *uuid.UUID, pageSize int) ([]uuid.UUID, error) {
	if pageSize <= 0 || pageSize > 200 {
		pageSize = 50
	}
	if _, err := TotalWeight(entityType); err != nil {
		return nil, err
	}
	rows, err := s.queries.ListIncompleteEntities(ctx,
		queries.ListIncompleteEntitiesParams{
			EntityType: string(entityType),
			MinMissing: minMissing,
			AfterID:    zeroUUID(afterID),
			PageSize:   pageSize,
		})
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// minMissingWeight is the smallest missing weight that counts as incomplete at a
// given threshold.
//
// EXTRACTED rather than inlined so the tests measure the real function. An earlier
// version had the arithmetic inline in CountIncomplete and the test re-derived it
// from the same formula, which is a tautology: the test and the code change
// together or not at all, and the off-by-one this extracted function now documents
// would have been invisible to a test that agreed with a buggy implementation.
func minMissingWeight(total, below int) int {
	return total*(100-below)/100 + 1
}

// zeroUUID is the "no cursor" value for a keyset page.
//
// The zero uuid rather than a nullable parameter, because `id > NULL` is NULL and
// the comparison would match nothing -- a keyset query with a null cursor returns
// an empty page, which looks exactly like "there is nothing left to do".
func zeroUUID(id *uuid.UUID) uuid.UUID {
	if id == nil {
		return uuid.Nil
	}
	return *id
}
