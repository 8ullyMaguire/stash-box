# SPEC — suki ideas addendum (2026-10-31)

Second and third pass over suki, after the capability addendum. The first pass took suki's
`docs/FEATURES.csv` (what it built). This one takes **what suki thought about**: the brainstorms in
`docs/brainstorm/`, the plans it filed, and the 306 rows on Concord project `suki`.

## Sources and what they produced

| source | candidates | note |
|---|---|---|
| `docs/brainstorm/*.md` (7 docs, gravity-100 excluded) | 165 | invites (54), trending (38), links directory (24), preservation DNA (22), recommendation upgrades (11), tag clusters (10), came affinity (4), taste gated (2) |
| Concord project `suki` | 306 | 251 `draft` + 55 `shipped`, including the whole `Gravity #1–100` set |
| — total candidates | 471 | |
| filed as `discussion` here | **290** | |
| dropped as duplicates | 13 | near-match against the 439 rows already on this project |
| dropped as prose fragments | 87 | brainstorm sub-details, not features ("Uses and expiry already exist", "Revocation") |
| dropped for want of a topic | 77 | the router could not place them; listed in the appendix, not deleted |
| conflicts | 1 | `API v2 and OpenAPI document` — GraphQL is the contract here |

`gravity-100` is excluded from the brainstorm count because all one hundred ideas are already filed on
the suki project as `Gravity #<n> — …` and arrive here through the Concord leg.

## Method, and where it is trustworthy

1. **Rename the medium, keep the mechanism** — picture→scene, artist→performer, pool→collection,
   booru→source, dhash→perceptual hash. Applied to title and body, so a row reads as a feature of *this*
   fork rather than a note about someone else's image board.
2. **Dedupe against everything already here** — exact title, then a near-match at 0.78. The near-match
   is what stops "Best of the week" and "Collection of the week" both landing as new ideas.
3. **Route to an extension by title, then by source document.** This is the weak step and it is worth
   saying plainly: the extension label is a **routing heuristic**, not a judgement about what the idea is
   for. A row whose extension looks wrong is a row whose title did not say enough, not a row that does not
   belong. Each row carries its source pointer (`concord:suki`, `brainstorm:<file>#<n>`) so the original
   sentence is one lookup away.
4. **Drop prose, not ideas.** 87 brainstorm items are sub-details ("uses and expiry already exist") rather
   than features, and 77 could not be routed at all. Both sets are in
   `docs/spec/suki-ideas-dropped.tsv` with the reason — an omission a reader can see is a decision; an
   invisible one is a defect. The 77 are the first thing to re-read if this intake is ever revisited.

## What is worth scheduling, in dependency order

The 290 rows are an ideas board. This is the subset the fork should actually do, and why each comes
before the next:

**S1 · Ranking instruments before ranking surfaces.** Suki's trending brainstorm is the most useful thing
in the whole intake because it starts by measuring: a tag has no history, counts are scalars, and three of
the four engagement signals are empty. The deliverable here is the substrate — a daily rollup per tag and
per performer, a decay-weighted count rather than a bare window, a stated minimum support, and **two
counts rendered side by side, never a bare arrow** ("30 this week, 10 last" is checkable; "↑200%" is not).
Without this, every trend surface is an opinion with a chart on it.

**S2 · Per-surface blend, not one global ranking.** The fork already has a For You surface and similarity;
what suki worked out is that search, similar, tag pages and the home rail want *different* blends, and that
a ranking which ages must carry its clock. That is a small change to the ranker contract with a large
effect on whether recommendations feel right in context.

**S3 · Preservation as evidence.** Signed manifests, existence proofs ("I still have this hash", with a
date), "preserved by N instances" rendered from attestations rather than promises, and drift alerts that
pause pulls instead of deleting what was already mirrored. This is the part of the mesh that has no
implementation yet on either side.

**S4 · Invites as an edge, and a public modlog.** Suki's finding is structural: the invite *code* exists
in both systems, but the invite *use* does not — without `invite_use(invite_code, user_id, used_at)` there
is no tree, no attribution, and no way to answer "who invited whom". Paired with a public modlog and
"interest-coded" invites (a cohort arrives sharing a slice), this is the cheapest growth mechanism that
composes with the trust ladder instead of bypassing it.

**S5 · Tag clusters and coverage as a weighted share.** Clusters give the recommender a vocabulary between
"one tag" and "the whole ontology"; coverage-as-a-weighted-share is the instrument that says whether the
corpus is drifting from the profile. Both are read-only instruments, which is why they come after the
rollup and before any new surface.

**S6 · Power-user surface last.** The TUI/CLI/keyboard set is real but it multiplies whatever exists; it is
worth building once the instruments above have something to show.

## Invariants worth adopting as spec, not as features

Suki's ADRs are named rules, and several of them answer questions this fork has open. Adopting the rule is
cheaper than re-deriving it:

| suki ADR | the rule | where it lands here |
|---|---|---|
| 0101 | an extension is code behind a switch | `docs/EXTENSIONS.md` §2 — already the contract |
| 0104 | a manifest is a claim about a moment | preservation manifests (S3): a manifest is dated, not timeless |
| 0103 | coverage is a weighted share | the coverage instrument (S5) |
| 0105 | an assignment is a row, an exposure is a denominator | experiment/rank evaluation: never count an impression as a preference |
| 0107 | pair for information, not only coverage | arena/Elo pairing: pairs chosen to inform, not to balance load |
| 0109 | a ranking that ages must carry its clock | every stored score is stored with the time it was computed |
| 0108 | an editorial slot must say whose voice it is | any operator pin is labelled as the operator's, never as the community's |
| 0099 | association is not co-occurrence | related-tag strips: co-occurrence alone is not a relationship |
| 0087 | an invite is an edge or it is a rumour | S4: `invite_use` is the feature; codes without edges are decoration |
| 0088 | affinity accelerates privilege, never punishment | trust/affinity may raise a limit, never lower someone's reach |
| 0081 | a refusal is taste, a takedown is not | keeps the personal / policy distinction out of the moderation queue |
| 0085 | a bulk takedown is a decision | bulk moderation is an audited object, not a query |
| 0097 | a quarantined scene keeps its address | quarantined content resolves with a status, never a 404 |
| 0096 | evidence never a switch | automated signals are attributed evidence; they do not flip a boolean that ships content |

## What this does not do

No ranking of the 290 was invented: they are `discussion` rows, and S1–S6 is a dependency order, not a
schedule. The extension labels are heuristics (see §Method). Nothing here changes `docs/PREMISE.md`; the
invariants above are all consistent with it, which is itself worth noting — suki arrived at the same wall
from a different direction.
