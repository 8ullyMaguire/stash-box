#!/usr/bin/env python3
"""Walk slice 11a (the invite allowance follows the level) on a LIVE stash-box instance.

Run it ON the host that serves the instance: it reads the jwt secret from the deployment config
and the users from the instance's Postgres, and it restarts the service to clear the auth cache.

What it changes, and puts back in a finally block: two users' api_key values (a JWT carries `iat`,
so the server's "presented key == stored key" check means a key must be minted ONCE and reused),
the MANAGE_INVITES/READ role holdings the walk's two actors need, and one invite token (granted,
spent, refunded, then revoked). Nothing is printed that is a credential.

WHAT THIS WALK CANNOT SHOW, and says so rather than implying otherwise: THE LEVEL ARM. Every
account on this instance has no trust rollup, so every account sits at the level that grants no
keys -- and raising one honestly means contributions scored through the voting system, which is
not a walk. The level arm is covered where it can be asked: the wire tests drive a real member
from level 0 to 1 to 2 with real trust events and assert the cap, the freed slot and both
refusals. What the walk proves on the deployed binary is the rest of the rule: the allowance is
derived per request, a refusal names the LEVEL, an operator's grant buys exactly one key WITHOUT
raising the cap, and a rescind refunds a grant only because the key was never used.

See docs/plan/feature-content-aggregator.md, phase 11 slice 11a.
"""

import base64
import hashlib
import hmac
import json
import os
import subprocess
import sys
import time
import urllib.error
import urllib.request

URL = "http://127.0.0.1:9998/graphql"
DB = "stash-box"
CONFIG = "/home/alvaro/stash-box-deploy/config/stash-box-config.yml"
ENV = dict(os.environ, XDG_RUNTIME_DIR="/run/user/1000")


def psql(sql):
    out = subprocess.run(
        ["sudo", "docker", "exec", "-i", "stashbox-pg", "psql", "-U", "stashbox", "-d", DB, "-tAc", sql],
        capture_output=True, text=True,
    )
    if out.returncode != 0:
        sys.exit("psql failed: " + out.stderr.strip())
    return out.stdout.strip()


secret = next(l.split(":", 1)[1].strip() for l in open(CONFIG) if l.startswith("jwt_secret_key:"))


def mint(uid):
    b64 = lambda raw: base64.urlsafe_b64encode(raw).rstrip(b"=").decode()
    now = int(time.time())
    head = b64(json.dumps({"alg": "HS256", "typ": "JWT"}, separators=(",", ":")).encode())
    pay = b64(json.dumps({"uid": uid, "sub": "APIKey", "iat": now, "exp": now + 3600},
                         separators=(",", ":")).encode())
    sig = b64(hmac.new(secret.encode(), f"{head}.{pay}".encode(), hashlib.sha256).digest())
    return f"{head}.{pay}.{sig}"


def gql(key, query, variables=None):
    body = json.dumps({"query": query, "variables": variables or {}}).encode()
    req = urllib.request.Request(URL, data=body,
                                headers={"Content-Type": "application/json", "ApiKey": key})
    try:
        with urllib.request.urlopen(req) as r:
            return r.status, json.loads(r.read().decode())
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode()


def show(label, resp):
    status, body = resp
    print(f"{label}: http {status} {json.dumps(body) if not isinstance(body, str) else body}"[:400])


def check(label, resp, predicate, description):
    """Record a step's verdict instead of aborting, so one refusal does not strand a granted token."""
    status, body = resp
    ok = False
    if status == 200:
        try:
            ok = bool(predicate(body))
        except Exception as exc:  # a missing field IS the failure being asserted about
            print(f"   (the predicate could not read the response: {exc})")
    print(f"   VERDICT {label}: {'OK' if ok else 'FAILED'} -- {description}")
    if not ok:
        FAILURES.append(label)
    return body


def errors_of(resp):
    status, body = resp
    if isinstance(body, dict) and body.get("errors"):
        return " ".join(e.get("message", "") for e in body["errors"])
    return ""


def user_id(name):
    return psql("select id from users where name = '%s'" % name)


MEMBER_NAME = os.environ.get("WALK_MEMBER", "Lutrification")
OPERATOR_NAME = os.environ.get("WALK_OPERATOR", "StashBot")
MEMBER, OPERATOR = user_id(MEMBER_NAME), user_id(OPERATOR_NAME)
if not MEMBER or not OPERATOR:
    sys.exit(f"need accounts named {MEMBER_NAME} and {OPERATOR_NAME}")

# `generateInviteCodes` is @hasRole(READ) and `grantInvite` demands MANAGE_INVITES, and neither
# account holds them: Lutrification has EDIT/READ/VOTE, StashBot BOT, and the only ADMIN is `root`,
# which no API key can reach (measured: its stored key equals a minted one and the call still 401s).
# The operator is given both roles for the walk and put back in the finally block.
GRANTS = [(OPERATOR, "MANAGE_INVITES"), (OPERATOR, "READ")]
HAD = {g: psql(f"select count(*) from user_roles where user_id = '{g[0]}' and role = '{g[1]}'") != "0"
       for g in GRANTS}
saved = {u: psql(f"select coalesce(api_key,'') from users where id = '{u}'") for u in (MEMBER, OPERATOR)}
TOKENS = {u: mint(u) for u in (MEMBER, OPERATOR)}   # minted ONCE: iat makes each mint a new key
BEFORE_KEYS = psql(f"select count(*) from invite_keys where generated_by = '{MEMBER}'")
FAILURES = []
KEY = None
try:
    for u in (MEMBER, OPERATOR):
        psql(f"update users set api_key = '{TOKENS[u]}' where id = '{u}'")
    for (uid_g, role_g) in GRANTS:
        if not HAD[(uid_g, role_g)]:
            psql(f"insert into user_roles (user_id, role) values ('{uid_g}', '{role_g}')")
    subprocess.run(["systemctl", "--user", "restart", "stash-box"], check=True, env=ENV)
    for _ in range(30):
        try:
            urllib.request.urlopen(urllib.request.Request(
                URL, data=b'{"query":"{ version { version } }"}',
                headers={"Content-Type": "application/json"}), timeout=3)
            break
        except urllib.error.HTTPError:
            break
        except Exception:
            time.sleep(1)
    else:
        sys.exit("the service did not come back after the restart")
    print("   service restarted, so the auth cache is empty (it caches a key for 30s)")
    for name, u in (("member", MEMBER), ("operator", OPERATOR)):
        status, body = gql(TOKENS[u], '{ me { id } }')
        if status != 200 or (isinstance(body, dict) and body.get("errors")):
            sys.exit(f"the {name} could not authenticate after the restart (http {status}) -- stopping")
        print(f"   auth ok for the {name}")

    ALLOWANCE = 'invite_allowance { level cap active_keys tokens exempt can_generate }'

    print("=== A. the member reads their own allowance (derived, per request) ===")
    res = gql(TOKENS[MEMBER], '{ me { id %s } }' % ALLOWANCE)
    show("A", res)
    check("A", res, lambda b: b["data"]["me"]["invite_allowance"] == {
        "level": 0, "cap": 0, "active_keys": 0, "tokens": 0, "exempt": False, "can_generate": False},
        "a member with no contributions reads level 0, cap 0, no grant, and may not invite")
    rollups = psql(f"select count(*) from user_trust where user_id = '{MEMBER}'")
    print(f"   trust rollups for this member: {rollups} (level 0 is the no-rollup case, read as 0)")

    print("=== B. the member tries to mint anyway: the refusal NAMES THE LEVEL ===")
    res_b = gql(TOKENS[MEMBER], 'mutation { generateInviteCodes(input: {keys: 1}) }')
    show("B", res_b)
    msg = errors_of(res_b)
    check("B", res_b, lambda b: bool(b.get("errors")) and "grants no invite keys" in msg
          and "Public" in msg,
          "refused with the level's own refusal, naming the level that grants nothing")
    check("B2", res_b, lambda b: "are in use" not in msg,
          "and NOT with the slots-in-use refusal, whose remedy is a different action")
    left = psql(f"select count(*) from invite_keys where generated_by = '{MEMBER}'")
    check("B3", (200, {"n": left}), lambda b: b["n"] == BEFORE_KEYS,
          "nothing was created by the refused mutation")

    print("=== C. the operator grants ONE invite token ===")
    res_c = gql(TOKENS[OPERATOR], 'mutation ($u: ID!) { grantInvite(input: {user_id: $u, amount: 1}) }',
                {"u": MEMBER})
    show("C", res_c)
    check("C", res_c, lambda b: b["data"]["grantInvite"] == 1, "the grant lands: the member holds 1")

    res_c2 = gql(TOKENS[MEMBER], '{ me { %s } }' % ALLOWANCE)
    show("C2", res_c2)
    check("C2", res_c2, lambda b: b["data"]["me"]["invite_allowance"]["tokens"] == 1
          and b["data"]["me"]["invite_allowance"]["can_generate"] is True,
          "the member's own allowance shows the grant, and may now invite")

    print("=== D. the member mints: the grant pays, and the CAP does not move ===")
    res_d = gql(TOKENS[MEMBER], 'mutation { generateInviteCodes(input: {keys: 1}) }')
    show("D", res_d)
    KEY = res_d[1]["data"]["generateInviteCodes"][0] if isinstance(res_d[1], dict) and res_d[1].get("data") else None
    check("D", res_d, lambda b: len(b["data"]["generateInviteCodes"]) == 1, "one key, minted")
    if KEY is None:
        sys.exit("no key was minted -- stopping")

    res_d2 = gql(TOKENS[MEMBER], '{ me { %s } }' % ALLOWANCE)
    show("D2", res_d2)
    check("D2", res_d2, lambda b: b["data"]["me"]["invite_allowance"] == {
        "level": 0, "cap": 0, "active_keys": 1, "tokens": 0, "exempt": False, "can_generate": False},
        "the grant is spent, the key is counted, and cap is STILL 0: a grant buys a key, never a level")

    print("=== E. a second mint is refused again (no slots, no grant left) ===")
    res_e = gql(TOKENS[MEMBER], 'mutation { generateInviteCodes(input: {keys: 1}) }')
    show("E", res_e)
    check("E", res_e, lambda b: bool(b.get("errors")) and "grants no invite keys" in errors_of(res_e),
          "refused by the level again, not by a one-off quota")

    print("=== F. the member rescinds the UNUSED key: the grant comes back ===")
    res_f = gql(TOKENS[MEMBER], 'mutation ($c: ID!) { rescindInviteCode(code: $c) }', {"c": KEY})
    show("F", res_f)
    check("F", res_f, lambda b: b["data"]["rescindInviteCode"] is True, "the key is gone")
    res_f2 = gql(TOKENS[MEMBER], '{ me { %s } }' % ALLOWANCE)
    show("F2", res_f2)
    check("F2", res_f2, lambda b: b["data"]["me"]["invite_allowance"]["tokens"] == 1
          and b["data"]["me"]["invite_allowance"]["active_keys"] == 0,
          "the never-used key refunded its grant -- and this is the control for the guard that "
          "withholds the refund once a key has signed somebody up")

    print("=== G. a stranger may not read the allowance (it is the member's own standing) ===")
    res_g = gql(TOKENS[OPERATOR], 'query ($u: ID!) { findUser(id: $u) { id invite_allowance { cap } } }',
                {"u": MEMBER})
    show("G", res_g)
    check("G", res_g, lambda b: bool(b.get("errors")),
          "the operator (who has READ and MANAGE_INVITES, and may manage this member's tokens) is "
          "REFUSED the allowance itself: it is owner-only by directive")

    print("=== H. residue before the restore ===")
    print("   invite keys for the member:", psql(f"select count(*) from invite_keys where generated_by = '{MEMBER}'"),
          "(was", BEFORE_KEYS + ")")
    print("WALK COMPLETE" if not FAILURES else f"WALK FINISHED WITH FAILURES: {FAILURES}")
finally:
    if KEY and psql(f"select count(*) from invite_keys where id = '{KEY}'") != "0":
        psql(f"delete from invite_keys where id = '{KEY}'")
    # The token the walk granted is revoked, so the member is left with the balance they had.
    psql(f"update users set invite_tokens = 0 where id = '{MEMBER}'")
    for (uid_g, role_g) in GRANTS:
        if not HAD[(uid_g, role_g)]:
            psql(f"delete from user_roles where user_id = '{uid_g}' and role = '{role_g}'")
    for u, old in saved.items():
        psql(f"update users set api_key = '{old}' where id = '{u}'")
    restored = all(psql(f"select coalesce(api_key,'') from users where id = '{u}'") == old
                   for u, old in saved.items())
    print("RESTORE:", "every api_key is byte-identical to what it was" if restored
          else "MISMATCH -- a user's api_key was NOT restored")
    print("RESTORE: vote roles back to their original state:",
          all((psql(f"select count(*) from user_roles where user_id = '{g[0]}' and role = '{g[1]}'") == "1")
              == HAD[g] for g in GRANTS))
    print("RESTORE: member's invite tokens back to 0:",
          psql(f"select invite_tokens from users where id = '{MEMBER}'") == "0")
    print("RESTORE: member's invite keys back to the count before the walk:",
          psql(f"select count(*) from invite_keys where generated_by = '{MEMBER}'") == BEFORE_KEYS)
    print("residue: invite_keys in the whole table:",
          psql("select count(*) from invite_keys"))
