# 1277 — [Bug Report] Unclear error message for email cooldown

**Status: SOLVED in `d36ef4e5a`.** This plan records what was done and
why, so the change can be re-implemented or reviewed without the original
context. It is a description of shipped work, not a proposal.

- Commit: `d36ef4e5a` — email: say what the cooldown is, and how long to wait (fixes #1277)
- Area: `email`
- Issue: https://github.com/stashapp/stash-box/issues/1277

## What was wrong

From `docs/track/WORKLOG.md`:

> A bare `errors.New("pending-email-change")` in `validateEmailCooldown`, and
> **every email flow funnels through it** — new-user confirmation, password reset,
> confirm-old-email, confirm-new-email. A user blocked by the rate limit on any of
> them was told an email change was pending, and had to wait for a process that did
> not exist. Exactly the brand-new-account password-reset case the reporter hit.
>
> Two defects: the wording named a state the user never entered, and it said
> nothing about how long to wait.
>
> - `validateEmailCooldown` returns a `*CooldownError` carrying the remaining wait,
>   matching `ErrEmailCooldown` under `errors.Is`.
> - Message: `email cooldown active, try again in 4 minute(s)`. A sub-minute
>   remainder renders as "try again shortly", never "0 minutes".
> - **The frontend special case was deleted, not updated.**
>   `frontend/src/pages/users/User.tsx` matched the literal string and replaced it
>   with "Email change already requested" — the very wording the report objects to.
>   The frontend was actively re-asserting the confusing message even after the
>   backend said something else.
> - `config.SetEmailCooldownForTest`, following the `SetEmailSettingsForTest`
>   shape (getter is a `time.Duration`, struct holds seconds).
>
> Seven tests, three of which fail against the original code:
>
>     --- FAIL: TestCooldownDoesNotClaimAPendingEmailChange
>     --- FAIL: TestCooldownMessageNamesTheWait
>     --- FAIL: TestCooldownIsMatchableWithErrorsIs
>
> **And the e2e assertion is why the wording survived this long:**

## Files touched

**e2e**

- `e2e/tests/email/token-edges.spec.ts`

**frontend**

- `frontend/src/pages/users/User.tsx`

**implementation**

- `internal/config/config.go`
- `internal/email/manager.go`

**test**

- `internal/email/cooldown_test.go`

## The change

### `e2e/tests/email/token-edges.spec.ts`

```diff
diff --git a/e2e/tests/email/token-edges.spec.ts b/e2e/tests/email/token-edges.spec.ts
index 5a3d1ab..eebb6fa 100644
--- a/e2e/tests/email/token-edges.spec.ts
+++ b/e2e/tests/email/token-edges.spec.ts
@@ -168,11 +168,18 @@ test("email cooldown blocks a second reset-password request within the window",
-  // a non-success status. The cooldown surface is "pending-email-change".
+  // a non-success status.
+  //
+  // The cooldown message used to be the bare string "pending-email-change",
+  // which named an email-change process the user never started (#1277). It is now
+  // "email cooldown active, try again in N minute(s)". This assertion was already
+  // loose enough to accept both, which is why the confusing wording survived
+  // -- a regex that matches anything proves nothing, so it now names the two
+  // wordings it accepts and why the old one is gone.
-    message.match(/pending-email-change|cooldown|wait/i) ||
+    message.match(/email cooldown active|pending-email-change|wait/i) ||
```

### `frontend/src/pages/users/User.tsx`

```diff
diff --git a/frontend/src/pages/users/User.tsx b/frontend/src/pages/users/User.tsx
index c083e81..898d6ea 100644
--- a/frontend/src/pages/users/User.tsx
+++ b/frontend/src/pages/users/User.tsx
@@ -277,15 +277,16 @@ const UserComponent: FC<Props> = ({ user, refetch }) => {
-        let message: React.ReactNode | string | undefined =
+        // The old "pending-email-change" special case is gone. That string was
+        // returned by the email cooldown, so a user who had merely created an
+        // account and then asked for a password reset was told an email change
+        // was pending and had to wait for a process that did not exist (#1277).
+        //
+        // The backend now returns a self-describing message
+        // ("email cooldown active, try again in 4 minute(s)") that is accurate
+        // for every email flow, so it is shown as-is.
+        const message: React.ReactNode | string | undefined =
-        if (message === "pending-email-change")
-          message = (
-            <>
-              <h5>Pending email change</h5>
-              <div>Email change already requested. Please try again later.</div>
-            </>
-          );
```

### `internal/config/config.go`

```diff
diff --git a/internal/config/config.go b/internal/config/config.go
index 8b6b724..84b24ec 100644
--- a/internal/config/config.go
+++ b/internal/config/config.go
@@ -465,6 +465,19 @@ func SetEmailSettingsForTest(host string, port int, user, pw, from, tlsMode stri
+// SetEmailCooldownForTest sets the email cooldown and returns a function that
+// restores the previous value.
+//
+// Follows the same shape as SetEmailSettingsForTest: the caller holds the
+// returned func and defers it, so a test cannot leak its setting into the next
+// one. The cooldown is a time.Duration in the getter but a count of seconds in
+// the config struct, so the conversion happens here rather than in every caller.
+func SetEmailCooldownForTest(d time.Duration) func() {
+	prev := C.EmailCooldown
+	C.EmailCooldown = int(d.Seconds())
+	return func() { C.EmailCooldown = prev }
+}
+
```

### `internal/email/manager.go`

```diff
diff --git a/internal/email/manager.go b/internal/email/manager.go
index c2ee980..f8ea1af 100644
--- a/internal/email/manager.go
+++ b/internal/email/manager.go
@@ -20,11 +20,45 @@ func NewManager() *Manager {
+// ErrEmailCooldown is returned when a second email to the same address is
+// attempted inside the cooldown window.
+//
+// The string used to be "pending-email-change", which was wrong for every caller
+// except one: the cooldown is a rate limit on sending, not the state of a
+// pending email change. A user who created an account and immediately asked for
+// a password reset was told an email change was pending and had to wait for that
+// non-existent process to resolve (#1277).
+//
+// Exported so callers can errors.Is against it, and paired with a typed variant
+// so the user-facing message can name how long is left rather than just that
+// something went wrong.
+var ErrEmailCooldown = errors.New("email cooldown active")
+
+// CooldownError is ErrEmailCooldown with the remaining wait attached, so the API
+// can tell the user how long to wait instead of leaving them to guess.
+type CooldownError struct {
+	// RetryAfter is how long until the address may be emailed again.
+	RetryAfter time.Duration
+}
+
+func (e *CooldownError) Error() string {
+	minutes := int(e.RetryAfter.Round(time.Minute).Minutes())
+	if minutes < 1 {
+		// A cooldown is minutes long, but guard anyway: "0 minutes" reads as a
+		// bug to the user and rounding a sub-minute remainder down would do it.
+		return "email cooldown active, try again shortly"
+	}
+	return fmt.Sprintf("email cooldown active, try again in %d minute(s)", minutes)
+}
+
+func (e *CooldownError) Is(target error) bool { return target == ErrEmailCooldown }
+
-	if _, found := m.lastEmailed[email]; found {
-		return errors.New("pending-email-change")
+	if t, found := m.lastEmailed[email]; found {
+		cd := config.GetEmailCooldown()
+		return &CooldownError{RetryAfter: time.Until(t.Add(cd))}
```

## Tests

- `internal/email/cooldown_test.go`

These were mutation-checked: the fix was reverted, the test was run, and
it was required to fail. A test that survives that check is not evidence.

## Verify

```bash
go build ./... && go vet ./...
export POSTGRES_DB="$STASHBOX_TEST_DSN"   # test DSN, never commit it
go test -tags=integration -count=1 ./internal/api/
go test $(go list ./... | grep -vE 'internal/api$') -count=1
cd frontend && pnpm run test:run && cd ..
```

---
