package invite

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stashapp/stash-box/internal/queries"
)

// InvitePath is where a key's landing page lives. One constant, because the URL a member shares, the
// URL the QR encodes and the URL the router serves have to be the same string or the QR is a lie.
const InvitePath = "/invite/"

// LinkURL is the address a key is shared as: the instance's own URL, then the code.
//
// It is built from the configured `host_url` rather than from the request, because the QR is rendered
// for a member to hand to somebody ELSE -- a code rendered against whatever host happened to serve the
// request would be a code that works for the person who asked for it and for nobody they showed it to.
func LinkURL(baseURL string, keyID uuid.UUID) string {
	return strings.TrimSuffix(baseURL, "/") + InvitePath + keyID.String()
}

// RecordScan records that somebody OPENED a key's landing page.
//
// THREE THINGS IT DELIBERATELY DOES NOT DO:
//
//   - It does not record a visitor. No address, no agent, no cookie, no account: this is an
//     unauthenticated GET, and a scan is a count, a kind and a time (the spec's §11 privacy line).
//   - It does not fail on a code that is not a key. A URL somebody invented is not an event, and
//     neither an error page nor a log line is owed to whoever typed it.
//   - It does not record a scan of a key NOBODY COULD USE. The landing page serves the same document
//     either way -- that is the anti-enumeration answer, since a page that differed would let a
//     stranger probe for live keys -- but a scan of an expired or spent key is not counted, which is
//     the plan's own sentence and the reason this reads the key's facts at all.
func (s *Invite) RecordScan(ctx context.Context, code string) error {
	keyID, err := uuid.FromString(code)
	if err != nil {
		return nil
	}

	return s.withTxn(func(tx *queries.Queries) error {
		key, err := tx.FindInviteKey(ctx, keyID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}

		// The same rule the signup path spends, asked once about this key.
		pending, err := tx.FindUserTokensByInviteKey(ctx, keyID)
		if err != nil {
			return err
		}
		if Unusable(&key, len(pending), time.Now()) != nil {
			return nil
		}

		return RecordEvent(ctx, tx, key.ID, key.GeneratedBy, EventScan, nil)
	})
}
