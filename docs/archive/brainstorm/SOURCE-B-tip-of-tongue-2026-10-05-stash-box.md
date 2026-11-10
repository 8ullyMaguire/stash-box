# SOURCE-B — 100 ideas: finding a scene from broad strokes when ToMiT failed

*Read `README.md` first: that is the entry point, and it cross-references this
file and Source A. Do not implement from this file alone.*

*Brainstorm by a second agent, written after surveying the same stack (search,
identification board, collages, federation, taste/Elo, quests).*

*Brainstorm. Not a plan, not a commitment — an idea bank, grounded in what the
codebase actually has today.*

**The problem:** someone remembers a scene they saw a long time ago in very
broad strokes — an era, a vibe, half a performer name, a setting — and
"tip of my tongue" style communities couldn't find it. Those communities fail
because they have no structured data to search: it's prose threads all the way
down. This repo has the structured data. The gap is everything around it.

**Grounding** (surveyed 2026-10-05):

- `scene_search` (ParadeDB/BM25) indexes only **title, code, date, performer
  names, studio/network names+aliases** — it does *not* index `details`,
  `director`, or **tags** (migrations 35, 56, 61). That's a huge gap for
  broad-stroke recall.
- Trigram indexes exist on `scenes.title` and `scenes.code` (migrations 73/75)
  — fuzzy matching is already available but underused.
- The identification board (`SPEC §5`, `internal/service/identification`,
  `graphql/schema/types/identification.graphql`) takes free-text `description` +
  optional `snapshotId`, has candidates/votes, and deliberately never
  auto-creates metadata: *"a vote is EVIDENCE, not authority."*
- Collages are millisecond offsets over a scene's duration
  (`internal/service/collage`) — 24 timestamps replicate mesh-wide (SPEC §7.8).
- Federation `Question.Validate` (F1, `internal/service/federation/ask.go`)
  restricts what crosses a node boundary; foreign candidates are evidence only
  (F2, no column can hold a local entity id).
- Taste keys/vector (`internal/service/elo/taste.go`), pHash fingerprint
  clusters (`fingerprintClusters`), completion scores, the quest generator
  (`internal/service/quest`), notifications, webhooks, and the trust ladder all
  exist as building blocks.

**The unifying principle,** borrowed from the codebase's own doctrine: every
clue is evidence until a human resolves it. The recall machinery proposes, the
community disposes, and only the existing edit path writes metadata.

---

## A. Make what's already stored findable (1–15)

1. **Index `scene.details` in `scene_search`.** The free-text description is
   exactly where "broad strokes" live ("hotel room, rainy night"), and
   migrations 35/56 never added it to the tsvector/BM25 fields. Highest-ROI
   single change.
2. **Index `scene.director` the same way** — "directed by someone whose name I
   half-remember" is a classic recall cue.
3. **Index tag names + aliases into `scene_search`** via a denormalized
   `tag_names` column (same pattern as `performer_names`), updated by the
   existing trigger family. Half-remembered kink words are the most common
   broad stroke of all.
4. **Weight tags above studio in the disjunction_max** in `SearchScenes` — for
   vague queries, what happened matters more than who released it.
5. **Trigram fallback tier in `SearchScenes`:** when BM25 coverage tier yields
   zero results, fall back to `similarity()` on title/code using the existing
   73/75 GIN indexes instead of returning an empty page.
6. **"Did you mean" expansion using studio aliases and performer aliases**
   already in the index — a remembered-wrong name still lands.
7. **Search the *edit history* (old titles/codes)** — many scenes were
   re-titled; the memory is of the title it had when the user saw it.
   `queryEdits` can supply this.
8. **Rank by *memory age decay*:** scenes with matching titles that haven't
   changed in years outrank recent rebrands, because old memories map to
   stable names.

9. **Approximate-date slider search UI** — `date: DateCriterionInput` already
   supports ranges; expose "somewhere between 2014 and 2018" as a first-class
   control rather than requiring an exact date.
10. **Duration-range filter** ("I remember it was short, under 20 min") —
    `duration` is on the scene and in fingerprints but not in
    `SceneQueryInput`.
11. **"N performers" facet** (1-on-1, group, etc.) derivable from
    `scene_performers` count — a very common broad stroke.
12. **Setting/venue vocabulary as a controlled tag category** so "shot
    outdoors, in a kitchen, in a car" queries hit curated facets instead of
    prose.
13. **Search by URL fragment** — people often remember "it was on X site" and
    the site's domain is in `scene_urls`; make partial-domain match a
    supported `url:` query.
14. **Quote search:** let users paste the one line of dialogue they remember
    and BM25-match it against `details` (which often contains synopsis text)
    once idea #1 lands.
15. **"Unknown fields" diagnostics endpoint** that tells a user *why* their
    query returned nothing (no scene has both that studio and that tag),
    turning dead ends into a next step.

## B. Structured "broad strokes" query builder (16–27)

16. **A `RecallQuery` input type:** decade range, era, studio *or* "some
    studio I can't name", 0–5 remembered performers, tag inclusions/exclusions,
    duration band, setting — composed into the existing `SceneQueryInput`
    server-side.
17. **Negative filters as first-class:** "everything *except* this
    studio/performer I already checked" — ToMiT failures are often ruled-out
    candidates, and there's no NOT in `SceneQueryInput` today.
18. **"I know one performer for sure" anchor mode:** intersect on `performers`
    (exact) × fuzzy everything else, since one certain name collapses the
    space massively.
19. **Uncertainty-weighted fields:** let the user mark each clue as *certain*
    vs *vague*; certain fields use exact filters, vague fields use fuzzy
    BM25/trgm with lower boosts — the ranker already has boost plumbing.
20. **Interactive process-of-elimination UI:** after each result page, offer
    "closer / further" micro-feedback (like a 20-questions binary tree) that
    prunes facets.
21. **Facet-count breadcrumbs:** show "312 scenes match your era, 41 also match
    this tag" *before* the user commits to a filter, borrowed from commerce
    search.
22. **"Did you watch it in Stash?" bridge:** a query that asks the user's
    local Stash instance (via the Stash Box API/Stash integration in
    SPEC §7.11) for scenes played in a date window, then matches those
    fingerprints against the box — the strongest memory cue is *when you
    watched it*, not when it was made.

23. **Browsing-history import:** paste browser history from the tube site you
    visited; extract candidate URLs and resolve via the existing `url` filter /
    `findScenes` by URL.
24. **Screenshot → collage match:** user uploads a remembered frame; match its
    pHash against scene images / snapshot-derived hashes using the *existing*
    pHash distance machinery (`phash_distance`, `fingerprintClusters`).
25. **Reverse-collage search:** given one remembered frame, return scenes whose
    *any* snapshot is within Hamming distance — requires storing per-snapshot
    pHashes, a natural extension of `scene_snapshots`.
26. **Sketch/annotate fallback:** user draws the composition (two people, bed
    on left) and a human (or later, a model) matches against collages — routed
    through the identification board rather than pretending to be
    algorithmic.
27. **Color-palette fingerprint:** store dominant-color histograms per snapshot
    (cheap, 8 bins × 3 channels) so "I remember a scene that was mostly
    blue/dark" is a queryable predicate.

## C. Strengthen the identification board (28–43)

28. **Structured recall fields on `IdentificationPostInput`** alongside free
    text: era, approx duration, performer count, setting — the description is
    currently the *only* required field and nothing else is queryable.
29. **Expose the candidate `note` as a mutation input** — the schema comment
    literally says "not exposed as a mutation input yet: no caller in this
    version has a way to supply one." "Watermark visible in frame 3" is what
    solves hard cases.
30. **Threaded clarifying questions on a query** ("was it HD or SD? studio or
    indie?") — SPEC §5 says "let the community ask for more," but there's no
    structure for it; a `Clarification` sub-type keeps it as evidence, not
    metadata.
31. **Status field for "narrowing" vs "open"** so a query with 12 ruled-out
    candidates doesn't read the same as a fresh post.
32. **Auto-suggest candidates from the structured fields** — if the asker says
    "2016, two performers, Vixen," run `SceneQueryInput` server-side and
    pre-populate the candidate list for voters to confirm (still
    human-resolved, honoring the no-auto-metadata rule).
33. **"Similar solved queries" on the post form:** before submitting,
    BM25-match the description against *solved* query descriptions — the
    answer may already exist.
34. **Backfill link: every solved scene query feeds
    `resolvedIdentificationQueries` into the scene page** as "community
    identified this from a memory" — trust signal and SEO (SPEC §7.10) at
    once.
35. **Detective leaderboard already exists (`myIdentificationDetectiveScore`)
    — add solve *streaks* per SPEC §7.5** and surface them on the board queue
    so hard queries get attention.
36. **Bounty on a query:** attach a bonus-XP bounty (the `bonus_points`
    migration 81 and award service exist) that pays out only on
    `resolveIdentificationQuery` by a *different* user — aligning incentives
    with the "one suggester ≠ resolver" rule.
37. **Queue ranking by solvability:** open queries sorted by "has snapshot,"
    "has ≥1 candidate," "has clarifications" — the ones with evidence get eyes
    first, instead of newest-first only.
38. **Abandonment analytics:** `StatusAbandoned` queries that get *new
    candidates later* should be re-openable — memories resurface.
39. **Bulk "I recognize this face" mode:** show a grid of open queries with
    snapshots; a user who knows performers can sweep them in seconds (the vote
    path is already one tap).
40. **Collage crop as the query snapshot:** let askers pick *the frame they
    remember* from the target scene's collage when the query has `targetId`
    (scene known, performer unknown) — currently `snapshotId` is one field
    with no picker guidance.
41. **Solved-query → edit draft handoff:** one button converts a solved scene
    query into a `SceneDraft`/edit pre-filled with resolved metadata —
    SPEC §7.5 says identification "can trigger metadata creation" *through the
    edit path by a person*; make that path one click instead of retyping.
42. **Expired-evidence labeling:** candidates suggested >1 year ago against
    since-deleted entities (the `entity: Performer` null case) should show as
    "stale suggestion" rather than silently null.
43. **Cross-link queries that share candidates** — if three open queries all
    have "candidate X," show them together; a user solving one may solve all
    three.

## D. Visual evidence at scale (44–55)

44. **Snapshot generation quest:** collages need ≥12 snapshots and a duration
    (`ErrNotEnoughSnapshots`); generate quests that ask contributors to add
    snapshot offsets to under-covered scenes, since every collage is a future
    identification asset.
45. **Mesh-wide collage replication as a preservation target** (SPEC §7.8
    promises it) — a recall search can then match against collages even for
    scenes this instance has no metadata for.
46. **"Storyboards" (SPEC §7.6 mentions them for level 2+)** as an extension
    of collages: more frames, ordered, hover-scrubbable — for the user who
    remembers "there was a kitchen scene *early on*."
47. **Timestamp-to-frame deep links in query descriptions:** let an asker write
    "at ~12:30" and render a clickable frame from the collage — turns prose
    into visual evidence.
48. **Frame-level tag votes:** "this frame shows X" annotations on collage
    frames, aggregated as evidence (never metadata — same rule as
    candidates).
49. **Duplicate-collage detection** to catch the same scene listed under two
    records — pHash distance across collages, feeding the duplicate/merge flow
    (SPEC §7.7).
50. **Fingerprint-corroboration surface (migration 99 already exists)
    exposed as "known duplicates":** a remembered scene may exist under the
    wrong record; show the cluster so search returns both.
51. **OG-image / cover previews on every search result** — `SceneCard` exists;
    ensure it always shows a collage strip, because visual scanning beats
    reading for vague memories.
52. **"Random frame from your results" scratchpad:** user flips through random
    frames across result pages and flags "this looks familiar," and the
    backend re-ranks — browsing-as-query.
53. **Snapshot pHash cache table** with the same hamming-distance index as
    fingerprints (pg-spgist_hamming), making #24/#25 a single query.
54. **Silent 2-second preview GIFs for level 2+** (SPEC §7.8 allows optional
    previews) as the final confirmation step once the set is down to ~5
    candidates.
55. **Collage diff view for near-miss candidates** — when two scenes look
    alike, side-by-side collage comparison resolves which is the remembered
    one.

## E. Community recall paths (56–68)

56. **"Anyone seen this?" share link** that renders the query as a card for
    Discord/Reddit — ToMiT communities fail because they lack structured
    data; the share card *brings* the structure (era, tags, collage) back to
    the thread.
57. **Ingest replies from external threads:** paste a ToMiT thread URL; parse
    suggested titles as *candidate names* for the query — again, evidence not
    metadata (F1's no-URL rule means the ingestion happens server-side and
    only sanitized candidate strings cross any boundary).
58. **Guilds/stewardship (SPEC §7.12):** "adopt a tag/studio" users get pinged
    when a query touches their area — routing queries to the people most
    likely to know.
59. **Notification fan-out on new open queries** matching a user's taste
    vector (`internal/service/notification` exists) — "you're into this
    niche, here's an unsolved memory about it."
60. **Follow a query without voting** (subscription) so askers get push when
    candidates arrive.
61. **"Two-minute drill" daily quest:** surface 3 open queries on the home
    feed each day; answering is a streak (streak service exists).
62. **Confidence-calibrated voting:** show voters "7 of 8 solvers agreed"
    style stats so late voters aren't anchor-biased by the current leader
    (Elo's display-order lesson applies here too).
63. **Solve-quality feedback:** ask the asker "was it actually this?" after
    resolution; a query solved but rejected later should demote that
    resolver's detective score — prevents confidently-wrong solves, the
    archive's worst outcome per the identification package doc.
64. **Expertise decay:** detective scores weight recent solves more, matching
    the Elo decay philosophy (SPEC §7.9) — old knowledge shouldn't dominate
    current queues.

65. **"Ask without an account" rate-limited path** for drive-by ToMiT
    refugees, gated like the email cooldown (the read-open/post-requires-VOTE
    rule stays).
66. **Localized queues:** "solved near you / solved in your language" —
    memories are culture-bound (non-English titles are systematically
    underserved by English tokenizers; see #76).
67. **Weekly "cold case" spotlight** on the home feed: oldest open query with
    a snapshot, via webhook notification to watchers.
68. **Auction-style bounty escalation** where the asker's bonus points roll
    over weekly until claimed — burns stale XP, keeps hard cases alive.

## F. Federation: ask the mesh (69–77)

69. **Broadcast recall queries to peers** — the D2 `Question` + `ask.go` path
    already exists for identification; make *scene-recall* a question kind so
    peer communities answer where local knowledge is thin.
70. **Taste-based peer selection for recall** (`federation/select.go`): ask the
    peers whose taste vectors are *most similar to the asker* first — they're
    statistically likeliest to recognize the scene.
71. **Peer answers as foreign evidence surfaced on the query**
    (`federation_foreign_candidates`) — already read-only and F2-clean; the
    missing piece is showing it to *askers*, not just admins.
72. **Sanitized candidate-name pass-through:** peer answers are strings by
    design (F1); a recall answer like "looks like a 2017 Vixen release" can be
    BM25-matched *locally* against titles to auto-propose local candidates —
    the human still confirms.
73. **Mesh-wide solved-query mirror:** replicate solved identification queries
    (small payloads) so an instance can answer "already solved elsewhere"
    without re-asking.
74. **Federated cold-case quests (SPEC §7.7):** two instances with shared
    unsolved queries run a joint bounty campaign.
75. **Trust-weighted evidence display:** peer `trust_weight` (schema CHECK,
    ≤1) shown next to foreign evidence so readers discount weak sources —
    consistent with "a peer's word is worth less than the local community."
76. **Per-locale tokenizers for recall searches** — partition `scene_search`
    text by language or use ParadeDB's per-field analyzers so CJK/Cyrillic
    titles aren't unsearchable; broad strokes in non-English memory currently
    fail twice.
77. **Evidence expiry display (F3 already expires rows):** when foreign
    evidence has expired, the UI says "peer claims have expired" rather than
    showing stale hints as current.

## G. Taste & similarity recall (78–86)

78. **"More like this remembered one" entry point:** user drags any scene they
    *do* remember onto a workbench, and the system finds scenes similar by
    tag overlap + co-occurrence — using taste vectors as the similarity
    metric.
79. **Collaborative filtering: "users who ruled out the same candidates ended
    up at X"** — log ruled-out candidates as implicit negatives (evidence!)
    and mine them for re-ranking.
80. **Taste-fingerprint seeded result re-ranking** for vague queries: results
    ordered by "people with your Elo taste profile tended to pick this."
81. **Negative taste keys** — taste.go's key prefixes could gain `!:` entries
    for "user has repeatedly rejected this studio/tag," so recall search
    auto-demotes them (guarded by the same recompute-from-events rule as
    trust).
82. **Scene-level Glicko ratings as a tiebreak** within a coverage tier —
    established scenes outrank obscure twins when the query is ambiguous.

83. **"Similar to" block on every scene page** (SPEC §7.4 promises it;
    nothing in the schema provides it yet) — tag-weighted Jaccard + shared
    performers + same-director, no ML required.
84. **Instance gravity slider applied to recall results** (SPEC §7.14): a
    local user's vague query biases toward the instance's own
    heavily-cataloged niches first.
85. **Mesh trending as recall prior:** scenes trending across the mesh are
    likelier to be things people recently watched and might be re-searching
    for ("I saw it last week" is the easiest recall case).
86. **Nearest-neighbor scene embedding (later phase):** embed `details` + tags
    into a vector column with pgvector, ANN-search with the description
    fragment — but only *after* ideas #1/#3 make the text searchable, since
    embedding garbage text helps nothing.

## H. Quests, completion & preservation feeding recall (87–92)

87. **Completion-driven "recallability score":** scenes missing `details`,
    director, or tags are *unfindable by broad strokes*; add a `recallable`
    field to `Completion.missing` so quests directly target the gap this
    whole list is about.
88. **Quest: "add details to 20 scenes with no description"** — the quest
    generator (`quest.Generate`) already builds from completion fields;
    `details` just needs to be a scored field.
89. **Quest: "tag the top 100 most-viewed untagged scenes"** — popular scenes
    failing recall is the worst failure mode.
90. **Bounty: rare/lost-studio scenes (SPEC §7.7) get recall bounties** —
    obscure scenes are exactly what ToMiT can't find anywhere else.
91. **Preservation linkage:** identifying an orphan scene can trigger
    replication (SPEC §7.5) — a solved recall query should enqueue a
    preservation quest if the scene has <2 replicas.
92. **Expected-totals check (migration 97 exists):** surface "this studio
    claims 400 scenes, we have 120" so users know a missing scene may be
    *uncatalogued*, not unremembered — pointing the recall effort at ingestion
    instead of search.

## I. Anti-patterns & trust guardrails (93–97)

93. **Never let recall votes become metadata** — keep the identification
    package's rule ("a vote is evidence, not authority") even under pressure
    to auto-resolve; every idea above routes through
    `resolveIdentificationQuery` or the edit path by a named human.
94. **Deduplicate memory-bait:** same scene described in five queries should
    merge into one *thread* (candidates shared), not five parallel vote pools
    splitting evidence — enforce with a similarity check on post time window +
    structured fields, as evidence-linking, not entity-merging.
95. **"Confidently wrong" blast-radius control:** solved recalls that seed
    recommendations (#34/#83) must be marked provisional until corroborated
    by an edit or a second independent solve, because a wrong canonical link
    poisons every downstream discovery surface — exactly the failure the
    identification package doc warns about.
96. **Spam/bait detection on the board:** a flood of recall queries pointing
    at one studio is a voting-manipulation vector; rate-limit per user per day
    (email-cooldown pattern) and flag bursts to `queryModAudits`.
97. **Honor the content-access gate everywhere:** recall results show metadata
    + collage to level 0 (per SPEC §7.6) but never leak media URLs; snapshot
    assets need the same SSRF-safe URL handling as
    `internal/service/federation/imageurl.go`.

## J. Interfaces & reach (98–100)

98. **Recall-first search page redesign:** `pages/search/SearchAll.tsx` gets a
    "Remember it vaguely?" toggle that swaps the single box for the structured
    builder (#16) + collage grid + "post to the board" escape hatch that
    *pre-fills* the query from whatever they typed.
99. **CLI/API "recall" endpoint for power users and Stash integration:** one
    GraphQL query (`recallScenes(input: RecallQueryInput!)`) combining filters
    + fuzzy + board candidates, documented like the existing REST/GraphQL
    surface so third-party clients (SPEC §7.11) can build ToMiT-style bots on
    top.
100. **Post-solve feedback loop:** every resolved query writes its *successful
     clue set* (which fields were decisive) into a stats table, so the board
     can say "era + studio + duration found 94% of scenes" and teach askers
     what broad strokes are worth providing — the compounding improvement that
     makes this system beat a ToMiT thread over time.

---

## Suggested priority order

1. **#1 / #3 / #4** — index the missing fields (`details`, `director`, tags).
   Cheap, immediate, no new UI.
2. **#16 / #19 / #21** — the structured recall query builder.
3. **#28 / #29 / #32** — board structured fields + candidate notes.
4. **#44 / #53** — snapshot pHashes + collage quests (visual evidence).
5. **#69 / #71** — federated recall, surfaced to askers not just admins.
6. **#87 / #88** — make recallability a completion field so quests sustain it.
7. **#93 / #95** — guardrails as it scales.

The unifying principle: **every clue is evidence until a human resolves it.**





