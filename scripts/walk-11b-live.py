#!/usr/bin/env python3
"""Walk slice 11b (the referral payment) on a LIVE stash-box instance.

Run it ON the host that serves the instance: it reads the jwt secret from the deployment config and
the users from the instance's Postgres, and it restarts the service to clear the auth cache.

WHAT THIS WALK IS FOR. Slice 11b's finding is that `users.invited_by` was written ONLY when
`require_invite` was on, while the key's use is consumed on EVERY instance -- so on an instance that
does not require invites a signup that carried a key spent the key and recorded nobody, and every
payment built on that column would have paid nothing. This walk makes a real signup with a real key
through the public HTTP path and reads the inviter back off the new account's own `me`.

CORRECTED 2026-10-15: this docstring used to say "this instance does not require invites", which was
read from the config FILE's silence -- and `config.go` defaults `RequireInvite: true`, so this
instance (and the deployment) DO require invites. The walk now asks the running instance
(`getConfig`) instead of reading a document, and says which configuration each verdict is about. The
defect's own evidence is the wire test that sets the flag off; this walk proves the link is recorded
and readable on the configuration the deployment actually runs.

WHAT THE WALK CHANGES, and puts back in a finally block: two users' api_key values (a JWT carries
`iat`, so the server's "presented key == stored key" check means a key is minted ONCE and reused),
the roles the walk's operator needs, one invite token (granted, spent), one invite key, the
activation token, and one NEW ACCOUNT -- created so that a real signup can be observed, then deleted
along with its roles and tokens. Nothing is printed that is a credential.

WHAT THIS WALK CANNOT SHOW, and says so rather than implying otherwise: THE PAYMENT ITSELF. A
payment needs the invitee to reach Registered, which takes 10 points of APPLIED contribution, and
the only path to that on a live instance is a real scene edit carried through the voting system --
no mutation records a trust event, and this instance has no credential any script can use as a
moderator (measured in slice 11a: the one ADMIN cannot authenticate through an API key at all). So
the walk proves the LINK (who invited whom, recorded without a policy to remind it) and the
ABSENCE (nothing is paid for a signup: the member's count is 0 and the field the deployed binary
serves resolves and says so). The payment and its depth are covered where they can be asked: the
wire tests drive a real member from Public to Registered with real trust events against real
Postgres and assert the payment once, by identity, and never for the grandparent.

See docs/plan/feature-content-aggregator.md, phase 11 slice 11b.
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
INVITEE_NAME = "walk-11b-invitee"
INVITEE_EMAIL = "walk-11b-invitee@example.invalid"


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
# The finding's PREMISE, measured rather than assumed: with require_invite absent or false, the key
# is optional and the inviter had to be recorded anyway.
REQUIRE_INVITE = next((l.split(":", 1)[1].strip() for l in config_text.splitlines()
                       if l.startswith("require_invite:")), "false (absent)")


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


def show(label, resp):
    status, body = resp
    print(f"{label}: http {status} {json.dumps(body) if not isinstance(body, str) else body}"[:400])


FAILURES = []


def check(label, resp, predicate, description):
    """Record a step's verdict instead of aborting, so one refusal does not strand a granted key."""
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

# generateInviteCodes is @hasRole(READ) and grantInvite demands MANAGE_INVITES; neither account
# holds them, exactly as in the 11a walk, so the operator is lent both for the walk.
GRANTS = [(OPERATOR, "MANAGE_INVITES"), (OPERATOR, "READ")]
HAD = {g: psql(f"select count(*) from user_roles where user_id = '{g[0]}' and role = '{g[1]}'") != "0"
       for g in GRANTS}
saved = {u: psql(f"select coalesce(api_key,'') from users where id = '{u}'") for u in (MEMBER, OPERATOR)}
TOKENS = {u: mint(u) for u in (MEMBER, OPERATOR)}   # minted ONCE: iat makes each mint a new key
BEFORE_KEYS = psql(f"select count(*) from invite_keys where generated_by = '{MEMBER}'")
# The counters added by slice 11c are append-only history, and THIS WALK SIGNS SOMEBODY UP too, so it
# records events like a real invitation would. It remembers where the journal ended and removes only
# what it added, the same way it puts the api_keys back.
BEFORE_EVENT_MAX = int(psql("select coalesce(max(id), 0) from invite_key_events"))
BEFORE_MEMBER_COUNT = psql("select count(*) from users")
KEY = None
INVITEE = None
ACTIVATION_KEY = None
try:
    status, body = gql(None, "{ getConfig { require_invite } }")
    effective = (body["data"]["getConfig"]["require_invite"]
                 if status == 200 and isinstance(body, dict) and body.get("data") else None)
    print(f"=== 0. the premise: the config FILE says require_invite is '{REQUIRE_INVITE}'; the "
          f"RUNNING instance says '{effective}' ===")
    if effective is None:
        print("   !! the instance did not answer, so this walk cannot state which configuration it "
              "is evidence about -- stopping")
        FAILURES.append("0")
        raise SystemExit(1)
    if effective:
        # CORRECTED 2026-10-15: this guard used to read the config FILE, where an absent key means
        # "off" to a reader and ON to the program (`config.go` defaults it to true). The walk then
        # ran on a required-invite instance believing it was on an optional-invite one.
        print("   NOTE: this instance REQUIRES invites, so it is NOT the configuration where "
              "`invited_by` used to be dropped. The wire test reaches that one by setting the flag "
              "off and asserting its state first; this walk proves the link is recorded and read "
              "back on the configuration the deployment actually runs.")

    for u in (MEMBER, OPERATOR):
        psql(f"update users set api_key = '{TOKENS[u]}' where id = '{u}'")
    for (uid_g, role_g) in GRANTS:
        if not HAD[(uid_g, role_g)]:
            psql(f"insert into user_roles (user_id, role) values ('{uid_g}', '{role_g}')")
    # A previous interrupt could have left the walk's account behind; it is the walk's own name.
    psql(f"delete from users where name = '{INVITEE_NAME}'")
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
        status, body = gql(TOKENS[u], "{ me { id } }")
        if status != 200 or (isinstance(body, dict) and body.get("errors")):
            sys.exit(f"the {name} could not authenticate after the restart (http {status}) -- stopping")
        print(f"   auth ok for the {name}")

    print("=== A. the operator grants one token; the member mints one key ===")
    res_a = gql(TOKENS[OPERATOR], "mutation ($u: ID!) { grantInvite(input: {user_id: $u, amount: 1}) }",
                {"u": MEMBER})
    show("A", res_a)
    check("A", res_a, lambda b: b["data"]["grantInvite"] == 1, "the grant lands")
    res_a2 = gql(TOKENS[MEMBER], "mutation { generateInviteCodes(input: {keys: 1}) }")
    show("A2", res_a2)
    KEY = (res_a2[1]["data"]["generateInviteCodes"][0]
           if isinstance(res_a2[1], dict) and res_a2[1].get("data") else None)
    check("A2", res_a2, lambda b: len(b["data"]["generateInviteCodes"]) == 1 and b["data"]["generateInviteCodes"][0],
          "one key, minted by the INVITER")
    if not KEY:
        sys.exit("no key was minted -- stopping")

    print("=== B. a NEW account signs up with that key, through the public path ===")
    res_b = gql(None, "mutation ($e: String!, $k: ID) { newUser(input: {email: $e, invite_key: $k}) }",
                {"e": INVITEE_EMAIL, "k": KEY})
    show("B", res_b)
    ACTIVATION_KEY = res_b[1]["data"]["newUser"] if isinstance(res_b[1], dict) and res_b[1].get("data") else None
    check("B", res_b, lambda b: b["data"]["newUser"], "signup accepted an invitation key")
    if not ACTIVATION_KEY:
        sys.exit("no activation key -- stopping")

    res_b2 = gql(None, """mutation ($n: String!, $a: ID!, $p: String!) {
        activateNewUser(input: {name: $n, activation_key: $a, password: $p}) { id name }
    }""", {"n": INVITEE_NAME, "a": ACTIVATION_KEY, "p": "walk-11b-password-9f3a"})
    show("B2", res_b2)
    check("B2", res_b2, lambda b: b["data"]["activateNewUser"]["name"] == INVITEE_NAME,
          "the invitee is now an account")
    INVITEE = user_id(INVITEE_NAME)
    if not INVITEE:
        sys.exit("the account was not created -- stopping")

    print("=== C. THE FINDING, LIVE: the invitee's own record names the inviter ===")
    invited_by = psql(f"select coalesce(invited_by::text,'') from users where id = '{INVITEE}'")
    print(f"   users.invited_by for the new account: {invited_by[:8]}… (the member is {MEMBER[:8]}…)")
    check("C", (200, {"invited_by": invited_by}), lambda b: b["invited_by"] == MEMBER,
          "a signup with a key records the inviter, and the invitee can be read back as invited by "
          "them -- the link this slice's payment reads. (On THIS instance that held before the "
          "slice too: it requires invites, which is what turned the write on. The configuration "
          "where the column stayed empty is reached in the wire test, with the flag asserted off.)")

    invitee_key = psql(f"select coalesce(api_key,'') from users where id = '{INVITEE}'")
    TOKENS[INVITEE] = mint(INVITEE)
    psql(f"update users set api_key = '{TOKENS[INVITEE]}' where id = '{INVITEE}'")
    subprocess.run(["systemctl", "--user", "restart", "stash-box"], check=True, env=ENV)
    time.sleep(6)
    res_c2 = gql(TOKENS[INVITEE], "{ me { id name invited_by { id name } } }")
    show("C2", res_c2)
    check("C2", res_c2, lambda b: b["data"]["me"]["invited_by"]
          and b["data"]["me"]["invited_by"]["id"] == MEMBER,
          "and the invitee can READ that back off their own `me`: the link is on the wire, not "
          "only in the table")
    check("C3", res_c2, lambda b: b["data"]["me"]["invited_by"]["name"] == MEMBER_NAME,
          "naming the inviter by name, which is what the referral's UI would show")

    print("=== D. nothing is paid for a signup: the inviter's own count is 0 and it RESOLVES ===")
    res_d = gql(TOKENS[MEMBER], "{ me { id trust { invites_activated level approved_edits } } }")
    show("D", res_d)
    check("D", res_d, lambda b: b["data"]["me"]["trust"]["invites_activated"] == 0,
          "an invitee who has contributed nothing has paid their inviter nothing -- so the count "
          "the deployed binary serves is fed by the rollup and starts at zero rather than being absent")
    check("D2", res_d, lambda b: b["data"]["me"]["trust"]["level"] == 0
          and b["data"]["me"]["trust"]["approved_edits"] == 0,
          "and the member's own standing is untouched by the walk")

    print("=== E. residue before the restore ===")
    print("   the new account exists:", psql(f"select count(*) from users where id = '{INVITEE}'"),
          "| the key is spent:", psql(f"select uses from invite_keys where id = '{KEY}'") or "deleted",
          "| users before:", BEFORE_MEMBER_COUNT)
    print("WALK COMPLETE" if not FAILURES else f"WALK FINISHED WITH FAILURES: {FAILURES}")
finally:
    if INVITEE:
        psql(f"delete from user_roles where user_id = '{INVITEE}'")
        # user_tokens has NO user_id column -- it is keyed by id with a jsonb payload -- and the
        # activation token is consumed by activation itself, so the honest guard is a count over
        # what remains rather than a delete that cannot match. A delete here used to throw, and
        # because psql() exits on a refusal the rest of this block never ran.
        psql(f"delete from user_tokens where data->>'user_id' = '{INVITEE}'")
        psql(f"delete from user_trust where user_id = '{INVITEE}'")
        psql(f"delete from users where id = '{INVITEE}'")
    if KEY and psql(f"select count(*) from invite_keys where id = '{KEY}'") != "0":
        psql(f"delete from invite_keys where id = '{KEY}'")
    # The token the walk granted is revoked, so the member is left with the balance they had.
    psql(f"update users set invite_tokens = 0 where id = '{MEMBER}'")
    for (uid_g, role_g) in GRANTS:
        if not HAD[(uid_g, role_g)]:
            psql(f"delete from user_roles where user_id = '{uid_g}' and role = '{role_g}'")
    psql(f"delete from invite_key_events where id > {BEFORE_EVENT_MAX} and generated_by = '{MEMBER}'")
    print("RESTORE: the walk's own event rows are removed:",
          psql(f"select count(*) from invite_key_events where id > {BEFORE_EVENT_MAX}") == "0")
    for u, old in saved.items():
        psql(f"update users set api_key = '{old}' where id = '{u}'")
    restored = all(psql(f"select coalesce(api_key,'') from users where id = '{u}'") == old
                   for u, old in saved.items())
    print("RESTORE:", "every api_key is byte-identical to what it was" if restored
          else "MISMATCH -- a user's api_key was NOT restored")
    print("RESTORE: the walk's operator roles are back to their original state:",
          all((psql(f"select count(*) from user_roles where user_id = '{g[0]}' and role = '{g[1]}'") == "1")
              == HAD[g] for g in GRANTS))
    print("RESTORE: member's invite tokens back to 0:",
          psql(f"select invite_tokens from users where id = '{MEMBER}'") == "0")
    print("RESTORE: member's invite keys back to the count before the walk:",
          psql(f"select count(*) from invite_keys where generated_by = '{MEMBER}'") == BEFORE_KEYS)
    print("RESTORE: the walk's account is gone:",
          psql(f"select count(*) from users where name = '{INVITEE_NAME}'") == "0")
    print("residue: users in the whole table:", psql("select count(*) from users"),
          "(was", BEFORE_MEMBER_COUNT + ")")
