package api

import (
	"context"

	"github.com/stashapp/stash-box/internal/config"
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

// shareBaseURL is where a shared link points: the configured `host_url` when the operator has set
// one, and otherwise the address this request arrived at.
//
// THE PRECEDENCE IS THE POINT, AND THE FALLBACK IS A MEASURED DECISION rather than a convenience. A
// deployment with no `host_url` is the DEFAULT deployment, and on one of those the first version of
// this resolver produced a link of the form "/invite/<code>" -- which ends with the right path, reads
// correctly in a printout, and cannot be handed to anybody. Measured on the deployed instance.
//
// The reason to prefer the configured value is unchanged: a member who shares a link from a staging
// hostname should not hand somebody a link that only works on staging. But the default has to produce
// a USABLE link, and the request's own address is the only other one this process knows -- nginx
// forwards `Host` and `X-Forwarded-Proto`, so on a proxied instance it is the address the member is
// actually using, which is the address their invitee needs.
func (r *inviteKeyResolver) shareBaseURL(ctx context.Context) string {
	if configured := config.GetHostURL(); configured != "" {
		return configured
	}
	if fromRequest, ok := ctx.Value(BaseURLCtxKey).(string); ok {
		return fromRequest
	}
	return ""
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
