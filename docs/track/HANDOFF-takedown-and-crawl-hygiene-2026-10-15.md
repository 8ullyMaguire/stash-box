# HANDOFF — keeping the aggregation, and what makes that defensible (2026-10-15)

## The question this answers

Owner, after adopting the verdict that aggregation-as-described is the wrong product:

> *"we have already implemented most of this functionality and it would be a pity to remove it now and
> make it wasted time. what's the best way to keep that functionality while minimizing dmca risk"*

The answer built today: **keep the metadata machine, take away every property that made it a target.**
Nothing was deleted. Three capabilities were added, one invariant was made mechanical, and the posture
that was a switch in one place is now a switch plus four things that hold whatever the switch says.

## What was built (all of it verified, none of it a note)

1. **The takedown verb** — `internal/service/content/takedown.go`, `internal/api/resolver_takedown.go`,
   migration `134_content_source_takedown` (`content_source_block`), GraphQL `takedownContentUrl` /
   `blockContentHost` / `releaseContentBlock` (ADMIN). Idempotent, audited in `mod_audit` with the
   operator and the reason, enforced at BOTH the submit path and the drain (`content/policy.go`), and the
   metadata stays when an address is taken out of service.
2. **Host blocks** — a `HOST` block refuses an entire site; a `URL` block refuses one normalized address;
   `UNIQUE (kind, value)` makes a repeat a no-op; release exists because a refusal with no release is a
   support queue.
3. **Crawl hygiene** — `internal/service/hostrules`, used by both clients that make automated outbound
   requests: the ingest page fetch (`internal/service/ingest/extract/http.go`) and the health sweep's
   `HEAD` (`internal/service/content/health.go`). `robots.txt` (RFC 9309: wildcards, `$` anchoring,
   longest-match-wins, allow-wins-ties, groups combined rather than most-specific-wins), fail-closed on an
   unreadable file, one shared robots cache, one per-host pace (`content_fetch_min_interval`, default 2s).
   A refused path is never requested and is recorded as `robots_disallowed`; in the sweep a policy
   refusal is INCONCLUSIVE while a host that cannot answer at all is a check failure.
4. **The mechanical refusal** — `scripts/check-no-content-plane.py` (14 patterns, `--self-test`), wired
   into `docs/goal-check.py` clause C7 as a fourth item: no player, no embed, no proxy, no transcode, no
   `mp4`, no frame of anybody else's video, ever, in a green tree.

## What "keep it" means in practice

The functionality is fully present and one key away: `content_fetch_sources: true` in `stash-box.yml`
registers the cron jobs and lets the page fetch, the sweep and the Wayback submission run — now obeying
the host's rules and the pace, and answerable with a takedown the same day. The switch stays **off** by
default because the default is what every operator who forks this inherits; "fetch other people's pages"
should be a decision somebody made. Nothing about the refusal (item 4) depends on that key.

## Verification actually run (commands, and what they returned)

| Command | Result |
|---|---|
| `go test ./internal/service/hostrules/ -count=1` | ok. The pacing test FAILED first: the implementation reserved the *decision* time rather than the *departure* slot, so two requests could leave back-to-back. Fixed in `wait`. |
| `go test ./internal/service/ingest/extract/ -count=1` | ok. Two existing tests caught a taxonomy error — "robots.txt said no" and "robots.txt could not be read" were one error; split into `ErrDisallowed` / `ErrRobotsUnavailable`, with the sweep treating the second as a genuine check failure (that IS link rot) and the extractor reporting it as unreachable. |
| `go test ./internal/service/content/ -count=1` | ok, after the existing `TestHTTPCheckerSeparatesARefusalFromADeath` was updated: the checker now legitimately asks `robots.txt` first, so the server answers that path and the HEAD assertion still holds. |
| `go test -tags integration -count=1 -p 1 -run TestADrainRecordsARobotsRefusal ./internal/api/` | ok — a real drain against an `httptest` host: `reason=robots_disallowed`, `status=pending`, page hits **0**; the control path fetches and counts 1. |
| `python3 scripts/check-no-content-plane.py --self-test` | ok — finds a planted `<video>`, tolerates a clean file, and is a text scan (comments count). |
| `python3 scripts/check-no-content-plane.py` (then with a planted `iframe` probe file, then without) | exit 0 → exit 1 naming `internal/service/content/player_probe_tmp.go:4` → exit 0. The check is proven in both directions, which is the only way a "zero hits" claim is evidence. |

## What is left, and the honest boundaries

- **The switch is off by default.** Flipping it is one key; if you want it on for this instance, that is
  the change — say so and I will set it in the deployment's `stash-box.yml` rather than in the code
  default.
- **`Crawl-delay` in `robots.txt` is deliberately ignored**: this instance's pace is configured, and a
  host-set delay that changes behaviour from outside would be invisible in the operator's log. Named so
  it is a decision rather than an oversight.
- **The robots rule is not member-configurable** and never will be. A host's terms are the host's.
- **Phase 14's remaining items are refused-with-reasons, not deferred-by-forgetting**: no CSAM hash list
  (nothing accepts uploads), no registered agent/counter-notice paperwork (process about a host), no age
  gate (nothing publishes performer content), no transparency report yet (the `mod_audit` rows are the
  report; rendering it is phase 16).
- **Not covered by the switch, and unchanged**: `internal/storage/favicon.go`, `internal/webhook`,
  `internal/service/federation/client.go`, `internal/sdbimport`. Those are upstream or mesh behaviours,
  and the respec's claim is about content sources.
- **The forks/mirrors are not in scope here** — this is the metadata instance's posture.
