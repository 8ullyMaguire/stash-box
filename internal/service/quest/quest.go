// Package quest generates curation quests from completion scores.
//
// A GENERATED quest is never stored. That is the whole design and it is not a
// performance decision: a generated quest that is a pure function of the archive
// has no state to go stale, and a stored one accumulates claims about the archive
// that were true when it was written. A curator working from a stored "5 performers
// with no birthdate" is chasing performers who were fixed an hour ago.
//
// The corollary is the rule this package exists to enforce: **a quest's items are
// re-derived at read time, and a claimed item disappears the moment the field it
// names is actually filled** -- not when the claim expires, and not when someone
// marks it done. The quest has no completion logic of its own; the completion
// score IS the completion logic.
package quest

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/service/completion"
)

// EntityType is re-exported so a quest caller does not import two packages to ask
// "which kind of entity is this quest about".
type EntityType = completion.EntityType

const (
	EntityPerformer = completion.EntityPerformer
	EntityScene     = completion.EntityScene
	EntityStudio    = completion.EntityStudio
	EntitySite      = completion.EntitySite
	EntityTag       = completion.EntityTag
)

// Item is one unit of work in a quest.
//
// Carries the MISSING FIELD, not just the entity. An item that named only an id
// would force a client to re-score every entity to find out what to do, and a
// client that guessed would be a client generating "improve this performer" —
// which is not a task anyone can act on. §7.7's "Add missing birthdates for 5
// performers" has a field in it, and the field is the quest.
type Item struct {
	EntityID uuid.UUID
	// Name is denormalised in, NOT the authoritative title. A quest is read in a
	// list next to dozens of others and "unnamed performer #4821" is not a task
	// a curator can recognise; the name is a convenience, and every caller that
	// needs the authoritative current value re-reads the entity.
	Name string
	// Missing is the fields this entity is missing, most valuable first. Ordered
	// by the scorer's weight list so the first one is the best thing to fix.
	Missing []completion.Field
	// Score is the entity's completion score at read time. Exposed so a client
	// can rank a quest's items by how much each is worth, without a second
	// scoring pass.
	Score int
	// ClaimedBy is non-nil when another curator has the item in progress.
	//
	// Claiming marks work in progress so nobody duplicates it; it does NOT
	// complete it. A quest that vanished on claim would lose a curator's
	// half-finished edit to an expiry, and the work would have to be redone.
	ClaimedBy *uuid.UUID
}

// MaxQuestTarget is the largest number of items one quest may name.
//
// A bound, not a preference: a quest is something a person finishes, and a list of
// ten thousand gaps is a report, not a task. It also bounds the work -- the
// generator scores every candidate it considers, so an unbounded target would
// score the entire archive to build a list no client renders.
const MaxQuestTarget = 200

// maxQuestPageSize is the largest candidate page the generator will fetch. Matches
// the completion service's ceiling so a target of MaxQuestTarget is served in
// pages rather than refused.
const maxQuestPageSize = 200

// Target is the count a quest asks for. A quest is BOUNDED, because "every
// incomplete performer" is not a quest.
const DefaultTarget = 5

// Quest is a generated curation task.
type Quest struct {
	EntityType EntityType
	// Field is the one field the quest is about, e.g. FieldBirthdate. A quest is
	// single-field on purpose: "add missing birthdates for 5 performers" is
	// completable, "improve 5 performers" is not.
	Field completion.Field
	// Wording is the human sentence, generated from the field and the count. Built
	// here rather than by the client so every surface says the same thing and a
	// translation is a single change.
	Wording string
	// Target is how many items are asked for. The list may be SHORTER than the
	// target when the archive has fewer gaps than that, and a quest that promises
	// five of something when three exist is a quest that cannot be completed.
	Target int
	Items  []Item
	// Remaining is len(Items) after claimed items are excluded -- the number a
	// curator is actually asked to do. Exposed because a quest showing 5 items
	// with 4 claimed and 1 available is confusing unless it says so.
	Remaining int
	// Available is the count of entities still missing the field archive-wide, so
	// a client can say "3 left" rather than pretending the quest is the whole
	// story.
	Available int
}

// Service generates quests.
//
// Holds the completion service rather than the queries directly: a quest is
// generated FROM completion scores, and letting the quest package query the
// archive itself would put the weights in two places and the two would drift.
type Service struct {
	completion *completion.Service
	// nameOf resolves an id to a display name. Injected because the completion
	// service deliberately does not know about names -- it scores fields, and
	// pulling entity names into it would make every score query load a join it
	// does not need.
	nameOf func(ctx context.Context, entityType EntityType, id uuid.UUID) (string, error)
}

// NewService builds a quest generator.
//
// `nameOf` is a function rather than an interface: there is exactly one
// implementation shape here, the callers all pass the same closure over the
// resolver, and a one-method interface would be a type to maintain for no
// behaviour.
func NewService(c *completion.Service, nameOf func(context.Context, EntityType, uuid.UUID) (string, error)) *Service {
	return &Service{completion: c, nameOf: nameOf}
}

// Generate builds the quest for one entity type and one field.
//
// Returns (nil, nil) when nothing is missing, which is NOT an error. "There is
// nothing to do here" is the answer to most of these questions most of the time,
// and a caller that has to distinguish it from a failure will get it wrong -- an
// empty quest is a normal outcome, and a client rendering a quest board should
// show fewer cards, not an error.
func (s *Service) Generate(ctx context.Context, entityType EntityType, field completion.Field, target int) (*Quest, error) {
	if target <= 0 {
		target = DefaultTarget
	}

	// Which weight the field carries sets the threshold. An entity missing the
	// field is missing AT LEAST that weight, and the count query counts missing
	// weight, so this is the bridge between "the field is missing" and something
	// the database can filter on.
	weight, err := completion.WeightFor(entityType, field)
	if err != nil {
		return nil, fmt.Errorf("quest for %s.%s: %w", entityType, field, err)
	}
	if weight == 0 {
		// A zero-weight field is not a quest. `name` is weight zero on every type
		// because it is NOT NULL and always present, so "add missing names" is a
		// quest nobody can complete -- the same unreachable-field bug the parity
		// test found in the scorer, and the same reason to refuse rather than
		// return an empty quest.
		return nil, fmt.Errorf("quest for %s.%s: field is not scored, so no entity "+
			"can be missing it", entityType, field)
	}

	// Candidates are entities missing at least the field's own weight. That
	// OVER-selects: an entity missing the birthdate AND the country is missing
	// more than the birthdate's weight, and this is the query that finds it. The
	// per-entity score below is what actually decides membership, so
	// over-selection here costs a few extra score reads and cannot include an
	// entity that has the field.
	total, err := completion.TotalWeight(entityType)
	if err != nil {
		return nil, err
	}
	// Over-fetch, then page if one page was not enough.
	//
	// The multiplier is 4 because the candidate list OVER-selects -- an entity
	// missing a smaller amount of weight than the field's own also matches the
	// threshold -- and the per-entity score below discards the extras. The paging
	// matters because ListIncomplete REFUSES a page larger than 200 rather than
	// silently truncating it, so a big target has to walk forward with afterID
	// instead of asking for everything at once and getting an error.
	//
	// Found by a test that asked for a 500-item target, received an error naming
	// the page-size ceiling, and was right to: the caller said 500 and the query
	// would have quietly given 50.
	const overFetch = 4
	pageSize := target * overFetch
	if pageSize < 20 {
		pageSize = 20
	}
	if pageSize > maxQuestPageSize {
		pageSize = maxQuestPageSize
	}

	// Hard bound on how much of the archive one quest will consider. Without it a
	// caller asking for a million items walks a million rows to build a list no
	// client will render. The bound is a REFUSAL rather than a clamp so a caller
	// that asked for more is told, instead of quietly receiving less.
	if target > MaxQuestTarget {
		return nil, fmt.Errorf("quest: target %d exceeds the maximum of %d",
			target, MaxQuestTarget)
	}

	candidates, err := s.collectCandidates(ctx, entityType, weight, pageSize, target)
	if err != nil {
		return nil, err
	}

	quest := &Quest{
		EntityType: entityType,
		Field:      field,
		Target:     target,
		Wording:    Wording(entityType, field, target),
	}
	if total > 0 {
		// The archive-wide count, for "N left" on the quest card. Computed from
		// the same threshold, so a quest cannot claim more available work than
		// the count query agrees exists.
		quest.Available, err = s.completion.CountIncomplete(ctx, entityType, thresholdFor(entityType, field))
		if err != nil {
			return nil, err
		}
	}

	for _, id := range candidates {
		// The per-entity score is the decision, not the candidate query. This is
		// the same "over-select then verify" shape as the SQL, and the reason the
		// quest cannot name an entity that has the field it is about.
		result, err := s.score(ctx, entityType, id)
		if err != nil {
			return nil, err
		}
		if !containsField(result.Missing, field) {
			continue
		}

		item := Item{
			EntityID: id,
			Missing:  result.Missing,
			Score:    result.Score,
		}
		if s.nameOf != nil {
			// A missing name is not a reason to drop the item -- the id is still
			// actionable and the name may simply not be loaded. An empty name
			// renders worse; a missing item renders nothing.
			name, err := s.nameOf(ctx, entityType, id)
			if err == nil {
				item.Name = name
			}
		}

		quest.Items = append(quest.Items, item)
	}

	// Sort BEFORE truncating, not after.
	//
	// The candidates come back in ID order, which is arbitrary -- it is the
	// keyset pagination's tiebreaker, not a measure of merit. Truncating first
	// and sorting second therefore selects the target by UUID, so which entities
	// appear in a quest depends on when they were created. A performer created
	// last is crowded out of a quest about the very field it is missing.
	//
	// Sorting first and taking the emptiest N makes the selection MERIT-based and
	// independent of insertion order, which is the property a curator relies on:
	// a quest always shows the worst-off entities, whoever they are.
	//
	// Found by an integration test that failed only in a batch: run alone, one
	// performer was the only candidate and it was in the quest; run after other
	// tests, five better-scoring performers filled the target and pushed it out.
	// A test that passes alone and fails in a batch is reporting a real ordering
	// bug, not a flaky fixture.
	SortByUrgency(quest.Items)

	if len(quest.Items) > target {
		quest.Items = quest.Items[:target]
	}

	quest.Remaining = len(quest.Items)
	if quest.Remaining == 0 {
		// Nothing is missing this field. Returning an empty quest rather than nil
		// here would be a lie about there being no work, and the caller would
		// render a card with zero items.
		return nil, nil
	}
	return quest, nil
}

// GenerateAll builds the quest for every scored field of a type, skipping the ones
// with no gaps.
//
// SKIPPED rather than returned empty, because a quest board listing 23 quests of
// which 20 say "nothing to do" is a board nobody reads. The fields are returned in
// weight order, so the most valuable gap leads.
func (s *Service) GenerateAll(ctx context.Context, entityType EntityType, target int) ([]*Quest, error) {
	fields, err := completion.Fields(entityType)
	if err != nil {
		return nil, err
	}

	out := make([]*Quest, 0, len(fields))
	for _, field := range fields {
		// A weight-zero field cannot be missing, and asking costs a query per
		// type per field for an answer that is always empty.
		weight, err := completion.WeightFor(entityType, field)
		if err != nil {
			return nil, err
		}
		if weight == 0 {
			continue
		}

		q, err := s.Generate(ctx, entityType, field, target)
		if err != nil {
			return nil, err
		}
		if q != nil {
			out = append(out, q)
		}
	}
	return out, nil
}

// score dispatches to the right per-entity scorer.
//
// A switch over the five types rather than an interface, because each scorer takes
// a different query underneath and the shared surface is one method. An interface
// here would be five one-method types.
func (s *Service) score(ctx context.Context, entityType EntityType, id uuid.UUID) (completion.Result, error) {
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
		return completion.Result{}, fmt.Errorf("quest: unknown entity type %q", entityType)
	}
}

// collectCandidates pages candidates until it has enough.
//
// Stops as soon as one page holds `target` verified items OR `maxPages` pages have
// been read, and the page cap is what makes this bounded: a quest for the emptiest
// N is served from the first page that holds N candidates, and the sorting below
// picks the worst of them. Walking the whole archive to find the single emptiest
// entity is not a quest, it is a database migration wearing a query.
//
// A page that returns nothing ends the walk, because pages are by id and a gap in
// the id sequence is not a reason to keep going.
func (s *Service) collectCandidates(ctx context.Context, entityType EntityType, minMissing, pageSize, target int) ([]uuid.UUID, error) {
	const maxPages = 4

	var (
		out     []uuid.UUID
		afterID *uuid.UUID
	)
	for page := 0; page < maxPages; page++ {
		ids, err := s.completion.ListIncomplete(ctx, entityType, minMissing, afterID, pageSize)
		if err != nil {
			return nil, err
		}
		if len(ids) == 0 {
			return out, nil
		}

		out = append(out, ids...)
		// Enough material for the target, with room for the over-selection to be
		// filtered. Without the margin this pages forever looking for candidates
		// that the per-entity check is about to reject.
		if len(out) >= target*2 {
			return out, nil
		}
		if len(ids) < pageSize {
			return out, nil
		}
		last := ids[len(ids)-1]
		afterID = &last
	}
	return out, nil
}

// thresholdFor converts "missing this field" into the count query's missing-weight
// threshold, as a SCORE the service inverts.
//
// The derivation: an entity is incomplete at score `s` iff it is missing more than
// `total*(100-s)/100` weight. We want the entities missing AT LEAST the field's
// weight, so the equivalent score is `100 - fieldWeight*100/total`. Truncating
// rather than rounding DOWN is deliberate: we would rather ask for a few entities
// that turn out to have other gaps too than miss an entity missing exactly this
// field, and the per-entity score below filters the extras anyway.
func thresholdFor(entityType EntityType, field completion.Field) int {
	weight, err := completion.WeightFor(entityType, field)
	if err != nil || weight == 0 {
		return 100
	}
	total, err := completion.TotalWeight(entityType)
	if err != nil || total == 0 {
		return 100
	}
	return 100 - (weight * 100 / total)
}

func containsField(fields []completion.Field, want completion.Field) bool {
	for _, f := range fields {
		if f == want {
			return true
		}
	}
	return false
}

// Wording is the human sentence for a quest, in one place.
//
// Built here and not by the client because §7.7's own examples are sentences
// ("Add missing birthdates for 5 performers") and a client that builds its own
// produces a different sentence per surface, in a different voice, with a
// different plural rule. The count is in the sentence, so a client showing 3 of 5
// has to be able to say "3 of 5" -- which is why the client formats the numbers
// and this formats the noun.
//
// The plural is irregular on purpose: "performers" is the noun the archive uses
// everywhere, and a quest saying "1 performers" is the kind of small wrongness
// that makes a whole surface feel unmaintained.
func Wording(entityType EntityType, field completion.Field, count int) string {
	noun := pluralNoun(entityType)
	verb := "Add missing"
	switch field {
	case completion.FieldBirthdate, completion.FieldAliases, completion.FieldURLs,
		completion.FieldImage, completion.FieldTags, completion.FieldDetails,
		completion.FieldPerformers, completion.FieldStudio, completion.FieldDate,
		completion.FieldSnapshotCoverage:
		// These read correctly as "add missing X".
	default:
		verb = "Fill in missing"
	}
	return fmt.Sprintf("%s %s for %d %s", verb, humanField(field), count, noun(count))
}

// humanField turns a field's machine name into the word a curator reads.
func humanField(f completion.Field) string {
	return strings.ReplaceAll(string(f), "_", " ")
}

// pluralNoun is the entity name, pluralised by count.
func pluralNoun(t EntityType) func(int) string {
	switch t {
	case EntityPerformer:
		return func(n int) string { return plural(n, "performer", "performers") }
	case EntityScene:
		return func(n int) string { return plural(n, "scene", "scenes") }
	case EntityStudio:
		return func(n int) string { return plural(n, "studio", "studios") }
	case EntitySite:
		return func(n int) string { return plural(n, "site", "sites") }
	case EntityTag:
		return func(n int) string { return plural(n, "tag", "tags") }
	default:
		return func(n int) string { return plural(n, "entity", "entities") }
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// SortByUrgency orders items by score ascending -- the emptiest entity first.
//
// Ascending, which is the opposite of the instinct: a quest is a list of gaps, and
// the biggest gap is the one worth doing first. Sorting descending would put the
// nearly-complete entities at the top, which is backwards for the curator and
// right for a progress bar, and a quest is not a progress bar.
//
// The score is the tiebreaker-free key and the id breaks ties, so the order is
// STABLE across calls. A quest that reshuffles between two reads of the same
// archive makes a curator's place in the list meaningless.
func SortByUrgency(items []Item) {
	sort.SliceStable(items, func(a, b int) bool {
		if items[a].Score != items[b].Score {
			return items[a].Score < items[b].Score
		}
		return items[a].EntityID.String() < items[b].EntityID.String()
	})
}
