package collage

import (
	"errors"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/stashapp/stash-box/internal/queries"
)

// Conversions between the sqlc row types and the package's own types.
//
// Kept in one file because they are all the same concern and all mechanical. They
// are also the boundary where a NULL column becomes a nil pointer or a zero
// value, and that translation is a decision rather than a cast: getting it wrong
// turns "this snapshot has no author" into "this snapshot was authored by the
// nil UUID", which is a different and much worse claim.

// nullUUID converts an optional uuid to the NullUUID sqlc wants.
func nullUUID(id *uuid.UUID) uuid.NullUUID {
	if id == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *id, Valid: true}
}

// uuidPtr is the inverse.
func uuidPtr(id uuid.NullUUID) *uuid.UUID {
	if !id.Valid {
		return nil
	}
	out := id.UUID
	return &out
}

// pgInt8 wraps a duration for the nullable bigint columns.
//
// pgtype rather than a pointer because the generated params use pgtype, and
// converting through *int64 would need a second helper for no gain.
func pgInt8(v int64) pgtype.Int8 {
	return pgtype.Int8{Int64: v, Valid: true}
}

// int64Ptr is the inverse, and the place where a NULL duration becomes nil --
// which is the ErrNoDuration state, distinct from a duration of zero.
func int64Ptr(v pgtype.Int8) *int64 {
	if !v.Valid {
		return nil
	}
	out := v.Int64
	return &out
}

// isUniqueViolation reports whether an error is a Postgres unique-constraint
// failure.
//
// By SQLSTATE rather than by a pgx error type, and the reason is which contract is
// stable: the unique constraint is the database's, its SQLSTATE (23505) is
// documented and permanent, and pgx's error classification is an implementation
// detail of a library that is free to change how it names things. A test asserting
// on a pgx error type would break on a library upgrade while the behaviour under
// test was unchanged.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}

// snapshotFromRow converts a stored snapshot.
func snapshotFromRow(row queries.SceneSnapshot) *Snapshot {
	return &Snapshot{
		ID:          row.ID,
		SceneID:     row.SceneID,
		TimestampMS: row.TimestampMs,
		CollageID:   uuidPtr(row.CollageID),
		CreatedBy:   uuidPtr(row.CreatedBy),
	}
}

// collageFromRow converts a stored collage.
func collageFromRow(row queries.Collage) *Collage {
	return &Collage{
		ID:                row.ID,
		SceneID:           row.SceneID,
		FrameCount:        row.FrameCount,
		Strategy:          Strategy(row.Strategy),
		SourceDurationMS:  int64Ptr(row.SourceDurationMs),
		CurrentDurationMS: int64Ptr(row.CurrentDurationMs),
	}
}
