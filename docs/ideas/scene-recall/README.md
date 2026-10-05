# Scene recall — 200 ideas, two sources, merged

**The problem this whole directory exists for.** Someone remembers a scene they
saw a long time ago in very broad strokes — an era, a vibe, half a performer
name, a setting — and a "tip of my tongue" community could not find it. Those
communities fail for a structural reason: it is prose threads all the way down,
with no structured data to search. This repo *has* the structured data. The gap
is everything around it.

This is an **idea bank, not a plan and not a commitment.** Nothing here has been
specced. `docs/SPEC.md` is the spec home; `docs/plan/` holds hand-written feature
plans; `docs/plans/` (plural) is the generated per-issue lineage. This new
`docs/ideas/` directory is a fourth thing: raw input awaiting triage.

---

## The files

| File | What it is | Status |
|---|---|---|
| `SOURCE-A-tip-of-tongue-2026-10-05.md` | 100 ideas, independently brainstormed and grounded in the tree on 2026-10-05 | verbatim, unmodified |
| `SOURCE-B-tip-of-tongue-2026-10-05.md` | 100 more ideas from a second agent surveying the same stack | verbatim, with one factual correction (§Corrections) |
| `README.md` | this file: the merged entry point, the merge record, the overlap map | current |

Both sources are preserved **whole and in their original wording.** Where they
overlap, that is information — two agents independently reaching for the same
lever is a much stronger signal that it is the right lever than either being
right alone. Nothing was deleted to make the merge tidy.

---

## Merge record: what both sources agree is the top of the list

Every item below appears in **both** lists. This is the strongest signal in the
whole directory.

| Lever | Source A | Source B | Note |
|---|---|---|---|
| **Index `scene.details`** | #1 | #1 | Both ranked it first. The single highest-ROI change in the set. |
| **Index tag names into `scene_search`** | #3 | #3 | Both call it the most common broad stroke. |
| **Index `scene.director`** | #2 | #2 | |
| **Trigram fallback when BM25 returns nothing** | #5 | #5 | The indexes exist (migrations 73/75); no live query uses them. |
| **Weighted tags above studio** | #4 | #4 | |
| **Structured `RecallQuery` input** | #16 | #16 | Same name, same shape: era + performers + tags + duration, composed into `SceneQueryInput`. |
| **Uncertainty-weighted clues** | #19 | #19 | Both: mark each clue certain vs vague. |
| **Facet-count breadcrumbs** | #21 | #21 | |
| **Structured fields on `IdentificationPostInput`** | #28 | #28 | |
| **Expose the candidate `note` as a mutation input** | #29 | #29 | The schema comment says so verbatim. |
| **Auto-suggest candidates from structured fields** | #32 | #32 | Both insist it stays human-resolved. |
| **Snapshot-generation quests** | #44 | #44 | |
| **Snapshot pHash table** | #53 | #53 | Makes frame-upload matching one query. |
| **Federated recall broadcast** | #69 | #69 | The D2 `ask.go` path already exists. |
| **Surface foreign evidence to askers** | #71 | #71 | Built for admins; askers can't see it. |
| **Recallability as a completion field** | #87 | #87 | So quests sustain the fix. |
| **Quest: add details** | #88 | #88 | |
| **Never let votes become metadata** | #93 | #93 | Both treat this as the constraint the whole thing hangs on. |
| **Provisional marking of solved recalls** | #95 | #95 | |

**18 levers reached independently by two agents.** The read: this is not a
matter of taste about what to build next. The search table is the bottleneck,
and the identification board is the fallback that already exists underneath it.

---

## Corrections applied to Source B

Source B was written after surveying the same tree and is accurate on
essentially every existence claim. One factual error was found during the merge,
and it is worth recording because it is the *kind* of error this directory will
keep producing:

> **Source B #17 claimed** "there is no NOT in `SceneQueryInput` today."
>
> **False.** `CriterionModifier` includes `EXCLUDES`, and
> `internal/service/scene/query.go` honours it for `id`, `studios` and `tags`
> (lines 145, 203, 223), with integration coverage at
> `internal/api/scene_integration_test.go:561` and `:650`. Negative filters are
> a UI and vocabulary problem, not a missing operator. The idea survives, weaker.

The generalisable lesson, and the reason this section exists: **an idea list
that claims something does not exist is more expensive than one that does not
claim it at all**, because a downstream agent will size the work from the claim
and skip the check. Every "X does not exist" in either source is a finding to
re-verify before it becomes a ticket.

---

## The shortest possible summary of the state of play

**What exists and is usable right now:**

- `scene_search` (migrations 35 → 56 → 61) — ParadeDB BM25 over `scene_title`,
  `scene_date`, `studio_name`, `network_name`, `studio_aliases`,
  `network_aliases`, `performer_names`, `scene_code`. Maintained by triggers.
- Trigram GIN indexes on `scenes.title`, `scenes.code`, `performers.name`,
  `performers.disambiguation` (73/75) — **no live query uses them.**
- The identification board: `identification_queries` / `_candidates` /
  `_candidate_votes` (79), federation (89), a Go service with
  `Post/Get/ListOpen/Suggest/Vote/Resolve/Abandon`. Deliberately never
  auto-creates metadata.
- Collages: 12–24 millisecond offsets over a scene's duration (78).
- Fingerprints: MD5/OSHASH/PHASH with a bktree SP-GiST index and a working
  Hamming-distance traversal (`ExpandPhashNeighbors`, `config.PHashDistance`).
- `completion` scoring per entity type, the quest generator, notifications,
  webhooks, trust events, Elo/Glicko, taste vectors, `expected_totals` (97),
  `fingerprint_corroboration` view (99).

**What does not exist at all (grep-verified 2026-10-05):**

- Any ML or vector infrastructure. No `clip`, `onnx`, `embedding`, `pgvector`,
  `qdrant`, `tantivy`, or `meilisearch` anywhere in `go.mod` or `internal/`. The
  only cosine in the codebase is `internal/service/federation/select.go`'s
  taste-vector comparison.
- Any "similar to" affordance. `grep -r similarTo graphql/schema/` returns
  nothing. SPEC §7.4 promises it; no code provides it.
- Any indexing of `scene.details`, `scene.director` or scene tags for search.
- Any date-range, duration-range or performer-count filter on `SceneQueryInput`
  (`date` is `DateCriterionInput`, i.e. one value plus a comparison modifier).
- Any pHash on `scene_snapshots` or `images` — only video-file fingerprints
  have hashes.

**The unifying constraint both sources converged on independently:** every clue
is evidence until a human resolves it. The recall machinery proposes; the
community disposes; only the existing edit path writes metadata. Every idea in
both lists routes through `resolveIdentificationQuery` or the edit path, by a
named person. Anything that would create metadata from a vote is not in either
list, and should not be added to it.

---

## Where a third agent should start

Do not start at idea #1. Start here:

1. **Build the evaluation set first.** 150–200 remembered descriptions with
   known answers. Almost every source's best idea is unfalsifiable without it,
   and the board's own solved queries are the corpus. Until this exists, every
   "this improves recall" claim is a vibe.
2. **Re-verify the `X does not exist` claims** in both sources. One of five
   spot-checked claims was already false (see Corrections).
3. **Then #1/#3/#4** — index `details`, tags and `director`, weight tags up.
   It is one migration and one query change, and it is the item both sources
   ranked first.
4. **Then measure.** With the eval set in place, the fuzzy/parse/trigram ideas
   (Source A #2/#5, Source B #5/#76) become testable rather than plausible.

**Two honesty notes the third agent should not skip:**

- The strongest *technical* answer — CLIP / face / appearance matching — is the
  weakest *fit*. There is no ML stack, and per SPEC §7.17 the mesh replicates
  questions and never content. It belongs in a phase after the text is
  searchable, not before.
- "ToMiT communities couldn't find it" is usually **not** a matching-algorithm
  failure. It is a vocabulary failure (people cannot name what they remember)
  plus a data-coverage failure (the scene may not be in the database at all).
  Source A #54 and Source B #92 attack the first and second respectively. If
  the scene was never catalogued, better search cannot find it — and idea #92's
  expected-totals surface is the honest way to tell an asker so.

---

## Provenance

- Surveyed 2026-10-05 against `main` at `6fb5c0e8`, migrations newest `102`.
- Source B arrived already written in the working tree as
  `docs/IDEAS-scene-recall.md`; it was moved here unchanged rather than
  duplicated, so there is exactly one copy.
- Both lists were produced by brainstorming, not by measuring a corpus. That is
  the appropriate register for an idea bank and the wrong register for a plan.