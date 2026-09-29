# 1177 — Fingerprinted scenes can be linked multiple times

**Status: INVESTIGATED, DELIBERATELY NOT CHANGED.** No code change was
made, and none should be made without a concrete reproduction. The
reasoning is recorded here because it is the deliverable: an issue closed
with no explanation gets re-investigated from scratch.

- Issue: https://github.com/stashapp/stash-box/issues/1177

## Why no code change

The query already deduplicates, and an OShash id can never also be a Phash id, so the reported duplication is unreachable in this schema.

## If this is revisited

A candidate test passed against unfixed code, which is the only reason the premise was disproved. Both the test and the speculative dedup in internal/service/fingerprint/cluster.go were removed. If this is revisited, write the test FIRST and watch it fail before writing any fix.

---
