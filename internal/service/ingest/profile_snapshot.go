package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/pkg/urlkey"
)

// ErrNoSuchPerformer is returned when a snapshot names a performer this instance does not have.
//
// A named error rather than the foreign key's violation: the caller's next move is to say "that
// performer does not exist", and a constraint name is not an instruction.
var ErrNoSuchPerformer = errors.New("there is no such performer")

// ProfileSnapshot is one capture of what a page said about a performer.
type ProfileSnapshot struct {
	ID            uuid.UUID
	PerformerID   uuid.UUID
	SourceURL     string
	URLNormalized string
	// CapturedAt is the database's clock at the moment of the capture, not this process's.
	CapturedAt  time.Time
	ContentHash string
	// Content is the extractor's reading of the page, as it read it.
	Content json.RawMessage
}

// SnapshotPerformerProfile fetches a page and records what it says about a performer.
//
// THE FETCH IS THE PIPELINE'S OWN EXTRACTOR. A snapshot is a submission whose result is recorded
// instead of merged, so it must not grow a second network path that could disagree with the first
// about what a page says.
//
// A FAILED FETCH RECORDS NOTHING. ErrGone, ErrUnreachable and ErrUnsupportedPage all come back to
// the caller with no row written: a record of a failed fetch is not evidence of anything, and the
// table's whole purpose is to hold claims somebody can check. The reason is returned, so the caller
// can distinguish "the page is gone" from "the host had a bad day" -- which is the difference
// between accepting the loss and trying again.
//
// CAPTURING THE SAME CONTENT TWICE IS ONE RECORD, and capturing CHANGED content is a second one.
// Both fall out of the table's unique key; see migration 124. Nothing here decides it, which is
// deliberate: the rule belongs where it cannot be forgotten by the next caller.
func (s *Service) SnapshotPerformerProfile(ctx context.Context, performerID uuid.UUID, rawURL string) (ProfileSnapshot, error) {
	normalized, err := urlkey.Normalize(rawURL)
	if err != nil {
		return ProfileSnapshot{}, fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}

	if _, err := s.q.FindPerformer(ctx, performerID); err != nil {
		// pgx.ErrNoRows, not sql.ErrNoRows: sqlc generates pgx-native queries in this repo, and
		// checking the database/sql sentinel would mean the refusal never fires and a missing
		// performer surfaced as a constraint violation from the insert below.
		if errors.Is(err, pgx.ErrNoRows) {
			return ProfileSnapshot{}, ErrNoSuchPerformer
		}
		return ProfileSnapshot{}, fmt.Errorf("looking up the performer: %w", err)
	}

	// The RAW url is what gets fetched: the extractor follows shorteners and reports where it
	// landed in ResolvedURL, and normalising first would take that decision away from it.
	meta, err := s.extractor.Extract(ctx, rawURL)
	if err != nil {
		return ProfileSnapshot{}, err
	}

	// json.Marshal of a struct writes its fields in declaration order, so the same Metadata always
	// produces the same bytes and therefore the same hash. That is what lets a later capture be
	// compared with an earlier one by hash alone -- a map or a re-ordered encoder would give two
	// hashes for one page and turn every capture into a new record.
	content, err := json.Marshal(meta)
	if err != nil {
		return ProfileSnapshot{}, fmt.Errorf("recording what %s says: %w", rawURL, err)
	}
	sum := sha256.Sum256(content)

	row, err := s.q.CreatePerformerProfileSnapshot(ctx, queries.CreatePerformerProfileSnapshotParams{
		ID:            uuid.Must(uuid.NewV7()),
		PerformerID:   performerID,
		SourceUrl:     rawURL,
		UrlNormalized: normalized,
		ContentHash:   hex.EncodeToString(sum[:]),
		Content:       content,
	})
	if err != nil {
		return ProfileSnapshot{}, fmt.Errorf("recording the snapshot of %s: %w", rawURL, err)
	}

	return ProfileSnapshot{
		ID:            row.ID,
		PerformerID:   row.PerformerID,
		SourceURL:     row.SourceUrl,
		URLNormalized: row.UrlNormalized,
		CapturedAt:    row.CapturedAt,
		ContentHash:   row.ContentHash,
		Content:       json.RawMessage(row.Content),
	}, nil
}

// ListPerformerProfileSnapshots reads a performer's captures, newest first.
func (s *Service) ListPerformerProfileSnapshots(ctx context.Context, performerID uuid.UUID) ([]ProfileSnapshot, error) {
	rows, err := s.q.ListPerformerProfileSnapshots(ctx, performerID)
	if err != nil {
		return nil, fmt.Errorf("reading the performer's snapshots: %w", err)
	}

	out := make([]ProfileSnapshot, 0, len(rows))
	for _, row := range rows {
		out = append(out, ProfileSnapshot{
			ID:            row.ID,
			PerformerID:   row.PerformerID,
			SourceURL:     row.SourceUrl,
			URLNormalized: row.UrlNormalized,
			CapturedAt:    row.CapturedAt,
			ContentHash:   row.ContentHash,
			Content:       json.RawMessage(row.Content),
		})
	}
	return out, nil
}
