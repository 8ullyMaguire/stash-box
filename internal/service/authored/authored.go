// Package authored implements AUTHORED curation quests, bounties and claiming
// (SPEC §7.7).
//
// It is the counterpart to the generated-quest package, and the two differ in one
// load-bearing way:
//
//   - A GENERATED quest is a pure function of the archive. It is never stored, its
//     items are re-derived at read time, and it cannot carry a bounty.
//   - An AUTHORED quest is a PROMISE made by a person: "these five performers are
//     missing birthdates, and this is worth triple". Its items are fixed at
//     authoring time and its bounty is stored.
//
// The reason bounties live here and not on a generated quest is that a bounty is a
// promise by a person. A generator that could manufacture one would be a
// generator inventing a reward, and every reward in the system would be
// unauditable: nobody could say who decided this gap was worth triple.
package authored

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/completion"
	"github.com/stashapp/stash-box/internal/service/quest"
)

// ErrAlreadyClaimed is returned when another curator holds the claim.
//
// A distinct error rather than a generic failure because the caller's response is
// different: show "taken by someone else", do not show a 500. A curator clicking
// "claim" on a race they lost needs to be told, and being told is the whole feature.
var ErrAlreadyClaimed = errors.New("authored quest: item already claimed by another curator")

// ErrNotClaimedByCaller is returned when releasing a claim the caller does not
// hold. Restoring "not mine" rather than a success, because a caller told its
// release worked would believe the item is free when somebody else is working on
// it.
var ErrNotClaimedByCaller = errors.New("authored quest: item is not claimed by this curator")

// Item is one unit of an authored quest's work, with its claim resolved.
type Item struct {
	// The row type, not AuthoredQuestItem, because the read joins the claim and
	// sqlc only generates a row struct for a query with extra columns. Embedding
	// the plain table type would mean a second Item for reads and writes.
	queries.FindAuthoredQuestItemsRow
	// StillMissing is the reconciled truth: does this entity still lack the
	// quest's field?
	//
	// RECONCILED ON READ, not stored, and this is the field that makes an authored
	// quest different from a generated one. A generated quest's items vanish when
	// the field is filled because they were re-derived. An authored quest's items
	// are a claim about the archive that can turn out to be WRONG -- the performer
	// got a birthdate from a different quest, or an edit someone else submitted.
	//
	// A stored "completed" flag would be a second source of truth beside the
	// completion score, and the whole point of the completion service is that the
	// score is derived. So the item is re-scored on read and this is the answer.
	StillMissing bool
	// Missing is the entity's full missing list, so a client can show what else
	// would help without a second round trip.
	Missing []completion.Field
	// Score is the entity's completion score at read time.
	Score int
}

// Quest is an authored quest with its items reconciled against the archive.
type Quest struct {
	queries.AuthoredQuest
	Items []Item
	// Remaining is the count of items that still lack the field -- the number a
	// curator is actually asked to do. Derived, and NOT the row count: a quest
	// whose three items are all filled is complete with three rows and zero
	// remaining, and conflating the two is how a quest reports "2 remaining" while
	// listing two finished items.
	Remaining int
	// Completed is the count of items whose field has since been filled, by anyone.
	// Kept because "you did this one" is most of why a curator returns to a quest,
	// and it cannot be derived from Remaining.
	Completed int
}

// TotalPoints is what a curator earns for completing this quest: the base
// KindQuestCompleted value plus the bounty.
//
// The bounty MULTIPLIES nothing -- it adds. A multiplier would scale a base value
// that lives in the trust package, and the two would then disagree about what a
// quest is worth: the trust enum's 20 points is the value of a quest with no
// bounty, and an authored quest's stored bounty is the operator's addition to it.
// One place decides the sum, and it is here.
func (q *Quest) TotalPoints() int {
	return trustPointsQuestCompleted + q.BountyPoints
}

// trustPointsQuestCompleted mirrors trust.PointsPerKind[KindQuestCompleted].
//
// Duplicated rather than imported because importing trust from here would make
// quest completion depend on the trust service's internals, and the trust enum is
// allowed to move its values -- at which point this constant must move with it, and
// a test that pins the two together is what catches it if it does not.
// TestTrustPointsMatchTheTrustEnum compares them.
const trustPointsQuestCompleted = 20

// Service manages authored quests.
type Service struct {
	queries    *queries.Queries
	withTxn    queries.WithTxnFunc
	completion *completion.Service
}

// NewService builds the service.
//
// withTxn is required rather than optional because a quest and its items are
// created together, and a quest with no items is a quest nobody can do -- an
// orphan that a client would render as an empty card. One transaction, so the
// pair is atomic.
func NewService(queries *queries.Queries, withTxn queries.WithTxnFunc, completion *completion.Service) *Service {
	return &Service{queries: queries, withTxn: withTxn, completion: completion}
}

// AuthorQuestInput is what a person promises.
type AuthorQuestInput struct {
	// EntityType and Field are required, and the field must be SCORED. A quest
	// against an unweighted field is one nobody can complete, and it is the same
	// unreachable-field bug the generated generator refuses.
	EntityType quest.EntityType
	Field      completion.Field
	// Target is how many items are asked for. Zero means the default.
	Target int
	// BountyPoints is extra trust on top of the base value. Zero is legitimate --
	// an authored quest with no bounty is a curator's personal to-do list.
	BountyPoints int
	// Reason is why this gap is worth anyone's time. Optional in the schema,
	// strongly encouraged: "these are all one studio with no catalogue" is the
	// difference between a quest a curator accepts and one they skip.
	Reason string
	// AuthoredBy is who promised it.
	AuthoredBy uuid.UUID
	// ExpiresAt is when the promise lapses. Zero means never.
	ExpiresAt time.Time
	// EntityIDs are the entities the quest names. At least one, and no more than
	// Target -- authoring ten items for a target of five is a promise the quest
	// cannot keep, so the excess is refused rather than truncated. Truncating
	// would silently drop entities the author named, and the author would have no
	// way to tell.
	EntityIDs []uuid.UUID
}

// AuthorQuest creates a quest and its items in one transaction.
//
// Every entity is checked against the completion score BEFORE the insert, so an
// authored quest cannot promise to fix something that is already fixed. That check
// is the reason this is more than an insert: "add missing birthdates for these five
// performers" is a claim about the archive, and letting a person write a claim the
// archive contradicts produces a quest that is complete on arrival and pays a
// bounty for nothing.
func (s *Service) AuthorQuest(ctx context.Context, in AuthorQuestInput) (*Quest, error) {
	weight, err := completion.WeightFor(in.EntityType, in.Field)
	if err != nil {
		return nil, fmt.Errorf("author quest for %s.%s: %w", in.EntityType, in.Field, err)
	}
	if weight == 0 {
		return nil, fmt.Errorf("author quest for %s.%s: field is not scored, so no "+
			"entity can be missing it", in.EntityType, in.Field)
	}

	target := in.Target
	if target <= 0 {
		target = quest.DefaultTarget
	}
	if len(in.EntityIDs) == 0 {
		return nil, errors.New("author quest: a quest with no items is not a quest")
	}
	if len(in.EntityIDs) > target {
		return nil, fmt.Errorf("author quest: %d items named for a target of %d",
			len(in.EntityIDs), target)
	}
	if in.BountyPoints < 0 {
		return nil, errors.New("author quest: bounty points cannot be negative")
	}

	// Check the claim about the archive before writing it. Scored here, inside the
	// service, rather than by a caller: a caller that checked and then someone
	// else filled the field in between would author a stale promise, and the
	// window is exactly the kind that only shows up under load.
	alreadyHave := make([]uuid.UUID, 0, len(in.EntityIDs))
	for _, id := range in.EntityIDs {
		missing, err := s.missingFields(ctx, in.EntityType, id)
		if err != nil {
			return nil, err
		}
		if !containsField(missing, in.Field) {
			alreadyHave = append(alreadyHave, id)
		}
	}
	if len(alreadyHave) > 0 {
		return nil, fmt.Errorf("author quest: %d of %d entities already have %s "+
			"(%s): a quest must promise work that exists",
			len(alreadyHave), len(in.EntityIDs), in.Field, uuidList(alreadyHave))
	}

	var created queries.AuthoredQuest
	err = s.withTxn(func(tx *queries.Queries) error {
		var qerr error
		created, qerr = tx.CreateAuthoredQuest(ctx, queries.CreateAuthoredQuestParams{
			ID:           uuid.Must(uuid.NewV7()),
			EntityType:   string(in.EntityType),
			Field:        string(in.Field),
			Target:       target,
			BountyPoints: in.BountyPoints,
			Reason:       strOrNil(in.Reason),
			AuthoredBy:   nullUUID(in.AuthoredBy),
			ExpiresAt:    nullTime(in.ExpiresAt),
		})
		if qerr != nil {
			return qerr
		}
		for _, id := range in.EntityIDs {
			if _, qerr = tx.AddAuthoredQuestItem(ctx, queries.AddAuthoredQuestItemParams{
				ID:         uuid.Must(uuid.NewV7()),
				QuestID:    created.ID,
				EntityType: string(in.EntityType),
				EntityID:   id,
			}); qerr != nil {
				return qerr
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Returned through the normal read path so a caller cannot get a quest back
	// in a different shape from the one a later Get returns. Two shapes for one
	// entity is two places for a field to be missing.
	return s.Get(ctx, created.ID)
}

// Get reads a quest with its items reconciled against the archive.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*Quest, error) {
	questRow, err := s.queries.FindAuthoredQuest(ctx, id)
	if err != nil {
		return nil, err
	}
	items, err := s.queries.FindAuthoredQuestItems(ctx, id)
	if err != nil {
		return nil, err
	}

	out := &Quest{AuthoredQuest: questRow}
	for _, row := range items {
		item := Item{FindAuthoredQuestItemsRow: row}
		missing, err := s.missingFields(ctx, quest.EntityType(questRow.EntityType), row.EntityID)
		if err != nil {
			return nil, err
		}
		item.Missing = missing
		item.StillMissing = containsField(missing, completion.Field(questRow.Field))
		if item.StillMissing {
			out.Remaining++
		} else {
			out.Completed++
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

// Board lists the active quests.
//
// "Active" is the database's definition -- expires_at in the future or null -- and
// the client does not re-filter it. A client-side expiry filter and a server-side
// one disagree the moment a clock is involved, and the disagreement is a quest that
// looks available on one surface and is not.
func (s *Service) Board(ctx context.Context) ([]*Quest, error) {
	rows, err := s.queries.FindActiveAuthoredQuests(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*Quest, 0, len(rows))
	for _, row := range rows {
		q, err := s.Get(ctx, row.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, nil
}

// Claim takes a claim on an item.
//
// Idempotent for the same curator and a distinct error for a different one, which
// is the whole concurrency story:
//
//   - Free item: claimed.
//   - Claimed by YOU: succeeds and returns the row. A retried request must not
//     tell a curator they lost a race they won -- the network dropped their first
//     call and this is the retry.
//   - Claimed by SOMEONE ELSE: ErrAlreadyClaimed. The guarded UPDATE returns no
//     rows, and "no rows" is the database saying the WHERE clause excluded this
//     row, which is precisely the race being lost.
func (s *Service) Claim(ctx context.Context, itemID, curatorID uuid.UUID) (*Item, error) {
	claimed, err := s.queries.ClaimAuthoredQuestItem(ctx, queries.ClaimAuthoredQuestItemParams{
		ID:        itemID,
		ClaimedBy: nullUUID(curatorID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrAlreadyClaimed
		}
		return nil, err
	}

	// Claim() reads back through the table type rather than the joined row, so
	// the item it returns has no claimer's name. That is deliberate and is filled
	// by the caller's own identity, which it already knows: the claim it just
	// took. Joining the name here would be a second read to return a value the
	// caller supplied.
	item := Item{FindAuthoredQuestItemsRow: queries.FindAuthoredQuestItemsRow{
		ID:         claimed.ID,
		QuestID:    claimed.QuestID,
		EntityType: claimed.EntityType,
		EntityID:   claimed.EntityID,
		ClaimedBy:  claimed.ClaimedBy,
		ClaimedAt:  claimed.ClaimedAt,
		CreatedAt:  claimed.CreatedAt,
	}}
	questRow, err := s.queries.FindAuthoredQuest(ctx, claimed.QuestID)
	if err != nil {
		return nil, err
	}
	missing, err := s.missingFields(ctx, quest.EntityType(questRow.EntityType), claimed.EntityID)
	if err != nil {
		return nil, err
	}
	item.Missing = missing
	item.StillMissing = containsField(missing, completion.Field(questRow.Field))
	return &item, nil
}

// Release gives up a claim.
//
// Scoped to the caller's own claim in the WHERE, so one curator cannot release
// another's work. NotClaimedByCaller rather than success, because a caller told
// its release worked would believe the item is free.
func (s *Service) Release(ctx context.Context, itemID, curatorID uuid.UUID) error {
	_, err := s.queries.ReleaseAuthoredQuestItem(ctx, queries.ReleaseAuthoredQuestItemParams{
		ID:        itemID,
		ClaimedBy: nullUUID(curatorID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotClaimedByCaller
		}
		return err
	}
	return nil
}

// ExpireStaleClaims releases claims older than staleAfter.
//
// The backstop for curators who walked away, NOT the normal path -- releasing is
// something a curator does, and this is what happens when they do not. The grace
// period is the caller's because "how long is a claim good for" is an operator
// decision, not a constant in this package.
func (s *Service) ExpireStaleClaims(ctx context.Context, staleAfter time.Time) (int, error) {
	rows, err := s.queries.ExpireStaleQuestClaims(ctx, staleAfter)
	if err != nil {
		return 0, err
	}
	return len(rows), nil
}

// ClaimsByUser is a curator's in-progress work, newest first.
func (s *Service) ClaimsByUser(ctx context.Context, curatorID uuid.UUID) ([]queries.FindClaimsByUserRow, error) {
	return s.queries.FindClaimsByUser(ctx, nullUUID(curatorID))
}

// missingFields scores one entity, and translates "gone" into "everything is
// missing".
//
// A soft-deleted performer returns no rows, and a quest naming it is a quest whose
// item can never be completed. Treating it as missing is WRONG in the direction
// that matters: the curator would keep seeing an item to fix. So a vanished entity
// is reported as fully missing, which keeps it visible for reconciliation, and the
// distinction the quest actually needs -- "is this item done" -- stays answerable.
func (s *Service) missingFields(ctx context.Context, entityType quest.EntityType, id uuid.UUID) ([]completion.Field, error) {
	result, err := s.score(ctx, entityType, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return result.Missing, nil
}

func (s *Service) score(ctx context.Context, entityType quest.EntityType, id uuid.UUID) (completion.Result, error) {
	switch entityType {
	case completion.EntityPerformer:
		return s.completion.Performer(ctx, id)
	case completion.EntityScene:
		return s.completion.Scene(ctx, id)
	case completion.EntityStudio:
		return s.completion.Studio(ctx, id)
	case completion.EntitySite:
		return s.completion.Site(ctx, id)
	case completion.EntityTag:
		return s.completion.Tag(ctx, id)
	default:
		return completion.Result{}, fmt.Errorf("authored quest: unknown entity type %q", entityType)
	}
}

func containsField(fields []completion.Field, want completion.Field) bool {
	for _, f := range fields {
		if f == want {
			return true
		}
	}
	return false
}

func strOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func nullUUID(id uuid.UUID) uuid.NullUUID {
	return uuid.NullUUID{UUID: id, Valid: true}
}

func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// uuidList renders ids compactly for an error message.
//
// Capped, because an authoring mistake that names fifty already-complete entities
// produces an error nobody reads to the end, and the ids that matter are the first
// few.
func uuidList(ids []uuid.UUID) string {
	const show = 3
	if len(ids) <= show {
		out := ""
		for i, id := range ids {
			if i > 0 {
				out += ", "
			}
			out += id.String()
		}
		return out
	}
	return fmt.Sprintf("%s ... and %d more", ids[0].String(), len(ids)-show)
}
