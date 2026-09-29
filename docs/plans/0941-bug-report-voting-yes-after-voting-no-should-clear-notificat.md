# 0941 — [Bug Report] voting yes after voting no should clear notification

**Status: SOLVED in `fc66fdf1a`.** This plan records what was done and
why, so the change can be re-implemented or reviewed without the original
context. It is a description of shipped work, not a proposal.

- Commit: `fc66fdf1a` — notification: clear DOWNVOTE_OWN_EDIT when a reject vote is changed to accept (fixes #941)
- Area: `notification`
- Issue: https://github.com/stashapp/stash-box/issues/941

## What was wrong

From `docs/track/WORKLOG.md`:

> ### What #941 actually was
>
> A voter rejects an edit, then changes that vote to accept. The author keeps the
> `DOWNVOTE_OWN_EDIT` notification and is told their edit was downvoted when the
> tally shows no reject votes at all.
>
> `resolver_mutation_edit.go` had an `if reject { fire downvote notification }`
> with **no else** — nothing retracted a notification it had already raised.
>
> ### The first fix was wrong, and the full suite caught it
>
> The obvious fix is a paired delete:
>
> DELETE FROM notifications WHERE id = $1 AND type = 'DOWNVOTE_OWN_EDIT';
>
> That passes in isolation. It failed in the full suite, and the failure was
> **not** a flake or a timing issue — it was a genuine data-loss bug:
>
> expected: 1   actual: 2
>
> A `DOWNVOTE_OWN_EDIT` row is keyed on (user, type, edit), **not** per vote, and
> `notifications` has **no unique constraint** (`41_notifications.up.sql`) — so
> each reject vote inserts another row. Unconditionally deleting on the first
> flip would have hidden a *second voter's live rejection* from the author. The
> fix now guards on there being no remaining reject votes:
>
> AND NOT EXISTS (
>     SELECT 1 FROM edit_votes WHERE edit_id = $1 AND vote = 'REJECT'

## Files touched

**implementation**

- `internal/api/resolver_mutation_edit.go`
- `internal/service/notification/service.go`

**query (generated)**

- `internal/queries/notification.sql.go`
- `internal/queries/querier.go`

**query source**

- `internal/queries/sql/notification.sql`

**test**

- `internal/api/notification_integration_test.go`

## The change

### `internal/api/resolver_mutation_edit.go`

```diff
diff --git a/internal/api/resolver_mutation_edit.go b/internal/api/resolver_mutation_edit.go
index 8da269e..3c51801 100644
--- a/internal/api/resolver_mutation_edit.go
+++ b/internal/api/resolver_mutation_edit.go
@@ -95,6 +95,12 @@ func (r *mutationResolver) EditVote(ctx context.Context, input models.EditVoteIn
+		} else {
+			// The voter is no longer rejecting this edit, so any downvote
+			// notification already raised for it is stale — otherwise the author
+			// is told their edit was downvoted when the tally shows no reject
+			// votes at all (issue #941).
+			go r.services.Notification().OnEditDownvoteCleared(context.Background(), edit)
```

### `internal/queries/notification.sql.go`

```diff
diff --git a/internal/queries/notification.sql.go b/internal/queries/notification.sql.go
index 079a4c8..e561fc4 100644
--- a/internal/queries/notification.sql.go
+++ b/internal/queries/notification.sql.go
@@ -11,6 +11,27 @@ import (
+const clearDownvoteEditNotifications = `-- name: ClearDownvoteEditNotifications :exec
+DELETE FROM notifications
+WHERE id = $1
+  AND type = 'DOWNVOTE_OWN_EDIT'
+  AND NOT EXISTS (
+      SELECT 1 FROM edit_votes WHERE edit_id = $1 AND vote = 'REJECT'
+  )
+`
+
+// Only clear once NO reject votes remain on the edit.
+//
+// A DOWNVOTE_OWN_EDIT notification is per (author, edit), not per vote, so it
+// must survive as long as ANY voter is still rejecting. Deleting it whenever
+// one voter flips to accept would silently hide a live rejection from the
+// author (issue #941 is the single-voter case; this guards the multi-voter
+// one).
+func (q *Queries) ClearDownvoteEditNotifications(ctx context.Context, id uuid.UUID) error {
+	_, err := q.db.Exec(ctx, clearDownvoteEditNotifications, id)
+	return err
+}
+
```

### `internal/queries/querier.go`

```diff
diff --git a/internal/queries/querier.go b/internal/queries/querier.go
index 5340b82..b5e11e5 100644
--- a/internal/queries/querier.go
+++ b/internal/queries/querier.go
@@ -12,6 +12,14 @@ import (
+	// Only clear once NO reject votes remain on the edit.
+	//
+	// A DOWNVOTE_OWN_EDIT notification is per (author, edit), not per vote, so it
+	// must survive as long as ANY voter is still rejecting. Deleting it whenever
+	// one voter flips to accept would silently hide a live rejection from the
+	// author (issue #941 is the single-voter case; this guards the multi-voter
+	// one).
+	ClearDownvoteEditNotifications(ctx context.Context, id uuid.UUID) error
```

### `internal/queries/sql/notification.sql`

```diff
diff --git a/internal/queries/sql/notification.sql b/internal/queries/sql/notification.sql
index cb08e38..866ef68 100644
--- a/internal/queries/sql/notification.sql
+++ b/internal/queries/sql/notification.sql
@@ -80,6 +80,21 @@ FROM edits E
+-- name: ClearDownvoteEditNotifications :exec
+-- Only clear once NO reject votes remain on the edit.
+--
+-- A DOWNVOTE_OWN_EDIT notification is per (author, edit), not per vote, so it
+-- must survive as long as ANY voter is still rejecting. Deleting it whenever
+-- one voter flips to accept would silently hide a live rejection from the
+-- author (issue #941 is the single-voter case; this guards the multi-voter
+-- one).
+DELETE FROM notifications
+WHERE id = $1
+  AND type = 'DOWNVOTE_OWN_EDIT'
+  AND NOT EXISTS (
+      SELECT 1 FROM edit_votes WHERE edit_id = $1 AND vote = 'REJECT'
+  );
+
```

### `internal/service/notification/service.go`

```diff
diff --git a/internal/service/notification/service.go b/internal/service/notification/service.go
index 86b876c..02e02c8 100644
--- a/internal/service/notification/service.go
+++ b/internal/service/notification/service.go
@@ -241,6 +241,22 @@ func (s *Notification) OnEditDownvote(ctx context.Context, edit *models.Edit) {
+// OnEditDownvoteCleared retracts the DOWNVOTE_OWN_EDIT notifications raised
+// for an edit.
+//
+// A voter who rejects an edit and then changes their vote to accept still
+// leaves the notification behind, so the edit author is told their edit was
+// downvoted when the tally shows no reject votes at all (issue #941). The
+// downvote is no longer true, so the notification has to go.
+//
+// Scoped to the DOWNVOTE_OWN_EDIT type so the other notifications attached to
+// the same edit (comments, favourites, fingerprints) are untouched.
+func (s *Notification) OnEditDownvoteCleared(ctx context.Context, edit *models.Edit) {
+	if err := s.queries.ClearDownvoteEditNotifications(ctx, edit.ID); err != nil {
+		logger.Errorf("Failed to clear downvote edit notifications: %v", err)
+	}
+}
+
```

## Tests

- `internal/api/notification_integration_test.go`

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
