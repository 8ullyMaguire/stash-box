# SPEC — suki capability addendum (2026-10-31)

Source of record: `~/code/rust/suki/docs/FEATURES.csv` — 173 rows, columns area/feature/description/
where, each naming the file or route the mechanism lives at. Suki is a self-hosted image board for fan
art (booru ingest, tag taxonomy, pools, taste ranking, removals, ops); this fork is a metadata archive
for adult video. Same family, so the mechanisms transfer — but far fewer do than transferred to darkhold,
because this fork already has the machinery: edit voting, facets, saved searches, appeals, fingerprint
clusters, Elo and lint quests.

## Verdicts

**76 fit**, 95 present, 2 conflict — of 173.

The rule is the same one the Rediscovery intake used, and it is deliberately biased: `present` only where
a named GraphQL type or repo doc implements it (the evidence column names it), `conflict` only where a
premise commitment or the stack is inverted, and **everything else is `fit`** — an idea row, not a claim
that the work is done. A duplicate idea is visible and cheap; a false "already built" stops work.

What that splits into is the interesting part: suki's **ingest** half is mostly absent here (import runs,
backfill cursors, refusal backoff, yield reporting, source confidence), while its **catalogue, community,
moderation and API** halves are mostly already built. In other words this is not a feature gap in the
product — it is a gap in the *ingest* pipeline, which is exactly the extension seam the architecture
already reserves (`ingest.adapter`).

## Extension map

Every fitted feature is bound to the extension that would ship it, using the catalogue in
`docs/EXTENSIONS.md`. Three extensions were added to that catalogue while writing this addendum, because
suki's features had nowhere honest to sit: `ingest`, `moderation`, `preservation` (and `api-surface`,
for the query contract). All four ship enabled — they serve ingestion, the operator's queue, the
preservation promise and the one-contract rule.

## The table (173 rows)


### ingest · Ingest and provenance (26 features) — 18 fit / 8 present / 0 conflict

| feature | suki description | verdict | extension | evidence / reason |
|---|---|---|---|---|
| **per-booru importer** (`import --source`) | reads a source's API and writes pictures | present | `ingest` | ContentSource + IngestSubmission |
| **xml and json api kinds** (`src/services/import/`) | one fetcher per api shape | fit | `ingest` | one adapter per API shape, selected by source config |
| **tag canonicalisation** (`services/import/`) | lowercase underscore names that boorus agree on | present | `core-tags` | tag normalisation in the edit write path |
| **html entity decoding** (`html_escape::decode_html_entities`) | an accented title no longer kills a page | fit | `ingest` |  |
| **source tag map** (`source_tag_map`) | per-source rename applied before global aliases | fit | `ingest` | per-source rename applied before the global alias table |
| **tag blocklist** (`blocked_tag`) | refused tags never enter the corpus | present | `ingest` | per-source blocked tags on ContentSource |
| **takedown hash ban** (`takedown`) | known hashes are skipped and tombstones kept | fit | `preservation` | a removed work cannot return on the next crawl |
| **md5 dedupe** (`duplicate_of`) | one picture from many sources stays one row | present | `ingest` | Fingerprint + QueryExistingScene duplicate check |
| **parent claim** (`parent_external`) | keeps the source's parent id and links later | fit | `core-metadata` | resolve the source parent link later |
| **source pools** (`pools::ensure_mirror`) | mirrored ordered sequences | fit | `ingest` | mirrored ordered sequences |
| **source notes** (`note.origin = 1`) | pixel boxes converted to fractions | fit | `ingest` |  |
| **publication dates** (`published_at`) | posted_at with a precision and a merge rule | present | `core-metadata` | FuzzyDate: precision is explicit |
| **source confidence** (`source.confidence`) | which source wins a disagreement | fit | `ingest` | which source wins a disagreement |
| **quality and ai flag** (`image.quality`) | stored at import and filtered everywhere | fit | `ingest` | flag stored at import, filtered everywhere |
| **review queue** (`SUKI_MODERATION_MODE`) | pending_review until somebody approves | present | `moderation` | DraftSubmissionStatus |
| **approve pending** (`import --approve-pending`) | releases withheld pictures to the catalogue | present | `moderation` | draft review and approval |
| **import runs** (`import_run`) | one row per invocation with its counters | fit | `ingest` | one row per invocation with its counters |
| **yield per source** (`/moderation/imports`) | explained and pinned share over 30 days | fit | `ingest` |  |
| **backfill cursor** (`source.backfill_page`) | pages walked persist and wrap at an empty page | fit | `ingest` | walk position persists per source |
| **refusal backoff** (`backfill::back_off`) | a 401/403/429 parks the source instead of retrying | fit | `ingest` | a 401/403/429 parks the source instead of retrying |
| **scheduler** (`scripts/import-due.sh + suki-import.timer`) | timer runs whatever is due | fit | `ingest` | whatever is due runs on a timer |
| **submission import** (`import --from-submissions`) | drains what moderators accepted | present | `ingest` | IngestSubmission accepted becomes an edit |
| **discovery** (`suki-discover.timer`) | day suggests tags worth crawling | fit | `ingest` | propose tags worth crawling from what readers engage with |
| **gravity pull** (`suki-gravity.timer`) | queries a booru for what the profile wants | fit | `gravity` | crawl targeted at the instance taste profile |
| **mirror sync** (`suki-pools.timer`) | weekly pool and note refresh | fit | `preservation` |  |
| **favourites import** (`suki-favourites.sh`) | seeds the profile from a booru account | fit | `gravity` | seed a profile from an external account |

### catalogue · Catalogue and discovery (25 features) — 6 fit / 19 present / 0 conflict

| feature | suki description | verdict | extension | evidence / reason |
|---|---|---|---|---|
| **grid** (`/`) | server-rendered tiles with a cursor listing | present | `surface-injection` | scene grid |
| **infinite scroll** (`/images/partial`) | fragment that appends the next page | present | `ux-density` |  |
| **image page** (`/image/{source}/{external_id}`) | one picture with everything attached | present | `surface-injection` | Scene page |
| **variants** (`/i/{source}/{external_id}/{variant}`) | thumb sample and full from the cache | present | `core-metadata` | Image, ImageTypeGroup |
| **random** (`/random`) | picks a visible picture | present | `rediscovery` | random scene |
| **prev and next** (`image page`) | neighbours by id on the picture page | fit | `surface-injection` |  |
| **tag page** (`/tag/{name}`) | count sparkline facets and the grid | present | `search` |  |
| **related tags** (`/tag/{name}`) | co-occurrence cloud on the tag page | present | `search` | TagFacets co-occurrence |
| **series neighbours** (`tag page`) | series that share characters | fit | `surface-injection` |  |
| **artist page** (`/artist/{name}`) | one artist's work in publication order | present | `core-metadata` | Performer page |
| **artist sort** (`/artist/{name}?sort=`) | chronological popular or new | present | `search` |  |
| **aliases on artist pages** (`services/artist.rs`) | an alias answers 301 | present | `core-metadata` | performer alias resolution |
| **tag index** (`/tags`) | every tag by weight with a sparkline | present | `search` |  |
| **tag browser** (`/tags/browse`) | filter-as-you-type over the taxonomy | present | `search` |  |
| **artist index** (`/artists`) | every artist tag | present | `search` |  |
| **pools index** (`/pools`) | ordered sequences as cards | present | `lists` | List index |
| **pool page** (`/pool/{slug}`) | one sequence in its own order | present | `lists` |  |
| **pool reader** (`/pool/{slug}/read/{n}`) | one page at a time | fit | `lists` | one item at a time, in the collection order |
| **best of the week** (`/best`) | what the sources ranked highest | fit | `rediscovery` |  |
| **front page** (`/`) | the taste-ordered catalogue | present | `surface-injection` |  |
| **for-you redirect** (`/for-you`) | points at the front page | present | `gravity` | For You route |
| **similar pictures** (`services/similar.rs`) | tag rarity decides the strip | present | `gravity` | SimilarScene |
| **neighbour interest** (`services/personal_taste.rs`) | what similar people kept | present | `gravity` | AffinityContribution + scene_for_you |
| **viewer keys** (`assets/suki.js`) | f x arrows zoom fullscreen from the keyboard | fit | `ux-density` |  |
| **copy tags and share** (`assets/suki.js`) | two buttons on the picture page | fit | `ux-density` |  |

### search · Search (9 features) — 3 fit / 6 present / 0 conflict

| feature | suki description | verdict | extension | evidence / reason |
|---|---|---|---|---|
| **query language** (`services/search.rs`) | fields negation ranges wildcards | present | `search` |  |
| **search page** (`/search`) | the box and the results | present | `search` |  |
| **implications** (`implications`) | an added tag carries its implied tags | present | `core-tags` | TagImplication |
| **term and union caps** (`MAX_TERMS`) | 24 terms and a 32-wide union | fit | `search` | stated caps keep a crafted query from a scan |
| **saved searches** (`/account/searches`) | a query kept and re-run | present | `search` | SavedSearch |
| **watched searches** (`/account/watches`) | a saved search that mails and feeds | present | `search` | SavedSearchMatch + notifications |
| **query suffix** (`/account/query-suffix`) | a filter appended to every search | fit | `search` | per-account exclusions applied to every search |
| **did you mean** (`probably_meant`) | one-word trigram correction | present | `search` | SearchDiagnostic |
| **seen field** (`is_query_field`) | seen:false is a first-class term | fit | `search` | seen:false as a composable term |

### personal · Reader state and account (27 features) — 16 fit / 11 present / 0 conflict

| feature | suki description | verdict | extension | evidence / reason |
|---|---|---|---|---|
| **account page** (`/account`) | everything about the reader | present | `identity` |  |
| **preferences** (`/account/preferences`) | per-page sort theme and ai choices | present | `identity` |  |
| **guest settings import** (`/account/import`) | localStorage filters become rows | fit | `identity` | local filters survive signup |
| **blacklist modes** (`user_filter.mode`) | hide or blur by tag | fit | `safety` | hide or blur, two different promises |
| **favourites list** (`/favorites`) | kept pictures with export | present | `ratings` | FavoritePerformerScene, FavoriteStudioScene |
| **favourite toggle** (`/favorites/{id}`) | the star writes a row | present | `ratings` |  |
| **seen and dwell** (`user_seen`) | every look recorded with milliseconds | fit | `ratings` | dwell time as the honest quality signal |
| **forget my history** (`/account/seen/forget`) | deletes the seen rows | fit | `identity` |  |
| **skip** (`/skip/{id}`) | explicit not-interested | fit | `ratings` | explicit not-interested, stored apart from a hide |
| **came** (`/came/{id}`) | one private day a day | fit | `ratings` | private daily marker; a lighter signal than a rating |
| **pin** (`image.pinned`) | curation that outranks everything | fit | `editorial` | curation that outranks the ranking |
| **notes as a member** (`/image/{source}/{external_id}/notes`) | rectangles of commentary | fit | `social` | passage-level commentary, privacy per note |
| **pool building** (`/pool/{id}/update`) | create add reorder remove | present | `lists` | List, ListItem |
| **submissions** (`/submit`) | send a source id in | present | `ingest` | IngestSubmission |
| **dmail** (`/dmail`) | private two-sided messages | fit | `social` | two-sided private messages |
| **api keys** (`/account/api-keys`) | named tokens with revoke | present | `identity` |  |
| **sessions** (`/account/sessions`) | list and revoke signed-in devices | present | `identity` | SessionState |
| **notifications** (`/account/notifications`) | inbox with read state | present | `social` | Notification |
| **export** (`/account/export.json`) | every row about you as stored | fit | `integrity` | every row about you as stored |
| **delete account** (`/account/delete`) | erasure in one transaction | fit | `identity` | erasure in one transaction in FK order |
| **public profile toggle** (`/account/public`) | whether strangers see the profile | fit | `identity` |  |
| **theme and density** (`assets/suki.js + cookie`) | dark or light and a dense grid; remembered | present | `ux-density` |  |
| **guest favourites** (`frontend/src/personal.js`) | kept in localStorage until signed in | fit | `identity` | state for readers without an account |
| **guest blacklist** (`suki.filters`) | filters applied in the browser on every tile | fit | `safety` |  |
| **password reset** (`/reset/{token}`) | a mailed link that is stored as a hash | present | `identity` |  |
| **confirmation link** (`/confirm`) | proves an address and grants trust 1 | fit | `governance` | verification is the event that grants trust 1 |
| **link to a parent** (`/image/{source}/{external_id}/links`) | claim that a picture is a version | fit | `core-metadata` | a reader proposes a version relationship |

### community · Community (14 features) — 5 fit / 9 present / 0 conflict

| feature | suki description | verdict | extension | evidence / reason |
|---|---|---|---|---|
| **profile** (`/u/{name}`) | a view assembled by name | present | `social` |  |
| **comments on pictures** (`comment`) | threaded with reactions | present | `social` | Comment |
| **comments index** (`/comments`) | site-wide newest first | fit | `social` | site-wide newest first |
| **votes** (`/image/{source}/{external_id}/vote`) | up and down as rows with undo | present | `governance` | EditVote |
| **forum** (`/forum`) | boards threads and replies | fit | `forums` |  |
| **forum gate** (`/forum`) | hides itself until there is a conversation | fit | `forums` | hide it until there is a conversation |
| **wiki** (`/wiki/{name}`) | tag commentary with history | fit | `core-tags` | tag commentary with history |
| **tag verification** (`/verify`) | three confirms and a margin | present | `governance` | OntologyFieldDecision + vote thresholds |
| **quests** (`/quests`) | counts plus a link never a second queue | present | `core-tags` | LintQuestDefinition, LintCandidate |
| **arena** (`/arena`) | one picture against another | present | `games` | EloMatchup |
| **leaderboard** (`/leaderboard`) | sum of points in a window | present | `games` | LeaderboardEntry |
| **points ledger** (`point_event`) | every award a row with its factors | fit | `governance` | every award a row with its factors |
| **streaks** (`streak_tier`) | consecutive days and a multiplier | present | `games` | Streak, ContributionDay |
| **invites** (`invite`) | codes that grant a trust rung | present | `identity` | InviteKey, InviteAllowance |

### ranking · Ranking and taste (16 features) — 9 fit / 7 present / 0 conflict

| feature | suki description | verdict | extension | evidence / reason |
|---|---|---|---|---|
| **operator profile** (`operator_taste`) | weights per tag and author | present | `gravity` | instance taste weights |
| **signal weights** (`taste_signal`) | pin favourite arena came per role | fit | `gravity` | per-role weights as data |
| **taste score** (`image.taste_score`) | one number every listing sorts by | present | `gravity` |  |
| **for-you order** (`Sort::ForYou`) | score then source score then id | present | `gravity` |  |
| **personal profile** (`user_tag_affinity`) | per-account weights | present | `gravity` | AffinityContribution |
| **facets** (`services/facets.rs`) | co-occurrence weighting for related tags | present | `search` | TagFacets |
| **coverage** (`/taste`) | north star instrument over visible pictures | fit | `admin` | share of the corpus carrying each taste dimension |
| **coverage by kind** (`/taste`) | the same split by tag kind | fit | `admin` |  |
| **corpus share** (`/taste`) | how much of the collection carries a kind | fit | `admin` |  |
| **profile owner** (`ranking.operator_user_id`) | whose taste the rankings serve | fit | `gravity` | whose taste the ranking serves is a recorded field |
| **affinity weighting** (`ranking.affinity_blend`) | a neighbour counts by agreement | present | `gravity` |  |
| **neighbour cap** (`ranking.neighbour_cap`) | strangers never outweigh the owner | fit | `gravity` | strangers never outweigh the owner |
| **came gate** (`ranking.came_neighbours`) | a rank needs 3 accounts behind it | fit | `governance` | a rank needs N independent accounts behind it |
| **provenance line** (`/taste`) | how much of the profile is somebody else | fit | `surface-injection` | how much of the profile is someone else |
| **taste page** (`/taste`) | inspectable weights with reasons | present | `gravity` |  |
| **lately** (`/taste`) | what the operator came to by kind | fit | `gravity` |  |

### moderation · Moderation (24 features) — 5 fit / 19 present / 0 conflict

| feature | suki description | verdict | extension | evidence / reason |
|---|---|---|---|---|
| **reports queue** (`/moderation/reports`) | decide about reported pictures | present | `moderation` |  |
| **report form** (`/report/{id}`) | any account may report | present | `moderation` | ContentSourceAppeal + report flow |
| **submissions queue** (`/moderation/submissions`) | accept or decline | present | `moderation` |  |
| **bulk actions** (`/moderation/bulk`) | one query capped at 500 | present | `moderation` | bulk edit submission |
| **bin** (`/moderation/trash`) | soft delete with restore | fit | `moderation` | soft delete with restore |
| **what changed** (`/moderation/changes`) | append-only snapshot of every edit | present | `moderation` | EntityChange |
| **revert** (`/moderation/changes/{id}/revert`) | writes a new row never an edit | present | `moderation` | revert writes a new edit |
| **duplicates** (`/moderation/duplicates`) | fold a copy into its keeper | present | `moderation` | QueryExistingScene, FingerprintCluster |
| **merge** (`services/merge.rs`) | a folded copy answers 308 | present | `moderation` | merge edits |
| **tag aliases** (`/moderation/tags`) | merge tags and approve aliases | present | `core-tags` | tag merge edits |
| **alias suggestions** (`/moderation/aliases`) | proposals from co-occurrence | fit | `core-tags` | proposals from co-occurrence |
| **implications queue** (`/moderation/implications`) | review implied tags | present | `core-tags` | OntologyFieldDecision |
| **permissions** (`/moderation/permissions`) | registry of who may do what | present | `identity` | role registry |
| **role grants** (`role_grant`) | per-user permission grants | present | `identity` |  |
| **settings registry** (`/moderation/settings`) | every knob with its default | fit | `admin` | every knob with its default and validation |
| **untagged list** (`/moderation/untagged`) | pictures with no tags | present | `moderation` | LintCandidate |
| **imports page** (`/moderation/imports`) | runs refusals and yield | present | `ingest` | ContentSource admin |
| **sources health** (`/sources`) | is each schedule alive | fit | `ingest` |  |
| **storage page** (`/moderation/storage`) | budgets and what would be evicted | fit | `admin` |  |
| **preservation list** (`/moderation/preservation`) | pictures that may never be evicted | present | `preservation` | Snapshot, ArchiveEntityCount |
| **removals** (`/moderation/removals`) | takedown requests and decisions | present | `safety` | ContentSourceAppeal, ContentTakedownResult |
| **modlog** (`/moderation/log`) | every moderator action | present | `moderation` | ModAudit |
| **signups** (`/moderation/signups`) | vouch an account past the mail problem | present | `identity` | Vouch |
| **source rules** (`source`) | per-source blocklists and schedules | present | `ingest` | ContentSource config |

### ops · Operations (16 features) — 10 fit / 6 present / 0 conflict

| feature | suki description | verdict | extension | evidence / reason |
|---|---|---|---|---|
| **deploy script** (`deploy_thinkcentre.sh`) | binary nginx and cloudflare in one run | present | `ops` | docs/deploy + Makefile |
| **layout sweep** (`scripts/site-audit`) | browser gate over every route | fit | `ops` | browser gate over every route |
| **backup** (`scripts/suki-backup.sh`) | daily dump with a restore drill | present | `ops` | deploy docs + Makefile targets |
| **restore drill** (`scripts/suki-restore-drill.sh`) | proves a dump restores | fit | `preservation` | a scheduled restore that asserts content |
| **retention sweep** (`bin/maintain.rs`) | sessions tokens seen rows past their window | fit | `integrity` | sessions, tokens and seen rows past their window |
| **storage eviction** (`services/storage.rs`) | two budgets newest first inside each | fit | `preservation` | two budgets, newest first, preservation list excluded |
| **health** (`/healthz`) | unauthenticated liveness | present | `ops` | health endpoint |
| **feeds** (`/feed.xml`) | site tag pool and watch atom | fit | `ops` |  |
| **sitemap** (`/sitemap.xml`) | safe ratings only | fit | `ops` | safe ratings only |
| **robots** (`/robots.txt`) | explicit and questionable disallowed | present | `ops` |  |
| **opensearch** (`/opensearch.xml`) | browser search box entry | fit | `ops` |  |
| **asset embedding** (`rust-embed`) | css and js compiled into the binary | present | `ops` | embedded frontend |
| **watches notifier** (`suki-notify.timer`) | mails and writes feeds for what changed | present | `search` | saved-search notification job |
| **source health check** (`suki-sources-check.timer`) | asks every source whether it answers | fit | `ingest` |  |
| **telegram bot** (`src/bin/telegram.rs`) | a loop around the library with a token | fit | `ops` |  |
| **discord bot** (`/discord/interactions`) | slash commands through an endpoint | fit | `ops` |  |

### api · API (8 features) — 0 fit / 7 present / 1 conflict

| feature | suki description | verdict | extension | evidence / reason |
|---|---|---|---|---|
| **images list** (`/api/v1/images`) | cursor-paged with every filter | present | `api-surface` | scene list query |
| **image by id** (`/api/v1/images/{source}/{external_id}`) | one picture by source and id | present | `api-surface` |  |
| **catalogue indexes** (`/api/v1/tags`) | tags characters series artists pools | present | `api-surface` | tag, performer, studio queries |
| **stats** (`/api/v1/stats`) | totals coverage and coverage by kind | present | `api-surface` | InstanceMetric |
| **authors** (`/api/v1/authors`) | image authors by taste | present | `api-surface` | performer queries |
| **arena endpoints** (`/api/v1/arena/pair`) | pair and match | present | `api-surface` | Elo queries |
| **auth endpoints** (`/api/v1/me`) | register login me invites | present | `api-surface` |  |
| **openapi** (`/api/openapi.json`) | generated description of the API | conflict | `api-surface` | The GraphQL schema is this fork's generated contract. An OpenAPI document describes a REST surface already rejected as core, so it would document the second API, not this one. |

### safety · Safety and privacy (8 features) — 4 fit / 3 present / 1 conflict

| feature | suki description | verdict | extension | evidence / reason |
|---|---|---|---|---|
| **visibility predicates** (`push_filters`) | four filters every listing applies | present | `safety` | AccessRule + Filter in the query layer |
| **privacy export** (`services/privacy.rs`) | one section per table | fit | `integrity` | generated from the table list, one section per table |
| **privacy erase** (`services/privacy.rs`) | one transaction in foreign key order | fit | `integrity` |  |
| **retention windows** (`/privacy`) | stated on the page and enforced by a job | fit | `integrity` | stated on the page and enforced by a job |
| **ai hiding** (`image.ai_generated`) | ai pictures never shown but kept | fit | `safety` | hidden but kept: a visibility decision, not a data loss |
| **rate limiting** (``) | per-client limits in front of the app | present | `safety` | per-client limits |
| **schema only** (`payout`) | subscription and payout tables nothing writes | conflict | `integrity` | Not a feature: suki records that its own subscription and payout tables have no writer. Copying a disclosed defect as a feature would launder it. |
| **rate limit middleware** (`rate_limit::enforce`) | per-client limit before any handler runs | present | `safety` | limits enforced before the handler |

## What is worth taking first

Three mechanisms are worth more than the other ninety, in this order:

1. **Import runs with counters and per-source yield** — without a row per invocation, "ingestion is slow"
   and "ingestion is broken" are indistinguishable from outside, and a source that silently stopped
   producing looks the same as one that had nothing new.
2. **Backfill cursor + refusal backoff** — the two mechanisms that make a crawl resumable and polite. A
   crash costing one page instead of a crawl is the difference between an ingest pipeline and an incident.
3. **Takedown hash ban with tombstones** — in an archive whose promise is preservation, a takedown that the
   next crawl silently undoes is worse than no takedown at all.

Everything else in the fitted set is either small (a page, a button, a config field) or belongs to an
extension that is off by default (`editorial` pinning, `forums`).

## What was excluded, and why

* **`openapi` (api)** — the GraphQL schema is the contract here. An OpenAPI document would describe the
  REST surface this fork already rejected as core, so shipping it would document the wrong API.
* **`schema only` (safety)** — suki's own disclosure that its subscription and payout tables have no
  writer. That is a defect note, not a feature; copying it would launder a known gap into a spec item.
* **Already present, not re-filed**: 88 features, each with the type that implements it in the table.

