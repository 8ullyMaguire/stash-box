# 0703 — [Bug Report] Unable to Update Merges

**Status: SOLVED in `031e84d1d`.** This plan records what was done and
why, so the change can be re-implemented or reviewed without the original
context. It is a description of shipped work, not a proposal.

- Commit: `031e84d1d` — edits: let a merge edit's sources be edited when updating (fixes #703)
- Area: `edits`
- Issue: https://github.com/stashapp/stash-box/issues/703

## What was wrong

From `docs/track/WORKLOG.md`:

> Backend was never the problem: `tag.go:99` reads `input.Edit.MergeSourceIds` on
> the update path as well as create. The frontend form submitted those ids but
> rendered no control for them, so a user could not add or drop a source — the
> only recourse was cancelling and refiling, losing votes and comments. The
> `EditUpdate` query already fetched `merge_sources` and `operation`; nothing read
> them.
>
> `MergeSourceEditor` extracted rather than copy-pasted across three pages: the
> selector differs (multi-select tags/performers, single-select studios) but the
> list, remove control and target exclusion are identical.
>
> Evidence — 9 tests over tag/studio/performer. Mutation-verified by forcing
> `isMerge = false` (the pre-#703 behaviour) in all three pages: **6 fail, 3 pass**
> — the 3 that pass are the non-merge guards, which is correct.

## Files touched

**frontend**

- `frontend/src/components/mergeSourceEditor/MergeSourceEditor.tsx`
- `frontend/src/components/mergeSourceEditor/index.ts`
- `frontend/src/pages/performers/PerformerEditUpdate.tsx`
- `frontend/src/pages/performers/__tests__/PerformerEditUpdate.test.tsx`
- `frontend/src/pages/studios/StudioEditUpdate.tsx`
- `frontend/src/pages/studios/__tests__/StudioEditUpdate.test.tsx`
- `frontend/src/pages/tags/TagEditUpdate.tsx`
- `frontend/src/pages/tags/__tests__/TagEditUpdate.test.tsx`

## The change

### `frontend/src/components/mergeSourceEditor/MergeSourceEditor.tsx`

```diff
diff --git a/frontend/src/components/mergeSourceEditor/MergeSourceEditor.tsx b/frontend/src/components/mergeSourceEditor/MergeSourceEditor.tsx
index 0000000..e77e904
--- /dev/null
+++ b/frontend/src/components/mergeSourceEditor/MergeSourceEditor.tsx
@@ -0,0 +1,83 @@
+import { type FC, useState } from "react";
+import { Col, Row } from "react-bootstrap";
+import type { OperationEnum } from "src/graphql";
+
+export interface MergeSource {
+  id: string;
+  name: string;
+}
+
+interface Props {
+  /** Ids currently on the edit. Names are filled in as they load. */
+  sources: MergeSource[];
+  /** Called with the new full list whenever it changes. */
+  onChange: (sources: MergeSource[]) => void;
+  /** Renders the "add a source" control. */
+  children: (excludeIds: string[]) => React.ReactNode;
+  /** Id of the merge target, so it can never be selected as a source. */
+  targetId?: string | null;
+  testId?: string;
+  label?: string;
+}
+
+// A merge edit's source list, shared by the per-entity update forms.
+//
+// #703: updating a merge edit used to open the plain entity form, which carried
+// the merge sources on submit but offered no way to see, add or drop them, so the
+// only recourse was cancelling the edit and filing a new one.
+export const MergeSourceEditor: FC<Props> = ({
+  sources,
+  onChange,
+  children,
+  targetId,
+  testId = "merge-source-list",
+  label = "Merge sources",
+}) => {
+  const [internal, setInternal] = useState<MergeSource[] | null>(null);
+  const current = internal ?? sources;
+
+  const update = (next: MergeSource[]) => {
+    setInternal(next);
+    onChange(next);
+  };
+
+  const excludeIds = [
+    ...(targetId ? [targetId] : []),
+    ...current.map((s) => s.id),
+  ];
+
+  return (
+    <Row className="g-0">
+      <Col xs={6}>
+        <label className="form-label" htmlFor={`${testId}-select`}>
+          {label}
+        </label>
+        {children(excludeIds)}
    ... (trimmed; run `git show` for the full diff)
```

### `frontend/src/components/mergeSourceEditor/index.ts`

```diff
diff --git a/frontend/src/components/mergeSourceEditor/index.ts b/frontend/src/components/mergeSourceEditor/index.ts
index 0000000..e69795a
--- /dev/null
+++ b/frontend/src/components/mergeSourceEditor/index.ts
@@ -0,0 +1,2 @@
+export type { MergeSource } from "./MergeSourceEditor";
+export { isMergeEdit, MergeSourceEditor } from "./MergeSourceEditor";
```

### `frontend/src/pages/performers/PerformerEditUpdate.tsx`

```diff
diff --git a/frontend/src/pages/performers/PerformerEditUpdate.tsx b/frontend/src/pages/performers/PerformerEditUpdate.tsx
index eea7bad..9459d76 100644
--- a/frontend/src/pages/performers/PerformerEditUpdate.tsx
+++ b/frontend/src/pages/performers/PerformerEditUpdate.tsx
@@ -1,11 +1,20 @@
+import {
+  isMergeEdit,
+  type MergeSource,
+  MergeSourceEditor,
+} from "src/components/mergeSourceEditor";
+import PerformerSelect from "src/components/performerSelect";
+  type PerformerFragment,
+import { PerformerFragmentDoc } from "src/graphql/types";
+import { useEntities } from "src/hooks";
@@ -17,6 +26,21 @@ import { ROUTE_EDIT } from "src/constants";
+  const isMerge = isMergeEdit(edit.operation);
+
+  // Seeded from the edit so the current sources are visible and adjustable
+  // (#703: the update form offered no way to touch the merge).
+  const [mergeSources, setMergeSources] = useState<MergeSource[]>(
+    edit.merge_sources.map((s) => ({ id: s.id, name: "" })),
+  );
+
+  const { sources: loadedSources } = useEntities<PerformerFragment>(
+    mergeSources,
+    "findPerformer",
+    PerformerFragmentDoc,
+    { enabled: isMerge },
+  );
+
@@ -51,7 +75,9 @@ export const PerformerEditUpdate: FC<{ edit: EditUpdate }> = ({ edit }) => {
-            merge_source_ids: edit.merge_sources.map((s) => s.id),
+            merge_source_ids: isMerge
+              ? mergeSources.map((s) => s.id)
+              : edit.merge_sources.map((s) => s.id),
@@ -65,6 +91,12 @@ export const PerformerEditUpdate: FC<{ edit: EditUpdate }> = ({ edit }) => {
+  const nameById = new Map(loadedSources.map((t) => [t.id, t.name]));
+  const named = mergeSources.map((s) => ({
+    ...s,
+    name: nameById.get(s.id) ?? s.name,
+  }));
+
@@ -75,6 +107,31 @@ export const PerformerEditUpdate: FC<{ edit: EditUpdate }> = ({ edit }) => {
+      {isMerge && (
+        <>
+          <MergeSourceEditor
+            sources={named}
+            onChange={setMergeSources}
+            targetId={edit.target?.id}
+          >
+            {(excludeIds) => (
+              <PerformerSelect
+                performers={[]}
+                onChange={(performers) =>
+                  setMergeSources((curr) => [
+                    ...curr,
+                    ...performers.map((p) => ({ id: p.id, name: p.name })),
+                  ])
+                }
+                message="Search for performers to merge..."
    ... (trimmed; run `git show` for the full diff)
```

### `frontend/src/pages/performers/__tests__/PerformerEditUpdate.test.tsx`

```diff
diff --git a/frontend/src/pages/performers/__tests__/PerformerEditUpdate.test.tsx b/frontend/src/pages/performers/__tests__/PerformerEditUpdate.test.tsx
index 0000000..c4f29e5
--- /dev/null
+++ b/frontend/src/pages/performers/__tests__/PerformerEditUpdate.test.tsx
@@ -0,0 +1,88 @@
+import { screen, waitFor } from "@testing-library/react";
+import type { EditUpdateQuery } from "src/graphql";
+import { renderForm } from "src/test/renderForm";
+import { describe, expect, it } from "vitest";
+
+import { PerformerEditUpdate } from "../PerformerEditUpdate";
+
+type Edit = NonNullable<EditUpdateQuery["findEdit"]>;
+
+const baseEdit = {
+  __typename: "Edit",
+  id: "e-1",
+  target_type: "PERFORMER",
+  operation: "MERGE",
+  status: "PENDING",
+  applied: false,
+  closed: null,
+  created: "2026-01-01T00:00:00Z",
+  updated: null,
+  updatable: true,
+  update_count: 0,
+  vote_count: 0,
+  options: null,
+  user: null,
+  merge_sources: [
+    { __typename: "Performer", id: "src-1" },
+    { __typename: "Performer", id: "src-2" },
+  ],
+  target: {
+    __typename: "Performer",
+    id: "p-1",
+    name: "Target",
+    aliases: [],
+    deleted: false,
+  },
+  details: {
+    __typename: "PerformerEdit",
+    name: "Target",
+    aliases: [],
+    urls: [],
+    tattoos: [],
+    piercings: [],
+    images: [],
+  },
+} as unknown as Edit;
+
+const editWith = (overrides: Partial<Edit>) =>
+  ({ ...baseEdit, ...overrides }) as Edit;
+
+const sourceItems = () =>
+  screen.getByTestId("merge-source-list").querySelectorAll("li");
+
+// #703 applies to every entity type: the update form has to let the merge
+// sources be seen and changed, not just round-tripped blind.
+describe("PerformerEditUpdate merge sources (#703)", () => {
    ... (trimmed; run `git show` for the full diff)
```

### `frontend/src/pages/studios/StudioEditUpdate.tsx`

```diff
diff --git a/frontend/src/pages/studios/StudioEditUpdate.tsx b/frontend/src/pages/studios/StudioEditUpdate.tsx
index 4d4c8f5..e2b7d79 100644
--- a/frontend/src/pages/studios/StudioEditUpdate.tsx
+++ b/frontend/src/pages/studios/StudioEditUpdate.tsx
@@ -1,11 +1,20 @@
+import {
+  isMergeEdit,
+  type MergeSource,
+  MergeSourceEditor,
+} from "src/components/mergeSourceEditor";
+import StudioSelect from "src/components/studioSelect";
+  type StudioFragment,
+import { StudioFragmentDoc } from "src/graphql/types";
+import { useEntities } from "src/hooks";
@@ -17,6 +26,21 @@ import { ROUTE_EDIT } from "src/constants";
+  const isMerge = isMergeEdit(edit.operation);
+
+  // Seeded from the edit so the current sources are visible and adjustable
+  // (#703: the update form offered no way to touch the merge).
+  const [mergeSources, setMergeSources] = useState<MergeSource[]>(
+    edit.merge_sources.map((s) => ({ id: s.id, name: "" })),
+  );
+
+  const { sources: loadedSources } = useEntities<StudioFragment>(
+    mergeSources,
+    "findStudio",
+    StudioFragmentDoc,
+    { enabled: isMerge },
+  );
+
@@ -41,7 +65,9 @@ export const StudioEditUpdate: FC<{ edit: EditUpdate }> = ({ edit }) => {
-            merge_source_ids: edit.merge_sources.map((s) => s.id),
+            merge_source_ids: isMerge
+              ? mergeSources.map((s) => s.id)
+              : edit.merge_sources.map((s) => s.id),
@@ -51,6 +77,12 @@ export const StudioEditUpdate: FC<{ edit: EditUpdate }> = ({ edit }) => {
+  const nameById = new Map(loadedSources.map((t) => [t.id, t.name]));
+  const named = mergeSources.map((s) => ({
+    ...s,
+    name: nameById.get(s.id) ?? s.name,
+  }));
+
@@ -61,6 +93,33 @@ export const StudioEditUpdate: FC<{ edit: EditUpdate }> = ({ edit }) => {
+      {isMerge && (
+        <>
+          <MergeSourceEditor
+            sources={named}
+            onChange={setMergeSources}
+            targetId={edit.target?.id}
+          >
+            {() => (
+              // StudioSelect is single-value, so each pick adds one source.
+              <StudioSelect
+                onChange={(studio) => {
+                  if (!studio) return;
+                  setMergeSources((curr) =>
+                    curr.some((s) => s.id === studio.id)
+                      ? curr
+                      : [...curr, { id: studio.id, name: studio.name }],
+                  );
    ... (trimmed; run `git show` for the full diff)
```

### `frontend/src/pages/studios/__tests__/StudioEditUpdate.test.tsx`

```diff
diff --git a/frontend/src/pages/studios/__tests__/StudioEditUpdate.test.tsx b/frontend/src/pages/studios/__tests__/StudioEditUpdate.test.tsx
index 0000000..1bdd2a6
--- /dev/null
+++ b/frontend/src/pages/studios/__tests__/StudioEditUpdate.test.tsx
@@ -0,0 +1,87 @@
+import { screen, waitFor } from "@testing-library/react";
+import type { EditUpdateQuery } from "src/graphql";
+import { renderForm } from "src/test/renderForm";
+import { describe, expect, it } from "vitest";
+
+import { StudioEditUpdate } from "../StudioEditUpdate";
+
+type Edit = NonNullable<EditUpdateQuery["findEdit"]>;
+
+const baseEdit = {
+  __typename: "Edit",
+  id: "e-1",
+  target_type: "STUDIO",
+  operation: "MERGE",
+  status: "PENDING",
+  applied: false,
+  closed: null,
+  created: "2026-01-01T00:00:00Z",
+  updated: null,
+  updatable: true,
+  update_count: 0,
+  vote_count: 0,
+  options: null,
+  user: null,
+  merge_sources: [
+    { __typename: "Studio", id: "src-1" },
+    { __typename: "Studio", id: "src-2" },
+  ],
+  target: {
+    __typename: "Studio",
+    id: "s-1",
+    name: "Target",
+    urls: [],
+    parent: null,
+    child_studios: [],
+    images: [],
+    deleted: false,
+  },
+  details: {
+    __typename: "StudioEdit",
+    name: "Target",
+    urls: [],
+    images: [],
+    parent: null,
+  },
+} as unknown as Edit;
+
+const editWith = (overrides: Partial<Edit>) =>
+  ({ ...baseEdit, ...overrides }) as Edit;
+
+const sourceItems = () =>
+  screen.getByTestId("merge-source-list").querySelectorAll("li");
+
+describe("StudioEditUpdate merge sources (#703)", () => {
+  it("lists the existing merge sources on a merge edit", async () => {
    ... (trimmed; run `git show` for the full diff)
```

### `frontend/src/pages/tags/TagEditUpdate.tsx`

```diff
diff --git a/frontend/src/pages/tags/TagEditUpdate.tsx b/frontend/src/pages/tags/TagEditUpdate.tsx
index b90ab0b..e3807db 100644
--- a/frontend/src/pages/tags/TagEditUpdate.tsx
+++ b/frontend/src/pages/tags/TagEditUpdate.tsx
@@ -1,22 +1,47 @@
+import {
+  isMergeEdit,
+  type MergeSource,
+  MergeSourceEditor,
+} from "src/components/mergeSourceEditor";
+import TagSelect from "src/components/tagSelect";
+import Title from "src/components/title";
+import { ROUTE_EDIT } from "src/constants";
+  type TagFragment as Tag,
+import { TagFragmentDoc } from "src/graphql/types";
+import { useEntities } from "src/hooks";
-import Title from "src/components/title";
-import { ROUTE_EDIT } from "src/constants";
-
+  const isMerge = isMergeEdit(edit.operation);
+
+  // Seeded from the edit so the current sources are visible immediately and can
+  // be added to or removed from (#703: the update form used to render the plain
+  // tag form with no way to touch the merge at all).
+  const [mergeSources, setMergeSources] = useState<MergeSource[]>(
+    edit.merge_sources.map((s) => ({ id: s.id, name: "" })),
+  );
+
+  // Resolves the seeded ids into names. The edit query returns ids only.
+  const { sources: loadedSources } = useEntities<Tag>(
+    mergeSources,
+    "findTag",
+    TagFragmentDoc,
+    { enabled: isMerge },
+  );
+
@@ -38,7 +63,9 @@ export const TagEditUpdate: FC<{ edit: EditUpdate }> = ({ edit }) => {
-            merge_source_ids: edit.merge_sources.map((s) => s.id),
+            merge_source_ids: isMerge
+              ? mergeSources.map((s) => s.id)
+              : edit.merge_sources.map((s) => s.id),
@@ -48,6 +75,13 @@ export const TagEditUpdate: FC<{ edit: EditUpdate }> = ({ edit }) => {
+  // Names arrive asynchronously; show the fetched name once it is known.
+  const nameById = new Map(loadedSources.map((t) => [t.id, t.name]));
+  const named = mergeSources.map((s) => ({
+    ...s,
+    name: nameById.get(s.id) ?? s.name,
+  }));
+
@@ -58,6 +92,31 @@ export const TagEditUpdate: FC<{ edit: EditUpdate }> = ({ edit }) => {
+      {isMerge && (
+        <>
+          <MergeSourceEditor
+            sources={named}
+            onChange={setMergeSources}
+            targetId={edit.target?.id}
+          >
+            {(excludeIds) => (
+              <TagSelect
+                tags={[]}
    ... (trimmed; run `git show` for the full diff)
```

### `frontend/src/pages/tags/__tests__/TagEditUpdate.test.tsx`

```diff
diff --git a/frontend/src/pages/tags/__tests__/TagEditUpdate.test.tsx b/frontend/src/pages/tags/__tests__/TagEditUpdate.test.tsx
index 0000000..4576e3e
--- /dev/null
+++ b/frontend/src/pages/tags/__tests__/TagEditUpdate.test.tsx
@@ -0,0 +1,88 @@
+import { screen, waitFor } from "@testing-library/react";
+import type { EditUpdateQuery } from "src/graphql";
+import { renderForm } from "src/test/renderForm";
+import { describe, expect, it } from "vitest";
+
+import { TagEditUpdate } from "../TagEditUpdate";
+
+type Edit = NonNullable<EditUpdateQuery["findEdit"]>;
+
+const baseEdit = {
+  __typename: "Edit",
+  id: "e-1",
+  target_type: "TAG",
+  operation: "MERGE",
+  status: "PENDING",
+  applied: false,
+  closed: null,
+  created: "2026-01-01T00:00:00Z",
+  updated: null,
+  updatable: true,
+  update_count: 0,
+  vote_count: 0,
+  options: null,
+  user: null,
+  merge_sources: [
+    { __typename: "Tag", id: "src-1" },
+    { __typename: "Tag", id: "src-2" },
+  ],
+  target: {
+    __typename: "Tag",
+    id: "tag-1",
+    name: "Target",
+    aliases: [],
+    deleted: false,
+  },
+  details: {
+    __typename: "TagEdit",
+    name: "Target",
+    description: null,
+    aliases: [],
+    category: null,
+  },
+} as unknown as Edit;
+
+const editWith = (overrides: Partial<Edit>) =>
+  ({ ...baseEdit, ...overrides }) as Edit;
+
+const sourceItems = () =>
+  screen.getByTestId("merge-source-list").querySelectorAll("li");
+
+describe("TagEditUpdate merge sources (#703)", () => {
+  // Before the fix the update form rendered the generic tag form with no merge
+  // sources at all, so a merge edit could not be adjusted -- the only recourse
+  // was cancelling the edit and resubmitting a new one.
+  it("lists the existing merge sources on a merge edit", async () => {
    ... (trimmed; run `git show` for the full diff)
```

## Tests

- `frontend/src/pages/performers/__tests__/PerformerEditUpdate.test.tsx`
- `frontend/src/pages/studios/__tests__/StudioEditUpdate.test.tsx`
- `frontend/src/pages/tags/__tests__/TagEditUpdate.test.tsx`

These were mutation-checked: the fix was reverted, the test was run, and
it was required to fail. A test that survives that check is not evidence.

## Verify

```bash
go build ./... && go vet ./...
export POSTGRES_DB="$STASHBOX_TEST_DSN"   # test DSN, never commit it
go test -tags=integration -count=1 ./internal/api/
go test $(go list ./... | grep -vE 'internal/api$') -count=1
cd frontend && pnpm run test:run && cd ..
```

---
