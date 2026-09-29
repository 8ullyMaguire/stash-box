# 0734 — [Bug Report] SMTP over TLS is unsupported

**Status: SOLVED in `176758e1a`.** This plan records what was done and
why, so the change can be re-implemented or reviewed without the original
context. It is a description of shipped work, not a proposal.

- Commit: `176758e1a` — email: add implicit TLS (SMTPS) support for port 465 (fixes #734)
- Area: `email`
- Issue: https://github.com/stashapp/stash-box/issues/734

## What was wrong

From `docs/track/WORKLOG.md`:

> `Connecting to port 465 with implicit TLS doesn't work currently, only port 587
> with STARTTLS, which is less secure.`
>
> Two layers, both needing the change:
>
> - `config.GetEmailTLSMode()` recognized only `mandatory` / `opportunistic` /
>   `none`. A value of `implicit` fell through to the `default: return "mandatory"`
>   branch — so it was **silently** downgraded to STARTTLS, not rejected.
> - `email.Manager.Send()`'s switch had no case for implicit TLS, so
>   `mail.WithSSL()` was never attached.
>
> The distinction is what the first byte on the wire is. Implicit TLS (RFC 8314)
> wraps the TCP connection before any SMTP command; STARTTLS (RFC 3207) sends a
> cleartext `EHLO` first and upgrades mid-session. A 465 server does not speak
> STARTTLS at all, so the downgrade makes the client wait forever for a banner
> that never comes.
>
> Fix: `GetEmailTLSMode()` accepts `implicit`, and `Send()` maps it to
> `mail.WithSSL()` plus `mail.WithTLSPolicy(mail.NoTLS)`.
>
> `WithSSL()` rather than `WithSSLPort()`: per go-mail's own docs an explicit
> `WithPort` takes precedence and skips the automatic 465 selection, and the port
> is set from config a few lines above. `NoTLS` alongside it suppresses the
> STARTTLS probe — the connection is already encrypted, and the server does not
> advertise STARTTLS.

## Files touched

**implementation**

- `README.md`
- `internal/config/config.go`
- `internal/email/manager.go`

**test**

- `internal/email/manager_test.go`

## The change

### `README.md`

```diff
diff --git a/README.md b/README.md
index 16e5073..8b6766b 100644
--- a/README.md
+++ b/README.md
@@ -84,7 +84,7 @@ There are two ways to authenticate a user in Stash-box: a session or an API key.
-| `email_tls_mode` | `mandatory` | STARTTLS policy for the SMTP client. `mandatory` requires STARTTLS, `opportunistic` uses it when offered, `none` disables TLS. |
+| `email_tls_mode` | `mandatory` | Transport security for the SMTP client. `mandatory` requires STARTTLS, `opportunistic` uses STARTTLS when offered, `none` disables TLS, and `implicit` uses implicit TLS (SMTPS, RFC 8314) — required by port 465, which does not speak STARTTLS. |
```

### `internal/config/config.go`

```diff
diff --git a/internal/config/config.go b/internal/config/config.go
index 266409c..5a97ff3 100644
--- a/internal/config/config.go
+++ b/internal/config/config.go
@@ -265,12 +265,23 @@ func GetEmailFrom() string {
-// GetEmailTLSMode returns the configured STARTTLS policy for the SMTP client.
-// Recognized values: "mandatory" (default), "opportunistic", "none". Anything
-// else falls back to "mandatory" to preserve secure-by-default behavior.
+// GetEmailTLSMode returns the configured transport security mode for the SMTP
+// client.
+//
+// Recognized values:
+//
+//	"mandatory"    STARTTLS is required (default).
+//	"opportunistic" use STARTTLS if the server offers it, otherwise plaintext.
+//	"implicit"     implicit TLS (SMTPS, RFC 8314) -- the connection is wrapped
+//	               in TLS before any SMTP command. Required by port 465, which
+//	               does not speak STARTTLS.
+//	"none"         plaintext, no encryption.
+//
+// Anything else falls back to "mandatory" to preserve secure-by-default
+// behavior.
-	case "opportunistic", "none":
+	case "opportunistic", "none", "implicit":
@@ -429,6 +440,31 @@ func Initialize() error {
+// SetEmailSettingsForTest overrides the SMTP connection settings for the
+// duration of a test. It exists because C is unexported and the email package
+// cannot configure the manager it drives from a test otherwise; there is no
+// other production caller.
+//
+// Passing a restore function keeps callers from having to snapshot C themselves.
+func SetEmailSettingsForTest(host string, port int, user, pw, from, tlsMode string) func() {
+	prevHost, prevPort := C.EmailHost, C.EmailPort
+	prevUser, prevPW := C.EmailUser, C.EmailPW
+	prevFrom, prevTLS := C.EmailFrom, C.EmailTLSMode
+
+	C.EmailHost = host
+	C.EmailPort = port
+	C.EmailUser = user
+	C.EmailPW = pw
+	C.EmailFrom = from
+	C.EmailTLSMode = tlsMode
+
+	return func() {
+		C.EmailHost, C.EmailPort = prevHost, prevPort
+		C.EmailUser, C.EmailPW = prevUser, prevPW
+		C.EmailFrom, C.EmailTLSMode = prevFrom, prevTLS
+	}
+}
+
```

### `internal/email/manager.go`

```diff
diff --git a/internal/email/manager.go b/internal/email/manager.go
index b536541..c2ee980 100644
--- a/internal/email/manager.go
+++ b/internal/email/manager.go
@@ -79,6 +79,26 @@ func (m *Manager) Send(email, subject, text, html string) error {
+	case "implicit":
+		// Implicit TLS (SMTPS, RFC 8314): the TCP connection is wrapped in TLS
+		// before any SMTP command is sent. This is what port 465 expects, and
+		// it is NOT the same thing as STARTTLS (RFC 3207), where the session
+		// starts in plaintext and is upgraded mid-connection.
+		//
+		// go-mail's TLSPolicy only describes STARTTLS behaviour, so there is no
+		// policy that yields implicit TLS -- WithSSL() is the only knob for it.
+		// Using WithSSLPort instead would be wrong here: we already set the port
+		// explicitly above, and per its docs an explicit WithPort takes
+		// precedence and skips the automatic 465 selection.
+		//
+		// Setting the policy to NoTLS as well is deliberate. Implicit TLS servers
+		// do not advertise STARTTLS, so asking for it on an already-encrypted
+		// connection would only risk a spurious failure; the encryption is
+		// established during the dial.
+		opts = append(opts,
+			mail.WithSSL(),
+			mail.WithTLSPolicy(mail.NoTLS),
+		)
```

## Tests

- `internal/email/manager_test.go`

These were mutation-checked: the fix was reverted, the test was run, and
it was required to fail. A test that survives that check is not evidence.

## Verify

```bash
go build ./... && go vet ./...
export POSTGRES_DB="$STASHBOX_TEST_DSN"   # test DSN, never commit it
go test -tags=integration -count=1 ./internal/api/
go test $(go list ./... | grep -vE 'internal/api$') -count=1
```

---
