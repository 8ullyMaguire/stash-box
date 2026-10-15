//go:build integration

package api_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/config"
	"github.com/stashapp/stash-box/internal/service/invite"
)

// THE LINK AND ITS QR (SPEC §11, plan phase 11 slice 11c): `InviteKey.link` and `InviteKey.qr_svg`.
//
// THE CLAIM THIS FILE EXISTS FOR: the link a member copies and the link the QR code encodes are the
// SAME STRING, and the code decodes back to it with an INDEPENDENT decoder. A QR that sends somebody
// somewhere else than the link beside it is worse than no QR at all, and the way that happens in
// practice is two call sites building the URL -- which is why both fields come from one function and
// this test reads both off the wire rather than asserting the function.

type inviteShare struct {
	ID    string `json:"id"`
	Link  string `json:"link"`
	QrSVG string `json:"qr_svg"`
}

// inviteShares reads the member's own keys WITH their share fields, which is the only way they are
// reachable (`invite_codes` is owner-only).
func (c *graphqlClient) inviteShares() ([]inviteShare, error) {
	q := `{ me { invite_codes { id link qr_svg } } }`
	var resp struct {
		Me *struct {
			InviteCodes []inviteShare `json:"invite_codes"`
		}
	}
	if err := c.Post(q, &resp); err != nil {
		return nil, err
	}
	if resp.Me == nil {
		return nil, nil
	}
	return resp.Me.InviteCodes, nil
}

func TestAKeysLinkIsTheInstancesOwnAddressAndTheQrEncodesThatSameLink(t *testing.T) {
	member, runner := inviteMember(t)
	grantInvite(t, member.ID, 1)
	keys := keyIDs(t, mustKeys(t, runner, 1, nil))

	shares, err := runner.client.inviteShares()
	require.NoError(t, err)
	require.Len(t, shares, 1, "the member has the one key they just generated")

	share := shares[0]
	assert.Equal(t, keys[0].String(), share.ID, "the key read back is the key that was minted")

	// THE ADDRESS IS THE INSTANCE'S OWN, not the request's host: a member who asks from a staging
	// hostname must not be handed a link that only works there, because the person they give it to is
	// somewhere else.
	expected := invite.LinkURL(config.GetHostURL(), keys[0])
	assert.Equal(t, expected, share.Link)
	assert.True(t, strings.HasPrefix(share.Link, strings.TrimSuffix(config.GetHostURL(), "/")),
		"the link starts at the configured host_url (%s), not at the request's host", config.GetHostURL())

	// It is a document, it is not empty, and it names no other key.
	require.NotEmpty(t, share.QrSVG)
	assert.True(t, strings.HasPrefix(share.QrSVG, "<svg "), "the QR is a standalone SVG document")
	assert.True(t, strings.HasSuffix(share.QrSVG, "</svg>"))
	assert.Contains(t, share.QrSVG, "viewBox=")
	assert.NotContains(t, share.QrSVG, share.Link,
		"the URL is not written into the document as text: the picture is geometry")

	// THE PLAN'S VERIFICATION, at the API level: the rendered code decodes back to the link with an
	// independent decoder. Skipped (not failed) where the tools are absent; the live walk runs it
	// unconditionally on the host that has them.
	zbar, err := exec.LookPath("zbarimg")
	if err != nil {
		t.Skip("zbarimg is not installed, so the QR cannot be decoded by an independent reader here")
	}
	rsvg, err := exec.LookPath("rsvg-convert")
	require.NoError(t, err, "zbarimg is present, so the SVG needs a renderer to reach it")

	dir := t.TempDir()
	svgPath := filepath.Join(dir, "key.svg")
	pngPath := filepath.Join(dir, "key.png")
	require.NoError(t, os.WriteFile(svgPath, []byte(share.QrSVG), 0o600))
	out, err := exec.Command(rsvg, "-w", "600", svgPath, "-o", pngPath).CombinedOutput()
	require.NoError(t, err, "rsvg-convert: %s", out)
	out, err = exec.Command(zbar, "--quiet", "--raw", pngPath).CombinedOutput()
	require.NoError(t, err, "zbarimg: %s", out)

	assert.Equal(t, share.Link, strings.TrimSpace(string(out)),
		"the QR decodes to the SAME link the member copies: if these two disagree, the code is a lie")
}
