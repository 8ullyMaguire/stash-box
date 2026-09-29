# 0525 — Deleted images break the edit forms

**Status: INVESTIGATED, DELIBERATELY NOT CHANGED.** No code change was
made, and none should be made without a concrete reproduction. The
reasoning is recorded here because it is the deliverable: an issue closed
with no explanation gets re-investigated from scratch.

- Issue: https://github.com/stashapp/stash-box/issues/525

## Why no code change

Already fixed upstream in this fork. The candidate tests were written, passed on the current code, and were deleted rather than kept.

## If this is revisited

A test that passes on unfixed code is not regression proof. If a related symptom reappears, confirm the fix is still present in internal/api/loaders.go imageList nil filtering before assuming a regression.

---
