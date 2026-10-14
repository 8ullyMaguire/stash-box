// Package tagontology is the tag ontology: the implication graph (migration 111) and the
// versions over it (migration 112).
//
// An implication says a tag is effectively also another tag: `cowgirl_position` implies
// `woman_on_top`, so a reader filtering by `woman_on_top` must find the specific things.
// NOTHING here writes an implied tag onto `scene_tags`: the graph is the owning table and
// every read derives from it, because `scene_tags` has no source column and a materialised
// implication would be indistinguishable from a person's tag. See
// docs/spec/feature-tag-ontology.md section 2.
package tagontology

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/converter"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/queries"
)

// The reasons an implication change is refused BEFORE it becomes an edit. The database
// refuses the same things again when the edit is applied (the cycle trigger and the
// self-implication CHECK), and the database is the authority -- these exist so that the
// person submitting an edit is told while they can still change it, instead of finding out
// from a failed apply days later in a comment written by a bot.
var (
	ErrSelfImplication   = errors.New("a tag cannot imply itself")
	ErrImplicationCycle  = errors.New("that implication would create a cycle")
	ErrImpliedTagUnknown = errors.New("implied tag does not exist")
)

type Service struct {
	queries *queries.Queries
}

func New(q *queries.Queries) *Service {
	return &Service{queries: q}
}

// Expand answers, for each requested tag id, which tag ids satisfy it: the tag itself plus
// every tag that implies it, transitively.
//
// This is what makes the hierarchy useful to a reader. The result is a map rather than a
// flat set because "all of these" is a per-REQUESTED-tag question -- an item tagged only
// `cowgirl_position` satisfies both `woman_on_top` and `vaginal_sex` while carrying neither,
// and a flat set of ids cannot express that.
func (s *Service) Expand(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID][]uuid.UUID, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	rows, err := s.queries.ExpandTagIDsByImplications(ctx, ids)
	if err != nil {
		return nil, err
	}

	// A tag always satisfies itself, including one that does not exist: a filter naming an
	// unknown tag must match nothing, not fail.
	out := make(map[uuid.UUID][]uuid.UUID, len(ids))
	for _, id := range ids {
		out[id] = []uuid.UUID{id}
	}
	for _, row := range rows {
		if row.RootID == row.TagID {
			continue
		}
		out[row.RootID] = append(out[row.RootID], row.TagID)
	}

	return out, nil
}

// ValidateImplications reports the first reason an implication change must be refused, or
// nil. `added` and `removed` are what an edit asks for, and both are always relative to the
// tag being edited: an added id means "this tag implies that".
func (s *Service) ValidateImplications(ctx context.Context, tagID uuid.UUID, added, removed []uuid.UUID) error {
	if len(added) == 0 && len(removed) == 0 {
		return nil
	}

	// Every referenced tag has to exist, on both sides: removing a tag that is gone is a
	// no-op the caller meant as something, and adding one that never existed would be a dead
	// edge no later reader could explain.
	ids := make([]uuid.UUID, 0, len(added)+len(removed))
	ids = append(ids, added...)
	ids = append(ids, removed...)
	found, err := s.queries.FindTagsByIds(ctx, ids)
	if err != nil {
		return err
	}
	known := make(map[uuid.UUID]struct{}, len(found))
	for _, tag := range found {
		known[tag.ID] = struct{}{}
	}
	for _, id := range ids {
		if _, ok := known[id]; !ok {
			return fmt.Errorf("%w: %s", ErrImpliedTagUnknown, id)
		}
	}

	for _, implied := range added {
		// A tag implying itself is refused by the CHECK as well; this arm exists so the
		// message names the rule instead of coming back as a constraint violation.
		if implied == tagID {
			return fmt.Errorf("%w: %s", ErrSelfImplication, tagID)
		}

		// Adding A -> B closes a cycle exactly when B already reaches A. The tail is what
		// has to be walked: walking the head's ancestors answers a different question.
		reachable, err := s.impliedClosure(ctx, implied)
		if err != nil {
			return err
		}
		if _, cycle := reachable[tagID]; cycle {
			return fmt.Errorf("%w: %s already implies %s", ErrImplicationCycle, implied, tagID)
		}
	}

	return nil
}

// impliedClosure is every tag id reachable by following implications from `tagID`, including
// `tagID` itself.
func (s *Service) impliedClosure(ctx context.Context, tagID uuid.UUID) (map[uuid.UUID]struct{}, error) {
	rows, err := s.queries.FindTransitiveImplicationsByTagID(ctx, tagID)
	if err != nil {
		return nil, err
	}

	closure := make(map[uuid.UUID]struct{}, len(rows)+1)
	closure[tagID] = struct{}{}
	for _, row := range rows {
		closure[row.ID] = struct{}{}
	}

	return closure, nil
}

// Implies is the direct relation: what this tag implies.
func (s *Service) Implies(ctx context.Context, tagID uuid.UUID) ([]models.Tag, error) {
	rows, err := s.queries.FindImplicationsByTagID(ctx, tagID)
	if err != nil {
		return nil, err
	}

	return converter.TagsToModels(rows), nil
}

// ImpliedBy is the direct relation the other way: what implies this tag.
func (s *Service) ImpliedBy(ctx context.Context, tagID uuid.UUID) ([]models.Tag, error) {
	rows, err := s.queries.FindImpliedByTagID(ctx, tagID)
	if err != nil {
		return nil, err
	}

	return converter.TagsToModels(rows), nil
}

// ImpliesTransitive is everything below this tag, with how far below.
func (s *Service) ImpliesTransitive(ctx context.Context, tagID uuid.UUID) ([]models.TagAtDepth, error) {
	rows, err := s.queries.FindTransitiveImplicationsByTagID(ctx, tagID)
	if err != nil {
		return nil, err
	}

	out := make([]models.TagAtDepth, 0, len(rows))
	for _, row := range rows {
		out = append(out, models.TagAtDepth{
			Tag: converter.TagToModel(queries.Tag{
				ID:          row.ID,
				Name:        row.Name,
				Description: row.Description,
				CreatedAt:   row.CreatedAt,
				UpdatedAt:   row.UpdatedAt,
				Deleted:     row.Deleted,
				CategoryID:  row.CategoryID,
			}),
			Depth: int(row.Depth),
		})
	}

	return out, nil
}

// ImpliedByTransitive is every tag a reader could filter by to find this one.
func (s *Service) ImpliedByTransitive(ctx context.Context, tagID uuid.UUID) ([]models.TagAtDepth, error) {
	rows, err := s.queries.FindTransitiveImpliedByTagID(ctx, tagID)
	if err != nil {
		return nil, err
	}

	out := make([]models.TagAtDepth, 0, len(rows))
	for _, row := range rows {
		out = append(out, models.TagAtDepth{
			Tag: converter.TagToModel(queries.Tag{
				ID:          row.ID,
				Name:        row.Name,
				Description: row.Description,
				CreatedAt:   row.CreatedAt,
				UpdatedAt:   row.UpdatedAt,
				Deleted:     row.Deleted,
				CategoryID:  row.CategoryID,
			}),
			Depth: int(row.Depth),
		})
	}

	return out, nil
}

// List is the whole ontology, by name, ordered: what an operator reads and what a reviewer
// diffs two versions of.
func (s *Service) List(ctx context.Context) ([]models.TagImplication, error) {
	rows, err := s.queries.ListTagImplications(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]models.TagImplication, 0, len(rows))
	for _, row := range rows {
		out = append(out, models.TagImplication{
			TagID:        row.TagID,
			TagName:      row.TagName,
			ImpliedTagID: row.ImpliedTagID,
			ImpliedName:  row.ImpliedName,
			CreatedAt:    row.CreatedAt,
		})
	}

	return out, nil
}

// CurrentVersion is the version of the ontology in force.
func (s *Service) CurrentVersion(ctx context.Context) (int, error) {
	return s.queries.CurrentOntologyVersion(ctx)
}

// Versions is the history, newest first, without the snapshots.
func (s *Service) Versions(ctx context.Context, limit int) ([]models.OntologyVersion, error) {
	rows, err := s.queries.ListOntologyVersions(ctx, int32(limit))
	if err != nil {
		return nil, err
	}

	out := make([]models.OntologyVersion, 0, len(rows))
	for _, row := range rows {
		out = append(out, models.OntologyVersion{
			Version:   row.Version,
			Note:      row.Note,
			CreatedAt: row.CreatedAt,
		})
	}

	return out, nil
}

// Version is one version with its snapshot document.
func (s *Service) Version(ctx context.Context, version int) (*models.OntologyVersion, error) {
	row, err := s.queries.FindOntologyVersion(ctx, version)
	if err != nil {
		return nil, err
	}

	return &models.OntologyVersion{
		Version:   row.Version,
		Snapshot:  string(row.Snapshot),
		Note:      row.Note,
		CreatedAt: row.CreatedAt,
	}, nil
}

// Retarget moves every implication touching `sourceID` onto `targetID`, for a merge.
//
// The order is load-bearing and the deleting statements come first:
//
//  1. the pairs that would become self-implications once the two tags are one;
//  2. rows whose retargeted pair another row already holds (the surviving tag already
//     implies what the merged-away tag implied -- the same fact, not a conflict);
//  3. only then the two retargets, which cannot violate the primary key because 2 removed
//     everything they could collide with.
//
// The cycle trigger runs on every statement here and is the authority: a merge whose result
// would fold a cycle into the surviving tag is refused, and the caller's transaction rolls
// the whole apply back.
func (s *Service) Retarget(ctx context.Context, sourceID, targetID uuid.UUID) error {
	if sourceID == targetID {
		return nil
	}

	// The params structs are spelled out one by one because sqlc orders their fields by
	// first appearance in each statement, and the retargets name the target first (SET) while
	// the deletes name the source first (WHERE). They are not one convertible type, and a
	// conversion that compiled because someone reordered a statement would silently swap the
	// two ids -- which is exactly the merge that eats the wrong tag.
	if err := s.queries.DeleteSelfTagImplicationsForMerge(ctx, queries.DeleteSelfTagImplicationsForMergeParams{
		SourceID: sourceID,
		TargetID: targetID,
	}); err != nil {
		return err
	}
	if err := s.queries.DeleteDuplicateTagImplicationTails(ctx, queries.DeleteDuplicateTagImplicationTailsParams{
		SourceID: sourceID,
		TargetID: targetID,
	}); err != nil {
		return err
	}
	if err := s.queries.DeleteDuplicateTagImplicationHeads(ctx, queries.DeleteDuplicateTagImplicationHeadsParams{
		SourceID: sourceID,
		TargetID: targetID,
	}); err != nil {
		return err
	}
	if err := s.queries.RetargetTagImplicationTails(ctx, queries.RetargetTagImplicationTailsParams{
		TargetID: targetID,
		SourceID: sourceID,
	}); err != nil {
		return err
	}

	return s.queries.RetargetTagImplicationHeads(ctx, queries.RetargetTagImplicationHeadsParams{
		TargetID: targetID,
		SourceID: sourceID,
	})
}

// SetImplications applies an edit's implication change to a tag: the additions first, then
// the removals, both idempotent.
//
// Additions first on purpose. An edit that adds and removes the same pair is a contradiction,
// and doing the removal last means the file's stated intent ("this tag does not imply that")
// wins; the database would have refused the removal-then-add ordering's result only by luck.
func (s *Service) SetImplications(ctx context.Context, tagID uuid.UUID, added, removed []uuid.UUID) error {
	for _, implied := range added {
		if err := s.queries.CreateTagImplication(ctx, queries.CreateTagImplicationParams{
			TagID:        tagID,
			ImpliedTagID: implied,
		}); err != nil {
			return err
		}
	}

	for _, implied := range removed {
		if err := s.queries.DeleteTagImplication(ctx, queries.DeleteTagImplicationParams{
			TagID:        tagID,
			ImpliedTagID: implied,
		}); err != nil {
			return err
		}
	}

	return nil
}

// A compile-time assertion that the model's time field and the row's agree, so a change to
// either is a build error here rather than a zero timestamp in an API response.
var _ = func(v models.OntologyVersion) time.Time { return v.CreatedAt }
