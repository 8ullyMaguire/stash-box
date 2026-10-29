# Monetisation — stash-box / stashdb

Programme spec: `/home/alvaro/secondbrain/10-Projects/2026-10-29-estate-monetization-spec.md`
Phases and order: `/home/alvaro/secondbrain/10-Projects/2026-10-29-monetization-roadmap.md`

## This product

Sponsorship only. No banners: it is an API-first catalogue and clients, not readers, use it.

## Where the code goes

* `frontend/index.html — React shell (no ad code belongs here)`
* `the about/donation route — one sponsor line, rel=sponsored`

## Known gap

Nothing to measure: no display inventory by design.

## Rules that are not negotiable (full list in the programme spec)

* No ad before an age gate, on an error page, or on any `/api/*` path — the estate's no-trap gate checks
  the last one.
* Every slot has a reserved height; CLS stays at 0. A banner that pushes text is worse than no banner.
* Consent before any personalised ad; non-personalised is the default, not the fallback.
* Adult inventory uses adult networks only. A mainstream network on this surface ends that account.

## Measurement

`~/estate-gates/pageviews.sh` on thinkcentre reports this host's pageviews; `rpm.py` prints RPM against
`revenue.csv`. Nothing here counts as measured until both are non-zero for a full week.
