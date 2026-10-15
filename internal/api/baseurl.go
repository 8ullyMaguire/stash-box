package api

import (
	"context"

	"github.com/stashapp/stash-box/internal/config"
)

// baseURL is where a link this instance HANDS OUT points: the configured `host_url` when the operator
// has set one, and otherwise the address the request arrived at.
//
// THE PRECEDENCE IS THE POINT, and the fallback is a MEASURED decision rather than a convenience. A
// deployment with no `host_url` is the default deployment, and on one of those a URL built as a bare
// path ("/invite/<code>", "/feeds/saved-search/<token>") reads correctly in a printout and cannot be
// handed to anybody. Measured on the deployed instance.
//
// A configured value still wins, for its original reason: a member who copies a link from a staging
// hostname must not hand somebody a link that only works on staging. The request's own address is the
// only other one this process knows, and behind a proxy it is the address the member is actually using
// -- which is the address their invitee or their feed reader needs.
//
// ONE FUNCTION, THREE CALLERS (invitation links, the QR that encodes them, and feed URLs), because two
// call sites building the same address is how the link a member copies and the link a QR encodes stop
// agreeing -- and the shape of that failure is a QR code that sends somebody somewhere else.
func baseURL(ctx context.Context) string {
	if configured := config.GetHostURL(); configured != "" {
		return configured
	}
	if fromRequest, ok := ctx.Value(BaseURLCtxKey).(string); ok {
		return fromRequest
	}
	return ""
}
