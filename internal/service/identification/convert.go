package identification

import (
	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/queries"
)

// Conversions between the sqlc row types and this package's own.
//
// Kept together because they are the same mechanical concern, and because this is
// the boundary where a NULL column becomes a nil pointer -- a translation that is a
// decision, not a cast. Getting it wrong turns "nobody suggested this" into "the
// nil UUID suggested this", which is a different and much worse claim.

// nullUUID converts an optional uuid for a query parameter.
func nullUUID(id *uuid.UUID) uuid.NullUUID {
	if id == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *id, Valid: true}
}

// uuidPtr is the inverse: a NULL column becomes nil, which for ResolvedID is what
// makes "not solved yet" representable.
func uuidPtr(id uuid.NullUUID) *uuid.UUID {
	if !id.Valid {
		return nil
	}
	out := id.UUID
	return &out
}

// strPtr converts a string for a nullable column parameter.
func strPtr(v string) *string { return &v }

// targetTypePtr converts a nullable resolved_type.
func targetTypePtr(s *string) *TargetType {
	if s == nil {
		return nil
	}
	t := TargetType(*s)
	return &t
}

// queryFromRow converts a stored query.
func queryFromRow(row queries.IdentificationQuery) *Query {
	return &Query{
		ID:           row.ID,
		TargetType:   TargetType(row.TargetType),
		TargetID:     uuidPtr(row.TargetID),
		Description:  row.Description,
		CollageID:    uuidPtr(row.CollageID),
		SnapshotID:   uuidPtr(row.SnapshotID),
		CreatedBy:    uuidPtr(row.CreatedBy),
		Status:       Status(row.Status),
		ResolvedType: targetTypePtr(row.ResolvedType),
		ResolvedID:   uuidPtr(row.ResolvedID),
		ResolvedBy:   uuidPtr(row.ResolvedBy),
		CreatedAt:    row.CreatedAt,
		UpdatedAt:    row.UpdatedAt,
	}
}

// queryFromRowOrErr is queryFromRow with the not-found case mapped to the
// service's sentinel, so Resolve's transaction body reads as one flow instead of
// repeating the same three lines.
func queryFromRowOrErr(row queries.IdentificationQuery, err error) (*Query, error) {
	if err != nil {
		return nil, err
	}
	return queryFromRow(row), nil
}

// queriesFromRows converts a list.
func queriesFromRows(rows []queries.IdentificationQuery) []*Query {
	out := make([]*Query, 0, len(rows))
	for _, row := range rows {
		out = append(out, queryFromRow(row))
	}
	return out
}
