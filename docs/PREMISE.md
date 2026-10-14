# Stash Box — premise

*A one-page description of what this is, for thinking out loud about. Written
2026-10-14, after the spec's direction changed twice: from a metadata-only fork of
stash-box, to a federated metadata mesh, to a content aggregator with the whole of
NexusXXX in scope.*

**In one line:** a community-curated, federated archive and discovery engine for
adult content — catalog everything, curate together, discover everywhere, preserve
forever.

**What it is.** Today it is a metadata database: performers, scenes, studios,
sites, tags, images, reviews, fingerprints, Elo rankings, and a "which was that?"
identification board — with a public GraphQL API that the Stash desktop app
already talks to. The programme in progress extends it into a content aggregator:
video stays `scene` and gets extended, and six other kinds (image, gallery,
animation, text, audio, comic) arrive as `content_items` on the polymorphism that
already exists. So an instance stops merely describing content and starts holding
and serving it.

**Two layers of social.** One instance is its own portal, community and archive —
its own theme, trust thresholds, access rules, storage budget. Instances also peer:
each publishes a taste profile (from votes, reviews, tags, curation history) and a
capability profile (storage, bandwidth, policies), and peers with similar taste
share recommendations, cross-instance trending and preservation duties. The mesh
moves metadata, rankings, reviews, snapshots and questions. It never moves content,
and content never broadcasts.

**The commitments that make it unusual** — and the ones worth arguing with:

- Everything is a claim, and every claim goes through one write path: propose,
  vote, apply. A plurality is evidence, not authority.
- Nothing derived is stored. Tallies, reputation and scores are computed from the
  rows that justify them, because a stored score is a second answer that can drift
  from the first.
- A signal is a fact about a work, never about a reader. That rule is what decides
  what a "trending" surface is even allowed to count.
- Chronological surfaces stay chronological by contract: one feed has no ranking
  input at all. Ranking lives on discovery surfaces, where it is explicit (Elo,
  taste affinity, instance gravity) and explainable.
- Automation may suggest, never decide. A face model or a third-party service
  produces attributed evidence, never a candidate the community then votes on; an
  auto-tagger produces an edit draft, never a tag.
- External identification sources are linked, not scraped; no biometric query
  leaves the instance unless the operator enables that specific provider; and
  provenance — who said this, from where, with what confidence — is machine-readable.
- Preservation is the default posture: a scene should exist on three instances
  unless configured otherwise, and trust is what unlocks access.
- Local-first: an instance works fully on its own. The mesh adds reach, not
  required function.

**Honest state.** Built, tested and pushed: the metadata core, the tag ontology,
the identification board and its peer federation, the discovery layer (a query
language, similarity, taste profile, For You, serendipity), and social follows with
an activity feed. Next in the order the plan sets: the other content kinds, then
the media plane, then moderation and legal policy text — which is a *precondition*
for storing any content, because holding content is what makes an instance a host.

**Open questions.** Where exactly is the line between "discovery ranks" and "feeds
don't"? Is "suggestion only" the right ceiling for face matching and auto-tagging,
or does it make them useless at scale? How much content should an instance hold
rather than link to, given hosting changes its legal posture? Is a one-instance
experience good enough, when the mesh needs a second node to mean anything? And is
"aggregator" even the right word for this, or is it a curated archive that happens
to hold files?
