package invite

import (
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/stashapp/stash-box/internal/queries"
)

func key(uses *int, expire *time.Time) *queries.InviteKey {
	return &queries.InviteKey{Uses: uses, ExpireTime: expire}
}

// THE RULE TWO CALLERS SPEND, TESTED ONCE (phase 11 slice 11c). The signup path refuses on it; the
// landing page counts a visit by it. Both must answer the same question the same way, and the only
// way to know they do is for the rule to be one function -- which is why this table is the whole
// behaviour and neither caller restates any of it.
func TestUnusableIsTheOneAnswerToWhetherAKeyMayBeUsed(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	yesterday := now.Add(-24 * time.Hour)
	tomorrow := now.Add(24 * time.Hour)
	one, five := 1, 5

	cases := []struct {
		name    string
		key     *queries.InviteKey
		pending int
		want    error
		why     string
	}{
		{"no key at all", nil, 0, nil,
			"nothing about a key that does not exist can make it unusable; whether one is REQUIRED is a different question with a different owner"},
		{"live, unlimited, unused", key(nil, nil), 0, nil,
			"a NULL uses is UNLIMITED, which is what the column means and not 'one'"},
		{"live, unlimited, used before", key(nil, nil), 40, nil,
			"and it stays usable however many times it has been used"},
		{"live, uses left", key(&five, nil), 2, nil, "three of five spent"},
		{"expired", key(nil, &yesterday), 0, ErrExpired, "an expiry that has passed is the key's own fact"},
		{"expiring exactly now", key(nil, &now), 0, nil,
			"an instant that IS now has not passed yet: the boundary is pinned so the rule does not drift by a tick"},
		{"expires later", key(nil, &tomorrow), 0, nil, ""},
		{"one use, none pending", key(&one, nil), 0, nil, "the one use is still available"},
		{"one use, one pending", key(&one, nil), 1, ErrUsed,
			"a use is consumed at ACTIVATION, so a pending activation is a use already promised"},
		{"one use, two pending", key(&one, nil), 2, ErrUsed, ""},
		{"five uses, five pending", key(&five, nil), 5, ErrUsed, "exactly exhausted"},
		{"expired AND exhausted", key(&one, &yesterday), 9, ErrExpired,
			"the earlier cause wins: an expired key's visits are not counted for one reason, and it should be the honest one"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Unusable(tc.key, tc.pending, now)
			if tc.want == nil {
				assert.NoError(t, err, tc.why)
				return
			}
			assert.ErrorIs(t, err, tc.want, tc.why)
		})
	}
}

// The link a member shares, the link the QR encodes and the link the router serves are one string.
// The trailing slash is the case worth pinning: `${host_url}/invite/x` and `${host_url}//invite/x` are
// different strings and only one of them is a working link.
func TestLinkURLIsBuiltFromTheInstancesOwnAddress(t *testing.T) {
	id := uuid.Must(uuid.FromString("01a10641-c1ef-7d92-bbd9-1d0808cf0e64"))

	assert.Equal(t, "https://example.test/invite/01a10641-c1ef-7d92-bbd9-1d0808cf0e64",
		LinkURL("https://example.test", id))
	assert.Equal(t, "https://example.test/invite/01a10641-c1ef-7d92-bbd9-1d0808cf0e64",
		LinkURL("https://example.test/", id),
		"one trailing slash is one link, not two")
	assert.Equal(t, "/invite/01a10641-c1ef-7d92-bbd9-1d0808cf0e64", LinkURL("", id),
		"a host_url that was never configured still produces the path the router serves")
	assert.Equal(t, InvitePath+"01a10641-c1ef-7d92-bbd9-1d0808cf0e64",
		LinkURL("https://example.test", id)[len("https://example.test"):],
		"and the path is the constant the router mounts, so the two cannot drift apart")
}
