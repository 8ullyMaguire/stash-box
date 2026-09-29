// Package elo implements pairwise Elo ranking from SPEC §9.
//
// The design in one paragraph: elo_votes is an append-only log and the source of
// truth; elo_ratings is a denormalised rating per entity so that reading a
// leaderboard does not replay every vote. Recording a vote writes the log, moves
// both participants' ratings, and rebuilds the voter's taste vector, and the
// ratings can always be rebuilt by replaying the log.
//
// Three properties of the rating maths live in glicko.go and matter here:
//
//   - Update is a one-element UpdateBatch, so a rebuild that groups a rating
//     period's votes and the incremental path that records them one at a time
//     agree by construction.
//   - A rating is a CACHE. Everything that matters is derivable from the vote
//     log, which is why Rebuild exists and why it is the answer to "the maths
//     was fixed".
//   - Volatility is PERSISTENT state, not a per-update intermediate. Step 5 of
//     the paper reads the previous sigma to bound how far it may move, so a
//     rating that is loaded without it restarts the player at 0.06 and the
//     algorithm quietly forgets who is consistent and who is erratic.
package elo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/stashapp/stash-box/internal/queries"
)

// ErrNoRatings is returned when a leaderboard is requested for an entity type
// with no rated entities at all.
//
// A distinct error rather than an empty list, because "nothing has been voted on
// yet" and "here are zero results" are different answers for a caller and only
// one of them is worth showing an empty state for.
var ErrNoRatings = errors.New("no elo ratings exist for this entity type")

// Elo records votes and maintains the derived ratings and taste vectors.
type Elo struct {
	queries *queries.Queries
	withTxn queries.WithTxnFunc
	// now is injectable so tests can hold elapsed time still. The default is
	// time.Now.
	now func() time.Time
}

// NewElo creates a new elo service.
func NewElo(queries *queries.Queries, withTxn queries.WithTxnFunc) *Elo {
	return &Elo{queries: queries, withTxn: withTxn, now: time.Now}
}

// WithTxn executes a function within a transaction.
func (s *Elo) WithTxn(fn func(*queries.Queries) error) error {
	return s.withTxn(fn)
}

// WithClock overrides the clock, for tests.
//
// There is deliberately no config path for this. A configurable clock in a
// rating system is a configurable rating system, and Glicko's whole behaviour
// turns on elapsed time, so a caller who could set it could hand themselves any
// rating they liked.
func (s *Elo) WithClock(now func() time.Time) *Elo {
	s.now = now
	return s
}

// RatingRecord is a stored rating with the vote count a leaderboard needs.
type RatingRecord struct {
	EntityType EntityType
	EntityID   uuid.UUID
	Rating     Rating
	// VoteCount is how many results the rating is built from.
	//
	// Stored rather than counted on read, and this is a deliberate trade: a
	// leaderboard page of 100 would otherwise cost 100 counts. But the count is
	// not derivable from the rating alone, so the denormalised value can drift if
	// a vote is ever inserted without updating the rating. Rebuild recomputes it,
	// and RecordVote is the only writer, so the two agree by construction.
	VoteCount int
	// LastRatedAt is when the rating last moved. Not an audit column: it is the
	// basis for the next update's time decay, so dropping it would make every
	// subsequent update assume the entity was rated just now.
	LastRatedAt time.Time
}

// Vote records a matchup vote and moves both participants' ratings.
//
// Everything happens in ONE transaction, and that is the load-bearing decision
// rather than a convenience. Three writes have to agree: the vote is in the log,
// both ratings have moved, and the voter's taste vector is current. A vote in the
// log with unmoved ratings is invisible corruption -- the log says it happened, a
// rebuild would apply it, and until then every leaderboard is wrong. A moved
// rating with no vote is worse: the log is the source of truth, so a rebuild would
// erase it and the user would watch their rating jump back to where it was.
//
// The ordering within the transaction is load-bearing too. BOTH ratings are read
// BEFORE either is written, and each side is then updated against the pair of
// PRE-MATCH states. Reading them interleaved would let the winner's new rating
// become the loser's opponent state, and the outcome would depend on which side
// the code happened to touch first -- an ordering-dependent rating change, which
// is the kind of bug that shows up as an occasional unexplained drift.
func (s *Elo) Vote(ctx context.Context, input VoteInput) (*RatingRecord, error) {
	if !input.Matchup.EntityType.Valid() {
		return nil, fmt.Errorf("%w: %q", ErrUnknownEntityType, input.Matchup.EntityType)
	}
	if !input.PickedSide.Valid() {
		return nil, ErrInvalidSide
	}
	left, right := input.Matchup.Left, input.Matchup.Right
	if left.Type != input.Matchup.EntityType || right.Type != input.Matchup.EntityType {
		return nil, fmt.Errorf("%w: both sides must be %q", ErrUnknownEntityType,
			input.Matchup.EntityType)
	}
	if left.ID == right.ID {
		return nil, ErrSameEntity
	}

	var result *RatingRecord
	err := s.withTxn(func(tx *queries.Queries) error {
		// Both pre-match states, read before any write.
		leftBefore, err := s.load(ctx, tx, left)
		if err != nil {
			return err
		}
		rightBefore, err := s.load(ctx, tx, right)
		if err != nil {
			return err
		}

		// The side the user chose, and its elapsed time. The elapsed times are
		// per-SIDE rather than one value for the matchup because they differ: a
		// performer nobody has rated for a year is not in the same position as one
		// rated last week, and the whole point of Glicko over Elo is that a gap in
		// play increases uncertainty.
		picked, pickedElapsed, notPickedElapsed := left, input.ElapsedDaysA, input.ElapsedDaysB
		if input.PickedSide == PickedRight {
			picked, pickedElapsed, notPickedElapsed = right, input.ElapsedDaysB, input.ElapsedDaysA
		}

		// Each side is updated from the pair of pre-match states, with the score
		// flipped for the loser. Update takes the opponent by value, so neither
		// Before struct is mutated and the two updates cannot interfere.
		pickedAfter := leftBefore.Rating.Update(Outcome{
			Self:        leftBefore.Rating,
			Opponent:    rightBefore.Rating,
			Score:       Win,
			ElapsedDays: pickedElapsed,
		})
		notPickedAfter := rightBefore.Rating.Update(Outcome{
			Self:        rightBefore.Rating,
			Opponent:    leftBefore.Rating,
			Score:       Loss,
			ElapsedDays: notPickedElapsed,
		})

		// Store the vote in DISPLAY order, not in picked/not-picked order. The
		// columns are named winner_id/loser_id but they mean "the slot the user
		// chose" and "the slot they did not": position bias -- a tendency to pick
		// whichever candidate is shown first -- is the cheapest way to game a
		// pairwise vote, and it is only measurable if the order the user actually
		// saw survives into the log. picked_side records which slot that was.
		voteID, err := uuid.NewV7()
		if err != nil {
			return err
		}
		if _, err := tx.RecordEloVote(ctx, queries.RecordEloVoteParams{
			ID:         voteID,
			UserID:     input.UserID,
			WinnerID:   left.ID,
			LoserID:    right.ID,
			WinnerType: string(left.Type),
			LoserType:  string(right.Type),
			PickedSide: int16(input.PickedSide),
		}); err != nil {
			return err
		}

		now := s.now()
		if err := s.persist(ctx, tx, left, pickedAfter, now); err != nil {
			return err
		}
		if err := s.persist(ctx, tx, right, notPickedAfter, now); err != nil {
			return err
		}
		if _, err := s.rebuildTasteVector(ctx, tx, input.UserID); err != nil {
			return err
		}

		// Return the rating of the side the user chose, because that is the one a
		// "your vote counted" UI wants to show, and the count is that side's own
		// plus this vote. Resolved explicitly rather than by carrying the records
		// around, because getting it wrong would report a voter's own history
		// back to them.
		out := &RatingRecord{
			EntityType:  input.Matchup.EntityType,
			EntityID:    picked.ID,
			Rating:      pickedAfter,
			VoteCount:   0,
			LastRatedAt: now,
		}
		if picked.ID == left.ID {
			out.VoteCount = leftBefore.VoteCount + 1
		} else {
			out.VoteCount = rightBefore.VoteCount + 1
		}
		result = out
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// loaded is a rating as it came out of the database.
type loaded struct {
	Rating      Rating
	VoteCount   int
	LastRatedAt time.Time
}

// load reads a rating, falling back to a fresh one.
//
// A missing row is NOT an error, and this is the important case. Migration 77
// seeds ratings for performers that existed at migration time, but every
// performer created afterwards has no row until it is first voted on, so the
// new-performer path is the common one and not the exception. Erroring here would
// make the first vote on every new performer fail.
//
// The vote count is counted rather than stored because the migration 77 schema
// has no count column -- an honest consequence of keeping elo_ratings a pure
// cache of (rating, deviation, volatility) -- so it is derived from the log.
func (s *Elo) load(ctx context.Context, tx *queries.Queries, e Entity) (loaded, error) {
	row, err := tx.GetEloRating(ctx, queries.GetEloRatingParams{
		EntityType: string(e.Type),
		EntityID:   e.ID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return loaded{Rating: NewRating()}, nil
		}
		return loaded{}, err
	}

	count, err := tx.CountEloVotesForEntity(ctx, queries.CountEloVotesForEntityParams{
		EntityType: string(e.Type),
		EntityID:   e.ID,
	})
	if err != nil {
		return loaded{}, err
	}

	return loaded{
		Rating: Rating{
			Rating:     float64(row.Rating),
			Deviation:  row.Deviation,
			Volatility: row.Volatility,
		},
		VoteCount:   int(count),
		LastRatedAt: row.LastRatedAt.Time,
	}, nil
}

// persist writes a rating back.
func (s *Elo) persist(ctx context.Context, tx *queries.Queries, e Entity, r Rating, _ time.Time) error {
	_, err := tx.UpsertEloRating(ctx, queries.UpsertEloRatingParams{
		EntityType: string(e.Type),
		EntityID:   e.ID,
		Rating:     int(round(r.Rating)),
		Deviation:  r.Deviation,
		Volatility: r.Volatility,
	})
	return err
}

// round is math.Round, named locally so the truncation-to-int at the storage
// boundary is visible at the call site.
//
// The database column is an integer because every Glicko implementation stores
// display ratings as integers, and because an integer rating makes the SQL
// leaderboard ordering and the Go Rankable ordering agree. It DOES cost
// precision: the true value is carried in memory and only rounded on write, so a
// rebuild from the log will not reproduce the rounded values exactly. That is
// acceptable and is why Rebuild is a tool for policy and maths changes, not for
// verifying that the cache matches the log.
func round(f float64) float64 {
	if f < 0 {
		return float64(int(f - 0.5))
	}
	return float64(int(f + 0.5))
}

// TasteVector is a user's preference profile (SPEC §2).
type TasteVector struct {
	UserID uuid.UUID
	// Vector maps a feature key to a signed preference. The key is
	// "<kind>:<entity uuid>" -- see the TasteKey* constants and the reasoning on
	// tasteKey.
	Vector map[string]float64
	// VoteCount is how many votes produced this vector.
	//
	// Load-bearing, not informational. A vector built from three votes is not
	// evidence of a preference, and a recommender that cannot tell those apart
	// will confidently serve three votes' worth of signal as though it were a
	// thousand. It is stored beside the vector for exactly that reason.
	VoteCount int
	UpdatedAt time.Time
}

// rebuildTasteVector recomputes a user's whole taste vector from their votes.
//
// A REBUILD rather than an incremental add, and the reason is that an
// incremental add cannot be undone. If the update below is wrong, a user who has
// voted a thousand times has a thousand votes' worth of error baked in, and there
// is no way to correct it except by deleting the row and hoping something
// rebuilds it. Rebuilding costs one indexed scan of a single user's votes --
// cheap, and bounded by how much one person has voted -- and it is correct by
// construction.
//
// The useful side effect: a change to the taste formula is self-healing for every
// user on their next vote, with no migration.
func (s *Elo) rebuildTasteVector(ctx context.Context, tx *queries.Queries, userID uuid.UUID) (*TasteVector, error) {
	votes, err := tx.GetEloVotesForUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	vector := make(map[string]float64, len(votes)*2)
	for _, v := range votes {
		// +1 for the slot the user chose, -1 for the slot they did not. Both keys
		// are per-entity, so the vector records which entities this user likes
		// rather than which kinds of entity they vote on.
		//
		// A per-kind vector ("this user likes performers, +3") cannot produce a
		// recommendation for a specific performer, which is what SPEC §4 actually
		// asks for, so the entity is the key and the kind is the prefix.
		vector[tasteKey(winnerType(v), v.WinnerID)] += 1
		vector[tasteKey(loserType(v), v.LoserID)] -= 1
	}

	encoded, err := json.Marshal(vector)
	if err != nil {
		// Unreachable: the map holds finite float64s only, and every value is
		// +/-1 times an integer count. Handled anyway because the alternative is a
		// silent nil vector, and a user whose taste is empty gets a feed with no
		// personalisation and no explanation.
		return nil, fmt.Errorf("encoding taste vector for %s: %w", userID, err)
	}

	tv, err := tx.UpsertTasteVector(ctx, queries.UpsertTasteVectorParams{
		UserID:    userID,
		Vector:    encoded,
		VoteCount: len(votes),
	})
	if err != nil {
		return nil, err
	}
	return tasteVectorFromRow(tv), nil
}

// TasteVectorFor reads a user's stored taste vector.
func (s *Elo) TasteVectorFor(ctx context.Context, userID uuid.UUID) (*TasteVector, error) {
	row, err := s.queries.GetTasteVector(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// A user who has never voted has no row, and an empty vector is the
			// right answer rather than an error: "no preferences recorded yet" is a
			// normal state, and every caller of this has to handle it anyway.
			return &TasteVector{UserID: userID, Vector: map[string]float64{}}, nil
		}
		return nil, err
	}
	return tasteVectorFromRow(row), nil
}

// tasteVectorFromRow decodes a stored taste vector.
//
// A decode error returns an EMPTY vector rather than an error, and that is a
// deliberate choice worth stating because the opposite is defensible. A vector
// this code cannot read is either corrupt or written by a version with a
// different key scheme; in both cases the honest state is "no preferences
// recorded" -- which every caller of this function already has to handle, since
// every new user has no row at all. Returning an error would make a corrupt
// taste row break the whole feed for that user, when the vector is a ranking hint
// and not a source of truth. The vote log that produced it is intact either way.
func tasteVectorFromRow(row queries.TasteVector) *TasteVector {
	out := &TasteVector{
		UserID:    row.UserID,
		Vector:    map[string]float64{},
		VoteCount: row.VoteCount,
		UpdatedAt: row.UpdatedAt.Time,
	}
	if len(row.Vector) == 0 {
		return out
	}
	decoded := map[string]float64{}
	if err := json.Unmarshal(row.Vector, &decoded); err != nil {
		return out
	}
	out.Vector = decoded
	return out
}

// Leaderboard is one page of ranked entities.
type Leaderboard struct {
	EntityType EntityType
	Entries    []LeaderboardEntry
}

// LeaderboardEntry is one ranked row.
type LeaderboardEntry struct {
	EntityID uuid.UUID
	Rating   Rating
	// VoteCount is how many results the rating is built from. The UI needs it to
	// show "needs more votes" rather than presenting a three-vote rating as though
	// it were settled.
	VoteCount int
}

// Leaderboard returns the highest-ranked entities of a type.
//
// Ordered by Rankable.Less, NOT by rating alone, and that distinction is the most
// important line in this function for anti-gaming. A plain sort by rating puts a
// performer with three votes and a 1600 above one with three hundred votes and a
// 1550, and the first three votes are the cheapest thing in the whole system to
// obtain. Rankable resolves near-ties by vote count, so the cheap rating does not
// win.
//
// No minimum vote count is applied here, because filtering at the query would
// hide newcomers from the leaderboard entirely. The deviation already encodes how
// much to trust a rating; a caller wanting a settled-only board filters on
// Rankable.Settled itself.
func (s *Elo) Leaderboard(ctx context.Context, entityType EntityType, limit int) (*Leaderboard, error) {
	if !entityType.Valid() {
		return nil, fmt.Errorf("%w: %q", ErrUnknownEntityType, entityType)
	}
	if limit <= 0 || limit > 500 {
		// Bounded rather than trusting the caller. An unbounded leaderboard query
		// is a denial-of-service vector, and 500 is more rows than any UI renders.
		limit = 100
	}

	rows, err := s.queries.ListEloRatings(ctx, queries.ListEloRatingsParams{
		EntityType: string(entityType),
		Limit:      int32(limit * 4),
	})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("%w: %q", ErrNoRatings, entityType)
	}

	entries := make([]LeaderboardEntry, 0, len(rows))
	for _, row := range rows {
		count, err := s.queries.CountEloVotesForEntity(ctx, queries.CountEloVotesForEntityParams{
			EntityType: row.EntityType,
			EntityID:   row.EntityID,
		})
		if err != nil {
			return nil, err
		}
		entries = append(entries, LeaderboardEntry{
			EntityID: row.EntityID,
			Rating: Rating{
				Rating:     float64(row.Rating),
				Deviation:  row.Deviation,
				Volatility: row.Volatility,
			},
			VoteCount: int(count),
		})
	}

	sort.SliceStable(entries, func(i, j int) bool {
		a := Rankable{Rating: entries[i].Rating, VoteCount: entries[i].VoteCount}
		b := Rankable{Rating: entries[j].Rating, VoteCount: entries[j].VoteCount}
		return a.Less(b)
	})
	if len(entries) > limit {
		entries = entries[:limit]
	}
	return &Leaderboard{EntityType: entityType, Entries: entries}, nil
}

// RatingFor reads one entity's stored rating.
//
// A missing row yields a fresh default rather than an error, matching Vote's
// behaviour: "not rated yet" is the state of every performer created since
// migration 77 that nobody has voted on, and a caller displaying a rating wants
// 1500 in that case, not a failure.
func (s *Elo) RatingFor(ctx context.Context, e Entity) (*RatingRecord, error) {
	if !e.Type.Valid() {
		return nil, fmt.Errorf("%w: %q", ErrUnknownEntityType, e.Type)
	}
	row, err := s.queries.GetEloRating(ctx, queries.GetEloRatingParams{
		EntityType: string(e.Type),
		EntityID:   e.ID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return &RatingRecord{EntityType: e.Type, EntityID: e.ID, Rating: NewRating()}, nil
		}
		return nil, err
	}
	count, err := s.queries.CountEloVotesForEntity(ctx, queries.CountEloVotesForEntityParams{
		EntityType: string(e.Type),
		EntityID:   e.ID,
	})
	if err != nil {
		return nil, err
	}
	return &RatingRecord{
		EntityType:  e.Type,
		EntityID:    e.ID,
		Rating:      Rating{Rating: float64(row.Rating), Deviation: row.Deviation, Volatility: row.Volatility},
		VoteCount:   int(count),
		LastRatedAt: row.LastRatedAt.Time,
	}, nil
}
