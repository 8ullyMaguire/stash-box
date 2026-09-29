# 0660 — [Bug Report] Limit Field Lengths on Form Fields

**Status: SOLVED in `07ccfaaab`.** This plan records what was done and
why, so the change can be re-implemented or reviewed without the original
context. It is a description of shipped work, not a proposal.

- Commit: `07ccfaaab` — edit: reject overlong field values at edit creation (fixes #660)
- Area: `edit`
- Issue: https://github.com/stashapp/stash-box/issues/660

## What was wrong

From `docs/track/WORKLOG.md`:

> **The real defect.** The form accepts a value longer than its database column,
> the edit passes review, and the write fails much later:
>
> pq: value too long for type character varying(255)
>
> raised by the cron sweep that *applies* edits — not by the contributor who
> typed the value. By then the edit is closed and the votes are wasted. The
> reporter's expectation ("Successful scene creation") is not achievable: the
> column physically cannot hold it, so rejecting early is the only correct
> behaviour.
>
> **Where the limits actually live** — the schema, not Go:
>
> tags.name          varchar(255)     scenes.title      varchar(255)
> tags.description   varchar(255)     studios.name      varchar(255)
>
> **The fix.** `validator.MaxLength` plus `Checked` variants of the
> `*EditFromDiff` / `*EditFromCreate` methods, wired into the create **and**
> modify paths for tag, studio, scene, and performer. A single
> `MaxStringLength` constant is used rather than a per-field table because every
> constrained column in the schema is `varchar(255)`.
>
> Two details that a naive fix gets wrong:
>
> - **Runes, not bytes.** The columns are sized in characters, so a
>   255-character multi-byte name must be accepted. `len(string)` would reject it.
> - **`nil` is never a violation.** `nil` means *either* "not proposed" *or*
>   "explicit deletion" — the #802 distinction. Treating `nil` as a length error

## Files touched

**implementation**

- `internal/models/extension_edit_details.go`
- `internal/models/validator/validator.go`
- `internal/service/edit/performer.go`
- `internal/service/edit/scene.go`
- `internal/service/edit/studio.go`
- `internal/service/edit/tag.go`

**test**

- `internal/api/edit_field_length_integration_test.go`
- `internal/models/field_length_test.go`

## The change

### `internal/models/extension_edit_details.go`

```diff
diff --git a/internal/models/extension_edit_details.go b/internal/models/extension_edit_details.go
index 2740314..4269974 100644
--- a/internal/models/extension_edit_details.go
+++ b/internal/models/extension_edit_details.go
@@ -2,9 +2,34 @@ package models
+	"github.com/stashapp/stash-box/internal/models/validator"
+// validateTagEditLengths rejects values that would overflow their column.
+//
+// Without this the failure surfaces at apply time as
+// `pq: value too long for type character varying(255)` from a background cron
+// sweep, far from the contributor who typed the value (issue #660).
+func (e TagEditDetailsInput) validateTagEditLengths() error {
+	if err := validator.MaxLength("name", e.Name); err != nil {
+		return err
+	}
+	return validator.MaxLength("description", e.Description)
+}
+
+// TagEditFromDiffChecked is TagEditFromDiff with input length validation.
+//
+// It exists alongside TagEditFromDiff rather than changing that function's
+// signature so the existing call sites and their tests keep their shape; the
+// service layer calls the Checked variant.
+func (e TagEditDetailsInput) TagEditFromDiffChecked(orig Tag, inputArgs utils.ArgumentsQuery) (TagEditData, error) {
+	if err := e.validateTagEditLengths(); err != nil {
+		return TagEditData{}, err
+	}
+	return e.TagEditFromDiff(orig, inputArgs), nil
+}
+
@@ -34,6 +59,14 @@ func (e TagEditDetailsInput) TagEditFromMerge(orig Tag, sources []uuid.UUID, inp
+// TagEditFromCreateChecked is TagEditFromCreate with input length validation.
+func (e TagEditDetailsInput) TagEditFromCreateChecked(inputArgs utils.ArgumentsQuery) (TagEditData, error) {
+	if err := e.validateTagEditLengths(); err != nil {
+		return TagEditData{}, err
+	}
+	return e.TagEditFromCreate(inputArgs), nil
+}
+
@@ -42,6 +75,18 @@ func (e TagEditDetailsInput) TagEditFromCreate(inputArgs utils.ArgumentsQuery) T
+// PerformerEditFromDiffChecked is PerformerEditFromDiff with input length
+// validation.
+func (e PerformerEditDetailsInput) PerformerEditFromDiffChecked(orig Performer, inputArgs utils.ArgumentsQuery) (*PerformerEditData, error) {
+	if err := validator.MaxLength("name", e.Name); err != nil {
+		return nil, err
+	}
+	if err := validator.MaxLength("disambiguation", e.Disambiguation); err != nil {
+		return nil, err
+	}
+	return e.PerformerEditFromDiff(orig, inputArgs)
+}
+
@@ -136,6 +181,14 @@ func (e PerformerEditDetailsInput) PerformerEditFromCreate(inputArgs utils.Argum
+// StudioEditFromDiffChecked is StudioEditFromDiff with input length validation.
+func (e StudioEditDetailsInput) StudioEditFromDiffChecked(orig Studio, inputArgs utils.ArgumentsQuery) (*StudioEditData, error) {
+	if err := validator.MaxLength("name", e.Name); err != nil {
+		return nil, err
+	}
+	return e.StudioEditFromDiff(orig, inputArgs)
+}
    ... (trimmed; run `git show` for the full diff)
```

### `internal/models/validator/validator.go`

```diff
diff --git a/internal/models/validator/validator.go b/internal/models/validator/validator.go
index a76145d..c5e3463 100644
--- a/internal/models/validator/validator.go
+++ b/internal/models/validator/validator.go
@@ -87,3 +87,42 @@ func EnumPtr[T StringEnum](field string, old *string, current *T) error {
+
+// ErrFieldTooLong is returned when a submitted value exceeds the length of the
+// database column it will be written to.
+type ErrFieldTooLong struct {
+	Field    string
+	Length   int
+	Max      int
+}
+
+func (e *ErrFieldTooLong) Error() string {
+	return fmt.Sprintf("%s is %d characters; the maximum is %d", e.Field, e.Length, e.Max)
+}
+
+// MaxStringLength is the length of the varchar(255) columns that hold these
+// fields (see internal/database/migrations/postgres/01_initial.up.sql).
+//
+// It is a single constant rather than a per-field table because every
+// constrained column in the schema is declared varchar(255); if a future
+// migration narrows one of them, this constant is the single place to revisit
+// and the tests will fail loudly rather than the change passing silently.
+const MaxStringLength = 255
+
+// MaxLength validates that a submitted string field fits its column.
+//
+// A nil value is accepted: it means the edit does not propose a value for the
+// field, which is the same wire representation as an explicit deletion. Neither
+// is a length violation, and conflating them here would reject ordinary edits
+// (see issue #802 on why null and absent must stay distinct).
+func MaxLength(field string, value *string) error {
+	if value == nil {
+		return nil
+	}
+	// Count runes, not bytes: the columns are sized in characters, so a
+	// 255-character name of multi-byte text must be accepted.
+	if n := len([]rune(*value)); n > MaxStringLength {
+		return &ErrFieldTooLong{Field: field, Length: n, Max: MaxStringLength}
+	}
+	return nil
+}
```

### `internal/service/edit/performer.go`

```diff
diff --git a/internal/service/edit/performer.go b/internal/service/edit/performer.go
index e0760a0..0cfed65 100644
--- a/internal/service/edit/performer.go
+++ b/internal/service/edit/performer.go
@@ -64,7 +64,7 @@ func (m *PerformerEditProcessor) modifyEdit(input models.PerformerEditInput, inp
-	performerEdit, err := input.Details.PerformerEditFromDiff(performer, detailArgs)
+	performerEdit, err := input.Details.PerformerEditFromDiffChecked(performer, detailArgs)
```

### `internal/service/edit/scene.go`

```diff
diff --git a/internal/service/edit/scene.go b/internal/service/edit/scene.go
index 8af49c6..fe7945a 100644
--- a/internal/service/edit/scene.go
+++ b/internal/service/edit/scene.go
@@ -66,7 +66,7 @@ func (m *SceneEditProcessor) modifyEdit(input models.SceneEditInput, inputArgs u
-	sceneEdit, err := input.Details.SceneEditFromDiff(scene, detailArgs)
+	sceneEdit, err := input.Details.SceneEditFromDiffChecked(scene, detailArgs)
```

### `internal/service/edit/studio.go`

```diff
diff --git a/internal/service/edit/studio.go b/internal/service/edit/studio.go
index a59666c..d077ee1 100644
--- a/internal/service/edit/studio.go
+++ b/internal/service/edit/studio.go
@@ -65,7 +65,7 @@ func (m *StudioEditProcessor) modifyEdit(input models.StudioEditInput, inputArgs
-	studioEdit, err := input.Details.StudioEditFromDiff(studio, detailArgs)
+	studioEdit, err := input.Details.StudioEditFromDiffChecked(studio, detailArgs)
@@ -168,7 +168,10 @@ func (m *StudioEditProcessor) mergeEdit(input models.StudioEditInput, inputArgs
-	studioEdit := input.Details.StudioEditFromCreate()
+	studioEdit, err := input.Details.StudioEditFromCreateChecked()
+	if err != nil {
+		return err
+	}
```

### `internal/service/edit/tag.go`

```diff
diff --git a/internal/service/edit/tag.go b/internal/service/edit/tag.go
index d41a102..e46d19c 100644
--- a/internal/service/edit/tag.go
+++ b/internal/service/edit/tag.go
@@ -61,7 +61,10 @@ func (m *TagEditProcessor) modifyEdit(input models.TagEditInput, inputArgs utils
-	tagEdit := input.Details.TagEditFromDiff(tag, detailArgs)
+	tagEdit, err := input.Details.TagEditFromDiffChecked(tag, detailArgs)
+	if err != nil {
+		return err
+	}
@@ -126,7 +129,10 @@ func (m *TagEditProcessor) mergeEdit(input models.TagEditInput, inputArgs utils.
-	tagEdit := input.Details.TagEditFromCreate(inputArgs)
+	tagEdit, err := input.Details.TagEditFromCreateChecked(inputArgs)
+	if err != nil {
+		return err
+	}
```

## Tests

- `internal/api/edit_field_length_integration_test.go`
- `internal/models/field_length_test.go`

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
