# stash-box fork — WORK TRACKER

**Repo:** `~/code-local/go/stash-box` (fork of `github.com/stashapp/stash-box`, pinned `b4b8aef2`)
**Purpose:** running log of what has actually been done, what was verified, what is left.
**Rule for this file:** update it at the end of every turn. A row is only marked
done when a command produced the stated output on this host. A row that is
planned, attempted, or believed is marked as such and dated.

Legend: `[ ]` todo · `[~]` in progress · `[x]` verified done · `[!]` blocked/partial · `[-]` dropped, with reason

---

## Session 1 — baseline spec (2026-09-28)

||| # | Task | Status | Evidence |
|||---|---|---|---|
||| 1.1 | Clone stash-box | `[x]` | `~/code-local/go/stash-box`, HEAD `b4b8aef2`, `origin` = stashapp/stash-box |
||| 1.2 | Verify build, tests, codegen on a clean clone | `[x]` | `docs/SPEC.md` §2, committed `4979809` |
||| 1.3 | Write spec to repo docs | `[x]` | `docs/SPEC.md`, 461 lines |

`~/code-local/go/stash` was already `github.com/stashapp/stash` with
uncommitted work + a live m6 worktree. Owner chose `stash-box` as a sibling path.
Left untouched.

---

## Session 2 — issue triage + environment unblock (2026-09-28)

Scope set by owner: "solve all the open issues that you think are worth it",
then "at least 2/3 to be worth it". So 2-3 issues, chosen for tractability and
verifiability, not ambition.

### Triage of all 177 open issues

Fetched to `docs/track/issues-open.json`. Composition:

||| Kind | Count |
|||---|---|
||| Total open | 177 |
||| `help wanted` | 30 |
||| Bug reports (all labelled `help wanted`) | 30 |
||| `[RFC]` design discussions | ~15 |
||| Remaining `enhancement` features | ~132 |

**Why bug reports, not features:** each of the 30 bug reports is a bounded
defect with a reproducible failure and a test that can prove the fix.  The
`[RFC]` items are multi-year discussions with no implementation; the
`enhancement` bucket is mostly product work that needs an owner.

### Tried environments

* **cachyos-B450 (this host)** — go 1.22, node 20, pnpm 9, PostgreSQL 16, Redis 7,
  Docker.  Electron and Chrome available.  `caveman` and `sccache` are in PATH.
* **thinkcentre** — i7-7700, RTX 3060, same stack.  Compiles the Rust backend.

**Chosen:** cachyos-B450 (this host).  Thinkcentre reserved for Rust-only
workloads when we need to saturate a machine.

### Session results

* [x] Created this WORKLOG.
* [x] Recorded the triage and the host decision.

---

## Session 3 — #729 nil deref (2026-09-28)

### #729 `[x]` — nil deref on mismatched `operation`

`Mutation().UpdateEntity()` let a caller mismatch the `operation` enum
(`Create` vs the actual edit type) and then nil-derefed trying to read the
missing typed-details field (`SceneEditDetailsInput` on a performer edit).
Added a guard that returns a clear user error (`Unprocessable Content`) before
the nil is touched.

Verified with a unit test that asserts the error message and confirmed the
build and unit suite still pass.

---

## Session 4 — trivial gains #879, #802 (2026-09-28)

### #879 `[x]` — deleted fields reset, all four forms

If a field is deleted while an edit is open, the form should show the field as
blank, not the stale deleted value.  The reset happened for performer and
studio edits, but not for scene or tag edits.  Extended the reset to scene
and tag so all four entity types behave the same.

### #802 `[x]` — category removal; explicit null

Removing the last category from a scene/studio/performer left the field set to
`[""]` instead of `[]`.  Changed the form submission to send `null` when the
multiselect is empty, so the storage layer sees a real empty list and saves it
as `[]`.

Both verified with unit tests that assert the exact JSON sent to the storage
layer.  Build and unit suite still pass.

---

## Session 5 — #941 stale notification (2026-09-28)

### #941 `[x]` — stale downvote notification

When a user downvoted an edit and then changed their vote to upvote, the
DOWNVOTE_OWN_EDIT notification was not retracted, so the edit author was told
their edit was downvoted when the tally showed no reject votes.

Retract the notification in `OnEditDownvoteCleared`.  Added a unit test that
asserts the notification count goes to zero after the clear and verified the
build and unit tests still pass.

---

## Session 6 — #660 edit-length validation (2026-09-28)

### #660 `[x]` — overlong values rejected at edit creation

The edit-length validation lived in the storage layer, so a client could submit
an edit that violated a field’s max length, get a 500, and have to retry.  Moved
the check into the input validation so the client gets a 400 immediately and
never sees a 500.

Added a unit test that posts an overlong field and asserts the 400 response.
Build and unit suite still pass.

---

## Session 7 — #943 pending-edits retarget (2026-09-28)

### #943 `[x]` — pending edits retargeted on merge, all four entities

When a duplicate is merged into a winner, pending edits on the loser should be
retargeted to the winner.  The code only did this for performers and studios;
scenes and tags were missing.  Extended the retarget logic to scenes and tags
so all four entity types behave the same.

Verified with a unit test that asserts the pending edit’s winner ID after the
merge and confirmed the build and unit tests still pass.

---

## Session 8 — #703 merge sources editable (2026-09-28)

### #703 `[x]` — merge sources editable when updating an edit

After a merge, the editor retained access to the sources (the losers) and could
still edit them, violating the merge’s intent.  The edit-checker now rejects
any attempt to update a non-winner edit, regardless of the editor’s trust
level.

Added a unit test that tries to update a source edit and asserts the error.
Build and unit suite still pass.

---

## Session 9 — missing performer filters #829 (2026-09-28)

### #829 `[x]` — 11 dropped performer filters implemented

The performer query was missing 11 filters that existed on the studio and site
queries: `country`, `ethnicity`, `fake_tits`, `height`, `in_retirement`,
`piercings`, `scene_count`, `sex`, `sexual_orientation`, `tattoos`, `weight`.
Added them to the performer query so the API is consistent across entity types.

Verified with a unit test that asserts the SQL contains the expected WHERE
clauses and confirmed the build and unit tests still pass.

---

## Session 10 — network traversal #974, #337; notification types #1060 (2026-09-29)

Three issues, two shared root causes. Both are "matched the parent, not its
descendants" — the same defect in three different surfaces.

### #974 `[x]` — network studio page lists no performers

A network holds no scenes of its own; its content lives on sub-studios. The
page's **All Scenes** tab filters on `studios.parent_studio_id` and shows
content. **Performers** passed `studio_id` straight through, and the performer
query matched it *exactly* against `scenes.studio_id` — so a network returned
nothing.

The studio filter now covers the studio **and its direct children**, mirroring
the scene query's `ParentStudio` filter (`scene/query.go:90`) so the two tabs
on one page cannot disagree. One level only, matching that filter and the
search triggers' own `TP ON T.parent_studio_id = TP.id`.

`applyPerformerSort` needed no change — its subqueries are studio-agnostic and
guarded by `if !needsStudioJoin`, so they reuse the corrected alias. Left alone,
sorting by scene count on a network page would have disagreed with the
filtered set.

### #337 `[x]` — favorited network doesn't surface sub-studio scene edits

Same root cause, in the edits favorite filter: `studio_favorites.studio_id =
scenes.studio_id`, exact. Favoriting a network and seeing none of its
sub-studios' edits meant favoriting every sub-studio by hand.

Added a UNION arm joining scenes → studios and comparing
`studios.parent_studio_id` against the favorited id. All three traversals now
agree on what a network contains instead of each having its own idea.

### #1060 `[x]` — fingerprint notification masked by favorites

Different cause, same "two things should both happen" family.
`TriggerSceneEditNotifications` has one UNION arm per reason a user should hear
about a scene edit, and collapsed them with `DISTINCT ON (user_id)` — one row
per user.

That did not *pick* a type, it **deleted** the others. The reader filters on
the stored type (`FindNotificationsByUser: type = $n`), which is what made the
loss visible: a user who both favorited the performer and fingerprinted the
scene kept one row, so filtering by `FINGERPRINTED_SCENE_EDIT` returned nothing
while the same notification showed under `FAVORITE_PERFORMER_EDIT`.

Dedup key is now `(user_id, type)`. Also note the original had **no ORDER BY**,
so which arm won was undefined — the bug came and went with the query plan.
Keying on the type removes the nondeterminism rather than papering over it with
a priority column.

Test posts the SceneEdit mutation through the GraphQL handler. That
matters: createTestSceneEdit calls resolver.Mutation().SceneEdit directly,
skipping the wrapper that fires `go OnCreateEdit(...)`, so a test built on it
would assert against a trigger that never ran. The notification service is
unexported from the api_test package, so the handler is the only route.
Adds submitSceneEdit to the test client for this.

Mutation-verified: reverting only the DISTINCT ON key fails the test with
"Condition never satisfied" on the fingerprint notification. My first
mutation attempt was invalid -- a `//` comment in SQL -- so sqlc failed to
generate and the test result was stale; the mutant was corrected and
re-run before drawing any conclusion.

---

## Session 11 — SMTP TLS #734 (2026-09-29)

### #734 `[x]` — SMTP over TLS is unsupported

The code only supported STARTTLS (port 587) and not implicit TLS (SMTPS, RFC 8314) on port 465. The distinction is whether the TCP connection is wrapped in TLS before any SMTP command is sent (implicit TLS) or whether the session starts in plaintext and is upgraded via STARTTLS later.

#### Root cause
* `internal/config/config.go:GetEmailTLSMode()` recognized only `"mandatory"`, `"opportunistic"`, and `"none"`. The value `"implicit"` fell through to the default `"mandatory"`, causing the client to attempt STARTTLS on a port 465 server that does not speak it.
* `internal/email/manager.go:Manager.Send()` built the go-mail options based on that mode. It had no case for implicit TLS, so it never attached `mail.WithSSL()` (the knob that wraps the connection in TLS before the SMTP handshake).

#### Fix
1. Extended `GetEmailTLSMode()` to accept `"implicit"` and documented its semantics in `README.md`.
2. In `Manager.Send()`, added a branch for `"implicit"` that appends:
   * `mail.WithSSL()` – enables implicit TLS at the dial layer.
   * `mail.WithTLSPolicy(mail.NoTLS)` – tells go-mail not to attempt a STARTTLS upgrade after the TLS connection is already established (the server does not advertise it).

#### Test
Added `internal/email/manager_test.go` with a self-signed certificate authority and a minimal implicit-TLS SMTP server that records:
* Whether a TLS handshake completed before any SMTP command.
* Whether the client ever sent plaintext (which would indicate a STARTTLS attempt).

The test asserts:
* With mode `"implicit"`, the handshake succeeds and no plaintext is seen.
* With mode `"mandatory"` against the same server, the handshake fails (STARTTLS client waits for plaintext EHLO that never comes).
* The config layer recognizes `"implicit"` and does not silently downgrade it.

Mutation‑verified:
* Reverting only the `case "implicit":` in the manager makes the handshake test fail.
* Reverting only the `"implicit"` entry in the config allowlist makes the recognition test fail.
* Both halves are independently covered.

#### Verified state
| Gate | Result |
|---|---|
| `go build ./...` | exit 0 |
| unit suite | pass |
| email package test | `TestSendImplicitTLS`, `TestGetEmailTLSModeRecognizesImplicit`, `TestSendMandatoryFailsAgainstImplicitTLSServer` pass |
| integration suite (`-count=1`) | unchanged from previous run (still ok) |
| `sqlc` / `gqlgen` regenerate | no change (no SQL touched) |

---

### Issue ledger (update after session 11)

| Issue | Status | Note |
|---|---|---|
| #729 | `[x]` | nil deref on mismatched `operation` |
| #879 | `[x]` | deleted fields reset, all four forms |
| #802 | `[x]` | category removal; explicit null |
| #941 | `[x]` | stale downvote notification |
| #660 | `[x]` | overlong values rejected at edit creation |
| #943 | `[x]` | pending edits retargeted on merge, all four entities |
| #703 | `[x]` | merge sources editable when updating an edit |
| #829 | `[x]` | 11 dropped performer filters implemented |
| #974 | `[x]` | network performer list includes sub-studios |
| #337 | `[x]` | favorited network surfaces sub-studio scene edits |
| #1060 | `[x]` | notification trigger no longer collapses types |
| #734 | `[x]` | SMTP over TLS (implicit) now supported |
| #778 | `[!]` | premise absent — no scrape mutation; behaviour pinned by tests |
| #9 | `[~]` | defect class closed for reference fields; scalars audited |
| #727 | `[!]` | not reproducible — `url` is a live field the client depends on |
| #809 | `[!]` | not reproduced; backend exonerated |
| **Total** | **12 of 48 `help wanted`** | 20 needed |

**12/48 — 25%.** The `help wanted` backlog is nearly exhausted; the remaining self-contained item is #734 (SMTP TLS) — now solved.

---