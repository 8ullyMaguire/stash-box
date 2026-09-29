package api

import (
	"context"
	"errors"
	"fmt"

	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/auth"
	"github.com/stashapp/stash-box/internal/dataloader"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/service/elo"
)

func (r *queryResolver) EloMatchup(ctx context.Context, entityType models.EloEntityType) (*models.EloMatchup, error) {
	if entityType != models.EloEntityTypePerformer {
		// The schema accepts every entity type, but only performers have a
		// candidate query behind them. Returning null for an unbuilt type is a
		// deliberate contract: SPEC §9 ranks seven kinds of entity, one of which is
		// wired up in this phase, and a client that renders an empty state handles
		// it without a special case.
		return nil, nil
	}

	user := auth.GetCurrentUser(ctx)
	kind := elo.EntityType(entityType)

	offer, err := r.services.Elo().OfferFor(ctx, user.ID, kind, elo.NewShuffler())
	if err != nil {
		if errors.Is(err, elo.ErrNoMatchup) {
			// Nothing to compare yet. An empty state, not a failure: every client
			// already has a "nothing to vote on" view and none should have to
			// handle an error for it.
			return nil, nil
		}
		return nil, err
	}

	left, err := dataloader.For(ctx).PerformerByID.Load(offer.Matchup.Left.ID)
	if err != nil {
		return nil, err
	}
	right, err := dataloader.For(ctx).PerformerByID.Load(offer.Matchup.Right.ID)
	if err != nil {
		return nil, err
	}

	return &models.EloMatchup{
		EntityType:   entityType,
		Left:         left,
		Right:        right,
		TimesOffered: offer.TimesOffered,
	}, nil
}

func (r *mutationResolver) VoteElo(ctx context.Context, input models.EloVoteInput) (*models.EloVoteResult, error) {
	user := auth.GetCurrentUser(ctx)
	kind := elo.EntityType(input.Matchup.EntityType)
	if !kind.Valid() {
		return nil, fmt.Errorf("%w: %q", elo.ErrUnknownEntityType, input.Matchup.EntityType)
	}

	// The elapsed time is NOT taken from the client. A client-supplied elapsed
	// time is a client-supplied rating: passing 0 every time would hold a
	// deviation at its floor and freeze the rating, and passing a huge value would
	// reset the user's uncertainty on every vote. ElapsedDaysFor derives it from
	// stored last_rated_at using the service's own clock.
	elapsed, err := r.services.Elo().ElapsedDaysFor(ctx, []elo.Entity{
		{Type: kind, ID: input.Matchup.Left},
		{Type: kind, ID: input.Matchup.Right},
	})
	if err != nil {
		return nil, err
	}

	result, err := r.services.Elo().Vote(ctx, elo.VoteInput{
		UserID: user.ID,
		Matchup: elo.Matchup{
			EntityType: kind,
			Left:       elo.Entity{Type: kind, ID: input.Matchup.Left},
			Right:      elo.Entity{Type: kind, ID: input.Matchup.Right},
		},
		PickedSide:   elo.PickedSide(input.PickedSide),
		ElapsedDaysA: elapsed[0],
		ElapsedDaysB: elapsed[1],
	})
	if err != nil {
		return nil, err
	}

	rating := eloRatingModel(kind, result.EntityID, result)
	return &models.EloVoteResult{Rating: &rating}, nil
}

func (r *queryResolver) EloRating(ctx context.Context, entityType models.EloEntityType, id uuid.UUID) (*models.EloRating, error) {
	kind := elo.EntityType(entityType)
	if !kind.Valid() {
		return nil, fmt.Errorf("%w: %q", elo.ErrUnknownEntityType, entityType)
	}

	record, err := r.services.Elo().RatingFor(ctx, elo.Entity{Type: kind, ID: id})
	if err != nil {
		return nil, err
	}
	out := eloRatingModel(kind, id, record)
	return &out, nil
}

func (r *queryResolver) EloLeaderboard(ctx context.Context, entityType models.EloEntityType, limit *int) (*models.EloLeaderboard, error) {
	kind := elo.EntityType(entityType)
	if !kind.Valid() {
		return nil, fmt.Errorf("%w: %q", elo.ErrUnknownEntityType, entityType)
	}
	n := 100
	if limit != nil {
		n = int(*limit)
	}

	board, err := r.services.Elo().Leaderboard(ctx, kind, n)
	if err != nil {
		// The service is precise about "nothing has been rated for this type yet",
		// and that precision stops here. A client asking for a leaderboard before
		// anyone has voted wants an empty list; surfacing the distinction as a
		// GraphQL error would push an empty-state check onto every client.
		if errors.Is(err, elo.ErrNoRatings) {
			return &models.EloLeaderboard{EntityType: entityType}, nil
		}
		return nil, err
	}

	entries := make([]models.EloLeaderboardEntry, 0, len(board.Entries))
	for _, e := range board.Entries {
		entry := models.EloLeaderboardEntry{
			EntityID:  e.EntityID,
			Rating:    int(e.Rating.Rating),
			Deviation: e.Rating.Deviation,
			VoteCount: e.VoteCount,
		}
		if kind == elo.EntityPerformer {
			// Nullable on purpose: elo_ratings has no foreign key, so a rating row
			// can outlive the performer it names. A dangling row is a real state
			// and the client must be able to render the number without a name.
			if p, err := dataloader.For(ctx).PerformerByID.Load(e.EntityID); err == nil {
				entry.Performer = p
			}
		}
		entries = append(entries, entry)
	}

	return &models.EloLeaderboard{EntityType: entityType, Entries: entries}, nil
}

// eloRatingModel converts a service record to the GraphQL shape.
func eloRatingModel(kind elo.EntityType, id uuid.UUID, record *elo.RatingRecord) models.EloRating {
	return models.EloRating{
		EntityType: models.EloEntityType(kind),
		EntityID:   id,
		Rating:     int(record.Rating.Rating),
		Deviation:  record.Rating.Deviation,
		VoteCount:  record.VoteCount,
	}
}
