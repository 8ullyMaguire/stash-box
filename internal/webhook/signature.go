package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"
)

// SignatureHeader carries the HMAC of the delivery.
const SignatureHeader = "X-Stash-Box-Signature-256"

// TimestampHeader carries the unix-seconds timestamp the signature covers.
const TimestampHeader = "X-Stash-Box-Timestamp"

// SignaturePrefix is the algorithm tag, following the convention GitHub set.
//
// Present because "the first 64 hex characters of a SHA-256 HMAC" is a signature
// FORMAT and not just a value, and a consumer that sees a bare hex string has to
// guess what produced it. The prefix also means a second algorithm can be added
// later without breaking consumers that switch on the tag.
const SignaturePrefix = "sha256="

// signedTemplate is what gets signed: the timestamp, a dot, then the body.
//
// THE DOT IS LOAD-BEARING, not cosmetic. Without a separator, "1" + "23" and "12" +
// "3" produce the same signed material, so an attacker can move a digit between
// the timestamp and the body and the signature still verifies. One unambiguous
// separator makes the split between the two fields explicit.
const signedTemplate = "%s.%s"

// Sign produces the signature header value for a body at a moment.
//
// The SECRET is a parameter and is never stored, never logged, and never returned
// by anything in this package. The service keeps a HASH of it; the plaintext exists
// only in the response to the one request that creates the endpoint, which is the
// only moment a consumer can possibly have it.
func Sign(secret, body []byte, at time.Time) string {
	return SignaturePrefix + signRaw(secret, body, at)
}

// signRaw is the hex MAC without the algorithm tag.
func signRaw(secret, body []byte, at time.Time) string {
	mac := hmac.New(sha256.New, secret)
	// hash.Hash.Write never returns an error, by the interface contract.
	_, _ = mac.Write([]byte(fmt.Sprintf(signedTemplate,
		strconv.FormatInt(at.Unix(), 10), body)))
	return hex.EncodeToString(mac.Sum(nil))
}

// Timestamp renders the header value for a moment.
//
// Unix SECONDS, not milliseconds and not RFC3339, and the reason is that this
// value is part of the signed material precisely so a captured delivery cannot be
// replayed forever. A millisecond timestamp here and a second-based window check
// reading it is a replay window 1000x what anyone intended, and it is invisible
// because both halves are individually correct.
func Timestamp(at time.Time) string {
	return strconv.FormatInt(at.Unix(), 10)
}

// Verify checks a signature against a body and a timestamp, with a replay window.
//
// hmac.Equal, NOT `==` and NOT bytes.Equal. That is not a style preference: `==` on
// a MAC compares byte by byte and RETURNS AT THE FIRST DIFFERENCE, so the time it
// takes tells an attacker how many leading bytes they guessed correctly. Over a
// network with jitter that is measurable, and the entire point of signing a payload
// is that the signature cannot be learned one byte at a time. hmac.Equal runs in
// time independent of the content.
//
// The window is a parameter, not an implicit clock read, so a test can pin BOTH
// ends of the boundary instead of racing one.
func Verify(secret []byte, header, timestamp string, body []byte, now time.Time, window time.Duration) error {
	const hexLen = sha256.Size * 2

	if len(header) <= len(SignaturePrefix) || header[:len(SignaturePrefix)] != SignaturePrefix {
		return fmt.Errorf("signature missing the %q prefix", SignaturePrefix)
	}
	encoded := header[len(SignaturePrefix):]
	if len(encoded) != hexLen {
		// Length checked BEFORE decoding, so a truncated or padded header is a
		// clear error rather than a decode failure. hmac.Equal handles unequal
		// lengths correctly, but naming the problem is what makes it debuggable.
		return fmt.Errorf("signature is %d hex chars, want %d", len(encoded), hexLen)
	}
	got, err := hex.DecodeString(encoded)
	if err != nil {
		// Malformed, not WRONG. The two mean different things: a consumer sending
		// a malformed header has a bug, and one sending a valid-looking wrong
		// header is being attacked.
		return fmt.Errorf("signature is not valid hex: %w", err)
	}

	secs, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return fmt.Errorf("timestamp header is not a unix time: %w", err)
	}
	signedAt := time.Unix(secs, 0)

	// The REPLAY RULE, and the reason the timestamp is inside the signed material
	// at all: without it a captured request is valid forever, and a webhook that
	// forwards "a scene was added" can be replayed to make a consumer add it a
	// thousand times.
	//
	// Checked BEFORE the MAC so an old timestamp is reported as an old timestamp
	// rather than as a signature mismatch, which is the diagnostic a consumer
	// needs and the one they cannot get any other way.
	age := now.Sub(signedAt)
	if age < 0 {
		age = -age
	}
	if age > window {
		return fmt.Errorf("signature timestamp is %s old, past the %s window", age, window)
	}

	want, err := hex.DecodeString(signRaw(secret, body, signedAt))
	if err != nil {
		return fmt.Errorf("recomputing the signature failed: %w", err)
	}
	if !hmac.Equal(got, want) {
		return fmt.Errorf("signature mismatch")
	}
	return nil
}
