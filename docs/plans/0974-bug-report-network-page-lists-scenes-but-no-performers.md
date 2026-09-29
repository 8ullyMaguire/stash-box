# 0974 — [Bug Report] Network page lists scenes but no performers

**Status: SOLVED in `613f21ed6`.** This plan records what was done and
why, so the change can be re-implemented or reviewed without the original
context. It is a description of shipped work, not a proposal.

- Commit: `613f21ed6` — performer: include sub-studio scenes in a network's performer list (fixes #974)
- Area: `performer`
- Issue: https://github.com/stashapp/stash-box/issues/974

## What was wrong

From `docs/track/WORKLOG.md`:

> A network holds no scenes of its own; its content lives on sub-studios. The
> page's **All Scenes** tab filters on `studios.parent_studio_id` and shows
> content. **Performers** passed `studio_id` straight through, and the performer
> query matched it *exactly* against `scenes.studio_id` — so a network returned
> nothing.
>
> The studio filter now covers the studio **and its direct children**, mirroring
> the scene query's `ParentStudio` filter (`scene/query.go:90`) so the two tabs
> on one page cannot disagree. One level only, matching that filter and the
> search triggers' own `TP ON T.parent_studio_id = TP.id`.
>
> `applyPerformerSort` needed no change — its subqueries are studio-agnostic and
> guarded by `if !needsStudioJoin`, so they reuse the corrected alias. Left alone,
> sorting by scene count on a network page would have disagreed with the
> filtered set.

## Files touched

**implementation**

- `internal/service/performer/query.go`

**test**

- `internal/api/studio_network_performers_integration_test.go`

## The change

### `internal/service/performer/query.go`

```diff
diff --git a/internal/service/performer/query.go b/internal/service/performer/query.go
index 9b3e6bf..cdbe6a1 100644
--- a/internal/service/performer/query.go
+++ b/internal/service/performer/query.go
@@ -59,6 +59,24 @@ func (s *Performer) buildPerformerQuery(psql sq.StatementBuilderType, input mode
+	// A network/parent studio holds no scenes of its own -- its content lives
+	// on its sub-studios. Matching studio_id exactly therefore returns nothing
+	// for a network page (#974), while the sibling "All Scenes" tab does show
+	// content because it filters on studios.parent_studio_id.
+	//
+	// So the studio filter covers the studio itself AND its direct children,
+	// mirroring the scene query's ParentStudio filter exactly, so the two tabs
+	// on a network page cannot disagree about what belongs to the network.
+	//
+	// Deliberately one level, matching that scene filter. A recursive walk
+	// would make the two paths disagree in the other direction, and the data
+	// model treats networks as a single level (search triggers join
+	// TP ON T.parent_studio_id = TP.id).
+	//
+	// scenes holds studio_id; studios holds its own id and parent_studio_id.
+	// Both are bound to the same studio id.
+	studioFilter := "(scenes.studio_id = ? OR studios.parent_studio_id = ?)"
+
@@ -66,9 +84,11 @@ func (s *Performer) buildPerformerQuery(psql sq.StatementBuilderType, input mode
-					JOIN scenes ON scene_id = id AND studio_id = ?
+					JOIN scenes ON scene_id = id
+					JOIN studios ON scenes.studio_id = studios.id
+					WHERE `+studioFilter+`
-				) D ON performers.id = D.performer_id`, input.StudioID)
+				) D ON performers.id = D.performer_id`, input.StudioID, input.StudioID)
@@ -78,9 +98,11 @@ func (s *Performer) buildPerformerQuery(psql sq.StatementBuilderType, input mode
-					JOIN scenes ON scene_id = id AND studio_id = ?
+					JOIN scenes ON scene_id = id
+					JOIN studios ON scenes.studio_id = studios.id
+					WHERE `+studioFilter+`
-				) D ON performers.id = D.performer_id`, input.StudioID)
+				) D ON performers.id = D.performer_id`, input.StudioID, input.StudioID)
```

## Tests

- `internal/api/studio_network_performers_integration_test.go`

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
