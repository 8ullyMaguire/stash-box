# 1205 — [Bug Report] Low quality Performer image prioritization

**Status: SOLVED in `6a2377041`.** This plan records what was done and
why, so the change can be re-implemented or reviewed without the original
context. It is a description of shipped work, not a proposal.

- Commit: `6a2377041` — image: let resolution outrank aspect ratio for performer portraits (fixes #1205)
- Area: `image`
- Issue: https://github.com/stashapp/stash-box/issues/1205

## What was wrong

From `docs/track/WORKLOG.md`:

> The intent of #1089 was to prioritize a 2:3 image aspect ratio. ... in some
>   cases, very low resolution photos are chosen over more suitable ones. ...
>   For example, a 200x300 image (a perfect 2:3 ratio) gets prioritized over a
>   1200x2000 image.
>
> `OrderPortrait` compared ratio distance first, height only as a tie-break. A
> 200×300 thumbnail is exactly 2:3 and won outright. `performer.Images` returns
> this order and **the first entry is the display image** — visible, not cosmetic.
>
> Fixed by bucketing pixel area (800 000) and comparing buckets *before* ratios.
> Buckets rather than raw areas, because the report asks for the ratio to become
> secondary only on a **major** difference — same tier still means ratio decides,
> so #1089 survives where it should. "Biggest always wins" would have passed the
> reported example and broken the point of #1089.
>
> **Two existing cases changed, and both changes are the fix working:**
>
> expected: 400x600, 422x600, 1080x1920, 640x480, 600x400, 1920x1080
> actual:   1080x1920, 1920x1080, 400x600, 422x600, 640x480, 600x400
>
> 400×600 is the ideal 2:3 at 240k pixels; 1080×1920 is 2.07M. Each pair is one
> bucket, so the ratio comparison still orders them internally — only the tiers
> moved. The second case swaps the same two images and the property it pins (a
> zero-width image sorts last) is unaffected.
>
> Six new tests, two of them guards against "resolution always wins":
>
> - `TestOrderPortraitStillPrefersIdealRatioAtComparableResolution` — two large

## Files touched

**implementation**

- `internal/image/sort.go`

**test**

- `internal/image/portrait_quality_test.go`
- `internal/image/sort_test.go`

## The change

### `internal/image/sort.go`

```diff
diff --git a/internal/image/sort.go b/internal/image/sort.go
index 4055355..4a3a4a5 100644
--- a/internal/image/sort.go
+++ b/internal/image/sort.go
@@ -24,12 +24,49 @@ func OrderLandscape(p []models.Image) {
-// Sorts by distance from ideal aspect ratio of 2:3; ties broken by largest --> smallest height.
+// portraitResolutionFloor is the pixel area below which an image is treated as
+// too small to be chosen on aspect ratio alone.
+//
+// #1089 prioritized the 2:3 portrait ratio, and that is still the primary
+// criterion -- but ratio was the ONLY criterion, with height as a tie-break, so
+// resolution could never outrank a better ratio. A 200x300 thumbnail is exactly
+// 2:3 and beat a 1200x2000 portrait (#1205).
+//
+// The floor is a band, not a rule that resolution always wins. Two images within
+// one step of this floor of each other are still ordered by ratio, so #1089's
+// intent is preserved for the normal case where all the candidate images are a
+// similar size. Only a MAJOR difference in resolution is allowed to override the
+// ratio, which is what the report asks for.
+//
+// 800*1000 = 800_000 pixels, roughly a 900x900 image: comfortably above the
+// thumbnails and phone-wallpaper crops that caused the report, and below the
+// 1200x2000+ portraits it is protecting.
+const portraitResolutionFloor = 800_000
+
+// resolutionBucket returns the image's area divided by the floor.
+//
+// Comparing buckets rather than raw areas is what makes this a band: two images
+// in the same bucket are treated as the same resolution tier and fall through to
+// the aspect-ratio comparison, while a large gap in buckets dominates it.
+func resolutionBucket(img models.Image) int {
+	return int(img.Width) * int(img.Height) / portraitResolutionFloor
+}
+
+// Sorts by distance from ideal aspect ratio of 2:3, with resolution as the
+// primary discriminator when the candidates differ substantially in size; ties
+// broken by largest --> smallest height.
+		// A major resolution difference outranks the ratio (#1205). Without
+		// this, a perfect-ratio thumbnail sorts above a high-resolution
+		// portrait, and since the first entry is the performer's display
+		// image, the UI showed the low-quality one.
+		if bucketA, bucketB := resolutionBucket(p[a]), resolutionBucket(p[b]); bucketA != bucketB {
+			return bucketA > bucketB
+		}
```

## Tests

- `internal/image/portrait_quality_test.go`
- `internal/image/sort_test.go`

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
