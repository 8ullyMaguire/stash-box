#!/usr/bin/env python3
"""Live walk: the detector registry's STRANDED state (SPEC 7.24.4, plan phase 3a).

What this walks, and why it needs a live instance rather than a test:

`enabled` is a stored fact (an operator's switch) and `implemented` is a computed
one (whether THIS binary has a query for the slug). The state that matters is the
row where they disagree -- switched on, unimplementable by this build -- because:
the board's own validation REFUSES to answer while such a row stands, so every
curator sees an error rather than a work list, and the ONLY thing that can tell
them why is the registry query, which must therefore keep answering while the
board is refused.

That pair of claims (board refused, registry answering) cannot be asserted by a
unit test and must not be asserted by an e2e spec: planting the row breaks the
board for every parallel spec. So it is a walk, with a finally that removes it.

The connection string and URL come from the environment and there are no
defaults: a walk that silently points at the wrong instance is the defect this
whole file exists to avoid.
  STASH_BOX_DATABASE  a libpq-style DSN WITHOUT a scheme: user:pw@host:port/db
  STASH_BOX_URL       e.g. http://127.0.0.1:9997
  STASH_BOX_ADMIN     the admin account whose stored api_key is used (default e2e_admin)

Run: STASH_BOX_DATABASE=... STASH_BOX_URL=... python3 scripts/walk-3a-lint-registry-live.py
"""

import json
import os
import re
import subprocess
import sys
import urllib.error
import urllib.request

SLUG = "walk_3a_not_in_this_build"

DSN = os.environ.get("STASH_BOX_DATABASE", "")
URL = os.environ.get("STASH_BOX_URL", "")
ADMIN = os.environ.get("STASH_BOX_ADMIN", "e2e_admin")

if not DSN or not URL:
    sys.exit("STASH_BOX_DATABASE and STASH_BOX_URL are required (no defaults)")

# ONE regex, per the skill: splitting on '@' then ':' crashes on the first DSN.
M = re.match(r"^(?P<user>[^:]+):(?P<password>[^@]*)@(?P<host>[^:]+):(?P<port>\d+)/(?P<db>[^?]+)", DSN)
if not M:
    sys.exit(f"could not parse STASH_BOX_DATABASE: {DSN!r}")
assert M is not None  # narrowing for the type checker; the guard above is the rule

verdicts = []


def verdict(label, ok, description):
    verdicts.append(ok)
    print(f"VERDICT {label}: {'OK' if ok else 'FAILED'} -- {description}")


def psql(sql):
    env = dict(os.environ, PGPASSWORD=M["password"])
    out = subprocess.run(
        ["psql", "-h", M["host"], "-p", M["port"], "-U", M["user"], "-d", M["db"], "-tAc", sql],
        capture_output=True, text=True, env=env,
    )
    if out.returncode != 0:
        print(f"!! psql refused: {out.stderr.strip()}")
        return ""
    return out.stdout.strip()


def gql(query, key):
    body = json.dumps({"query": query}).encode()
    req = urllib.request.Request(
        f"{URL}/graphql", data=body,
        headers={"Content-Type": "application/json", "ApiKey": key},
    )
    with urllib.request.urlopen(req) as resp:
        return json.load(resp)


def errors_of(payload):
    return [e.get("message", "") for e in (payload.get("errors") or [])]


BOARD = "{ lintQuestBoard(limit: 1) { slug count } }"
DEFS = "{ lintQuestDefinitions { slug enabled implemented isBuiltin } }"

# The admin's key is the one the server has stored, and it is read rather than
# minted: a minted token is refused until it is written back to the row, which is
# the trap the skill spells out.
# `users` has no soft-delete column on this schema; the name is the key.
admin_key = psql(f"select api_key from users where name = '{ADMIN}'")
if not admin_key:
    sys.exit(f"no stored api_key for {ADMIN}; start the instance with the bootstrap env first")


def plant(enabled):
    psql(
        "insert into lint_quest_definitions (slug, description, enabled, bounty_points, is_builtin, authored_by) "
        f"values ('{SLUG}', 'planted by walk-3a', {str(enabled).lower()}, 1, true, null) "
        f"on conflict (slug) do update set enabled = {str(enabled).lower()}"
    )
    return psql(f"select enabled from lint_quest_definitions where slug = '{SLUG}'")


def remove():
    psql(f"delete from lint_quest_definitions where slug = '{SLUG}'")
    return psql(f"select count(*) from lint_quest_definitions where slug = '{SLUG}'")


try:
    # ---- state 1: the row exists and is switched on -------------------------
    planted = plant(True)
    verdict("planted", planted == "t", f"the row exists and is enabled (enabled={planted!r})")
    if planted != "t":
        raise SystemExit("the subject of this walk was never created; nothing below would mean anything")

    # ---- the registry reports the disagreement ------------------------------
    defs = gql(DEFS, admin_key)
    row = next((d for d in (defs.get("data") or {}).get("lintQuestDefinitions", []) if d["slug"] == SLUG), None)
    verdict(
        "registry-names-it",
        bool(row) and row.get("enabled") is True and row.get("implemented") is False,
        f"the registry reports enabled=True implemented=False (row={row})",
    )

    # ---- the board is REFUSED, and says which slug --------------------------
    board = gql(BOARD, admin_key)
    msgs = errors_of(board)
    verdict(
        "board-refused",
        not board.get("data") and any(SLUG in m for m in msgs),
        f"the board answered with an error naming the slug (errors={msgs[:1]})",
    )
    print(f"      the refusal, verbatim: {msgs[0] if msgs else '(no error)'}")

    # ---- and the registry STILL answers while the board is refused ----------
    # This is the claim the page exists for: the diagnostic must survive the fault.
    still = gql(DEFS, admin_key)
    still_row = next((d for d in (still.get("data") or {}).get("lintQuestDefinitions", []) if d["slug"] == SLUG), None)
    verdict(
        "registry-survives-the-refusal",
        bool(still_row) and not errors_of(still),
        "the registry answers with the offending row while the board is refused",
    )

    # ---- state 2: retired -> the board works again --------------------------
    retired = plant(False)
    verdict("retired", retired == "f", f"the row is switched off (enabled={retired!r})")
    after = gql(BOARD, admin_key)
    verdict(
        "board-recovers",
        bool(after.get("data")) and not errors_of(after),
        "retiring the row is enough for the board to answer again",
    )
    # The retired row is still LISTED -- retirement is a rollback tool, not a delete.
    retired_defs = gql(DEFS, admin_key)
    retired_row = next((d for d in (retired_defs.get("data") or {}).get("lintQuestDefinitions", []) if d["slug"] == SLUG), None)
    verdict(
        "retired-row-still-listed",
        bool(retired_row) and retired_row.get("enabled") is False,
        f"a retired row stays in the registry (row={retired_row})",
    )
finally:
    left = remove()
    verdict("cleaned-up", left == "0", f"the planted row is gone (rows left={left!r})")

print()
print(f"{sum(verdicts)}/{len(verdicts)} verdicts OK")
sys.exit(0 if all(verdicts) else 1)
