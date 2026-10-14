# SPEC — D2: the identification board federates

Implements **SPEC §7.23.1 row D2**: *"Identification board federates. A query
broadcasts to peers with matching taste vectors. Metadata plane only
(§7.17.1). Content never broadcasts."*

Status: **steps 1–6 built** (`d3934900` … `cdb90055`); **§8 (third-party
identification sources) specified 2026-10-14, not started**. This document is the
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

---

## 8. Third-party identification sources (specified 2026-10-14, not started)

The owner pasted a catalogue of external identification resources (2026-10-14):
face-matching models and services, reverse image/video search engines, JAV/AV
code lookup tools, anime taggers, community forums, and MCP servers for
stash-box-style catalogues. This section says which of them are **shapes of
features this board already has**, which are new, and what each one is allowed to
do here — because the division between "a source suggested it" and "the local
community voted for it" is the whole of F2, and a third party is a source the
operator does not control.

### 8.1 The catalogue, placed

| Resource | What it is | Placement | Decision |
|---|---|---|---|
| **StashFace ONNX**, **MegaFace** | ONNX face models trained on StashDB / meta4allphotos | a **local provider** (sidecar) | F9, F10 — runs here, nothing leaves; suggestions only |
| **FaceCheck.ID**, **xzeno.ai**, **PornstarByFace**, **SocialFinder.ai**, **Camgirlfinder**, **FaceSpy**, **OnlyFinder**, **AV女優判定AI**, **Doppelgänger** | remote face-search services | a **remote provider** | F10 — off by default; a biometric query is the operator's decision |
| **SauceNAO**, **IQDB**, **trace.moe**, **TinEye**, **Yandex Images**, **FindIt.moe**, **ImgOps**, **SauceMatch**, **AVScan**, **Revideo.io**, **FrameTrace** | reverse image / video search | **deep link first** (7a), remote provider later | F8 — the link needs no key and no terms review; the API call is a separate decision per provider |
| **IAFD**, **StashDB**, **indexxx**, **PornPics**, **Bang**, **meta4allphotos** | performer/scene metadata catalogues | **the source layer** (migrations 108–110, `content_sources` + `scene_urls`) | already the right shape: a source is a URL claim with health, not a scrape |
| **avbuzz** (FANZA GraphQL), **jvav** (JavBus/Javdb), **AvHub**, **AVサーチくん**, **Rina**, **AvDict** | JAV metadata + AV-code recognition | **the ingestion pipeline** (phase 2) + **code extraction** | F12 — code extraction is a pure function and ships first |
| **WD14 Tagging System**, **WD Tagger**, **pixai-tagger**, **image-tagger** | ONNX anime taggers | a **local tagging provider** | F9 — output is a tag *suggestion*; the edit system is the only write path |
| **mcp-stashbox**, **ThePornDB MCP**, **desaira MCP** | MCP servers over adult catalogues | **phase 13** (API/interop) | §8.4 — expose stash-box as an MCP server; consuming another MCP source is a provider like any other |
| **r/TOMP**, **r/HentaiSource**, **NameThatPorn**, **Stash Forum starter guide** | community channels where a human asks | **deep links + a thread URL on the query** | F12 — a hand-off to people, never a scraper |
| **Erasa** | DMCA/takedown service | **§17 policy**, phase 14 | not an identification source; recorded here so it is not re-pasted as one |

### 8.2 The decisions

**F7 — A third-party source is EVIDENCE, never a vote, and never a local
candidate row.** F2 extended across a new boundary: a peer is remote and
accountable by attestation, a third-party service is remote and accountable by
nothing. Its answer is stored as source evidence with its name and URL attached
and is **not** insertable into `identification_candidates`, so it cannot be voted
on, cannot win a plurality, and cannot become a canonical link. The alternative —
letting a face-search API propose a candidate the community then votes on — makes
the local tally a function of a service the operator does not control.

**F8 — Deep links first, API calls later, and the two are separately enabled.**
Most of the reverse-search resources need no key and no agreement to be *linked*:
the server renders a URL with the image address in it, the user's own browser does
the lookup, and nothing about the instance's users leaves it. That is the half
that ships. A provider that CALLS an external API is a different decision per
provider (keys, terms, rate limits, and what the request reveals), so it is
opt-in, operator-configured, and recorded in the evidence row's `provider` field
so a reader can tell which half produced it.

**F9 — Local models are providers too, and their output is a suggestion.** The
face models and the anime taggers are the resources whose fit is best here,
because they run *inside* the operator's own infrastructure: no third party sees
the images, no key is needed, and the result is reproducible. Their output is a
*suggestion* with a confidence and a provenance — for the board, an evidence row;
for tags, a draft for the edit system, which is the only path that writes a tag.
An auto-tagger that wrote tags directly would be a second, unaudited writer of
shared content, and this codebase already has one write path by design.

**F10 — No biometric query leaves the instance unless the operator turns it on,
per provider.** Sending a face to a third-party search service is a data-flow
decision about a person who is not in the room, and it is the one integration in
this catalogue that can be wrong in a way no test detects. The default is
disabled; enabling one is an operator action recorded in config; and the spec
requires that the provider's own row in `docs/SPEC.md`'s policy table say what it
receives. (The §17 policy section already governs this class of decision — this
is its identification-shaped case.)

**F11 — Provenance is machine-readable, not prose.** Every suggestion carries
`provider`, `source_url` and, where the provider gives one, a `confidence`. "Which
source said this" is what makes a suggestion checkable by a human, and a free-text
`note` alone would make it un-queryable — the same reasoning that put the peer's
attestation on a foreign candidate.

**F12 — Community channels are hand-offs, never integrations.** r/TOMP,
r/HentaiSource, NameThatPorn and the forum guide are places a *person* asks a
question. The board can link to them (with the question pre-filled where the
channel supports it) and can record the thread's URL back on the query so the
answer is findable later. Scraping them, or posting on a user's behalf, is out:
it breaks their terms, it makes the instance's traffic look like a bot, and it
would put words in a user's mouth.

### 8.3 What this does NOT ship

- **No scraping of any listed resource**, and no storing of their raw output as
  canonical: a suggestion is attributed evidence, nothing more.
- **No automatic resolution.** A source may only suggest; a query is still
  resolved by the local community's votes, or abandoned.
- **No automatic media upload to a third party** (F10), in either half.
- **No JAV catalogue ingestion** from FANZA/JavBus by default: the AV-code
  extraction is a pure function and ships, while a per-site scrape is a source
  operator decision like any other (§8.4).
- **No boundary weakening**: `identification_source_evidence` is a third table
  beside `identification_foreign_candidates`, and the existing
  `TestForeignEvidenceCannotReachLocalVotePath` gains a sibling for it.

### 8.4 Where the non-identification resources land

Three groups in the catalogue belong to other surfaces, and they are recorded
here so a later reader does not re-file them as identification work:

- **MCP (phase 13).** `mcp-stashbox` shows the shape stash-box should *expose*:
  search scenes and performers, read records, identify files by fingerprint. That
  is a read-only MCP server over surfaces that already exist, and it is the
  cheapest interop the programme has left. Consuming an external MCP source
  (ThePornDB, desaira) is a **provider** in this section's sense — evidence with
  provenance — and not a second ingestion pipeline.
- **Sources and codes (phases 2/12).** IAFD/indexxx/PornPics/Bang are domains the
  source layer already models (`content_sources`, `scene_urls`, `sites.regex`);
  what is new is a **seed list** of the well-known hosts and, for the JAV set, an
  **AV-code extractor** — a pure function that recognises `ABC-123` in a title,
  filename or URL and returns the code plus the canonical URLs it implies. It
  belongs with ingestion because it is a *normalisation* step, and it is testable
  without a network.
- **Facilities (phases 10/16).** Face detection also serves **face-centred crop
  suggestions** (`95_image_crops` exists; complaint #84), and the anime taggers
  serve the **image/gallery/animation content kinds** (phase 3) — both are the
  same provider interface with a different output, which is why F9 puts them
  behind one registry rather than in three subsystems.
