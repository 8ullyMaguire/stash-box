# EXTENSIONS — the kernel, the extension host, and what ships on

Owner instruction, 2026-10-31: *"I basically want to make the most featureful and configurable site
possible for each instance admin to choose their own experience, so everything should be a plugin /
extension with the default enabled extensions based on my priorities: maximizing quality content I
would enjoy, pulling users towards my taste with recommendations on every surface, rediscovery of
obscure content, user driven with minimal operator overview."*

That sentence decides the architecture in this document. Read `docs/PREMISE.md` first for what the
product is; this file is how it is assembled.

## 1. What is kernel, and why the list is short

The kernel is what every extension needs in order to exist at all. It is deliberately four things:

| kernel piece | why it cannot be an extension |
|---|---|
| the **claim write path** (propose → vote → apply) | every extension mutates through it; two write paths is two truths |
| the **entity identity and merge chain** (ids, redirects, tombstones) | an id that a plugin can mint is an id nobody can federate |
| the **extension host** (manifest, capability grants, hook dispatch) | the thing that loads extensions cannot be one |
| the **config + migration runner** | an extension that can migrate the database without the runner is privileged code with a nice name |

## 2. The extension contract

An extension is a directory with a manifest and a binary or a Lua entrypoint. Nothing else is
negotiated, and there is no in-process scripting of arbitrary Go.

```yaml
# extensions/gravity-novelty/extension.yml
name: gravity-novelty
version: 1.2.0
provides: [recommendation.ranker]
requires: [core-metadata]
hooks: [recommendation.rank, item.created]
capabilities: [read:items, read:ratings]        # deny-by-default, granted per instance
config_schema: config.schema.json               # rendered as a form in /admin
data: true                                      # gets its own schema-prefixed tables
```

Five rules make this safe enough to run on somebody's archive:

1. **Capabilities are deny-by-default and named**, matching the spec's granularity (`read:items`,
   `write:tags`, `read:users`, …). An extension that asks for `write:*` is asking the operator for trust,
   and the install screen says so in those words.
2. **Extensions never write tables they do not own**, and owned tables are schema-prefixed
   (`ext_<name>_*`) so a disable is a rename, not a data loss. Disabling keeps data; that is the whole
   point of "configurable".
3. **Every hook is a query transformation or a side effect, never a second source of truth.** A ranker
   reorders a candidate set the kernel produced; it cannot invent items. That mirrors the premise's
   "automation may suggest, never decide".
4. **Hooks are ordered and named**, so a composition is debuggable: `recommendation.rank` runs in a
   declared priority order and each extension's contribution can be attributed in the explanation the
   reader sees.
5. **An unhealthy extension degrades, never blocks**: timeouts, a per-extension circuit breaker, and the
   kernel serves the unhooked result with a note in `/admin`, the same way the engine degrades when its
   worker is behind.

## 3. Extension points (the seams that exist)

| seam | what an extension may do | example extensions |
|---|---|---|
| `recommendation.candidates` | add a candidate source to the recall stage | `neighbour-cf`, `item-cf`, `tag-centroid` |
| `recommendation.rank` | reorder / reweight a candidate set | `gravity`, `novelty`, `deep-cut`, `serendipity`, `diversity` |
| `recommendation.explain` | add an explanation variant | `because-you-liked`, `taste-twin` |
| `search.rank` | reorder search results | `intent-boost`, `obscurity-boost` |
| `surface.item` | inject a strip on the item page | `similar`, `more-by-performer`, `rediscovery-rail` |
| `surface.feed` | inject into the chrono feed **after** it | `whats-new` (never reorders — the feed contract) |
| `ingest.adapter` | fetch metadata from an external source | `hydrus`, `iafd`, `studio-scraper` |
| `ingest.dedupe` | add a comparison algorithm | `phash-threshold`, `title-shingle` |
| `governance.threshold` | set quorum / trust rungs | `trust-thresholds`, `jury-sampling` |
| `moderation.triage` | pre-sort the report queue | `spam-heuristics`, `sockpuppet` |
| `notify.channel` | add a delivery channel | `telegram`, `ntfy`, `matrix`, `rss` |
| `theme` | ship CSS/JS variants | `dense-dark`, `reader-mode` |
| `admin.panel` | add a dashboard panel | `engine-health`, `tag-velocity` |
| `job.schedule` | register a cron job | `nightly-refit`, `link-probe`, `integrity-check` |

## 4. The default profile ("taste-first")

The defaults are not neutral, because a neutral default is a product decision made by accident. Each
extension below ships **enabled** on a fresh instance, because it serves one of the four stated
priorities:

| extension | priority served | why it is on by default |
|---|---|---|
| `core-metadata` — Core item metadata | quality | Every surface ranks metadata; an item without a canonical record cannot be recommended. |
| `core-tags` — Tag ontology and consensus | quality | Tags are the content vector the recommender reads; consensus keeps the vocabulary honest. |
| `identity` — Identity and trust | user-driven | Trust rungs decide who may vote, so governance is only as good as identity. |
| `ratings` — Ratings, seen and favourites | quality | The explicit signal the recommender learns from; without it Gravity is a popularity page. |
| `gravity` — Gravity recommendation engine | quality | The owner's first priority: pull every reader toward the works they would actually enjoy. |
| `rediscovery` — Obscurity and rediscovery surfaces | obscurity | Novelty boost, deep cuts, hidden gems: the second priority, and the one a popularity ranking erases. |
| `surface-injection` — Recommendations on every surface | quality | The owner asked for recommendations everywhere; a rec page nobody visits is a shelf in a cellar. |
| `search` — Search and discovery | quality | The intent signal, and the fallback when the recommender is cold. |
| `lists` — Lists and collections | user-driven | Curation labour is the platform's fuel; lists are where readers do it. |
| `social` — Social layer | user-driven | Follows and comments create the neighbourhood the recommender needs; kept chronological by contract. |
| `governance` — Quorum governance | user-driven | The owner's third priority: user-driven with minimal operator overview. |
| `safety` — Safety, policy and abuse | minimal-operator | The only surface that earns its keep by needing the operator least: automate the boring report triage. |
| `ops` — Operations and deployability | minimal-operator | An instance nobody has to babysit is the premise of minimal operator overview. |
| `admin` — Admin and observability | minimal-operator | Read-mostly telemetry, so the operator can see health without administering anything. |
| `integrity` — Data integrity and portability | minimal-operator | Backups, exports and merge chains are the difference between an archive and a bet. |
| `plugin-host` — Extension host | minimal-operator | The owner's structural request: every surface is an extension, so the host is not optional. |
| `ux-density` — Dense reading UI | quality | Density is a taste decision that favours browsing; the reader should need one screen per decision. |
| `i18n-a11y` — Internationalisation and accessibility | quality | An archive that cannot be read by a screen reader has quietly excluded a share of its curators. |
| `docs-kit` — Documentation and onboarding | minimal-operator | Every question answered by docs is a question the operator does not answer twice. |
| `ingest` — Ingest and source adapters | quality | An archive grows by ingesting other archives, so every ranking downstream is only as good as what arrived. |
| `moderation` — Moderation queue and versioned edits | minimal-operator | The one queue that must not depend on the operator being awake; versioned edits are what make a revert possible. |
| `preservation` — Preservation and mirroring | minimal-operator | The premise promises preservation by default, and a mirror nobody checks is a claim rather than a copy. |
| `api-surface` — Query surface | integration | Every client reads one contract; a second surface is how contracts start to disagree. |

And these ship **disabled**, each with the reason recorded, so an operator turning one on knows what
they are buying:

| extension | why it is off |
|---|---|
| `forums` — Forums and groups | Off by default: a forum with no traffic is a worse first impression than none, and the social layer already carries discussion. |
| `games` — Games and gamification | Off by default: points and badges compete with the thing the reader came for, and can be re-enabled per instance. |
| `editorial` — Editorial pinning | Off by default: the owner asked for user-driven with minimal operator overview, and a pin outranks every user signal. |
| `rest-facade` — REST facade | Off by default: the contract is GraphQL; a REST surface is a second contract that can drift from the first. |
| `sqlite-backend` — SQLite backend | Off by default: this fork is PostgreSQL-first and its migrations, LISTEN/NOTIFY and JSONB are Postgres. A SQLite mode is a port, not a flag. |
| `ssr-frontend` — Server-rendered no-JS frontend | Off by default: the fork ships a React client; a no-JS reader is a second frontend, not a setting. |
| `governance-override` — Covert moderation | Shadow-limiting and impersonation are shipped only as audited, logged tools, never as silent defaults. |

Two defaults deserve their own paragraph, because they are where this document argues with the spec:

- **Recommendations on every surface is an injection, not a page.** A `/recommendations` page is a
  destination nobody visits twice; the priority is served by injecting a ranked strip into every place a
  reader already is (item page, search results, tag page, profile, feed *after* the chronological block).
  That is what `surface-injection` means, and it is why `surface.item` and `search.rank` are seams.
- **Obscurity is a first-class ranker, not a filter.** "Rediscovery of obscure content" is served by a
  novelty/deep-cut ranker that is *enabled*, not by a checkbox that demotes popular work. The measurable
  target is the share of surfaces showing at least one item in the bottom popularity quartile.

## 5. Per-instance configuration

The operator picks a profile, then edits it. Nothing in a profile is special-cased in code:


```yaml
profile: taste-first          # taste-first | scholarly | minimal | custom
extensions:
  gravity: {enabled: true, ranker_weights: {affinity: 0.5, cf: 0.3, novelty: 0.2}}
  rediscovery: {enabled: true, deep_cut_share: 0.1}
  games: {enabled: false}
surface_injection:
  item_page: [similar, rediscovery-rail, more-by-performer]
  search: [intent-boost, obscurity-boost]
  feed: []                    # the chrono feed stays chrono; injections land below it
governance:
  tag_confirmation: {votes: 3, window: 72h}
  edit_quorum: {votes: 2, trust_gate: 1}
```

`docs/EXTENSIONS.md` §4 is the default; `docs/ROADMAP-extensions.md` is the order the seams get built in.
The Concord project carries the per-feature rows, so an admin reading one row can see the extension it
belongs to and whether their profile has it on.

