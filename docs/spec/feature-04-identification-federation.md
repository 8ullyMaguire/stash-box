# SPEC — D2: the identification board federates

Implements **SPEC §7.23.1 row D2**: *"Identification board federates. A query
broadcasts to peers with matching taste vectors. Metadata plane only
(§7.17.1). Content never broadcasts."*

Status: **spec written 2026-09-30, nothing built yet.** This document is the
"what and why"; `docs/plan/feature-04-identification-federation.md` is the
"how and what proves it".

---

## 1. Why this row is next

The 03b plan's definition of done requires "SPEC §7.23 rows D1–D8 each either
implemented or explicitly deferred with a reason." Checked against the tree on
2026-09-30:

| Row | Subject | State |
|---|---|---|
| D1 | Vanguard trust-weighted voting | **built** — `elo.RecordEloVote` stores the weight, migration 85 |
| D2 | Identification board federates | **not built** — this spec |
| D3 | Gravity slider formula | **built** — `internal/service/elo/gravity.go`, four-factor product |
| D4 | Access gate is a conjunction | **built** — SPEC §7.23.3 W1 |
| D5 | Per-entity denylist after the gate | **built** — migrations 87, `trust` service |
| D6 | Guilds, mentorship, adoption, roadmap | **not built** — deferred, see §6 |
| D7 | Mobile app, browser extension | **spec-level** — already in §7.11, not a code row |
| D8 | Sync cadence, air-gap bundles | **spec-level** — already in §7.17.4 |

D2 is chosen over D6 because its prerequisites are already in the tree and D6's
are not: `taste_vectors` (migration 77) with a stored `vote_count`, and a
complete identification board (`internal/service/identification`, migration 79)
with queries, candidates, and votes. D6 would need a guild model, a mentorship
state machine, and an adoption-trust model — none of which exist, and none of
which have a spec section.

## 2. What exists, verified by search on 2026-09-30

- `internal/service/identification/service.go` — `Post`, `Get`, `ListOpen`,
  `Suggest`, `GetCandidate`, `Vote`, `Unvote`, `HasVoted`, `ListCandidates`,
  `Resolve`, `Abandon`, `WithTxn`. Local only: no network calls, no peer
  concept. Two of these matter for the design below:
  - `ListCandidates(queryID)` is the **local** candidate list, and is the
    boundary F2 protects — foreign evidence is never returned by it.
  - `Resolve(queryID, type, id, by, recordTrust)` is the one function that turns
    a query into a canonical link. It takes a `recordTrust` callback, which is
    where a resolved identification feeds trust; federation must not be able to
    reach this path.
- Tables `identification_queries`, `identification_candidates`,
  `identification_candidate_votes` (migration 79).
- `taste_vectors(user_id, vector jsonb, vote_count int, updated_at)` (migration
  77), with the documented invariant that **a user with no votes has no row**.
- The service's own governing rule, quoted from its package doc: *"a vote is
  EVIDENCE, not authority … A plurality vote is not a creation."*

## 3. What this builds

A peer registry, a taste-based peer selector, and a query broadcast that carries
**questions and candidate evidence only**.

Three pieces, in dependency order:

1. **Peer registry** — an instance this node knows about: its base URL, an
   attestation, a trust weight, and a last-seen timestamp. Operator-configured;
   no discovery protocol (see §5).
2. **Peer selection** — rank known peers by taste-vector similarity to the
   asking user, above a minimum sample size, and take the top N.
3. **Broadcast** — send an open query and its local candidates to selected
   peers; record their replies as *foreign candidate evidence* on the local
   query, attributed to the peer and signed by its attestation.

## 4. The decisions, and what each rules out

**F1 — Federation carries questions and candidates. Never content.** D2's own
note and §7.17.1 both say this. A snapshot image is content; a description of
what someone is looking for is not. The broadcast payload is a closed struct with
no field capable of holding media, so "content never broadcasts" is enforced by
the type rather than by a code review.

**F2 — A foreign candidate is evidence, never a vote, and never a local row.**
This is the existing service's rule applied across the boundary. A peer's
suggestion lands in its own table and is shown with the peer's name and
attestation attached. It is not inserted into `identification_candidates`, so it
cannot be voted on locally, cannot win a plurality, and cannot become a
canonical link. The alternative — merging foreign candidates into the local
table — would let any peer inject a candidate that the local community then
votes on, which is the exact failure the package doc warns about, arriving over
the network.

**F3 — A peer's answer is scoped to one query and expires.** Foreign evidence is
attached to the query it answered, never to the entity. Re-querying a peer for
a different query is a fresh request, and a peer that has not been seen inside
the staleness window is not asked. Otherwise a peer's stale opinion accumulates
on a query nobody re-checked.

**F4 — Taste matching uses `vote_count` as a floor, and cosine over the
vector's keys.** A vector built from three votes is not evidence of a
preference. This is the same reasoning the migration's own comment records, and
ignoring `vote_count` would let a single enthusiastic voter define a peer's
entire relevance.

**F5 — Trust weight multiplies, and the peer registry is operator-only.** An
operator decides who this node talks to; the mesh does not extend the peer set.
An open peer set is an SSRF and impersonation surface, and the attestation is
what makes a reply attributable at all.

**F6 — No discovery protocol in this phase.** Peers are configured. mDNS, DHT,
and gossip are all a later decision, and the spec already flags "one instance or
a protocol" as an open question to be decided against the code (§6.1). Building
discovery now would settle that question by accident.

## 5. Explicitly not shipping

- Peer discovery, gossip, DHT, onion transport (F6).
- Content or snapshot replication of any kind (F1).
- Automatic merge of a foreign candidate into a local one, ever (F2).
- A foreign candidate being locally votable (F2).
- Peer-side changes: this spec builds the **client** side. A peer that does not
  implement this receives nothing and is simply never asked again after its
  staleness window lapses.

## 6. D6 deferred, with the reason

Guilds, mentorship, adoption, a public roadmap, and voting over it are a
community layer with **no ranking effect on the mesh** (D6's own note). None of
the primitives exist: there is no guild membership, no mentorship relation, and
no adoption state anywhere in the schema, and each needs its own spec section
and its own trust questions. Building a community layer before the metadata
plane has a second node to talk to would produce a social graph with nothing to
socialise about. Deferred to a plan of its own, after this one, when there is
more than one instance to reason about.

## 7. Definition of done

Carried forward from the 03b plan's gate, which is not restated here but still
applies:

- `go build ./...` and `go vet ./...` clean.
- `go test ./...` green **including the pre-existing suites, with no test deleted
  or weakened to make a new one pass.**
- Every mutation listed in the plan killed, or the missing test written.
- The broadcast payload type has **no field capable of holding media**, proven by
  a test that reflects over it.
- A foreign candidate provably cannot reach the local vote path, proven by a test
  that attempts it and expects a rejection.
