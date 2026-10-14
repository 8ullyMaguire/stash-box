# SPEC — the tag ontology (aggregator programme, phase 4)

NexusXXX §4.1–§4.6. This repo already has `tags` (`name`, `description`, `aliases`,
`deleted`, `category_id`, `created_at`, `updated_at`) and `tag_categories` that nest, with a
cycle-rejecting trigger (migration 106) and the `TagCategoryTreeNode` walk in GraphQL. What
is missing is **the level below the category**: a relation between tags, and a version over
the relation.

This phase adds the tag graph. Tiers (`content_tier`, `requires_opt_in`) are phase 5, the
search language is phase 7, embeddings and co-occurrence are later phases; §2 says what is
deferred and why.

## 1. What an implication is

One row of `tag_implications(tag_id, implied_tag_id)`: **anything tagged `tag_id` is
effectively also tagged `implied_tag_id`.** The source spec is explicit about the direction
(§4.2): *"Tagging `cowgirl_position` automatically applies `woman_on_top`, `vaginal_sex`,
`sex`."* So the relation points from the specific to the general, and a reader searching a
general tag must find the specific things tagged with it.

**The plan's phase-4 line states this backwards** ("applying `cowgirl_position` returns
items tagged `woman_on_top`"). NexusXXX §4.2 settles it and this spec follows §4.2: filtering
by T returns items tagged T *or any tag that implies T*.

## 2. One owning table, derived at read time

Implications are **never materialised onto `scene_tags`** and never copied onto a tag row.

NexusXXX §4.3 models an implied tag as a `TagApplication` with `source: implication`, which
is a materialised row. Rejected here, for a reason that is about this schema and not about
taste:

- `scene_tags(scene_id, tag_id)` has no `source` column, so a materialised implication is
  indistinguishable from a human's tag. "Was this tag applied by a person?" is then
  unanswerable, and the row cannot be removed when the implication is, because nothing
  records which rows a rule produced.
- Adding an implication must retro-apply to every existing item. Materialised, that is a
  backfill job; derived, it is free and instant. Removing one is the same in reverse.
- The fork's own rule (§3.2 of the crosswalk, "generated vs hand-written — know which is
  which") makes a derived list of the owning table the only shape where the two cannot
  disagree, and a materialised copy is a second source of truth that disagrees on the first
  merge.

So the graph is read through a recursive CTE at query time, and the only write is one row in
one table.

## 3. The DAG, and where the cycle is refused

The graph is a DAG. The refusal is at the database, not in the service, for the same reason
migration 106's category cycle is: a service check protects the paths that remember to call
it, and a graph is edited by edits, merges, seeds and future migrations.

- A `BEFORE INSERT OR UPDATE` trigger walks the ancestors of the proposed tail and raises
  `check_violation` if the head is reachable — the same recursive shape as
  `tag_categories_no_cycle()`, deliberately not a second invention.
- `CHECK (tag_id <> implied_tag_id)` refuses a self-implication.
- The closure query is written with `UNION` (not `UNION ALL`) and a depth bound. Both are
  belt-and-braces against a graph the trigger should make impossible: without them a corrupt
  graph hangs a search that is serving a reader. **A depth bound turns a corrupt graph into a
  wrong answer instead of a hung request**, which is the failure mode a search surface can
  survive; the migration comment says so.

## 4. The observable surface

### 4.1 Search — the point of the whole feature

Filtering by a tag T matches items tagged T **or any tag that implies T, transitively**.

| Modifier | Effect with implications |
|---|---|
| `INCLUDES` (any of) | union of each requested tag's closure |
| `INCLUDES_ALL` (all of) | every requested tag is satisfied by the item's tags *or something that implies it* |
| `EXCLUDES` | refuses an item that carries **any** tag in the closure — `-sex` must exclude what implies `sex`, or the hierarchy is useless for filtering |

`INCLUDES_ALL` is the one that cannot be done by expanding the value list: the existing
implementation proves "all of these" with `GROUP BY fk HAVING COUNT(*) = len(values)`, and a
count over an expanded list would demand one row per implied tag. An item tagged only
`cowgirl_position` (implying `woman_on_top` and `vaginal_sex`) satisfies "all of
`woman_on_top`, `vaginal_sex`" while contributing one join row. So `INCLUDES_ALL` becomes one
`EXISTS` per requested tag, each over that tag's closure.

### 4.2 Reading the graph

```
Tag.implies: [Tag!]!        # direct: what THIS tag implies
Tag.implied_by: [Tag!]!     # direct: what implies THIS tag
Tag.implies_transitive      # everything below it (depth-bearing, like TagCategoryTreeNode)
Tag.implied_by_transitive
```

Direct and transitive are separate fields on purpose: an edit form needs the direct pair to
be editable, and a reader-facing page needs the transitive set to be honest about what a
filter will return.

### 4.3 Changing the ontology is governed, because it is shared data

An implication is changed **only through a tag edit** (`TagEditDetailsInput
{ added_implications, removed_implications }`, both `[ID!]`), never through a direct
mutation. Two reasons, one of them an existing test:

- `TestNoResolverWritesASharedFieldDirectly` (internal/api) exists so that no resolver writes
  a shared field without going through `collab`/`Edits`. A `tagImplicationCreate` mutation
  would be exactly that, and tag data is the most shared data in the repo.
- §4.6's governance (proposal → discussion → vote → implement) is what the edit system
  already is. A second proposal table would be a second vote counter, which the repo has a
  standing rule against (`never store a tally`).

The two fields are on the *edited tag* and read as "this tag implies these". An edit that
would create a cycle is refused **when the edit is submitted** (the service checks, so the
submitter gets an error naming the cycle) **and again when it is applied** (the trigger,
which is the authority).

### 4.4 Versioning

`ontology_versions(version int, snapshot jsonb, note text, created_by, created_at)`.

- A version row is written by an `AFTER INSERT OR DELETE` trigger on `tag_implications`, in
  the same transaction as the change, so "the version moved but the change did not land"
  cannot happen.
- The trigger takes `LOCK TABLE ontology_versions IN EXCLUSIVE MODE` and computes
  `MAX(version) + 1`. Ontology changes are rare and a version collision would be a confusing
  failure (`duplicate key`), so serialising them is the cheap correct answer.
- **The snapshot is the implication set by tag NAME, ordered**: `[{"tag": "...", "implies":
  "..."}]`. Names, not ids, because the document's readers are people reviewing an ontology
  change, and `tags.name` is unique. The limit is stated in the migration: this is the
  implication graph, not the whole ontology — tag renames and category changes are not in it.
- Version 0 is seeded as the empty ontology, so a reader always has a row and "version 1" is
  the first change rather than an absence.
- `Query.ontologyVersion: Int!` and `Query.ontologyVersions(limit): [OntologyVersion!]!`
  expose it. A client can say which ontology an answer came from.

## 5. Merges

Merging tag S into T must not destroy implications silently. `mergeInto` retargets, in this
order, inside the DB:

1. Implications where S is the **implied** tail: `(head, S) → (head, T)`.
2. Implications where S is the **head**: `(S, tail) → (T, tail)`.
3. Rows that became self-implications (`(T, T)`) are deleted — the merge means the two tags
   are one.
4. Rows that now duplicate an existing pair are deleted, so the retarget cannot violate the
   primary key.

The cycle trigger runs on every statement of the retarget and is the authority: a merge that
would fold a cycle into the surviving tag is refused rather than applying a corrupt graph.

## 6. What is deferred, and to which phase

| NexusXXX | Here |
|---|---|
| §4.1 `requires_opt_in`, `content_tier` | phase 5 (the tier table) |
| §4.1 `wiki` (markdown, versioned) | not this programme; `description` exists |
| §4.1 `embedding`, `co_occurrence`, `count` | later phases (similarity §8, discovery) |
| §4.3 `confidence`, `source`, `votes` per application | phase 6 (trust) + phase 7 (discovery); `scene_tags` has no source column and this spec deliberately does not add one |
| §4.4 AI auto-tagging | not in this programme (no model is installed; §5.5 of the programme spec) |
| §4.5 the boolean search language | phase 7 |
| §4.6 forum thread + 7-day discussion / 3-day vote windows | the edit system's comment thread and vote window; a `status` enum on the change is not added because `edits`/`edit_votes` already answer "where is it" |

## 7. Verification

Every claim below was run; each names the command and what it returned.

- **`scripts/verify-111.sh`** (21/21, against `sbx-scratch`) — the constraints and the
  trigger, at the database: a self-implication is refused by the trigger's own message; a
  two-node cycle is refused on INSERT; a **longer** cycle (`A→B→C→D→A`) is refused — three
  steps away, which a check that only compares the two endpoints waves through; the pair is
  unique; the trigger's **UPDATE** arm is exercised separately, because a merge retargets rows
  and an INSERT-only guard would let a merge fold a cycle in (the refused retarget leaves the
  row alone); the hierarchy is readable transitively (four tags above one node, at depths 1–4)
  and the expansion **groups by the tag that was asked for**, which is the shape `INCLUDES_ALL`
  needs; deleting a tag cascades its implications rather than leaving dangling edges.
- **`scripts/verify-112.sh`** (28/28) — the version table: version 0 seeded as the empty
  ontology, and a snapshot that is not a list refused by the CHECK; the note and the snapshot
  for an add, a removal and a retarget; the document is ordered (so two versions diff by eye)
  and COMPLETE rather than a log; a change writes its version **in its own transaction** (a
  rolled-back change leaves no version and no edge); two concurrent writers serialise — the
  second waits and then succeeds, and dropping `LOCK TABLE` from the live function turns that
  into `duplicate key value violates unique constraint "ontology_versions_pkey"`; and the merge
  retarget **extracted by name** from `internal/queries/sql/tag_ontology.sql`, in its order
  (self-pair, duplicate tails, duplicate heads, then both retargets), including a merge that
  would fold a cycle in being refused by the trigger rather than applied.
- **Unit** (`internal/service/query/criterion_expansion_test.go`) — the expansion identity (an
  id the hierarchy does not know satisfies itself; an empty list is not a licence to match
  nothing), the deduplicated union, and the SQL **shape** each modifier produces: `INCLUDES`
  and `EXCLUDES` as one semi-join over the union, `INCLUDES_ALL` as one `EXISTS` per requested
  tag with **no** `COUNT(*)`, and the counting form preserved for the no-hierarchy case. Shape,
  not rows: an assertion on rows passes with the wrong shape whenever a fixture carries the
  tags directly.
- **Integration** (`internal/api/tag_ontology_integration_test.go`, real database + GraphQL) —
  a tag edit carrying implications applies and the search then expands through it; **the
  control is the same search run before the implication exists**; removing the implication
  takes the expansion away again; the scene still carries only the tag a person put on it
  (nothing is materialised); `INCLUDES_ALL` is satisfied by one specific tag implying both
  requested tags; `EXCLUDES` refuses what implies the excluded tag; a cycle-creating edit is
  refused at submit with the reason in the message; and a merge moves both ends of the
  merged-away tag's implications while its id resolves to the survivor.
- **Two mutations, each reddening exactly one test and no neighbour.** Forcing the counting
  form for the hierarchy reddens `TestIncludesAllAsksOncePerRequestedTag` (unit) and
  `TestIncludesAllIsSatisfiedByOneTagThatImpliesBoth` (integration); making an unknown id
  expand to nothing reddens only `TestAValueTheHierarchyDoesNotKnowSatisfiesItself`.
- **`docs/plan-verify.py`** and **`docs/goal-check.py`** are run over this spec's programme, and
  the track row names this phase's files.

## 8. Reversals this phase records

1. **The migration numbers in the plan.** Phase 4's text says 110 and 111; 110 is
   `content_source_normalized_url` (phase 2, landed). This phase is **111 and 112**.
2. **The direction of the worked example**, per §1 above.
3. **`ontology_changes` is not a table.** The plan lists one with its own status machine; the
   edit system is that machine, and a second one would be a second place a change's state
   lives.
