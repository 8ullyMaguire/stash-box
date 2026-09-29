# Intake review — "Stash Box Full Specification" (2026-09-29)

**Draft:** a rewritten, much longer version of the product vision the owner pasted
as the standing goal across the last several turns.
**Destination:** `docs/SPEC.md` §7 (`Product vision — the federated discovery mesh`).

**This is an AMENDMENT pass, not a fresh intake.** §7 is already an earlier
revision of this exact vision, so the work is reconciling two versions of one
document, not triaging a stranger's proposal. That distinction changes the
procedure: a fresh intake dedupes against the spec and rejects duplicates; here
the duplicate IS the spec section, and rejecting it would delete the product.

## What the draft adds that §7 does not have

Probed three surfaces — spec text, migrations, Go code — and kept the answers
separate, because "in the spec", "specified but unbuilt", and "absent" are three
different verdicts.

| Draft concept | Spec text | Code | Verdict |
|---|---|---|---|
| **Vanguard role** (§2.2) | 0 hits | 0 files | **ADD** — genuinely new |
| **Onion-routed metadata sync** (§4.5) | 0 | 0 | **ADD** |
| **Peer reputation keys + signed attestations** (§1.4) | 0 | 0 | **ADD** |
| **Peering tiers** (§1.3) | 0 | 0 | **ADD** |
| **Quorum queue for untrusted changes** (§3.1) | 0 | 0 | **ADD** |
| **Divergence markers / metadata forks** (§1.4, §4.6) | fork: 27, but all DB-fork meaning | 0 | **ADD** — §7 never says this |
| **Preservation alerts** (§4.4) | 0 | 0 | **ADD** |
| **Manifest + content hash, random challenge** (§4.4) | 1 (one word) | 0 | **EXTEND** §7.3 |
| **Auto-archive by cross-instance enjoyment** (§4.2) | 0 | 0 | **ADD** — and see the rejection below |
| **Storage allocation algorithm** (§4.3) | 1 (one word) | 0 | **EXTEND** §7.3 |
| **Storage allocation log** (§4.3) | 0 | 0 | **ADD** |
| **P2P layer: DHT/BitTorrent/eDonkey/Kad/IPFS** (§5) | 0 | 0 | **REJECT — see below** |
| **Local-first personal nodes** (§5.2) | 0 | 0 | **ADOPT, re-scoped** |
| **Anonymity levels** (§5.4) | 0 | 0 | **ADD** |
| **Air-gapped export/import bundles** (§4.5) | 0 | 0 | **ADD** |
| **Sybil resistance via attestation decay** (§1.4) | 0 | 0 | **ADD** |
| **SDK list widened to Rust** (§7.2) | 1 | 0 | **ADOPT** |
| **Relay nodes** (§1.1) | 0 | 0 | **ADD** |
| Capability profile | 2 | 0 | already §7.2 |
| Guilds / adopt-a-site / roadmap votes | 1–3 | 0 | already §7.12 |

`replicas_hosted` is **already a column on `user_trust`** (migration 76) with a
comment naming SPEC §6 — so the spec anticipated preservation before this draft
existed, and this draft fills in the mechanism rather than the intent.

## Rejections, with reasons (so a future draft does not re-litigate)

### 1. The P2P content layer — REJECTED, and this is the significant one

§5.1 proposes BitTorrent, WebTorrent, DHT, eDonkey2000, Kad, IPFS, and a custom
swarm protocol. Rejected as a product surface for **this** fork, on three
grounds, each independently sufficient:

- **It is a different product.** A swarm client is a peer-to-peer filesharing
  daemon. It has no metadata, no GraphQL, no schema compatibility surface, and
  none of the curation or trust machinery that is the entire point of this repo.
  §4.5 of the same draft already draws the line — "full content never moves over
  onion routing; it moves over the P2P layer" — so the draft itself contains the
  seam. Adopting §5 wholesale would build a BitTorrent client that a GraphQL
  server has to babysit.
- **Licence exposure is real and unresolved.** `eDonkey2000`/`Kad` are eMule-lineage
  and the reference implementations are GPL. Linking or embedding them into an MIT
  project is a distribution event. This fork is MIT (§4.2 of the spec) and the
  spec's own text flags copyleft as a thing a fork must reason about. Adopting
  the vocabulary now, before anyone has answered the licence question, puts a
  known-unanswered question inside the spec where it will be inherited silently.
- **The spec's own downstream constraint forbids it silently.** §4.3: the
  downstream consumer is the Stash desktop app and the GraphQL schema is a
  public compatibility surface. A swarm protocol adds no GraphQL, and any
  federation work that changes query semantics breaks a desktop app that cannot
  be updated in lockstep. This draft is the first to propose an entire subsystem
  with **zero** GraphQL surface.

The mechanism is not thrown away — it is **re-routed to where it belongs**: the
mesh's content transport is an *instance-to-instance* protocol (§4.5's P2P layer,
one implementation, spec-agnostic), not a public swarm protocol for arbitrary
third-party swarms. See §7.17 in the spec for the recording.

### 2. "Vanguards get weighted influence on gravity tuning" — REJECTED in part

The draft grants vanguards *weighted influence on instance gravity*. Adopted with
a correction: the same trust-weighting principle already in the spec's Elo section
would, applied to gravity, let a small group of users steer every user's
recommendations. Gravity is an **operator** control (§9 lists it explicitly); the
draft's own §2.2 also says operators can appoint vanguards manually, which is the
sanctioned influence path. So: vanguard status is computed and displayed, status
is earned dynamically, and vanguards get *priority* and *nomination* privileges —
but gravity is not a weighted vote. Recorded, not silently reshaped.

### 3. "Enforcement of attestation weight" — REJECTED as unenforceable as written

The draft says peer instances "can accept, weight, or reject attestations based
on their trust in the attesting instance", and separately that attestation weight
decays and is penalised by peer disagreement. Adopted, but the spec must say what
happens when peers **disagree** about an attestation, because the draft does not
and it is the one case that decides whether the system is safe. Recorded as
steward arbitration, the same re-route §3.1 already uses for metadata conflicts.

## Adaptations made (the auditable delta)

- **"Trust level 5 — Steward"** collides with the repo's existing `RoleEnum`
  (READ, VOTE, EDIT, MODERATE, ADMIN). Migration 76's comment says this in as many
  words: "Trust is NOT a role." So trust levels 0–5 and roles stay separate
  primitives; §7.6 is amended to say so rather than restating it.
- **SDK list**: added Rust alongside Python/JS/Go, which the draft lists and §7.11
  does not.
- **Section placement**: everything lands as leaves under §7. Renumbering is not
  performed — §7's own subsections are cited from the plan files, and a doc-wide
  grep is the only safe test, which is why this pass adds rather than moves.
