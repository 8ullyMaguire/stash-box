# HANDOFF — the content-plane respec, 2026-10-15

Repo `/home/alvaro/code-local/go/stash-box`, branch `main`. Spec `docs/spec/feature-content-aggregator.md`,
plan `docs/plan/feature-content-aggregator.md`, state table `docs/track/TRACK-content-aggregator.md`.
The commit carrying this handoff is the one to read first: it is the whole respec in one diff.

## What the owner decided

> *"disable/remove functionality like content aggregation and discussion board that can give trouble unless
> there's a way to build them to avoid many dmca's, keep identification board only."*

Six documents' worth of verdict came with it, and it is adopted as the reasoning rather than just the
instruction: aggregation aimed at the right problem by the wrong means. "Find the scene" is what a reader
wants; a site that embeds, proxies, transcodes or hosts third-party content is the category that is DMCA'd
out of existence, and one auto-scraped link to material that must never be indexed ends the project.

## What changed, in one table

| | Decision |
|---|---|
| Phase 10 — the media plane (hosting, transcoding, the variant ladder, the five players, PWA caching, CDN) | **REMOVED from the programme.** Never built (`grep -rn "iframe\|<video\|transcod\|ffmpeg\|m3u8\|hls" internal/ graphql/` is empty). Its identification half — the face matcher and the image taggers — was already specified in `docs/spec/feature-04-identification-federation.md` §8 and stays there. |
| Slice 8f — forums | **REMOVED.** The cost is the moderation, not the code: metadata edits are moderated by the voting system this repo already has, while posts need humans for spam, harassment and piracy links at a rate that scales with traffic. The plan keeps the constraint a future design would have to beat rather than deleting the idea. |
| Phases 1, 2, 3, 9 — sources, ingest, content items, preservation | **KEPT and re-framed as REFERENCES.** A source row is a URL and its health; the pipeline reads a page's METADATA to propose an edit a human still has to approve. Nothing stores bytes; nothing serves a work. |
| Every outbound request to a content source | **GATED OFF by default** — one switch, `content_fetch_sources` (`internal/config`), covering exactly three call sites: the ingest page fetch, the health sweep's HEAD, and Wayback submission. |
| The identification board | **Untouched.** Text, fingerprints and attribution: a member says "this is the scene you asked about" and the answer opens an edit. |
| Frame indexing | **Kept only as the HASH, never the frame**: an opted-in Stash client contributes perceptual hashes of its own keyframes, clustered by the fingerprint service. Decided direction, NOT BUILT, and deliberately without a phase number. CLIP-style visual similarity stays refused. |

## How the switch is enforced, because "off by default" is a claim

Two ends, one condition:

1. **The cron jobs are not registered at all** when it is off (`internal/cron`'s `contentPlaneJobs`, which
   `register` is the only caller of), and startup logs once, naming the switch, that they are absent. A job
   that is not in the scheduler cannot be run by anything — not a future tick, not a refactor that loses an
   `if` — whereas a job that runs and returns immediately is one edit away from fetching again.
2. **The entry points refuse by name** (`internal/service/content/policy.go`'s `RequireFetching`, used by
   `Sweep`, `CheckSource`, `SweepResurrections`, `ArchiveSources`, `ingest.Submit` and `ingest.process`), so
   a caller this file has never heard of cannot fetch either. `process` RECORDS the refusal against the
   queue row rather than returning it, which is this pipeline's own rule — the row is the record of what
   happened — and returning it would strand the claimed row in `running` and make `Drain` log a lie.

What the switch does NOT cover is named in the spec's respec section so nobody assumes more than it does:
`internal/storage/favicon.go` (upstream's member-triggered site-icon fetch), `internal/webhook`, the
federation client, and `sdbimport`.

## Where the programme is now

`docs/goal-check.py`: **C8 reads 8 of 16 phases not done** (8, 11, 12, 13, 14, 15, 16, 17) — phase 10 is out
of the count because it is out of the plan. Phases 1-7, 9 are done. The next session's work is unchanged by
the respec and sits in the TRACK table: **8e collaborative lists** (the design is in the plan; the open
question is WHO MAY ADD, which is not who may see), then phases 11-17, which the respec does not touch.

## Evidence

- **12 mutations, 12 killed, 0 survivors**, each reddening exactly its own named test, every file restored
  and hash-verified.
- Unit: 47 packages, 0 failures. Integration (`-tags integration -p 1`) against a real Postgres: 50
  packages, 0 failures; `internal/api` is `ok` with 762 test functions, 0 failures, 0 skips.
- `gofmt -l internal/` clean; `go vet` clean with and without the integration tag.
- 11 new test functions: 2 in `internal/config`, 4 in `internal/service/content`, 3 in `internal/cron`,
  2 in `internal/api`.

## The one thing this pass got wrong, recorded because it is the useful part

The first version of the plan's respec section dropped `content_item_media` in a migration, on the strength
of a grep for that table name which had never actually been run — it had been run for three OTHER names.
The integration suite refused the claim by name: `relation "content_item_media" does not exist`, in
`TestCountIncompleteAgreesWithThePerEntityScore`. The table is read by the completeness score (a 30-point
"no renditions" term) and written by `contentItemEdit`, holds addresses and never bytes, and therefore
belongs with `content_sources` as a references table. The migration was deleted and the spec, plan and
TRACK were corrected. **A claim that a thing is unreferenced is a measurement.** The migration was also the
only part of this respec that would have changed a product rule (what "incomplete" means for a content
item) as a side effect, which is reason enough on its own not to have done it.

## Not done, named rather than implied

- Frame hashes (above): decided, not built.
- Phase 8's remaining slice (8e) and phases 11-17: untouched by this respec.
- No live walk: this respec's surfaces are a refusal and an absence, and both were driven through the real
  wire and the real scheduler in tests. A deployed instance would show the same thing in the startup log
  ("content_fetch_sources is off: ... the ingest drain and the health sweep are NOT registered") and in
  `submitContentUrl`'s answer, and neither was run against a deployment in this pass.
