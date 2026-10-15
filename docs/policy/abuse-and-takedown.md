# Abuse and takedown policy

This is the repository's copy of the text published at `GET /policy`. Both exist on purpose and they are
different documents for different readers: the served page is what a person holding a notice is told, and
this file additionally records WHY each commitment is what it is, and what the peer relationship is on the
other side of it. Neither is generated from the other (`internal/api/routes_policy.go` says why), and the
integration test asserts the served page still names the commitments below, so the two cannot quietly drift
apart in the direction that matters.

**Phase 14's ordering, restated here because this file is the artifact that gates phase 10.** The plan's hard
constraint is that this text precedes the media plane's first stored byte. The reason is not paperwork: a
catalogue that indexes addresses has one set of obligations, and a host that serves copies has another
(higher) set. Enabling `content_delivery` makes this instance a host, so an operator who enables it must
revise this policy -- and the served page says so, in the last section, before the first byte.

## What this instance is, in one sentence

A catalogue: it records addresses, fingerprints and descriptions so works can be found and identified. It
does not store works and does not serve them, so a removal here removes an index entry, not a copy.

## The notice path (built, phase 14 slice 1)

| Step | What happens | Where it lives |
|---|---|---|
| A notice arrives at the configured address | `abuse_contact`; the served policy states when it is unset rather than showing a blank | `GET /policy`, `internal/config` |
| Verified request to remove material | actioned the same day, acknowledged within 24 hours, and the answer names what was taken out | operator procedure; the audit trail is the record |
| The address stops being used | `takedownContentUrl` for one URL, `blockContentHost` for a site; both mark every existing record AND refuse the address from entering again | `internal/service/content/takedown.go`, migration 134 |
| The metadata stays | fingerprints and descriptions survive, marked refused: they are what recognises the same work at a new address, and deleting them is what lets material come back unnoticed | `content_sources` status, `mod_audit` |
| The refusal is enforced in one place | the submit path and the drain both consult the block list, so a refused address is refused at submission AND skipped if it was already queued | `internal/service/content/policy.go` |
| Every decision is recorded | operator, reason and key in `mod_audit` | migration 57, 134 |

## The appeal (built, phase 14 slice 2)

A refusal that can only go one way is a verdict, not a policy. Any authenticated member may contest a
refusal, and the appeal is decided by a moderator who is **neither the operator who recorded the block nor
the member who filed the appeal** -- enforced by comparing identities, never by checking a role, because a
role check passes for the same human holding a second role, which is the failure the rule exists to prevent.

- **Filed against the address**, not a case id: the block list is an operator surface, and a person who was
  refused knows what they submitted.
- **One appeal per person per block.** A second is refused by name rather than silently merged.
- **A grant releases the refusal** through the same code path an operator's own release uses, in the same
  transaction as the decision, so there is one release path and no second meaning for "unblocked".
- **The appeal survives the refusal it contests.** A release deletes the block row; the appeal's reference to
  it goes null (`ON DELETE SET NULL`) while `kind` and `value` stay, because "somebody contested this and
  here is what happened" is the fact an operator investigates months later.
- **Decisions are recorded twice over**: the deadline-free record is the appeal row itself (who filed, when,
  who decided, what was decided, and the note), and the trail row is in `mod_audit` under
  `SOURCE_APPEAL` / `SOURCE_APPEAL_DECISION`.
- **Half-states cannot exist.** Outcome, decider and decision time move together under CHECK constraints, so
  "decided with no outcome" and "granted by nobody" are unrepresentable rather than merely unlikely.

## The per-peer relationship

This instance is a peer, not a platform, and it administers nothing but its own database.

- **Receiving**: if a notice concerns an address obtained from this instance, the expectation is not blind
  forwarding. Apply your own law and your own judgement, and tell us if you refuse -- a refusal we know about
  is one we can answer for.
- **Sending**: when this instance receives a notice about an address a peer references, it removes its own
  record and does not reach into the peer's. It holds an address, not a copy.
- **No platform-wide policy is claimed.** This text binds this instance. What another operator does is theirs
  to publish, and a single policy spanning instances this operator does not administer would be a claim about
  machines nobody here controls.

## Deliberately not done, and why

- **Hash matching against an external CSAM list.** Nothing here accepts uploads, so there is no ingest to
  match against. The refusal is at the source layer, where a refused source is simply not fetched.
- **A registered DMCA agent, counter-notice paperwork, designated-agent registration.** Paperwork about a
  host, and this programme is not one. Enabling hosting re-opens this line.
- **Performer age verification as a gate.** Nothing here publishes performer content.
- **Elected moderators.** A governance question about a membership, and it needs members first.
- **A published transparency report.** The `mod_audit` rows are the report's contents; rendering them is UI
  work in phase 16, and this slice does not claim a report it does not render.

## Verification of the claims above

```bash
go test ./internal/service/content/ -count=1                    # the rules, as a table
POSTGRES_DB='postgres:smoke_pw@127.0.0.1:55434/stash-box-test?sslmode=disable' \
  go test -tags integration -count=1 -p 1 -run 'Appeal|Policy' ./internal/api/
python3 scripts/walk-14-live.py                                 # the same claims, against the deployed instance
```
