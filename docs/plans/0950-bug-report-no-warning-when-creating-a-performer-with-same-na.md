# 0950 — [Bug Report] No warning when creating a performer with same name+disambiguation

**Status: SOLVED in `b8390eae1`.** This plan records what was done and
why, so the change can be re-implemented or reviewed without the original
context. It is a description of shipped work, not a proposal.

- Commit: `b8390eae1` — performer: reject a duplicate create edit with a message naming the entity (fixes #950)
- Area: `performer`
- Issue: https://github.com/stashapp/stash-box/issues/950

## What was wrong

From `docs/track/WORKLOG.md`:

> Reported: the edit fails at apply time with
>
> Unknown Error: Error creating Performer: pq: duplicate key value violates
> unique constraint "index_active_performers_on_name"
>
> Migration 06:
>
> CREATE UNIQUE INDEX "index_active_performers_on_name" ON "performers"
>   ("name", "disambiguation") WHERE NOT "deleted";
>
> **What I got wrong first, and it took a probe to correct.** I assumed the index
> rejected duplicates whenever they existed, and expected my test to reproduce the
> `pq` error on the pre-fix code. It did not — the edit **applied successfully**,
> no error at all. So I stopped and probed the index directly:
>
> INSERT (name, disambiguation) VALUES ('probe3-950', NULL)   -- ok
> INSERT (name, disambiguation) VALUES ('probe3-950', NULL)   -- ALSO ok
> INSERT (name, disambiguation) VALUES ('probe3-950', '')     -- ALSO ok
>
> -- but with a non-empty disambiguation:
> ERROR: duplicate key value violates unique constraint
>        "index_active_performers_on_name"
>
> **NULLs do not collide in a Postgres unique index**, and `disambiguation` is
> nullable with no default on the edit input. So for a performer with no
> disambiguation — most of them — the database does not reject the duplicate at
> all, and the actual pre-fix behaviour was a **silent second active performer**,
> not a `pq` error.

## Files touched

**implementation**

- `internal/service/edit/edit.go`
- `internal/service/edit/performer.go`

**test**

- `internal/api/performer_duplicate_create_integration_test.go`

## The change

### `internal/service/edit/edit.go`

```diff
diff --git a/internal/service/edit/edit.go b/internal/service/edit/edit.go
index d38d815..18ab0b9 100644
--- a/internal/service/edit/edit.go
+++ b/internal/service/edit/edit.go
@@ -17,6 +17,21 @@ var ErrMergeIDMissing = errors.New("merge target ID is required")
+// ErrPerformerAlreadyExists is returned when a create edit proposes a
+// performer whose name and disambiguation already match an existing one.
+//
+// The database enforces this with a partial unique index
+// (index_active_performers_on_name, migration 06), so without this check the
+// contributor only finds out when the edit is *applied* -- after it has been
+// voted on and closed -- and gets a raw
+//
+//	pq: duplicate key value violates unique constraint "index_active_performers_on_name"
+//
+// which names a database object rather than the entity that already exists
+// (#950). Failing at apply time is also the worst possible moment: the voters
+// have spent their vote on an edit that can never land.
+var ErrPerformerAlreadyExists = errors.New("a performer with this name and disambiguation already exists")
+
```

### `internal/service/edit/performer.go`

```diff
diff --git a/internal/service/edit/performer.go b/internal/service/edit/performer.go
index 54956ff..3c0c895 100644
--- a/internal/service/edit/performer.go
+++ b/internal/service/edit/performer.go
@@ -231,6 +231,38 @@ func (m *PerformerEditProcessor) applyCreate(data *models.PerformerEditData) err
+	// Reject a duplicate before inserting, so the failure names the entity
+	// instead of a Postgres index (#950).
+	//
+	// This mirrors index_active_performers_on_name rather than replacing it:
+	// the index still has the final word if two applies race, but the ordinary
+	// case -- an edit filed for a performer that already exists -- fails here
+	// with something the contributor and the modbot can both act on.
+	//
+	// FindExistingPerformers is the query the frontend already uses for its
+	// duplicate warning, and it matches on the same (name, disambiguation) pair
+	// the index constrains, including treating a nil disambiguation as empty.
+	// FindPerformerByName would have been the wrong tool: it ignores
+	// disambiguation entirely, so it would reject legitimately distinct
+	// performers and miss the case where the disambiguations differ.
+	if data.New.Name != nil {
+		existing, err := m.queries.FindExistingPerformers(m.context, queries.FindExistingPerformersParams{
+			Name:           data.New.Name,
+			Disambiguation: data.New.Disambiguation,
+		})
+		if err != nil {
+			return err
+		}
+
+		for i := range existing {
+			if existing[i].ID == newPerformer.ID {
+				// The same record, e.g. a draft re-applied.
+				continue
+			}
+			return fmt.Errorf("%w: %s", ErrPerformerAlreadyExists, existing[i].Name)
+		}
+	}
+
```

## Tests

- `internal/api/performer_duplicate_create_integration_test.go`

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
