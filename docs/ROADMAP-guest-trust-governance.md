# Guest access, trust levels and self-governance — stash-box

Status: **proposal**, from the owner's request of 2026-10-16. It records intent in this repo's own
terms; nothing here is implemented. The estate-wide standard (items GS-1…GS-5, every setting
configurable, defaults and firewalls) is:

    secondbrain/10-Projects/2026-10-16-guest-access-trust-and-self-governance.md

This file is the site-specific half: what exists here today, what this site adopts, and the
firewalls that differ from the standard because this site is not the same shape as the others.

Stack: Go.

## Where stash-box stands today (measured 2026-10-16)

- `moderator` (214), `reputation` (18), `trust_level` (6) exist
- no guest identity, no consent gate

## What this site adopts

- GS-1 … GS-5, with the adult-content firewalls

## Config deltas (everything else is the standard's default)

- `consent.age_gate = required`
- `guest.upload_quarantine = true`
- `guest.mau_limit = 500`

## Firewalls specific to this site

- The cookie toast is **not** an age gate — two separate gates, two separate records; conflating them is how a consent dialog becomes an accidental age verification.
- Guest-visible content must respect the same content gating as a new account, from the first request.

## Order of work

GS-3 (both gates) → GS-1 → GS-2 → GS-4 → GS-5. The standard's §9 explains the ordering: consent
first, because the guest identity *is* a cookie and the gate has to exist before the thing it gates;
the fold second, because offering guest access without it creates work to clean up later.

## If this is implemented

Every step needs a test that fails without it, and the numbers belong in this file when they
are real — the estate's rule is that a claim of done carries the command that proves it. The
two items in the standard that nothing in this repo has yet are the elastic trust rule (§4)
and the jury drawn from the eligible pool (§5): both are cheap to fake and therefore need a
test that would pass for the wrong reason to be written, and killed, on purpose.
