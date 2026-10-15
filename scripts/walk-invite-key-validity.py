#!/usr/bin/env python3
"""Walk the invite key's own facts on a LIVE instance: is a PRESENTED key validated?

Run it ON the host that serves the instance. It reads the jwt secret from the deployment config and
the users from the instance's Postgres, and it restarts the service to clear the auth cache.

WHY THIS EXISTS. `internal/service/user/validate.go` wraps the whole of `validateInviteKey` in
`if config.GetRequireInvite()` -- including "is this key expired" and "has this key been used up".
So on the DEFAULT configuration (the one this instance and the deployed one run) a key is OPTIONAL
and, because the two checks sit behind the same flag, it is also UNVALIDATED: a `uses = 1` key that
has already signed somebody up can sign up the next, and an expiry is not enforced anywhere. The
same family of defect as slice 11b's `invited_by`: a POLICY FLAG gating a FACT ABOUT A KEY. Whether
a key is mandatory is policy; whether the key you were handed is spent is a fact, and a spent key
must be refused in both configurations.

THE SEQUENCE, and it is the sequence that makes it a verdict rather than a reading:

  A. a member mints ONE key with `uses: 1`;
  B. the FIRST signup with it is accepted (the key is good, and must work);
  C. the SECOND signup with the SAME key must be REFUSED -- it is spent. Today it is accepted.
  D. a key whose `expire_time` is in the past must be REFUSED at signup. Today it is accepted;
  E. the minted-without-`uses` default: the schema documents "if not set ... one use" while the
     code stores NULL, which this script reports as the number of signups an unqualified key takes
     (NULL means unlimited in every query that reads it).

WHAT IT CHANGES AND PUTS BACK: two members' api_keys, the roles the operator needs, the keys it
mints, one `expire_time` it moves into the past, and every account it creates. Nothing is printed
that is a credential.

See docs/plan/feature-content-aggregator.md, phase 11 slice 11b.
"""

import json
import os
import subprocess
import sys
import time
import urllib.error
import urllib.request

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from importlib.machinery import SourceFileLoader

# The walk borrows the 11b script's plumbing (mint/gql/psql) rather than restating it: two copies of
# a JWT minter is two places for the auth cache's 30-second rule to be got wrong.
_11b = SourceFileLoader("walk11b", os.path.join(os.path.dirname(os.path.abspath(__file__)),
                                                "walk-11b-live.py"))

URL = "http://127.0.0.1:9998/graphql"
DB = "stash-box"
CONFIG = "/home/alvaro/stash-box-deploy/config/stash-box-config.yml"
ENV = dict(os.environ, XDG_USER_DIR="", XDG_RUNTIME_DIR="/run/user/1000")
STAMP = str(int(time.time()))[-6:]


def psql(sql):
    out = subprocess.run(
        ["sudo", "docker", "exec", "-i", "stashbox-pg", "psql", "-U", "stashbox", "-d", DB, "-tAc", sql],
        capture_output=True, text=True,
    )
    if out.returncode != 0:
        sys.exit("psql failed: " + out.stderr.strip())
    return out.stdout.strip()


config_text = open(CONFIG).read()
secret = next(l.split(":", 1)[1].strip() for l in config_text.splitlines()
              if l.startswith("jwt_secret_key:"))
FILE_SAYS = next((l.split(":", 1)[1].strip() for l in config_text.splitlines()
                    if l.startswith("require_invite:")), "absent from the file")

import base64
import hashlib
import hmac


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
                                headers={"Content-Type": "application/json", "ApiKey": key or ""})
    try:
        with urllib.request.urlopen(req) as r:
            return r.status, json.loads(r.read().decode())
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode()


FAILURES = []


def effective_require_invite():
    """The RUNNING instance's own answer.

    The config file omitting the key does NOT mean the feature is off: `config.go` defaults
    `RequireInvite: true`, so a file that says nothing means ON. Reading the file was the mistake this
    walk exists to not repeat -- it asserted a premise about a configuration nobody had asked the
    program about.
    """
    status, body = gql(None, "{ getConfig { require_invite } }")
    if status == 200 and isinstance(body, dict) and body.get("data"):
        return bool(body["data"]["getConfig"]["require_invite"]), json.dumps(body["data"])
    return None, f"http {status} {body}"


def check(label, ok, description, detail=""):
    print(f"   VERDICT {label}: {'OK' if ok else 'FAILED'} -- {description}" + (f" [{detail}]" if detail else ""))
    if not ok:
        FAILURES.append(label)


def err_of(resp):
    status, body = resp
    if isinstance(body, dict) and body.get("errors"):
        return " ".join(e.get("message", "") for e in body["errors"])
    return ""


def signup(email, key):
    """The whole public path: newUser, then activateNewUser. Returns (ok, message)."""
    res = gql(None, "mutation ($e: String!, $k: ID) { newUser(input: {email: $e, invite_key: $k}) }",
              {"e": email, "k": key})
    if err_of(res):
        return False, err_of(res)
    status, body = res
    if status != 200 or not isinstance(body, dict) or not body.get("data"):
        return False, f"http {status} {body}"
    act = body["data"]["newUser"]
    res2 = gql(None, """mutation ($n: String!, $a: ID!, $p: String!) {
        activateNewUser(input: {name: $n, activation_key: $a, password: $p}) { id name }
    }""", {"n": email.split("@")[0], "a": act, "p": "walk-key-validity-9f3a"})
    if err_of(res2):
        return False, err_of(res2)
    status, body = res2
    if status != 200 or not isinstance(body, dict) or not body.get("data"):
        return False, f"http {status} {body}"
    return True, body["data"]["activateNewUser"]["id"]


def user_id(name):
    return psql("select id from users where name = '%s'" % name)


MEMBER_NAME = os.environ.get("WALK_MEMBER", "Lutrification")
OPERATOR_NAME = os.environ.get("WALK_OPERATOR", "StashBot")
MEMBER, OPERATOR = user_id(MEMBER_NAME), user_id(OPERATOR_NAME)
if not MEMBER or not OPERATOR:
    sys.exit(f"need accounts named {MEMBER_NAME} and {OPERATOR_NAME}")

GRANTS = [(OPERATOR, "MANAGE_INVITES"), (OPERATOR, "READ")]
HAD = {g: psql(f"select count(*) from user_roles where user_id = '{g[0]}' and role = '{g[1]}'") != "0"
       for g in GRANTS}
saved = {u: psql(f"select coalesce(api_key,'') from users where id = '{u}'") for u in (MEMBER, OPERATOR)}
TOKENS = {u: mint(u) for u in (MEMBER, OPERATOR)}
BEFORE_KEYS = psql(f"select count(*) from invite_keys where generated_by = '{MEMBER}'")
BEFORE_USERS = psql("select count(*) from users")
CREATED_NAMES = [f"walk-kv-{STAMP}-a", f"walk-kv-{STAMP}-b", f"walk-kv-{STAMP}-c"]
KEYS = []
try:
    effective, raw = effective_require_invite()
    print(f"=== 0. the premise: the config FILE says require_invite is '{FILE_SAYS}'; "
          f"the RUNNING instance says '{effective}' ===")
    print(f"   (the file's silence means ON: config.go defaults it to true. {raw[:120]})")
    check("0", effective is not None,
          "the instance's effective configuration was read from the instance, not from a document")
    if effective is not False:
        print("   NOTE: this instance REQUIRES invites, so it is the configuration where the two "
              "drift defects (a spent key accepted, an expiry ignored) CANNOT show -- those hold "
              "when a key is optional. The refusals below are still the right verdicts here; the "
              "flag is turned off for them in the wire tests, which is where the defect's evidence "
              "lives. This walk is about the key's own facts being enforced at all.")

    for u in (MEMBER, OPERATOR):
        psql(f"update users set api_key = '{TOKENS[u]}' where id = '{u}'")
    for (uid_g, role_g) in GRANTS:
        if not HAD[(uid_g, role_g)]:
            psql(f"insert into user_roles (user_id, role) values ('{uid_g}', '{role_g}')")
    for n in CREATED_NAMES:
        psql(f"delete from users where name = '{n}'")
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
    for name, u in (("member", MEMBER), ("operator", OPERATOR)):
        status, body = gql(TOKENS[u], "{ me { id } }")
        if status != 200 or (isinstance(body, dict) and body.get("errors")):
            sys.exit(f"the {name} could not authenticate after the restart (http {status}) -- stopping")
    print("   auth ok for the member and the operator")

    print("=== A. a key with exactly ONE use, minted by the member ===")
    res = gql(TOKENS[OPERATOR], "mutation ($u: ID!) { grantInvite(input: {user_id: $u, amount: 3}) }",
              {"u": MEMBER})
    check("A0", isinstance(res[1], dict) and res[1].get("data", {}).get("grantInvite") == 3,
          "the operator's grants land (3 tokens, one per key the walk mints)")
    res = gql(TOKENS[MEMBER], "mutation { generateInviteCodes(input: {keys: 1, uses: 1}) }")
    one_use = (res[1]["data"]["generateInviteCodes"][0]
               if isinstance(res[1], dict) and res[1].get("data") else None)
    check("A", bool(one_use), "a key with uses=1 is minted")
    if not one_use:
        sys.exit("no key was minted -- stopping")
    KEYS.append(one_use)
    stored = psql(f"select coalesce(uses::text,'NULL') from invite_keys where id = '{one_use}'")
    check("A2", stored == "1", "and the stored use count is 1, not NULL", f"uses={stored}")

    print("=== B. the FIRST signup with it must succeed ===")
    ok, detail = signup(f"{CREATED_NAMES[0]}@example.invalid", one_use)
    check("B", ok, "the key works once, which is what it is for", detail[:120])

    print("=== C. the SECOND signup with the SAME key must be REFUSED ===")
    ok, detail = signup(f"{CREATED_NAMES[1]}@example.invalid", one_use)
    check("C", not ok, "a SPENT key is refused at signup -- this is the defect this walk exists for",
          detail[:160])
    check("C2", ok is False and ("used" in detail or "invite" in detail or "key" in detail),
          "and the refusal is about the KEY, not an unrelated failure", detail[:160])

    print("=== D. an EXPIRED key must be REFUSED at signup ===")
    res = gql(TOKENS[MEMBER], "mutation { generateInviteCodes(input: {keys: 1, uses: 5}) }")
    expiring = (res[1]["data"]["generateInviteCodes"][0]
                if isinstance(res[1], dict) and res[1].get("data") else None)
    if not expiring:
        sys.exit("no second key was minted -- stopping")
    KEYS.append(expiring)
    # A TTL is counted in seconds from now, so the past is set directly: the state under test is the
    # state, however it got there.
    psql(f"update invite_keys set expire_time = now() - interval '1 day' where id = '{expiring}'")
    expired = psql(f"select (expire_time < now())::text from invite_keys where id = '{expiring}'")
    check("D0", expired == "true", "the key's expire_time is in the past", f"expired={expired}")
    ok, detail = signup(f"{CREATED_NAMES[2]}@example.invalid", expiring)
    check("D", not ok, "an EXPIRED key is refused at signup -- the other half of the same defect",
          detail[:160])

    print("=== E. what a minted-without-`uses` key actually is ===")
    res = gql(TOKENS[MEMBER], "mutation { generateInviteCodes(input: {keys: 1}) }")
    unqualified = (res[1]["data"]["generateInviteCodes"][0]
                   if isinstance(res[1], dict) and res[1].get("data") else None)
    if unqualified:
        KEYS.append(unqualified)
        stored = psql(f"select coalesce(uses::text,'NULL') from invite_keys where id = '{unqualified}'")
        print(f"   the schema documents: 'If not set, the invite key will have one use'")
        print(f"   the database stores:  uses={stored}  (NULL means UNLIMITED in every query that reads it)")
        check("E", stored != "NULL",
              "a key minted without an explicit use count stores the ONE use the schema documents")
        ok, detail = signup(f"walk-kv-{STAMP}-d@example.invalid", unqualified)
        ok2, detail2 = signup(f"walk-kv-{STAMP}-e@example.invalid", unqualified)
        print(f"   and it took a first signup: {ok}, then a second: {ok2}")
        CREATED_NAMES += [f"walk-kv-{STAMP}-d", f"walk-kv-{STAMP}-e"]
    else:
        check("E", False, "a key was minted without an explicit use count")

    print("=== F. residue before the restore ===")
    print(f"   accounts created by this walk: {psql('select count(*) from users')} total (was {BEFORE_USERS})")
    print("WALK COMPLETE" if not FAILURES else f"WALK FINISHED WITH FAILURES: {FAILURES}")
finally:
    for n in CREATED_NAMES:
        uid = user_id(n)
        if uid:
            psql(f"delete from user_roles where user_id = '{uid}'")
            psql(f"delete from user_tokens where data->>'user_id' = '{uid}'")
            psql(f"delete from user_trust where user_id = '{uid}'")
            psql(f"delete from users where id = '{uid}'")
    for k in KEYS:
        psql(f"delete from invite_keys where id = '{k}'")
    psql(f"update users set invite_tokens = 0 where id = '{MEMBER}'")
    for (uid_g, role_g) in GRANTS:
        if not HAD[(uid_g, role_g)]:
            psql(f"delete from user_roles where user_id = '{uid_g}' and role = '{role_g}'")
    for u, old in saved.items():
        psql(f"update users set api_key = '{old}' where id = '{u}'")
    print("RESTORE: api_keys byte-identical:",
          all(psql(f"select coalesce(api_key,'') from users where id = '{u}'") == old
              for u, old in saved.items()))
    print("RESTORE: operator roles as they were:",
          all((psql(f"select count(*) from user_roles where user_id = '{g[0]}' and role = '{g[1]}'") == "1")
              == HAD[g] for g in GRANTS))
    print("RESTORE: member's keys back to the count before the walk:",
          psql(f"select count(*) from invite_keys where generated_by = '{MEMBER}'") == BEFORE_KEYS)
    print("RESTORE: every account the walk created is gone:",
          all(psql(f"select count(*) from users where name = '{n}'") == "0" for n in CREATED_NAMES))
    print("residue: users in the table:", psql("select count(*) from users"), "(was", BEFORE_USERS + ")")
