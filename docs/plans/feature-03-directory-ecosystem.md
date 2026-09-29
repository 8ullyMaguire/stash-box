# Phase 3 — site/studio directory, reviews, Stash integration, public API, browser extension

**Status: not started. Depends on Phases 1 and 2.**

Vision §10. The directory is the mainstream-traffic play: public pages for every
site, performer, studio, scene and tag, with structured reviews.

---

## Step 1 — reviews

Reviews are new user-generated content, so they need moderation and they are the
first place the trust levels earn their keep.

```sql
CREATE TABLE reviews (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    author_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    entity_type TEXT NOT NULL,
    entity_id UUID NOT NULL,
    -- 1..5. NULL for reviews of things not rated (e.g. a studio's ethics).
    rating INTEGER CHECK (rating IS NULL OR rating BETWEEN 1 AND 5),
    body TEXT NOT NULL,
    -- "verified usage" flag from vision §10.
    verified BOOLEAN NOT NULL DEFAULT FALSE,
    status TEXT NOT NULL DEFAULT 'published',  -- 'published' | 'flagged' | 'removed'
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX reviews_entity_idx ON reviews(entity_type, entity_id, created_at DESC);
```

Two decisions to record before coding:

- **Editing a review** creates a new version rather than mutating the row, so a
  rating history exists. Cheaper alternative: mutate and accept no history. Pick
  one and write it down; the vision's "verified usage" flag is much weaker
  without history.
- **One review per author per entity**, enforced by a `UNIQUE` constraint, not
  only by the service. Same rule as the identification suggestions in Phase 1.

## Step 2 — site profiles and the directory

Sites already exist as an entity (issue #621 was about a deleted site breaking
the /edits page). The directory adds the *directory-specific* fields from vision
§10: description, categories, pricing, payment methods, features, pros/cons,
alternatives.

```bash
grep -n "type Site struct" -A 25 internal/models/site.go
```

Add a migration with the new columns; **do not edit the existing migration.**
`alternatives` is a many-to-many self-relation, so a join table:

```sql
CREATE TABLE site_alternatives (
    site_id UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    alternative_site_id UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    PRIMARY KEY (site_id, alternative_site_id),
    CHECK (site_id <> alternative_site_id)
);
```

The `CHECK` prevents a site listing itself, which is otherwise reachable through
the GraphQL mutation and is a self-referential-loop bug that hangs the UI.

Verification:

```bash
export POSTGRES_DB="postgres:[REDACTED]@127.0.0.1:5434/stash-box-test?sslmode=disable"
go test -tags=integration -count=1 -run TestSiteDirectory ./internal/api/
```

Required: the self-alternative is rejected by the database; a deleted site
resolves to nothing (the #1007 contract — use `FindSiteWithRedirect` and not the
raw by-id query); an alternative chain does not hang the resolver.

## Step 3 — the public API surface

Vision §11: GraphQL **and** REST, SDKs, webhooks.

**The GraphQL API already exists and is already the public API** once Phase 1's
middleware is in. Do not build a second query surface. What is new:

- **REST**: a thin read-only subset for consumers who cannot run GraphQL. Scope
  it to a handful of read endpoints, not a mirror. A REST mirror is unbounded
  work and gets stale.
- **Webhooks**: a delivery queue with retries.

```sql
CREATE TABLE webhook_endpoints (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- HMAC secret, stored hashed like a password. Never store or log it.
    secret_hash TEXT NOT NULL,
    target_url TEXT NOT NULL,
    event_types TEXT[] NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE webhook_deliveries (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    endpoint_id UUID NOT NULL REFERENCES webhook_endpoints(id) ON DELETE CASCADE,
    event_type TEXT NOT NULL,
    payload JSONB NOT NULL,
    -- 0, 1, 2, 3 then abandoned.
    attempt INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error TEXT
);
```

The security requirements are not optional and are the part most likely to be
skipped under time pressure:

- **SSRF**: a user-supplied `target_url` can point at `169.254.169.254` or
  `localhost`. Validate the resolved IP against private ranges *after* DNS
  resolution, and re-check on retry, because DNS rebinding is the standard bypass.
- **Timing-safe signature comparison** (`hmac.Equal`), not `==`.
- **Never log the secret**, in any form, at any level.

Verification:

```bash
go test ./internal/webhook/ -count=1 -v
```

Required: a `169.254.169.254` target is rejected; a signature comparison uses the
constant-time path; a failed delivery retries with backoff and eventually gives
up; **the secret never appears in a log capture**. That last one is the guard —
assert it by capturing the logger output, because "it isn't logged" is otherwise
an unverified claim.

## Step 4 — Stash app integration

Two-way sync per vision §11. The Stash app is a separate codebase not in this
repo, so **the integration surface here is a documented API contract plus the
endpoints the app calls.** Do not invent client-side behaviour.

```bash
grep -rn "stash" internal/api/ --include="*.go" -il | head
```

If nothing exists, write `docs/plan/stash-integration-contract.md` specifying the
endpoints, the auth model, and the conflict-resolution rule, and implement only
what that contract requires.

**The conflict rule is the decision worth making early.** When Stash and the box
disagree on a field, the box wins (it is the curated, voted archive) and the app
shows the divergence. The alternative — last-write-wins — loses curated data to
an unmoderated client, which is the failure mode the whole edit system exists to
prevent.

## Step 5 — browser extension

Out-of-process: a WebExtension in its own directory, not in the Go module. It
reads the public API from Phase 1/3.

Scaffolding and manifest first, then the overlay. Note in the worklog that this
phase has no Go tests and its verification is a manual browser check plus the
extension's own test runner — do not claim it is covered by `go test`.

---

## Standing rules for every phase

These are the traps that actually cost time across the 26 issues already closed.
They are not hypothetical; each one produced a wrong result that had to be undone.

1. **Mutation-check every test.** Delete the implementation, run the test, watch
   it fail, restore. A test that passes on unfixed code is worse than no test.
   Five issues turned on this: #525, #950, #1007, #1177, #605.
2. **A test that exercises a helper is not a test of the caller.** In #605, four
   tests covered a content-type detector and all four stayed green when the line
   that *called* it was deleted. Always assert the wiring.
3. **The shared integration database is not isolated.** Any assertion about
   membership in an unfiltered collection must pin `PerPage`; the default is 25
   and other tests' fixtures push yours off the end. Hit in both directions
   (#829, #1007).
4. **A destroy mutation hard-deletes; a destroy EDIT soft-deletes.** Any test
   about `deleted = true` must apply the edit, or it tests a row that no longer
   exists. This silently produced four vacuous tests in #1007.
5. **`approveEdit` calls `t.Errorf` on any error**, so it cannot assert an
   expected failure. Call `ApproveEdit` directly and assert the `Applied` flag or
   the modbot comment — apply failures become `Unknown Error: %v` on the comment
   and the edit is marked failed, never returned as an error.
6. **A regex that matches anything proves nothing.** The e2e cooldown assertion
   in #1277 matched `/cooldown|wait/i` and so passed through the entire life of
   the bug it should have caught.
7. **Regenerate, then verify idempotence.** `sqlc generate` and `gqlgen` must be
   run twice with no diff. A green test run after failed codegen is not a pass.
8. **`internal/image` must stay in the unit run.** It was excluded until #1205,
   which is how a missing PNG/JPEG decoder (#948) stayed hidden behind a
   build-tag split.
9. **Rebuild the frontend before browser-verifying anything.** A stale
   `frontend/build` renders every route blank with HTTP 200. Use
   `node node_modules/vite/bin/vite.js build`, not `pnpm build` (hoisted linker).
10. **Do not hand-edit generated files.** `internal/queries/*.sql.go`,
    `internal/models/generated_*.go` and `graphql/generated.go` come from sqlc and
    gqlgen. Change the `.sql` / `.graphql` and regenerate.
