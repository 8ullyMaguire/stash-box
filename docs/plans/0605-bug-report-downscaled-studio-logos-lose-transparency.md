# 0605 — [Bug Report] Downscaled Studio Logos Lose Transparency

**Status: SOLVED in `3a28581e1`.** This plan records what was done and
why, so the change can be re-implemented or reviewed without the original
context. It is a description of shipped work, not a proposal.

- Commit: `3a28581e1` — image: set Content-Type on resized images so logos actually render (fixes #605)
- Area: `image`
- Issue: https://github.com/stashapp/stash-box/issues/605

## What was wrong

From `docs/track/WORKLOG.md`:

> The report has three claims. **Two are wrong, and establishing which was most of
> the work:**
>
> 1. *"Stash-Box automatically downscales the image into a JPG"* — there is no
>    conversion on upload. `image.Resize` is called from
>    `internal/api/routes_image.go` on the **serving** path only, when a client asks
>    for a size. The row and the file on disk are never touched.
> 2. *"into a JPG"* — a PNG comes back as **lossless WebP**, which has a real
>    alpha channel. `resize_unix.go` has `if format == vips.ImageTypePNG { ... }`.
> 3. The transparency *is* lost, and the cause is neither: **the resize branch set
>    no `Content-Type` at all.** Go sniffs the body, has no WebP detector, and
>    returns `application/octet-stream` — which browsers refuse to render. A logo
>    requested at a reduced size simply did not display, which reads to a user as
>    a logo with a black background.
>
> **The obvious suspect was wrong, and a probe settled it.** `vips.InterestingNone`
> does not add an alpha channel, so it looked like the culprit. Running *every*
> `vips.Interesting` value over a 2000×2000 transparent PNG:
>
>     None bands=4   All bands=4   Last bands=4   Centre bands=4
>     Entropy bands=4   Attention bands=4   Low bands=4   High bands=4
>
> All four bands, every time. The alpha was never being dropped in the resizer.
> `TestResizePreservesTransparency` now pins that, so a future change cannot
> reintroduce a JPEG conversion and quietly destroy the alpha — which is the
> reporter's failure mode even though it is not today's bug.
>
> **A mutant survived the first four tests — the fifth exists because of it:**

## Files touched

**implementation**

- `internal/api/routes_image.go`

**test**

- `internal/api/resized_content_type_integration_test.go`
- `internal/api/tag_edit_category_removal_integration_test.go`
- `internal/image/resize_alpha_test.go`

## The change

### `internal/api/routes_image.go`

```diff
diff --git a/internal/api/routes_image.go b/internal/api/routes_image.go
index 6fd9ea7..0a09e0b 100644
--- a/internal/api/routes_image.go
+++ b/internal/api/routes_image.go
@@ -125,6 +125,19 @@ func (rs imageRoutes) image(w http.ResponseWriter, r *http.Request) {
+		// Set the content type for the bytes we are about to write (#605).
+		//
+		// The resizer does not preserve the source format: a PNG comes back as
+		// WebP (lossless, which does keep the alpha channel) and everything else
+		// comes back as JPEG. Without an explicit header, Go sniffs the body and
+		// the response is labelled application/octet-stream, which browsers
+		// refuse to render -- so a studio logo requested at a reduced size did
+		// not display at all, and a logo that did display lost its
+		// transparency in clients that fell back to a renderer.
+		//
+		// The stored file is untouched: this only affects the response.
+		w.Header().Set("Content-Type", ResizedContentType(data))
+
@@ -186,6 +199,31 @@ func (rs imageRoutes) siteImage(w http.ResponseWriter, r *http.Request) {
+// ResizedContentType returns the Content-Type for bytes produced by
+// image.Resize.
+//
+// The format is decided by the resizer, not by the stored file: PNG in, lossless
+// WebP out (which preserves the alpha channel a studio logo needs), everything
+// else in, JPEG out. Detecting from the bytes rather than from the source image
+// is the only reliable option, because the two do not correspond.
+//
+// Falls back to image/jpeg, matching the resizer's default output, so an
+// unrecognised body is still labelled as an image rather than as
+// application/octet-stream -- which browsers refuse to render (#605).
+func ResizedContentType(data []byte) string {
+	if len(data) >= 12 &&
+		string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
+		return "image/webp"
+	}
+	if len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF {
+		return "image/jpeg"
+	}
+	if len(data) >= 8 && string(data[1:4]) == "PNG" {
+		return "image/png"
+	}
+	return "image/jpeg"
+}
+
```

## Tests

- `internal/api/resized_content_type_integration_test.go`
- `internal/api/tag_edit_category_removal_integration_test.go`
- `internal/image/resize_alpha_test.go`

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
