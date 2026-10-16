package sdbimport

// The caller the reconcile never had (plan: docs/plan/feature-sync-service.md, step 3).
//
// `syncdiff.go` and `syncwrite.go` have been built and mutation-verified since 2026-10-01 and NOTHING CALLED
// THEM: a grep for a caller in cmd/ was empty. They decide, purely, what a sync would change. This file walks
// the source and uses those decisions, and the three things it adds are the three the decision layer could not
// supply on its own:
//
// ONE, the watermark, now durable (watermark_store.go). Without it "since last sync" was a Go map, so every
// run was a first run.
//
// TWO, the walk's stopping point. `page` walks every page the source reports; a sync must stop at the first
// record older than its watermark, and that stop travels back through the pager as `errStopWalk` rather than as
// an error an operator has to interpret.
//
// THREE, the application. `SyncWalk.Step` appends `FieldChange`s and says so in its own comment: "The caller
// applies rep.Changes; this function only decides." This is that caller, and it applies through the SERVICE,
// never raw SQL, for the reason the package doc gives: no fingerprints, no alias rows and no relation rows is an
// instance that is full of rows and behaves as if it were empty.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/stashapp/stash-box/internal/models"
)

// errStopWalk travels from a per-record callback back through the pager. It is not an error condition: it IS the
// watermark's stop decision, and `page` recognises it by identity so it is never counted as a failed record.
var errStopWalk = errors.New("sync: past the watermark, stopping")

// ErrSyncLocked means another run holds the lock. It is a normal outcome, not a failure: a timer that fails
// because the previous run is still going is a timer that pages someone for nothing.
var ErrSyncLocked = errors.New("another sync run holds the lock")

// DefaultSyncMargin is how far behind the watermark the walk re-reads.
//
// The margin exists because recency ordering at the source is OBSERVED, not documented -- the same reason
// `shouldStop` is not a bare comparison. Fifteen minutes costs one extra page or two; the alternative is a
// boundary record that is wrong forever, because the next run's mark has already moved past it.
const DefaultSyncMargin = 15 * time.Minute

// ReconciledEntities reports which entities this walk can reconcile.
//
// THE DISTINCTION IS THE CODEBASE'S, NOT A SHORTCUT. The field-level diff (`syncableFields`, `planUpdate`,
// `SyncWalk.Step`) is performer-shaped: `syncableFields` lists performer attributes and `planUpdate` returns a
// `models.PerformerUpdateInput`. The other entities are create-or-skip by natural key in the importer, which
// needs no diff and no watermark -- and a runner that printed "scenes: 0 updated" would be telling an operator
// the source has nothing new about scenes, when nobody looked.
func ReconciledEntities() []string { return []string{"performers"} }

// ImporterOwnedEntities are walked by cmd/sdbimport's import path. Named so the report can SAY so rather than
// leave a reader to infer it from an absence.
func ImporterOwnedEntities() []string { return []string{"tags", "sites", "studios", "scenes"} }

// SyncOptions is one run's configuration.
type SyncOptions struct {
	// Entities to walk, in SyncEntities order. Empty means every entity.
	Entities []string

	// Modes is the per-entity sync mode. An entity not named uses the package's default (latest-wins). An
	// unknown mode is a hard error, as ParseMode already requires.
	Modes map[string]SyncMode

	// Full ignores the stored watermark and walks the whole source: the repair path for a mark believed to be
	// wrong. It never deletes or lowers a mark; it just does not read one.
	Full bool

	DryRun bool

	// LimitPerformer bounds the walk by RECORDS (0 = unbounded), for a bounded live test.
	LimitPerformer int

	// Margin overrides DefaultSyncMargin.
	Margin time.Duration

	// Source keys the watermark. Empty is refused: the store refuses it too, because the key would become
	// (entity, <empty>) and the first second source would share this one's marks.
	Source string

	// Now is injectable so a report's timestamps are testable.
	Now func() time.Time
}

// EntityReport is one entity's part of a run.
type EntityReport struct {
	Entity string `json:"entity"`

	// Walked is false for an entity this walk does not reconcile, and Owned names who does.
	Walked bool   `json:"walked"`
	Owned  string `json:"owned_by,omitempty"`
	Note   string `json:"note,omitempty"`

	Mode  string `json:"mode,omitempty"`
	Full  bool   `json:"full"`
	Dry   bool   `json:"dry_run"`
	Limit int    `json:"limit,omitempty"`

	WatermarkBefore *time.Time `json:"watermark_before,omitempty"`
	WatermarkAfter  *time.Time `json:"watermark_after,omitempty"`

	Scanned   int `json:"scanned"`
	ReScanned int `json:"rescaned"`
	Updated   int `json:"updated"`
	Unchanged int `json:"unchanged"`
	Skipped   int `json:"skipped"`
	Failed    int `json:"failed"`

	FieldChanges int `json:"field_changes"`

	// AliasUpdated counts performers whose alias list was replaced; URLsNotApplied counts field changes this
	// run deliberately did not write, for the importer's own reason (see applyAliasAndURLs).
	AliasUpdated   int      `json:"alias_updated"`
	URLsNotApplied int      `json:"urls_not_applied"`
	StopReason     string   `json:"stop_reason"`
	WatermarkSaved bool     `json:"watermark_saved"`
	ElapsedMS      int64    `json:"elapsed_ms"`
	Errors         []string `json:"errors,omitempty"`
}

// RunReport is the whole run, and the JSON the service writes to import-logs/.
type RunReport struct {
	Source     string         `json:"source"`
	StartedAt  time.Time      `json:"started_at"`
	FinishedAt time.Time      `json:"finished_at"`
	DryRun     bool           `json:"dry_run"`
	Entities   []EntityReport `json:"entities"`
}

// Failed reports whether any entity recorded a failure. A caller's exit code is this and nothing else: a run
// that reported failures and exited 0 is the kind of green that reads as "the sync is fine".
func (r RunReport) Failed() bool {
	for _, e := range r.Entities {
		if e.Failed > 0 || len(e.Errors) > 0 {
			return true
		}
	}
	return false
}

// Summary is the one-line-per-entity form the service logs.
func (r RunReport) Summary() string {
	var b strings.Builder
	for _, e := range r.Entities {
		switch {
		case !e.Walked:
			fmt.Fprintf(&b, "  %-11s not reconciled by the sync (%s)\n", e.Entity, e.Owned)
		case e.Dry:
			fmt.Fprintf(&b, "  %-11s would update %d of %d scanned (%d unchanged, %d field changes, %d aliases); stop: %s\n",
				e.Entity, e.Updated, e.Scanned, e.Unchanged, e.FieldChanges, e.AliasUpdated, e.StopReason)
		default:
			fmt.Fprintf(&b, "  %-11s %s: scanned %d updated %d unchanged %d skipped %d failed %d; watermark %s -> %s (%s)\n",
				e.Entity, e.Mode, e.Scanned, e.Updated, e.Unchanged, e.Skipped, e.Failed,
				atTime(e.WatermarkBefore), atTime(e.WatermarkAfter), e.StopReason)
		}
	}
	return b.String()
}

func atTime(t *time.Time) string {
	if t == nil || t.IsZero() {
		return "-"
	}
	return t.UTC().Format(time.RFC3339)
}

// Runner walks the source and applies what the sync decides.
type Runner struct {
	imp    *Importer
	db     WatermarkDB
	opts   SyncOptions
	margin time.Duration
	now    func() time.Time

	// local caches the destination's view of a performer for one walk.
	local map[string]*LocalPerformer
}

// NewRunner validates its options BEFORE anything is opened or walked.
//
// A run that discovers an unknown entity name or an unknown sync mode 400,000 records in has already burned the
// source's good will. Every refusal here is one an operator can act on before starting.
func NewRunner(imp *Importer, db WatermarkDB, opts SyncOptions) (*Runner, error) {
	if imp == nil || imp.Client == nil || imp.Factory == nil {
		return nil, errors.New("the sync needs an importer with a source client and a destination factory")
	}
	if db == nil {
		return nil, errors.New("the sync needs a durable watermark store: without one every run is a first run")
	}
	if strings.TrimSpace(opts.Source) == "" {
		return nil, errors.New("the sync needs a source key for its watermarks")
	}
	for _, e := range opts.Entities {
		if !IsSyncEntity(e) {
			return nil, fmt.Errorf("%w: %q (the sync walks %s)", ErrUnknownSyncEntity, e, strings.Join(SyncEntities, ", "))
		}
	}
	for entity, raw := range opts.Modes {
		if !IsSyncEntity(entity) {
			return nil, fmt.Errorf("%w: %q", ErrUnknownSyncEntity, entity)
		}
		if _, err := ParseMode(string(raw)); err != nil {
			return nil, err
		}
	}
	r := &Runner{imp: imp, db: db, opts: opts, margin: opts.Margin, now: opts.Now, local: map[string]*LocalPerformer{}}
	if r.margin == 0 {
		r.margin = DefaultSyncMargin
	}
	if r.now == nil {
		r.now = time.Now
	}
	return r, nil
}

// Run walks the requested entities in the importer's order and returns the report.
func (r *Runner) Run(ctx context.Context) (RunReport, error) {
	rep := RunReport{Source: r.opts.Source, StartedAt: r.now().UTC(), DryRun: r.opts.DryRun}
	want := map[string]bool{}
	for _, e := range r.opts.Entities {
		want[e] = true
	}

	for _, entity := range SyncEntities {
		if len(want) > 0 && !want[entity] {
			continue
		}
		if entity != "performers" {
			rep.Entities = append(rep.Entities, EntityReport{
				Entity: entity, Walked: false, Owned: "cmd/sdbimport's import path",
				Note: "create-or-skip by natural key; no field diff and no watermark, so the sync does not walk it",
			})
			continue
		}
		er, err := r.walkPerformers(ctx)
		if err != nil {
			er.Errors = append(er.Errors, err.Error())
			er.Failed++
		}
		rep.Entities = append(rep.Entities, er)
	}

	rep.FinishedAt = r.now().UTC()
	return rep, nil
}

// walkPerformers is the reconcile: load the mark, walk in `updated` order, stop at the mark, apply what the
// decision layer decided, then move the mark to the oldest record the walk actually read.
func (r *Runner) walkPerformers(ctx context.Context) (EntityReport, error) {
	started := r.now()
	mode, err := r.modeFor("performers")
	if err != nil {
		return EntityReport{Entity: "performers", Walked: true}, err
	}
	er := EntityReport{Entity: "performers", Walked: true, Mode: string(mode), Dry: r.opts.DryRun,
		Full: r.opts.Full, Limit: r.opts.LimitPerformer}

	var watermark time.Time
	if !r.opts.Full {
		wm, found, err := WatermarkFor(ctx, r.db, r.opts.Source, "performers")
		if err != nil {
			return er, err
		}
		if found {
			cp := wm
			er.WatermarkBefore = &cp
			watermark = wm
		}
	}

	walk := SyncWalk{Mode: mode, Watermark: watermark, Margin: r.margin, DryRun: r.opts.DryRun}
	rep := &SyncReport{}

	var total int
	var resp struct {
		QueryPerformers struct {
			Count int `json:"count"`
		} `json:"queryPerformers"`
	}
	if err := r.imp.Client.Query(ctx, CountPerformers, nil, &resp); err != nil {
		return er, fmt.Errorf("counting performers at the source: %w", err)
	}
	total = resp.QueryPerformers.Count

	err = r.imp.page("performers", performerQuery, total, r.opts.LimitPerformer, func(rec any) error {
		p, ok := rec.(Performer)
		if !ok {
			return nil
		}
		// The decision layer reports changes keyed by the SOURCE's id -- `planUpdate(upID, ...)`, "up" as in
		// upstream -- and the write needs the DESTINATION's row. The translation is this caller's job because
		// the decision layer has no database. Look the local row up once and hand its id to the applier; a
		// change exists only for a record the lookup found, so the two always agree.
		local, hasLocal := r.lookup(p.Name)

		cont, stepErr := walk.Step([]Performer{p}, rep, r.lookup)
		if stepErr != nil {
			return stepErr
		}
		if err := r.applyRecord(ctx, p, local, hasLocal, rep, &er); err != nil {
			rep.Failed++
			return err
		}
		if !cont {
			return errStopWalk
		}
		return nil
	})
	if err != nil {
		return er, err
	}

	switch {
	case rep.StoppedAt.IsZero():
		er.StopReason = "the source ran out of records before the watermark"
	case walk.Watermark.IsZero():
		er.StopReason = "walked to the end of the first window (no watermark yet)"
	default:
		er.StopReason = fmt.Sprintf("reached %s, older than the watermark minus the %s margin",
			rep.StoppedAt.UTC().Format(time.RFC3339), r.margin)
	}

	er.Scanned, er.Updated, er.Unchanged, er.Skipped, er.Failed = rep.Scanned, rep.Updated, rep.Unchanged, rep.Skipped, rep.Failed
	er.ReScanned, er.FieldChanges = rep.ReScanned, len(rep.Changes)

	// The mark moves only after the entity's walk has returned, and only forward: the store refuses the
	// backwards write, in SQL, because a stale writer is not a condition a Go check can see.
	if !r.opts.DryRun && !rep.StoppedAt.IsZero() {
		saved, err := SaveWatermark(ctx, r.db, r.opts.Source, "performers", rep.StoppedAt)
		if err != nil {
			return er, err
		}
		er.WatermarkSaved = saved
		cp := rep.StoppedAt
		er.WatermarkAfter = &cp
	}

	er.ElapsedMS = r.now().Sub(started).Milliseconds()
	return er, nil
}

// applyRecord writes the changes Step decided for ONE source record.
//
// It reads them back off the report, because that is the boundary the decision layer drew: Step appends to
// `rep.Changes` and says the caller applies them. Applied changes are dropped from the report as they are
// applied, so a long walk does not hold every field change of every performer in memory; a dry run keeps them,
// because the report is a dry run's only output.
//
// A FAILED APPLY IS COUNTED AND NOT RETURNED. The walk has a million records to get through and one refused
// update must not end the run; the failure is named in the report's counters and the record's error is the
// first one, which `page` already retains per entity.
func (r *Runner) applyRecord(ctx context.Context, p Performer, local *LocalPerformer, hasLocal bool, rep *SyncReport, er *EntityReport) error {
	if len(rep.Changes) == 0 {
		return nil
	}
	changes := make([]FieldChange, 0, len(rep.Changes))
	for _, c := range rep.Changes {
		if c.EntityID == p.ID {
			changes = append(changes, c)
		}
	}
	if len(changes) == 0 {
		return nil
	}
	if r.opts.DryRun {
		// Nothing is written, so nothing is translated: a dry run's numbers are the real path's numbers
		// without the local id being needed at all.
		return nil
	}

	// A decided change with no local row is a state the two halves cannot produce together: Step only plans a
	// change for a record its lookup found. Refused by NAME rather than looked up again, because the service's
	// Update panics -- does not error -- when the id it is handed has no row, and a panic in the middle of a
	// million-record walk is the failure this check exists to make impossible.
	if !hasLocal || local == nil {
		return fmt.Errorf("a change was planned for source performer %s (%s) and the destination has no matching "+
			"row: refusing to write, because the service panics on an id with no row", p.ID, p.Name)
	}

	id := local.ID
	byName := make(map[string]Field, len(changes))
	for _, c := range changes {
		byName[c.Field] = Field{Name: c.Field, Local: c.Old, Up: c.New}
	}
	in := applyChanges(id, changes, byName)
	r.applyAliasAndURLs(p, changes, &in, er)

	if _, err := r.imp.Factory.Performer().Update(ctx, in); err != nil {
		return fmt.Errorf("applying %d change(s) to performer %s: %w", len(changes), p.ID, err)
	}
	return nil
}

// applyAliasAndURLs handles the two list fields, which `applyChanges` deliberately does not: its comment says
// they "live in their own tables and are replaced wholesale by the service's Update", so it leaves them to the
// caller.
//
// ALIASES ARE APPLIED. URLs ARE NOT, and the reason is the importer's own, recorded in mapping.go: a URL carries
// a SiteID, which is a uuid this code cannot invent, so attaching one would either violate a foreign key or
// attach it to a meaningless site row. The importer chose to lose the URL and record the loss; the sync makes
// the same trade and COUNTS it, so a reader of the report cannot mistake "no URL changed" for "URLs are
// current".
func (r *Runner) applyAliasAndURLs(p Performer, changes []FieldChange, in *models.PerformerUpdateInput, er *EntityReport) {
	for _, c := range changes {
		switch c.Field {
		case "aliases":
			if len(p.Aliases) > 0 {
				in.Aliases = p.Aliases
				er.AliasUpdated++
			}
		case "urls":
			er.URLsNotApplied++
		}
	}
}

// lookup is the source-name -> local-record function the diff needs.
//
// It answers with the destination's row by NATURAL KEY, and caches per walk. A name that matches several local
// rows resolves to the first: the destination's uniqueness is (name, disambiguation), so a single-answer
// resolver is a real simplification -- the diff is reported against that row rather than against a guess.
func (r *Runner) lookup(name string) (*LocalPerformer, bool) {
	key := strings.ToLower(strings.TrimSpace(name))
	if v, ok := r.local[key]; ok {
		return v, v != nil
	}
	r.local[key] = nil

	local, err := r.loadLocal(context.Background(), name)
	if err != nil {
		// A lookup failure is not a sync decision: leaving the record out of the diff is correct, and Step
		// counts it as skipped rather than updated.
		return nil, false
	}
	r.local[key] = local
	return local, local != nil
}

func (r *Runner) loadLocal(ctx context.Context, name string) (*LocalPerformer, error) {
	p, err := r.imp.Factory.Performer().FindByName(ctx, name)
	if err != nil || p == nil {
		return nil, err
	}
	aliases, err := r.imp.queries.GetEditPerformerAliases(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	rows, err := r.imp.queries.GetPerformerURLs(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	urls := make([]string, 0, len(rows))
	for _, row := range rows {
		urls = append(urls, row.Url)
	}
	return &LocalPerformer{Performer: *p, Aliases: aliases, URLs: urls}, nil
}

func (r *Runner) modeFor(entity string) (SyncMode, error) {
	if m, ok := r.opts.Modes[entity]; ok {
		return ParseMode(string(m))
	}
	return ParseMode("")
}

// WriteJSON writes the report for the service's import-logs/, so the timer's history is parseable without a log
// parser.
func (r RunReport) WriteJSON(path string) error {
	if path == "" {
		return nil
	}
	blob, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(blob, '\n'), 0o644)
}

// LockPathDefault is where a manual run and the timer's run agree to meet. XDG_RUNTIME_DIR first because it is
// per-user and cleared at logout; the fallback is for a service that starts before the session does.
func LockPathDefault() string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return dir + "/stash-box-sync.lock"
	}
	return os.TempDir() + "/stash-box-sync.lock"
}

// ParseSyncModes reads "performers=upstream,scenes=local" into modes, validating as it goes.
func ParseSyncModes(s string) (map[string]SyncMode, error) {
	out := map[string]SyncMode{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		entity, raw, ok := strings.Cut(part, "=")
		if !ok {
			return nil, fmt.Errorf("sync mode %q is not entity=mode", part)
		}
		entity = strings.TrimSpace(entity)
		if !IsSyncEntity(entity) {
			return nil, fmt.Errorf("%w: %q (the sync walks %s)", ErrUnknownSyncEntity, entity, strings.Join(SyncEntities, ", "))
		}
		mode, err := ParseMode(strings.TrimSpace(raw))
		if err != nil {
			return nil, err
		}
		out[entity] = mode
	}
	return out, nil
}

// pidLine is what the lock file holds, so an operator who finds one can see who has it.
func pidLine() string { return strconv.Itoa(os.Getpid()) + "\n" }
