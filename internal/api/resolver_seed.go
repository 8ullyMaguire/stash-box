package api

import (
	"context"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/stashapp/stash-box/internal/auth"
	"github.com/stashapp/stash-box/internal/dataloader"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/queries"
)

// The seed-cohort and campaign surface (SPEC §11, plan phase 11 slice 11d).
//
// THE MODELS ARE BUILT HERE RATHER THAN READ OFF A ROW, because a cohort's size and members and a
// campaign's cohort are QUESTIONS, not columns: the roster is read at the moment it is asked for, since
// membership is never cached into a payment (`seed.Service` says so at length). Each list item costs a
// handful of small reads, and that is a deliberate trade for a surface whose lists are curated handfuls
// -- an operator's cohorts and campaigns -- rather than a feed.
//
// EVERY FIELD IS ADMIN by directive in the schema, so there is no second check here: two checks for one
// rule are two places for it to say different things.
type seedResolver struct{ *Resolver }

// seedCohort turns a row into the model, filling the two fields that are questions.
func (r *seedResolver) seedCohort(ctx context.Context, row *queries.SeedCohort) (*models.SeedCohort, error) {
	members, err := r.services.Seed().CohortMembers(ctx, row.ID)
	if err != nil {
		return nil, err
	}

	count, err := r.services.Seed().MemberCount(ctx, row.ID)
	if err != nil {
		return nil, err
	}

	model := &models.SeedCohort{
		ID:          row.ID,
		Name:        row.Name,
		Description: row.Description,
		CreatedAt:   row.CreatedAt.Time,
		MemberCount: count,
		Members:     make([]models.SeedCohortMember, 0, len(members)),
	}
	for _, m := range members {
		model.Members = append(model.Members, models.SeedCohortMember{
			UserID:  m.UserID,
			Name:    m.Name,
			AddedAt: m.AddedAt.Time,
		})
	}
	return model, nil
}

func (r *seedResolver) seedCampaign(ctx context.Context, row *queries.SeedCampaign) (*models.SeedCampaign, error) {
	cohortRow, err := r.services.Seed().Cohort(ctx, row.CohortID)
	if err != nil {
		return nil, err
	}
	cohort, err := r.seedCohort(ctx, cohortRow)
	if err != nil {
		return nil, err
	}

	model := &models.SeedCampaign{
		ID:        row.ID,
		Cohort:    cohort,
		Name:      row.Name,
		Points:    int(row.Points),
		CreatedAt: row.CreatedAt.Time,
		LastRunAt: seedNullableTime(row.LastRunAt),
	}
	// Who ran it, when somebody did. Absent stays absent: "nobody has run this" and "run by an account
	// that no longer exists" are answered by `last_run_at` and the load, in that order.
	if row.LastRunBy.Valid {
		user, err := dataloader.For(ctx).UserByID.Load(row.LastRunBy.UUID)
		if err != nil {
			return nil, err
		}
		model.LastRunBy = user
	}
	return model, nil
}

// seedNullableTime is the "never ran" distinction, kept as a pointer rather than a zero time: a
// campaign's `last_run_at` is NULL until somebody runs it, and time.Time's zero value is a real instant
// that would read as 1 January year 1.
func seedNullableTime(ts pgtype.Timestamptz) *time.Time {
	if !ts.Valid {
		return nil
	}
	return &ts.Time
}

func (r *seedResolver) seedCampaignsFrom(ctx context.Context, rows []queries.SeedCampaign) ([]models.SeedCampaign, error) {
	out := make([]models.SeedCampaign, 0, len(rows))
	for i := range rows {
		model, err := r.seedCampaign(ctx, &rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, *model)
	}
	return out, nil
}

func (r *queryResolver) SeedCohorts(ctx context.Context) ([]models.SeedCohort, error) {
	rows, err := r.services.Seed().Cohorts(ctx)
	if err != nil {
		return nil, err
	}

	sr := &seedResolver{r.Resolver}
	out := make([]models.SeedCohort, 0, len(rows))
	for i := range rows {
		model, err := sr.seedCohort(ctx, &rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, *model)
	}
	return out, nil
}

func (r *queryResolver) SeedCohort(ctx context.Context, id uuid.UUID) (*models.SeedCohort, error) {
	row, err := r.services.Seed().Cohort(ctx, id)
	if err != nil {
		return nil, err
	}
	return (&seedResolver{r.Resolver}).seedCohort(ctx, row)
}

func (r *queryResolver) SeedCampaigns(ctx context.Context) ([]models.SeedCampaign, error) {
	rows, err := r.services.Seed().Campaigns(ctx)
	if err != nil {
		return nil, err
	}
	return (&seedResolver{r.Resolver}).seedCampaignsFrom(ctx, rows)
}

func (r *queryResolver) SeedCampaign(ctx context.Context, id uuid.UUID) (*models.SeedCampaign, error) {
	row, err := r.services.Seed().Campaign(ctx, id)
	if err != nil {
		return nil, err
	}
	return (&seedResolver{r.Resolver}).seedCampaign(ctx, row)
}

func (r *mutationResolver) CreateSeedCohort(ctx context.Context, input models.SeedCohortCreateInput) (*models.SeedCohort, error) {
	row, err := r.services.Seed().CreateCohort(ctx, input.Name, input.Description)
	if err != nil {
		return nil, err
	}
	return (&seedResolver{r.Resolver}).seedCohort(ctx, row)
}

// AddSeedCohortMembers reports how many accounts were NEWLY added, which is not the length of the list.
func (r *mutationResolver) AddSeedCohortMembers(ctx context.Context, input models.SeedCohortMembersInput) (int, error) {
	return r.services.Seed().AddMembers(ctx, input.CohortID, input.UserIds)
}

func (r *mutationResolver) RemoveSeedCohortMember(ctx context.Context, cohortID uuid.UUID, userID uuid.UUID) (bool, error) {
	return r.services.Seed().RemoveMember(ctx, cohortID, userID)
}

func (r *mutationResolver) CreateSeedCampaign(ctx context.Context, input models.SeedCampaignCreateInput) (*models.SeedCampaign, error) {
	// The creator is taken from the authenticated caller rather than from the input: it is a fact about
	// who did this, and a caller-supplied one would be a claim.
	user := auth.GetCurrentUser(ctx)

	row, err := r.services.Seed().CreateCampaign(ctx, input.CohortID, input.Name, input.Points, user.ID)
	if err != nil {
		return nil, err
	}
	return (&seedResolver{r.Resolver}).seedCampaign(ctx, row)
}

func (r *mutationResolver) RunSeedCampaign(ctx context.Context, id uuid.UUID) (*models.SeedCampaignRunResult, error) {
	user := auth.GetCurrentUser(ctx)

	result, err := r.services.Seed().RunCampaign(ctx, id, user.ID)
	if err != nil {
		return nil, err
	}

	campaign, err := (&seedResolver{r.Resolver}).seedCampaign(ctx, result.Campaign)
	if err != nil {
		return nil, err
	}

	return &models.SeedCampaignRunResult{
		Campaign: campaign,
		Members:  result.Members,
		Paid:     result.Paid,
		Skipped:  result.Skipped,
	}, nil
}
