package sdbimport

// The durable half of the incremental sync (plan: docs/plan/feature-sync-service.md, step 2).
//
// `Watermarks` has been a Go map since the reconcile was written, which makes "since last sync" a property of
// a process rather than of the instance: a restart, a crash, or the next timer tick starts from zero. This file
// gives it a store. Everything below is about the three ways a mark can lie.
//
// ONE: A MARK THAT MOVED PAST RECORDS THAT WERE NEVER WRITTEN. The mark is saved once per entity, AFTER that
// entity's walk returns -- never mid-walk, and never at the start. A run that is interrupted must replay, and
// replaying is free: the diff is idempotent by construction. Skipping is not free, because nothing ever goes
// back.
//
// TWO: A MARK THAT REWINDS. An older value overwriting a newer one happens when a second process runs with a
// stale map in hand -- a manual run beside a timer run, which is exactly what the lock in the runner exists to
// prevent but cannot prevent across a crash. The store refuses to move a mark backwards, in SQL rather than in
// Go, so the guarantee holds for every writer including one written later.
//
// THREE: TWO SOURCES SHARING ONE MARK. The key is (entity, source). Whichever source ran second would read the
// other's position and skip everything older, silently, forever.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// WatermarkDB is the small slice of the pool this store needs, so a caller can hand it a *pgxpool.Pool and a
// test can hand it anything that satisfies the same three methods.
type WatermarkDB interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// ErrUnknownSyncEntity is returned for an entity name the sync does not walk.
//
// A refusal rather than a new row: `performers ` with a trailing space, or `performer`, would otherwise create
// a SECOND mark that no later run ever reads -- so the entity would re-walk from the beginning on every run
// while a stale row sat beside it looking like state.
var ErrUnknownSyncEntity = errors.New("unknown sync entity")

// SyncEntities is the set of entities the sync walks, in the order it walks them. The order is the importer's
// own (a scene resolves its performers), and this list is the one place both use.
var SyncEntities = []string{"tags", "sites", "studios", "performers", "scenes"}

// IsSyncEntity reports whether the sync walks this entity.
func IsSyncEntity(entity string) bool {
	for _, e := range SyncEntities {
		if e == entity {
			return true
		}
	}
	return false
}

// LoadWatermarks reads every mark this source has.
//
// An entity with no row is ABSENT from the map rather than present and zero, and that is the runner's cue to
// walk it in full. Keeping the two states distinct matters: `Get` returns the zero time for both, so a caller
// that needs to tell "never synced" from "synced to the beginning of time" has to ask the map, and the map
// answers.
func LoadWatermarks(ctx context.Context, db WatermarkDB, source string) (Watermarks, error) {
	if err := validateSource(source); err != nil {
		return nil, err
	}
	rows, err := db.Query(ctx, `SELECT entity, watermark FROM sync_watermarks WHERE source = $1`, source)
	if err != nil {
		return nil, fmt.Errorf("reading sync watermarks for %s: %w", source, err)
	}
	defer rows.Close()

	w := Watermarks{}
	for rows.Next() {
		var entity string
		var at time.Time
		if err := rows.Scan(&entity, &at); err != nil {
			return nil, err
		}
		if !IsSyncEntity(entity) {
			// A row for an entity this build no longer walks: kept in the map so a run does not delete it,
			// and reported rather than hidden, because it is the only trace that the walk used to be wider.
			w.Set("?"+entity, at)
			continue
		}
		w.Set(entity, at)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return w, nil
}

// SaveWatermark records that an entity's walk completed with this high-water mark.
//
// IT NEVER MOVES A MARK BACKWARDS, and that is enforced in the statement rather than around it: the `WHERE`
// clause on the conflict branch means a stale writer's value is ignored instead of applied. The row count comes
// back so a caller can say whether the write landed -- an UPDATE that changes nothing is indistinguishable
// from one that worked if the caller only checks for an error.
func SaveWatermark(ctx context.Context, db WatermarkDB, source, entity string, at time.Time) (bool, error) {
	if err := validateSource(source); err != nil {
		return false, err
	}
	if !IsSyncEntity(entity) {
		return false, fmt.Errorf("%w: %q (the sync walks %s)", ErrUnknownSyncEntity, entity, strings.Join(SyncEntities, ", "))
	}
	if at.IsZero() {
		return false, errors.New("refusing to save a zero watermark: a run that read nothing has no position, " +
			"and saving one would look like a completed walk that found the source empty")
	}
	tag, err := db.Exec(ctx, `
		INSERT INTO sync_watermarks (entity, source, watermark, updated_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (entity, source) DO UPDATE
		   SET watermark = EXCLUDED.watermark, updated_at = now()
		 WHERE sync_watermarks.watermark < EXCLUDED.watermark`,
		entity, source, at)
	if err != nil {
		return false, fmt.Errorf("saving the %s watermark for %s: %w", entity, source, err)
	}
	return tag.RowsAffected() > 0, nil
}

// WatermarkFor is LoadWatermarks for one entity, for the runner's per-entity step.
func WatermarkFor(ctx context.Context, db WatermarkDB, source, entity string) (time.Time, bool, error) {
	if !IsSyncEntity(entity) {
		return time.Time{}, false, fmt.Errorf("%w: %q", ErrUnknownSyncEntity, entity)
	}
	var at time.Time
	err := db.QueryRow(ctx, `SELECT watermark FROM sync_watermarks WHERE source = $1 AND entity = $2`,
		source, entity).Scan(&at)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	return at, true, nil
}

// SourceOf derives the watermark key from the configured source endpoint: the host, so that
// https://stashdb.org/graphql and https://stashdb.org/graphql/ are the same source and a second endpoint on the
// same host is not silently a different one.
func SourceOf(endpoint string) (string, error) {
	e := strings.TrimSpace(endpoint)
	if e == "" {
		return "", errors.New("no source endpoint configured, so there is no watermark key to use")
	}
	host := e
	for _, prefix := range []string{"https://", "http://"} {
		host = strings.TrimPrefix(host, prefix)
	}
	if i := strings.IndexAny(host, "/?#"); i >= 0 {
		host = host[:i]
	}
	if host == "" {
		return "", fmt.Errorf("the source endpoint %q has no host", endpoint)
	}
	return host, nil
}

func validateSource(source string) error {
	if strings.TrimSpace(source) == "" {
		return errors.New("a watermark needs a source: without one the key is (entity, <empty>) and the first " +
			"second source would share this one's marks")
	}
	return nil
}
