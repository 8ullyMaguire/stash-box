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
**that section is superseded here, not contradicted quietly.** §5 below lists
every such reversal with what it costs, because a reversal nobody recorded is how
a spec starts lying about itself.

Source draft: `~/secondbrain/10-Projects/nexusxxx/2026-10-14-nexusxxx-spec-v2.md`
(v2.0, 23 sections). The crosswalk that scored the overlap is
`~/secondbrain/10-Projects/nexusxxx/2026-10-14-nexusxxx-stash-box-crosswalk.md`;
where it recommended *not* transferring an item, this spec overrides it and says
so.

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
| §7.5 | self-hosting, transcoding, tiered storage, CDN | `image` local/S3; no media | 10 |
| §8.1–8.4 | 8 discovery modes, search language, trending/freshness | search + trending exist (migration 60) | 7 |
| §8.5 | content DNA vector, similarity + explanations | elo + `performer_similar` | 7 |
| §8.6 | taste profile, affinity table, For You | taste fingerprints via elo votes | 7 |
| §8.7–8.8 | social discovery, serendipity engine | none | 7 |
| §8.9 | "where I left off" | none | 7 |
| §9.1 | typed comments that write metadata | `edit_comments` exist (on edits) | 8 |
| §9.2 | forums | none, deliberately | 8 |
| §9.3 | playlists, fork, auto-update | `list` (107) | 8 (extend) |
| §10 | T0–T3 tiers, warnings vs gates, onboarding wizard | denylist + access rules | 5 |
| §11 | invite allocation, referrals, QR, seed cohorts | `InviteKey` only | 11 |
| §12.1–12.5 | metadata federation, ontology sync, hash network, specialised instances | mesh §7.17/§7.18 is **ahead**; hash network new | 12 |
| §12.6 | ActivityPub | none (rejected in the crosswalk) | 12 |
| §13.1–13.5 | 5 players/viewers | collages only | 10 |
| §14 | facial recognition, community ID, profile claiming | identification board; no face matching | 10 |
| §15.1–15.3 | REST+GraphQL API, integrations, exports | GraphQL, webhooks; no REST/RSS/exports | 13 |
| §16 | anti-doomscroll design + transparency report | none | 16 |
| §17 | CSAM/DMCA/age/consent policy, appeals, elected mods | **nothing** — see §5 | 14 |
| §18 | premium, creator subs, ads, data licensing | none (MIT) | 15 |
| §19 | service split, ES/Meili, vector store, NATS, MinIO, CDN | single Go binary, PG + pg_search | 7, 10 |
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
| **Content is hosted, proxied and transcoded** (media plane) | §7.19 "the P2P content layer — REJECTED" | Re-opens the legal surface the metadata/media split exists to avoid, and §7.18 gains real obligations. Phase 14's policy text must land **before** Phase 10's first byte is stored. |
| **Money changes hands** (premium, creator subs, ads, data licensing) | MIT licence + §7.13 operator autonomy | The fork is MIT; a fork-level monetisation feature is not a licence change but it is a governance one. Modelled as per-operator config with the code open, and no feature is gated behind it (§18.2's "always free" list is adopted verbatim). |
| **Sub-scores and reputation become visible** | `user.graphql`'s `@isUserOwner` "a user's standing is not other users' business" | The owner's draft makes them public (§5.2, §20.2). Phase 6 must decide per-field: level public, raw vote weights private. Leaving it undecided is what makes the two specs disagree. |
| **Taste profiles are federated, not local-first** | §8.6 "storage: local_only" default | Mesh peering (§7.17.3) ranks peers *by* taste similarity, which needs the fingerprint server-side. Opt-in, with the local-only mode kept for members who decline. |
| **ActivityPub federation** | crosswalk §5 "not transferred" | A second identity model next to §7.17's signed relationships. Deferred to Phase 12 and only after the mesh protocol is published. |
| **Facial recognition at ingest** | §7.25.3 "a vote is evidence, never authority" | The draft auto-applies a performer above 90% confidence. Phase 10 adopts the *pipeline* but keeps the fork's rule: a face match opens an edit or a board entry, it never writes a performer link. |

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
5. Each new dependency (yt-dlp, a face model, a vector store, ffmpeg) has a
   record of the decision and a fallback, per the project's own habit of writing
   down what it deliberately did not install.

## 6. Non-goals

None of NexusXXX's functionality is dropped. What is explicitly *not* in this
spec: rewriting `scene` into a `content_item`, breaking the Stash desktop app's
GraphQL contract, and any phase beginning before its predecessors are green. The
phase order is a dependency order, not a preference order.
