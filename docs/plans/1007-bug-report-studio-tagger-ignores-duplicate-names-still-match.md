# 1007 — [Bug Report] Studio Tagger ignores duplicate names + still matches deleted studios

**Status: SOLVED in `9686b955a`.** This plan records what was done and
why, so the change can be re-implemented or reviewed without the original
context. It is a description of shipped work, not a proposal.

- Commit: `9686b955a` — studio/tag/performer: never resolve a soft-deleted entity by id (fixes #1007)
- Area: `studio/tag/performer`
- Issue: https://github.com/stashapp/stash-box/issues/1007

## What was wrong

From `docs/track/WORKLOG.md`:

> When I ran the Stash tagger to tag scenes it did not seem to be working.
>   Scenes have thumbnails but they were not added to my studio. I then found
>   that I could see the studio on the performer but it was deleted.
>
> Three queries, no `deleted` filter at all:
>
> -- name: FindStudio :one     SELECT * FROM studios WHERE id = $1;
> -- name: FindTag :one        SELECT * FROM tags WHERE id = $1;
> -- name: FindPerformer :one  SELECT * FROM performers WHERE id = $1;
>
> Every `FindByName` sibling filters correctly (`AND deleted = false`), so the
> inconsistency was **only in the id path** — the path the tagger uses and the path
> a client holding a stale id uses.
>
> The redirect-aware queries already existed and were already correct. Only the
> drafts path used them.
>
> SELECT S.* FROM studios S
> WHERE S.id = $1 AND S.deleted = FALSE
> UNION
> SELECT SS.* FROM studio_redirects R
> JOIN studios SS ON SS.id = R.target_id
> WHERE R.source_id = $1 AND SS.deleted = FALSE;
>
> `FindByID` now uses it. `FindTagWithRedirect`/`FindPerformerWithRedirect` are
> `:many`, so the first row is taken — the UNION cannot match both arms for one id.
>
> `Studio.Favorite` deliberately keeps the raw `FindStudio`: it is the favorite

## Files touched

**implementation**

- `internal/service/performer/service.go`
- `internal/service/studio/service.go`
- `internal/service/tag/service.go`

**test**

- `internal/api/deleted_entity_lookup_integration_test.go`
- `internal/api/performer_edit_integration_test.go`
- `internal/api/performer_filter_integration_test.go`
- `internal/api/studio_edit_integration_test.go`
- `internal/api/tag_edit_integration_test.go`

## The change

### `internal/service/performer/service.go`

```diff
diff --git a/internal/service/performer/service.go b/internal/service/performer/service.go
index ec9bca2..0f6e481 100644
--- a/internal/service/performer/service.go
+++ b/internal/service/performer/service.go
@@ -42,11 +42,19 @@ func (s *Performer) RefreshPopularityAllTime(ctx context.Context) error {
-	performer, err := s.queries.FindPerformer(ctx, id)
+	// FindPerformerWithRedirect, not FindPerformer: FindPerformer is
+	// `WHERE id = $1` with no deleted filter, so it returns soft-deleted
+	// performers and merged sources instead of their survivors (#1007).
+	performers, err := s.queries.FindPerformerWithRedirect(ctx, id)
-	return converter.PerformerToModelPtr(performer), nil
+	if len(performers) == 0 {
+		return nil, nil
+	}
+	// The UNION can only match one arm for a given id: a live performer, or the
+	// survivor this one was merged into. Never both.
+	return converter.PerformerToModelPtr(performers[0]), nil
```

### `internal/service/studio/service.go`

```diff
diff --git a/internal/service/studio/service.go b/internal/service/studio/service.go
index f5a82e6..f9fee79 100644
--- a/internal/service/studio/service.go
+++ b/internal/service/studio/service.go
@@ -37,7 +37,19 @@ func (s *Studio) WithTxn(fn func(*queries.Queries) error) error {
-	studio, err := s.queries.FindStudio(ctx, id)
+	// FindStudioWithRedirect, not FindStudio: FindStudio is `WHERE id = $1`
+	// with no deleted filter, so it happily returns a soft-deleted studio --
+	// and a merged source instead of its surviving target. That is what the
+	// Stash tagger hit (#1007): it queries by name, and with two studios
+	// sharing a name the deleted one could win, so the tagger linked images to
+	// a record nobody can see.
+	//
+	// FindStudioWithRedirect is the query that was already written to do the
+	// right thing (`AND deleted = FALSE`, plus the redirect hop); only the
+	// drafts path was using it. Now the public findStudio query does too, so
+	// a merged id resolves to the survivor and a deleted id resolves to
+	// nothing.
+	studio, err := s.queries.FindStudioWithRedirect(ctx, id)
```

### `internal/service/tag/service.go`

```diff
diff --git a/internal/service/tag/service.go b/internal/service/tag/service.go
index 42188b1..e859669 100644
--- a/internal/service/tag/service.go
+++ b/internal/service/tag/service.go
@@ -35,11 +35,20 @@ func (s *Tag) WithTxn(fn func(*queries.Queries) error) error {
-	tag, err := s.queries.FindTag(ctx, id)
+	// FindTagWithRedirect, not FindTag: FindTag is `WHERE id = $1` with no
+	// deleted filter, so it returns soft-deleted tags and merged sources
+	// instead of their survivors (#1007 -- same defect as FindStudio, and
+	// pointed out in the same issue).
+	tags, err := s.queries.FindTagWithRedirect(ctx, id)
-	return converter.TagToModelPtr(tag), nil
+	if len(tags) == 0 {
+		return nil, nil
+	}
+	// The UNION can only match one arm for a given id: a live tag, or the
+	// survivor this tag was merged into. Never both.
+	return converter.TagToModelPtr(tags[0]), nil
```

## Tests

- `internal/api/deleted_entity_lookup_integration_test.go`
- `internal/api/performer_edit_integration_test.go`
- `internal/api/performer_filter_integration_test.go`
- `internal/api/studio_edit_integration_test.go`
- `internal/api/tag_edit_integration_test.go`

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
