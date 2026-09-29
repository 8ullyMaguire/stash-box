# 0943 — [Bug Report] Edits are not updated when entities are merged

**Status: SOLVED in `da26e32b3`.** This plan records what was done and
why, so the change can be re-implemented or reviewed without the original
context. It is a description of shipped work, not a proposal.

- Commit: `da26e32b3` — edit: retarget pending edits when entities are merged (fixes #943)
- Area: `edit`
- Issue: https://github.com/stashapp/stash-box/issues/943

## What was wrong

From `docs/track/WORKLOG.md`:

> A merge soft-deletes the source and writes a redirect. An edit still `PENDING`
> against the source kept addressing the deleted entity: it can never be applied
> and never appears in the survivor's edit list.
>
> Four `:execrows` queries, one per entity, using `sqlc.arg(new_id)` /
> `sqlc.arg(old_id)` — positional `$1/$2` generate a `TagID` / `TagID_2` struct,
> named args generate `OldID` / `NewID`. Only `PENDING` is retargeted: an edit
> that reached a verdict has history voters agreed to.
>
> Wired into all four merge paths (performer, scene, studio, tag).
>
> Evidence — each test **mutation-verified**: deleting only the service-layer
> call, leaving schema and generated queries intact, fails the matching test with
> the edit still addressing the deleted source.
>
> || Test | Mutation result |
> ||---|---|
> || `TestMergeRetargetsPendingTagEdit` | FAIL |
> || `TestMergeRetargetsPendingStudioEdit` | FAIL |
> || `TestMergeRetargetsPendingPerformerEdit` | FAIL |
> || `TestMergeRetargetsPendingSceneEdit` | FAIL |

## Files touched

**implementation**

- `internal/service/edit/performer.go`
- `internal/service/edit/scene.go`
- `internal/service/edit/studio.go`
- `internal/service/edit/tag.go`

**query (generated)**

- `internal/queries/edit.sql.go`
- `internal/queries/querier.go`

**query source**

- `internal/queries/sql/edit.sql`

**test**

- `internal/api/edit_merge_retarget_integration_test.go`

## The change

### `internal/queries/edit.sql.go`

```diff
diff --git a/internal/queries/edit.sql.go b/internal/queries/edit.sql.go
index 5fd2548..4d79a85 100644
--- a/internal/queries/edit.sql.go
+++ b/internal/queries/edit.sql.go
@@ -1454,3 +1454,103 @@ func (q *Queries) UpdateEditData(ctx context.Context, arg UpdateEditDataParams)
+
+const updatePendingPerformerEditsTarget = `-- name: UpdatePendingPerformerEditsTarget :execrows
+UPDATE performer_edits pe
+SET performer_id = $1
+FROM edits e
+WHERE e.id = pe.edit_id
+  AND pe.performer_id = $2
+  AND e.status = 'PENDING'
+`
+
+type UpdatePendingPerformerEditsTargetParams struct {
+	NewID uuid.UUID `db:"new_id" json:"new_id"`
+	OldID uuid.UUID `db:"old_id" json:"old_id"`
+}
+
+// Retarget PENDING performer edits from a merged-away performer to the merge survivor.
+//
+// Issue #943: a merge soft-deletes the source and adds a redirect, but edits still
+// pointing at the source are left addressing a deleted entity. They can never be
+// applied and never show up under the surviving performer's edit list.
+//
+// The target lives in performer_edits, not on edits, so this rewrites the join row.
+// Only PENDING is touched: an edit that already reached ACCEPTED/REJECTED has a
+// verdict whose meaning must not change under the author or voters, and rewriting
+// it would silently re-attribute history.
+//
+// Returns the number of rows retargeted so callers can log the effect.
+func (q *Queries) UpdatePendingPerformerEditsTarget(ctx context.Context, arg UpdatePendingPerformerEditsTargetParams) (int64, error) {
+	result, err := q.db.Exec(ctx, updatePendingPerformerEditsTarget, arg.NewID, arg.OldID)
+	if err != nil {
+		return 0, err
+	}
+	return result.RowsAffected(), nil
+}
+
+const updatePendingSceneEditsTarget = `-- name: UpdatePendingSceneEditsTarget :execrows
+UPDATE scene_edits se
+SET scene_id = $1
+FROM edits e
+WHERE e.id = se.edit_id
+  AND se.scene_id = $2
+  AND e.status = 'PENDING'
+`
+
+type UpdatePendingSceneEditsTargetParams struct {
+	NewID uuid.UUID `db:"new_id" json:"new_id"`
+	OldID uuid.UUID `db:"old_id" json:"old_id"`
+}
+
+func (q *Queries) UpdatePendingSceneEditsTarget(ctx context.Context, arg UpdatePendingSceneEditsTargetParams) (int64, error) {
+	result, err := q.db.Exec(ctx, updatePendingSceneEditsTarget, arg.NewID, arg.OldID)
+	if err != nil {
+		return 0, err
+	}
+	return result.RowsAffected(), nil
    ... (trimmed; run `git show` for the full diff)
```

### `internal/queries/querier.go`

```diff
diff --git a/internal/queries/querier.go b/internal/queries/querier.go
index b5e11e5..76f4877 100644
--- a/internal/queries/querier.go
+++ b/internal/queries/querier.go
@@ -371,6 +371,22 @@ type Querier interface {
+	// Retarget PENDING performer edits from a merged-away performer to the merge survivor.
+	//
+	// Issue #943: a merge soft-deletes the source and adds a redirect, but edits still
+	// pointing at the source are left addressing a deleted entity. They can never be
+	// applied and never show up under the surviving performer's edit list.
+	//
+	// The target lives in performer_edits, not on edits, so this rewrites the join row.
+	// Only PENDING is touched: an edit that already reached ACCEPTED/REJECTED has a
+	// verdict whose meaning must not change under the author or voters, and rewriting
+	// it would silently re-attribute history.
+	//
+	// Returns the number of rows retargeted so callers can log the effect.
+	UpdatePendingPerformerEditsTarget(ctx context.Context, arg UpdatePendingPerformerEditsTargetParams) (int64, error)
+	UpdatePendingSceneEditsTarget(ctx context.Context, arg UpdatePendingSceneEditsTargetParams) (int64, error)
+	UpdatePendingStudioEditsTarget(ctx context.Context, arg UpdatePendingStudioEditsTargetParams) (int64, error)
+	UpdatePendingTagEditsTarget(ctx context.Context, arg UpdatePendingTagEditsTargetParams) (int64, error)
```

### `internal/queries/sql/edit.sql`

```diff
diff --git a/internal/queries/sql/edit.sql b/internal/queries/sql/edit.sql
index 3979a45..6458b87 100644
--- a/internal/queries/sql/edit.sql
+++ b/internal/queries/sql/edit.sql
@@ -70,6 +70,50 @@ INSERT INTO scene_edits (edit_id, scene_id) VALUES ($1, $2);
+-- Retarget PENDING performer edits from a merged-away performer to the merge survivor.
+--
+-- Issue #943: a merge soft-deletes the source and adds a redirect, but edits still
+-- pointing at the source are left addressing a deleted entity. They can never be
+-- applied and never show up under the surviving performer's edit list.
+--
+-- The target lives in performer_edits, not on edits, so this rewrites the join row.
+-- Only PENDING is touched: an edit that already reached ACCEPTED/REJECTED has a
+-- verdict whose meaning must not change under the author or voters, and rewriting
+-- it would silently re-attribute history.
+--
+-- Returns the number of rows retargeted so callers can log the effect.
+-- name: UpdatePendingPerformerEditsTarget :execrows
+UPDATE performer_edits pe
+SET performer_id = sqlc.arg(new_id)
+FROM edits e
+WHERE e.id = pe.edit_id
+  AND pe.performer_id = sqlc.arg(old_id)
+  AND e.status = 'PENDING';
+
+-- name: UpdatePendingSceneEditsTarget :execrows
+UPDATE scene_edits se
+SET scene_id = sqlc.arg(new_id)
+FROM edits e
+WHERE e.id = se.edit_id
+  AND se.scene_id = sqlc.arg(old_id)
+  AND e.status = 'PENDING';
+
+-- name: UpdatePendingStudioEditsTarget :execrows
+UPDATE studio_edits se
+SET studio_id = sqlc.arg(new_id)
+FROM edits e
+WHERE e.id = se.edit_id
+  AND se.studio_id = sqlc.arg(old_id)
+  AND e.status = 'PENDING';
+
+-- name: UpdatePendingTagEditsTarget :execrows
+UPDATE tag_edits te
+SET tag_id = sqlc.arg(new_id)
+FROM edits e
+WHERE e.id = te.edit_id
+  AND te.tag_id = sqlc.arg(old_id)
+  AND e.status = 'PENDING';
+
```

### `internal/service/edit/performer.go`

```diff
diff --git a/internal/service/edit/performer.go b/internal/service/edit/performer.go
index 0cfed65..54956ff 100644
--- a/internal/service/edit/performer.go
+++ b/internal/service/edit/performer.go
@@ -549,6 +549,14 @@ func (m *PerformerEditProcessor) MergeInto(source *models.Performer, target *mod
+	// Update pending edits that target the old performer to point to the new one.
+	if _, err := m.queries.UpdatePendingPerformerEditsTarget(m.context, queries.UpdatePendingPerformerEditsTargetParams{
+		OldID: source.ID,
+		NewID: target.ID,
+	}); err != nil {
+		return err
+	}
+
```

### `internal/service/edit/scene.go`

```diff
diff --git a/internal/service/edit/scene.go b/internal/service/edit/scene.go
index fe7945a..a534daa 100644
--- a/internal/service/edit/scene.go
+++ b/internal/service/edit/scene.go
@@ -652,6 +652,14 @@ func (m *SceneEditProcessor) MergeInto(source queries.Scene, target queries.Scen
+	// Update pending edits that target the old scene to point to the new one.
+	if _, err := m.queries.UpdatePendingSceneEditsTarget(m.context, queries.UpdatePendingSceneEditsTargetParams{
+		OldID: source.ID,
+		NewID: target.ID,
+	}); err != nil {
+		return err
+	}
+
```

### `internal/service/edit/studio.go`

```diff
diff --git a/internal/service/edit/studio.go b/internal/service/edit/studio.go
index d077ee1..eda32b7 100644
--- a/internal/service/edit/studio.go
+++ b/internal/service/edit/studio.go
@@ -436,6 +436,15 @@ func (m *StudioEditProcessor) mergeInto(sourceID uuid.UUID, targetID uuid.UUID)
+	// Retarget edits still waiting on the deleted source so they address the
+	// survivor. Without this they can never be applied (#943).
+	if _, err = m.queries.UpdatePendingStudioEditsTarget(m.context, queries.UpdatePendingStudioEditsTargetParams{
+		OldID: sourceID,
+		NewID: targetID,
+	}); err != nil {
+		return err
+	}
+
```

### `internal/service/edit/tag.go`

```diff
diff --git a/internal/service/edit/tag.go b/internal/service/edit/tag.go
index e46d19c..4850afb 100644
--- a/internal/service/edit/tag.go
+++ b/internal/service/edit/tag.go
@@ -326,6 +326,15 @@ func (m *TagEditProcessor) mergeInto(sourceID uuid.UUID, targetID uuid.UUID) err
+	// Retarget edits still waiting on the deleted source so they address the
+	// survivor. Without this they can never be applied (#943).
+	if _, err = m.queries.UpdatePendingTagEditsTarget(m.context, queries.UpdatePendingTagEditsTargetParams{
+		OldID: sourceID,
+		NewID: targetID,
+	}); err != nil {
+		return err
+	}
+
```

## Tests

- `internal/api/edit_merge_retarget_integration_test.go`

These were mutation-checked: the fix was reverted, the test was run, and
it was required to fail. A test that survives that check is not evidence.

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
