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
| Hosting, the variant ladder, the media toolchain, the five players, PWA offline caching, CDN/edge | phase 10 (media plane) — **struck here, restored gated by the second respec below** | It is what makes an instance a HOST rather than a catalogue: immediate, existential legal exposure, a different product, and 100x the operations — which is why it ships as a capability every config leaves OFF rather than as a default every fork inherits. Nothing has been built: `grep -rn "iframe|<video|transcod|ffmpeg|m3u8|hls" internal/ graphql/` is empty, and `scripts/check-no-content-plane.py` fails the build if that changes outside `internal/service/mediaplane/`. The one table that could be mistaken for a start on it, `content_item_media` (migration 117), holds only ADDRESSES: an editor records a work's renditions through `contentItemEdit` (a URL, a kind — `original`, `preview`, `stream`, `download`, `archive` — and dimensions) and the completeness score already reads the presence of those rows as evidence. No code serves, proxies or converts what it names, so it is a references table like `content_sources` and it STAYS: deleting it would silently change what "incomplete" means for a content item, which is a curation rule and not any respec's business. |
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

**KEEPING the functionality is the answer that was asked for, and these are the four things that make
it defensible.** Owner follow-up, same day: *"we have already implemented most of this functionality and
it would be a pity to remove it now and make it wasted time — what's the best way to keep that
functionality while minimizing DMCA risk?"* The answer is not to delete the pipeline and not to run it
bare: it is to keep the metadata machine and take away every property that made it a target. Four exist
now, three of them built on 2026-10-15 and each verified by a test rather than by this paragraph:

1. **An operator can take a work, a URL or a whole host out of service in one mutation.**
   `takedownContentUrl` marks the catalogue's record and every source URL for it; `blockContentHost`
   refuses an entire host going forward; `releaseContentBlock` undoes a mistake, because a refusal with
   no release is a support queue. `content_source_block` holds the refusals (a host or a normalized URL,
   unique on the pair), the submit path and the drain both consult it, and every takedown writes a
   `mod_audit` row naming the operator and the reason. **The metadata stays**: a takedown takes the
   ADDRESS out of service, never the claim about the work — the metadata/media split's whole point, and
   what an actual notice asks for.
2. **Crawl hygiene is the host's rules and one pace, in one place.** Both clients that make automated
   outbound requests — the ingest page fetch and the health sweep's `HEAD` — obey `robots.txt` and a
   per-host interval through a single `internal/service/hostrules` (`Default`), because two clients with
   two caches would knock on one host twice as often as either believed. A refused path is recorded as
   `robots_disallowed` and never fetched; an unreadable `robots.txt` fails closed (RFC 9309). Neither the
   rules nor the pace is configurable by a member: a host's terms are the host's.
   **The one outbound request this does not cover, and why** — Wayback "Save Page Now"
   (`internal/service/content/wayback.go`): robots.txt governs *fetching a host's paths*, and this
   instance does not fetch the source page there — it asks archive.org to do so, on archive.org's own
   behalf. The request is a POST to that service's API, and the service's own fetching is the service's
   behaviour, not this instance's. It stays gated by `content_fetch_sources` like every other outbound
   request, and its volume is bounded by the sweep's batch limit. Named here so a reader finds a decision
   rather than a gap.
3. **The metadata is read, and nothing is ever served.** The refused list in the table above is enforced
   by `scripts/check-no-content-plane.py`, which `docs/goal-check.py` runs on every gate: fourteen
   patterns (`iframe`, `<video`, `transcode`, `ffmpeg`, `m3u8`, `hls`, `.mp4`, `content-range`,
   `video.js`, `poster=`, `<source src`, `accept-ranges`, `webrtc`, `rtmp`), zero hits in the tree, and a
   self-test that proves the scanner still finds a planted one. A rule that lives in a document is a
   hope; this one fails the build.
4. **The default is still OFF, and the reason is stated.** `content_fetch_sources: false` remains the
   shipped value, because the default is what every operator who forks this instance inherits, and
   "turn it on and let it fetch" should be a decision somebody made rather than a behaviour nobody
   noticed. **On is a supported posture** — that is what changed today — and enabling it is one key in
   `stash-box.yml`. What does not change with the switch is guardrail 3: the tree cannot grow a player,
   an embed or a proxy while the gate runs, whatever the configuration says.

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

**Consequence for the predicate:** phase 10 was removed from the plan rather than left "not started", so
`goal-check.py`'s clause C8 counts the phases this programme actually intends to finish. A phase kept as a
tombstone and counted is a goal nobody can complete. **Superseded the same day — see the second respec
below: phase 10 is back in the plan as a gated, off-by-default capability, and C8 counts it again.**

---

## Second respec, 2026-10-15 — the media plane returns, gated and OFF by default

Owner instruction 2026-10-15, later the same day: *"include it in the spec again but disable it by default
in config."* This does not undo the first respec's reasoning; it moves the refusal from the CODE to the
DEFAULT, which is where the risk actually lived.

**The reversal chain, stated plainly, because three positions in one day is a decision a reader has to
follow rather than a slip.** (1) The draft carried hosting and players as phase 10. (2) The first respec
struck the phase, on the argument that an instance which serves third-party material is a host, and a host
is the category of site that is DMCA'd out of existence; the "keep the functionality defensibly" follow-up
then added the outbound switch. (3) This respec restores the phase as a SPECIFIED, PLANNED capability with
its switch off, because the argument above was about the DEFAULT and not about the design: the thing that
would have made every fork a host is that an operator who read nothing still got hosting, not that the
capability can exist. A capability an operator turns on deliberately, knowing what it means, is the same
posture this programme already ships for outbound fetching — and it is the posture the owners of the
catalogue can actually change their minds about later, which a deleted design cannot.

**Restored to the plan:** phase 10, as slices 10a–10f — the switch, hosting, the variant ladder and the
media toolchain, the players, PWA offline caching, the CDN/edge — each with the media-plane design the
earlier struck section carried. `content_item_media` was never part of the reversal: a row there is an
address an editor recorded, not bytes.

**The switch is `content_delivery` (`internal/config`), default `false`, and it is read in four places,
because a switch that is only written down is a claim about a configuration file:**

1. **The gate (`internal/service/mediaplane`).** `RequireDelivery()` returns `ErrDeliveryDisabled` whose
   message names the key, and `PostureLine()` returns the sentence the process logs. A slice that serves
   anything asks the gate first; the module exists so that the first slice has somewhere to ask and so a
   capability arriving without asking is visible.
2. **The startup log.** The process states which posture it is in, next to the version, at every startup
   (`internal/api`), because an operator's question is "which posture is THIS instance running" and the
   answer must not require grepping a config file.
3. **The build gate (`scripts/check-no-content-plane.py`).** It now checks three things instead of one:
   the serving patterns still fail anywhere in the tree **except the media plane's own module**
   (`internal/service/mediaplane/`) — the plane may not arrive as a sprinkle — the switch field exists with
   its `content_delivery` key and its shipped default is `false`, and the startup path actually reads the
   posture line. `--self-test` proves each arm can fail, on both sides of the allowlist.
4. **The shipped defaults**, asserted by tests in `internal/config` and `internal/service/mediaplane`.

**What the switch does NOT change.** The first respec's refusals that were never about hosting stay
refused, and the switch does not license them:

- **No third-party embedding or proxying, ever.** The gate's patterns (`iframe`, an embedded player, a
  proxy of somebody else's stream, byte-range serving of a file this instance does not own) stay hits
  outside the media-plane module whatever the configuration says. Serving your own catalogue is a
  decision; serving somebody else's is the DMCA category, and no switch turns it on.
- **A model's output is still a suggestion**, never a written link (feature-04 §8): the identification
  half of the old phase 10 was never in this phase.
- **The takedown verb applies to the media plane the moment one exists** (phase 14, slice 1): a
  `content_source_block` row refuses an address, and served bytes are refused by the same rows.
- **The instance still holds no copy of a work it does not own.** The media plane serves what an operator
  hosts; the catalogue stays claims and addresses.

**New dependencies the restored phase may want, each needing its own decision record:** a media toolchain
binary and a downloader are no longer excluded by this spec — they are excluded by phase 10's own slices
until 10c and 10f are started, and installing one is a decision with a fallback, written down first, in
the habit this repo already has.

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
| `EntityType` polymorphism (`performer|scene|studio|site|tag`) — used by reviews, completion, elo, favorites, lists, `expected_totals` | built | every new content kind in §4 |
| `tags` with `aliases`, `tag_categories` **with nesting** (migration 106, cycle-guarded, `TagCategoryTreeNode`) | built; no tag-to-tag graph | §4 ontology (the gap is the level below the category) |
| `content_denylist_tag/studio/performer` (migration 87), `AccessRuleSet` with `enforced`/`unenforced` | built | §5 tiers, §17 |
| `completion` (score, `missing`, `countIncompleteEntities`) + `field_verification_state` (96) + `expected_totals` (97) | built | §3.4 measurement, §6 quests |
| `collages` (12–24 frame storyboards, migration 105 refreshes duration) | built | §19 media plane, §10 identification |
| `score`/pHash clusters + `fingerprint_cluster` + oshash corroboration (99) | built | §3.3 dedup, §8 mirror resolution |
| `identification` board (migration 79) + `IdentificationDetective` | built | §10 |
| `elo` (Glicko-2: rating + deviation, leaderboards by distinguishable rating) | built | §7 discovery, §6 leaderboards |
| `streak`, `badge`, `quest`, `award`, `authored` quests (100 bounty pricing) | built | §6 gamification |
| `invite` / `InviteKey` / `RoleEnum {INVITE, MANAGE_INVITES}` | built; allocation by level (slice 11a); referrals paid once at the invitee's first real level (11b); a presented key is validated and consumed in BOTH configurations — `require_invite` decides only whether a key is MANDATORY, never whether the key you were handed is usable, nor whether using one counts; an unqualified key is ONE use, as the schema documents; QR/counters/cohorts not yet | §11 growth, §7 |
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
| §7.5 | self-hosting, transcoding, tiered storage, CDN | **restored as a gated capability — phase 10, `content_delivery` off by default** (second respec 2026-10-15). `image` local/S3 stays: it is upstream's metadata-asset store | 10 |
| §8.1–8.4 | 8 discovery modes, search language, trending/freshness | search + trending exist (migration 60) | 7 |
| §8.5 | content DNA vector, similarity + explanations | elo + `performer_similar` | 7 |
| §8.6 | taste profile, affinity table, For You | taste fingerprints via elo votes | 7 |
| §8.7–8.8 | social discovery, serendipity engine | none | 7 |
| §8.9 | "where I left off" | none | 7 |
| §9.1 | typed comments that write metadata | `edit_comments` exist (on edits) | 8 |
| §9.2 | forums | **deferred by the respec** — a moderation surface that scales with traffic, competing with Reddit and Discord for posts | — |
| §9.3 | playlists, fork, auto-update | `list` (107) | 8 (extend) |
| §10 | T0–T3 tiers, warnings vs gates, onboarding wizard | denylist + access rules | 5 |
| §11 | invite allocation, referrals, QR, seed cohorts | `InviteKey` + the level-derived allowance (11a), the referral payment (11b), the durable event journal, the three counters, the landing page and the QR (11c: `invite_key_events` + `User.invite_stats` + `GET /invite/<code>` + `InviteKey.link`/`qr_svg`), seed cohorts and campaigns (11d: `seed_cohorts`/`seed_cohort_members`/`seed_campaigns` + `SeedCampaignRunResult` + the `seed_fast_track` points-only kind) | 11 |
| §12.1–12.5 | metadata federation, ontology sync, hash network, specialised instances | mesh §7.17/§7.18 is **ahead**; hash network new | 12 |
| §12.6 | ActivityPub | none (rejected in the crosswalk) | 12 |
| §13.1–13.5 | 5 players/viewers | **refused by the respec** — no player, no embed, no proxy. Reading *metadata* is a different surface and it is in: a comic's chapter list and page count are `content_item_chapters` (phase 3) | — |
| §14 | facial recognition, community ID, profile claiming | identification board (untouched); local models live in `feature-04-identification-federation.md` §8, where a model's output is a suggestion | 4 (feature 04) |
| §15.1–15.3 | REST+GraphQL API, integrations, exports | GraphQL, webhooks (13a: the read-only MCP server, `cmd/stash-box-mcp` + `internal/mcp`, four tools over the existing READ queries); no REST/RSS/exports yet (13b–13d) | 13 |
| §16 | anti-doomscroll design + transparency report | none | 16 |
| §17 | CSAM/DMCA/age/consent policy, appeals, elected mods | **the notice path (slice 14.1) and, from slice 14.2, the published policy text plus the appeal a DIFFERENT moderator hears** — still nothing for hash matching, age verification, a registered agent or elected moderators, each named in §9 | 14 |
| §18 | premium, creator subs, ads, data licensing | none (MIT) | 15 |
| §19 | service split, ES/Meili, vector store, NATS, MinIO, CDN | single Go binary, PG + pg_search. MinIO and the CDN are phase 10's (gated, off by default); the vector store is phase 7's | 7 |
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
| **Content is hosted, proxied and transcoded** (media plane) — **REVERSED AGAIN, 2026-10-15: restored as a GATED capability, off by default** | §7.19 "the P2P content layer — REJECTED", which was right about the risk | The first reversal's mistake was the DEFAULT, not the design: it made every fork a host whose operator had decided nothing. The capability is now specified and planned (phase 10) behind `content_delivery`, false in every shipped config, read by a gate whose refusal names the key, stated in the startup log, and checked by `scripts/check-no-content-plane.py` — which still fails the build for third-party embedding or proxying anywhere outside `internal/service/mediaplane/`, whatever the switch says. **The rendition table `content_item_media` STAYS** (a row is a URL an editor recorded, and the completeness score reads it — see the plan's respec section for how an earlier draft of this very sentence was wrong). What the sequencing costs: nothing is built yet, and turning the switch on is the operator's written decision that this instance is a host. |
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
   feature's local models; the media plane's toolchain and CDN, phase 10) has a record of the
   decision and a fallback, per the project's own habit of writing down what it deliberately did
   not install. **The media toolchain and a downloader are phase 10's**, gated by
   `content_delivery` (off in every shipped config): until slice 10c or 10f is started, neither is
   installed, and installing one is a decision with a fallback written down first.

## 6. Non-goals

Functionality IS dropped, by owner decision: **a general discussion board** (slice 8f), for the
reasons the respec section gives. **The content plane is NOT dropped — it is gated**: hosting, the
players, the PWA and any embed or proxy of a third party's content are phase 10, off by default
(`content_delivery`), with third-party embedding and proxying still refused outright by the build
gate. What is explicitly *not* in this spec: rewriting `scene` into a `content_item`, breaking the
Stash desktop app's GraphQL contract, and any phase beginning before its predecessors are green. The
phase order is a dependency order, not a preference order.

## 7. Growth: invites, referrals and cohorts (phase 11)

**An invite allowance is DERIVED, never stored.** How many keys a member may hold usable at once follows
from their trust level (`invite.AllocationFor`: 0 at Public, then 1, 3, 5, 10, 25), counted from the key
rows each time. **Zero at the level every registered account starts at** is the rule's whole point: an
account created five minutes ago may not mint invite keys, for the same reason L4 costs 3× L3 — the
capability has to be earned by contributing. A stored allowance would be a second answer to a question the
level already answers and would drift from it the first time a level changed.

`users.invite_tokens` stays, and the distinction is deliberate: an operator's discretionary grant is a fact
about an operator's decision that no level can reconstruct, and it is how an instance invites someone with
no standing yet. Slots are spent before tokens, so a grant is not burned by a member who did not need it.
The INVITE / MANAGE_INVITES roles are the operator's delegation and are exempt from both, which is stated
here so the exemption is a decision a reader can find rather than an accident of ordering.

**A refusal names which allowance ran out.** "No invite tokens available" and "your level allows this many
keys and they are all in use" have different remedies — ask an operator, or contribute — so they are two
sentinels, not one message.

**A referral pays the invitee's referrer once, when the INVITEE reaches the first level above the one
everyone starts at, one level of depth.** Never at signup: an account that registers and leaves is not
growth, and paying at signup pays for an email address. The payment is a trust EVENT keyed to the invitee
(`kind=invite_activated`, `entity_type=user`, `entity_id=<invitee>`), so the existing dedup key is what
makes it once-ever: the level is recomputed on every contributing event and can oscillate through decay,
and a payment that depended on the level being observed at the right moment would pay twice or never. One
level of depth means the grandparent is paid nothing, asserted by identity. A self-invite pays nobody.

**A fast-track is a points event, not a level override.** A cohort campaign awards its members a one-time
trust event keyed to the campaign, and the level then follows from the single curve in
`internal/service/trust`. A campaign that set a level directly would be a second implementation of "what
level is this person" — the exact thing the trust phase exists to prevent.

**Built 2026-10-15 as `invite_key_events` + `User.invite_stats`** (owner-only, reachable through `me`;
counted across every key the member has ever minted, INCLUDING keys that no longer exist, which is what the
denormalised referrer on each event is for). A signup is recorded in the transaction that creates the
activation token and an activation in the one that creates the account, in both cases before the key is
consumed — consuming the last use deletes the key, so that is the only moment both the referrer and the
account are in hand. A kind with no events reads **0**, which is why the mapping starts every kind at zero
and ignores a kind it does not know.

**Built 2026-10-15: the landing page is `GET /invite/<code>`** — unauthenticated, records a visit and
records nobody (a count, a kind and a time: no address, no agent, no cookie, no account), serves the SAME
document and headers for a live, expired, spent or invented code (only the echo of what the visitor typed
differs), and counts no visit to a key nobody could have used. The link it shares, the link the QR encodes
and the path the router serves are one string (`invite.LinkURL`), built from `host_url` when the operator
has configured one and otherwise from the address the request arrived at — a configured value wins because
a link shared from a staging hostname must not send somebody to staging, but a default deployment must
still produce a link somebody can be handed, and `host_url` alone produced a bare path; the QR is SVG
geometry rendered server-side (`InviteKey.qr_svg`), verified by decoding back with an independent decoder.

## 8. Interoperability: the read surfaces (phase 13)

**The programme's interop is READ-ONLY, and both halves of it are decided by one question: what does the
surface add?** Nothing, in both cases -- and that is the design, because a catalogue of references whose
whole posture is one private graph is only as safe as its widest read path.

**13a, the MCP server (`cmd/stash-box-mcp` + `internal/mcp`), is a SEPARATE PROCESS on stdio.** Every tool
maps to a query that already exists and already carries `@hasRole(role: READ)`, so the slice adds no read path
and no authority; the binary is launched by the client and talks to the instance's own GraphQL API with the
operator's key, so the instance's authorization is the only authorization and there is nothing new listening.
The published JSON schema is DERIVED from the validator's argument list -- a hand-written schema beside a
hand-written checker is two descriptions of one rule, and its failure is a tool that advertises an argument it
ignores, which answers a different question than the caller asked with nothing saying so. A GraphQL refusal is
returned as a tool ERROR carrying the instance's own words, while `data.findScene: null` stays a successful
absence: "no such scene" and "you are not authorized" are different answers and `isError` is what tells a
client which it got. **A finding worth the slice: `FingerprintHash` is HEXADECIMAL read in base 16**, so a
decimal rendering of the same number names a different fingerprint and comes back empty -- which reads exactly
like "this instance has never seen this file". The repo paid an afternoon for that once; the tool refuses a
non-hexadecimal hash naming base 16, and says the same in the description of the tool AND of the argument,
because a tool a model calls has its description READ.

**13b, a feed per saved search (`GET /feeds/saved-search/<token>`), is a bearer in a URL, and the token is
designed for the place it has to live.** A feed reader sends no headers, so the credential is in the address
-- which ends up in the reader's config file, in a proxy's log and in the `Referer` of every link clicked out
of the feed. So: ONE token per saved search rather than the member's `api_key` (a leaked URL must cost one
search, not an account); ONLY THE SHA-256 IS STORED (`saved_searches.feed_token_hash`, migration 140), which
makes "that URL is shown once" a fact about the schema rather than a policy it contradicts; and enabling again
ROTATES, so a member who pasted their feed somewhere has one remedy and it is the gesture they already know.
**Every miss is the same 404** -- a token never minted, a token whose feed was disabled, a token whose search
was deleted and a path that is not a token at all are one answer, body included, because 401 would confirm a
search exists and 403 that it belongs to somebody else, and a private instance must not be a scanner for which
searches exist. A valid token whose search matches nothing is a valid feed with zero items: that is a fact
about the catalogue, not about the token, and a reader handed a 404 for it would drop the feed as broken. The
records are the OWNER'S OWN VIEW -- the handler runs the stored text through the same query path a live query
takes with the search's owner placed in the context, because that path reads the current user (`favorites:`
means the OWNER's favourites; the rating ceilings are keyed on the reader), so a feed that ran as nobody would
not merely be imprecise, it would fail. The response carries `X-Robots-Tag: noindex` (a bearer on an indexed
page is a public bearer), `Cache-Control: private` (a shared cache would hand one member's search to the next
caller) and `Referrer-Policy: no-referrer` (a reader clicking an item would otherwise send the token along with
it). Items carry the record's id as a **non-permalink guid** -- a title changes and a host can move, and a
reader keying on either would show the same record twice -- a link to the record, a `pubDate` in RFC 822 with a
NUMERIC zone (`time.RFC1123` writes the word "UTC"; Go behaviour, unreadable date, whole feed gone), and a
plain-text description of who is in it, who made it, when it came out and how long it is. No enclosure, no
frame, no media URL: a feed that carried one would be the content plane with a polling schedule.

**13c — the export, and why what it leaves out goes in the envelope rather than in the document.**
`exportScenes(ids: [ID!]!): HydrusExport!` renders records this instance holds as Hydrus import JSON. A GraphQL
field rather than a route, for 13a's reason (one serving surface, one auth story), inheriting
`@hasRole(role: READ)` -- and **bounded to 100 ids, with no query language, no browsing and no "everything"**,
because an export renders records the caller ALREADY HAS; a field that took a query would be a second read
surface with its own paging, ceilings and rate limit to keep in step. The document is a BUNDLE of sidecars keyed
by record id, each value exactly the shape Hydrus documents for a JSON sidecar -- `metadata` holding lists of
strings under `urls` and `tags`, with notes joined `name: text` -- read from Hydrus's own sidecar documentation
rather than recalled, because a format guessed at produces an export that loads into nothing and the failure
looks like the other program's bug. Every string emitted as a tag is namespaced (`studio:`, `performer:`,
`tag:`, `code:`, `date:`), so an importer can take exactly what it wants and leave the rest; a title or a
description is a **note, never a tag**, since routing one into a tag store gives the file a tag for every
sentence in its name; the record's own tags are sorted while its addresses keep the record's own order, because
a document whose bytes change between two runs over the same record is a document nobody can diff. Fingerprints
are deliberately NOT carried: the documented sidecar has no hash field, and the one place Hydrus trusts a hash
is as the key of a file it hashed itself -- a client-reported md5 in a note would be a false key in exactly the
place that matters.

**A denied record is ABSENT, and the envelope is where the caller learns it.** The payload carries
`record_count` and `omitted`, and the omission list is ONE list: "this instance has no such record" and "this
instance will not serve it" are one answer from here, because telling them apart would confirm the content
exists. The absence is produced by the loader's own gate -- `LoadIds` -> `deniedSceneIDs`, the same code that
makes a record absent from a search -- rather than by a check written beside the new feature, and the mutation
battery proves that by neutering the gate at the LOADER and watching the export's own test go red. Nothing is
annotated inside the document, because an export that carried a taken-down record with a flag on it would be a
copy of that record in somebody else's client.

**Measured 2026-10-15 — where the round trip actually lives, and the plan was wrong about it.** The plan
predicted that feeding an exported address back through `submitContentURL` would answer `duplicate`. It does
not: dedup is on the ingest QUEUE (migration 109's partial unique index over `url_normalized` for rows that are
pending, running or done), and a record's own `scene_urls` rows were never queued, so the submission is
`queued`. A test written to the old wording would have passed only on addresses an earlier test happened to
queue, which is a test of the queue. The round trip that exists is the stronger one: the job is drained, the
extractor is an instrument that FAILS the test if it is called, and the queue row must end `done` with outcome
`attached` and `confidence` NULL -- the path settled from the database alone. **The live deployment widens the
gap between the two tables:** `scene_urls` holds 1,171,306 rows and `content_sources` holds 0, because
migration 108's projection is a trigger on writes made after it and this instance's catalogue predates it --
so on that deployment a submission of a record's own address is NOT recognised by the pipeline, and
`walk-13c-live.py` reports that measurement instead of fetching somebody else's page to make a green tick.

**13d — the limit, and why the unit is what the caller PRESENTS.** `internal/ratelimit` counts requests per
credential and refuses the ones over the limit, mounted on the two public surfaces (`/graphql` and the feed
route) and on nothing else -- the SPA, the assets and the invitation landing page are not a caller spending an
API budget, and a limit that covered them would be felt by a member loading a page. **The unit is the API key
for GraphQL and the feed's own token for a feed URL**, because the plan's reasoning is exact: a limit keyed on
the address punishes everyone behind a shared one (a house, a university, a VPN) and a limit keyed on the
ACCOUNT punishes a member for using two clients. The address is used only as the fallback for a caller that
presents NOTHING, where there is no identity to share -- which is also why a feed reader is counted by its
token: it sends no headers, so a reader counted by address would share one bucket with every other reader
behind the same proxy.

**The numbers, and where "off" lives.** 600 requests per minute per credential with a burst of 120, both
configurable; **ZERO MEANS OFF**, expressed as the number rather than a boolean beside it, because a switch
next to a number is two places to state one thing and the number already says whether there is a limit. On by
default, UNLIKE the content switches, and the difference is what each protects: those gate a capability an
operator adopts deliberately, so the safe default is off; this one protects the instance from a caller that
loops -- an accident or a scraper -- and a limit that is off by default protects nobody. A refusal is 429 with
`Retry-After`, `X-RateLimit-Limit` and `X-RateLimit-Remaining` -- on the allowed responses too, because a
client that only learns the limit when it is refused will be refused again -- and a body of
`{"errors":[{"message": "... allows 600 requests per minute ... Retry in 5 seconds."}]}`, which is the shape
every other error on this API already has. That shape is the defect the plan names: a 429 returning HTML or a
bare string is a client bug reported as a server mystery.

**Buckets are memory, and they forget on purpose.** A row per request would be a write per read on a read
surface, and a limit whose history survives a restart is a limit an operator cannot lower in a hurry. A restart
resets every bucket, which is honest: the question a bucket answers is "how fast is this caller going RIGHT
NOW", and that question has no yesterday. The table is bounded by a hard cap with two passes -- first the
credentials that have refilled (a bucket nobody has touched since it refilled carries no information, so
forgetting it costs the limit nothing), then, if that frees nothing, the least recently used. **The trade is
stated rather than hidden:** a caller who invents enough credentials can push a real one out of the table and
get its own burst back, at a cost of thousands of requests per victim -- against which the alternative is
memory that any client can make this process allocate without limit.

**Two defects this slice's own tests found, both in the bound.** Making room on EVERY request is destructive
rather than merely wasteful: at the cap, every request evicts somebody, so a busy instance's table spends its
time throwing away the credentials it is supposed to be counting -- room is now made only for a credential that
is not already tracked. And the least-recently-used search was seeded with the current instant, which made every
bucket whose last touch was that same instant ineligible -- so a flood of credentials arriving in one moment
never evicted anything while the map sat over the cap looking like the pass had run.

**NOT built, and named rather than implied:** there is still no REST surface, no user-facing export UI, and
no consuming of external MCP servers (that is a provider, per `feature-04-identification-federation.md` §8).

**Counters are row counts, and the rows must OUTLIVE what they count.** A scan is a row, a signup is a row
for the key, an activation is a row that was consumed; a member sees all three because "someone looked",
"someone gave an address" and "someone joined" are three different facts, and only the last is a person.
No counter column exists for any of them. **Corrected 2026-10-15 (measured):** the rows this section named
are gone — `pending_activations` was dropped by migration 37, the signup and activation rows are both
`user_tokens` rows that activation deletes, and a key whose last use is consumed at activation is deleted
too (on a required-invite instance; on one that does not require invites a key was never consumed at all,
which is the invitation-integrity defect fixed the same day), so a member's key that signed somebody up
can leave nothing to count. The counters therefore rest on a
durable per-event row carrying the REFERRER's id independently of the key (`invite_key_events`), with no
cascading delete: a counter that dies with the key it counts reports zero for the one key a member actually
asks about. **A scan records nobody** — it is an unauthenticated GET, so it carries a count, a kind and a
timestamp, and never an address, an agent or an account; signups and activations carry the account they
became, and what the member is shown stays counts. An expired or spent key gets the same landing page as a
live one (minus the scan row), so the page cannot be used to probe for a valid key.


---

## 9. Moderation and legal: the notice, the appeal, and the text (phase 14)

**What phase 14 owes phase 10, stated as the hard constraint it is.** The notice path itself is built
(`takedownContentUrl` / `blockContentHost` / `releaseContentBlock`, `content_source_block`, the refusal
consulted by both the submit path and the drain — §25 line above and the plan's slice 14.1). What phase 10
waits for is the **TEXT**: a published statement of what this instance does with a notice, what it will not
do, what a PEER instance is asked to do, and what a person can do about a decision they disagree with. The
ordering is not bureaucracy. The moment this instance stores a byte of somebody's work it is a host, and a
host whose operator improvises a response to the first notice it receives has no policy — it has a mood.

**Written per-instance AND per-peer, never per-platform.** What a peer must do when it receives a takedown
forwarded by this instance, and what happens when it refuses, is a relationship between two instances; a
platform-wide policy is a claim about machines this operator does not administer, and this programme
federates with people it does not administer by design.

**The appeal is the one part of §17 that is not machinery for a host, and it is the part with a rule worth
enforcing in code.** A takedown that can only go one way is not a policy, it is a verdict. So an appeal is a
ROW (never a counter, for the reason §8's counters are rows), and three things are structural rather than
procedural:

- **A different moderator, asserted by IDENTITY.** The operator who recorded the block cannot decide the
  appeal against it, and the person who filed it cannot decide it either. Both are comparisons between
  `users.id` values, not role checks: a role check would pass for the same human holding a second role, which
  is the failure mode this rule exists to prevent.
- **A granted appeal RELEASES the block, through the one release path an operator's own release already
  uses.** Two code paths that both unblock a host eventually disagree about what unblocking means, and the
  audit trail is where the disagreement shows up first.
- **The appeal SURVIVES the block it contests.** Release deletes the block row, so the appeal's reference to
  it is `ON DELETE SET NULL` with the block's `(kind, value)` denormalised onto the appeal. This is §8's
  invite-counter lesson one layer down: a record that dies with its subject reports nothing about the one
  decision a person wants to read.

- **The blocker is carried ON the appeal, not read back from the block.** This is the fourth structural
  point and the one that took a second reading to see: "a different moderator" is a comparison against the
  operator who recorded the block, and a release DELETES that block row — so a rule read from the block is
  unenforceable in exactly the case that matters, the appeal still pending after somebody released the block,
  where the sole decider left would be the operator whose decision is in question. `blocked_by_id` is
  therefore a column on the appeal, copied from the block in the same transaction that reads it — the last
  moment the block is guaranteed to exist — and the rule reads the appeal, which nothing deletes.

**What a person appeals AGAINST is the `(kind, value)` key, not an internal id.** The block list is an
operator surface; a member who was refused knows the address they submitted, not a row id, and an appeal
that can only be filed with an id nobody shows them is an appeal nobody files.

**Half-states are unrepresentable, not merely unlikely.** Outcome, decider and decision time travel together
under CHECK constraints, so "decided with no outcome" and "granted by nobody" cannot be stored — the same
shape as phase 9e's archive pair and §8's key-must-have-an-object rule.

**The surface, and who may reach it.** `fileContentAppeal(input)` is gated on being signed in and NOTHING
else — an operator cannot know whose submission an address was, and requiring an appellant to prove ownership
turns a complaint about a block into a claim about a person. `decideContentAppeal(id, outcome, note)` is
ADMIN, matching `takedownContentUrl`: the same authority answers for the decision and for the objection to
it, and the rule neither directive can express (the decider is neither the blocker nor the appellant) is the
service's, by identity. `contentAppeals(pending_only, limit, offset)` is ADMIN and defaults to the pending
queue, because an appeal nobody can list is an appeal nobody answers. Every decision-derived field
(`block_id`, `outcome`, `decision_note`, `decided_at`, `decided_by`) is NULLABLE and null until the moment it
exists: a pending appeal that reported an empty-string outcome would read as a decision to any client's
`if (outcome)` test. **The nulls live in the MODEL's types (`*string`, `*time.Time`), not in resolver
methods** — the first implementation did the latter and the deployed instance sent `"outcome": ""`, because
gqlgen only generates a resolver for a field whose model type does not already match the schema, so a method
added beside an over-wide field is dead code the compiler cannot see. A wire contract that a client reads has
to be in the type that carries it, and only a live read of the bytes shows which of the two a client gets. `kind` and `outcome` are STRINGS rather than enums, like `ContentSourceBlock.kind` and
for its recorded reason — the closed vocabulary is a CHECK constraint today, and publishing a GraphQL enum
before it settles makes every later addition a breaking schema change.

**A policy with no address to send a notice to is not a policy, and the page says so.** The contact is a new
`abuse_contact` config key, empty by default; when it is unset the served page names the gap in as many words
and says the instance should not be storing content until its operator sets it. That is phase 14's ordering
made visible in the artifact itself rather than only argued here — and it is the one page whose own state is
part of its text.

**The text is SERVED, not filed.** `GET /policy` is public, unauthenticated and static, like the invite
landing page: a notice path nobody can find is not a notice path, and a complainant should not need an
account to read what an instance will do with their notice. The transparency REPORT the policy points at is
sliced separately in phase 16 (slice 16a, §16 below): the `mod_audit` rows already were the report's
contents, and serving them is what turned the promise into an accounting.

**NOT built, and named rather than implied:** CSAM hash matching against an external list (nothing here
accepts uploads, so there is no ingest to match against; the refusal is at the source layer, where a refused
source is simply not fetched); a registered DMCA agent, counter-notice paperwork and a designated-agent
registration (paperwork about a host this programme is not); performer age verification as a gate (nothing
here publishes performer content); elected moderators (a governance question about a membership, not a code
shape, and it needs members first).


## 16. The transparency report, served (phase 16, slice 16a)

`GET /transparency` (HTML) and `GET /transparency/json` (the same figures, machine-readable). Public,
unauthenticated, static, mounted before the SPA catch-all, deliberately NOT `noindex` for the same reason the
policy page is not: a report nobody can find is not a report.

**Every figure is a count over this instance's own tables**, because a transparency report is precisely the
artifact `docs/goal-check.py`'s rule is about — *"Never trust a count written in a document. Recompute it."*
There is no number on the page that a document supplied, the JSON exists so a reader can check the figures
mechanically, and the integration test recomputes each one with its own SQL and requires the report to agree.

**Two kinds of number, kept apart as separate structures rather than separate sentences.** The commonest way a
report of this kind misleads is by mixing a state with a period's activity, so:

- **In force now** is state with NO window, read from the content tables (which have no retention): refusals in
  force, catalogue records out of service, appeals filed / pending / decided / granted / denied. A refusal
  recorded two years ago is still refused, and putting it inside "the last 30 days" would understate it.
- **In the window** is activity, read from `mod_audit`, which HAS a retention
  (`mod_audit_retention_days`, 30 days by default, applied by `DeleteExpiredModAudits` when it is `> 0`).

**The window IS the retention, not a second configuration key.** Two numbers naming one period is one number
that will disagree with itself, and the retention is the one that decides what an activity count can possibly
see. A retention of `0` or less means nothing is deleted, so the report says the records are kept
indefinitely and reports `records_kept_forever: true` with a nominal decade-wide window rather than pretending
to a bound it does not have; a negative value is treated as "off", like every other `> 0` guard in this
repository, and not as a window that ends in the future.

**A release is not a new refusal, and not double.** Refusals and releases are counted apart although the
release reuses the takedown's action (migration 134's "one value per decision"), and both are restricted to the
BLOCK-side actions. That restriction is load-bearing: a granted appeal writes TWO rows — the release it
performs through `releaseKey`, and the decision it records, which also carries `released = true` — so counting
every such row reports one release as two, in the exact case an appellant checks the figure against. The first
run of the integration test returned five releases for three facts and is what found this.

**Denials are reported as denials.** The appeal figures come from the appeal rows rather than from the audit
trail, so a granted appeal against a block somebody had already released does not inflate this instance's
reversal count.

**Named as open, so a reader of this section is not misled about the phase:** slice 16a is the report. The five
key pages, the §23 metrics as first-class counters, keyboard navigation, alt text, high contrast, reduced
motion and the WCAG 2.1 AA target are phase 16's remaining work and are NOT claimed here — the antidoomscroll
commitments are served as part of the report's own next slice, with the switches that enforce them, rather
than asserted in prose.


## 16.2 The instance metrics, defined once and recomputed (phase 16, slice 16b)

`instanceMetrics` on the GraphQL API, `@hasRole(role: ADMIN)`. This section defines the counters, because a
metric is a number plus a DEFINITION and a number without one is not a measurement: two readers of
`scenes_total` who disagree about whether it includes deleted scenes are reading two different figures, and
nothing in the response would tell them so. So every metric is served with its definition attached, and the
definition lives beside the column that computes it in one place — `internal/service/metrics/metrics.go` —
where adding a metric means wiring a column explicitly, and the compiler refuses the wiring without it.

**The §23 list is not in this repository, and this section does not pretend to be it.** The owner's draft
names "17 metrics with targets"; the draft is not here, so this slice measures WHAT THIS INSTANCE CAN
ACTUALLY COUNT — from tables the programme already writes — and names the two things it deliberately does
not do:

- **No invented targets.** A target is a statement of the owner's intent about what the instance SHOULD do,
  and fabricating one would put a claim about somebody else's plans into a served response. The figure is
  reported; the target stays an owner decision, recorded here as such rather than guessed.
- **No product-metric machinery (no event log, no session tracking, no per-action counters).** A counter that
  is incremented at a mutation site is a second write path that can disagree with the table it counts, and
  every figure here is instead DERIVED from the rows the features already write — the same discipline as §16.1
  and the same rule from `docs/goal-check.py` ("Never trust a count written in a document. Recompute it.").
  What this buys is that a metric cannot be wrong about the record without the record being wrong, and what it
  costs is stated in the metrics themselves: **activity is measured by `users.last_api_call`, the only
  activity signal this instance keeps**, and each such metric's definition says so.

**One window, named once.** Every windowed figure uses the same 30 days, served as `windowDays` alongside
`since`, because a response where each metric had its own window would be a table nobody could read across.
The definitions that are NOT windowed are the totals, and the response separates them by saying so in each
definition rather than by grouping them: a reader scanning one metric needs its own sentence, not a section
heading three screens up.

**The metrics, and what each counts** (served with these definitions):

| key | counts |
|---|---|
| `members_total` | Accounts, excluding the instance's own bots (`user_roles.role = 'BOT'`) and the `[deleted user]` placeholder row. |
| `members_active_window` | Of those, the ones whose `last_api_call` is inside the window — the only activity signal this instance keeps. |
| `members_created_window` | Of those, the ones whose account was created inside the window. |
| `members_invited` | Accounts that arrived through an invite (`invited_by` is set) — phase 11's chain, counted rather than asserted. |
| `scenes_total` | Catalogue scenes not marked deleted. |
| `scenes_created_window` | Of those, the ones created inside the window. |
| `scenes_with_sources` | Non-deleted scenes with at least one recorded source address — the aggregation coverage this programme exists for. |
| `scenes_with_fingerprint` | Non-deleted scenes carrying a fingerprint. |
| `fingerprints_total` | Fingerprint rows. |
| `tags_total` | Tags. |
| `tag_implications_total` | Tag implications (phase 4's ontology). |
| `sources_total` | Recorded source addresses, whatever their health. |
| `sources_checked_window` | Source addresses whose health was last checked inside the window. |
| `sources_unknown` | Source addresses no health check has ever looked at (`status = 'unknown'`) — `0` here means the sweep has seen everything it holds. |
| `edits_pending` | Edits awaiting a decision (`status = 'PENDING'`). |
| `edits_decided_window` | Edits that reached a decision inside the window (`closed_at`). |
| `identification_candidates_pending` | Candidate identifications waiting for a moderator. |
| `invites_issued` | Invite keys generated (phase 11). |
| `invites_unused` | Of those, the ones with no use recorded. |

**Why `instanceMetrics` and not a metrics PAGE as the primary surface.** The report (§16.1) is addressed to
the public and is served as a page; this is an OPERATOR surface about the instance's own operation, so it is
behind `ADMIN` and shaped as data a page or a script can render. The page came second (`/metrics`, slice 16c)
and computes nothing of its own: it renders the query's list, which is what keeps every metric to one
definition.

**The page's contract, and the three things it refuses to do.** `/metrics` needs `ADMIN`, like the query
behind it, and its nav entry is shown to exactly the people who can read it rather than to everybody and then
refused. On the page:

- **it does not invent a label for a figure.** `members_total` is the name a reader matches against a report,
  a log line and the API, and a second name invented for the screen would be a second thing to keep in step;
- **it does not hide a figure that arrives without a definition.** Such a figure cannot be read, and dropping
  it would let the page look complete while a number went unexplained, so it is shown with the fact that its
  sentence is missing;
- **it does not decide for itself which figures are windowed.** Every key containing `window` must say so in
  its definition (§16.2's own unit test enforces that), and the page marks exactly those keys, so a figure
  bound to a window cannot be read there as a lifetime total.

The refusal is also distinguished from a fault: a member without the role is told the figures need `ADMIN`,
not that something went wrong, because sending somebody to look for a fault that does not exist is its own
kind of wrong answer.


## 16.3 Accessibility: the target, the tool that measured it, and what a green gate does not mean
(phase 16, slices 16d and 16e)

Phase 16's verification sentence is two clauses: *"keyboard-only navigation reaches every control on each key
page; the a11y target is recorded with the tool that measured it."* This section is both clauses, and it is
written as a test rather than as a policy because a policy cannot fail.

**The target is WCAG 2.1 level AA**, and **the tool is axe-core**, run in a real browser through Playwright
(`@axe-core/playwright`), over the four tag sets that make up that level: `wcag2a, wcag2aa, wcag21a, wcag21aa`.
The tool is named because the phase asks for it to be named: "the a11y target is recorded with the tool that
measured it" means a reader must be able to re-run the measurement, and `e2e/tests/a11y.spec.ts` is that
measurement. A run with `A11Y_REPORT=1` writes axe's own findings, page by page, to `e2e/a11y-report.json`.

**The pages are the five the phase names** — home dashboard, search results, content detail, tag page,
performer page — **plus `/metrics`**, which this phase added. Each is opened with data in it: an empty search
page and a populated one have different DOM, and the populated one is what a reader gets.

**What a green gate does not mean, said out loud because a green accessibility gate is easy to over-read.**
axe decides only the subset of WCAG that a machine can decide from the DOM. It can see that an image has an
`alt` attribute; it cannot see whether the sentence in it means anything. It can see that a button has a
discernible name; it cannot see whether the name describes what the button does. So this gate is a **floor,
not a certificate**, and the second check exists to cover the half axe cannot reach.

**The second instrument is the keyboard walk.** axe cannot tell whether a control that is focusable in the DOM
ever actually receives focus, and that difference is a real defect class: an element with a positive tabindex,
a trap, or a stray `tabindex="-1"` all leave a control that a keyboard user cannot use while the markup looks
correct. The test enumerates every control the DOM says is focusable — excluding ones the browser itself puts
beyond the keyboard (`:disabled`, `[tabindex="-1"]`, and anything inside an `aria-hidden` subtree, because a
disabled control is not a control a keyboard user was promised) — then presses Tab until every one of them has
received focus, and fails naming the ones that never did.

**The subject is counted before its reachability is asserted.** "Every control is reachable" is vacuously true
of a page that rendered no controls, which is exactly what a broken selector produces; so the count is asserted
`> 0` first, and the same discipline applies to the axe half, where a run that analysed zero nodes and a run
that analysed three hundred both print "0 violations" — which is why the report records `passes` and
`incomplete` counts beside the violations.

**What the first run found** (2026-10-16, six pages, 12 tests, before any of it was fixed):

| rule | impact | pages | what it was |
|---|---|---|---|
| `button-name` | critical | all six | the notification bell: a `<Button>` inside a `<Link>`, with only an icon in it — two interactive controls in one tab stop and no accessible name on either |
| `link-name` | serious | all six | the same element, seen as the anchor around the button |
| `label` | critical | all six | the header search: react-select numbers its own input id (`react-select-2-input`) when `inputId` is absent, and an input with a generated id has no label to point at it |
| `color-contrast` | serious | all six | the instance's own theme: muted text on cards, the active nav item, card headers and the secondary line of each performer card |

The three structural violations were defects in this repository's own markup and are fixed in it: the bell is
now one link carrying the count in its accessible name (`Notifications, 3 unread`), and a search field whose
caller supplies no `inputId` now gets a real label and a stable id. Contrast is a property of the theme rather
than of the markup, and §16.5 records what was done about it and what was left.

## 16.4 The keyboard contract, in two halves

Tab reaches every control the DOM says is focusable -- and a tablist's inactive tabs are not reached by Tab,
which is CORRECT: the WAI-ARIA tabs pattern puts one tab in the tab order and moves between them with the arrow
keys. A check that had insisted on Tab-only reachability would have flagged three working pages, and a check
that simply excluded tablists would have left the widget unverified. So the gate does both: controls inside a
`[role="tablist"]` are excluded from the Tab walk, and a second test presses ArrowRight through every tablist
on every key page and requires each tab to receive focus. The exclusion carries its own proof.

Both halves count their subject first. "Every control is reachable" is vacuously true of a page that rendered no
controls; "every tab is reachable with the arrow keys" is vacuously true of a tablist with no tabs.

## 16.5 The palette: the same page in the mode a developer is not looking at

Two of the four defects this phase's gate found were invisible in the dark palette, which is the one the
application was written in: `.text-muted` compiled to a static `#bfccd6` (1.53:1 on the light page) and `.card`
hard-coded `#30404d`, the dark surface, so in light mode a card stayed dark while its text followed the theme
(1.54:1). Neither is reachable by a unit test of the tokens -- `theme.test.ts` already asserted every
`theme.ts` value against its `theme.scss` counterpart and the text/surface pairs against each other; what it
could not see was a component that never asked for either.

The rule this slice adds to the token layer, and the reason it is a rule rather than a fix: **a colour that is
TEXT on a surface must be asserted against both backgrounds it can sit on.** The dark palette's link colour was
`#48aff0`: 5.96:1 on the page background, and **4.41:1 on a card surface** -- passing where a reader usually
sees it and failing the moment a link lands on a card. `theme.test.ts` now checks `--sb-link` against both, in
both palettes, and the gate found the same trap independently from the other end (the outline button's text at
2.36:1 on a dark card). The tokens are `#6cc4f2` in dark and `#0f628f` in light.

**Order is part of the fix.** The rules that carry the link token have to come AFTER bootstrap's, because they
are deliberately the same specificity and win only by being later; placed beside the tokens they lost and the
gate kept measuring the compiled `#48aff0`. The file says so where they are.

**A token is only correct where the surface under it is token-driven.** Substituting a token for a static
colour is right where the surface follows the palette and wrong where it does not, and the same substitution can
be both in one file. `.navbar-dark` is a FIXED dark bar (`$secondary`) in both palettes, so its links and its
palette-switcher buttons need a fixed near-white: giving them `var(--sb-text)` produced `#182026` on `#394b59`
-- **1.82:1, fifteen links and two buttons on every route** -- with the six key pages still green, because the
dark palette was fine and light mode is the half nobody looks at. `nav-link` and `.navbar .btn-outline-*` are
therefore deliberately NOT tokenised, and the file says why at both sites. The same reasoning from the other
side: `.alert-secondary` was a fixed light `#d7dbde` panel, so in the dark palette it held a dark-mode link on
a light panel (1.39:1); a surface that does not follow the palette cannot hold text that does.

**What this slice does not claim.** The light palette has not been rendered across all 33 stylesheets; §16.6 is
the sweep that measures the routes the application serves, and it is not the same claim. `forced-colors` is
audited separately in §16.8, because it is a different rendering mode and not a third palette.

## 16.6 The sweep: every route, both palettes (phase 16, slice 16f)

The six key pages are audited with data in them (§16.3), which is the right instrument for "does THIS page
work". It is the wrong instrument for "the stylesheets are shared": a token that is broken in light mode shows
up on whichever page happens to render the rule, and six hand-picked pages cannot name the pages nobody
picked. So the gate also walks **every route the SPA serves** -- twenty of them -- in both palettes and runs the
same WCAG 2.1 AA rule set on each. The routes are counted before the result is trusted
(`expect(audited).toBe(SWEEP_ROUTES.length)`), because "every route is clean" over zero routes is the vacuous
form of the claim.

It paid for itself on the first run: five routes were dirty in the dark palette and five in light, none of them
among the six the phase had already audited, and every one of them a real defect:

- `/performers` and `/edits` -- unlabelled filter selects and icon-only sort buttons (three react-selects, six
  `<Form.Select>`s of which five had a visible label that pointed at nothing, and the direction toggles).
- `/users` -- the edit control was a `<Button>` inside a `<Link>`, reported eleven times, once per row, as both
  `button-name` and `link-name`. The same shape as the notification bell in §16.3, on a page the earlier gate
  never visited.
- `/image-types` -- 27 muted descriptions on the drag-list strip at 2.61:1.
- `/lists` -- an in-text link distinguished by colour alone (`link-in-text-block`), and a
  `.btn-outline-secondary` used as text: `$secondary` (`#394b59`) at **1.59:1** on the dark page background.
  That last one is why `.btn-outline-secondary` now takes the text token -- and why the navbar's instance of
  the same class takes the fixed near-white instead (see §16.5).
- The first run of the sweep found two of these in the LIGHT palette only, which is the half of the claim
  §16.5's closing paragraph had explicitly left unmeasured.

The sweep's failure message carries the measured colours (`#5c7080 on #ededee = 4.39`), because a
`color-contrast` finding without them is a finding somebody has to reproduce by hand before they can act.

## 16.7 WCAG 1.4.11 non-text contrast, measured by hand (phase 16, slice 16f)

axe has no rule for 1.4.11 in this rule set, and that is exactly how a 2.36:1 outline-button border shipped
unnoticed: nobody computed it. So the gate computes it, for a named list of UI components (outline button, text
input, select, active tab): walk up for the nearest ancestor with a non-transparent background, composite the
rgba layers down to the page, and take the contrast ratio of the component's border colour against that
surface. 3:1 is the requirement, and a component that does not render on the audited page is reported as NOT
RENDERED rather than silently skipped -- the assertion also requires at least one to have been measured, or
"no weak boundaries" would be true of an empty list.

## 16.8 forced-colors: the mode where the palette is not the author's (phase 16, slice 16f)

The phase's last item, and the one the palette work could not touch: `forced-colors` (Windows high contrast)
ignores `color`, `background-color` and `border-color` and substitutes the reader's system colours, so a page
that is clean in both palettes can still lose an affordance here. It is a different rendering mode, not a third
palette, and it gets its own check.

**Half of it is not the author's to fail, and the gate says so by measurement rather than by assumption.** The
first run reported `color-contrast x21 (.active)`. That number was wrong: under forced colors Chromium
substitutes the system colours at paint time while `getComputedStyle` and axe keep reading the author's, and the
elements in question render at **11:1** -- `rgb(0, 0, 159)` on white, measured by the test itself. In this mode
the colours in use are the reader's own, so the rule is disabled **with that measurement in the comment**, and
the test then asserts the other direction: `matchMedia('(forced-colors: active)')` is true and the rendered
link colour really does meet 4.5:1. Without that second assertion the rule switch could hide a page that never
entered the mode at all.

**The half that IS the author's**: anything that carries meaning with no text. The test measures a named set --
the two palette-switcher buttons and the notification bell, all of which are a box with an icon in it -- and
requires a rendered box of at least 8x8. A control whose box collapses is exactly what this mode produces and
exactly what no rule set can see, because a hidden control raises no violation: it simply is not there.
Proven, not asserted: adding `nav.navbar button { display: none }` under `@media (forced-colors: active)` fails
the check naming both palette switchers while axe stays green.

## 18. Monetization: the switch, the always-free list, and the leak this instance refuses (phase 15)

Plan phase 15 is NexusXXX §18 modelled as **operator configuration, open code**. This fork is MIT, so what an
instance charges for is its operator's decision; what this programme owes a reader is that the decision is
published, that the code is open, and that §18.2's list of what is always free cannot be shortened quietly.

**The switch, and what off means.** `[monetization] enabled` (`monetization_enabled`) is `false` in every
shipped config, and the streams an operator may name are `premium_subscription`, `creator_subs`, `ads`,
`data_licensing` and `commercial_api_tier` (`monetization_streams`). A stream is not a feature: §18.1's
"higher API limits" is the FEATURE `premium_api_tier`, and `premium_subscription` is the STREAM that sells it,
which is why the two names differ -- a configuration that names a feature where a stream belongs is refused at
startup with the list of streams. Off is not a synonym for "free for all" in the sense of "no rules": off means NO stream may be
on, so a stream key that is true while the switch is false is REFUSED BY NAME at startup
(`monetization_enabled is false, so the stream "ads" cannot be on`), and a key with a typo is refused with the
list of streams this version knows. Every stream is read through `monetization.RequireStream(name)`, which is
the only reader -- a config key that no process mentions is a key nobody can tell was ignored, which is the
failure this repository has already shipped once (`aria-presets` wrote `commons.enabled = false` and no code
read the key). The startup log states the posture in one line, both ways, naming the key.

**A stream that has no implementation is not a stream that is on.** Of §18.1's five revenue streams only one has
code on this tree: "higher API limits", and it is the one whose substrate already existed (the per-credential
rate limiter of slice 13d). `creator_subs`, `ads`, `data_licensing` and `commercial_api_tier` are named, and
enabling one is refused by name with the reason it cannot exist yet (`ads` needs the contextual renderer and the
two-slot bound; `creator_subs` needs payments and phase 10's media plane; `data_licensing` needs the aggregate
export; `commercial_api_tier` is the same mechanism as the premium tier and is not a second one). This is the
difference between a specified switch and a switch that lies.

**§18.2 is code, not prose, and the gate refuses to gate it.** The always-free list is seven `Capability` values
in `internal/service/monetization/alwaysfree.go`, each carrying §18.2's own words, the surfaces that provide it
on THIS instance (a route path, a GraphQL field, a suite name), and whether it is built. Two consequences:

1. `AlwaysFree()` and `PremiumFeatures()` are asserted DISJOINT by a unit test, so a future edit cannot list the
   same thing as free and as for sale.
2. `monetization.Gate(capability)` returns `ErrAlwaysFree` for any name on that list, and the refusal names it:
   *"search is always free (§18.2) and cannot be gated"*. A future caller who tries to put a core path behind a
   paywall gets a refusal from the gate rather than a silently working paywall, which is the only form of this
   rule that survives the next feature.

**§18.2's items that do not exist yet are named as unbuilt, never claimed.** "Full data export" is on §18.2's
list and this instance cannot do it (phase 13d is planned and not started). The published list therefore renders
it as *not built*, with the phase that would build it, because the alternative -- a page that promises an export
nobody can run -- is the shape `docs/goal-check.py` calls out by name: *"Never trust a count written in a
document. Recompute it."* A capability is claimed free only when a surface this instance serves provides it, and
the surfaces in the list are the ones the leak test calls.

**The premium tier is a SECOND LIMIT, never a second access rule.** §18.1's "higher API limits" is realised as
the free limit unchanged in both modes and a premium credential counted against a higher one
(`monetization_premium_requests_per_minute`, default 1200 against the free 600, with its own burst). The unit is
the credential, because that is what this limit counts: the API key for a script, and the connecting address for
a browser session, exactly as slice 13d decided -- so a browser session is unaffected by a tier, and this is
stated rather than discovered. The tier is read from the credential itself: an API key is a signed JWT carrying
its member's id (`internal/service/user/apikey.go`), and ONE implementation of "who is this key" is reused
(`user.GetUserIDFromAPIKey`) rather than a second parse. A credential that does not authenticate is a free
caller, never an error: a rate limiter that fails closed on a bad key would turn a typo into a 429.

**A tier change starts a fresh bucket.** Tokens are counted against a limit, and when the limit changes, the
count in hand was an answer about a different question. Buckets are therefore keyed on (tier, credential): a
member who becomes premium gets the premium bucket immediately, and a lapsed one gets the free bucket, rather
than inheriting tokens minted against the other limit. The limiter still forgets on purpose -- a bucket is
memory, not a row.

**How a member comes to be premium, in slice a.** An operator's comp list
(`monetization_premium_members`, a list of member ids) is the grant mechanism of the first slice, and the plan
records why: §18.3's automated grants (TL4 permanent, top-10 monthly, seasonal winners) need the recorded grant
they are audited by, which is slice b, and inventing a stored grant with no writer would be the "no writer, no
reader" defect this programme has already found once. When slice b lands, the comp list is REFUSED BY NAME --
so the configuration and the store cannot both claim to be the source of truth about who pays.

**What is NOT built, named one by one rather than implied:** ads (§18.1's contextual-only renderer, the
no-tracking rule and the two-slot bound), creator subscriptions (a payment path and the media plane),
data licensing (an aggregate export with a licence registry), the commercial API tier (deliberately the same
mechanism as the premium tier, so it is not built twice), §18.3's reward pass, and §18.2's own full export.

**Published, not documented.** `GET /monetization` renders the posture for a reader and `/monetization/json`
serves the same struct for anybody who wants to check it -- the shape slice 16a established, and for the same
reason: a posture nobody can find is not a posture, and the two renderings come from one value so the page
cannot disagree with itself. No `noindex`, public, static, mounted before the SPA catch-all.

**Verification.** With the switch off: every §18.2 surface answers as it did (the leak test compares the two
modes rather than inspecting one), the page says the instance charges for nothing, and the limiter's headers
report the free limit for every credential. With it on and the premium tier stream enabled: the free limit is
STILL the free limit for an ungranted credential, a granted credential's `X-RateLimit-Limit` is the higher one,
and every §18.2 capability still answers.

Three instruments, because each can fail where the others pass. `scripts/mutate-monetization.py` puts the rules
back one at a time and requires the named test to redden — 14 probes, each naming its test, the tree restored
green at the end, and the two probes whose FIRST version was not a verdict (an anchor that matched twice, a
mutation that was a spelling of the original) recorded in the file rather than quietly dropped.
`scripts/walk-15-live.py` runs the same binary TWICE against the same database — as it ships and with the
premium tier sold to a member who already exists — because the switch is read once at startup and no single
process can show both halves of the sentence; it reads the member's STORED API key rather than signing one
(the server compares the presented key with the stored one) and writes nothing to the database. It is also
what found the two things this section had wrong: the stream is `premium_subscription` while the FEATURE is
`premium_api_tier`, and the page named `monetization_enabled` in one posture and not the other. And
`docs/goal-check.py` clause **C9** asserts the posture statically, counting what it examines (seven
capabilities in code, two schema directives, five shipped configurations) so that a scan which reads nothing
cannot pass for a page that promises nothing.
