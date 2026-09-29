# 0778 — libvips availability on a bare host

**Status: INVESTIGATED, DELIBERATELY NOT CHANGED.** No code change was
made, and none should be made without a concrete reproduction. The
reasoning is recorded here because it is the deliverable: an issue closed
with no explanation gets re-investigated from scratch.

- Issue: https://github.com/stashapp/stash-box/issues/778

## Why no code change

Depends on CGO and system libraries being present; environmental, not a code defect.

## If this is revisited

The check that matters is that the test build still compiles where libvips is absent; see the build-tag split in internal/image.

---
