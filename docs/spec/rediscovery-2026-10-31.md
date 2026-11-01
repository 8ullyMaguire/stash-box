# REDISCOVERY — A Metadata-Only Collaborative Filtering & Cataloging Platform

**Provenance.** Pasted verbatim by the operator on 2026-10-31 as an external product spec to be
weighed against this fork's premise (`docs/PREMISE.md`) and programme (`docs/SPEC.md`). Not adopted;
this file is the source of record for the intake, and `docs/spec/rediscovery-adoption.md` is where the
per-feature verdicts live.

---

## Premise

Rediscovery is a self-hosted, metadata-only collaborative bookmarking, tagging, and recommendation platform designed to run on commodity hardware (a single modest VPS or even a Raspberry Pi for small communities), where users collectively catalog, annotate, and rate items they have encountered across the web by storing only descriptive metadata (titles, tags, hashes, URLs, durations, descriptions, performer credits, studio attributions, dates, and user-generated annotations) — never the content itself — and the system uses the emergent structure of overlapping user taste profiles, weighted tagging consensus, and collaborative filtering gravity to surface increasingly precise recommendations, letting niche items that would otherwise be lost to time and algorithmic obscurity become findable again through a self-organizing, user-governed, operator-minimal architecture that treats every participant's curation labor as a first-class signal, requires no editorial staff, and scales gracefully from a single user's personal catalog to a mid-sized community of tens of thousands, all while remaining fully open-source and deployable with a single Docker command.

---

## Feature Specification (Categorized)

### A · Core Data Model (1–30)

1. Every cataloged item is represented as an **Item Record** consisting exclusively of metadata fields — no binary blobs, thumbnails, or media are stored on-platform.
2. Each Item Record carries a globally unique **Item ID** (UUIDv7, time-sortable).
3. Items have a required **canonical title** field (free-text, Unicode-safe, max 512 chars).
4. Items have an optional **original-language title** field for non-English originals.
5. Items support an **alias list** — unlimited alternative titles, romanizations, abbreviations.
6. Items carry a **source URL** field (primary location the submitter encountered the content).
7. Items support a **mirror URL list** for additional known locations.
8. Items carry a **perceptual hash** field (pHash, dHash, or aHash) so duplicates can be detected across re-encodes.
9. Items carry an optional **cryptographic hash** field (SHA-256) for exact-match deduplication.
10. Items store a **media type** enum: video, image-set, audio, interactive, text, mixed, unknown.
11. Items store a **duration** field (nullable, seconds, integer) for time-based media.
12. Items store a **resolution** field (nullable, e.g., 1080p, 4K) as user-reported metadata.
13. Items store a **file-size estimate** field (nullable, bytes) as user-reported metadata.
14. Items store a **release date** or **first-seen date** (ISO 8601).
15. Items store a **studio/producer** attribution field (free-text, normalized via suggestions).
16. Items store a **performer credit list** — ordered list of performer names with optional role annotations.
17. Performer names link to **Performer Records** which are their own first-class entities.
18. Performer Records hold: aliases, active-years range, self-described attributes, and linked items.
19. Items store a **series/collection** reference — linking items that are episodes or parts of a set.
20. Series Records hold: title, ordering scheme (sequential, date-based, unordered), and child items.
21. Items carry a **language** tag (ISO 639-1) and an **origin country** tag (ISO 3166-1).
22. Items carry a **content descriptor set** — a flat set of community-standardized short descriptor strings.
23. Items carry a **user-submitted free-text description** (max 2000 chars, Markdown-lite).
24. Items carry a **content-warning flags** set, distinct from descriptive tags, to support filtering.
25. Every Item Record tracks its **submitter user ID**, **submission timestamp**, and **edit history**.
26. Edit history is stored as an append-only **changelog** — each edit records who, when, which fields, old→new values.
27. Item Records have a **status** enum: `active`, `pending-review`, `merged`, `deleted`, `disputed`.
28. Deleted items are soft-deleted — metadata retained internally for merge-chain integrity but hidden from search.
29. Merged items carry a **redirect pointer** to the surviving canonical Item Record.
30. All text fields are stored UTF-8, indexed with ICU-aware collation for correct multilingual sorting.

### B · Tagging & Taxonomy (31–70)

31. The tagging system is **folksonomy-first** — any user can apply any tag string to any item.
32. Tags are normalized on write: lowercased, trimmed, collapsed whitespace, stripped of leading/trailing punctuation.
33. Tags have a **namespace prefix** system: `genre:`, `act:`, `attribute:`, `mood:`, `setting:`, `era:`, `kink:`, `bodytype:`, `clothing:`, `camerawork:`, or un-namespaced for general tags.
34. The system maintains a **Tag Dictionary** — a living registry of all tags that have ever been applied.
35. Each Tag Dictionary entry tracks: first use date, total item count, total user count, synonym links, parent tag, child tags.
36. Tags can be linked as **synonyms** by any user; synonym proposals enter a lightweight vote queue.
37. Synonym links are directional: secondary → primary; searches on secondary auto-expand to primary.
38. Tags can be organized into **hierarchical trees**: e.g., `genre:outdoor` → `genre:outdoor:beach`.
39. Hierarchy proposals are user-submitted and resolved via lazy consensus (auto-accepted if no objection in 72 h).
40. Each item's tags carry a **per-user attribution** — the system remembers who applied which tag.
41. Tags on an item carry a **consensus weight**: the count of distinct users who independently applied that tag.
42. Tags with consensus weight ≥ 3 are promoted to **confirmed tags** and rendered with a visual indicator.
43. Tags with consensus weight = 1 remain **provisional** and are searchable but styled differently.
44. Users can **dispute** a tag on an item — a negative vote that decrements the consensus weight.
45. If a tag's net weight drops to ≤ −2, it is auto-hidden (soft-removed) from the item.
46. A **tag implication** system allows rule-based auto-tagging: e.g., `tag:A implies tag:B` proposals.
47. Tag implications are voted on globally; accepted implications retroactively apply to all matching items.
48. A **tag alias** is a hard redirect at the dictionary level — searches and submissions for alias X always resolve to canonical Y.
49. A **tag blacklist** per user: tags the user never wants to see in results or recommendations.
50. A **tag whitelist** per user: affinity tags the user wants boosted in recommendation scoring.
51. The system supports **negative tags** on items: explicit assertions that a tag does NOT apply (helps train models).
52. Tags support an optional **intensity/degree** qualifier: e.g., `mood:romantic(mild)`, `mood:romantic(strong)`.
53. The system tracks **tag velocity** — how fast a tag's usage is growing — to surface trending descriptors.
54. A **tag cloud** visualization for any item, performer, series, or the global catalog.
55. Tags are **searchable with boolean logic**: AND, OR, NOT, parentheses, namespace filters.
56. Tag search supports **wildcard matching**: `act:*` returns all items with any tag in the `act:` namespace.
57. A **tag suggestion engine**: when a user begins tagging an item, the system suggests tags based on similar items' tags.
58. Tag suggestions are ranked by **co-occurrence frequency** with already-applied tags.
59. The system computes **tag correlation matrices** offline — pairs of tags that frequently co-occur.
60. The system surfaces **under-tagged items** — items with fewer than N tags — as a gamified curation task.
61. Batch tagging: a user can select multiple items and apply a tag to all of them in one action.
62. A **tag audit log** per item: chronological record of every tag addition, removal, dispute.
63. Tag namespaces are **operator-configurable** at instance level — the operator can add or rename namespace prefixes.
64. Tag length limit: 64 characters per tag string (excluding namespace prefix).
65. Maximum tags per item: 200 (configurable by operator).
66. Tags are stored in a dedicated **tags table** with an inverted index for sub-millisecond lookup.
67. A **related tags** API endpoint: given a tag, return the top-N most correlated tags by PMI (pointwise mutual information).
68. Tags carry a **spoiler flag** — users can mark a tag as a spoiler, hiding it behind a click-to-reveal.
69. The system supports **timed tags** for video items — tags that apply only to a specific timestamp range (chapter-level tagging).
70. Timed tags are stored as (tag, start_seconds, end_seconds) tuples and are searchable with a "contains moment matching" query.
### C · User Accounts & Identity (71–100)

71. User registration requires only a **username** and **password** — no email required (email optional for recovery).
72. Passwords are stored using **Argon2id** with per-user salt.
73. Optional **TOTP 2FA** support for account security.
74. Users have a **public profile page** showing: username, join date, catalog stats, public lists, taste fingerprint.
75. Users can set a **display name** separate from their login username.
76. Users can write a short **bio** (max 500 chars).
77. Users have a **privacy level** toggle: `public`, `semi-private` (stats visible, lists hidden), `private` (fully hidden).
78. Private users still contribute to collaborative filtering — their data is used in aggregate but never individually exposed.
79. Users have a **role** enum: `member`, `trusted`, `moderator`, `admin` (operator-assigned).
80. The `trusted` role is auto-granted after configurable thresholds (e.g., 50 items cataloged, 30 days active, net-positive tag consensus).
81. Only `trusted` users can vote on tag implications, synonym proposals, and hierarchy changes.
82. `moderator` role can resolve disputes, force-merge items, and issue temporary mutes.
83. `admin` role is for the instance operator — full access to configuration, user management, data export.
84. Users can **export their entire personal data** as a JSON or CSV archive at any time (GDPR-style).
85. Users can **delete their account** — personal data is scrubbed, but their tag/rating contributions are anonymized and retained for catalog integrity.
86. Users can **block** other users — bilateral invisibility in all social features.
87. Users can maintain **multiple independent taste profiles** under one account (e.g., "mood A" vs. "mood B") to avoid polluting a single recommendation vector.
88. Each taste profile has its own ratings, lists, blacklists, and whitelists.
89. The active taste profile is switchable via a single dropdown in the UI.
90. Users accumulate a **curation reputation score** based on: how often their tags reach confirmed status, how often their submissions are upvoted, how rarely their edits are reverted.
91. Reputation score is visible on profile and used as a tiebreaker in consensus disputes.
92. Users can set a **preferred language** for the UI and for default title display.
93. Users can set a **timezone** for correct display of timestamps.
94. API access is available via **personal API tokens** — users can generate/revoke tokens from settings.
95. API tokens support **scoped permissions** — read-only, write-own, or full-access.
96. The system tracks **last-active timestamp** per user for staleness detection in recommendation scoring (recent activity weighted higher).
97. Users can enable **login notifications** — receive an alert (in-app or optional email) on new login from unrecognized IP.
98. Sessions are managed via **signed JWTs** with configurable expiry (default 30 days, sliding).
99. A **session list** in settings shows all active sessions with IP and user-agent; users can revoke any session.
100. Rate limiting is enforced per-user: configurable limits on submissions, edits, and API calls per hour.

### D · Rating & Interaction System (101–140)

101. Users can **rate** any item on a configurable scale — default is 1–10 integer stars (operator can set 1–5, 1–100, or thumbs-up/down).
102. Ratings are **private by default** — only the aggregate is public unless the user opts to make their ratings public.
103. Each item displays a **Bayesian weighted average** rating (smoothed to prevent small-sample extremes).
104. Each item displays the **raw arithmetic mean**, **median**, and **rating count** alongside the Bayesian average.
105. Each item displays a **rating distribution histogram** — bar chart of how many users gave each score.
106. Users can mark an item as **seen/consumed** without rating it — a binary "I have encountered this" flag.
107. Users can mark an item as **want-to-see** — a wishlist/backlog flag.
108. Users can mark an item as **favorite** — a special flag distinct from a high rating, indicating personal significance.
109. Favorites are optionally public and displayed on the user's profile.
110. Users can **upvote** or **downvote** the quality of an Item Record's metadata (not the content itself) — "is this entry well-documented?"
111. Metadata quality votes feed into a **catalog health score** per item.
112. Items with low catalog health are surfaced to curators as "needs attention."
113. Users can write a **short review** (max 1000 chars) attached to their rating — text-only, no media embeds.
114. Reviews can be marked **spoiler** — hidden behind a click gate.
115. Reviews can be upvoted by other users as "helpful."
116. The system computes a **taste compatibility score** between any two users based on rating vector cosine similarity.
117. Users can see their top-N most compatible and least compatible users (if those users are public).
118. The system supports **rating imports** — bulk upload of ratings from a CSV or JSON file.
119. Rating changes are versioned — a user can see the history of their own rating for any item.
120. The system computes **per-user rating statistics**: mean, standard deviation, skewness — displayed on profile.
121. Users who rate everything 10 or everything 1 are auto-detected; their ratings are down-weighted in aggregate calculations.
122. A **"rate similar"** workflow: after rating an item, the system immediately suggests 5 similar un-rated items.
123. Users can **flag** an item for: duplicate, incorrect metadata, broken URL, spam, or policy violation.
124. Flags enter a community-driven **resolution queue** visible to trusted/moderator users.
125. Flags are resolved by lazy consensus: first trusted user to act resolves it; a second can override.
126. Users can **annotate** an item with timestamped notes (for video: "at 3:42, scene changes to...").
127. Annotations are personal by default but can be shared publicly.
128. Public annotations from multiple users are merged into a **community timeline** overlay for an item.
129. Users can **react** to items with a small emoji set (😍🔥😴💎🗑️) for lightweight sentiment without a full rating.
130. Reactions are aggregated and displayed as counts on the item.
131. The system tracks a **personal consumption log**: a timestamped diary of when the user interacted with each item.
132. The consumption log is exportable.
133. Users can leave a **"tip" note** on an item — a short freeform annotation aimed at future viewers (e.g., "best part starts at 12:00").
134. Tip notes are voted on for helpfulness and the top tip is pinned.
135. Users can mark an item as **"lost media"** — indicating the source URL is dead and the content may be unfindable.
136. Lost media items are visually distinct in search results and carry a community bounty for anyone who finds a live source.
137. Users can **endorse** another user's review — a lightweight signal of agreement.
138. The system detects **rating rings** — small groups of users who suspiciously rate the same items identically — and flags them for review.
139. A **daily/weekly digest** (opt-in, in-app or email): personalized summary of new items matching the user's taste profile.
140. Users can set a **minimum catalog health score** filter — suppressing items that are poorly documented from their results.
### E · Collaborative Filtering & Recommendation Engine — "Gravity" (141–200)

141. The recommendation engine is the platform's core value proposition, branded internally as **Gravity** — it pulls users toward items that their taste neighborhood has loved.
142. Gravity uses a **hybrid collaborative filtering** approach: user-user CF + item-item CF + content-based (tag-vector) signals, blended by a configurable weight vector.
143. **User-user CF**: for each target user, identify the top-K most taste-compatible users (neighbors) and recommend their highly-rated items that the target hasn't seen.
144. User similarity is computed via **cosine similarity** on the rating vector, considering only co-rated items (minimum overlap threshold: 5 items).
145. **Item-item CF**: for each item the target user has rated highly, find items most frequently co-rated-highly by the same users, and recommend those.
146. Item similarity is stored in a precomputed **sparse similarity matrix**, rebuilt on a configurable schedule (default: nightly).
147. **Content-based signal**: each item is represented as a **TF-IDF-weighted tag vector**; the user's taste profile is the centroid of their positively-rated items' tag vectors.
148. Content-based recommendations are the cosine-nearest items to the user's taste centroid that the user hasn't seen.
149. The three signals (user-CF, item-CF, content-based) are blended using **learned weights** per user — the system tracks which signal source has historically produced ratings the user ended up liking and adjusts weights accordingly.
150. A **cold-start onboarding** flow: new users are shown a grid of 30 diverse, popular items and asked to mark seen/unseen and rate any they recognize; this seeds their taste profile.
151. If a new user skips onboarding, the system falls back to **popularity-weighted recommendations** until enough data accumulates.
152. Gravity supports **negative signal**: items the user has rated poorly, blacklisted tags, and blocked performers all repel recommendations.
153. The system maintains a **taste embedding** per user: a dense vector (default 64 dimensions) learned via **matrix factorization** (ALS — Alternating Least Squares) on the user-item rating matrix.
154. Item embeddings are learned in the same latent space — recommendation is a nearest-neighbor lookup in embedding space.
155. Matrix factorization is retrained on a configurable schedule (default: nightly) using **implicit** (seen/unseen, favorite, time-spent) and **explicit** (ratings) signals.
156. The system supports **Approximate Nearest Neighbor** search (via HNSW index, using `hnswlib` or `usearch`) for sub-millisecond recommendation retrieval even at scale.
157. Gravity applies a **diversity injection**: the final recommendation list is re-ranked to ensure no single tag, performer, or studio dominates more than a configurable percentage.
158. Gravity applies a **novelty boost**: items with fewer total ratings receive a configurable uplift to combat popularity bias and surface obscure content.
159. Gravity applies a **serendipity injection**: a configurable percentage (default 10%) of recommendation slots are filled with items outside the user's taste bubble but loved by outlier-neighbors.
160. Gravity applies **temporal decay**: ratings from long ago contribute less to the user's taste profile than recent ratings (half-life configurable, default 365 days).
161. The system can generate a **"Because you liked X"** explanation for each recommendation, citing the specific item(s) and/or taste-neighbor(s) that triggered it.
162. The system can generate a **"Users like you also loved"** explanation variant.
163. The system can generate a **"Matches your tag affinity for [tag]"** explanation variant.
164. Users can give **feedback on recommendations**: thumbs-up (good rec), thumbs-down (bad rec), or "already seen" — this feedback is fed back into the weight-learning loop.
165. A **"Surprise me"** button: returns a single random item, weighted by predicted rating (i.e., biased random toward things the user is likely to enjoy).
166. A **"Deep cut"** button: returns a recommendation from the bottom quartile of popularity — maximizing obscurity while still predicting positive reception.
167. A **"Explore neighborhood"** mode: shows the user their nearest taste-neighbors' recent favorites as a feed.
168. A **"Taste map"** visualization: a 2D UMAP projection of the item embedding space with the user's rated items highlighted and recommended items pulsing.
169. A **"Taste fingerprint"** widget: a radar chart of the user's top-10 tag affinities, displayable on their profile.
170. The system computes **per-tag affinity scores** for each user: how much more (or less) the user rates items with that tag versus their personal mean.
171. Tag affinity scores power content-based recs and are exposed in the API.
172. The system detects **taste drift**: if a user's recent ratings diverge significantly from their historical profile, the system upweights recent data and notifies the user ("Your taste seems to be evolving — we're adjusting").
173. Gravity supports a **"focus mode"**: the user temporarily constrains recommendations to a specific tag, performer, or studio — the engine restricts its search space accordingly.
174. Gravity supports **negative focus**: "show me things I might like that have NOTHING to do with [tag/performer/studio]."
175. The system maintains a **recommendation cache** per user — precomputed top-200 recs, refreshed on rating activity or on a schedule.
176. Recommendation API response time target: < 100ms from cache, < 2s on cold computation.
177. Gravity supports **group recommendations**: given a set of 2+ users, find items that maximize predicted enjoyment across all members (aggregation strategy: least-misery or average, user-selectable).
178. The system tracks **recommendation hit rate**: what percentage of recommended items does the user eventually interact with? This is the primary engine quality metric.
179. The system tracks **recommendation satisfaction rate**: of interacted recommendations, what percentage did the user rate positively?
180. Both hit rate and satisfaction rate are displayed to the operator in an **engine health dashboard**.
181. The engine supports **pluggable algorithm backends** — the operator can swap in a different algorithm (e.g., Bayesian Personalized Ranking, LightFM, or a custom model) via a defined interface.
182. All recommendation computation runs **asynchronously** in a background worker queue — never blocks HTTP request threads.
183. The engine degrades gracefully: if the worker is behind, stale cached recs are served with a "recommendations may be slightly outdated" indicator.
184. The engine supports **A/B testing**: the operator can run two algorithms simultaneously and compare hit/satisfaction rates.
185. The system computes **global item popularity** using a time-decayed score (Wilson score interval on recent ratings) — used as a fallback and a blending signal.
186. Popularity is computed per-tag as well: "most popular items tagged `mood:romantic` this month."
187. The system identifies **hidden gems**: items with high average rating but low total ratings — surfaced in a dedicated discovery feed.
188. The system identifies **polarizing items**: items with high rating variance — surfaced in a dedicated "love it or hate it" feed.
189. The system computes **taste clusters**: unsupervised clustering (k-means or DBSCAN) on user embeddings to identify natural communities of taste.
190. Each cluster is auto-labeled with its top distinguishing tags.
191. Users can see which cluster(s) they belong to and browse cluster-level charts.
192. The system computes **item archetypes**: clustering on item embeddings to identify natural groupings.
193. Archetype pages show: representative items, distinguishing tags, most/least enthusiastic user clusters.
194. The recommendation engine runs entirely on **CPU** — no GPU required, ensuring commodity hardware compatibility.
195. ALS factorization for 100K users × 500K items × 64 dimensions completes in < 30 minutes on a 4-core machine using BLAS-optimized libraries.
196. HNSW index for 500K items × 64 dimensions fits in < 500 MB RAM and queries in < 1 ms.
197. The engine's **retraining schedule** is configurable: manual, cron-based, or triggered by a threshold of new ratings since last train.
198. The engine exports a **model artifact** (numpy arrays + index files) that can be version-controlled and rolled back.
199. The system exposes a **"Why not this?"** tool: the user can input an item they expected to be recommended and the system explains why it wasn't (low predicted score, already in blacklisted tag, too similar to a negatively-rated item, etc.).
200. All Gravity computations are **deterministic** given the same input data and random seed — ensuring reproducibility for debugging.
### F · Search & Discovery (201–240)

201. **Full-text search** across item titles, aliases, descriptions, performer names, studio names, and tags.
202. Search is powered by an **embedded search engine** — either SQLite FTS5 (small instances) or Meilisearch/Typesense sidecar (larger instances).
203. Search supports **typo tolerance** — Levenshtein distance ≤ 2 for terms > 5 characters.
204. Search supports **prefix matching** — results appear as the user types.
205. Search supports **faceted filtering**: narrow results by tag, performer, studio, year range, duration range, resolution, rating range, media type.
206. Search supports **boolean tag expressions**: `tag:A AND tag:B NOT tag:C`.
207. Search supports **sorting**: by relevance, rating (Bayesian), rating count, date added, date released, popularity score, catalog health score.
208. Search results display: title, top-5 confirmed tags, average rating, rating count, duration, and a "predicted score for you" badge (from Gravity).
209. An **advanced search** form exposes all filterable fields with dropdowns and range sliders.
210. Search queries are **logged per-user** (opt-out available) to improve recommendation signals (search-as-intent).
211. A **"similar items"** link on every item page: shows the top-20 nearest items by item embedding distance.
212. A **"more by this performer"** link on every item page.
213. A **"more from this studio"** link on every item page.
214. A **"more in this series"** link on every item page (chronologically ordered).
215. A **random item** button: uniformly random across the entire catalog (no taste weighting — pure serendipity).
216. A **"feeling lucky"** button: random item weighted by predicted score for the current user.
217. A **browse-by-tag** page: an alphabetical/hierarchical explorer of the tag dictionary with item counts.
218. A **browse-by-performer** page: alphabetical with item counts, sortable by popularity or catalog health.
219. A **browse-by-studio** page: same as performer.
220. A **browse-by-year** page: timeline visualization of catalog density by release year.
221. A **trending** page: items with the most new ratings/interactions in the last 24h, 7d, 30d.
222. A **recently added** page: newest Item Records, reverse chronological.
223. A **recently updated** page: items whose metadata was most recently edited.
224. A **"lost and found"** page: aggregation of items marked as lost media — community members can attempt to locate them.
225. A **"needs tags"** page: items with fewer than N tags, sorted by popularity (so the most-seen under-tagged items get attention first).
226. A **"needs ratings"** page: items with fewer than N ratings.
227. A **"controversial"** page: items with the highest rating standard deviation.
228. A **"hall of fame"** page: all-time highest-rated items with a minimum rating count threshold.
229. A **"worst of"** page: lowest-rated items (opt-in visibility, off by default).
230. A **saved searches** feature: users can save complex queries and receive notifications when new items match.
231. Saved searches support **RSS/Atom feed output** — users can subscribe in their feed reader.
232. The search index is updated in **near-real-time** — new items are searchable within seconds of submission.
233. Search supports **multi-language stemming** — English, Spanish, French, German, Japanese, and operator-extensible.
234. Search supports **quoted exact-match phrases**.
235. Search supports **field-specific queries**: `performer:"name" studio:"name" title:"name"`.
236. An **autocomplete/suggestion** dropdown appears as the user types in the search bar, showing matching items, performers, tags, and studios.
237. The system tracks **search result click-through rates** to improve relevance ranking over time.
238. A **"did you mean?"** suggestion for queries that return zero results but are close to known terms.
239. Search results support **infinite scroll** or **paginated** mode (user preference).
240. The search API is fully accessible via the public REST API — enabling third-party clients and integrations.

### G · User Lists & Collections (241–270)

241. Users can create **custom lists** — ordered collections of items with a title and optional description.
242. Lists have a **visibility** toggle: public, unlisted (accessible via link), or private.
243. Lists support **ranked ordering** (drag-and-drop reorder) or **unordered set** mode.
244. Users can create unlimited lists.
245. Lists have a **comment thread** (if public) — other users can discuss the list.
246. Users can **fork** a public list — creating their own copy to modify.
247. Users can **follow** a public list — receiving notifications when items are added.
248. Lists support **collaborative editing** — the list owner can invite other users as co-editors.
249. Lists display aggregate statistics: total items, average rating, tag distribution.
250. Lists can be **exported** as JSON, CSV, or plain-text.
251. Lists can be **imported** from JSON or CSV (matching by title, hash, or URL).
252. A **"smart list"** feature: auto-populated lists based on a saved search query — the list updates dynamically as new matching items are cataloged.
253. Smart lists support all the same filters as advanced search.
254. Users have a built-in **"Favorites"** list (a special system list backed by the favorite flag).
255. Users have a built-in **"Want to See"** list (a special system list backed by the wishlist flag).
256. Users have a built-in **"Seen"** list (a special system list backed by the seen flag).
257. A **"top lists"** discovery page: community-curated lists sorted by follower count.
258. Lists can be **tagged** with descriptive tags for discoverability.
259. List search: full-text search across list titles, descriptions, and tags.
260. Users can **upvote** lists to signal quality — upvotes contribute to list ranking on the top lists page.
261. Lists support a **cover image URL** (external link) for visual presentation.
262. A **"list comparison"** tool: given two lists, show intersection, unique-to-A, unique-to-B.
263. A **"list merge"** tool: combine two lists with deduplication.
264. Lists support **sections/headers** — dividers within the list for organization (e.g., "Tier 1", "Tier 2").
265. Lists carry a **last-updated timestamp** and an **update count** displayed publicly.
266. Users can **embed** a public list on external sites via an iframe or oEmbed endpoint.
267. A **"challenge list"** subtype: a curated list intended as a viewing challenge (e.g., "The 100 Essential [Genre] Works") with per-user progress tracking.
268. Challenge lists show a **progress bar** on the user's profile.
269. Lists support **item-level notes** — the list creator can annotate each item within the list context.
270. The system enforces a **maximum list size** (default: 10,000 items) to prevent abuse.

### H · Community & Social Features (271–310)

271. A **global feed** showing recent activity: new items, new ratings (if user is public), new lists, new reviews.
272. The global feed is filterable by activity type and by tag.
273. Users can **follow** other users — their public activity appears in a **personal feed**.
274. Follow relationships are **asymmetric** (Twitter-style, not mutual-required).
275. The personal feed is reverse-chronological with optional algorithmic reordering (boosting activity from most-compatible users).
276. Users can **comment** on items — threaded discussion with Markdown-lite formatting.
277. Comments can be upvoted/downvoted; comments are sorted by score (Wilson lower bound) or chronologically.
278. Comments have a **maximum depth** of 5 levels to prevent deep nesting on small screens.
279. Users can **mention** other users in comments with @username — the mentioned user receives a notification.
280. A **notifications** system: in-app notifications for mentions, list follows, review endorsements, flag resolutions, recommendation digests.
281. Notifications are accessible via a bell icon with unread count badge.
282. Notifications are paginated and can be marked read individually or all-at-once.
283. Notifications can be configured per-type: on/off, in-app only, or in-app + email.
284. A **direct messaging** system between users — encrypted at rest, with block/report support.
285. DMs can be disabled per-user in settings.
286. A **forums** subsystem: topic-based discussion boards organized by category (meta, recommendations, help, off-topic).
287. Forum posts support Markdown, @mentions, and item/list/performer links via shortcodes.
288. Forum moderation uses the same role system as the rest of the platform.
289. A **"who else liked this?"** feature on items: shows avatars/links to public users who rated the item highly (≥ 8/10).
290. A **leaderboard** page: top curators by reputation score, most items cataloged, most tags confirmed.
291. Leaderboards are opt-in — users can hide themselves from leaderboards in settings.
292. A **"taste twin"** feature: each user is shown their single most taste-compatible public user, with a compatibility percentage.
293. Users can form **groups** — named communities with a shared feed, shared lists, and a group chat.
294. Groups have roles: owner, moderator, member.
295. Groups can be public (discoverable, open join), approval-required, or invite-only.
296. A **group recommendation** feature: items recommended for the group based on aggregate member tastes (see feature 177).
297. A **"this or that"** game: the system presents two items and the user picks which they prefer — the pairwise data enriches taste profiling via Bradley-Terry modeling.
298. A **"rate roulette"** game: the system presents an un-rated item (from items the user has marked "seen" but not rated) and asks for a quick rating.
299. A **"tag sprint"** game: the system presents an under-tagged item and challenges the user to add as many applicable tags as possible in 60 seconds.
300. Games contribute to reputation score and a **gamification badges** system.
301. Badges are awarded for milestones: 10/50/100/500/1000 items rated, first list created, first review, 100 tags confirmed, etc.
302. Badges are displayed on the user's profile.
303. A **"match percentage"** is displayed between the current user and any other public user on their profile page.
304. Users can **report** any content (item, comment, review, DM, list) for policy violation.
305. Reports go to the **moderation queue** (visible to moderators and operator).
306. The moderation queue is the **only operator-facing review surface** — keeping the "minimal operator overhead" promise.
307. Automated **spam detection** on submissions and comments using basic heuristics: link density, repetition, new-account burst.
308. Suspected spam is auto-held for moderator review.
309. A **community announcements** channel: operator can post announcements that appear as a banner or pinned feed item.
310. A **changelog** page: operator or system posts about new features, maintenance windows, policy changes.
### I · Anti-Abuse, Content Policy & Safety (311–340)

311. The platform enforces a **terms of service** that the operator can customize — users must accept on registration.
312. Content policy is **operator-defined** — the system provides a template but does not impose its own content restrictions beyond legal minimums.
313. The system supports **mandatory content-warning tags**: operator can define a set of tags that, if applied, trigger a click-through interstitial before showing the item's full metadata.
314. Users can set **global content filters** based on tags — items matching filtered tags are hidden from all views (search, browse, recommendations, feeds).
315. Content filters are applied at the **query level** for performance (filtered items never leave the database).
316. The system supports an **instance-level tag blocklist**: the operator can ban specific tags entirely — items cannot be tagged with them and search queries are stripped of them.
317. A **flood control** system: maximum N item submissions per user per hour (default 20, configurable).
318. A **sockpuppet detection** heuristic: flags accounts with suspiciously similar behavior patterns (same IP, same rating patterns, created in burst).
319. Moderators can **mute** a user for a configurable duration — the user can browse but cannot submit, edit, tag, rate, or comment.
320. Moderators can **ban** a user — full access revocation, IP optionally blocked.
321. Ban appeals are handled via a dedicated **appeal form** that emails the operator directly.
322. The system keeps an **audit log** of all moderator actions: who, when, what, reason.
323. The audit log is visible to the operator and optionally to all moderators for accountability.
324. The system supports **shadow-limiting**: a misbehaving user's contributions remain visible only to themselves — invisible to everyone else.
325. The system supports **CAPTCHA** on registration (hCaptcha or turnstile, operator-configurable).
326. The system supports **invite-only registration** mode: new users can only join via an invite code from an existing member.
327. Invite codes can be limited-use (single-use or N-use) and expire after a configurable duration.
328. The system supports **account age gates** on certain actions: e.g., only accounts > 7 days old can vote on tag proposals.
329. The system supports **contribution rate limits** that scale with reputation: higher-rep users get higher limits.
330. IP-based rate limiting at the **reverse proxy level** (documentation provided for Nginx/Caddy configuration).
331. The system supports **automated nightly integrity checks**: orphaned records, broken merge chains, tag dictionary inconsistencies.
332. Integrity check results are logged and optionally emailed to the operator.
333. The system supports **read-only mode**: operator can flip a switch to make the instance non-writable during maintenance.
334. The system supports **allowlisted registration domains** for email-required instances (e.g., only `@university.edu` users can register).
335. The system logs all **authentication failures** and supports fail2ban-compatible log formatting.
336. All user-facing forms have **CSRF protection** via double-submit cookies.
337. The system sets **security headers**: HSTS, X-Content-Type-Options, X-Frame-Options, CSP.
338. All cookies are **HttpOnly, Secure, SameSite=Lax**.
339. The system supports **content hash deduplication on submit**: if a new item's perceptual hash matches an existing item within threshold, the submitter is warned and given the option to merge or proceed.
340. Duplicate detection results are cached to avoid redundant hash comparisons.

### J · API & Integration (341–370)

341. A **RESTful JSON API** covering all platform functionality: items, tags, performers, studios, series, users, lists, ratings, recommendations, search.
342. API versioning via **URL prefix** (`/api/v1/`).
343. API authentication via **Bearer token** (personal API tokens from user settings).
344. Unauthenticated API access is allowed for read-only public data (configurable by operator).
345. API responses follow a consistent **envelope format**: `{ "data": ..., "meta": { "page", "total", "limit" }, "errors": [...] }`.
346. API supports **pagination** via `?page=N&limit=N` (max limit 100, default 25).
347. API supports **sparse fieldsets**: `?fields=title,rating,tags` to reduce payload size.
348. API supports **relationship inclusion**: `?include=performers,studio,series` to reduce round-trips.
349. API supports **bulk operations**: POST/PATCH/DELETE on arrays of items/ratings/tags in a single request (max 100 per batch).
350. API supports **webhook registration**: users/operators can register webhooks for events (new item, new rating on specific item, new match for saved search).
351. Webhooks fire HTTP POST requests with a signed payload (HMAC-SHA256).
352. A **WebSocket** endpoint for real-time updates: live feed, notification push, live search-as-you-type.
353. An **RSS/Atom feed** for: new items (global or per-tag), user activity (if public), list updates, saved search matches.
354. An **OPML export** of all a user's RSS subscriptions on the platform.
355. API rate limiting: default 300 requests/minute per token, configurable by operator.
356. Rate limit headers are returned in every response: `X-RateLimit-Limit`, `X-RateLimit-Remaining`, `X-RateLimit-Reset`.
357. The API exposes the **recommendation engine** endpoints: `GET /api/v1/recommendations` (personal), `POST /api/v1/recommendations/group` (group).
358. The API exposes a **taste profile** endpoint: `GET /api/v1/users/me/taste` returning tag affinities, embedding vector, and cluster memberships.
359. The API exposes a **similar items** endpoint: `GET /api/v1/items/:id/similar`.
360. The API exposes a **duplicate check** endpoint: `POST /api/v1/items/duplicate-check` accepting a hash or title and returning potential matches.
361. **OpenAPI 3.1 specification** is auto-generated and served at `/api/docs`.
362. A **Swagger UI** instance is served at `/api/playground` for interactive API exploration.
363. The API supports **JSON:API** format as an alternative content type (via `Accept` header).
364. The system supports **import from external catalogs** via configurable adapters — operator can write a plugin to import from another database.
365. The system supports **export of the entire catalog** as a compressed JSON dump (operator only).
366. The system supports **ActivityPub** (optional module) for federation between Rediscovery instances — items, ratings, and lists can flow across instances.
367. Federation supports **selective sync**: instance operators can choose which tags/categories to federate.
368. The API supports **conditional requests** via ETag/If-None-Match for efficient polling.
369. The API supports **CORS** with configurable allowed origins.
370. All API error responses include a machine-readable **error code** and a human-readable **message**.

### K · Architecture & Commodity Hardware (371–420)

371. The entire application is packaged as a **single statically-linked binary** (backend) + a **static frontend bundle** — no runtime dependencies except the database.
372. The backend is written in a **memory-safe, compiled language** (Rust or Go) for minimal resource consumption.
373. The default database is **SQLite** (WAL mode) — zero-configuration, single-file, suitable for instances up to ~500K items and ~50K users.
374. An optional **PostgreSQL** backend is supported for larger instances — switchable via a single config flag.
375. The application requires **≤ 256 MB RAM** at idle with a warm cache for a 100K-item catalog on SQLite.
376. The application requires **≤ 1 GB RAM** for a 500K-item catalog with the recommendation engine's in-memory structures.
377. The application runs on a **single $5/month VPS** (1 vCPU, 1 GB RAM, 25 GB SSD) for small communities (≤ 5K users, ≤ 100K items).
378. The application runs on a **Raspberry Pi 4** (4 GB RAM) for personal/home-server use.
379. The **frontend** is a server-rendered HTML application (with progressive enhancement via minimal JS) — not a heavy SPA.
380. Frontend pages are **< 100 KB** total transfer (HTML + CSS + JS), excluding user-uploaded avatars.
381. Frontend works **without JavaScript** for core flows (browse, search, read) — JS enhances interactions (live search, drag-and-drop, voting) but is not required.
382. Frontend is **responsive** — mobile-first layout that works on all screen sizes.
383. The system uses **no external CDN** — all assets are self-hosted.
384. The system uses **no third-party analytics or tracking** — zero external requests from the frontend.
385. The system uses **no cookies except functional ones** (session, CSRF) — no tracking cookies.
386. The backend serves both the **frontend** and the **API** — single process, single port.
387. An optional **background worker** process handles: recommendation engine retraining, search index updates, integrity checks, digest generation.
388. The background worker can run in-process (default, for single-server setups) or as a separate process connected via a lightweight job queue (Redis or an embedded queue).
389. For the embedded queue, the system uses **SQLite as the job queue** — no Redis dependency for small instances.
390. The system supports **horizontal read scaling** via read replicas (PostgreSQL) or Litestream-replicated SQLite.
391. The system's **data directory** contains: the database file, the search index, the recommendation model artifacts, and the configuration file — all in one predictable location.
392. Backup is as simple as **copying the data directory** (or using `sqlite3 .backup` for live backup).
393. The system supports **automated backups** to local disk or S3-compatible storage on a cron schedule.
394. Database migrations are **embedded in the binary** and auto-applied on startup — no manual migration step.
395. The system logs to **stdout/stderr** in structured JSON format — compatible with any log aggregator.
396. Log verbosity is configurable: `error`, `warn`, `info`, `debug`, `trace`.
397. The system exposes a **health check** endpoint at `/health` — returns 200 OK with uptime and basic stats.
398. The system exposes a **metrics** endpoint at `/metrics` in Prometheus-compatible format (optional, off by default).
399. Metrics include: request count, latency percentiles, active users, items count, recommendation cache hit rate, background job queue depth.
400. Deployment is documented for: **Docker**, **Docker Compose**, **systemd**, **bare binary**, and **NixOS module**.
401. A `docker-compose.yml` one-liner brings up the full stack: app + (optional) PostgreSQL + (optional) Meilisearch.
402. The Docker image is **< 50 MB** (distroless base).
403. The system supports **configuration via environment variables**, a TOML config file, or CLI flags (in that priority order).
404. All configuration options have **sane defaults** — the system runs with zero configuration for a personal instance.
405. The system supports **TLS termination** natively (via built-in ACME/Let's Encrypt support) or via reverse proxy (documented for Nginx, Caddy, Traefik).
406. The system supports **Unix socket** listening for reverse-proxy setups.
407. The system supports **graceful shutdown** — in-flight requests are completed, background jobs are checkpointed.
408. The system supports **live reloading of configuration** (SIGHUP) without restart for most settings.
409. The system's **startup time** is < 3 seconds on commodity hardware.
410. The system runs on **Linux (x86_64, ARM64), macOS (x86_64, ARM64), and FreeBSD** — pre-built binaries for all.
411. The system supports **Windows** via WSL2 or Docker.
412. The build system produces **reproducible builds** — same source → same binary hash.
413. The system includes a **built-in benchmark suite** that the operator can run to verify hardware adequacy.
414. The benchmark suite tests: search latency, recommendation computation time, concurrent user simulation.
415. The system supports **connection pooling** for PostgreSQL (via built-in pool, default 10 connections).
416. SQLite mode uses **WAL journal** with `busy_timeout` and `PRAGMA synchronous=NORMAL` for safe concurrent reads with low-latency writes.
417. The system uses **prepared statements** exclusively — no string-interpolated SQL.
418. Database schema uses **covering indexes** on hot query paths (tag lookups, rating aggregations, search).
419. The system supports **online vacuuming** and `ANALYZE` on a schedule to keep SQLite performance stable.
420. Memory usage is bounded via **configurable cache sizes** for the database page cache, search index cache, and recommendation structures.
### L · Operator Administration (421–450)

421. An **admin dashboard** accessible at `/admin` — web-based, no separate tool required.
422. Dashboard shows: total users, total items, total ratings, active users (24h/7d/30d), database size, recommendation engine status, job queue depth.
423. Dashboard shows **growth charts**: new users, new items, new ratings over time.
424. Dashboard shows **moderation queue depth** and average resolution time.
425. Operator can **configure all instance settings** from the dashboard — no config file editing required for runtime-changeable settings.
426. Operator can **manage users**: search, view profiles, change roles, mute, ban, delete.
427. Operator can **impersonate** a user for debugging (with audit logging).
428. Operator can **broadcast announcements** from the dashboard.
429. Operator can **trigger recommendation engine retraining** manually from the dashboard.
430. Operator can **view recommendation engine metrics**: training time, model size, cache hit rate, hit/satisfaction rates.
431. Operator can **view and manage the tag dictionary**: rename tags, merge tags, delete tags, manage synonyms/implications/hierarchy.
432. Operator can **configure the rating scale** (1–5, 1–10, 1–100, thumbs) — changing scale offers a migration tool to rescale existing ratings.
433. Operator can **configure registration mode**: open, invite-only, approval-required, or closed.
434. Operator can **configure the instance name, description, logo URL, and color theme**.
435. Operator can **configure content policy text** displayed on registration and in the footer.
436. Operator can **configure flood control thresholds** per role.
437. Operator can **configure recommendation engine parameters**: retrain schedule, embedding dimensions, diversity/novelty/serendipity weights, decay half-life.
438. Operator can **view the audit log** from the dashboard.
439. Operator can **export/import the full database** from the dashboard for migration or backup.
440. Operator can **schedule maintenance windows** — the system shows a banner to users and optionally enters read-only mode.
441. Operator can set **instance-level default content filters** — applied to all users unless individually overridden.
442. Operator can **pin items** to the homepage or discovery page for editorial promotion (the one exception to "no editorial staff" — operator can optionally curate).
443. Operator can **define custom badge criteria** beyond the defaults.
444. Operator can **manage federation peers** (if ActivityPub module is enabled).
445. Operator can **view per-user statistics**: items submitted, tags applied, ratings given, reports filed, flags resolved.
446. Operator can **configure email settings**: SMTP server for notifications and recovery emails.
447. Operator can **configure external authentication**: OAuth2/OIDC (e.g., login with a self-hosted Authentik/Keycloak).
448. Operator can **view slow query logs** from the dashboard for performance tuning.
449. The admin dashboard itself is **protected by role check and optionally by a separate admin password** for defense-in-depth.
450. All admin actions are **idempotent** and confirmable — destructive actions require a confirmation dialog with the action spelled out.

### M · Data Integrity, Portability & Privacy (451–480)

451. All data is stored **locally** — no cloud dependencies, no SaaS calls, no phoning home.
452. The system **never contacts any external server** unless explicitly configured (e.g., federation, SMTP, ACME).
453. A **full data export** in machine-readable format (JSON lines or CSV) is available to the operator.
454. The export includes: all items, tags, performers, studios, series, users (anonymizable), ratings, lists, reviews, comments, audit logs.
455. A **full data import** from the same format is supported — enabling instance migration.
456. The system supports **merging two instance databases** with conflict resolution (operator-guided).
457. User data export (feature 84) includes: all ratings, lists, reviews, tags applied, consumption log, taste profile, and settings.
458. User data is stored in a **normalized relational schema** — no embedded documents or opaque blobs.
459. The schema is **fully documented** in a schema reference guide within the repo.
460. Foreign key constraints are enforced at the **database level** for referential integrity.
461. The system uses **database transactions** for all multi-step operations — no partial writes on failure.
462. The system supports **point-in-time recovery** via WAL archiving (SQLite) or PostgreSQL's native PITR.
463. Perceptual hashes are indexed for **sub-linear duplicate search** using a BK-tree or VP-tree structure.
464. The system stores **no IP addresses** in the application database — IP-based rate limiting is handled at the reverse proxy or in ephemeral memory.
465. The system stores **no tracking data** beyond what is functionally necessary (ratings, tags, interactions).
466. The operator can configure a **data retention policy**: auto-delete consumption logs older than N days, auto-anonymize inactive accounts after N days.
467. The system supports **right-to-erasure** requests: operator can trigger a full data scrub for a specific user.
468. Passwords are **never logged** — not even in debug mode.
469. API tokens are stored as **salted hashes** — the raw token is shown once on creation and never again.
470. Database backups can be **encrypted at rest** using age or GPG (operator-configured).
471. The system performs **schema validation** on startup — if the database is from an incompatible version, a clear error is shown with migration instructions.
472. The system writes a **version stamp** to the database on each migration for traceability.
473. The system supports **logical replication** between instances for read scaling or geo-distribution (PostgreSQL mode).
474. The system supports **streaming backups** via Litestream (SQLite mode) to S3-compatible storage.
475. All exports include a **schema version header** so importers can detect format mismatches.
476. The system checksums export files with **SHA-256** for integrity verification.
477. The system supports **selective export**: export only items matching a tag filter, or only a specific user's data.
478. The system's database file can be **directly queried** with standard SQLite or PostgreSQL tools for ad-hoc analysis.
479. The system includes a **built-in SQL console** (operator-only) accessible via the admin dashboard for read-only queries.
480. The system ensures **backward compatibility** for at least 2 major versions — old databases can be migrated forward.

### N · Frontend & User Experience (481–520)

481. The UI follows a **content-density-first** design — maximum information per viewport, minimal whitespace, no hero images or decorative fluff.
482. The default theme is **dark mode** with a muted color palette — easy on the eyes for extended browsing.
483. A **light mode** theme is available, switchable in user settings.
484. Operator can configure **custom CSS** for instance branding.
485. The UI uses **semantic HTML5** — fully accessible to screen readers and keyboard navigation.
486. All interactive elements have **visible focus indicators** — WCAG 2.1 AA compliant.
487. All images (avatars, external thumbnails if linked) have **alt text** fields.
488. The UI supports **keyboard shortcuts** for power users: `s` for search, `r` for random, `/` for focus search bar, `j/k` for navigation, `1-0` for rating.
489. Keyboard shortcuts are documented in a **cheat sheet** modal (triggered by `?`).
490. The UI is fully functional at **320px viewport width** (small phones).
491. The UI supports **infinite scroll** or **pagination** — user's choice, stored in preferences.
492. Item pages have a **sticky action bar** at the top: rate, favorite, mark seen, add to list, flag.
493. Tag pills on item pages are **clickable** — clicking a tag navigates to a search for that tag.
494. The UI supports **drag-and-drop** for reordering list items.
495. The UI supports **batch selection** in search results and lists for bulk operations (rate, tag, add to list).
496. The UI has a **toast notification system** for non-blocking feedback (e.g., "Rating saved", "Item added to list").
497. The UI has a **command palette** (Ctrl+K / Cmd+K) for quick navigation to any page, item, or action.
498. The recommendation page is a **dedicated full-page experience** showing Gravity's picks with explanations.
499. The taste map (feature 168) uses **Canvas or SVG** rendering — no heavy WebGL dependency.
500. Forms use **client-side validation** with clear error messages before submission.
501. All data tables support **column sorting** by clicking headers.
502. All data tables support **column filtering** via a filter row.
503. The UI shows **relative timestamps** ("3 hours ago") with absolute timestamps on hover.
504. The UI supports **localization** (i18n) — English is default, community-contributed translations are loaded from JSON files.
505. The UI renders **Markdown-lite** in descriptions, reviews, and comments: bold, italic, links, lists, code, blockquotes — no images or HTML.
506. The UI has a **"compact mode"** toggle: reduces line-height, font-size, and padding for users who want maximum density.
507. The UI has a **"reader mode"** for review/comment threads: single-column, larger font, no sidebar.
508. Page transitions are **instant** (< 100ms perceived) via `<link rel="prefetch">` on hover for navigation links.
509. The login page supports **"remember me"** — extends session duration to 90 days.
510. The registration page includes a **password strength meter**.
511. The UI shows a **global search bar** prominently in the header on every page.
512. The UI has an **"onboarding wizard"** for first-time users: explains key concepts, walks through taste seeding, privacy settings.
513. The UI has a **"what's new" modal** shown once after each update, summarizing new features.
514. The UI supports **right-to-left (RTL) text** for Arabic, Hebrew, etc.
515. The UI avoids **layout shift** — all dynamically loaded content has pre-allocated dimensions.
516. The UI loads **above-the-fold content first** via server-side rendering — no blank-page spinner.
517. Item pages show a **"metadata completeness" indicator** — a progress bar showing how many recommended fields are filled.
518. Error pages (404, 500) are **branded and helpful** — include search bar and navigation links.
519. The UI supports **custom user CSS** — users can inject their own stylesheet via settings (scoped to their session).
520. The UI passes **Google Lighthouse** accessibility and performance audits with scores ≥ 90.
### O · Extensibility & Plugin System (521–550)

521. The system supports a **plugin architecture** — operator can drop plugin binaries or scripts into a plugins directory.
522. Plugins can hook into **lifecycle events**: item created, item updated, item deleted, user registered, rating submitted, tag applied.
523. Plugins can register **custom API endpoints** under `/api/v1/plugins/:plugin-name/`.
524. Plugins can register **custom admin dashboard panels**.
525. Plugins can register **custom UI widgets** that appear on item pages, profile pages, or the sidebar.
526. Plugins communicate with the core system via a **defined plugin API** (function calls for in-process plugins, HTTP/gRPC for out-of-process plugins).
527. A **scraper plugin framework**: plugins can fetch metadata from external sources (websites, APIs) and populate Item Records.
528. Scraper plugins run in a **sandboxed environment** with network access but no filesystem access beyond their own data directory.
529. A **deduplication plugin framework**: plugins can implement custom hash comparison algorithms beyond the built-in pHash/SHA-256.
530. An **import/export plugin framework**: plugins can add support for importing from or exporting to external platforms' data formats.
531. A **notification plugin framework**: plugins can add notification channels (Telegram, Discord, Pushover, ntfy, etc.).
532. A **theme plugin framework**: plugins can add CSS/JS themes selectable by users or operator.
533. A **recommendation algorithm plugin framework**: plugins can implement the `RecommendationBackend` interface and be selected by the operator.
534. Plugins have a **manifest file** declaring: name, version, author, permissions required, hooks used.
535. The operator can **enable/disable plugins** from the admin dashboard.
536. Plugin permissions are **granular**: read-items, write-items, read-users, write-users, read-ratings, etc. — operator must approve on install.
537. Plugins can store their own data in **namespaced database tables** created via migration.
538. A **community plugin registry** (a simple Git repository with a JSON index) for discovering third-party plugins.
539. The system ships with **no plugins enabled by default** — keeping the core minimal.
540. A **Lua scripting** option for lightweight plugins that don't warrant a compiled binary.
541. Lua plugins have access to a sandboxed subset of the plugin API — no FFI, no file I/O.
542. Plugins can define **custom tag namespaces** and custom metadata fields on Item Records.
543. Custom metadata fields are stored in a **schemaless key-value extension table** linked to the item.
544. Custom fields are searchable if the plugin registers them with the search index.
545. Plugins can define **scheduled tasks** that run on the background worker's cron.
546. The system exposes a **plugin development SDK** with a template repo, test harness, and documentation.
547. Plugin updates are applied by **replacing the binary/script** — the system detects version changes and runs plugin migrations.
548. Plugins can be **disabled without data loss** — their data tables remain and are available if re-enabled.
549. The system logs plugin errors separately in a **plugin log** to isolate issues.
550. A **plugin health check**: the system periodically pings active plugins and marks them unhealthy if unresponsive.

### P · Documentation & Onboarding (551–575)

551. **Comprehensive documentation** is shipped with the binary and served at `/docs` on the instance.
552. Documentation covers: installation, configuration, usage, API reference, plugin development, troubleshooting, and FAQ.
553. Installation docs provide **copy-paste commands** for: Docker, Docker Compose, systemd, Nix, bare binary.
554. A **quickstart guide** gets a new operator from zero to running instance in < 5 minutes.
555. The API reference is **auto-generated from the OpenAPI spec** and always in sync with the running version.
556. A **glossary** defines all platform-specific terminology (Item Record, Gravity, taste fingerprint, etc.).
557. A **contributing guide** for developers: how to build, test, submit patches.
558. The project uses **semantic versioning** (SemVer) for all releases.
559. A **changelog** is maintained in `CHANGELOG.md` with human-readable descriptions per release.
560. Documentation supports **full-text search** via a client-side search index (Pagefind or Lunr).
561. Documentation includes **architecture diagrams** (Mermaid or SVG) showing data flow, component relationships, and deployment topologies.
562. Documentation includes a **performance tuning guide** with recommendations for different hardware profiles.
563. Documentation includes a **migration guide** for operators upgrading between major versions.
564. Documentation includes a **security hardening guide**: reverse proxy config, firewall rules, backup encryption, update cadence.
565. Documentation includes **worked examples** for common tasks: "How to find a video I vaguely remember," "How to set up collaborative tagging," "How to tune Gravity for my community."
566. An **interactive tutorial** within the app (opt-in) that walks new users through: rating their first item, creating a list, using search, reading recommendations.
567. Tooltips and **contextual help icons** throughout the UI link to relevant documentation sections.
568. The project maintains a **public roadmap** (e.g., GitHub Issues board or a dedicated page).
569. The project maintains **community chat** (Matrix, Discord, or IRC) linked from the docs and the repo.
570. The project includes **example datasets** (synthetic, not real content) for testing and demonstration.
571. The project includes **automated integration tests** covering all API endpoints and critical UI flows.
572. The project includes **load test scripts** (k6 or similar) for the operator to validate capacity.
573. The project includes **database seeding scripts** for generating realistic test data at scale.
574. Documentation is versioned alongside the code — each release tag includes matching docs.
575. A **man page** is generated for the CLI flags and configuration options.

### Q · Accessibility & Internationalization (576–600)

576. All UI text is extracted into **i18n resource files** — no hardcoded English strings in templates.
577. The default locale is **English (en-US)** — additional locales are loaded from JSON/YAML files.
578. The system ships with community-contributed translations for: **Spanish, French, German, Portuguese, Japanese, Chinese (Simplified), Russian, Korean** (as available).
579. The operator can add custom locale files without code changes.
580. The UI uses **CLDR-based** number formatting (thousands separators, decimal marks) per locale.
581. The UI uses **locale-aware date formatting** (DD/MM/YYYY vs. MM/DD/YYYY etc.).
582. Search and tagging support **CJK tokenization** (bigram or dictionary-based) for correct Chinese/Japanese/Korean search.
583. Tag normalization respects **Unicode equivalence** — NFC normalization on write.
584. The UI supports **font stacks** that include CJK and Arabic/Hebrew glyphs.
585. All `<html>` tags include the correct **`lang` attribute** based on user locale.
586. The system generates **accessible recommendation explanations** — screen reader users can understand why an item was recommended.
587. All charts and visualizations have **textual alternatives** — data tables or `aria-label` descriptions.
588. Color is never the **sole indicator** of state — icons, patterns, or text accompany all color cues.
589. The UI supports **reduced motion** (`prefers-reduced-motion`) — animations are suppressed.
590. The UI supports **high contrast** (`prefers-contrast`) — borders and text are strengthened.
591. Forms use **explicit `<label>` elements** associated with inputs via `for`/`id`.
592. Error messages are linked to their form fields via **`aria-describedby`**.
593. Modal dialogs **trap focus** and are dismissible via Escape.
594. The skip-to-content link is the **first focusable element** on every page.
595. Interactive elements have **minimum touch target size** of 44×44 CSS pixels on mobile.
596. The UI has been tested with **NVDA, VoiceOver, and TalkBack** screen readers.
597. A **language detection** hint on item descriptions helps screen readers switch voice profiles.
598. Pluralization in i18n uses **ICU MessageFormat** rules for correct plural forms across languages.
599. The UI supports **user-overridable font size** via a setting (independent of browser zoom).
600. The documentation itself is available in **multiple languages** (community-contributed, best-effort).

---

This specification delivers a self-organizing, metadata-only cataloging and rediscovery platform that runs on a $5 VPS or a Raspberry Pi, requires near-zero operator involvement after initial setup, leverages every user's curation as fuel for a collaborative filtering engine that naturally pulls each individual toward their precise taste neighborhood, and scales from a personal archive to a mid-sized community without architectural changes.
