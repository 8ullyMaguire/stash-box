# SPEC — the content aggregator programme (NexusXXX parity)

Owner instruction, 2026-10-14: *"I want stash-box to include all functionality of
nexusxxx to use it as a content aggregator, without losing any of the current
functionality. Update docs/spec/plan/etc then continue developing stash-box until
completion."* And on scope: *"'until completion': until stash-box is fully
implemented and tested including all new functionality."*

**The no-loss rule comes first, and it has two halves.** Nothing the fork does
today may regress — the public GraphQL surface the Stash desktop app depends on
(§4 constraint 3), every migration, every gate clause in `docs/goal-check.py`,
and every existing spec section. And nothing in the NexusXXX draft may be dropped
as "not applicable": the owner has explicitly opted in to the material this
fork's own vision had kept out (§7.19's rejected content layer, monetization,
ActivityPub). Where an earlier section of this SPEC says a thing is out of scope,
**that section is superseded here, not contradicted quietly.** §4 below lists
every such reversal with what it costs, because a reversal nobody recorded is how
a spec starts lying about itself.

Source draft: `~/secondbrain/10-Projects/nexusxxx/2026-10-14-nexusxxx-spec-v2.md`
(v2.0, 23 sections). The crosswalk that scored the overlap is
`~/secondbrain/10-Projects/nexusxxx/2026-10-14-nexusxxx-stash-box-crosswalk.md`;
where it recommended *not* transferring an item, this spec overrides it and says
so.

---

## Respec 2026-10-15 — this programme builds a metadata engine, not a content plane

Owner instruction 2026-10-15: *"disable/remove functionality like content aggregation and discussion
board that can give trouble unless there's a way to build them to avoid many dmca's, keep identification
board only."* The accompanying verdict is adopted as the reasoning, not just the instruction: aggregation
aimed at the right problem by the wrong means. "Find the scene" is what a reader wants, and a metadata
database nobody visits cannot deliver it — but a site that embeds, proxies, transcodes or hosts third-party
content is the exact category that is DMCA'd out of existence, and one auto-scraped link to material that
must never be indexed ends the project outright. §7.19 of the draft refused the content layer for the same
reason, and this respec puts that refusal back.

**What is REFUSED, and struck from the programme:**

| Refused | Was | Why |
|---|---|---|
| Hosting, transcoding, the variant ladder, the five players, PWA offline caching, CDN/edge | phase 10 (media plane) | It is what makes an instance a HOST rather than a catalogue: immediate, existential legal exposure, a different product, and 100x the operations. None of it was ever built — `grep -rn "iframe\|<video\|transcod\|ffmpeg\|m3u8\|hls" internal/ graphql/` is empty. The one table that could be mistaken for a start on it, `content_item_media` (migration 117), holds only ADDRESSES: an editor records a work's renditions through `contentItemEdit` (a URL, a kind — `original`, `preview`, `stream`, `download`, `archive` — and dimensions) and the completeness score already reads the presence of those rows as evidence. No code serves, proxies or transcodes what it names, so it is a references table like `content_sources` and it STAYS: deleting it would silently change what "incomplete" means for a content item, which is a curation rule and not this respec's business. |
| A general discussion board / forum | slice 8f | A moderation surface for spam, harassment and piracy links that scales with traffic, competing with Reddit and Discord for posts before this instance has the traffic to sustain either. Deferred indefinitely rather than built "because it is cheap". |

**What is KEPT, and re-framed as REFERENCES rather than content.** Recording where a work came from,
checking that the link still resolves, and reading a page's *metadata* to propose an edit (phases 1, 2, 3
and 9) is a catalogue of pointers. That is the whole legal difference: the instance holds claims about
works and the addresses of pages, and never a copy of a work. `content_sources` records a URL and its
health; `ingest_queue` fetches a page to read a title and a performer list and proposes an edit, which a
human still has to approve; `content_items` is metadata about non-video kinds, and a rendition URL an editor
recorded is an address in the same sense `content_sources`' is. Nothing here holds bytes, and nothing here serves
a work to a reader.

**Every outbound request to a content source is gated OFF by default**, by one switch
(`content_fetch_sources`, `internal/config`), because "which behaviours make this instance a scraper" is a
legal posture and not a tuning knob — an operator turns it on deliberately, in writing, knowing what it
means:

- `internal/service/ingest/extract/http.go` — the page fetch the ingest pipeline performs;
- `internal/service/content/health.go` — the health sweep's HEAD request to a source URL;
- `internal/service/content/wayback.go` — Wayback "Save Page Now" submissions.

The switch is enforced at BOTH ends: the cron jobs are not registered at all when it is off (so no tick
can run, and the absence is logged once at startup naming the switch), the service entry points refuse
with a named error, and `submitContentUrl` refuses, so nothing is queued for a drain that will never run.
What it does NOT cover, named so nobody assumes it does: `internal/storage/favicon.go` (upstream's
site-icon fetch, member-triggered, one icon for a metadata page), `internal/webhook` (this instance calling
a URL an operator configured), `internal/service/federation/client.go` (peers the operator peered with) and
`internal/sdbimport` (the directory importer). Those are pre-existing upstream or mesh behaviours, and the
respec's claim is about **content sources**, which is what made the programme legally interesting.

**Frame indexing: the hash, never the frame.** The draft's frame-level visual search is kept ONLY in the
form that needs no content access: an opted-in Stash desktop client contributes perceptual hashes of its
own keyframes, and the box clusters those hashes for duplicate detection and "is this the same scene from
another source". No server-side video processing, no frame storage, no frame serving — the frames never
leave the member's machine, which is also why it lands with the fingerprint service rather than with a
media plane. **Read this as a decided direction and not a built feature: it is not built, and it is not a
phase of this programme.** The broad version (CLIP-style visual similarity) stays refused: §7.25.7 is
right that there is no ML stack here, and the text search is not exhausted.

**The comments surface is NOT the board that was struck**, and the distinction is recorded so that neither a
reader nor a future contributor reads "the discussion board is removed" as "commenting on an edit is
removed": `EditComment` is anchored to an edit (or, through the fork's typed layer, to an entity), its
kinds are a closed vocabulary whose output is an EDIT, and it is hidden and updated through the edit
system's own moderation path. What the owner struck is a standalone board with its own public threads —
a surface that needs humans for spam, harassment and piracy links, at a rate that scales with traffic.

**The identification board is untouched, and it is what remains.** It is text, fingerprints and
attribution — a member says "this is the scene you asked about" and the answer opens an edit. Nothing in
this respec removes a single surface of it, and the local identification models the media-plane section
used to carry (face matcher, image taggers) were already moved to
`docs/spec/feature-04-identification-federation.md` §8, where they stay, under that feature's own rule
that a model's output is a suggestion and never a link.

**Consequence for the predicate:** phase 10 is removed from the plan rather than left "not started", so
`goal-check.py`'s clause C8 counts the phases this programme actually intends to finish. A phase kept as a
tombstone and counted is a goal nobody can complete.

---

## 0. Priority — aggregation and discovery lead, and nothing else outranks them

Owner instruction, 2026-10-14, after reading the first cut of this programme:

> *"content aggregation and discovery (algorithms, search, etc) should be the
> priority"*

The first cut of the plan did not say that. It sequenced the phases by dependency
alone — source layer, ingestion, tag ontology, tiers, trust, then discovery at
position seven — and §4 below now records that as a reversal rather than letting
it stand. The priority is:

1. **The aggregation spine.** A URL becomes a content record with sources,
   deduplication, metadata and tracked health. Phases 1 and 2. Without this there
   is nothing to discover.
2. **Discovery.** Search that is more than free text: the query language, tags as
   filters and as a graph, saved searches, freshness and trending, then the
   algorithmic half — content DNA and similarity, taste profiles, For You, the
   serendipity parameter, social and "where I left off". Phases 4 and 7, and they
   are the reason the programme exists.
3. **Everything else supports those two.** Breadth of content kinds, preservation
   at scale, tiers, social, trust, growth, media hosting, federation, monetisation
   and the UI are all in this programme and none of them may be dropped — the
   no-loss rule still binds. They are ordered behind the two things that make the
   platform worth opening, not ahead of them.

Two consequences of this ordering that are decisions, not accidents, and their
costs are stated in §4: **tag ontology lands before tiers** (faceted discovery is
the differentiator §1's own table names first, and the current tag model already
carries aliases and nested categories), and **discovery ships before the content
gate**, so the default is everything visible and the gate lands before any public
launch.

---

## 1. What this repo already has, measured

Verified against the tree at `9183a280`, not read from a doc. This matters because
the tracker here has been wrong before in both directions (a "vocabulary project"
that had a table, a "no type exists at all" that had a service, eleven queries and
an integration test).

| Primitive | State | Reused for |
|---|---|---|
| `edit` / `edit_votes` / `edit_comments` / `mod_audit` | built, 4 480 lines in `internal/service/edit` | NexusXXX §3.4, §4.6, §17.2 |
| `EntityType` polymorphism (`performer\|scene\|studio\|site\|tag`) — used by reviews, completion, elo, favorites, lists, `expected_totals` | built | every new content kind in §4 |
| `tags` with `aliases`, `tag_categories` **with nesting** (migration 106, cycle-guarded, `TagCategoryTreeNode`) | built; no tag-to-tag graph | §4 ontology (the gap is the level below the category) |
| `content_denylist_tag/studio/performer` (migration 87), `AccessRuleSet` with `enforced`/`unenforced` | built | §5 tiers, §17 |
| `completion` (score, `missing`, `countIncompleteEntities`) + `field_verification_state` (96) + `expected_totals` (97) | built | §3.4 measurement, §6 quests |
| `collages` (12–24 frame storyboards, migration 105 refreshes duration) | built | §19 media plane, §10 identification |
| `score`/pHash clusters + `fingerprint_cluster` + oshash corroboration (99) | built | §3.3 dedup, §8 mirror resolution |
| `identification` board (migration 79) + `IdentificationDetective` | built | §10 |
| `elo` (Glicko-2: rating + deviation, leaderboards by distinguishable rating) | built | §7 discovery, §6 leaderboards |
| `streak`, `badge`, `quest`, `award`, `authored` quests (100 bounty pricing) | built | §6 gamification |
| `invite` / `InviteKey` / `RoleEnum {INVITE, MANAGE_INVITES}` | built; no allocation or referral semantics | §11 growth |
| `list` + `list_items` + publication audit (107) | built | §9.3 playlists |
| `federation` peers, mesh roles, replication (§7.17/§7.18), `webhook`, `image` (local/S3), `site` directory `scene_search` (BM25, migration 103 recall columns) | built | §8, §12, §13, §15 |
| `scene_urls (scene_id, url, site_id)` — **the only source/URL record for content** | built, no health, no status, no platform beyond the site FK | §2 source layer — this is the aggregator's seed |
| `content_sources` + the `scene_sources` view + the one-way `scene_urls` mirror (migration 108, phase 1) | **built** — status machine as a CHECK, one canonical source per scene, health columns, backfilled; membership stays owned by `scene_urls` | §2, §3.2 `sources[]`, §7.3's status machine (the sweep itself is phase 9) |
| `Scene.sources` on the GraphQL API | **built** — batched through a dataloader, reading the view so health survives an edit | §3.2, §8's discovery surfaces |

**The single biggest gap:** there is no content that this repo knows about which a
member does not already own. `scene_urls` records where a *member's own file* came
from. NexusXXX's whole premise is content indexed from a URL with no local copy.
That is Phase 1.

---

## 2. How aggregated content is modelled (the one architectural decision)

**`scene` stays the video content item, extended; a new `content_item` carries the
non-video kinds.** Three reasons, and the third decides it:

1. `scene` already has every attribute NexusXXX's `ContentItem` lists (title,
   details, date, studio, performers, tags, links, fingerprints, images,
   completion, elo, reviews) and 30+ services read it.
2. Aggregated video *is* a scene in every respect except that nobody has the file:
   `scene_fingerprints` is a separate table, so a scene row without fingerprints is
   already legal today.
3. A second video table would fork every one of those 30 readers, which is the
   loss the no-loss rule forbids.

So video = `scenes` + a new `content_sources` table (Phase 1) + an `origin` column
distinguishing a library scene from an aggregated one. Non-video kinds (image,
gallery, text, audio, comic) get `content_items` with the same `EntityType`
polymorphism the reviews/completion/elo/lists tables already use — so a text item
gets tags, reviews, completion, elo and lists with no new plumbing (Phase 3).

---

## 3. Parity inventory — all 23 NexusXXX sections

"Exists" = present and sufficient. "Partial" = present but missing the named
behaviour. "New" = nothing to extend. Phase numbers are §Phases in
`docs/plan/feature-content-aggregator.md`.

| NexusXXX | What it asks for | State here | Phase |
|---|---|---|---|
| §1–2 | thesis, 9 design principles | n/a (this spec is the answer) | — |
| §3.1 | 6 content types | scenes only | 3 |
| §3.2 | ContentItem with `sources[]`, media variants, tiers, DNA | `scenes` + `scene_urls` (no health/status) | 1, 7 |
| §3.3 | dedup groups, canonical election, merge/split votes | fingerprint clusters + merge edits exist | 2 (extend) |
| §3.4 | metadata snapshots, diff, rollback, anomaly detection | `edit` + changelog + `field_verification_state` | 2 (extend) |
| §4.1–4.5 | tag ontology: implications, DAG, tiers, search language | aliases + **nested categories**; no tag graph | 4 |
| §4.6 | ontology versioning + RFC | none | 4 |
| §5.1 | TL0–TL4 ladder | six-level ladder §7.6 | exists |
| §5.2 | visible sub-scores | `UserTrust` curve — **owner-private today** | 6 (decision) |
| §5.3 | decay, recovery, streak freeze | `streak` only | 6 |
| §5.4 | vouching | none | 6 |
| §6.1 | achievement catalogue | quests/badges exist, thresholds differ | 6 (extend) |
| §6.2 | contribution heatmap | none | 6 |
| §6.3 | leaderboards filterable by level, opt-out | elo leaderboards exist | 6 (extend) |
| §6.4 | seasonal events | awards exist | 6 (extend) |
| §7.1–7.2 | ingestion from 8 source classes, pipeline | `sdbimport` only | 2 |
| §7.3 | link health daemon, statuses, resurrection queue | none | 1, 9 |
| §7.4 | performer archive/snapshots | performer profiles + `profile_snapshots`? no | 9 |
| §7.5 | self-hosting, transcoding, tiered storage, CDN | **refused by the respec** — the instance is a catalogue, not a host. `image` local/S3 stays: it is upstream's metadata-asset store | — |
| §8.1–8.4 | 8 discovery modes, search language, trending/freshness | search + trending exist (migration 60) | 7 |
| §8.5 | content DNA vector, similarity + explanations | elo + `performer_similar` | 7 |
| §8.6 | taste profile, affinity table, For You | taste fingerprints via elo votes | 7 |
| §8.7–8.8 | social discovery, serendipity engine | none | 7 |
| §8.9 | "where I left off" | none | 7 |
| §9.1 | typed comments that write metadata | `edit_comments` exist (on edits) | 8 |
| §9.2 | forums | **deferred by the respec** — a moderation surface that scales with traffic, competing with Reddit and Discord for posts | — |
| §9.3 | playlists, fork, auto-update | `list` (107) | 8 (extend) |
| §10 | T0–T3 tiers, warnings vs gates, onboarding wizard | denylist + access rules | 5 |
| §11 | invite allocation, referrals, QR, seed cohorts | `InviteKey` only | 11 |
| §12.1–12.5 | metadata federation, ontology sync, hash network, specialised instances | mesh §7.17/§7.18 is **ahead**; hash network new | 12 |
| §12.6 | ActivityPub | none (rejected in the crosswalk) | 12 |
| §13.1–13.5 | 5 players/viewers | **refused by the respec** — no player, no embed, no proxy. Reading *metadata* is a different surface and it is in: a comic's chapter list and page count are `content_item_chapters` (phase 3) | — |
| §14 | facial recognition, community ID, profile claiming | identification board (untouched); local models live in `feature-04-identification-federation.md` §8, where a model's output is a suggestion | 4 (feature 04) |
| §15.1–15.3 | REST+GraphQL API, integrations, exports | GraphQL, webhooks; no REST/RSS/exports | 13 |
| §16 | anti-doomscroll design + transparency report | none | 16 |
| §17 | CSAM/DMCA/age/consent policy, appeals, elected mods | **nothing** — see §5 | 14 |
| §18 | premium, creator subs, ads, data licensing | none (MIT) | 15 |
| §19 | service split, ES/Meili, vector store, NATS, MinIO, CDN | single Go binary, PG + pg_search. MinIO and the CDN were the media plane's; the vector store is phase 7's | 7 |
| §20 | UI: 5 key pages, mobile PWA, WCAG 2.1 AA | React SPA, 25 page dirs | 16 |
| §21 | risk register | partial | 16 |
| §22 | 6-phase roadmap | — | this plan |
| §23 | 17 metrics with targets | completion/archive measured; no product metrics | 16 |

---

## 4. Reversals this spec makes, and what each costs

Each of these contradicts an earlier section of this SPEC or the crosswalk. The
owner's instruction is the authority; the cost is stated so the next reader does
not discover it as a surprise.

| Reversal | Supersedes | Cost, stated plainly |
|---|---|---|
| **Content is hosted, proxied and transcoded** (media plane) — **REVERSED AGAIN, 2026-10-15: NOT BUILT** | §7.19 "the P2P content layer — REJECTED", which was right | The reversal was a mistake and the owner's own verdict is the argument: it re-opens the legal surface the metadata/media split exists to avoid, makes every scraped link an existential risk, and turns a metadata engine into a tube site. Phase 10 is struck from the plan, **the rendition table `content_item_media` STAYS** (a row is a URL an editor recorded, and the completeness score reads it — see the plan's respec section for how an earlier draft of this very sentence was wrong), and the cost of the reversal-now-reversed is that the draft's §13 and §7.5 parity items are answered "no" rather than "later". What it buys: an instance that holds claims and addresses, and never a copy of a work. |
| **Money changes hands** (premium, creator subs, ads, data licensing) | MIT licence + §7.13 operator autonomy | The fork is MIT; a fork-level monetisation feature is not a licence change but it is a governance one. Modelled as per-operator config with the code open, and no feature is gated behind it (§18.2's "always free" list is adopted verbatim). |
| **Sub-scores and reputation become visible** | `user.graphql`'s `@isUserOwner` "a user's standing is not other users' business" | The owner's draft makes them public (§5.2, §20.2). Phase 6 must decide per-field: level public, raw vote weights private. Leaving it undecided is what makes the two specs disagree. |
| **Taste profiles are federated, not local-first** | §8.6 "storage: local_only" default | Mesh peering (§7.17.3) ranks peers *by* taste similarity, which needs the fingerprint server-side. Opt-in, with the local-only mode kept for members who decline. |
| **ActivityPub federation** | crosswalk §5 "not transferred" | A second identity model next to §7.17's signed relationships. Deferred to Phase 12 and only after the mesh protocol is published. |
| **Facial recognition at ingest** | §7.25.3 "a vote is evidence, never authority" | The draft auto-applies a performer above 90% confidence. Phase 10 adopts the *pipeline* but keeps the fork's rule: a face match opens an edit or a board entry, it never writes a performer link. |
| **Aggregation and discovery lead the programme** (§0) | this plan's original dependency-only sequence, which put discovery seventh | Tag ontology lands before tiers; tiers, social, trust, growth, hosting, federation and monetisation all land after discovery. The cost, stated: **discovery ships before the content gate**, so the default is everything visible until Phase 5, and the gate is a precondition for any public launch rather than something launched with. The second cost: no phase of discovery may be paused waiting for a governance feature, which means the tag ontology (Phase 4) has to be finished rather than sampled. |

---

## 5. What "completion" means

`docs/goal-check.py` is this repo's completion predicate and exits 0 only when
every clause passes. The programme is complete when:

1. Every clause in `goal-check.py` passes, including the six currently failing or
   unknown (C1 undecided PRs, C3 unbuilt plans, C6b integration suite, C7 gofmt).
2. `goal-check.py` gains one clause per phase of this programme, each asserting
   the phase's *observable* surface (a table, a GraphQL field, a route, a suite
   name), never a claim in a document.
3. `docs/plan-verify.py` passes for every plan document, including this one's.
4. Every phase has an integration test suite and a `scripts/verify-*.sh` database
   proof where the phase adds constraints.
5. Each new dependency the programme still wants (a vector store; the identification
   feature's local models) has a record of the decision and a fallback, per the project's own
   habit of writing down what it deliberately did not install. **ffmpeg and yt-dlp are no
   longer expected**: they were the media plane's, and the media plane is refused (respec,
   2026-10-15).

## 6. Non-goals

Functionality IS dropped, by owner decision: **the content plane** (hosting, transcoding, the
players, the PWA, any embed or proxy of a third party's content) and **a general discussion board**,
for the reasons the respec section gives. What is explicitly *not* in this spec: rewriting `scene` into a `content_item`, breaking the Stash desktop app's
GraphQL contract, and any phase beginning before its predecessors are green. The
phase order is a dependency order, not a preference order.
