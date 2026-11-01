# SPEC — Rediscovery intake: verdicts for all 600 features

Source of record: `docs/spec/rediscovery-2026-10-31.md` (pasted by the operator on 2026-10-31,
transcribed verbatim, 600 numbered features across sections A–Q). This file is the per-feature
verdict, so nothing is adopted by accident and nothing is dropped in silence.

## The rule

Every feature gets exactly one verdict, and the rule for each is mechanical:

- **present** — a named type in `graphql/schema/types/` or a file in `docs/spec/` in *this* fork already
  implements it. The evidence column names it. No credit is taken for something merely planned.
- **conflict** — it contradicts a commitment in `docs/PREMISE.md` (visible write path, nothing derived
  stored, signals about works not readers, chronological feed stays chronological, automation suggests
  never decides) or it inverts the fork's stack (PostgreSQL, GraphQL, React client). Conflicts are not
  "no": each is shippable as an off-by-default extension once the conflict is named.
- **fit** — not demonstrably present, and not in conflict. Filed as a `discussion` row on the Concord
  project, bound to the extension that would ship it.

The defaults are honest about their shape: **absent evidence of an implementation, the verdict is `fit`,
not `present`.** A duplicate idea row is cheap and visible; a false "already built" is a lie that stops
work.

## The finding, before the table

The spec is largely a re-derivation of what this fork already runs: of its 600 features,

**220 are present**, **18 conflict** with a premise or the stack, and **362 fit** and are filed as ideas.

That ratio is the useful part of this intake: the sections that are mostly `present` (A, B, C, F, G)
describe machinery this fork already has and should be read as a checklist of *tuning knobs* rather than
a build list. The sections that are mostly `fit` (E, O, K, N, Q) are where the actual difference is —
above all **O, the extension system**, which is the one structural thing the owner asked for and this
fork does not have.

## Extension map

Every feature is bound to the extension that would ship it, with that extension's default state under
the owner's stated priorities (quality content, recommendations on every surface, rediscovery of
obscure work, user-driven with minimal operator overview). The catalogue, the defaults and the
reasoning are in `docs/EXTENSIONS.md`; the ordered work is in `docs/ROADMAP-extensions.md`.

## Verdict table (600 rows)


### A · Core Data Model (1–30) — 10 fit / 20 present / 0 conflict

| # | feature | verdict | extension | evidence / reason |
|---:|---|---|---|---|
| 1 | Every cataloged item is represented as an **Item Record** consisting exclusively of metadata fields — no binary blobs, thumbnails, or media are… | present | `core-metadata` | Scene / ContentItem records |
| 2 | Each Item Record carries a globally unique **Item ID** (UUIDv7, time-sortable). | fit | `core-metadata` |  |
| 3 | Items have a required **canonical title** field (free-text, Unicode-safe, max 512 chars). | present | `core-metadata` | Scene.title |
| 4 | Items have an optional **original-language title** field for non-English originals. | fit | `core-metadata` |  |
| 5 | Items support an **alias list** — unlimited alternative titles, romanizations, abbreviations. | present | `core-metadata` | Performer.alias_list |
| 6 | Items carry a **source URL** field (primary location the submitter encountered the content). | present | `core-metadata` | URL type |
| 7 | Items support a **mirror URL list** for additional known locations. | fit | `core-metadata` |  |
| 8 | Items carry a **perceptual hash** field (pHash, dHash, or aHash) so duplicates can be detected across re-encodes. | present | `core-metadata` | Fingerprint, DraftFingerprint, FingerprintCluster |
| 9 | Items carry an optional **cryptographic hash** field (SHA-256) for exact-match deduplication. | fit | `core-metadata` |  |
| 10 | Items store a **media type** enum: video, image-set, audio, interactive, text, mixed, unknown. | present | `core-metadata` | ContentItem kind (image, gallery, animation, text, audio, comic) |
| 11 | Items store a **duration** field (nullable, seconds, integer) for time-based media. | present | `core-metadata` | Scene.duration |
| 12 | Items store a **resolution** field (nullable, e.g., 1080p, 4K) as user-reported metadata. | fit | `core-metadata` |  |
| 13 | Items store a **file-size estimate** field (nullable, bytes) as user-reported metadata. | fit | `core-metadata` |  |
| 14 | Items store a **release date** or **first-seen date** (ISO 8601). | present | `core-metadata` | FuzzyDate |
| 15 | Items store a **studio/producer** attribution field (free-text, normalized via suggestions). | present | `core-metadata` | Studio |
| 16 | Items store a **performer credit list** — ordered list of performer names with optional role annotations. | present | `core-metadata` | PerformerAppearance |
| 17 | Performer names link to **Performer Records** which are their own first-class entities. | present | `core-metadata` | Performer |
| 18 | Performer Records hold: aliases, active-years range, self-described attributes, and linked items. | present | `core-metadata` | Performer.alias_list + career_start/career_end |
| 19 | Items store a **series/collection** reference — linking items that are episodes or parts of a set. | fit | `core-metadata` |  |
| 20 | Series Records hold: title, ordering scheme (sequential, date-based, unordered), and child items. | fit | `core-metadata` |  |
| 21 | Items carry a **language** tag (ISO 639-1) and an **origin country** tag (ISO 3166-1). | fit | `core-metadata` |  |
| 22 | Items carry a **content descriptor set** — a flat set of community-standardized short descriptor strings. | present | `core-metadata` | Tag |
| 23 | Items carry a **user-submitted free-text description** (max 2000 chars, Markdown-lite). | present | `core-metadata` | Scene.details |
| 24 | Items carry a **content-warning flags** set, distinct from descriptive tags, to support filtering. | present | `core-metadata` | ContentWarning |
| 25 | Every Item Record tracks its **submitter user ID**, **submission timestamp**, and **edit history**. | present | `core-metadata` | Edit (author, created_at) |
| 26 | Edit history is stored as an append-only **changelog** — each edit records who, when, which fields, old→new values. | present | `core-metadata` | Edit + EntityChange (append-only) |
| 27 | Item Records have a **status** enum: `active`, `pending-review`, `merged`, `deleted`, `disputed`. | present | `core-metadata` | DraftSubmissionStatus, edit states |
| 28 | Deleted items are soft-deleted — metadata retained internally for merge-chain integrity but hidden from search. | present | `core-metadata` | deleted scenes retained behind Scene.deleted |
| 29 | Merged items carry a **redirect pointer** to the surviving canonical Item Record. | present | `core-metadata` | scene merge edits (QueryExistingSceneResult) |
| 30 | All text fields are stored UTF-8, indexed with ICU-aware collation for correct multilingual sorting. | fit | `core-metadata` |  |

### B · Tagging & Taxonomy (31–70) — 19 fit / 21 present / 0 conflict

| # | feature | verdict | extension | evidence / reason |
|---:|---|---|---|---|
| 31 | The tagging system is **folksonomy-first** — any user can apply any tag string to any item. | present | `core-tags` | Tag (any user may apply) |
| 32 | Tags are normalized on write: lowercased, trimmed, collapsed whitespace, stripped of leading/trailing punctuation. | present | `core-tags` | tag normalisation in the edit write path |
| 33 | Tags have a **namespace prefix** system: `genre:`, `act:`, `attribute:`, `mood:`, `setting:`, `era:`, `kink:`, `bodytype:`, `clothing:`,… | present | `core-tags` | TagCategory |
| 34 | The system maintains a **Tag Dictionary** — a living registry of all tags that have ever been applied. | present | `core-tags` | Tag table |
| 35 | Each Tag Dictionary entry tracks: first use date, total item count, total user count, synonym links, parent tag, child tags. | present | `core-tags` | TagFacets |
| 36 | Tags can be linked as **synonyms** by any user; synonym proposals enter a lightweight vote queue. | fit | `core-tags` |  |
| 37 | Synonym links are directional: secondary → primary; searches on secondary auto-expand to primary. | present | `core-tags` | OntologyDeclaration expansion |
| 38 | Tags can be organized into **hierarchical trees**: e.g., `genre:outdoor` → `genre:outdoor:beach`. | present | `core-tags` | TagCategoryTreeNode |
| 39 | Hierarchy proposals are user-submitted and resolved via lazy consensus (auto-accepted if no objection in 72 h). | fit | `core-tags` |  |
| 40 | Each item's tags carry a **per-user attribution** — the system remembers who applied which tag. | present | `core-tags` | Edit authorship per tag row |
| 41 | Tags on an item carry a **consensus weight**: the count of distinct users who independently applied that tag. | present | `core-tags` | EditVote tallies |
| 42 | Tags with consensus weight ≥ 3 are promoted to **confirmed tags** and rendered with a visual indicator. | fit | `core-tags` |  |
| 43 | Tags with consensus weight = 1 remain **provisional** and are searchable but styled differently. | fit | `core-tags` |  |
| 44 | Users can **dispute** a tag on an item — a negative vote that decrements the consensus weight. | fit | `core-tags` |  |
| 45 | If a tag's net weight drops to ≤ −2, it is auto-hidden (soft-removed) from the item. | fit | `core-tags` |  |
| 46 | A **tag implication** system allows rule-based auto-tagging: e.g., `tag:A implies tag:B` proposals. | present | `core-tags` | TagImplication |
| 47 | Tag implications are voted on globally; accepted implications retroactively apply to all matching items. | present | `core-tags` | OntologyFieldDecision |
| 48 | A **tag alias** is a hard redirect at the dictionary level — searches and submissions for alias X always resolve to canonical Y. | fit | `core-tags` |  |
| 49 | A **tag blacklist** per user: tags the user never wants to see in results or recommendations. | present | `core-tags` | Filter (per-user exclusions) |
| 50 | A **tag whitelist** per user: affinity tags the user wants boosted in recommendation scoring. | present | `core-tags` | AffinityContribution |
| 51 | The system supports **negative tags** on items: explicit assertions that a tag does NOT apply (helps train models). | fit | `core-tags` |  |
| 52 | Tags support an optional **intensity/degree** qualifier: e.g., `mood:romantic(mild)`, `mood:romantic(strong)`. | fit | `core-tags` |  |
| 53 | The system tracks **tag velocity** — how fast a tag's usage is growing — to surface trending descriptors. | fit | `core-tags` |  |
| 54 | A **tag cloud** visualization for any item, performer, series, or the global catalog. | fit | `core-tags` |  |
| 55 | Tags are **searchable with boolean logic**: AND, OR, NOT, parentheses, namespace filters. | present | `core-tags` | discovery query language (docs/spec/feature-discovery.md §4) |
| 56 | Tag search supports **wildcard matching**: `act:*` returns all items with any tag in the `act:` namespace. | fit | `core-tags` |  |
| 57 | A **tag suggestion engine**: when a user begins tagging an item, the system suggests tags based on similar items' tags. | fit | `core-tags` |  |
| 58 | Tag suggestions are ranked by **co-occurrence frequency** with already-applied tags. | present | `core-tags` | TagFacets co-occurrence |
| 59 | The system computes **tag correlation matrices** offline — pairs of tags that frequently co-occur. | fit | `core-tags` |  |
| 60 | The system surfaces **under-tagged items** — items with fewer than N tags — as a gamified curation task. | present | `core-tags` | LintQuestDefinition, LintCandidate |
| 61 | Batch tagging: a user can select multiple items and apply a tag to all of them in one action. | fit | `core-tags` |  |
| 62 | A **tag audit log** per item: chronological record of every tag addition, removal, dispute. | present | `core-tags` | Edit history per entity |
| 63 | Tag namespaces are **operator-configurable** at instance level — the operator can add or rename namespace prefixes. | present | `core-tags` | TagCategory is operator-editable |
| 64 | Tag length limit: 64 characters per tag string (excluding namespace prefix). | fit | `core-tags` |  |
| 65 | Maximum tags per item: 200 (configurable by operator). | fit | `core-tags` |  |
| 66 | Tags are stored in a dedicated **tags table** with an inverted index for sub-millisecond lookup. | present | `core-tags` | tag indexes in migrations |
| 67 | A **related tags** API endpoint: given a tag, return the top-N most correlated tags by PMI (pointwise mutual information). | present | `core-tags` | TagFacets PMI ranking |
| 68 | Tags carry a **spoiler flag** — users can mark a tag as a spoiler, hiding it behind a click-to-reveal. | present | `core-tags` | ContentWarning spoiler mode |
| 69 | The system supports **timed tags** for video items — tags that apply only to a specific timestamp range (chapter-level tagging). | fit | `core-tags` |  |
| 70 | Timed tags are stored as (tag, start_seconds, end_seconds) tuples and are searchable with a "contains moment matching" query. | fit | `core-tags` |  |

### C · User Accounts & Identity (71–100) — 20 fit / 10 present / 0 conflict

| # | feature | verdict | extension | evidence / reason |
|---:|---|---|---|---|
| 71 | User registration requires only a **username** and **password** — no email required (email optional for recovery). | present | `identity` | User (username + password) |
| 72 | Passwords are stored using **Argon2id** with per-user salt. | fit | `identity` |  |
| 73 | Optional **TOTP 2FA** support for account security. | fit | `identity` |  |
| 74 | Users have a **public profile page** showing: username, join date, catalog stats, public lists, taste fingerprint. | present | `identity` | User profile page |
| 75 | Users can set a **display name** separate from their login username. | fit | `identity` |  |
| 76 | Users can write a short **bio** (max 500 chars). | fit | `identity` |  |
| 77 | Users have a **privacy level** toggle: `public`, `semi-private` (stats visible, lists hidden), `private` (fully hidden). | fit | `identity` |  |
| 78 | Private users still contribute to collaborative filtering — their data is used in aggregate but never individually exposed. | fit | `identity` |  |
| 79 | Users have a **role** enum: `member`, `trusted`, `moderator`, `admin` (operator-assigned). | present | `identity` | User roles (member..admin) |
| 80 | The `trusted` role is auto-granted after configurable thresholds (e.g., 50 items cataloged, 30 days active, net-positive tag consensus). | fit | `identity` |  |
| 81 | Only `trusted` users can vote on tag implications, synonym proposals, and hierarchy changes. | present | `identity` | edit voting gated on trust |
| 82 | `moderator` role can resolve disputes, force-merge items, and issue temporary mutes. | present | `identity` | ModAudit + moderator edit powers |
| 83 | `admin` role is for the instance operator — full access to configuration, user management, data export. | present | `identity` | admin role |
| 84 | Users can **export their entire personal data** as a JSON or CSV archive at any time (GDPR-style). | fit | `identity` |  |
| 85 | Users can **delete their account** — personal data is scrubbed, but their tag/rating contributions are anonymized and retained for catalog integrity. | fit | `identity` |  |
| 86 | Users can **block** other users — bilateral invisibility in all social features. | fit | `identity` |  |
| 87 | Users can maintain **multiple independent taste profiles** under one account (e.g., "mood A" vs. "mood B") to avoid polluting a single recommendation… | fit | `identity` |  |
| 88 | Each taste profile has its own ratings, lists, blacklists, and whitelists. | fit | `identity` |  |
| 89 | The active taste profile is switchable via a single dropdown in the UI. | fit | `identity` |  |
| 90 | Users accumulate a **curation reputation score** based on: how often their tags reach confirmed status, how often their submissions are upvoted, how… | present | `identity` | UserTrust, Standing, TierStanding |
| 91 | Reputation score is visible on profile and used as a tiebreaker in consensus disputes. | fit | `identity` |  |
| 92 | Users can set a **preferred language** for the UI and for default title display. | fit | `identity` |  |
| 93 | Users can set a **timezone** for correct display of timestamps. | fit | `identity` |  |
| 94 | API access is available via **personal API tokens** — users can generate/revoke tokens from settings. | present | `identity` | API keys |
| 95 | API tokens support **scoped permissions** — read-only, write-own, or full-access. | present | `identity` | API key scopes |
| 96 | The system tracks **last-active timestamp** per user for staleness detection in recommendation scoring (recent activity weighted higher). | fit | `identity` |  |
| 97 | Users can enable **login notifications** — receive an alert (in-app or optional email) on new login from unrecognized IP. | fit | `identity` |  |
| 98 | Sessions are managed via **signed JWTs** with configurable expiry (default 30 days, sliding). | present | `identity` | SessionState + signed session cookies |
| 99 | A **session list** in settings shows all active sessions with IP and user-agent; users can revoke any session. | fit | `identity` |  |
| 100 | Rate limiting is enforced per-user: configurable limits on submissions, edits, and API calls per hour. | fit | `identity` |  |

### D · Rating & Interaction (101–140) — 29 fit / 11 present / 0 conflict

| # | feature | verdict | extension | evidence / reason |
|---:|---|---|---|---|
| 101 | Users can **rate** any item on a configurable scale — default is 1–10 integer stars (operator can set 1–5, 1–100, or thumbs-up/down). | fit | `ratings` |  |
| 102 | Ratings are **private by default** — only the aggregate is public unless the user opts to make their ratings public. | fit | `ratings` |  |
| 103 | Each item displays a **Bayesian weighted average** rating (smoothed to prevent small-sample extremes). | fit | `ratings` |  |
| 104 | Each item displays the **raw arithmetic mean**, **median**, and **rating count** alongside the Bayesian average. | fit | `ratings` |  |
| 105 | Each item displays a **rating distribution histogram** — bar chart of how many users gave each score. | fit | `ratings` |  |
| 106 | Users can mark an item as **seen/consumed** without rating it — a binary "I have encountered this" flag. | present | `ratings` | seen state in scene_for_you |
| 107 | Users can mark an item as **want-to-see** — a wishlist/backlog flag. | fit | `ratings` |  |
| 108 | Users can mark an item as **favorite** — a special flag distinct from a high rating, indicating personal significance. | present | `ratings` | FavoritePerformerScene, FavoriteStudioScene |
| 109 | Favorites are optionally public and displayed on the user's profile. | fit | `ratings` |  |
| 110 | Users can **upvote** or **downvote** the quality of an Item Record's metadata (not the content itself) — "is this entry well-documented?" | present | `ratings` | EditVote on metadata edits |
| 111 | Metadata quality votes feed into a **catalog health score** per item. | present | `ratings` | CompletenessClaim, CompletenessVerdict |
| 112 | Items with low catalog health are surfaced to curators as "needs attention." | present | `ratings` | LintCandidate |
| 113 | Users can write a **short review** (max 1000 chars) attached to their rating — text-only, no media embeds. | present | `ratings` | Review, ReviewSummary |
| 114 | Reviews can be marked **spoiler** — hidden behind a click gate. | fit | `ratings` |  |
| 115 | Reviews can be upvoted by other users as "helpful." | present | `ratings` | ReviewSummary useful votes |
| 116 | The system computes a **taste compatibility score** between any two users based on rating vector cosine similarity. | present | `ratings` | AffinityContribution (taste profile) |
| 117 | Users can see their top-N most compatible and least compatible users (if those users are public). | fit | `ratings` |  |
| 118 | The system supports **rating imports** — bulk upload of ratings from a CSV or JSON file. | fit | `ratings` |  |
| 119 | Rating changes are versioned — a user can see the history of their own rating for any item. | fit | `ratings` |  |
| 120 | The system computes **per-user rating statistics**: mean, standard deviation, skewness — displayed on profile. | fit | `ratings` |  |
| 121 | Users who rate everything 10 or everything 1 are auto-detected; their ratings are down-weighted in aggregate calculations. | fit | `ratings` |  |
| 122 | A **"rate similar"** workflow: after rating an item, the system immediately suggests 5 similar un-rated items. | fit | `ratings` |  |
| 123 | Users can **flag** an item for: duplicate, incorrect metadata, broken URL, spam, or policy violation. | present | `ratings` | ContentSourceAppeal + edits |
| 124 | Flags enter a community-driven **resolution queue** visible to trusted/moderator users. | present | `ratings` | moderation queue |
| 125 | Flags are resolved by lazy consensus: first trusted user to act resolves it; a second can override. | fit | `ratings` |  |
| 126 | Users can **annotate** an item with timestamped notes (for video: "at 3:42, scene changes to..."). | fit | `ratings` |  |
| 127 | Annotations are personal by default but can be shared publicly. | fit | `ratings` |  |
| 128 | Public annotations from multiple users are merged into a **community timeline** overlay for an item. | fit | `ratings` |  |
| 129 | Users can **react** to items with a small emoji set (😍🔥😴💎🗑️) for lightweight sentiment without a full rating. | fit | `ratings` |  |
| 130 | Reactions are aggregated and displayed as counts on the item. | fit | `ratings` |  |
| 131 | The system tracks a **personal consumption log**: a timestamped diary of when the user interacted with each item. | fit | `ratings` |  |
| 132 | The consumption log is exportable. | fit | `ratings` |  |
| 133 | Users can leave a **"tip" note** on an item — a short freeform annotation aimed at future viewers (e.g., "best part starts at 12:00"). | fit | `ratings` |  |
| 134 | Tip notes are voted on for helpfulness and the top tip is pinned. | fit | `ratings` |  |
| 135 | Users can mark an item as **"lost media"** — indicating the source URL is dead and the content may be unfindable. | fit | `ratings` |  |
| 136 | Lost media items are visually distinct in search results and carry a community bounty for anyone who finds a live source. | fit | `ratings` |  |
| 137 | Users can **endorse** another user's review — a lightweight signal of agreement. | present | `ratings` | review votes |
| 138 | The system detects **rating rings** — small groups of users who suspiciously rate the same items identically — and flags them for review. | fit | `ratings` |  |
| 139 | A **daily/weekly digest** (opt-in, in-app or email): personalized summary of new items matching the user's taste profile. | fit | `ratings` |  |
| 140 | Users can set a **minimum catalog health score** filter — suppressing items that are poorly documented from their results. | fit | `ratings` |  |

### E · Collaborative Filtering ("Gravity") (141–200) — 50 fit / 10 present / 0 conflict

| # | feature | verdict | extension | evidence / reason |
|---:|---|---|---|---|
| 141 | The recommendation engine is the platform's core value proposition, branded internally as **Gravity** — it pulls users toward items that their taste… | fit | `gravity` |  |
| 142 | Gravity uses a **hybrid collaborative filtering** approach: user-user CF + item-item CF + content-based (tag-vector) signals, blended by a… | fit | `gravity` |  |
| 143 | **User-user CF**: for each target user, identify the top-K most taste-compatible users (neighbors) and recommend their highly-rated items that the… | present | `gravity` | scene_for_you user-neighbour scoring |
| 144 | User similarity is computed via **cosine similarity** on the rating vector, considering only co-rated items (minimum overlap threshold: 5 items). | fit | `gravity` |  |
| 145 | **Item-item CF**: for each item the target user has rated highly, find items most frequently co-rated-highly by the same users, and recommend those. | fit | `gravity` |  |
| 146 | Item similarity is stored in a precomputed **sparse similarity matrix**, rebuilt on a configurable schedule (default: nightly). | fit | `gravity` |  |
| 147 | **Content-based signal**: each item is represented as a **TF-IDF-weighted tag vector**; the user's taste profile is the centroid of their… | present | `gravity` | affinity from tags (AffinityContribution) |
| 148 | Content-based recommendations are the cosine-nearest items to the user's taste centroid that the user hasn't seen. | fit | `gravity` |  |
| 149 | The three signals (user-CF, item-CF, content-based) are blended using **learned weights** per user — the system tracks which signal source has… | fit | `gravity` |  |
| 150 | A **cold-start onboarding** flow: new users are shown a grid of 30 diverse, popular items and asked to mark seen/unseen and rate any they recognize;… | fit | `gravity` |  |
| 151 | If a new user skips onboarding, the system falls back to **popularity-weighted recommendations** until enough data accumulates. | fit | `gravity` |  |
| 152 | Gravity supports **negative signal**: items the user has rated poorly, blacklisted tags, and blocked performers all repel recommendations. | present | `gravity` | Filter + negative affinity |
| 153 | The system maintains a **taste embedding** per user: a dense vector (default 64 dimensions) learned via **matrix factorization** (ALS — Alternating… | fit | `gravity` |  |
| 154 | Item embeddings are learned in the same latent space — recommendation is a nearest-neighbor lookup in embedding space. | fit | `gravity` |  |
| 155 | Matrix factorization is retrained on a configurable schedule (default: nightly) using **implicit** (seen/unseen, favorite, time-spent) and… | fit | `gravity` |  |
| 156 | The system supports **Approximate Nearest Neighbor** search (via HNSW index, using `hnswlib` or `usearch`) for sub-millisecond recommendation… | fit | `gravity` |  |
| 157 | Gravity applies a **diversity injection**: the final recommendation list is re-ranked to ensure no single tag, performer, or studio dominates more… | fit | `rediscovery` |  |
| 158 | Gravity applies a **novelty boost**: items with fewer total ratings receive a configurable uplift to combat popularity bias and surface obscure… | fit | `rediscovery` |  |
| 159 | Gravity applies a **serendipity injection**: a configurable percentage (default 10%) of recommendation slots are filled with items outside the user's… | present | `rediscovery` | serendipity in scene_for_you |
| 160 | Gravity applies **temporal decay**: ratings from long ago contribute less to the user's taste profile than recent ratings (half-life configurable,… | present | `gravity` | time-decayed trending (docs/spec/feature-discovery.md §7) |
| 161 | The system can generate a **"Because you liked X"** explanation for each recommendation, citing the specific item(s) and/or taste-neighbor(s) that… | present | `gravity` | SimilarityReason |
| 162 | The system can generate a **"Users like you also loved"** explanation variant. | fit | `gravity` |  |
| 163 | The system can generate a **"Matches your tag affinity for [tag]"** explanation variant. | present | `gravity` | AffinityContribution reasons |
| 164 | Users can give **feedback on recommendations**: thumbs-up (good rec), thumbs-down (bad rec), or "already seen" — this feedback is fed back into the… | fit | `gravity` |  |
| 165 | A **"Surprise me"** button: returns a single random item, weighted by predicted rating (i.e., biased random toward things the user is likely to… | present | `rediscovery` | serendipity surface |
| 166 | A **"Deep cut"** button: returns a recommendation from the bottom quartile of popularity — maximizing obscurity while still predicting positive… | fit | `rediscovery` |  |
| 167 | A **"Explore neighborhood"** mode: shows the user their nearest taste-neighbors' recent favorites as a feed. | fit | `gravity` |  |
| 168 | A **"Taste map"** visualization: a 2D UMAP projection of the item embedding space with the user's rated items highlighted and recommended items… | fit | `gravity` |  |
| 169 | A **"Taste fingerprint"** widget: a radar chart of the user's top-10 tag affinities, displayable on their profile. | fit | `gravity` |  |
| 170 | The system computes **per-tag affinity scores** for each user: how much more (or less) the user rates items with that tag versus their personal mean. | present | `gravity` | AffinityContribution per-tag scores |
| 171 | Tag affinity scores power content-based recs and are exposed in the API. | fit | `gravity` |  |
| 172 | The system detects **taste drift**: if a user's recent ratings diverge significantly from their historical profile, the system upweights recent data… | fit | `gravity` |  |
| 173 | Gravity supports a **"focus mode"**: the user temporarily constrains recommendations to a specific tag, performer, or studio — the engine restricts… | fit | `gravity` |  |
| 174 | Gravity supports **negative focus**: "show me things I might like that have NOTHING to do with [tag/performer/studio]." | fit | `gravity` |  |
| 175 | The system maintains a **recommendation cache** per user — precomputed top-200 recs, refreshed on rating activity or on a schedule. | fit | `gravity` |  |
| 176 | Recommendation API response time target: < 100ms from cache, < 2s on cold computation. | fit | `gravity` |  |
| 177 | Gravity supports **group recommendations**: given a set of 2+ users, find items that maximize predicted enjoyment across all members (aggregation… | fit | `gravity` |  |
| 178 | The system tracks **recommendation hit rate**: what percentage of recommended items does the user eventually interact with? This is the primary… | fit | `gravity` |  |
| 179 | The system tracks **recommendation satisfaction rate**: of interacted recommendations, what percentage did the user rate positively? | fit | `gravity` |  |
| 180 | Both hit rate and satisfaction rate are displayed to the operator in an **engine health dashboard**. | fit | `gravity` |  |
| 181 | The engine supports **pluggable algorithm backends** — the operator can swap in a different algorithm (e.g., Bayesian Personalized Ranking, LightFM,… | fit | `gravity` |  |
| 182 | All recommendation computation runs **asynchronously** in a background worker queue — never blocks HTTP request threads. | fit | `gravity` |  |
| 183 | The engine degrades gracefully: if the worker is behind, stale cached recs are served with a "recommendations may be slightly outdated" indicator. | fit | `gravity` |  |
| 184 | The engine supports **A/B testing**: the operator can run two algorithms simultaneously and compare hit/satisfaction rates. | fit | `gravity` |  |
| 185 | The system computes **global item popularity** using a time-decayed score (Wilson score interval on recent ratings) — used as a fallback and a… | present | `gravity` | Elo + LeaderboardEntry |
| 186 | Popularity is computed per-tag as well: "most popular items tagged `mood:romantic` this month." | fit | `gravity` |  |
| 187 | The system identifies **hidden gems**: items with high average rating but low total ratings — surfaced in a dedicated discovery feed. | fit | `rediscovery` |  |
| 188 | The system identifies **polarizing items**: items with high rating variance — surfaced in a dedicated "love it or hate it" feed. | fit | `rediscovery` |  |
| 189 | The system computes **taste clusters**: unsupervised clustering (k-means or DBSCAN) on user embeddings to identify natural communities of taste. | fit | `gravity` |  |
| 190 | Each cluster is auto-labeled with its top distinguishing tags. | fit | `gravity` |  |
| 191 | Users can see which cluster(s) they belong to and browse cluster-level charts. | fit | `gravity` |  |
| 192 | The system computes **item archetypes**: clustering on item embeddings to identify natural groupings. | fit | `gravity` |  |
| 193 | Archetype pages show: representative items, distinguishing tags, most/least enthusiastic user clusters. | fit | `gravity` |  |
| 194 | The recommendation engine runs entirely on **CPU** — no GPU required, ensuring commodity hardware compatibility. | fit | `gravity` |  |
| 195 | ALS factorization for 100K users × 500K items × 64 dimensions completes in < 30 minutes on a 4-core machine using BLAS-optimized libraries. | fit | `gravity` |  |
| 196 | HNSW index for 500K items × 64 dimensions fits in < 500 MB RAM and queries in < 1 ms. | fit | `gravity` |  |
| 197 | The engine's **retraining schedule** is configurable: manual, cron-based, or triggered by a threshold of new ratings since last train. | fit | `gravity` |  |
| 198 | The engine exports a **model artifact** (numpy arrays + index files) that can be version-controlled and rolled back. | fit | `gravity` |  |
| 199 | The system exposes a **"Why not this?"** tool: the user can input an item they expected to be recommended and the system explains why it wasn't (low… | fit | `gravity` |  |
| 200 | All Gravity computations are **deterministic** given the same input data and random seed — ensuring reproducibility for debugging. | fit | `gravity` |  |

### F · Search & Discovery (201–240) — 12 fit / 28 present / 0 conflict

| # | feature | verdict | extension | evidence / reason |
|---:|---|---|---|---|
| 201 | **Full-text search** across item titles, aliases, descriptions, performer names, studio names, and tags. | present | `search` | query language full-text over titles/details |
| 202 | Search is powered by an **embedded search engine** — either SQLite FTS5 (small instances) or Meilisearch/Typesense sidecar (larger instances). | fit | `search` |  |
| 203 | Search supports **typo tolerance** — Levenshtein distance ≤ 2 for terms > 5 characters. | fit | `search` |  |
| 204 | Search supports **prefix matching** — results appear as the user types. | present | `search` | prefix matching in the query language |
| 205 | Search supports **faceted filtering**: narrow results by tag, performer, studio, year range, duration range, resolution, rating range, media type. | present | `search` | TagFacets + Filter |
| 206 | Search supports **boolean tag expressions**: `tag:A AND tag:B NOT tag:C`. | present | `search` | boolean tag expressions |
| 207 | Search supports **sorting**: by relevance, rating (Bayesian), rating count, date added, date released, popularity score, catalog health score. | present | `search` | Sort enum on QueryScenesResultType |
| 208 | Search results display: title, top-5 confirmed tags, average rating, rating count, duration, and a "predicted score for you" badge (from Gravity). | fit | `surface-injection` |  |
| 209 | An **advanced search** form exposes all filterable fields with dropdowns and range sliders. | present | `search` | advanced filter form |
| 210 | Search queries are **logged per-user** (opt-out available) to improve recommendation signals (search-as-intent). | present | `search` | SearchDiagnostic |
| 211 | A **"similar items"** link on every item page: shows the top-20 nearest items by item embedding distance. | present | `surface-injection` | Scene.similar (SimilarScene) |
| 212 | A **"more by this performer"** link on every item page. | present | `surface-injection` | performer page scene list |
| 213 | A **"more from this studio"** link on every item page. | present | `surface-injection` | studio page scene list |
| 214 | A **"more in this series"** link on every item page (chronologically ordered). | present | `surface-injection` | season/series grouping on studio page |
| 215 | A **random item** button: uniformly random across the entire catalog (no taste weighting — pure serendipity). | present | `search` | random scene surface |
| 216 | A **"feeling lucky"** button: random item weighted by predicted score for the current user. | fit | `search` |  |
| 217 | A **browse-by-tag** page: an alphabetical/hierarchical explorer of the tag dictionary with item counts. | present | `search` | tag browse page |
| 218 | A **browse-by-performer** page: alphabetical with item counts, sortable by popularity or catalog health. | present | `search` | performer browse page |
| 219 | A **browse-by-studio** page: same as performer. | present | `search` | studio browse page |
| 220 | A **browse-by-year** page: timeline visualization of catalog density by release year. | fit | `search` |  |
| 221 | A **trending** page: items with the most new ratings/interactions in the last 24h, 7d, 30d. | present | `search` | trending (SeasonalEvent + discovery §7) |
| 222 | A **recently added** page: newest Item Records, reverse chronological. | present | `search` | recently added sort |
| 223 | A **recently updated** page: items whose metadata was most recently edited. | present | `search` | recently updated sort |
| 224 | A **"lost and found"** page: aggregation of items marked as lost media — community members can attempt to locate them. | fit | `search` |  |
| 225 | A **"needs tags"** page: items with fewer than N tags, sorted by popularity (so the most-seen under-tagged items get attention first). | present | `search` | lint quests (needs tags) |
| 226 | A **"needs ratings"** page: items with fewer than N ratings. | fit | `search` |  |
| 227 | A **"controversial"** page: items with the highest rating standard deviation. | fit | `search` |  |
| 228 | A **"hall of fame"** page: all-time highest-rated items with a minimum rating count threshold. | present | `search` | EloLeaderboard |
| 229 | A **"worst of"** page: lowest-rated items (opt-in visibility, off by default). | fit | `search` |  |
| 230 | A **saved searches** feature: users can save complex queries and receive notifications when new items match. | present | `search` | SavedSearch, SavedSearchMatch |
| 231 | Saved searches support **RSS/Atom feed output** — users can subscribe in their feed reader. | fit | `search` |  |
| 232 | The search index is updated in **near-real-time** — new items are searchable within seconds of submission. | present | `search` | index on write |
| 233 | Search supports **multi-language stemming** — English, Spanish, French, German, Japanese, and operator-extensible. | fit | `search` |  |
| 234 | Search supports **quoted exact-match phrases**. | present | `search` | quoted phrases in the query language |
| 235 | Search supports **field-specific queries**: `performer:"name" studio:"name" title:"name"`. | present | `search` | field-scoped terms |
| 236 | An **autocomplete/suggestion** dropdown appears as the user types in the search bar, showing matching items, performers, tags, and studios. | present | `search` | autocomplete endpoint |
| 237 | The system tracks **search result click-through rates** to improve relevance ranking over time. | fit | `search` |  |
| 238 | A **"did you mean?"** suggestion for queries that return zero results but are close to known terms. | present | `search` | SearchDiagnostic did-you-mean |
| 239 | Search results support **infinite scroll** or **paginated** mode (user preference). | present | `search` | pagination + scroll |
| 240 | The search API is fully accessible via the public REST API — enabling third-party clients and integrations. | present | `search` | GraphQL query surface |

### G · User Lists & Collections (241–270) — 20 fit / 10 present / 0 conflict

| # | feature | verdict | extension | evidence / reason |
|---:|---|---|---|---|
| 241 | Users can create **custom lists** — ordered collections of items with a title and optional description. | present | `lists` | List, ListItem |
| 242 | Lists have a **visibility** toggle: public, unlisted (accessible via link), or private. | present | `lists` | List visibility |
| 243 | Lists support **ranked ordering** (drag-and-drop reorder) or **unordered set** mode. | present | `lists` | List ordered flag |
| 244 | Users can create unlimited lists. | present | `lists` | unlimited lists per user |
| 245 | Lists have a **comment thread** (if public) — other users can discuss the list. | fit | `lists` |  |
| 246 | Users can **fork** a public list — creating their own copy to modify. | fit | `lists` |  |
| 247 | Users can **follow** a public list — receiving notifications when items are added. | fit | `lists` |  |
| 248 | Lists support **collaborative editing** — the list owner can invite other users as co-editors. | fit | `lists` |  |
| 249 | Lists display aggregate statistics: total items, average rating, tag distribution. | fit | `lists` |  |
| 250 | Lists can be **exported** as JSON, CSV, or plain-text. | fit | `lists` |  |
| 251 | Lists can be **imported** from JSON or CSV (matching by title, hash, or URL). | fit | `lists` |  |
| 252 | A **"smart list"** feature: auto-populated lists based on a saved search query — the list updates dynamically as new matching items are cataloged. | present | `lists` | SavedSearch as smart list |
| 253 | Smart lists support all the same filters as advanced search. | present | `lists` | SavedSearch inherits the query language |
| 254 | Users have a built-in **"Favorites"** list (a special system list backed by the favorite flag). | present | `lists` | favourites as a system list |
| 255 | Users have a built-in **"Want to See"** list (a special system list backed by the wishlist flag). | fit | `lists` |  |
| 256 | Users have a built-in **"Seen"** list (a special system list backed by the seen flag). | present | `lists` | seen as a system list |
| 257 | A **"top lists"** discovery page: community-curated lists sorted by follower count. | fit | `lists` |  |
| 258 | Lists can be **tagged** with descriptive tags for discoverability. | fit | `lists` |  |
| 259 | List search: full-text search across list titles, descriptions, and tags. | fit | `lists` |  |
| 260 | Users can **upvote** lists to signal quality — upvotes contribute to list ranking on the top lists page. | fit | `lists` |  |
| 261 | Lists support a **cover image URL** (external link) for visual presentation. | present | `lists` | List cover image |
| 262 | A **"list comparison"** tool: given two lists, show intersection, unique-to-A, unique-to-B. | fit | `lists` |  |
| 263 | A **"list merge"** tool: combine two lists with deduplication. | fit | `lists` |  |
| 264 | Lists support **sections/headers** — dividers within the list for organization (e.g., "Tier 1", "Tier 2"). | fit | `lists` |  |
| 265 | Lists carry a **last-updated timestamp** and an **update count** displayed publicly. | present | `lists` | List updated timestamps |
| 266 | Users can **embed** a public list on external sites via an iframe or oEmbed endpoint. | fit | `lists` |  |
| 267 | A **"challenge list"** subtype: a curated list intended as a viewing challenge (e.g., "The 100 Essential [Genre] Works") with per-user progress… | fit | `lists` |  |
| 268 | Challenge lists show a **progress bar** on the user's profile. | fit | `lists` |  |
| 269 | Lists support **item-level notes** — the list creator can annotate each item within the list context. | fit | `lists` |  |
| 270 | The system enforces a **maximum list size** (default: 10,000 items) to prevent abuse. | fit | `lists` |  |

### H · Community & Social (271–310) — 28 fit / 12 present / 0 conflict

| # | feature | verdict | extension | evidence / reason |
|---:|---|---|---|---|
| 271 | A **global feed** showing recent activity: new items, new ratings (if user is public), new lists, new reviews. | present | `social` | ActivityItem feed |
| 272 | The global feed is filterable by activity type and by tag. | fit | `social` |  |
| 273 | Users can **follow** other users — their public activity appears in a **personal feed**. | present | `social` | Follow |
| 274 | Follow relationships are **asymmetric** (Twitter-style, not mutual-required). | present | `social` | asymmetric Follow |
| 275 | The personal feed is reverse-chronological with optional algorithmic reordering (boosting activity from most-compatible users). | fit | `social` |  |
| 276 | Users can **comment** on items — threaded discussion with Markdown-lite formatting. | present | `social` | Comment on scenes |
| 277 | Comments can be upvoted/downvoted; comments are sorted by score (Wilson lower bound) or chronologically. | fit | `social` |  |
| 278 | Comments have a **maximum depth** of 5 levels to prevent deep nesting on small screens. | fit | `social` |  |
| 279 | Users can **mention** other users in comments with @username — the mentioned user receives a notification. | fit | `social` |  |
| 280 | A **notifications** system: in-app notifications for mentions, list follows, review endorsements, flag resolutions, recommendation digests. | present | `social` | Notification |
| 281 | Notifications are accessible via a bell icon with unread count badge. | fit | `social` |  |
| 282 | Notifications are paginated and can be marked read individually or all-at-once. | present | `social` | notification pagination + read state |
| 283 | Notifications can be configured per-type: on/off, in-app only, or in-app + email. | fit | `social` |  |
| 284 | A **direct messaging** system between users — encrypted at rest, with block/report support. | fit | `social` |  |
| 285 | DMs can be disabled per-user in settings. | fit | `social` |  |
| 286 | A **forums** subsystem: topic-based discussion boards organized by category (meta, recommendations, help, off-topic). | fit | `forums` |  |
| 287 | Forum posts support Markdown, @mentions, and item/list/performer links via shortcodes. | fit | `forums` |  |
| 288 | Forum moderation uses the same role system as the rest of the platform. | fit | `forums` |  |
| 289 | A **"who else liked this?"** feature on items: shows avatars/links to public users who rated the item highly (≥ 8/10). | present | `social` | reviews and favourites are public by default |
| 290 | A **leaderboard** page: top curators by reputation score, most items cataloged, most tags confirmed. | present | `social` | LeaderboardEntry |
| 291 | Leaderboards are opt-in — users can hide themselves from leaderboards in settings. | fit | `social` |  |
| 292 | A **"taste twin"** feature: each user is shown their single most taste-compatible public user, with a compatibility percentage. | fit | `social` |  |
| 293 | Users can form **groups** — named communities with a shared feed, shared lists, and a group chat. | fit | `forums` |  |
| 294 | Groups have roles: owner, moderator, member. | fit | `forums` |  |
| 295 | Groups can be public (discoverable, open join), approval-required, or invite-only. | fit | `forums` |  |
| 296 | A **group recommendation** feature: items recommended for the group based on aggregate member tastes (see feature 177). | fit | `forums` |  |
| 297 | A **"this or that"** game: the system presents two items and the user picks which they prefer — the pairwise data enriches taste profiling via… | present | `games` | EloMatchup (pairwise) |
| 298 | A **"rate roulette"** game: the system presents an un-rated item (from items the user has marked "seen" but not rated) and asks for a quick rating. | fit | `games` |  |
| 299 | A **"tag sprint"** game: the system presents an under-tagged item and challenges the user to add as many applicable tags as possible in 60 seconds. | fit | `games` |  |
| 300 | Games contribute to reputation score and a **gamification badges** system. | fit | `games` |  |
| 301 | Badges are awarded for milestones: 10/50/100/500/1000 items rated, first list created, first review, 100 tags confirmed, etc. | fit | `games` |  |
| 302 | Badges are displayed on the user's profile. | fit | `games` |  |
| 303 | A **"match percentage"** is displayed between the current user and any other public user on their profile page. | fit | `social` |  |
| 304 | Users can **report** any content (item, comment, review, DM, list) for policy violation. | present | `social` | ContentSourceAppeal, report flow |
| 305 | Reports go to the **moderation queue** (visible to moderators and operator). | present | `social` | moderation queue |
| 306 | The moderation queue is the **only operator-facing review surface** — keeping the "minimal operator overhead" promise. | present | `social` | queue as the only review surface |
| 307 | Automated **spam detection** on submissions and comments using basic heuristics: link density, repetition, new-account burst. | fit | `social` |  |
| 308 | Suspected spam is auto-held for moderator review. | fit | `social` |  |
| 309 | A **community announcements** channel: operator can post announcements that appear as a banner or pinned feed item. | fit | `social` |  |
| 310 | A **changelog** page: operator or system posts about new features, maintenance windows, policy changes. | fit | `social` |  |

### I · Anti-Abuse, Policy & Safety (311–340) — 14 fit / 15 present / 1 conflict

| # | feature | verdict | extension | evidence / reason |
|---:|---|---|---|---|
| 311 | The platform enforces a **terms of service** that the operator can customize — users must accept on registration. | fit | `safety` |  |
| 312 | Content policy is **operator-defined** — the system provides a template but does not impose its own content restrictions beyond legal minimums. | present | `safety` | docs/policy/abuse-and-takedown.md |
| 313 | The system supports **mandatory content-warning tags**: operator can define a set of tags that, if applied, trigger a click-through interstitial… | present | `safety` | ContentWarning interstitial |
| 314 | Users can set **global content filters** based on tags — items matching filtered tags are hidden from all views (search, browse, recommendations,… | present | `safety` | Filter (global) |
| 315 | Content filters are applied at the **query level** for performance (filtered items never leave the database). | present | `safety` | filter applied in the query layer |
| 316 | The system supports an **instance-level tag blocklist**: the operator can ban specific tags entirely — items cannot be tagged with them and search… | present | `safety` | instance tag blocklist (Tag block edit) |
| 317 | A **flood control** system: maximum N item submissions per user per hour (default 20, configurable). | fit | `safety` |  |
| 318 | A **sockpuppet detection** heuristic: flags accounts with suspiciously similar behavior patterns (same IP, same rating patterns, created in burst). | fit | `safety` |  |
| 319 | Moderators can **mute** a user for a configurable duration — the user can browse but cannot submit, edit, tag, rate, or comment. | fit | `safety` |  |
| 320 | Moderators can **ban** a user — full access revocation, IP optionally blocked. | present | `safety` | user ban |
| 321 | Ban appeals are handled via a dedicated **appeal form** that emails the operator directly. | present | `safety` | ContentSourceAppeal |
| 322 | The system keeps an **audit log** of all moderator actions: who, when, what, reason. | present | `safety` | ModAudit |
| 323 | The audit log is visible to the operator and optionally to all moderators for accountability. | present | `safety` | ModAudit visible to moderators |
| 324 | The system supports **shadow-limiting**: a misbehaving user's contributions remain visible only to themselves — invisible to everyone else. | conflict | `governance-override` | Shadow-limiting makes a reader see contributions nobody else can: it contradicts the premise that every claim goes through one visible write path. Shippable only as an audited moderator… |
| 325 | The system supports **CAPTCHA** on registration (hCaptcha or turnstile, operator-configurable). | fit | `safety` |  |
| 326 | The system supports **invite-only registration** mode: new users can only join via an invite code from an existing member. | present | `safety` | InviteKey |
| 327 | Invite codes can be limited-use (single-use or N-use) and expire after a configurable duration. | present | `safety` | InviteAllowance |
| 328 | The system supports **account age gates** on certain actions: e.g., only accounts > 7 days old can vote on tag proposals. | fit | `safety` |  |
| 329 | The system supports **contribution rate limits** that scale with reputation: higher-rep users get higher limits. | fit | `safety` |  |
| 330 | IP-based rate limiting at the **reverse proxy level** (documentation provided for Nginx/Caddy configuration). | fit | `safety` |  |
| 331 | The system supports **automated nightly integrity checks**: orphaned records, broken merge chains, tag dictionary inconsistencies. | fit | `safety` |  |
| 332 | Integrity check results are logged and optionally emailed to the operator. | fit | `safety` |  |
| 333 | The system supports **read-only mode**: operator can flip a switch to make the instance non-writable during maintenance. | fit | `safety` |  |
| 334 | The system supports **allowlisted registration domains** for email-required instances (e.g., only `@university.edu` users can register). | fit | `safety` |  |
| 335 | The system logs all **authentication failures** and supports fail2ban-compatible log formatting. | fit | `safety` |  |
| 336 | All user-facing forms have **CSRF protection** via double-submit cookies. | present | `safety` | CSRF middleware |
| 337 | The system sets **security headers**: HSTS, X-Content-Type-Options, X-Frame-Options, CSP. | present | `safety` | security headers middleware |
| 338 | All cookies are **HttpOnly, Secure, SameSite=Lax**. | present | `safety` | session cookie flags |
| 339 | The system supports **content hash deduplication on submit**: if a new item's perceptual hash matches an existing item within threshold, the… | present | `safety` | DraftFingerprint duplicate check on submit |
| 340 | Duplicate detection results are cached to avoid redundant hash comparisons. | fit | `safety` |  |

### J · API & Integration (341–370) — 13 fit / 14 present / 3 conflict

| # | feature | verdict | extension | evidence / reason |
|---:|---|---|---|---|
| 341 | A **RESTful JSON API** covering all platform functionality: items, tags, performers, studios, series, users, lists, ratings, recommendations, search. | conflict | `rest-facade` | The fork's contract is GraphQL; a full REST surface is a second contract to keep in sync. Useful as an optional facade extension, wrong as core. |
| 342 | API versioning via **URL prefix** (`/api/v1/`). | fit | `rest-facade` |  |
| 343 | API authentication via **Bearer token** (personal API tokens from user settings). | present | `plugin-host` | API key bearer auth |
| 344 | Unauthenticated API access is allowed for read-only public data (configurable by operator). | present | `plugin-host` | public read queries |
| 345 | API responses follow a consistent **envelope format**: `{ "data": ..., "meta": { "page", "total", "limit" }, "errors": [...] }`. | conflict | `rest-facade` | A `{data,meta,errors}` envelope cannot sit over a GraphQL schema without inventing a second API. |
| 346 | API supports **pagination** via `?page=N&limit=N` (max limit 100, default 25). | present | `plugin-host` | first/after pagination |
| 347 | API supports **sparse fieldsets**: `?fields=title,rating,tags` to reduce payload size. | fit | `rest-facade` |  |
| 348 | API supports **relationship inclusion**: `?include=performers,studio,series` to reduce round-trips. | fit | `plugin-host` |  |
| 349 | API supports **bulk operations**: POST/PATCH/DELETE on arrays of items/ratings/tags in a single request (max 100 per batch). | present | `plugin-host` | bulk edit submission |
| 350 | API supports **webhook registration**: users/operators can register webhooks for events (new item, new rating on specific item, new match for saved… | fit | `plugin-host` |  |
| 351 | Webhooks fire HTTP POST requests with a signed payload (HMAC-SHA256). | fit | `plugin-host` |  |
| 352 | A **WebSocket** endpoint for real-time updates: live feed, notification push, live search-as-you-type. | fit | `plugin-host` |  |
| 353 | An **RSS/Atom feed** for: new items (global or per-tag), user activity (if public), list updates, saved search matches. | fit | `plugin-host` |  |
| 354 | An **OPML export** of all a user's RSS subscriptions on the platform. | fit | `plugin-host` |  |
| 355 | API rate limiting: default 300 requests/minute per token, configurable by operator. | fit | `plugin-host` |  |
| 356 | Rate limit headers are returned in every response: `X-RateLimit-Limit`, `X-RateLimit-Remaining`, `X-RateLimit-Reset`. | fit | `plugin-host` |  |
| 357 | The API exposes the **recommendation engine** endpoints: `GET /api/v1/recommendations` (personal), `POST /api/v1/recommendations/group` (group). | present | `plugin-host` | scene_for_you query |
| 358 | The API exposes a **taste profile** endpoint: `GET /api/v1/users/me/taste` returning tag affinities, embedding vector, and cluster memberships. | present | `plugin-host` | taste profile query |
| 359 | The API exposes a **similar items** endpoint: `GET /api/v1/items/:id/similar`. | present | `plugin-host` | similar scenes query |
| 360 | The API exposes a **duplicate check** endpoint: `POST /api/v1/items/duplicate-check` accepting a hash or title and returning potential matches. | present | `plugin-host` | QueryExistingScene |
| 361 | **OpenAPI 3.1 specification** is auto-generated and served at `/api/docs`. | fit | `plugin-host` |  |
| 362 | A **Swagger UI** instance is served at `/api/playground` for interactive API exploration. | present | `plugin-host` | GraphQL playground |
| 363 | The API supports **JSON:API** format as an alternative content type (via `Accept` header). | conflict | `rest-facade` | JSON:API is a second serialisation of every type; the same drift risk as the REST facade. |
| 364 | The system supports **import from external catalogs** via configurable adapters — operator can write a plugin to import from another database. | present | `plugin-host` | ContentSource + IngestSubmission |
| 365 | The system supports **export of the entire catalog** as a compressed JSON dump (operator only). | fit | `plugin-host` |  |
| 366 | The system supports **ActivityPub** (optional module) for federation between Rediscovery instances — items, ratings, and lists can flow across… | present | `social` | FederationPeer |
| 367 | Federation supports **selective sync**: instance operators can choose which tags/categories to federate. | present | `social` | federation selective sync |
| 368 | The API supports **conditional requests** via ETag/If-None-Match for efficient polling. | fit | `plugin-host` |  |
| 369 | The API supports **CORS** with configurable allowed origins. | present | `plugin-host` | configurable CORS |
| 370 | All API error responses include a machine-readable **error code** and a human-readable **message**. | present | `plugin-host` | GraphQL error extensions |

### K · Architecture & Hardware (371–420) — 23 fit / 19 present / 8 conflict

| # | feature | verdict | extension | evidence / reason |
|---:|---|---|---|---|
| 371 | The entire application is packaged as a **single statically-linked binary** (backend) + a **static frontend bundle** — no runtime dependencies except… | present | `ops` | single Go binary + static frontend |
| 372 | The backend is written in a **memory-safe, compiled language** (Rust or Go) for minimal resource consumption. | present | `ops` | Go |
| 373 | The default database is **SQLite** (WAL mode) — zero-configuration, single-file, suitable for instances up to ~500K items and ~50K users. | conflict | `sqlite-backend` | SQLite as the default backend is a port, not a flag: migrations, LISTEN/NOTIFY, JSONB and the fingerprint indexes assume PostgreSQL. |
| 374 | An optional **PostgreSQL** backend is supported for larger instances — switchable via a single config flag. | conflict | `sqlite-backend` | PostgreSQL is not optional here — it is the backend. "Optional Postgres" inverts the fork's stack. |
| 375 | The application requires **≤ 256 MB RAM** at idle with a warm cache for a 100K-item catalog on SQLite. | fit | `ops` |  |
| 376 | The application requires **≤ 1 GB RAM** for a 500K-item catalog with the recommendation engine's in-memory structures. | fit | `ops` |  |
| 377 | The application runs on a **single $5/month VPS** (1 vCPU, 1 GB RAM, 25 GB SSD) for small communities (≤ 5K users, ≤ 100K items). | fit | `ops` |  |
| 378 | The application runs on a **Raspberry Pi 4** (4 GB RAM) for personal/home-server use. | fit | `ops` |  |
| 379 | The **frontend** is a server-rendered HTML application (with progressive enhancement via minimal JS) — not a heavy SPA. | conflict | `ssr-frontend` | The fork ships a React client. A server-rendered no-JS frontend is a second frontend, not a setting. |
| 380 | Frontend pages are **< 100 KB** total transfer (HTML + CSS + JS), excluding user-uploaded avatars. | conflict | `ssr-frontend` | A <100 KB page budget cannot be honoured by the existing React bundle without replacing it. |
| 381 | Frontend works **without JavaScript** for core flows (browse, search, read) — JS enhances interactions (live search, drag-and-drop, voting) but is… | conflict | `ssr-frontend` | Core flows without JavaScript duplicate every screen the SPA already renders. |
| 382 | Frontend is **responsive** — mobile-first layout that works on all screen sizes. | present | `ops` | responsive layout |
| 383 | The system uses **no external CDN** — all assets are self-hosted. | present | `ops` | self-hosted assets |
| 384 | The system uses **no third-party analytics or tracking** — zero external requests from the frontend. | present | `ops` | no analytics |
| 385 | The system uses **no cookies except functional ones** (session, CSRF) — no tracking cookies. | present | `ops` | functional cookies only |
| 386 | The backend serves both the **frontend** and the **API** — single process, single port. | present | `ops` | single process serves API and UI |
| 387 | An optional **background worker** process handles: recommendation engine retraining, search index updates, integrity checks, digest generation. | fit | `ops` |  |
| 388 | The background worker can run in-process (default, for single-server setups) or as a separate process connected via a lightweight job queue (Redis or… | fit | `ops` |  |
| 389 | For the embedded queue, the system uses **SQLite as the job queue** — no Redis dependency for small instances. | conflict | `sqlite-backend` | SQLite-as-job-queue presumes the SQLite backend this fork does not have. |
| 390 | The system supports **horizontal read scaling** via read replicas (PostgreSQL) or Litestream-replicated SQLite. | fit | `ops` |  |
| 391 | The system's **data directory** contains: the database file, the search index, the recommendation model artifacts, and the configuration file — all… | conflict | `sqlite-backend` | The data directory is Postgres + object storage; there is no single-directory model. |
| 392 | Backup is as simple as **copying the data directory** (or using `sqlite3 .backup` for live backup). | fit | `ops` |  |
| 393 | The system supports **automated backups** to local disk or S3-compatible storage on a cron schedule. | fit | `ops` |  |
| 394 | Database migrations are **embedded in the binary** and auto-applied on startup — no manual migration step. | present | `ops` | migrations in-repo, applied at startup |
| 395 | The system logs to **stdout/stderr** in structured JSON format — compatible with any log aggregator. | present | `ops` | structured logging |
| 396 | Log verbosity is configurable: `error`, `warn`, `info`, `debug`, `trace`. | present | `ops` | log level config |
| 397 | The system exposes a **health check** endpoint at `/health` — returns 200 OK with uptime and basic stats. | present | `ops` | health endpoint |
| 398 | The system exposes a **metrics** endpoint at `/metrics` in Prometheus-compatible format (optional, off by default). | fit | `ops` |  |
| 399 | Metrics include: request count, latency percentiles, active users, items count, recommendation cache hit rate, background job queue depth. | fit | `ops` |  |
| 400 | Deployment is documented for: **Docker**, **Docker Compose**, **systemd**, **bare binary**, and **NixOS module**. | present | `ops` | docs/deploy |
| 401 | A `docker-compose.yml` one-liner brings up the full stack: app + (optional) PostgreSQL + (optional) Meilisearch. | present | `ops` | docker/ compose |
| 402 | The Docker image is **< 50 MB** (distroless base). | fit | `ops` |  |
| 403 | The system supports **configuration via environment variables**, a TOML config file, or CLI flags (in that priority order). | present | `ops` | stash-box-config.yml + env |
| 404 | All configuration options have **sane defaults** — the system runs with zero configuration for a personal instance. | present | `ops` | zero-config defaults |
| 405 | The system supports **TLS termination** natively (via built-in ACME/Let's Encrypt support) or via reverse proxy (documented for Nginx, Caddy,… | fit | `ops` |  |
| 406 | The system supports **Unix socket** listening for reverse-proxy setups. | fit | `ops` |  |
| 407 | The system supports **graceful shutdown** — in-flight requests are completed, background jobs are checkpointed. | fit | `ops` |  |
| 408 | The system supports **live reloading of configuration** (SIGHUP) without restart for most settings. | fit | `ops` |  |
| 409 | The system's **startup time** is < 3 seconds on commodity hardware. | fit | `ops` |  |
| 410 | The system runs on **Linux (x86_64, ARM64), macOS (x86_64, ARM64), and FreeBSD** — pre-built binaries for all. | present | `ops` | Linux binaries |
| 411 | The system supports **Windows** via WSL2 or Docker. | fit | `ops` |  |
| 412 | The build system produces **reproducible builds** — same source → same binary hash. | fit | `ops` |  |
| 413 | The system includes a **built-in benchmark suite** that the operator can run to verify hardware adequacy. | fit | `ops` |  |
| 414 | The benchmark suite tests: search latency, recommendation computation time, concurrent user simulation. | fit | `ops` |  |
| 415 | The system supports **connection pooling** for PostgreSQL (via built-in pool, default 10 connections). | present | `ops` | pgx pool |
| 416 | SQLite mode uses **WAL journal** with `busy_timeout` and `PRAGMA synchronous=NORMAL` for safe concurrent reads with low-latency writes. | conflict | `sqlite-backend` | WAL pragmas are SQLite-only; the Postgres equivalent is already tuned. |
| 417 | The system uses **prepared statements** exclusively — no string-interpolated SQL. | present | `ops` | sqlc prepared statements |
| 418 | Database schema uses **covering indexes** on hot query paths (tag lookups, rating aggregations, search). | present | `ops` | indexes on hot paths |
| 419 | The system supports **online vacuuming** and `ANALYZE` on a schedule to keep SQLite performance stable. | fit | `ops` |  |
| 420 | Memory usage is bounded via **configurable cache sizes** for the database page cache, search index cache, and recommendation structures. | fit | `ops` |  |

### L · Operator Administration (421–450) — 13 fit / 15 present / 2 conflict

| # | feature | verdict | extension | evidence / reason |
|---:|---|---|---|---|
| 421 | An **admin dashboard** accessible at `/admin` — web-based, no separate tool required. | present | `admin` | admin UI |
| 422 | Dashboard shows: total users, total items, total ratings, active users (24h/7d/30d), database size, recommendation engine status, job queue depth. | present | `admin` | InstanceMetric |
| 423 | Dashboard shows **growth charts**: new users, new items, new ratings over time. | fit | `admin` |  |
| 424 | Dashboard shows **moderation queue depth** and average resolution time. | present | `admin` | mod queue depth metric |
| 425 | Operator can **configure all instance settings** from the dashboard — no config file editing required for runtime-changeable settings. | present | `admin` | config surface |
| 426 | Operator can **manage users**: search, view profiles, change roles, mute, ban, delete. | present | `admin` | user admin |
| 427 | Operator can **impersonate** a user for debugging (with audit logging). | conflict | `governance-override` | Impersonation turns accountability inside out: actions taken as another user are attributed to them. The premise wants provenance on every action. Audited support tool at most, never a… |
| 428 | Operator can **broadcast announcements** from the dashboard. | fit | `admin` |  |
| 429 | Operator can **trigger recommendation engine retraining** manually from the dashboard. | fit | `admin` |  |
| 430 | Operator can **view recommendation engine metrics**: training time, model size, cache hit rate, hit/satisfaction rates. | fit | `admin` |  |
| 431 | Operator can **view and manage the tag dictionary**: rename tags, merge tags, delete tags, manage synonyms/implications/hierarchy. | present | `admin` | tag dictionary admin |
| 432 | Operator can **configure the rating scale** (1–5, 1–10, 1–100, thumbs) — changing scale offers a migration tool to rescale existing ratings. | fit | `admin` |  |
| 433 | Operator can **configure registration mode**: open, invite-only, approval-required, or closed. | present | `admin` | registration mode (invite key) |
| 434 | Operator can **configure the instance name, description, logo URL, and color theme**. | present | `admin` | instance branding in config |
| 435 | Operator can **configure content policy text** displayed on registration and in the footer. | present | `admin` | policy text in docs/policy |
| 436 | Operator can **configure flood control thresholds** per role. | fit | `admin` |  |
| 437 | Operator can **configure recommendation engine parameters**: retrain schedule, embedding dimensions, diversity/novelty/serendipity weights, decay… | fit | `admin` |  |
| 438 | Operator can **view the audit log** from the dashboard. | present | `admin` | ModAudit view |
| 439 | Operator can **export/import the full database** from the dashboard for migration or backup. | fit | `admin` |  |
| 440 | Operator can **schedule maintenance windows** — the system shows a banner to users and optionally enters read-only mode. | fit | `admin` |  |
| 441 | Operator can set **instance-level default content filters** — applied to all users unless individually overridden. | present | `admin` | instance default filters |
| 442 | Operator can **pin items** to the homepage or discovery page for editorial promotion (the one exception to "no editorial staff" — operator can… | conflict | `editorial` | Editorial pinning outranks every user signal, which is the operator overview the owner asked to minimise. Legitimate as an off-by-default extension, wrong as core. |
| 443 | Operator can **define custom badge criteria** beyond the defaults. | fit | `admin` |  |
| 444 | Operator can **manage federation peers** (if ActivityPub module is enabled). | present | `admin` | FederationPeer admin |
| 445 | Operator can **view per-user statistics**: items submitted, tags applied, ratings given, reports filed, flags resolved. | present | `admin` | UserEditCount, UserVoteCount |
| 446 | Operator can **configure email settings**: SMTP server for notifications and recovery emails. | present | `admin` | SMTP config |
| 447 | Operator can **configure external authentication**: OAuth2/OIDC (e.g., login with a self-hosted Authentik/Keycloak). | fit | `admin` |  |
| 448 | Operator can **view slow query logs** from the dashboard for performance tuning. | fit | `admin` |  |
| 449 | The admin dashboard itself is **protected by role check and optionally by a separate admin password** for defense-in-depth. | present | `admin` | role check on admin |
| 450 | All admin actions are **idempotent** and confirmable — destructive actions require a confirmation dialog with the action spelled out. | fit | `admin` |  |

### M · Data Integrity, Portability & Privacy (451–480) — 16 fit / 12 present / 2 conflict

| # | feature | verdict | extension | evidence / reason |
|---:|---|---|---|---|
| 451 | All data is stored **locally** — no cloud dependencies, no SaaS calls, no phoning home. | present | `integrity` | local data only |
| 452 | The system **never contacts any external server** unless explicitly configured (e.g., federation, SMTP, ACME). | present | `integrity` | no outbound calls by default |
| 453 | A **full data export** in machine-readable format (JSON lines or CSV) is available to the operator. | fit | `integrity` |  |
| 454 | The export includes: all items, tags, performers, studios, series, users (anonymizable), ratings, lists, reviews, comments, audit logs. | fit | `integrity` |  |
| 455 | A **full data import** from the same format is supported — enabling instance migration. | fit | `integrity` |  |
| 456 | The system supports **merging two instance databases** with conflict resolution (operator-guided). | fit | `integrity` |  |
| 457 | User data export (feature 84) includes: all ratings, lists, reviews, tags applied, consumption log, taste profile, and settings. | fit | `integrity` |  |
| 458 | User data is stored in a **normalized relational schema** — no embedded documents or opaque blobs. | present | `integrity` | normalised relational schema |
| 459 | The schema is **fully documented** in a schema reference guide within the repo. | fit | `integrity` |  |
| 460 | Foreign key constraints are enforced at the **database level** for referential integrity. | present | `integrity` | FK constraints in migrations |
| 461 | The system uses **database transactions** for all multi-step operations — no partial writes on failure. | present | `integrity` | transactions in the write path |
| 462 | The system supports **point-in-time recovery** via WAL archiving (SQLite) or PostgreSQL's native PITR. | conflict | `sqlite-backend` | PITR via WAL archiving is a Postgres deployment concern, not an application feature. |
| 463 | Perceptual hashes are indexed for **sub-linear duplicate search** using a BK-tree or VP-tree structure. | present | `integrity` | FingerprintCluster (BK-tree style) |
| 464 | The system stores **no IP addresses** in the application database — IP-based rate limiting is handled at the reverse proxy or in ephemeral memory. | fit | `integrity` |  |
| 465 | The system stores **no tracking data** beyond what is functionally necessary (ratings, tags, interactions). | present | `integrity` | no tracking tables |
| 466 | The operator can configure a **data retention policy**: auto-delete consumption logs older than N days, auto-anonymize inactive accounts after N days. | fit | `integrity` |  |
| 467 | The system supports **right-to-erasure** requests: operator can trigger a full data scrub for a specific user. | fit | `integrity` |  |
| 468 | Passwords are **never logged** — not even in debug mode. | present | `integrity` | passwords never logged |
| 469 | API tokens are stored as **salted hashes** — the raw token is shown once on creation and never again. | present | `integrity` | API keys stored hashed |
| 470 | Database backups can be **encrypted at rest** using age or GPG (operator-configured). | fit | `integrity` |  |
| 471 | The system performs **schema validation** on startup — if the database is from an incompatible version, a clear error is shown with migration… | present | `integrity` | schema version check at startup |
| 472 | The system writes a **version stamp** to the database on each migration for traceability. | present | `integrity` | Version table |
| 473 | The system supports **logical replication** between instances for read scaling or geo-distribution (PostgreSQL mode). | fit | `integrity` |  |
| 474 | The system supports **streaming backups** via Litestream (SQLite mode) to S3-compatible storage. | conflict | `sqlite-backend` | Litestream replicates SQLite; this fork runs Postgres. |
| 475 | All exports include a **schema version header** so importers can detect format mismatches. | fit | `integrity` |  |
| 476 | The system checksums export files with **SHA-256** for integrity verification. | fit | `integrity` |  |
| 477 | The system supports **selective export**: export only items matching a tag filter, or only a specific user's data. | fit | `integrity` |  |
| 478 | The system's database file can be **directly queried** with standard SQLite or PostgreSQL tools for ad-hoc analysis. | present | `integrity` | plain Postgres, queryable |
| 479 | The system includes a **built-in SQL console** (operator-only) accessible via the admin dashboard for read-only queries. | fit | `integrity` |  |
| 480 | The system ensures **backward compatibility** for at least 2 major versions — old databases can be migrated forward. | fit | `integrity` |  |

### N · Frontend & User Experience (481–520) — 26 fit / 13 present / 1 conflict

| # | feature | verdict | extension | evidence / reason |
|---:|---|---|---|---|
| 481 | The UI follows a **content-density-first** design — maximum information per viewport, minimal whitespace, no hero images or decorative fluff. | fit | `ux-density` |  |
| 482 | The default theme is **dark mode** with a muted color palette — easy on the eyes for extended browsing. | present | `ux-density` | dark default theme |
| 483 | A **light mode** theme is available, switchable in user settings. | present | `ux-density` | light theme |
| 484 | Operator can configure **custom CSS** for instance branding. | fit | `ux-density` |  |
| 485 | The UI uses **semantic HTML5** — fully accessible to screen readers and keyboard navigation. | fit | `ux-density` |  |
| 486 | All interactive elements have **visible focus indicators** — WCAG 2.1 AA compliant. | fit | `ux-density` |  |
| 487 | All images (avatars, external thumbnails if linked) have **alt text** fields. | fit | `ux-density` |  |
| 488 | The UI supports **keyboard shortcuts** for power users: `s` for search, `r` for random, `/` for focus search bar, `j/k` for navigation, `1-0` for… | fit | `ux-density` |  |
| 489 | Keyboard shortcuts are documented in a **cheat sheet** modal (triggered by `?`). | fit | `ux-density` |  |
| 490 | The UI is fully functional at **320px viewport width** (small phones). | fit | `ux-density` |  |
| 491 | The UI supports **infinite scroll** or **pagination** — user's choice, stored in preferences. | present | `ux-density` | pagination/scroll preference |
| 492 | Item pages have a **sticky action bar** at the top: rate, favorite, mark seen, add to list, flag. | fit | `ux-density` |  |
| 493 | Tag pills on item pages are **clickable** — clicking a tag navigates to a search for that tag. | present | `ux-density` | clickable tag pills |
| 494 | The UI supports **drag-and-drop** for reordering list items. | present | `ux-density` | list drag-and-drop |
| 495 | The UI supports **batch selection** in search results and lists for bulk operations (rate, tag, add to list). | present | `ux-density` | bulk selection in the scene grid |
| 496 | The UI has a **toast notification system** for non-blocking feedback (e.g., "Rating saved", "Item added to list"). | present | `ux-density` | toast feedback |
| 497 | The UI has a **command palette** (Ctrl+K / Cmd+K) for quick navigation to any page, item, or action. | fit | `ux-density` |  |
| 498 | The recommendation page is a **dedicated full-page experience** showing Gravity's picks with explanations. | present | `surface-injection` | For You page |
| 499 | The taste map (feature 168) uses **Canvas or SVG** rendering — no heavy WebGL dependency. | fit | `ux-density` |  |
| 500 | Forms use **client-side validation** with clear error messages before submission. | present | `ux-density` | form validation |
| 501 | All data tables support **column sorting** by clicking headers. | fit | `ux-density` |  |
| 502 | All data tables support **column filtering** via a filter row. | fit | `ux-density` |  |
| 503 | The UI shows **relative timestamps** ("3 hours ago") with absolute timestamps on hover. | fit | `ux-density` |  |
| 504 | The UI supports **localization** (i18n) — English is default, community-contributed translations are loaded from JSON files. | fit | `ux-density` |  |
| 505 | The UI renders **Markdown-lite** in descriptions, reviews, and comments: bold, italic, links, lists, code, blockquotes — no images or HTML. | fit | `ux-density` |  |
| 506 | The UI has a **"compact mode"** toggle: reduces line-height, font-size, and padding for users who want maximum density. | fit | `ux-density` |  |
| 507 | The UI has a **"reader mode"** for review/comment threads: single-column, larger font, no sidebar. | fit | `ux-density` |  |
| 508 | Page transitions are **instant** (< 100ms perceived) via `<link rel="prefetch">` on hover for navigation links. | fit | `ux-density` |  |
| 509 | The login page supports **"remember me"** — extends session duration to 90 days. | present | `ux-density` | remember-me session |
| 510 | The registration page includes a **password strength meter**. | fit | `ux-density` |  |
| 511 | The UI shows a **global search bar** prominently in the header on every page. | present | `ux-density` | global search bar |
| 512 | The UI has an **"onboarding wizard"** for first-time users: explains key concepts, walks through taste seeding, privacy settings. | fit | `ux-density` |  |
| 513 | The UI has a **"what's new" modal** shown once after each update, summarizing new features. | fit | `ux-density` |  |
| 514 | The UI supports **right-to-left (RTL) text** for Arabic, Hebrew, etc. | fit | `ux-density` |  |
| 515 | The UI avoids **layout shift** — all dynamically loaded content has pre-allocated dimensions. | fit | `ux-density` |  |
| 516 | The UI loads **above-the-fold content first** via server-side rendering — no blank-page spinner. | conflict | `ssr-frontend` | SSR above-the-fold is a frontend architecture, not a feature of the current client. |
| 517 | Item pages show a **"metadata completeness" indicator** — a progress bar showing how many recommended fields are filled. | present | `ux-density` | Completion / CompletenessClaim |
| 518 | Error pages (404, 500) are **branded and helpful** — include search bar and navigation links. | present | `ux-density` | error pages |
| 519 | The UI supports **custom user CSS** — users can inject their own stylesheet via settings (scoped to their session). | fit | `ux-density` |  |
| 520 | The UI passes **Google Lighthouse** accessibility and performance audits with scores ≥ 90. | fit | `ux-density` |  |

### O · Extensibility & Plugin System (521–550) — 29 fit / 0 present / 1 conflict

| # | feature | verdict | extension | evidence / reason |
|---:|---|---|---|---|
| 521 | The system supports a **plugin architecture** — operator can drop plugin binaries or scripts into a plugins directory. | fit | `plugin-host` |  |
| 522 | Plugins can hook into **lifecycle events**: item created, item updated, item deleted, user registered, rating submitted, tag applied. | fit | `plugin-host` |  |
| 523 | Plugins can register **custom API endpoints** under `/api/v1/plugins/:plugin-name/`. | fit | `plugin-host` |  |
| 524 | Plugins can register **custom admin dashboard panels**. | fit | `plugin-host` |  |
| 525 | Plugins can register **custom UI widgets** that appear on item pages, profile pages, or the sidebar. | fit | `plugin-host` |  |
| 526 | Plugins communicate with the core system via a **defined plugin API** (function calls for in-process plugins, HTTP/gRPC for out-of-process plugins). | fit | `plugin-host` |  |
| 527 | A **scraper plugin framework**: plugins can fetch metadata from external sources (websites, APIs) and populate Item Records. | fit | `plugin-host` |  |
| 528 | Scraper plugins run in a **sandboxed environment** with network access but no filesystem access beyond their own data directory. | fit | `plugin-host` |  |
| 529 | A **deduplication plugin framework**: plugins can implement custom hash comparison algorithms beyond the built-in pHash/SHA-256. | fit | `plugin-host` |  |
| 530 | An **import/export plugin framework**: plugins can add support for importing from or exporting to external platforms' data formats. | fit | `plugin-host` |  |
| 531 | A **notification plugin framework**: plugins can add notification channels (Telegram, Discord, Pushover, ntfy, etc.). | fit | `plugin-host` |  |
| 532 | A **theme plugin framework**: plugins can add CSS/JS themes selectable by users or operator. | fit | `plugin-host` |  |
| 533 | A **recommendation algorithm plugin framework**: plugins can implement the `RecommendationBackend` interface and be selected by the operator. | fit | `plugin-host` |  |
| 534 | Plugins have a **manifest file** declaring: name, version, author, permissions required, hooks used. | fit | `plugin-host` |  |
| 535 | The operator can **enable/disable plugins** from the admin dashboard. | fit | `plugin-host` |  |
| 536 | Plugin permissions are **granular**: read-items, write-items, read-users, write-users, read-ratings, etc. — operator must approve on install. | fit | `plugin-host` |  |
| 537 | Plugins can store their own data in **namespaced database tables** created via migration. | fit | `plugin-host` |  |
| 538 | A **community plugin registry** (a simple Git repository with a JSON index) for discovering third-party plugins. | fit | `plugin-host` |  |
| 539 | The system ships with **no plugins enabled by default** — keeping the core minimal. | conflict | `plugin-host` | Explicitly contradicts the owner's directive: extensions exist so each admin tunes their instance, and the taste-first set ships enabled by default. The spec's "no plugins by default" is… |
| 540 | A **Lua scripting** option for lightweight plugins that don't warrant a compiled binary. | fit | `plugin-host` |  |
| 541 | Lua plugins have access to a sandboxed subset of the plugin API — no FFI, no file I/O. | fit | `plugin-host` |  |
| 542 | Plugins can define **custom tag namespaces** and custom metadata fields on Item Records. | fit | `plugin-host` |  |
| 543 | Custom metadata fields are stored in a **schemaless key-value extension table** linked to the item. | fit | `plugin-host` |  |
| 544 | Custom fields are searchable if the plugin registers them with the search index. | fit | `plugin-host` |  |
| 545 | Plugins can define **scheduled tasks** that run on the background worker's cron. | fit | `plugin-host` |  |
| 546 | The system exposes a **plugin development SDK** with a template repo, test harness, and documentation. | fit | `plugin-host` |  |
| 547 | Plugin updates are applied by **replacing the binary/script** — the system detects version changes and runs plugin migrations. | fit | `plugin-host` |  |
| 548 | Plugins can be **disabled without data loss** — their data tables remain and are available if re-enabled. | fit | `plugin-host` |  |
| 549 | The system logs plugin errors separately in a **plugin log** to isolate issues. | fit | `plugin-host` |  |
| 550 | A **plugin health check**: the system periodically pings active plugins and marks them unhealthy if unresponsive. | fit | `plugin-host` |  |

### P · Documentation & Onboarding (551–575) — 17 fit / 8 present / 0 conflict

| # | feature | verdict | extension | evidence / reason |
|---:|---|---|---|---|
| 551 | **Comprehensive documentation** is shipped with the binary and served at `/docs` on the instance. | fit | `docs-kit` |  |
| 552 | Documentation covers: installation, configuration, usage, API reference, plugin development, troubleshooting, and FAQ. | fit | `docs-kit` |  |
| 553 | Installation docs provide **copy-paste commands** for: Docker, Docker Compose, systemd, Nix, bare binary. | present | `docs-kit` | install docs |
| 554 | A **quickstart guide** gets a new operator from zero to running instance in < 5 minutes. | fit | `docs-kit` |  |
| 555 | The API reference is **auto-generated from the OpenAPI spec** and always in sync with the running version. | present | `docs-kit` | gqlgen-generated schema docs |
| 556 | A **glossary** defines all platform-specific terminology (Item Record, Gravity, taste fingerprint, etc.). | fit | `docs-kit` |  |
| 557 | A **contributing guide** for developers: how to build, test, submit patches. | fit | `docs-kit` |  |
| 558 | The project uses **semantic versioning** (SemVer) for all releases. | present | `docs-kit` | SemVer tags |
| 559 | A **changelog** is maintained in `CHANGELOG.md` with human-readable descriptions per release. | present | `docs-kit` | CHANGELOG-since-clone.md |
| 560 | Documentation supports **full-text search** via a client-side search index (Pagefind or Lunr). | fit | `docs-kit` |  |
| 561 | Documentation includes **architecture diagrams** (Mermaid or SVG) showing data flow, component relationships, and deployment topologies. | fit | `docs-kit` |  |
| 562 | Documentation includes a **performance tuning guide** with recommendations for different hardware profiles. | fit | `docs-kit` |  |
| 563 | Documentation includes a **migration guide** for operators upgrading between major versions. | fit | `docs-kit` |  |
| 564 | Documentation includes a **security hardening guide**: reverse proxy config, firewall rules, backup encryption, update cadence. | fit | `docs-kit` |  |
| 565 | Documentation includes **worked examples** for common tasks: "How to find a video I vaguely remember," "How to set up collaborative tagging," "How to… | fit | `docs-kit` |  |
| 566 | An **interactive tutorial** within the app (opt-in) that walks new users through: rating their first item, creating a list, using search, reading… | fit | `docs-kit` |  |
| 567 | Tooltips and **contextual help icons** throughout the UI link to relevant documentation sections. | fit | `docs-kit` |  |
| 568 | The project maintains a **public roadmap** (e.g., GitHub Issues board or a dedicated page). | fit | `docs-kit` |  |
| 569 | The project maintains **community chat** (Matrix, Discord, or IRC) linked from the docs and the repo. | fit | `docs-kit` |  |
| 570 | The project includes **example datasets** (synthetic, not real content) for testing and demonstration. | present | `docs-kit` | SeedCohort, SeedCampaign, seed scripts |
| 571 | The project includes **automated integration tests** covering all API endpoints and critical UI flows. | present | `docs-kit` | integration test suite |
| 572 | The project includes **load test scripts** (k6 or similar) for the operator to validate capacity. | fit | `docs-kit` |  |
| 573 | The project includes **database seeding scripts** for generating realistic test data at scale. | present | `docs-kit` | seeding scripts |
| 574 | Documentation is versioned alongside the code — each release tag includes matching docs. | present | `docs-kit` | docs versioned with code |
| 575 | A **man page** is generated for the CLI flags and configuration options. | fit | `docs-kit` |  |

### Q · Accessibility & Internationalisation (576–600) — 23 fit / 2 present / 0 conflict

| # | feature | verdict | extension | evidence / reason |
|---:|---|---|---|---|
| 576 | All UI text is extracted into **i18n resource files** — no hardcoded English strings in templates. | fit | `i18n-a11y` |  |
| 577 | The default locale is **English (en-US)** — additional locales are loaded from JSON/YAML files. | present | `i18n-a11y` | default locale |
| 578 | The system ships with community-contributed translations for: **Spanish, French, German, Portuguese, Japanese, Chinese (Simplified), Russian,… | fit | `i18n-a11y` |  |
| 579 | The operator can add custom locale files without code changes. | fit | `i18n-a11y` |  |
| 580 | The UI uses **CLDR-based** number formatting (thousands separators, decimal marks) per locale. | fit | `i18n-a11y` |  |
| 581 | The UI uses **locale-aware date formatting** (DD/MM/YYYY vs. MM/DD/YYYY etc.). | fit | `i18n-a11y` |  |
| 582 | Search and tagging support **CJK tokenization** (bigram or dictionary-based) for correct Chinese/Japanese/Korean search. | fit | `i18n-a11y` |  |
| 583 | Tag normalization respects **Unicode equivalence** — NFC normalization on write. | fit | `i18n-a11y` |  |
| 584 | The UI supports **font stacks** that include CJK and Arabic/Hebrew glyphs. | fit | `i18n-a11y` |  |
| 585 | All `<html>` tags include the correct **`lang` attribute** based on user locale. | fit | `i18n-a11y` |  |
| 586 | The system generates **accessible recommendation explanations** — screen reader users can understand why an item was recommended. | fit | `i18n-a11y` |  |
| 587 | All charts and visualizations have **textual alternatives** — data tables or `aria-label` descriptions. | fit | `i18n-a11y` |  |
| 588 | Color is never the **sole indicator** of state — icons, patterns, or text accompany all color cues. | fit | `i18n-a11y` |  |
| 589 | The UI supports **reduced motion** (`prefers-reduced-motion`) — animations are suppressed. | fit | `i18n-a11y` |  |
| 590 | The UI supports **high contrast** (`prefers-contrast`) — borders and text are strengthened. | fit | `i18n-a11y` |  |
| 591 | Forms use **explicit `<label>` elements** associated with inputs via `for`/`id`. | present | `i18n-a11y` | labelled form fields |
| 592 | Error messages are linked to their form fields via **`aria-describedby`**. | fit | `i18n-a11y` |  |
| 593 | Modal dialogs **trap focus** and are dismissible via Escape. | fit | `i18n-a11y` |  |
| 594 | The skip-to-content link is the **first focusable element** on every page. | fit | `i18n-a11y` |  |
| 595 | Interactive elements have **minimum touch target size** of 44×44 CSS pixels on mobile. | fit | `i18n-a11y` |  |
| 596 | The UI has been tested with **NVDA, VoiceOver, and TalkBack** screen readers. | fit | `i18n-a11y` |  |
| 597 | A **language detection** hint on item descriptions helps screen readers switch voice profiles. | fit | `i18n-a11y` |  |
| 598 | Pluralization in i18n uses **ICU MessageFormat** rules for correct plural forms across languages. | fit | `i18n-a11y` |  |
| 599 | The UI supports **user-overridable font size** via a setting (independent of browser zoom). | fit | `i18n-a11y` |  |
| 600 | The documentation itself is available in **multiple languages** (community-contributed, best-effort). | fit | `i18n-a11y` |  |

## What this intake does not do

It does not change the fork's programme. The adoption decision (which `fit` items become planned work,
in what order) is `docs/ROADMAP-extensions.md`; the per-feature rows sit in Concord as `discussion`
until the owner ranks them. Nothing here is a commitment, and no estimate in this document was derived
from a measurement of the fork's code — the sizes are shape, not schedule.

