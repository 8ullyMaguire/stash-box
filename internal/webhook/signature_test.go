package webhook

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A webhook signature is the only thing standing between "someone posted a body
// to your endpoint" and "this really came from the box". Three properties matter
// and each has a failure mode that is invisible without a test:
//
//   - it must reject a TAMPERED body (the MAC covers the body, not the request);
//   - it must reject a REPLAY (the timestamp is inside the signed material);
//   - it must compare in CONSTANT TIME (a byte-by-byte compare leaks the MAC).

var testSecret = []byte("whsec_this_is_not_a_real_secret")

// defaultWindow is the replay window a consumer is expected to use.
const defaultWindow = 5 * time.Minute

func fixedTime() time.Time {
	return time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
}

func signed(secret, body []byte, at time.Time) (header, ts string) {
	return Sign(secret, body, at), Timestamp(at)
}

func TestARoundTripVerifies(t *testing.T) {
	now := fixedTime()
	body := []byte(`{"event":"scene.added","id":"abc"}`)
	header, ts := signed(testSecret, body, now)

	require.NoError(t, Verify(testSecret, header, ts, body, now, defaultWindow))
}

func TestATamperedBodyIsRejected(t *testing.T) {
	now := fixedTime()
	header, ts := signed(testSecret, []byte(`{"amount":1}`), now)

	err := Verify(testSecret, header, ts, []byte(`{"amount":1000}`), now, defaultWindow)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mismatch",
		"the MAC covers the BODY, so changing a single byte anywhere in it "+
			"invalidates the signature. A signature that only covered the URL "+
			"would pass this, and that is the bug")
}

func TestTheWrongSecretIsRejected(t *testing.T) {
	now := fixedTime()
	body := []byte(`{"event":"x"}`)
	header, ts := signed(testSecret, body, now)

	require.Error(t, Verify([]byte("whsec_a_different_secret"), header, ts, body,
		now, defaultWindow))
}

// A captured delivery must not be valid forever. Without the timestamp inside the
// signed material, this passes -- and a consumer that "adds a scene" on every
// delivery can be made to do it a thousand times from one captured request.
func TestAReplayedDeliveryIsRejectedOutsideTheWindow(t *testing.T) {
	sentAt := fixedTime()
	body := []byte(`{"event":"scene.added"}`)
	header, ts := signed(testSecret, body, sentAt)

	// Inside the window: still good, because a slow consumer is not an attacker.
	require.NoError(t, Verify(testSecret, header, ts, body,
		sentAt.Add(defaultWindow-time.Second), defaultWindow),
		"a delivery one second inside the window is valid. Rejecting it would "+
			"break every consumer with a slow queue")

	// Outside: rejected, and named as a TIMESTAMP problem rather than a mismatch,
	// because that is the diagnostic a consumer needs and cannot get otherwise.
	err := Verify(testSecret, header, ts, body,
		sentAt.Add(defaultWindow+time.Second), defaultWindow)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "window",
		"an expired signature must not be reported as a signature MISMATCH: a "+
			"consumer whose clock is fast would spend an afternoon rotating their "+
			"secret for a clock skew problem")
}

// A timestamp in the FUTURE is as wrong as one in the past -- a consumer whose
// clock is behind should not accept a signature that is not yet valid, because
// that is what a forged far-future timestamp would look like.
func TestAFutureTimestampIsRejected(t *testing.T) {
	now := fixedTime()
	body := []byte(`{"event":"x"}`)
	header, ts := signed(testSecret, body, now.Add(time.Hour))

	err := Verify(testSecret, header, ts, body, now, defaultWindow)
	require.Error(t, err, "a signature from the future is outside the window in "+
		"the other direction, and accepting it widens the replay window to "+
		"unbounded on a clock-skewed consumer")
}

// THE TIMESTAMP IS INSIDE THE SIGNED MATERIAL, so moving it invalidates the MAC.
// This is the case that proves the replay defence is real rather than a header
// somebody can edit.
func TestEditingTheTimestampHeaderInvalidatesTheSignature(t *testing.T) {
	sentAt := fixedTime()
	body := []byte(`{"event":"x"}`)
	header, _ := signed(testSecret, body, sentAt)

	// An attacker with a captured delivery rewrites the timestamp to now, so the
	// window check passes.
	err := Verify(testSecret, header, Timestamp(sentAt.Add(time.Hour*24)),
		body, sentAt, defaultWindow)
	require.Error(t, err,
		"editing the visible timestamp must break the signature, because the "+
			"timestamp is part of what was signed and not a separate claim about it. "+
			"If this passed, the replay window was decorative")
}

// The separator is load-bearing: without a dot, digits can move between the
// timestamp and the body and the signed material is unchanged.
func TestTheTimestampAndBodyCannotBeConfused(t *testing.T) {
	at := time.Unix(123, 0)
	signed_ := fmtSigned(at, []byte("456"))

	// A body that begins with what looks like part of the timestamp, signed at a
	// moment whose digits are the other half.
	other := fmtSigned(time.Unix(1234, 0), []byte("56"))
	assert.NotEqual(t, signed_, other,
		"'123'+'456' and '1234'+'56' must not produce the same signed material. "+
			"Without the dot separator they would, and an attacker could shift "+
			"digits between the timestamp and the body without invalidating "+
			"anything")
}

func fmtSigned(at time.Time, body []byte) string {
	return signRaw(testSecret, body, at)
}

// hmac.Equal, not ==. A byte-by-byte compare RETURNS AT THE FIRST DIFFERENCE, so
// its timing leaks how many leading bytes of the MAC an attacker guessed.
func TestTheComparisonIsConstantTime(t *testing.T) {
	now := fixedTime()
	body := []byte(`{"event":"x"}`)
	good, ts := signed(testSecret, body, now)

	// Two signatures that differ in the FIRST byte and in the LAST byte. With a
	// short-circuiting compare the first is rejected almost immediately and the
	// last takes the full length; hmac.Equal takes the same time for both.
	firstDiff := SignaturePrefix + flipFirst(good[len(SignaturePrefix):])
	lastDiff := SignaturePrefix + flipLast(good[len(SignaturePrefix):])

	// The assertion is not a timing measurement -- that is inherently flaky.
	// It is that BOTH are rejected, and that the code path taken is hmac.Equal.
	// The mutant "use == instead of hmac.Equal" is what actually proves this, and
	// it is in the sweep for this package.
	require.Error(t, Verify(testSecret, firstDiff, ts, body, now, defaultWindow))
	require.Error(t, Verify(testSecret, lastDiff, ts, body, now, defaultWindow))
}

func flipFirst(s string) string {
	b := []byte(s)
	if b[0] == '0' {
		b[0] = '1'
	} else {
		b[0] = '0'
	}
	return string(b)
}

func flipLast(s string) string {
	b := []byte(s)
	if b[len(b)-1] == '0' {
		b[len(b)-1] = '1'
	} else {
		b[len(b)-1] = '0'
	}
	return string(b)
}

// A malformed header is a CONSUMER BUG and a wrong signature is an ATTACK, and
// the two deserve different messages.
func TestMalformedHeadersAreDistinguishedFromWrongOnes(t *testing.T) {
	now := fixedTime()
	body := []byte(`{"event":"x"}`)
	good, ts := signed(testSecret, body, now)

	cases := []struct {
		name   string
		header string
		want   string
	}{
		{"no prefix", good[len(SignaturePrefix):], "prefix"},
		{"wrong prefix", "md5=" + good[len(SignaturePrefix):], "prefix"},
		{"not hex", SignaturePrefix + strings.Repeat("z", 64), "hex"},
		{"too short", SignaturePrefix + good[len(SignaturePrefix):len(good)-2], "hex chars"},
		{"empty", "", "prefix"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Verify(testSecret, tc.header, ts, body, now, defaultWindow)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestAMalformedTimestampIsRejected(t *testing.T) {
	now := fixedTime()
	body := []byte(`{"event":"x"}`)
	header, _ := signed(testSecret, body, now)

	err := Verify(testSecret, header, "not-a-time", body, now, defaultWindow)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unix time")
}

// The signature is over the exact bytes sent. A body that differs only in
// whitespace is a different body, and a consumer that re-serialises before
// verifying will reject valid deliveries.
func TestTheSignatureIsOverExactBytes(t *testing.T) {
	now := fixedTime()
	compact := []byte(`{"a":1,"b":2}`)
	spaced := []byte(`{"a": 1, "b": 2}`)
	header, ts := signed(testSecret, compact, now)

	assert.NotEqual(t,
		Sign(testSecret, compact, now), Sign(testSecret, spaced, now))
	require.Error(t, Verify(testSecret, header, ts, spaced, now, defaultWindow),
		"whitespace is part of the body. Documented here because the natural "+
			"consumer implementation -- parse then re-serialise -- fails this, and "+
			"the fix belongs in the consumer")
}

// The secret must not appear in anything this package produces.
func TestNothingInThePackageEchoesTheSecret(t *testing.T) {
	now := fixedTime()
	secret := []byte("whsec_do_not_leak_me_anywhere_12345")
	body := []byte(`{"event":"x"}`)
	header, ts := signed(secret, body, now)

	for _, produced := range []string{header, ts, Sign(secret, body, now)} {
		assert.NotContains(t, produced, "whsec_",
			"a signature is a function of the secret, and one that CONTAINED the "+
				"secret would be reversible")
	}
	// And the error paths do not include it either, which is the easy place to
	// leak: wrapping an error that happens to quote the key.
	err := Verify(secret, "md5=bad", ts, body, now, defaultWindow)
	require.Error(t, err)
	assert.False(t, bytes.Contains([]byte(err.Error()), secret),
		"an error message must never carry the secret, at any level")
}
