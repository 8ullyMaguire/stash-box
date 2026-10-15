package api

import (
	"context"

	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/qr"
	"github.com/stashapp/stash-box/internal/service/invite"
)

// inviteKeyResolver turns a key into the two things a member needs to share it (phase 11 slice 11c).
//
// BOTH FIELDS COME FROM ONE FUNCTION. The link a member copies and the link the QR encodes are the
// same string, because a QR code that sends somebody to a different address than the link next to it
// is worse than no QR code at all -- and the way that happens is two call sites building the URL.
//
// NO AUTHORISATION HERE, and that is deliberate rather than forgotten: an InviteKey is only ever
// reachable through `User.invite_codes`, which is `@isUserOwner`, or as the return value of the
// member's own `generateInviteKeys`. There is no query that serves another member's key, so there is
// no key here whose fields belong to somebody else. (If one is ever added, IT must carry the
// owner-directive; the field resolvers below cannot tell whom the object was fetched for.)
type inviteKeyResolver struct{ *Resolver }

// shareBaseURL is where a shared link points. The rule and its reasoning live in baseURL
// (internal/api/baseurl.go), shared with the feed URLs in slice 13b: one function, because the link a
// member copies and the link a QR encodes are the same string, and two implementations of "where does
// this instance say it lives" is how they stop agreeing.
func (r *inviteKeyResolver) shareBaseURL(ctx context.Context) string {
	return baseURL(ctx)
}

// Link is the address the key is shared as.
func (r *inviteKeyResolver) Link(ctx context.Context, key *models.InviteKey) (string, error) {
	return invite.LinkURL(r.shareBaseURL(ctx), key.ID), nil
}

// QRSVG renders that same link as an SVG document.
//
// The failure is a real error rather than an empty string: a member who is shown a blank square
// instead of a QR code has no way to tell it is not their link that is broken.
func (r *inviteKeyResolver) QRSVG(ctx context.Context, key *models.InviteKey) (string, error) {
	return qr.SVG(invite.LinkURL(r.shareBaseURL(ctx), key.ID), 4)
}
