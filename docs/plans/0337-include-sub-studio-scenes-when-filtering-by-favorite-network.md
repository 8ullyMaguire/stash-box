# 0337 — include sub-studio scenes when filtering by favorite network

**Status: SOLVED in `abed6bede`.** This plan records what was done and
why, so the change can be re-implemented or reviewed without the original
context. It is a description of shipped work, not a proposal.

- Commit: `abed6bede` — edits: include sub-studio scenes when filtering by favorite network (fixes #337)
- Area: `edits`
- Issue: https://github.com/stashapp/stash-box/issues/337

## What was wrong

From `docs/track/WORKLOG.md`:

> Same root cause, in the edits favorite filter: `studio_favorites.studio_id =
> scenes.studio_id`, exact. Favoriting a network and seeing none of its
> sub-studios' edits meant favoriting every sub-studio by hand.
>
> Added a UNION arm joining scenes → studios and comparing
> `studios.parent_studio_id` against the favorited id. All three traversals now
> agree on what a network contains instead of each having its own idea.

## Files touched

**implementation**

- `internal/service/edit/query.go`

**test**

- `internal/api/edit_favorite_network_integration_test.go`

## The change

### `internal/service/edit/query.go`

```diff
diff --git a/internal/service/edit/query.go b/internal/service/edit/query.go
index 4b564d3..893b60f 100644
--- a/internal/service/edit/query.go
+++ b/internal/service/edit/query.go
@@ -163,13 +163,30 @@ func (s *Edit) buildEditQuery(psql sq.StatementBuilderType, filter models.EditQu
+		// Studio favorites are matched against a scene's studio, either the
+		// favorited studio itself or that studio's parent (#337). Favoriting a
+		// network and not seeing its sub-studios' scene edits meant favoriting
+		// every sub-studio by hand, which is not what favoriting a network
+		// means. The parent arm is the same one-level traversal the performer
+		// studio filter uses (#974) and the scene query's ParentStudio filter
+		// uses, so all three agree on what a network contains.
+		//
+		// studios.parent_studio_id covers the child arm; joining scenes to
+		// studios and comparing studios.parent_studio_id covers the network arm.
-				(SELECT SE.edit_id FROM studio_favorites TF JOIN scenes S ON TF.studio_id = S.studio_id JOIN scene_edits SE ON S.id = SE.scene_id WHERE TF.user_id = ?)
+				(SELECT SE.edit_id FROM studio_favorites TF
+				 JOIN scenes S ON TF.studio_id = S.studio_id
+				 JOIN scene_edits SE ON S.id = SE.scene_id WHERE TF.user_id = ?)
+				UNION
+				(SELECT SE.edit_id FROM studio_favorites TF
+				 JOIN studios TS ON TS.parent_studio_id = TF.studio_id
+				 JOIN scenes S ON TS.id = S.studio_id
+				 JOIN scene_edits SE ON S.id = SE.scene_id WHERE TF.user_id = ?)
@@ -184,7 +201,8 @@ func (s *Edit) buildEditQuery(psql sq.StatementBuilderType, filter models.EditQu
-		query = query.Where(sq.Expr(favoriteClause, userID, userID, userID, userID, userID, userID))
+		query = query.Where(sq.Expr(favoriteClause,
+			userID, userID, userID, userID, userID, userID, userID))
```

## Tests

- `internal/api/edit_favorite_network_integration_test.go`

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
