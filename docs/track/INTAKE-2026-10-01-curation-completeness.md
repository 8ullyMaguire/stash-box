# Intake Review: curation-completeness-and-preservation ideas → spec v1.5

*Review of an AI-generated 31-idea ranking ("ideas for curation completeness,
archival and preservation, ranked by impact ÷ effort") against `docs/SPEC.md` and
the migration tree, on 2026-10-01. Adopted features land in a new §7.24 amendment;
this doc records adopted / adapted / already-built / rejected with reasons.*

*Every existence claim below was checked against the tree on 2026-10-01, not
inferred from the draft. Migration numbers are from
`internal/database/migrations/postgres/` (95 migrations, newest `95_image_crops`).*

---

## Headline: this draft is mostly already built, and its top-ranked idea conflicts with the spec

Two findings that change how much of this should be adopted:

1. **The draft's premise is out of date.** It says bounties "have them only as a
   one-liner" and ranks a bounty board #4. Migration `80_add_authored_quests` and
   `81_add_bonus_points` already exist: `authored_quests` with a bounded
   `bounty_points` column (`0..10000`), plus `KindQuestCompleted` bonus XP. A
   pricing layer is largely present.

2. **#4 as written is forbidden by the code's own comment.** Migration 80 says,
   verbatim:

   > A BOUNTY is an operator decision -- "this gap is worth triple" -- and a
   > promise made by a person. A generated quest must not be able to manufacture
   > one, so a bounty lives only on an AUTHORED quest, and the generator never
   > reads this table.

   The draft proposes auto-generating bounties from completion gaps, priced by
   marginal gain × rarity × age. That is exactly the thing the schema comment
   refuses, and the refusal has a stated reason (a bounty is a *promise*, so it
   needs a promisor). **Rejected as written; the pricing *formula* is adopted
   under the existing authored-quest model** — see A3 below.

The draft's genuinely valuable contributions are elsewhere: it found two cheap
signals that are derivable from data already collected, and it correctly
identified that the quest/completion machinery has no work-item generator.

---

## Adopted (with adaptations)

### A1. "Verified unknown" markers — the highest-value item in the draft [#1]

**Adopted as the prerequisite it is.** Every downstream item (bounty pricing,
quest freshness, completeness denominators, the state-of-archive page) needs a way
to say "this field is confirmed-absent, not merely missing," and nothing in 95
migrations expresses it.

Adapted to the fork's existing primitives:

- This is **not** a new completion subsystem. §7.7 already defines the completion
  score, and `76_add_user_trust` / `80_add_authored_quests` already define the
  quest kinds. A verified-unknown is a **fact-level assertion**, so it attaches to
  the existing edit/consensus machinery — `internal/service/edit` — rather than
  introducing a parallel write path.
- It must be **an edit, not a moderator field**, or trust levels and reputation
  (`81_add_bonus_points`) stop meaning anything: a curator with trust earns
  auto-approval, and "not publicly knowable" is a low-risk, high-value edit that
  should be earnable by exactly that logic.
- It needs a **reason code**, not free text. "Birthdate not publicly knowable" and
  "birthdate exists but nobody has looked" are different facts and the second one
  should stay farmable.
- **No XP for creating one on an entity you already touched** — otherwise
  "verified unknown" becomes the cheapest way to farm completion, which is the
  same class of problem §7.20 rejected for vanguards.

Spec home: **§7.24.1**.

### A2. Expected-total denominators [#9]

**Adopted.** The draft is right that completeness is unmeasurable without a
denominator, and this is a small, additive schema change to entities that already
carry source links (§7.7 counts "source links" today).

Adapted: **sourced, not asserted.** A denominator is a claim about the world, so
it carries `source_id` and is only accepted from a trusted contributor or a
moderator. An unsourced "about 412 scenes" is a rumour that would silently deflate
every completeness score on the instance. This is the same provenance discipline
§7.7's completion factors already imply.

Spec home: **§7.24.2**.

### A3. Bounty pricing formula — re-routed to the authored model [#4, part]

**Adopted, re-routed.** The draft's five design traps are good and mostly already
satisfied by migration 80:

| Draft trap | Already true? | Evidence |
|---|---|---|
| Pay on edit applied, not submit | **Yes** | 80's comment: the item leaves the quest "when the underlying field is actually filled, not when the claim expires" |
| Claw back on revert | **Partly** — `KindQuestCompleted` bonus is the mechanism; claw-back not verified | `81_add_bonus_points` |
| No payout for confirming your own edit | **No** — needs an explicit rule | — |
| Diminishing returns per entity | **No** | — |
| XP/reputation only, never money | **Yes** — `bounty_points` is INTEGER, no currency anywhere | 80, 81 |

**Re-routed:** instead of generated bounties, an operator (or a curator whose
trust permits it) **authors** a quest, and the *suggested* price is computed by
the formula and shown to the author, who sets the actual value. This keeps the
promisor, satisfies every trap the draft listed, and does not require the generator
to read `authored_quests` — which migration 80 forbids.

Two additions the draft did not ask for:

- **Self-confirmation gets no payout** (its own rule, now explicit).
- **Diminishing returns** per (user, entity, field) so the same gap cannot be
  farmed.

Spec home: **§7.24.3**.

### A4. Data-lint quests [#5]

**Adopted — the cheapest real work-item generator in the draft, and it needs no new
subsystem.** §7.7 already promises "data-lint" quests in spirit ("Resolve
duplicate suspicion", "Link 10 unlinked scenes"); this makes the source concrete.

Adapted:

- Each detector is a **named SQL query emitting quest candidates**, not new
  service code. The detectors the draft lists are largely expressible against
  tables that already exist: `scene_urls` / `performer_urls` / `sites.url` for
  duplicate URLs; `studios` + `scenes` for date-before-studio; alias collisions
  against `performer_aliases`; conflicting tags against the existing tag tables.
- **Alias collisions must not auto-merge.** They emit a quest; the merge path is
  the existing edit/consensus flow (`internal/service/edit`), which already writes
  all four redirect tables on merge.
- The same SQL is reusable as a **validation pass over imports** (§7.24.7), which
  is the higher-value half of this item.

Spec home: **§7.24.4**.

### A5. Unmatched-fingerprint demand board [#6]

**Adopted, with a hard boundary the draft states correctly and I am making
load-bearing.** Migration `18_fingerprint_user` gives `scene_fingerprints.user_id`
`NOT NULL` with an index on `(user_id, algorithm, hash)`, so the aggregation is
available. Migration `89_identification_federation` already has the identification
board.

The boundary: **aggregate by hash and count only — never store or expose the path,
the user, or the scene.** That is §7A's rule and the draft already respects it. A
miss is evidence a copy exists somewhere; the miss record must not become a
pointer to it.

Adopted as: aggregate misses by hash, rank by distinct-user count, expose only
hash + count, and **seed the identification board from the top misses** — which
is the one genuinely new lever and reuses migration 89 wholesale.

Spec home: **§7.24.5**.

### A6. Fingerprint corroboration factor [#10]

**Adopted — nearly free.** A scene with one fingerprint submission, or only one
hash algorithm, gets a "needs a second independent submission" flag and it becomes
a completion factor. The data to compute it is already there
(`scene_fingerprints.algorithm`, `user_id`, with the existing unique constraint on
`(scene_id, fingerprint_id, user_id)`); this is a query plus a completion factor.

Spec home: **§7.24.6** (same section as the completion work, since it is one
factor, not a subsystem).

### A7. Bulk vandalism rollback [#23]

**Adopted.** `mod_audit` exists (3 migrations reference it) and the audit trail is
§7.13's operator surface, so this is a **query over the existing audit table**
plus one audited action. It is the cheapest metadata-integrity win in the draft.

Caveat recorded: revert must write the *same* audit shape as a normal edit so the
trail does not have two formats.

Spec home: **§7.24.8**.

### A8. Completion-delta preview and "while you're here" nudge [#11]

**Adopted — cheap, and it is a UX layer over §7.7's existing completion score,
not a new score.** Show the before/after delta on the edit form, then suggest the
next-highest-value missing field for that entity. Needs a `completion_score`
read for the entity under edit, which §7.7 already requires ("progress bars
everywhere").

Spec home: **§7.24.9**.

### A9. Webhook announcements for new bounties and endangered scenes [#18]

**Adopted as wiring, not a feature.** Migration `84_add_webhooks` has
`webhook_endpoints` + `webhook_deliveries` with `event_type` and JSONB payload;
this adds two event types. Cheap, and it is why #18's I2/E1 rating is right.

Spec home: **§7.24.10**.

### A10. Review-queue aging bounties [#12] and easy-tier onboarding quests [#13]

**Adopted, together, because they are one loop.** Pending edits nobody votes on
block completeness (§7.7 "multi-user verification"). Pay XP for voting on the
oldest pending items; draw a "first five edits" funnel from the same board.

Adapted: **the payout is for the vote, never for the outcome of the vote**, so
there is no incentive to vote a particular way. This is §7.20's rejection applied
consistently — a reward keyed to influence over *what gets prioritized* is the
shape §7.20 refused.

Spec home: **§7.24.11**.

---

## Already built — no new spec row needed, one audit row

### B1. Redirect-permanence across merges and deletes [#2]

**Substantially built.** All four redirect tables exist in
`06_deletion_and_redirects` (`tag_redirects`, `performer_redirects`,
`scene_redirects`, `studio_redirects`), **and** all four merge paths write them:
`internal/service/edit/scene.go:648`, `studio.go:418`, `tag.go:310`,
`performer.go:571`, each calling `Update*Redirects`. Resolution on read exists
too (`FindStudioWithRedirect`, `findStudioWithRedirect`).

So the draft's "verify that merged or deleted scenes, studios and tags also keep a
redirect" **resolves to: yes, for merges; the delete path needs an audit row.**
Rather than a new feature, this is one row in the existing operator verification
surface — because the compatibility claim in §4.3 depends on it and nothing
currently tests it end-to-end.

### B2. Holder count from existing fingerprint submissions [#3]

**Derivable from data already collected.** `scene_fingerprints.user_id` is
`NOT NULL` and indexed, so `COUNT(DISTINCT user_id)` per scene is a lower bound on
replicas. The draft's own framing is correct — thresholded count, never who.

Adopted as a **derived signal feeding A5 and B1**, not as an independent feature:
it is a query, and the only real work is deciding the threshold and where it is
surfaced (§7.24.5 uses it for the endangered-copy notice, below).

---

## Adopted but deferred to a named phase (not rejected)

### D1. Endangered-copy notice [#15] and wanted list [#16]

**Adopted, deferred to the preservation phase.** Both are metadata-plane and both
depend on holder counts (B2). The endangered-copy notice is explicitly a message
*to a Stash client user* — no paths cross the boundary (§7A).

Recorded now so the ideas are not lost, and so that when the phase starts the
data dependency (B2) is already specified.

### D2. Link-rot checker + archive submission [#14]

**Adopted with a correction.** The draft says to reuse the webhook validator's
resolve-then-check because §7A.3 rule 2 makes this an SSRF surface. That is the
right instinct and the wrong function.

Verified: `internal/webhook/target.go` and `internal/service/federation/baseurl.go`
both contain SSRF resolution logic, and `internal/service/edit/validate.go:186`
has `validateURLs`. The **link-rot checker itself is a fetch**, so it needs the
same resolve-then-check discipline — and because it *submits live links to an
external service*, it also needs the operator opt-in the draft mentions. Adopted
at the preservation phase; the SSRF requirement is written into the spec row so
it is not rediscovered later.

### D3. Backup and restore drill [#7]

**Adopted, and it is the highest-E/lowest-risk item in the list.** `cmd/` has only
`sdbimport` and `stash-box` — **there is no backup command.** Preserving the
archive starts with preserving the instance, and a CI job that restores and runs
the suite is what makes every other preservation claim testable.

Adopted at the preservation phase, but flagged here as **the one item that should
not wait for the rest of the preservation phase**, because it gates the
credibility of the others.

### D4. Nightly signed metadata dump [#8]

**Adopted, deferred.** The draft is right that this is the cold-storage artifact
and the first draft of §7.17.4's air-gap bundle. No objection; the deferral is
only because it is downstream of D3 (backup) and B2 (holder data).

### D5. Early read-only metadata mirror [#30]

**Adopted as written — it carries no trust-model risk**, which the draft correctly
identifies, and §7.17.1 already names the role. Deferred to the federation phase.

### D6. Release variants and reference copy [#25]

**Adopted at the preservation phase.** Modelling encodes of one scene as variants
with a designated reference copy is correct and cheap — it is a
`scene_variants`-style table plus a pointer, and it makes "diverging hashes reveal
corruption" checkable. Needs one addition the draft omits: the reference copy must
be **re-checkable**, so the variant row needs a fingerprint-set comparison, not
just a boolean.

### D7. Cover and image upgrade quests [#17]

**Adopted.** Low-resolution or hash-duplicate images trigger an upgrade quest.
`94_image_types` / `95_image_crops` exist, and checksum repair already has a
service (`internal/service/image/checksum_repair_integration_test.go`), so
"hash-duplicate" is a query the existing image service can answer.

### D8. Freshness / re-verification quests [#20]

**Adopted at the curation phase.** Adding "last verified" to facts that rot
(studio active? site alive?) makes completeness include recency. This is one more
completion factor, and §7.17.2's freshness rule already establishes the pattern.

### D9. Gold-set reviewer calibration [#27]

**Adopted, and it is more load-bearing than the draft rates it.** §7.6 uses
voting consistency as a trust input; the draft is right that this needs an
objective basis. `85_add_vote_weight` exists, so the vote-weight mechanism is
present — this is the measurement that tells you whether the weights are correct.

### D10. Cross-entity inference suggestions [#26]

**Adopted, adapted.** Pre-filled draft edits from co-star graph gaps, studio-network
tag inheritance, and cluster members with differing performers. Adaptation: these
must be **draft edits requiring votes, never auto-applied** — same reason as A4's
alias collisions and the draft's own modbot-race caveat. The inference is a
suggestion; a human confirms it.

### D11. Public state-of-archive page [#21]

**Adopted.** A completion heatmap by studio, generated from §7.7's completion
scores. SEO and contributor-recruitment value, and it is a read-only view over
data the instance already computes. Note it must respect NSFW/privacy settings —
it is derived from per-entity scores, so it aggregates rather than exposing
entities.

### D12. Keyboard-driven triage mode [#22]

**Adopted.** Fast match/mismatch and merge-review on collages. It lowers
per-contribution effort, so throughput rises against every other curation item in
this document. Cheap, and §7.8's collage interactions already feed the completion
engine.

### D13. Bulk vandalism rollback — see A7 (adopted, not deferred).

### D14. Campaign weekends and events [#28]

**Adopted, low priority.** Time-boxed themed sprints on top of quests. Thin, but
§7.7 already has "federated quests: instances with similar taste run joint
campaigns", so this is a scoped instance of an existing mechanism.

### D15. Per-edit source citations [#24]

**Adopted.** Structured source link on high-impact fields, so completion separates
sourced from unsourced and confidence scoring has an input. §7.7 counts "source
links" already; this makes it a first-class field rather than a count.

### D16. Bot import pipeline [#29]

**Adopted, deferred to last, and the draft's own ranking is correct.** Largest
single completeness lever (I5/E4) and the most expensive. The draft's condition —
fix the modbot race (§8.1) first — is accepted without reservation. Batch drafts
at low trust in a quorum queue (§7.18.1) so humans only vote.

---

## Rejected (with reasons)

- **#4 as written — auto-generated bounties from completion gaps.** Rejected:
  migration `80_add_authored_quests` states in the schema comment that a bounty is
  an operator decision and a human promise, that it lives only on an authored
  quest, and that **the generator never reads that table**. Auto-pricing bounties
  is precisely the capability that comment refuses, and it has a stated reason:
  a bounty is a promise, so it needs a promisor. The pricing *formula* is adopted
  (A3) as a **suggestion shown to the author**, who sets the value.
  *(Future drafts will re-propose auto-pricing. This is the reason.)*

- **#31 Federated bounty exchange.** Rejected for now: deferred to Phase 4
  alongside federation generally, and it depends on cross-instance trust
  machinery that §7.17 does not yet have. The draft's own deferral is agreed;
  recorded here so it is not re-litigated in the next intake.

- **#30 as a *trust* mechanism** — no, this is D5, adopted as written. *(Listed
  here only to note the draft and this review agree.)*

- **Swarm/torrent mirroring, a vanguard vote on prioritization, real-money
  bounties, and any rule keyed on client-supplied region or device.** Rejected,
  and the draft already excludes them. Specifically: the region/device rules are
  §7.23 D5's denylist *evaluated after* the five access conditions, never instead
  of them, and real-money bounties are excluded by `bounty_points` being INTEGER.

- **#3's "how many copies exist" as an exact count.** Rejected as stated: distinct
  fingerprint submitters is a **lower bound**, not a count. It is adopted as a
  lower bound (B2) and must never be presented as an exact replica count, because
  one user with three copies contributes the same as one with one.

---

## Adaptation log (draft → spec deltas)

| Draft said | Spec says | Why |
|---|---|---|
| Auto-generate bounties priced by marginal gain × rarity × age (§#4) | Operator authors the quest; the formula computes a **suggested** price the author may override (§7.24.3) | Migration 80: a bounty is a human promise; the generator must not read `authored_quests` |
| "Verified unknown" as a new field | An **edit** through the existing consensus machinery, with a reason code and no XP for self-affirming (§7.24.1) | Keeps trust levels and reputation meaningful; stops it becoming the cheapest XP farm |
| Holder count = "how many copies exist" | Distinct fingerprint submitters, exposed **thresholded**, and described as a **lower bound** (§7.24.5) | One user with three copies ≠ three holders; presenting it as exact is wrong |
| Reuse the webhook validator for link-rot | Use the **federation/webhook resolve-then-check discipline**, because the checker itself performs fetches (§7.24.12) | `internal/webhook/target.go` + `internal/service/federation/baseurl.go` already hold the SSRF logic; the function is wrong, the discipline is right |
| Unmatched-fingerprint board | Aggregate by **hash + count only**; never path, user, or scene (§7.24.5) | §7A: a miss is evidence a copy exists; it must not become a pointer to it |
| Pay XP for voting on pending edits | Pay for the **vote**, never its outcome (§7.24.11) | §7.20's rejection applied consistently — no reward keyed to influence over what gets prioritized |
| Bot pipeline: "humans only vote and don't type" | Batch drafts at **low trust in a quorum queue** (§7.18.1), never auto-applied | Same reason as the alias-collision and inference-suggestion cases |
| Bot pipeline condition: "fix the modbot race (§8.1) first" | Accepted without reservation | §8.1 is a known failure mode; importing at scale before it is fixed amplifies it |
| Denominators as sourced fields | Sourced, trusted-contributor-or-moderator only; unsourced totals rejected (§7.24.2) | An unsourced total silently deflates every completion score on the instance |
| Alias-collision lint emits quests | Emits quests; **never auto-merges** | The merge path is the existing consensus flow and it writes all four redirect tables |

---

## Configuration staging (parking lot)

The draft is instance-agnostic — no presets, no ladder, no thresholds. Two values
it implies are instance configuration and belong with deployment, not in the spec:

- The **holder-count threshold** at which the endangered-copy notice fires
  (§7.24.5, D1).
- The **minimum resolution** below which a cover triggers an upgrade quest
  (§7.24.7, D7).

The rest — bounty ceilings, pricing weights, lint thresholds, quarantine windows —
are operator controls under §7.13 and are referenced, not specified.

---

## What actually landed

`docs/SPEC.md` gains **§7.24** (amendment, 12 adopted leaves + 1 audit row),
`docs/plan/` gains one phase section, `docs/track/` gains a handoff. Per §0, this
is what puts these ideas in scope — the draft's own note is right that none of
them are in scope until a spec row asks for them.

**Order of work, by dependency, not by the draft's ranking:**

1. **§7.24.1** verified-unknown (everything else prices or measures gaps)
2. **§7.24.2** denominators (makes completeness measurable)
3. **§7.24.4** data-lint quests (the work-item generator)
4. **§7.24.5** unmatched-fingerprint board + holder lower bound
5. **§7.24.6** corroboration factor · **§7.24.3** authored bounty pricing
6. **§7.24.8** rollback · **§7.24.9** delta preview · **§7.24.10** webhooks
7. **§7.24.11** review-queue bounties + onboarding
8. **§7.24.12** link-rot + archival (preservation phase, with D3 backup first)

The draft's own "best cheap wedge" (#1, #3, #5, #10–13) agrees with this order,
and is right for the wrong reason: those are cheap, but they are also *first*,
because #1 and #2 are what the rest price against.