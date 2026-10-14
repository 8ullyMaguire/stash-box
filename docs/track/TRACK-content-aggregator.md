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

| Phase | Name | State | Evidence |
|---|---|---|---|
| 1 | source layer (`content_sources`, health, GraphQL) | in progress | schema + reads done: `108_content_sources.{up,down}.sql`, `internal/queries/sql/content_source.sql`, `scripts/verify-108.sh` (49/49, mutation-tested). NOT done: service, GraphQL `Scene.sources`, integration test |
| 2 | ingestion pipeline (URL → content record) | not started | — |
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
| C8 aggregator programme | FAIL | this table: 15 phases not started, 2 in progress |
