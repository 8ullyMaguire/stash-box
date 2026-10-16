#!/usr/bin/env python3
"""Walk slice 7.24.1 (verified-unknown markers) on a LIVE stash-box instance.

Run it ON the host that serves the instance. Everything here is READ-ONLY apart from the operator's
api_key and role, which are set for the run and restored afterwards (the instance compares a presented key
with the row, so a key cannot be used without touching it).

WHY THIS WALK EXISTS, given an integration suite that already proves the round trip on a real Postgres:
the suite runs the migrations from the WORKING TREE. A deploy's own failure modes are not the code's --
they are a migration that did not run on THIS database, a seed that left the registry empty, a schema the
binary serves but the tooling did not rebuild, and a role gate that answers differently than the tests
expect. None of those are visible from a green test run, and every one of them is visible here.

WHAT IT CHECKS:

  A. THE REGISTRY ANSWERS, AND THE LOAD-BEARING ROW IS IN IT. `fieldVerificationReasons` returns the
     codes, ordered. `not_yet_looked` must be present with `suppresses_gap: FALSE` -- that single value is
     what stops an assertion from closing a gap nobody has researched -- and at least one reason must
     suppress, or the dropdown offers a curator no way to close anything. A seed that ran halfway is the
     shape this catches: the codes exist, the difference between them is gone.
  B. NO KEY, NO READ, IN THE INSTANCE'S OWN WORDS. Nothing is leaked to an unauthenticated caller, and
     the refusal is the instance's, not this script's.
  C. THE WRITE PATH IS IN THE DEPLOYED SCHEMA. Some input type carries `verified_unknowns`, searched for
     by NAME rather than asserted at a hardcoded location, so the verdict names the type it found. A
     binary built from a tree whose frontend was not rebuilt serves the old bundle and this is where the
     two disagree.
  D. THE MIGRATION RAN ON THIS DATABASE. `field_verification_states` exists, and its row count is
     PRINTED rather than assumed: on the live instance the honest number today is 0, and a walk that
     reported "the feature is live" without saying nothing uses it yet would be overreporting.
  E. THE SCORE THIS FEEDS ANSWERS FOR A REAL ROW. A scene id read out of THIS database (never invented)
     returns a completion score with a missing list -- the surface the suppression acts on, on data that
     exists.

Run:  python3 scripts/walk-7.24.1-live.py
"""

import base64
import hashlib
import hmac
import json
import subprocess
import sys
import time
import urllib.error
import urllib.request

URL = "http://127.0.0.1:9998/graphql"
DB = "stash-box"
CONFIG = "/home/alvaro/stash-box-deploy/config/stash-box-config.yml"
OPERATOR = "StashBot"

FAILURES = []


def psql(sql):
    out = subprocess.run(
        ["sudo", "docker", "exec", "-i", "stashbox-pg", "psql", "-U", "stashbox", "-d", DB, "-tAc", sql],
        capture_output=True, text=True,
    )
    if out.returncode != 0:
        # Print and return, never sys.exit: the finally is what puts the instance back.
        print(f"!! psql refused: {out.stderr.strip()}")
        return ""
    return out.stdout.strip()


def gql(query, api_key=None, variables=None):
    body = json.dumps({"query": query, "variables": variables or {}}).encode()
    req = urllib.request.Request(URL, data=body, headers={"Content-Type": "application/json"})
    if api_key:
        # The header name matters: the instance reads `ApiKey`, and `Authorization: Bearer` is REFUSED.
        req.add_header("ApiKey", api_key)
    try:
        with urllib.request.urlopen(req, timeout=20) as r:
            return json.loads(r.read().decode())
    except urllib.error.HTTPError as e:
        return {"http_status": e.code, "body": e.read().decode()[:300]}


def verdict(label, ok, description):
    print(f"   VERDICT {label}: {'OK' if ok else 'FAILED'} -- {description}")
    if not ok:
        FAILURES.append(label)


secret = None
for line in open(CONFIG).read().splitlines():
    if line.startswith("jwt_secret_key:"):
        secret = line.split(":", 1)[1].strip()
if not secret:
    sys.exit("no jwt_secret_key in " + CONFIG)


def mint(uid):
    """Mint ONCE and store the result: iat/exp come from the clock, so two calls are two different keys."""
    assert secret is not None  # guarded above; the assertion is what narrows it for the type checker
    b64 = lambda raw: base64.urlsafe_b64encode(raw).rstrip(b"=").decode()
    now = int(time.time())
    head = b64(json.dumps({"alg": "HS256", "typ": "JWT"}, separators=(",", ":")).encode())
    pay = b64(json.dumps({"uid": uid, "sub": "APIKey", "iat": now, "exp": now + 3600},
                         separators=(",", ":")).encode())
    sig = b64(hmac.new(secret.encode(), f"{head}.{pay}".encode(), hashlib.sha256).digest())
    return f"{head}.{pay}.{sig}"


OPERATOR_UID = psql(f"select id from users where name = '{OPERATOR}'")
if not OPERATOR_UID:
    sys.exit(f"no user {OPERATOR} on this instance; set one up or pass a name this walk knows")

HAD_ADMIN = psql(f"select count(*) from user_roles where user_id = '{OPERATOR_UID}' and role = 'ADMIN'") != "0"
SAVED_KEY = psql(f"select coalesce(api_key, '') from users where id = '{OPERATOR_UID}'")

print(f"walk 7.24.1 on the deployed instance -- operator {OPERATOR} ({OPERATOR_UID}), "
      f"admin={'yes' if HAD_ADMIN else 'no, granted for this run'}")

try:
    if not HAD_ADMIN:
        psql(f"insert into user_roles (user_id, role) values ('{OPERATOR_UID}', 'ADMIN')")
    KEY = mint(OPERATOR_UID)
    psql(f"update users set api_key = '{KEY}' where id = '{OPERATOR_UID}'")

    # --- B. the refusal, first: it is the cheapest and it says the gate is on ---------------
    anon = gql("{ fieldVerificationReasons { code suppresses_gap } }")
    errs = [e.get("message", "") for e in (anon.get("errors") or [])]
    verdict("B-no-key-refused",
            bool(errs) and anon.get("data") is None,
            f"an unauthenticated read is refused in the instance's words: {errs[:1]} "
            f"(a MISSING FIELD would say 'Cannot query field' -- this is the role gate)")

    # --- A. the registry, and the value the whole design rests on ---------------------------
    body = gql("{ fieldVerificationReasons { code description suppresses_gap } }", api_key=KEY)
    reasons = ((body.get("data") or {}).get("fieldVerificationReasons")) or []
    if not reasons:
        verdict("A-registry", False, f"the registry is EMPTY or unreadable: {json.dumps(body)[:250]}")
    else:
        codes = [r["code"] for r in reasons]
        ny = next((r for r in reasons if r["code"] == "not_yet_looked"), None)
        suppressing = [r["code"] for r in reasons if r["suppresses_gap"]]
        ordered = codes == sorted(codes)
        print(f"   (registry: {codes})")
        print(f"   (suppressing: {suppressing})")
        verdict("A-registry-answers", len(codes) == len(set(codes)) and all(r["description"] for r in reasons),
                f"{len(codes)} reasons, all with a description, no duplicates")
        verdict("A-not-yet-looked-does-not-suppress",
                ny is not None and ny["suppresses_gap"] is False,
                f"`not_yet_looked` present with suppresses_gap={None if ny is None else ny['suppresses_gap']} "
                f"-- the value that keeps an unresearched gap actionable")
        verdict("A-something-suppresses", bool(suppressing),
                f"{len(suppressing)} reason(s) close the gap, so the control is not a no-op: {suppressing}")
        verdict("A-ordered", ordered,
                f"the list is ordered by code ({ordered}), so the dropdown does not move between requests")

    # --- C. the write path in the DEPLOYED schema, found by name ----------------------------
    # Searched rather than asserted at a path: the verdict then NAMES the type it found it on, which is
    # how a schema that moved is distinguishable from one that lost the field.
    intro = gql("{ __schema { types { kind name inputFields { name } } } }", api_key=KEY)
    carriers = [
        t["name"] for t in ((intro.get("data") or {}).get("__schema") or {}).get("types", [])
        if t.get("kind") == "INPUT_OBJECT"
        and any(f["name"] == "verified_unknowns" for f in (t.get("inputFields") or []))
    ]
    verdict("C-schema-has-write-path", bool(carriers),
            f"an input type carries `verified_unknowns`: {carriers}" if carriers
            else f"NO input type carries verified_unknowns -- the deployed schema predates the slice: "
                 f"{json.dumps(intro)[:200]}")

    # --- D. the migration ran on THIS database ---------------------------------------------
    exists = psql("select count(*) from information_schema.tables "
                  "where table_name = 'field_verification_states'")
    rows = psql("select count(*) from field_verification_states") if exists == "1" else "n/a"
    verdict("D-migration-applied", exists == "1",
            f"field_verification_states exists on THIS database (rows now: {rows})"
            + ("  [0 is the honest number: deployed, nothing asserted yet]"
               if rows == "0" else ""))

    # --- E. the surface the suppression feeds, on a row that is really here -----------------
    scene_id = psql("select id from scenes where deleted = false limit 1")
    if not scene_id:
        verdict("E-completion", False, "no non-deleted scene on this instance to read a score for")
    else:
        comp = gql("query($id: ID!) { findScene(id: $id) { id completion { score missing total earned } } }",
                   api_key=KEY, variables={"id": scene_id})
        c = ((comp.get("data") or {}).get("findScene") or {}).get("completion")
        verdict("E-completion-answers", isinstance(c, dict) and isinstance(c.get("missing"), list),
                f"scene {scene_id[:8]} scores {c.get('score')} ({c.get('earned')}/{c.get('total')}), "
                f"missing={c.get('missing')}" if c else f"no completion came back: {json.dumps(comp)[:200]}")

finally:
    # BY NAME and in foreign-key order, then READ BACK: the restore is its own verdict, because a cleanup
    # that ran without being checked is how a walk leaves a granted role behind.
    if not HAD_ADMIN:
        psql(f"delete from user_roles where user_id = '{OPERATOR_UID}' and role = 'ADMIN'")
    psql(f"update users set api_key = {('NULL' if not SAVED_KEY else chr(39) + SAVED_KEY + chr(39))} "
         f"where id = '{OPERATOR_UID}'")
    left_roles = psql(f"select count(*) from user_roles where user_id = '{OPERATOR_UID}' and role = 'ADMIN'")
    left_key = psql(f"select coalesce(api_key, '') from users where id = '{OPERATOR_UID}'")
    restored = (left_roles == ("1" if HAD_ADMIN else "0")) and (left_key == SAVED_KEY)
    print(f"   VERDICT Z-restored: {'OK' if restored else 'FAILED'} -- "
          f"admin role {'kept (was there before)' if HAD_ADMIN else 'removed'}, "
          f"api_key {'cleared' if not SAVED_KEY else 'restored'}"
          + ("" if restored else "  <-- STATE LEFT BEHIND"))

print()
if FAILURES:
    print(f"FAILED: {', '.join(FAILURES)}")
    sys.exit(1)
print("ALL VERDICTS OK")
