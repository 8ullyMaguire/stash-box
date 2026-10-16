package api

import (
	"context"

	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/service/lint"
)

// LintQuestBoard returns the data-lint quest board (SPEC §7.24.4): every enabled detector this build
// implements, with the records it found.
//
// THE ERROR PATH IS THE INTERESTING ONE. The service validates the registry before it answers, so a
// response in this shape means every enabled row is backed by an implementation here -- and a registry
// that names one this build does not have comes back as an error naming the slug, not as a board that
// is quietly one detector short. That distinction was worth a live walk to find: the same registry that
// made `-validate` refuse produced a board with five detectors on it and nothing anywhere to say that a
// sixth had been skipped, which reads exactly like a detector that found nothing.
//
// The limit is passed through, not decided here: the board's caller knows how much it can show, and the
// service clamps what it is given so a client cannot ask for an unbounded scan.
func (r *queryResolver) LintQuestBoard(ctx context.Context, limit *int) ([]models.LintQuestReport, error) {
	requested := 0
	if limit != nil {
		requested = *limit
	}

	reports, err := r.services.Lint().Board(ctx, int32(requested))
	if err != nil {
		return nil, err
	}

	out := make([]models.LintQuestReport, 0, len(reports))
	for _, report := range reports {
		out = append(out, lintReportModel(report))
	}
	return out, nil
}

// LintQuestDefinitions returns the registry as it stands, including rows the board does not run.
//
// This is the operator's view and it is deliberately NOT the board's: a disabled row is how a noisy
// detector is retired without deleting the record that it once ran, and a row for a detector some other
// build ships is what a rollback leaves behind. Both are invisible on the board -- correctly, since
// neither is work -- and both need to be legible somewhere, because "why is this instance not running
// the detector I switched on" is otherwise unanswerable from outside the database.
//
// `implemented` is computed from the compiled registry rather than read from the row, which is the only
// way it can be true: the row cannot know what this binary contains.
func (r *queryResolver) LintQuestDefinitions(ctx context.Context) ([]models.LintQuestDefinition, error) {
	defs, err := r.services.Lint().Definitions(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]models.LintQuestDefinition, 0, len(defs))
	for _, def := range defs {
		out = append(out, models.LintQuestDefinition{
			Slug:         def.Slug,
			Description:  def.Description,
			Enabled:      def.Enabled,
			BountyPoints: int(def.BountyPoints),
			IsBuiltin:    def.IsBuiltin,
			Implemented:  lint.Implements(def.Slug),
		})
	}
	return out, nil
}

// lintReportModel maps one detector's answer onto the schema's shape.
//
// The candidate slice is built with make(..., 0, n) rather than appended to a nil slice: `candidates` is
// a non-null list in the schema, and a nil slice marshals as JSON null, which a strict client reads as a
// missing field rather than as "nothing found". An empty board row has to say zero, not nothing.
func lintReportModel(report lint.Report) models.LintQuestReport {
	out := models.LintQuestReport{
		Slug:         report.Slug,
		Description:  report.Description,
		EntityType:   report.EntityType,
		BountyPoints: report.BountyPoints,
		Count:        report.Count,
		Candidates:   make([]models.LintCandidate, 0, len(report.Candidates)),
	}
	if report.LimitNotice != "" {
		notice := report.LimitNotice
		out.LimitNotice = &notice
	}
	for _, candidate := range report.Candidates {
		out.Candidates = append(out.Candidates, models.LintCandidate{
			EntityID:   candidate.EntityID,
			EntityName: candidate.EntityName,
			Detail:     candidate.Detail,
		})
	}
	return out
}
