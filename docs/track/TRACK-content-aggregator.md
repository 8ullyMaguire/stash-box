# TRACK — the content aggregator programme (NexusXXX parity)

Owner instruction 2026-10-14: *"include all functionality of nexusxxx to use it as a
content aggregator, without losing any of the current functionality … continue
developing stash-box until completion"*, scoped as *"until stash-box is fully
implemented and tested including all new functionality."*

Spec: `docs/spec/feature-content-aggregator.md`. Plan:
`docs/plan/feature-content-aggregator.md`. This table is the state; clause **C8** of
`docs/goal-check.py` reads it, so a row marked `done` with no evidence fails the
predicate, and a phase left undone keeps the goal incomplete.

State values: `not started` | `in progress` | `done`. Evidence must name a commit
or a file that exists.

**Priority (owner instruction 2026-10-14: *"content aggregation and discovery
(algorithms, search, etc) should be the priority"*).** The work order is the
priority order, not the numbering below, and not the dependency order the first cut
of the plan carried:

1. the aggregation spine — **1 → 2**
2. discovery — **4 → 7**
3. breadth and durability — **3 → 9**
4. the supporting features — **5 → 8 → 6 → 11 → 13 → 14 → 10 → 12 → 15 → 16**, with
   **17** threaded through every phase

This table stays ordered by phase number so it can be read against the spec's parity
table and `goal-check.py` clause C8. The reversal and its cost are recorded in
`docs/spec/feature-content-aggregator.md` §0 and §4.

| Phase | Name | State | Evidence |
|---|---|---|---|
| 1 | source layer (`content_sources`, health, GraphQL) | done | migration 108 + `scripts/verify-108.sh` (49/49, mutation-tested); `internal/service/content/source.go`, `ContentSourcesLoader`, `Scene.sources`, `internal/api/scene_sources_integration_test.go` (4 cases, 3 mutations each naming its own failure). Frontend does not consume `sources` yet — that is phase 16, not this phase's surface |
| 2 | ingestion pipeline (URL → content record) | done | migration 109 (`scenes.origin` + `ingest_queue` with the status/outcome machine and a partial unique on the active URL) and 110 (`content_sources.url_normalized` + `ingest_queue.target_entity_id`); `internal/service/ingest/` (`normalize.go`, `dedup.go`, `pipeline.go`), `internal/service/ingest/extract/` (`Extractor`, the production `HTTPExtractor`, `Fake`), the `@every 1m` drain in `internal/cron`, `submitContentUrl(url:, sceneId:)` in `internal/api/resolver_mutation_content.go`; `scripts/verify-109.sh` (46/46, four mutations) + `scripts/verify-110.sh` (17/17) + `internal/api/ingest_integration_test.go` (11 tests, real DB and real HTTP, 4 mutations each reddening only its own). Three corrections the implementation forced are recorded in the plan and the spec: the fetch happens only when a record might be created (a taken-down or already-claimed URL is never re-requested), `gone` is `failed` and a policy refusal is `rejected` rather than both being `failed`, and the pipeline creates the `sites` row for a new host (without one there is no `scene_urls` membership, and a source with no membership appears on no page) |
| 3 | the other five content kinds (`content_items`) | not started | — |
| 4 | tag ontology (implications, versions, RFC) | not started | — |
| 5 | tiers, warnings and gates | not started | — |
| 6 | trust decay, vouching, sub-scores, gamification | not started | — |
| 7 | discovery (language, vectors, taste, serendipity) | not started | — |
| 8 | social (typed comments, forums, playlist extensions) | not started | — |
| 9 | preservation at scale (health daemon, resurrection) | not started | — |
| 10 | media plane (hosting, transcoding, players, PWA) | not started | — |
| 11 | growth (invites, referrals, cohorts) | not started | — |
| 12 | federation extensions (ontology, hash network, AP) | not started | — |
| 13 | API, interoperability, exports | not started | — |
| 14 | moderation and legal (policy text before Phase 10) | not started | — |
| 15 | monetization (operator configuration) | not started | — |
| 16 | UI, metrics, accessibility | not started | — |
| 17 | the predicate grows (a clause per phase) | in progress | `docs/goal-check.py` clause C8 reads this table |

## Environment

The integration suite needs Postgres with `pg_search` and `bktree`. The host's own
`postgres` package has neither and migration 56 fails without them.

```bash
sudo systemctl start docker.socket docker.service
docker run -d --name sbx-pg \
  -e POSTGRES_PASSWORD=smoke_pw -e POSTGRES_USER=postgres \
  -e POSTGRES_DB=stash-box-test -p 127.0.0.1:55434:5432 stashbox-pg18:latest
export POSTGRES_DB='postgres:smoke_pw@127.0.0.1:55434/stash-box-test?sslmode=disable'
```

Proven working 2026-10-14: the above, then
`go test -tags=integration -count=1 -p 1 ./internal/api/ -run 'TestListCreate…'`
→ `ok … 1.835s`, and `./scripts/provision-scratch-db.sh sbx-scratch` →
`migrations: 104 applied`, `version 107`, `tables: 86`, `triggers: 34`.

## Outstanding predicate clauses (measured 2026-10-14)

| Clause | State | Detail |
|---|---|---|
| C1 PRs decided | FAIL | 4/50 undecided: #1282, #1283, #1284, #1285 |
| C3 spec/plan built | FAIL | 6/10 plan documents not Built |
| C6b integration suite | runnable | needs the docker Postgres above; `POSTGRES_DB` is a DSN string, not a URL |
| C7 build clean | PASS | fixed: `make fmt` over the five files, none semantic |
| C8 aggregator programme | FAIL | this table: phases 1-2 done, phase 17 in progress, 14 phases not started |
