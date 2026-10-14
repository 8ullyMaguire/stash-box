# SPEC — the ingestion pipeline (aggregator programme, phase 2)

Implements §7.1–§7.3 and §3.3–§3.4 of the NexusXXX draft on top of phase 1's source
layer. Phase 1 gave a scene somewhere to record where content lives; this gives the
platform a way to *find* content nobody here owns and turn it into such a record.

Normative siblings: `docs/spec/feature-content-aggregator.md` (the programme, its
priority and its reversals), `docs/plan/feature-content-aggregator.md` (phases and
order). The programme spec's §0 applies: aggregation and discovery lead.

## 1. The pipeline

One URL in, one of four outcomes out. The stages are ordered because each one can end
the process early, and doing the cheap ones last is how a queue spends its budget on
duplicates:

1. **Normalise.** Strip tracking parameters, lowercase the host, drop the fragment, sort
   the query and remove the default port. Pure and total: no I/O, so its rules are a
   table in a test rather than a comment in a fetch path. `internal/service/ingest/
   normalize.go`.
2. **Reject.** Refuse early and loudly: a scheme that is not http(s), a host on the
   denylist, a URL already recorded as `dmca`. This is the cheapest possible check and
   the only one whose refusal a submitter must be told about.
3. **Dedup on the URL.** If any entity already claims this URL, attach nothing and
   return the existing entity. Outcome `attached`.
4. **Extract.** Ask the extractor interface for metadata. **This is the only stage that
   touches the network**, and it is behind an interface with a fake implementation used
   by every test, because a pipeline whose tests need a live tube site is a pipeline that
   is tested only when someone remembers to. It is also the stage that learns a shortened
   URL's final destination — resolving a redirect is an act of asking, so it belongs here
   and not in stage 1, whose whole value is being testable without a network.
5. **Dedup on the fingerprint.** If the extracted duration/size/oshash matches an
   existing scene's, the content is the same content at a second source: attach a
   source to the existing scene rather than creating a second one. Outcome `merged`.
   This is what `fingerprint_cluster` and migration 99's oshash corroboration already
   exist for.
6. **Create.** Otherwise write a scene with `origin = 'aggregate'` and attach the
   source. Outcome `created`.
7. **Tier and review.** Auto-approve when the source site is trusted *and* the
   extractor's confidence is high; otherwise park the record for review. Phase 5's
   tiers are not built yet, so every aggregated item is tier 0 (visible) until then —
   which is why the programme spec records "discovery ships before the gate" as a
   reversal with a cost.
8. **Archive.** Submit to the archiver (phase 9 owns the sweep; this stage only asks).

## 2. What a content record means, and what it does not

Aggregation creates a **scene** with `origin = 'aggregate'`, not a new content kind.
That is the programme spec's §2 decision and it is what lets every existing reader —
search, tags, reviews, completion, elo, lists, the identification board — work on
aggregated content with no new plumbing.

What it does NOT mean:

- **No file.** A scene with no fingerprints and no stored media is legal (phase 1
  established this) and is the normal case here. `origin` is how a reader tells "we
  host this" from "we point at this", which no existing column can express: a library
  scene that later gains a tube URL is still a library scene.
- **No automatic performer links.** The extractor's title parsing may *propose*
  performers; proposals become edits, never links (§7.25.3, the rule that survives
  contact with the aggregator: a machine's guess is evidence, not authority).
- **No authority over the member's own record.** If an aggregated URL matches a
  library scene, the source is attached to that scene. The member's scene is not
  replaced, renamed or merged into a new row.

## 3. The queue

A table, not a channel: the pipeline is resumable, rate-limited per host, and its
failures are inspectable after the fact. `ingest_queue` carries the URL, the normalised
URL (the dedup key), the process state, the outcome, attempts, the last error, the
extractor's confidence and the resulting scene.

**Two columns, not one, because they answer different questions.** `status` is where
the work is (`pending`, `running`, `done`, `failed`, `rejected`); `outcome` is what the
pipeline decided (`created`, `attached`, `merged`, or null while undecided). A single
enum would have to spell "done-by-merging" and "done-by-creating" as separate states,
which makes "how many of the last hundred ingestions were duplicates" a string
comparison instead of a filter — and that number is the one that says whether the
dedup rules work.

- **Claiming is `FOR UPDATE SKIP LOCKED`**, so several workers drain one queue without
  a lock table or a leader. Attempts increment at claim time, not at failure time: a
  worker killed mid-job is the case that matters, and a counter that only increments on
  a clean failure never counts it.
- **Backoff is a column** (`next_attempt_at`) so a failing host is retried later
  without a retry loop holding a worker.
- **`UNIQUE (url_normalized) WHERE status IN ('pending','running','done')`**: a URL
  that is queued, in flight or successfully ingested cannot be queued again, and a URL
  that failed permanently can be. Without the partial predicate, a genuine transient
  failure could never be retried; without the uniqueness, a popular URL would be
  ingested once per submission.

## 4. Trust, and who may submit

`EDIT` submits a URL — the same role that submits an edit, because that is the act:
proposing a change to the shared record. `BOT` may submit too (the crawler is a
client), and a bot's submissions are subject to the same dedup and review gates.

Auto-approval is deliberately narrow in the first cut: a **trusted site** (one already
in the directory with a configured regex) and a **high-confidence extraction**, or the
record waits. The alternative — auto-approving everything and reviewing after — makes
the review queue a place where bad data is already public, and this repo's own history
with the identification board is that a suggestion nobody has to approve becomes a
fact.

## 5. Failure modes this spec names in advance

| Failure | What the pipeline does | Why it is written down |
|---|---|---|
| The extractor cannot reach the host | `failed`, backoff, attempts counted | Otherwise a dead host is retried forever and one bad site starves the queue |
| The host serves a login wall | `rejected` with a reason, not `failed` | A retry cannot fix it; the reason is what stops a human re-queueing it |
| The page is gone (404) | The *source* is marked dead (phase 9's state machine), the queue row is `done` | The submission was handled correctly; the content is what is missing. Conflating them makes the queue's failure count meaningless |
| The URL is already a source | `done` / `attached` | Not an error, and reporting it as one teaches submitters that duplicates fail |
| Two workers claim the same URL | Impossible by construction | The claim is one statement with `SKIP LOCKED`; the test asserts a second claim returns nothing |
| Extraction yields nothing usable | `failed` with the extractor's own reason | A record with no title, no date and no performers is a URL, not content, and creating it would pollute discovery |

## 6. Verification

- **Pure functions, table-driven**: normalisation (trailing slash, case, tracking
  params, fragment, port, default-port removal, the http/https non-fold) and the dedup
  decision (attach / merge / create / reject, as a function of what is already known),
  plus the backoff's clamp. Each rule is a named subtest, so a failure names the rule.
  `internal/service/ingest/{normalize,dedup}_test.go`; five mutations run against them
  (attach/merge precedence swapped, denial precedence removed, query sorting dropped,
  trailing-slash trim dropped, backoff clamp removed), each reddening only its own
  assertions.
- **Migration 109's constraints and the claim query** at the SQL level, in
  `scripts/verify-109.sh` — the same harness shape as verify-108, including: a second
  claim returns no row while the first is running; a `done` URL cannot be re-queued; a
  `failed` one can; a claimed row's attempt count went up.
- **Integration**: three URLs resolving to one existing scene produce one scene, three
  sources and one cluster, asserted through the API.
- **Mutation**: breaking the dedup comparison (so it never matches) must redden exactly
  the duplicate test — if it reddens nothing, the dedup rules are not being exercised by
  the fixtures.
