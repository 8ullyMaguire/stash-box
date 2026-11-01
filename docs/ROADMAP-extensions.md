# ROADMAP — extensions and the taste-first profile

Ordered work for `docs/EXTENSIONS.md`. Sequencing rule from `docs/PREMISE.md`: a precondition that
makes later work honest outranks a feature that makes it bigger, and defects outrank both.

## P0 — the host (nothing else is composable without it)

1. `plugin-host`: manifest parsing, capability grants, deny-by-default install screen, hook registry with
   declared ordering, per-extension circuit breaker, `ext_<name>_*` schema-prefixed tables, disable-without-
   data-loss. Ship with **one** extension to prove the seam end to end (`notify.channel: rss`), not with ten.
2. `admin.panel`: extension list with health, capability grants, config forms rendered from
   `config_schema`, and an explain view for a single item's recommendation (which extensions touched it).
3. Hook attribution in the API: every recommendation response names the extensions that contributed, so
   "explainable" survives composition.

## P1 — turn the four priorities into seams

4. `recommendation.rank` seam + refit `gravity` into it as the first real extension (the fork's existing
   scorer becomes an extension, not a special case in the resolver).
5. `rediscovery` ranker: novelty boost, deep cut, bottom-quartile share as a measurable target.
6. `surface-injection`: item page, search results, tag page, profile; feed injection strictly below the
   chronological block.
7. `governance.threshold`: quorum, trust rungs and window as extension config, not constants.
8. `moderation.triage`: spam heuristics and report pre-sort, so the operator's queue shrinks without the
   operator.

## P2 — the intake's genuinely new ground

9. Ratings (`ratings` extension): explicit 1–10 with private-by-default, Bayesian display, per-user stats.
10. Taste profiles (multiple per account) and `identity` privacy levels — the mechanism that lets one
    person curate two tastes without poisoning either vector.
11. `search.rank` extension + typo tolerance + per-user saved-search feeds.
12. `ingest.adapter` as an extension surface, replacing the hardcoded source list.

## P3 — off-by-default extensions, built only when an instance asks

13. `forums`, `games`, `editorial`, `rest-facade`, `sqlite-backend`, `ssr-frontend` — each is a real
    project, and each conflicts with something in the premise or the stack. Build on request, never by
    default, and never as a silent toggle that changes the product's character.

## Deliberately not ordered

The intake's commodity-hardware section (K) and its SQLite/SSR/no-JS subsections conflict with this
fork's stack; a second deployment shape is a separate product decision, not a backlog item. Everything in
the intake that is already `present` needs tuning, not building, and tuning belongs to the extension it
lives in.
