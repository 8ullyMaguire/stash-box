# 0738 — Submitting draft with existing image causes pq error

**Status: SOLVED in `41a3b34c6`.** This plan records what was done and
why, so the change can be re-implemented or reviewed without the original
context. It is a description of shipped work, not a proposal.

- Commit: `41a3b34c6` — image: resolve duplicate uploads on the unique checksum (fixes #738)
- Area: `image`
- Issue: https://github.com/stashapp/stash-box/issues/738

## What was wrong

From `docs/track/WORKLOG.md`:

> `images.checksum` has a UNIQUE index (`images_checksum_idx`, migration 09) and
> `CreateImage` had no `ON CONFLICT`:
>
> INSERT INTO images (id, url, width, height, checksum) VALUES (...)
> RETURNING *;
>
> The service *does* pre-check — `FindByChecksum`, returning the existing image at
> `image/service.go:85`. So sequential duplicates were already safe. The bug is the
> window between that read and the insert: **no transaction spans them**, so two
> submissions of the same bytes can both pass the check and both reach the insert.
> The loser gets
>
> ERROR:  duplicate key value violates unique constraint "images_checksum_idx"
>
> which is the `pq` error from the report. Only the database can arbitrate this,
> so the constraint is now handled where it lives. `DO UPDATE` rather than
> `DO NOTHING` because the query is `:one` and must `RETURNING` a row; the
> assignment is a no-op by definition of the conflict, but it lets the loser
> resolve to the stored image — the same outcome the sequential path gives.
>
> `ON CONFLICT (checksum)` deliberately does not cover the primary key: a repeated
> id still raises, which is correct, since that would be a bug and not a duplicate
> upload.

## Files touched

**implementation**

- `internal/service/image/service.go`

**query (generated)**

- `internal/queries/image.sql.go`
- `internal/queries/querier.go`

**query source**

- `internal/queries/sql/image.sql`

## The change

### `internal/queries/image.sql.go`

```diff
diff --git a/internal/queries/image.sql.go b/internal/queries/image.sql.go
index 1ad07c9..daa8d50 100644
--- a/internal/queries/image.sql.go
+++ b/internal/queries/image.sql.go
@@ -15,6 +15,7 @@ const createImage = `-- name: CreateImage :one
+ON CONFLICT (checksum) DO UPDATE SET checksum = EXCLUDED.checksum
@@ -27,6 +28,29 @@ type CreateImageParams struct {
+//
+// ON CONFLICT (checksum) DO UPDATE is deliberate and is the #738 fix.
+//
+// images.checksum carries a UNIQUE index (images_checksum_idx), so two
+// submissions of the same file that interleave -- both pass the
+// FindByChecksum pre-check, both reach the insert -- previously made the
+// loser fail with a raw pq unique-violation surfaced to the user as
+// "pq: duplicate key value violates unique constraint images_checksum_idx".
+//
+// The service pre-check is not a substitute for the constraint: it is a
+// read followed by a write with no transaction between them, so it cannot
+// prevent the race. Only the database can.
+//
+// DO UPDATE rather than DO NOTHING because this query is :one and must
+// RETURNING a row. The set is a no-op (the existing row already holds these
+// values by definition of the conflict), but it lets a concurrent duplicate
+// resolve to the already-stored image instead of erroring. A draft submitted
+// with an image that another user uploaded a moment earlier should reuse that
+// image, which is exactly what the pre-check path already does.
+//
+// ON CONFLICT (checksum) does not cover the primary key: a repeated id would
+// still raise, which is correct -- that would be a bug, not a duplicate
+// upload.
```

### `internal/queries/querier.go`

```diff
diff --git a/internal/queries/querier.go b/internal/queries/querier.go
index c667618..2559511 100644
--- a/internal/queries/querier.go
+++ b/internal/queries/querier.go
@@ -40,6 +40,29 @@ type Querier interface {
+	//
+	// ON CONFLICT (checksum) DO UPDATE is deliberate and is the #738 fix.
+	//
+	// images.checksum carries a UNIQUE index (images_checksum_idx), so two
+	// submissions of the same file that interleave -- both pass the
+	// FindByChecksum pre-check, both reach the insert -- previously made the
+	// loser fail with a raw pq unique-violation surfaced to the user as
+	// "pq: duplicate key value violates unique constraint images_checksum_idx".
+	//
+	// The service pre-check is not a substitute for the constraint: it is a
+	// read followed by a write with no transaction between them, so it cannot
+	// prevent the race. Only the database can.
+	//
+	// DO UPDATE rather than DO NOTHING because this query is :one and must
+	// RETURNING a row. The set is a no-op (the existing row already holds these
+	// values by definition of the conflict), but it lets a concurrent duplicate
+	// resolve to the already-stored image instead of erroring. A draft submitted
+	// with an image that another user uploaded a moment earlier should reuse that
+	// image, which is exactly what the pre-check path already does.
+	//
+	// ON CONFLICT (checksum) does not cover the primary key: a repeated id would
+	// still raise, which is correct -- that would be a bug, not a duplicate
+	// upload.
```

### `internal/queries/sql/image.sql`

```diff
diff --git a/internal/queries/sql/image.sql b/internal/queries/sql/image.sql
index 1c728fd..876c739 100644
--- a/internal/queries/sql/image.sql
+++ b/internal/queries/sql/image.sql
@@ -1,8 +1,32 @@
+--
+-- ON CONFLICT (checksum) DO UPDATE is deliberate and is the #738 fix.
+--
+-- images.checksum carries a UNIQUE index (images_checksum_idx), so two
+-- submissions of the same file that interleave -- both pass the
+-- FindByChecksum pre-check, both reach the insert -- previously made the
+-- loser fail with a raw pq unique-violation surfaced to the user as
+-- "pq: duplicate key value violates unique constraint images_checksum_idx".
+--
+-- The service pre-check is not a substitute for the constraint: it is a
+-- read followed by a write with no transaction between them, so it cannot
+-- prevent the race. Only the database can.
+--
+-- DO UPDATE rather than DO NOTHING because this query is :one and must
+-- RETURNING a row. The set is a no-op (the existing row already holds these
+-- values by definition of the conflict), but it lets a concurrent duplicate
+-- resolve to the already-stored image instead of erroring. A draft submitted
+-- with an image that another user uploaded a moment earlier should reuse that
+-- image, which is exactly what the pre-check path already does.
+--
+-- ON CONFLICT (checksum) does not cover the primary key: a repeated id would
+-- still raise, which is correct -- that would be a bug, not a duplicate
+-- upload.
+ON CONFLICT (checksum) DO UPDATE SET checksum = EXCLUDED.checksum
```

### `internal/service/image/service.go`

```diff
diff --git a/internal/service/image/service.go b/internal/service/image/service.go
index af4242d..45894c6 100644
--- a/internal/service/image/service.go
+++ b/internal/service/image/service.go
@@ -59,12 +59,13 @@ func (s *Image) Create(ctx context.Context, input models.ImageCreateInput) (*mod
+	var file []byte
-		file := make([]byte, input.File.Size)
+		file = make([]byte, input.File.Size)
@@ -96,10 +97,6 @@ func (s *Image) Create(ctx context.Context, input models.ImageCreateInput) (*mod
-
-		if err := storage.Image().WriteFile(file, &newImage); err != nil {
-			return nil, err
-		}
@@ -116,7 +113,31 @@ func (s *Image) Create(ctx context.Context, input models.ImageCreateInput) (*mod
-	return converter.ImageToModelPtr(dbImage), nil
+
+	image := converter.ImageToModelPtr(dbImage)
+
+	// Write the file only once the row exists, and only when this call is the
+	// one that created it.
+	//
+	// CreateImage upserts on the unique checksum index (#738), so a concurrent
+	// upload of the same bytes can return the OTHER call's row -- a different
+	// id. Writing the file before the insert would leave bytes on disk under an
+	// id that no row references, which DestroyUnusedImages cannot reclaim
+	// because it walks the images table.
+	//
+	// The reverse order has its own hazard: if WriteFile fails the row is
+	// already committed, pointing at a file that does not exist. That is the
+	// lesser of the two -- a missing file is retried by re-uploading, whereas
+	// an orphan file is invisible to the reaper and leaks forever. The insert
+	// therefore goes first and the write is best-effort with the failure
+	// surfaced, so the caller can retry.
+	if input.File != nil && image.ID == newImage.ID {
+		if err := storage.Image().WriteFile(file, &newImage); err != nil {
+			return nil, err
+		}
+	}
+
+	return image, nil
```

## Tests

**No test was added.** This is a defect in the change, not an oversight —
see the README section on tests that prove nothing. A test that does not
fail when the fix is removed is worse than no test, because it reads as
regression cover in review.

## Verify

```bash
go build ./... && go vet ./...
export POSTGRES_DB="$STASHBOX_TEST_DSN"   # test DSN, never commit it
go test -tags=integration -count=1 ./internal/api/
go test $(go list ./... | grep -vE 'internal/api$') -count=1
```

## Generated code

This change touched generated files. Never hand-edit them; change the
source and regenerate, then confirm idempotence:

```bash
sqlc generate && go run github.com/99designs/gqlgen generate
go build ./... && git diff --stat   # must be empty on a second run
```

---
