# 0621 — [Bug Report] "Failed to load edits." after deleting a 'site' that is used in a pending edit.

**Status: SOLVED in `7853f0dbc`.** This plan records what was done and
why, so the change can be re-implemented or reviewed without the original
context. It is a description of shipped work, not a proposal.

- Commit: `7853f0dbc` — edit: drop URLs whose site was deleted (fixes #621)
- Area: `edit`
- Issue: https://github.com/stashapp/stash-box/issues/621

## What was wrong

From `docs/track/WORKLOG.md`:

> 1. Create a new site
>   2. Create a pending edit to create or modify a scene by adding a link of
>      that site type
>   3. Delete the site
>   4. Go to /edits and enjoy your `Error: Failed to load edits.`
>
> `GetMergedURLsForEdit` builds its result from two sources with **different
> lifetime rules**, which is the whole bug:
>
> | source | what it is | when a site is deleted |
> |---|---|---|
> | `current_urls` | `scene_urls`/`performer_urls`/`studio_urls` | foreign key — the row cascades away |
> | `added_urls` | `jsonb_array_elements(data->'new_data'->'added_urls')` | **not a foreign key, not cascaded** |
>
> So the site's own URLs vanished but the pending edit kept a dangling `site_id`
> in its JSON payload. That id reached `URL.site`, declared non-null as
> `site: Site!` (`misc.graphql:25`), so the dataloader's nil became
>
> the requested element is null which the schema does not allow
> path: [findEdit, details, urls, 0, site]
>
> and failed the **entire page** — one bad row taking out every edit on it.
>
> Fixed by joining `sites` in the final SELECT: a URL with no site left to render
> is not in the list at all.

## Files touched

**implementation**

- `internal/api/resolver_model_url.go`

**query (generated)**

- `internal/queries/edit.sql.go`
- `internal/queries/querier.go`

**query source**

- `internal/queries/sql/edit.sql`

**test**

- `internal/api/edit_deleted_site_url_integration_test.go`

## The change

### `internal/api/resolver_model_url.go`

```diff
diff --git a/internal/api/resolver_model_url.go b/internal/api/resolver_model_url.go
index bc24bdf..9b5aff9 100644
--- a/internal/api/resolver_model_url.go
+++ b/internal/api/resolver_model_url.go
@@ -23,5 +23,17 @@ func (r *urlResolver) Type(ctx context.Context, obj *models.URL) (string, error)
-	return strings.ToUpper(site.Name), err
+
+	// A missing site must not panic. Site is declared non-null, so a URL with a
+	// dangling site_id should already be filtered out upstream
+	// (GetMergedURLsForEdit joins sites), but this resolver is reachable from
+	// any model carrying a URL and the schema's own guarantee is not something
+	// to dereference blindly: a nil here is a panic that takes the request
+	// handler with it, which is strictly worse than an empty string on a
+	// deprecated field.
+	if site == nil {
+		return "", nil
+	}
+
+	return strings.ToUpper(site.Name), nil
```

### `internal/queries/edit.sql.go`

```diff
diff --git a/internal/queries/edit.sql.go b/internal/queries/edit.sql.go
index 4d79a85..ae1f7fc 100644
--- a/internal/queries/edit.sql.go
+++ b/internal/queries/edit.sql.go
@@ -1239,8 +1239,9 @@ final_urls AS (
-SELECT DISTINCT url, site_id FROM final_urls
-ORDER BY url
+SELECT DISTINCT final_urls.url, final_urls.site_id FROM final_urls
+JOIN sites s ON s.id = final_urls.site_id
+ORDER BY final_urls.url
@@ -1249,6 +1250,25 @@ type GetMergedURLsForEditRow struct {
+// Drop any URL whose site no longer exists.
+//
+// This is the #621 fix. current_urls is safe already: scene_urls/performer_urls/
+// studio_urls carry a foreign key to sites, so deleting a site cascades the
+// join rows away. But added_urls is read straight out of the edit's JSON
+// payload, which is not a foreign key and is not cascaded -- a site deleted
+// after the edit was filed left a dangling site_id behind.
+//
+// The dangling id then reached URL.site, which the schema declares non-null
+// (`site: Site!`), so the dataloader's nil became a GraphQL null-in-non-null
+// error and the whole /edits page failed to render -- one bad row taking out
+// every edit on the page. URL.type additionally dereferenced the nil site
+// without a check.
+//
+// Filtering here rather than relaxing the schema to `site: Site` is
+// deliberate: the frontend's URLFragment requires site { id name icon
+// category { ... } } to render a row at all, so a null site would not fix the
+// page, it would trade a loud failure for blank or broken rows. A URL whose
+// site is gone has no site to render, so it does not belong in the list.
```

### `internal/queries/querier.go`

```diff
diff --git a/internal/queries/querier.go b/internal/queries/querier.go
index 2559511..d2beb44 100644
--- a/internal/queries/querier.go
+++ b/internal/queries/querier.go
@@ -303,6 +303,25 @@ type Querier interface {
+	// Drop any URL whose site no longer exists.
+	//
+	// This is the #621 fix. current_urls is safe already: scene_urls/performer_urls/
+	// studio_urls carry a foreign key to sites, so deleting a site cascades the
+	// join rows away. But added_urls is read straight out of the edit's JSON
+	// payload, which is not a foreign key and is not cascaded -- a site deleted
+	// after the edit was filed left a dangling site_id behind.
+	//
+	// The dangling id then reached URL.site, which the schema declares non-null
+	// (`site: Site!`), so the dataloader's nil became a GraphQL null-in-non-null
+	// error and the whole /edits page failed to render -- one bad row taking out
+	// every edit on the page. URL.type additionally dereferenced the nil site
+	// without a check.
+	//
+	// Filtering here rather than relaxing the schema to `site: Site` is
+	// deliberate: the frontend's URLFragment requires site { id name icon
+	// category { ... } } to render a row at all, so a null site would not fix the
+	// page, it would trade a loud failure for blank or broken rows. A URL whose
+	// site is gone has no site to render, so it does not belong in the list.
```

### `internal/queries/sql/edit.sql`

```diff
diff --git a/internal/queries/sql/edit.sql b/internal/queries/sql/edit.sql
index 6458b87..e0b5d51 100644
--- a/internal/queries/sql/edit.sql
+++ b/internal/queries/sql/edit.sql
@@ -169,6 +169,25 @@ WHERE edit_id = $1;
+-- Drop any URL whose site no longer exists.
+--
+-- This is the #621 fix. current_urls is safe already: scene_urls/performer_urls/
+-- studio_urls carry a foreign key to sites, so deleting a site cascades the
+-- join rows away. But added_urls is read straight out of the edit's JSON
+-- payload, which is not a foreign key and is not cascaded -- a site deleted
+-- after the edit was filed left a dangling site_id behind.
+--
+-- The dangling id then reached URL.site, which the schema declares non-null
+-- (`site: Site!`), so the dataloader's nil became a GraphQL null-in-non-null
+-- error and the whole /edits page failed to render -- one bad row taking out
+-- every edit on the page. URL.type additionally dereferenced the nil site
+-- without a check.
+--
+-- Filtering here rather than relaxing the schema to `site: Site` is
+-- deliberate: the frontend's URLFragment requires site { id name icon
+-- category { ... } } to render a row at all, so a null site would not fix the
+-- page, it would trade a loud failure for blank or broken rows. A URL whose
+-- site is gone has no site to render, so it does not belong in the list.
@@ -208,8 +227,9 @@ final_urls AS (
-SELECT DISTINCT url, site_id FROM final_urls
-ORDER BY url;
+SELECT DISTINCT final_urls.url, final_urls.site_id FROM final_urls
+JOIN sites s ON s.id = final_urls.site_id
+ORDER BY final_urls.url;
```

## Tests

- `internal/api/edit_deleted_site_url_integration_test.go`

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
