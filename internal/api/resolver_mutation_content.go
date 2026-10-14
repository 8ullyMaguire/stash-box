package api

import (
	"context"
	"errors"

	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/auth"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/service/ingest"
)

// SubmitContentURL queues a URL for the aggregation pipeline (spec §1, §4).
//
// WHAT THIS DOES NOT DO: drain the queue. The submission is recorded and the cron tick
// processes it, because a request that fetched a third-party page would hold a connection
// open for as long as that page took to answer -- and the aggregator's whole design is that
// a slow host costs a queue row, not a request.
//
// sceneID arrives already parsed as a UUID: gqlgen's ID scalar rejects a malformed one
// before this function runs, so there is no string to validate here. That is the right
// place for the check -- a bad id is a malformed REQUEST, and a GraphQL type error says so
// without this resolver having to invent a message for it.
func (r *mutationResolver) SubmitContentURL(ctx context.Context, url string, sceneID *uuid.UUID) (*models.IngestSubmission, error) {
	user := auth.GetCurrentUser(ctx)

	submission, err := r.services.Ingest().Submit(ctx, url, sceneID, &user.ID)
	if errors.Is(err, ingest.ErrInvalidURL) {
		// A URL that cannot be ingested is reported as a resolved value rather than as an
		// error, and that is deliberate: the caller's form needs a sentence to show beside
		// the field, this is a bad input and not a broken server, and a GraphQL error would
		// surface it in the client's error channel where it cannot be rendered in place.
		message := err.Error()
		return &models.IngestSubmission{
			Status:  "invalid",
			Message: &message,
			// Empty rather than a guess: there is no normalised form of a URL that could
			// not be parsed, and inventing one would be the API stating something it does
			// not know.
			NormalizedURL: "",
		}, nil
	}
	if err != nil {
		return nil, err
	}

	result := &models.IngestSubmission{
		Status:        submission.Status,
		QueueID:       submission.QueueID,
		NormalizedURL: submission.NormalizedURL,
	}
	if submission.Reason != "" {
		reason := submission.Reason
		result.Message = &reason
	}
	return result, nil
}
