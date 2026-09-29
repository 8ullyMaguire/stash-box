# 0649 — [Bug Report] Bad behaviour when image backend or image location not specified

**Status: SOLVED in `4a9c6147b`.** This plan records what was done and
why, so the change can be re-implemented or reviewed without the original
context. It is a description of shipped work, not a proposal.

- Commit: `4a9c6147b` — storage: validate image_location on read, not just on write (fixes #649)
- Area: `storage`
- Issue: https://github.com/stashapp/stash-box/issues/649

## What was wrong

From `docs/track/WORKLOG.md`:

> If `image_location` is not specified, the system still allows images to be
>   created in the database, and it places the image files in the current
>   working directory. If you try to retrieve an image, you _then_ get an error
>   message indicating that the image location has not been specified.
>
> The mechanism is `filepath.Join`, and it is the whole bug:
>
> filepath.Join("", "ab/cd/<id>")   // "ab/cd/<id>"  -- RELATIVE
>
> Not an error, not an absolute path — a relative one. So the read was attempted
> against the process working directory and failed with a bare
> `stat ab/cd/<id>: no such file or directory` that names neither the image nor
> the missing setting. That is precisely the confusing error in the report.
>
> `WriteFile` already called `config.ValidateImageLocation`. `ReadFile` did not.
> The asymmetry *is* the defect: the write refused, the read guessed, and the
> guess was silently wrong rather than loudly refused. One guard, and both
> halves of the backend now agree.
>
> S3 is untouched and deliberately so: it reads its endpoint from
> `GetS3Config`, not `image_location`, so the guard does not apply.

## Files touched

**implementation**

- `internal/config/config.go`
- `internal/storage/file.go`

**test**

- `internal/storage/file_test.go`

## The change

### `internal/config/config.go`

```diff
diff --git a/internal/config/config.go b/internal/config/config.go
index 5a97ff3..8b6b724 100644
--- a/internal/config/config.go
+++ b/internal/config/config.go
@@ -465,6 +465,20 @@ func SetEmailSettingsForTest(host string, port int, user, pw, from, tlsMode stri
+// SetImageLocationForTest overrides image_location for the duration of a test
+// and returns a restore function.
+//
+// Like SetEmailSettingsForTest, this exists only because C is unexported and
+// the storage package has to exercise config-dependent behaviour from a test.
+// C.ImageLocation is empty in the test environment, which is precisely the
+// condition #649 is about, so a test that could not set it could not cover the
+// bug. There is no production caller.
+func SetImageLocationForTest(loc string) func() {
+	prev := C.ImageLocation
+	C.ImageLocation = loc
+	return func() { C.ImageLocation = prev }
+}
+
```

### `internal/storage/file.go`

```diff
diff --git a/internal/storage/file.go b/internal/storage/file.go
index 76d2bdf..41257d5 100644
--- a/internal/storage/file.go
+++ b/internal/storage/file.go
@@ -41,6 +41,18 @@ func (s *FileBackend) DestroyFile(image *models.Image) error {
+	// Validate before deriving the path, for the same reason WriteFile does.
+	//
+	// Without this, GetImageLocation() returns "" and filepath.Join("", "ab/cd")
+	// collapses to the RELATIVE path "ab/cd" -- so the read is attempted against
+	// the process working directory and fails with a bare "no such file or
+	// directory" that says nothing about the real cause. Worse, the write path
+	// had the same hole before it was validated, which is how files ended up
+	// scattered in the working directory in the first place (#649).
+	if err := config.ValidateImageLocation(); err != nil {
+		return nil, 0, err
+	}
+
```

## Tests

- `internal/storage/file_test.go`

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
