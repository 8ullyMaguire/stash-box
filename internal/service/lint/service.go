// Package lint runs the data-lint detectors that SPEC §7.24.4 names: "each detector is a named SQL
// query emitting quest candidates, against tables that already exist."
//
// # The shape, and why it is two halves
//
// Migration 98 is the REGISTRY: one row per detector, carrying the slug, the description a curator
// reads, whether it is enabled, and what a candidate is worth. The SQL is NOT in the row -- it lives
// in internal/queries/sql/lint.sql as a named query, and this package is the mapping between the two.
// Migration 98 states the reason as a property: "a slug that names no Go function is a broken quest,
// and the registry refuses it at startup instead." So Validate is not a nicety; it is the half of the
// design that keeps the registry and the code from disagreeing. A row whose slug has no detector, if
// it were quietly skipped, would look exactly like a detector that found nothing.
//
// # The three things this package refuses to do
//
//   - IT DOES NOT STORE CANDIDATES. A lint finding is a pure function of the archive, the same way a
//     generated quest is (see internal/service/quest): a stored "these two scenes share a URL" is
//     false the moment somebody fixes one of them, and a curator working from it is working on last
//     week's archive. Every candidate below is re-derived on read, and an item leaves the board when
//     the RECORD is fixed -- not when the board is refreshed, not when somebody claims it.
//   - IT DOES NOT MERGE, EDIT OR FIX ANYTHING. §7.24.4 is explicit that an alias collision "emits a
//     quest and never auto-merges", because the merge path is the consensus flow in
//     internal/service/edit. A lint that wrote would be an unattributable edit, which is the one
//     thing this schema's edit trail exists to prevent.
//   - IT DOES NOT SCORE. Bounty points come from the registry row, and the registry row is the
//     operator's statement about what a detector is worth. This package never computes a price; the
//     moment it did, there would be two answers to "what is this quest worth" and §7.24.3's audit
//     (100_bounty_pricing_audit) exists to keep that number in one place.
//
// # The import validation pass
//
// §7.24.4 calls this "the more valuable half": the same SQL that fills a board can be asked about the
// entities an import just touched. ValidateEntities is that question, and it is EXACT rather than
// best-effort because the filter is a parameter of the query (`sqlc.narg(entity_ids)`), not a filter
// over a bounded result in Go. The difference matters on a real archive: "the first 500 findings"
// filtered down to five imported performers is a pass that quietly stops reporting, and the symptom
// would be an import that looks clean.
package lint

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/completion"
)

// EntityType is the completion package's vocabulary, re-exported so a candidate cannot name an entity
// type the rest of the product does not know about.
type EntityType = completion.EntityType

const (
	// DefaultLimit is how many candidates a board shows per detector unless the caller says otherwise.
	// Small on purpose: a lint board is a work queue, and "5 things to fix" is a task while "4,812" is
	// a reason to close the tab. The sweep that wants everything passes its own limit.
	DefaultLimit = 5

	// HardLimit caps what a caller can ask for in one query. Detectors run against a live database and
	// some of them (the alias join, the URL collisions) scan tables with a row per (entity, url); an
	// unbounded ask is a query that decides on its own to take the instance down.
	HardLimit = 5000

	// DefaultDaysAhead is how far in the future a scene date may be before it counts as impossible.
	// A date tomorrow is a scheduled release; a date next year is a typo -- and where that line falls is
	// policy, so it is a field on the Service rather than a constant in the SQL.
	DefaultDaysAhead = 30

	// DefaultMinSide is the shorter side below which a performer picture is not usable as one. 300 is
	// the width of a thumbnail: below it the image cannot fill a card, which is the whole job.
	DefaultMinSide = 300
)

// Candidate is one thing for a curator to fix: an entity, and the detail that says why.
//
// Detail is a SENTENCE, assembled by the query, and it is deliberately not a struct of parts: every
// detector's finding is a different shape of fact ("this URL is on 3 scenes", "this alias is also
// somebody's name", "this date precedes a birthdate"), and a common struct would either be a union of
// every detector's fields or a bag of strings with no reader. The board renders it; a client that
// needs to branch on it should branch on Slug, which is stable, rather than parse the sentence.
type Candidate struct {
	EntityType string    `json:"entity_type"`
	EntityID   uuid.UUID `json:"entity_id"`
	// EntityName is denormalised in for the same reason internal/service/quest denormalises it: a
	// board read next to forty others cannot show an id and be useful.
	EntityName string `json:"entity_name"`
	Detail     string `json:"detail"`
}

// Detector is one registry row's other half.
//
// Run is a closure over a named query rather than a method on this struct, because the mapping is the
// point: a detector IS "this registry slug runs this SQL", and a struct with an if-chain in a Run
// method would move that mapping away from the declaration a reader is looking at.
type Detector struct {
	// Slug is the contract with the registry row and is never renamed: a rename orphans every
	// operator's saved filter and silently stops the detector firing.
	Slug string
	// Description says what is WRONG with the record, not what the query does. A curator decides
	// whether to work a quest from this line.
	Description string
	// EntityType is what a candidate's id points at.
	EntityType string
	// BountyPoints is what a candidate is worth when this detector seeds its registry row. It is
	// INTERNAL trust points, never money (see §7.24.3's audit row: this schema has no currency column).
	BountyPoints int

	run func(ctx context.Context, q queries.Querier, f filter) ([]Candidate, error)
}

// filter is what every detector's SQL takes: a row cap, an optional entity filter, and (for the
// detectors that need one) a threshold that is policy rather than structure.
type filter struct {
	Limit     int32
	EntityIDs []uuid.UUID
	// DaysAhead and MinSide are only read by the detectors that declare they use them; a detector
	// whose SQL does not take a threshold ignores the zero value.
	DaysAhead int
	MinSide   int
}

// Service runs detectors. It holds a queries.Querier rather than a pool so a test can hand it a fake
// and ask the questions that have nothing to do with SQL (which row is refused, what a candidate
// carries), while the SQL itself is tested against a real database where its joins mean something.
type Service struct {
	q queries.Querier

	Limit     int32
	DaysAhead int
	MinSide   int
}

// ErrUnknownDetector is returned when a caller asks for a slug this build does not implement.
var ErrUnknownDetector = errors.New("no detector implements this slug")

func NewService(q queries.Querier) *Service {
	return &Service{q: q, Limit: DefaultLimit, DaysAhead: DefaultDaysAhead, MinSide: DefaultMinSide}
}

// Builtins returns the shipped detectors. The ORDER is the order a board shows them, and it is by
// consequence rather than alphabet: a duplicated identity is the finding that makes everything else
// wrong (a URL pointing at the wrong person, a score computed for a record that should not exist),
// so it is first.
func Builtins() []Detector {
	return []Detector{
		{
			Slug:         "duplicate_performer_url",
			Description:  "Two performers record the same URL -- usually one person entered twice.",
			EntityType:   string(completion.EntityPerformer),
			BountyPoints: 10,
			run:          runDuplicatePerformerURLs,
		},
		{
			Slug:         "duplicate_scene_url",
			Description:  "Two scenes record the same URL -- usually one scene catalogued twice.",
			EntityType:   string(completion.EntityScene),
			BountyPoints: 10,
			run:          runDuplicateSceneURLs,
		},
		{
			Slug:         "performer_alias_collision",
			Description:  "A performer's alias is another performer's name or alias.",
			EntityType:   string(completion.EntityPerformer),
			BountyPoints: 8,
			run:          runAliasCollisions,
		},
		{
			Slug:         "impossible_scene_date",
			Description:  "A scene is dated in the future, or before one of its performers was born.",
			EntityType:   string(completion.EntityScene),
			BountyPoints: 5,
			run:          runImpossibleDates,
		},
		{
			Slug:         "low_resolution_performer_image",
			Description:  "A performer's picture is too small to use as one.",
			EntityType:   string(completion.EntityPerformer),
			BountyPoints: 3,
			run:          runLowResolutionImages,
		},
	}
}

// EnsureBuiltins seeds a registry row for every builtin that does not have one, and reports how many
// it inserted.
//
// DO NOTHING rather than DO UPDATE, in the SQL and therefore here: a row an operator has disabled or
// re-priced is an operator decision, and re-seeding it on every start would undo that silently --
// which is the failure mode the `is_builtin`/`authored_by` CHECK in migration 98 exists to make
// visible in the first place.
func (s *Service) EnsureBuiltins(ctx context.Context) (int, error) {
	inserted := 0
	for _, d := range Builtins() {
		err := s.insertBuiltin(ctx, d)
		switch {
		case err == nil:
			inserted++
		case errors.Is(err, errNoRow):
			// Already seeded. The row is the operator's now.
		default:
			return inserted, fmt.Errorf("seeding detector %q: %w", d.Slug, err)
		}
	}
	return inserted, nil
}

// errNoRow is what an ON CONFLICT DO NOTHING insert reports when it inserted nothing, mapped from
// pgx's ErrNoRows by the queries layer. Kept as our own name so the case is legible at the call site:
// "nothing happened, and that is the expected outcome".
var errNoRow = errors.New("no row")

// Validate refuses to let the instance serve a registry that disagrees with this build.
//
// TWO DIRECTIONS, and the second is the one that is usually missing:
//
//   - An ENABLED row whose slug names no detector is refused, BY NAME, with the list of slugs the
//     build does know. This is migration 98's stated contract.
//   - A DISABLED row whose slug names no detector is allowed, and is the normal state of a detector
//     this instance retired: the row is history and refusing to start over it would make a rollback
//     impossible. (An AUTHORED row is a different thing again -- a local rule with no SQL -- and this
//     package does not run those at all; see the note in Builtins' caller.)
func (s *Service) Validate(ctx context.Context) error {
	defs, err := s.Definitions(ctx)
	if err != nil {
		return fmt.Errorf("reading the lint registry: %w", err)
	}

	known := map[string]bool{}
	for _, d := range Builtins() {
		known[d.Slug] = true
	}

	var broken []string
	for _, def := range defs {
		if !def.Enabled {
			continue
		}
		if !known[def.Slug] {
			broken = append(broken, def.Slug)
		}
	}
	if len(broken) > 0 {
		sort.Strings(broken)
		return fmt.Errorf("%w: enabled detector(s) %v have no implementation in this build (it implements %v)",
			ErrUnknownDetector, broken, Slugs())
	}
	return nil
}

// Slugs returns every slug this build implements, sorted, for an error message a reader can act on.
func Slugs() []string {
	out := make([]string, 0, len(Builtins()))
	for _, d := range Builtins() {
		out = append(out, d.Slug)
	}
	sort.Strings(out)
	return out
}

// detectorFor finds the implementation for a slug.
func detectorFor(slug string) (Detector, bool) {
	for _, d := range Builtins() {
		if d.Slug == slug {
			return d, true
		}
	}
	return Detector{}, false
}

// Definitions returns the registry rows as they are, for an operator surface that lists what this
// instance has, including the rows this build cannot run.
func (s *Service) Definitions(ctx context.Context) ([]queries.LintQuestDefinition, error) {
	return s.q.ListLintQuestDefinitions(ctx)
}

// Report is one detector's answer: what it is, and what it found.
type Report struct {
	Slug         string      `json:"slug"`
	Description  string      `json:"description"`
	EntityType   string      `json:"entity_type"`
	BountyPoints int         `json:"bounty_points"`
	Candidates   []Candidate `json:"candidates"`
	// Count is len(Candidates) and exists so that a zero is legible: an empty slice marshals as `[]`
	// and a caller reading JSON should not have to know whether that means "nothing found" or "the
	// field was omitted".
	Count int `json:"count"`
	// LimitNotice is set when the detector filled its limit, which is a fact a reader needs: a board
	// showing exactly 5 of a detector that found 4,000 is a board that looks like the archive is fine.
	LimitNotice string `json:"limit_notice,omitempty"`
}

// Board runs every ENABLED detector this build implements and returns one Report each.
//
// A registry row that is enabled but unimplemented is SKIPPED here rather than reported as an error,
// because Validate is what refuses those (and the instance does not start without it). Doing both
// would mean two places decide what a broken registry means.
func (s *Service) Board(ctx context.Context, limit int32) ([]Report, error) {
	defs, err := s.q.ListEnabledLintQuestDefinitions(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading enabled detectors: %w", err)
	}

	reports := make([]Report, 0, len(defs))
	for _, def := range defs {
		d, ok := detectorFor(def.Slug)
		if !ok {
			continue
		}
		rep, err := s.run(ctx, d, def, filter{Limit: s.clamp(limit), DaysAhead: s.DaysAhead, MinSide: s.MinSide})
		if err != nil {
			// One detector failing does not hide the others: a board that collapses because one
			// query timed out is a board nobody can use to find out which one did.
			reports = append(reports, Report{
				Slug: def.Slug, Description: def.Description, EntityType: d.EntityType,
				BountyPoints: def.BountyPoints,
				Candidates:   []Candidate{}, Count: 0,
				LimitNotice: fmt.Sprintf("this detector failed: %v", err),
			})
			continue
		}
		reports = append(reports, rep)
	}
	return reports, nil
}

// Candidates runs ONE detector by slug, whether or not its registry row is enabled: a curator who
// asks a specific question gets an answer, and "disabled" governs the board, not the question.
func (s *Service) Candidates(ctx context.Context, slug string, limit int32) (Report, error) {
	d, ok := detectorFor(slug)
	if !ok {
		return Report{}, fmt.Errorf("%w: %q (this build implements %v)", ErrUnknownDetector, slug, Slugs())
	}
	def := queries.LintQuestDefinition{
		Slug:         d.Slug,
		Description:  d.Description,
		BountyPoints: d.BountyPoints,
		Enabled:      true,
	}
	if row, err := s.q.GetLintQuestDefinition(ctx, slug); err == nil {
		// The registry is authoritative for what a curator sees and what it is worth; this build is
		// authoritative for what the detector DOES.
		def = row
	}
	return s.run(ctx, d, def, filter{Limit: s.clamp(limit), DaysAhead: s.DaysAhead, MinSide: s.MinSide})
}

// ValidateEntities is the import validation pass: which of these entities, right now, look wrong.
//
// It answers about the entities it is GIVEN and about nothing else -- the filter is in the SQL, so an
// archive with ten thousand findings cannot push an imported entity's finding past a limit.
func (s *Service) ValidateEntities(ctx context.Context, refs []EntityRef) ([]Report, error) {
	if len(refs) == 0 {
		return nil, nil
	}

	byType := map[string][]uuid.UUID{}
	for _, r := range refs {
		byType[r.EntityType] = append(byType[r.EntityType], r.EntityID)
	}

	ids := make([]uuid.UUID, 0, len(refs))
	for _, r := range refs {
		ids = append(ids, r.EntityID)
	}

	var reports []Report
	for _, d := range Builtins() {
		typed := byType[d.EntityType]
		if len(typed) == 0 {
			continue
		}
		// The filter is the ids of THIS detector's entity type: passing performers to a scene detector
		// would match nothing and read as "clean" rather than as "not asked".
		def := queries.LintQuestDefinition{
			Slug: d.Slug, Description: d.Description,
			BountyPoints: d.BountyPoints, Enabled: true,
		}
		rep, err := s.run(ctx, d, def, filter{
			Limit:     s.clamp(int32(len(typed)) * 10),
			EntityIDs: typed,
			DaysAhead: s.DaysAhead,
			MinSide:   s.MinSide,
		})
		if err != nil {
			return nil, fmt.Errorf("validating %d %s(s) against %s: %w", len(typed), d.EntityType, d.Slug, err)
		}
		rep.Candidates = dedupe(rep.Candidates)
		rep.Count = len(rep.Candidates)
		reports = append(reports, rep)
	}
	return reports, nil
}

// EntityRef is one entity an import touched.
type EntityRef struct {
	EntityType string
	EntityID   uuid.UUID
}

// dedupe drops repeated (entity, detail) pairs. A detector whose SQL joins to a many-side (a scene
// with three performers, each of whom is too young) legitimately returns the same finding three
// times, and a board that showed it three times would overstate the work.
func dedupe(in []Candidate) []Candidate {
	if len(in) < 2 {
		return in
	}
	seen := map[string]bool{}
	out := in[:0]
	for _, c := range in {
		key := c.EntityID.String() + "\x00" + c.Detail
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, c)
	}
	return out
}

// run executes one detector and shapes its answer.
func (s *Service) run(ctx context.Context, d Detector, def queries.LintQuestDefinition, f filter) (Report, error) {
	started := time.Now()
	cands, err := d.run(ctx, s.q, f)
	if err != nil {
		return Report{}, err
	}

	rep := Report{
		Slug:         d.Slug,
		Description:  def.Description,
		EntityType:   d.EntityType,
		BountyPoints: int(def.BountyPoints),
		Candidates:   cands,
		Count:        len(cands),
	}
	if rep.Description == "" {
		rep.Description = d.Description
	}
	// A detector that returns exactly its limit is reported as POSSIBLY TRUNCATED rather than as
	// "found this many", because a board cannot tell the two apart and a reader must not be told the
	// archive is in better shape than it is. (`== limit` and not `> limit` because the query stops at
	// the limit; the flag is a statement about what is knowable, not about what happened.)
	if int(f.Limit) > 0 && len(cands) == int(f.Limit) {
		rep.LimitNotice = fmt.Sprintf("at least %d finding(s): this detector stopped at its limit of %d", len(cands), f.Limit)
	}
	_ = started
	return rep, nil
}

// clamp keeps a caller's limit inside what one query may ask for.
func (s *Service) clamp(limit int32) int32 {
	if limit <= 0 {
		limit = s.Limit
	}
	if limit > HardLimit {
		limit = HardLimit
	}
	return limit
}

func (s *Service) insertBuiltin(ctx context.Context, d Detector) error {
	_, err := s.q.InsertBuiltinLintQuestDefinition(ctx, queries.InsertBuiltinLintQuestDefinitionParams{
		Slug:         d.Slug,
		Description:  d.Description,
		BountyPoints: d.BountyPoints,
	})
	// ON CONFLICT DO NOTHING reports "I inserted nothing" as pgx.ErrNoRows, and the two callers above
	// are the reason this is translated rather than compared: `errors.Is(err, pgx.ErrNoRows)` at the
	// call site reads as "the database did not answer", when what happened is "the row is already
	// there and that is the expected outcome". Found by writing the test for the SECOND start.
	if errors.Is(err, pgx.ErrNoRows) {
		return errNoRow
	}
	return err
}

// --- the detectors themselves, one closure per named query ---------------------
//
// Each one is three lines: call the query, map its rows onto Candidate, return. The mapping is worth
// naming rather than hiding in the SQL: every query returns `entity_id, entity_name, detail`, and the
// entity TYPE is the detector's, not the row's -- a row cannot know whether its id points at a
// performer or a scene, and a query that returned the type as a literal string would let the two
// disagree.

func runDuplicateSceneURLs(ctx context.Context, q queries.Querier, f filter) ([]Candidate, error) {
	rows, err := q.LintDuplicateSceneURLs(ctx, queries.LintDuplicateSceneURLsParams{Limit: f.Limit, EntityIds: f.EntityIDs})
	if err != nil {
		return nil, err
	}
	out := make([]Candidate, 0, len(rows))
	for _, r := range rows {
		name := ""
		if r.EntityName != nil {
			name = *r.EntityName
		}
		out = append(out, Candidate{
			EntityType: string(completion.EntityScene),
			EntityID:   r.EntityID,
			EntityName: name,
			Detail:     fmt.Sprintf("%s is recorded on %d scenes", r.Detail, r.SharedWith),
		})
	}
	return out, nil
}

func runDuplicatePerformerURLs(ctx context.Context, q queries.Querier, f filter) ([]Candidate, error) {
	rows, err := q.LintDuplicatePerformerURLs(ctx, queries.LintDuplicatePerformerURLsParams{Limit: f.Limit, EntityIds: f.EntityIDs})
	if err != nil {
		return nil, err
	}
	out := make([]Candidate, 0, len(rows))
	for _, r := range rows {
		out = append(out, Candidate{
			EntityType: string(completion.EntityPerformer),
			EntityID:   r.EntityID,
			EntityName: r.EntityName,
			Detail:     fmt.Sprintf("%s is recorded on %d performers", r.Detail, r.SharedWith),
		})
	}
	return out, nil
}

func runAliasCollisions(ctx context.Context, q queries.Querier, f filter) ([]Candidate, error) {
	rows, err := q.LintPerformerAliasCollisions(ctx, queries.LintPerformerAliasCollisionsParams{Limit: f.Limit, EntityIds: f.EntityIDs})
	if err != nil {
		return nil, err
	}
	out := make([]Candidate, 0, len(rows))
	for _, r := range rows {
		out = append(out, Candidate{
			EntityType: string(completion.EntityPerformer),
			EntityID:   r.EntityID,
			EntityName: r.EntityName,
			Detail:     r.Detail,
		})
	}
	return out, nil
}

func runImpossibleDates(ctx context.Context, q queries.Querier, f filter) ([]Candidate, error) {
	rows, err := q.LintImpossibleSceneDates(ctx, queries.LintImpossibleSceneDatesParams{
		Limit: f.Limit, DaysAhead: f.DaysAhead, EntityIds: f.EntityIDs,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Candidate, 0, len(rows))
	for _, r := range rows {
		out = append(out, Candidate{
			EntityType: string(completion.EntityScene),
			EntityID:   r.EntityID,
			EntityName: r.EntityName,
			Detail:     r.Detail,
		})
	}
	return out, nil
}

func runLowResolutionImages(ctx context.Context, q queries.Querier, f filter) ([]Candidate, error) {
	rows, err := q.LintLowResolutionPerformerImages(ctx, queries.LintLowResolutionPerformerImagesParams{
		Limit: f.Limit, MinSide: f.MinSide, EntityIds: f.EntityIDs,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Candidate, 0, len(rows))
	for _, r := range rows {
		out = append(out, Candidate{
			EntityType: string(completion.EntityPerformer),
			EntityID:   r.EntityID,
			EntityName: r.EntityName,
			Detail:     fmt.Sprintf("%s, below the %dpx minimum", r.Detail, f.MinSide),
		})
	}
	return out, nil
}
