# 1060 — [Bug Report] Fingerprint edit filter masked by favorites

**Status: SOLVED in `06a2c87ba`.** This plan records what was done and
why, so the change can be re-implemented or reviewed without the original
context. It is a description of shipped work, not a proposal.

- Commit: `06a2c87ba` — notifications: keep every triggered notification type (fixes #1060)
- Area: `notifications`
- Issue: https://github.com/stashapp/stash-box/issues/1060

## What was wrong

From `docs/track/WORKLOG.md`:

> Different cause, same "two things should both happen" family.
> `TriggerSceneEditNotifications` has one UNION arm per reason a user should hear
> about a scene edit, and collapsed them with `DISTINCT ON (user_id)` — one row
> per user.
>
> That did not *pick* a type, it **deleted** the others. The reader filters on
> the stored type (`FindNotificationsByUser: type = $n`), which is what made the
> loss visible: a user who both favorited the performer and fingerprinted the
> scene kept one row, so filtering by `FINGERPRINTED_SCENE_EDIT` returned nothing
> while the same notification showed under `FAVORITE_PERFORMER_EDIT`.
>
> Dedup key is now `(user_id, type)`. Also note the original had **no ORDER BY**,
> so which arm won was undefined — the bug came and went with the query plan.
> Keying on type removes the nondeterminism rather than hiding it behind a
> priority column.

## Files touched

**query (generated)**

- `internal/queries/notification.sql.go`
- `internal/queries/querier.go`

**query source**

- `internal/queries/sql/notification.sql`

**test**

- `internal/api/graphql_client_test.go`
- `internal/api/notification_fingerprint_masking_integration_test.go`

## The change

### `internal/queries/notification.sql.go`

```diff
diff --git a/internal/queries/notification.sql.go b/internal/queries/notification.sql.go
index e561fc4..197f2c2 100644
--- a/internal/queries/notification.sql.go
+++ b/internal/queries/notification.sql.go
@@ -369,7 +369,7 @@ func (q *Queries) TriggerSceneCreationNotifications(ctx context.Context, id uuid
-SELECT DISTINCT ON (user_id) user_id, type, $1 FROM (
+SELECT DISTINCT ON (user_id, type) user_id, type, $1 FROM (
@@ -408,6 +408,22 @@ SELECT DISTINCT ON (user_id) user_id, type, $1 FROM (
+// One notification row per (user, type), NOT one per user.
+//
+// Each arm below is an independent reason this user should hear about this
+// edit, and the reader filters on the stored type
+// (FindNotificationsByUser: type = $n). Collapsing to a single row per user
+// with DISTINCT ON (user_id) therefore did not just pick a type, it DELETED
+// the others: a user who both favorited the scene's performer and had
+// submitted a fingerprint for the scene kept only whichever arm came first
+// out of an unordered set, so filtering by FINGERPRINTED_SCENE_EDIT returned
+// nothing (#1060).
+//
+// The arm without an ORDER BY made it worse -- which arm won was undefined,
+// so the bug appeared and disappeared with the query plan.
+//
+// DISTINCT ON (user_id, type) still collapses a user who qualifies twice for
+// the same type, which is the dedup that was actually wanted.
```

### `internal/queries/querier.go`

```diff
diff --git a/internal/queries/querier.go b/internal/queries/querier.go
index 76f4877..c667618 100644
--- a/internal/queries/querier.go
+++ b/internal/queries/querier.go
@@ -365,6 +365,23 @@ type Querier interface {
+	//
+	// One notification row per (user, type), NOT one per user.
+	//
+	// Each arm below is an independent reason this user should hear about this
+	// edit, and the reader filters on the stored type
+	// (FindNotificationsByUser: type = $n). Collapsing to a single row per user
+	// with DISTINCT ON (user_id) therefore did not just pick a type, it DELETED
+	// the others: a user who both favorited the scene's performer and had
+	// submitted a fingerprint for the scene kept only whichever arm came first
+	// out of an unordered set, so filtering by FINGERPRINTED_SCENE_EDIT returned
+	// nothing (#1060).
+	//
+	// The arm without an ORDER BY made it worse -- which arm won was undefined,
+	// so the bug appeared and disappeared with the query plan.
+	//
+	// DISTINCT ON (user_id, type) still collapses a user who qualifies twice for
+	// the same type, which is the dedup that was actually wanted.
```

### `internal/queries/sql/notification.sql`

```diff
diff --git a/internal/queries/sql/notification.sql b/internal/queries/sql/notification.sql
index 866ef68..7c74f7b 100644
--- a/internal/queries/sql/notification.sql
+++ b/internal/queries/sql/notification.sql
@@ -111,8 +111,25 @@ JOIN user_notifications N ON EV.user_id = N.user_id AND N.type = 'UPDATED_EDIT'
+--
+-- One notification row per (user, type), NOT one per user.
+--
+-- Each arm below is an independent reason this user should hear about this
+-- edit, and the reader filters on the stored type
+-- (FindNotificationsByUser: type = $n). Collapsing to a single row per user
+-- with DISTINCT ON (user_id) therefore did not just pick a type, it DELETED
+-- the others: a user who both favorited the scene's performer and had
+-- submitted a fingerprint for the scene kept only whichever arm came first
+-- out of an unordered set, so filtering by FINGERPRINTED_SCENE_EDIT returned
+-- nothing (#1060).
+--
+-- The arm without an ORDER BY made it worse -- which arm won was undefined,
+-- so the bug appeared and disappeared with the query plan.
+--
+-- DISTINCT ON (user_id, type) still collapses a user who qualifies twice for
+-- the same type, which is the dedup that was actually wanted.
-SELECT DISTINCT ON (user_id) user_id, type, $1 FROM (
+SELECT DISTINCT ON (user_id, type) user_id, type, $1 FROM (
```

## Tests

- `internal/api/graphql_client_test.go`
- `internal/api/notification_fingerprint_masking_integration_test.go`

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
