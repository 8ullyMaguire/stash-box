# 0583 — Password length limit rejects valid long passwords

**Status: PARTIALLY IMPLEMENTED.** The limit was investigated and is correct; the
error message was a real defect and has been fixed.

- **The 64-byte limit stands.** No code change, deliberately.
- **The error message is fixed.** `ErrPasswordTooLong` read `password > 64`, which
  a user reads as a *character* count.
- Tests: `internal/service/user/password_length_test.go` (6 tests).

This is the one issue in the set where the honest outcome was "the code is right
and the message is wrong", so both halves are recorded. This file is
hand-maintained: the generator skips it, because it describes a partial fix that
no single commit captures.

- Issue: https://github.com/stashapp/stash-box/issues/583

## Why the limit is unchanged

Closed as not-a-bug by a maintainer: bcrypt hashes a byte array, so the 64-BYTE
limit is deliberate. A 50-character password fails because it contains multi-byte
UTF-8. `len(password)` on a Go string returns bytes, so
`internal/service/user/validate.go` is correct as written and was not touched.

## The message fix, as implemented

`internal/service/user/user.go`:

```go
// ErrPasswordTooLong reports a limit in BYTES, because that is what the
// check measures and what bcrypt hashes. A 50-character password made of
// multi-byte UTF-8 exceeds 64 bytes while looking comfortably under 64
// characters, which is exactly the confusion in issue #583. The message
// names the unit so the user is not told their 50-character password is
// "too long" with no way to reconcile that with the form.
ErrPasswordTooLong = fmt.Errorf(
    "password is longer than %d bytes (not characters; multi-byte "+
        "characters such as accented letters or emoji count as more than one)",
    maxPasswordLength)
```

## Tests

`internal/service/user/password_length_test.go`:

| Test | Pins |
|---|---|
| `TestPasswordLengthLimitIsInBytesNotCharacters` | 50 chars / 100 bytes is rejected — the reporter's confusion, made concrete |
| `TestPasswordTooLongMessageNamesTheUnit` | the message says "bytes" and "not characters" |
| `TestPasswordTooLongMessageStatesTheLimit` | the message still contains "64" |
| `TestPasswordAtExactlyTheByteLimitIsAccepted` | 64 bytes is accepted (inclusive) |
| `TestPasswordOneByteOverTheLimitIsRejected` | 65 bytes is rejected |
| `TestLongAsciiPasswordIsAccepted` | a 64-char ASCII password works; a 128-char one is genuinely too long |

**Mutation check:** reverting the message to `password > 64` fails
`TestPasswordTooLongMessageNamesTheUnit` **and nothing else** — which is correct.
The other five assert the *limit*, which deliberately did not change, so they stay
green. A suite where every test flips on a message change would be testing one
string six times.

**The test caught its own author.** The first version hand-wrote a
"64-character" password that was 47 characters; `require.Len` failed it
immediately. The boundary cases are now sliced from a 67-symbol ASCII alphabet,
so the length is correct by construction.

**`validatePassword` takes `(username, emailAddr, password)`** and validates all
three. The tests pass valid values for the first two, because otherwise a
different error fires first and the test passes for the wrong reason.

## If the limit itself is ever revisited

Do not raise it past 72 bytes. `bcrypt.GenerateFromPassword` returns
`ErrPasswordTooLong` for input longer than 72 bytes, so a higher limit defers the
failure to a point where the user cannot act on the message. If a higher limit is
genuinely wanted, the hashing has to change first — pre-hashing to a fixed
length — which is a design decision with its own security trade-offs, not a bug
fix.

---
