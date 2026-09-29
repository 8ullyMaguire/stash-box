# 0802 — [Bug Report] Removing category is not a valid change

**Status: SOLVED in `8a37eaa8c`.** This plan records what was done and
why, so the change can be re-implemented or reviewed without the original
context. It is a description of shipped work, not a proposal.

- Commit: `8a37eaa8c` — integration test: verify explicit-null handling for category removal (fixes #802)
- Area: `integration test`
- Issue: https://github.com/stashapp/stash-box/issues/802

## What was wrong

From `docs/track/WORKLOG.md`:

> `Error: edit contains no changes` when clearing a tag's category.
>
> The mechanism: gqlgen flattens **"key absent"** and **"key present but null"**
> into the same nil Go pointer. The edit diff tells them apart by reading the raw
> argument map (`pkg/utils.ArgumentsQuery`, `inputArgs.Field("x").IsNull()`). The
> client was writing:
>
> category_id: data.category?.id,      // undefined when cleared → key OMITTED
>
> so the server correctly read "this edit does not touch the category", the diff
> came out empty, and the edit was rejected. `StudioForm` already used the right
> form (`parent_id: data.parent?.id ?? null`); `TagForm` and `SceneForm` did not.
>
> Fixed in `b3d1ff9`: `?? null` in both. `SceneForm`'s `studio_id` was the same
> latent bug, and is the reported symptom of #9.

## Files touched

**test**

- `internal/api/tag_edit_category_removal_integration_test.go`

## The change

## Tests

- `internal/api/tag_edit_category_removal_integration_test.go`

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
