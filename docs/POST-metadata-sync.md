# Incremental metadata sync from stashdb.org — implemented, and one design decision worth arguing about

## The problem

My instance has ~1.2M records imported from stashdb.org: 1,103,442 scenes, 110,202
performers, 2,934 tags. And it has been drifting since the day the import finished,
because nothing reconciles it.

The importer I wrote is insert-only — **zero `Update*` calls** in the whole package.
It looks up each entity by natural key and a hit is a skip. So a "refresh" is a
full re-read of the source that changes nothing:

```
scanned 1.2M records → created 0 → skipped 1,103,442 → ~9.5 hours of API traffic
```

A typo fixed upstream, a corrected birthdate, a studio that turned out to be a
duplicate — none of it reaches me. Nor does it reach anyone else running a similar
setup.

## The good news: the API already supports this

I checked before proposing anything, and incremental sync needs **no server
change**. Two things are already there:

- `PerformerSortEnum` includes `UPDATED_AT`
- `Performer` returns `updated`

Verified against the live API with a real session cookie. Sorted by `UPDATED_AT`
descending, pages come back **monotonically decreasing across the page boundary**:

```
page 1 (per_page=25)  newest 2026-10-01T05:46:11  oldest 2026-09-30T14:54:26
page 2 (per_page=25)  newest 2026-09-30T14:54:11  oldest 2026-09-30T13:03:08
page 3 (per_page=25)  newest 2026-09-30T11:57:47  oldest 2026-09-30T10:37:23
```

Page 2's newest is one second *older* than page 1's oldest. That is exactly the
property a watermark walk needs.

So: sort by recency, walk until a record predates your watermark, stop. No cursor,
no revision feed, no server work.

One gap: there is **no `updated_at` filter**. `PerformerQueryInput` has no date
criterion at all — I enumerated it, and there is no `updated_at`, `created_at`,
`last_updated`, `modified` or generic `date`. So the client pages down and stops
itself rather than asking for a range. Adding that filter is the single change that
would make this cheap, and I'd like to know if it's welcome upstream.

## The decision I actually want argued about

Here is the rule I implemented, and it is deliberately asymmetric:

> **Upstream may fill or change a field. Upstream may never clear one.**

The reason is that "the source has no value for this field" and "this query did not
ask for the field" arrive as **the same bytes on the wire**. Both are empty.

Reading silence as "empty" means: upstream doesn't happen to carry a `height` for
this performer, so the curator's `height` gets nulled. Silently. Irreversibly.
Discovered weeks later, or never.

So a genuine upstream deletion — a URL removed, an alias retracted — **does not
propagate**. I chose that deliberately:

> The safe failure is a stale value, not a destroyed one.

A stale height is a cosmetic problem someone fixes. A silently deleted height on
110,000 records, discovered by a user noticing their data is gone, is the kind of
bug that ends trust in a tool.

I recognise the counterargument: it means removals never land, so a retracted
identity stays attached forever. That is a real cost and I don't think it's
obviously wrong, but it's a judgement call about whose data matters more — and it
belongs to whoever runs the instance, not to me. Making it configurable per field
would be the honest follow-up.

## Three modes

| Mode | Behaviour | Needs provenance? |
|---|---|---|
| `latest-wins` (default) | Most recent write survives, either side | yes |
| `upstream-wins` | Upstream overwrites local | no |
| `local-wins` | Local preserved, upstream fills gaps only | no |

`latest-wins` is the default because it needs no configuration and is right for the
common case: a curator fixes a typo here, upstream fixes the same field later, and
the last correction is the one you want.

It is also the only mode that *cannot* be decided from the data alone, and I want to
be straight about how it actually works. We know when **we** last wrote a field. We
know when upstream last touched the **record**. We do **not** know when upstream
last touched **that field** — that's upstream's provenance and it isn't ours to
write. So latest-wins compares the field's local write time against the record's
upstream timestamp. That's an approximation, it's stated as one in the code, and
it's wrong when a local curator and upstream edit the same field inside the same
window. The other two modes have no such caveat, which is a real part of their cost.

## The stop condition needed a margin

If `updated` ordering were a stable total order, the stop is `t.Before(watermark)`.
It isn't documented as one, and I verified three pages — which is not a
specification. Two records sharing an `updated` value with unstable relative order
would put one on the wrong side of the boundary, and the next run's watermark has
already moved past it, so that row is never revisited.

So the stop is `t.Before(watermark - 1h)`. Re-reading an hour of upstream changes
costs a page or two. Missing a boundary row costs a record that is wrong forever.

## What the tests do

I mutation-tested rather than trusting green. Seven deliberate defects, each one
plausible as a review slip:

1. drop the never-clear guard
2. make `latest-wins` always write
3. make `local-wins` overwrite set fields
4. make the name comparison case-sensitive
5. remove the stop margin
6. make `listEq` order-sensitive
7. let an unknown mode silently default

**7 of 7 killed.** Two of those mutations had already been caught the hard way
during development — my first implementation *did* clear fields when upstream was
silent, and a test I wrote asserted "no change" while its fixture omitted URLs from
the source record. The fixture was wrong, not the code. Both are now pinned by
named tests: `TestSilentUpstreamDoesNotClear` and
`TestUpstreamChangeStillPropagates`, the second so the safety rule can't quietly
turn every field into a no-op.

## Status and what's not done

- **Done:** the diff, the three modes, the watermark walk with margin, the safety
  rule, 7/7 mutation coverage. Performers only.
- **Not done:** the write path. This is the diff-and-decide half; it plans and
  reports, it does not yet call `Update`. That's the next piece, and it's the piece
  where `Performer.Update` takes a *complete* input and a sparse call would clear
  every field it didn't set.
- **Not done:** studios, tags, scenes. Same seam.
- **Not done:** provenance table. Needed only for latest-wins to stop being an
  approximation.
- **Deliberately not automatic:** whether this runs on a timer is the operator's
  call. It writes to curated data.

## Two things I'd like to ask upstream

1. **Is `UPDATED_AT DESC` ordering across pages a guarantee or an accident?** The
   whole incremental design rests on it. Documenting it, or adding a tiebreaker
   (`UPDATED_AT` then `id`), would make it trustworthy. Three pages is evidence, not
   a spec.
2. **Would an `updated_at` filter on the query inputs be welcome?** It turns "page
   down and stop yourself" into "ask for what changed", and it's the difference
   between a cheap sync and a cheap-ish one.

## Environment

- stash-box fork, ~1.2M records, PostgreSQL 18.6
- source auth is a form POST to `/login` and the returned session cookie; HTTP
  Basic is **not** accepted by the API even though the same credentials work for
  the web form
- importer run times for reference: performers 2h55m, scenes 6h43m
