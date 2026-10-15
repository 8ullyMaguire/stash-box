#!/usr/bin/env python3
"""Walk slice 8e (collaborative lists) on a LIVE stash-box instance.

Run it ON the host that serves the instance: it reads the jwt secret from the deployment config
and the users from the instance's Postgres, and it needs to restart the service. Nothing is
printed that is a credential.

What it changes, and puts back in a finally block: three users' api_key values (a JWT carries
`iat`, so the server's "presented key == stored key" check means a key must be minted ONCE and
reused -- re-minting per call 401s against the key you just wrote), plus a MODIFY or READ role
holding that the walk's actors need and do not have. It creates ONE list and deletes it at the
end; the residue check prints what is left.

Environment: WALK_OWNER, WALK_COLAB, WALK_STRANGER name the three accounts (defaults are this
instance's). The walk refuses to run if the accounts are missing.

See docs/plan/feature-content-aggregator.md, slice 8e, "the walk".
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


def psql(sql):
    out = subprocess.run(
        ["sudo", "docker", "exec", "stashbox-pg", "psql", "-U", "stashbox", "-d", DB, "-tAc", sql],
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


def data_of(resp, label="step"):
    status, body = resp
    if status != 200:
        sys.exit(f"{label}: HTTP {status} -- stopping")
    if body.get("errors"):
        sys.exit(f"{label}: GraphQL error {json.dumps(body)[:300]}")
    return body["data"]


def check(label, resp, predicate, description):
    """Record a step's verdict instead of aborting: one failed step must not strand a list.

    The predicate gets the WHOLE response body, so an error body fails the step instead of
    raising KeyError -- a walk that crashes tells you less than one that reports.
    """
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


def user_id(name):
    return psql("select id from users where name = '%s'" % name)


# MEASURED on this instance: `root` cannot authenticate with a freshly minted key at all (its
# stored key equals the minted one and the call still gets a bare 401), while Lutrification,
# StashBot and [deleted user] all return 200 for `me`. The walk uses the three that work.
OWNER, COLAB = user_id(os.environ.get("WALK_OWNER", "Lutrification")), user_id(os.environ.get("WALK_COLAB", "StashBot"))
if not OWNER or not COLAB:
    sys.exit("need users named Lutrification and StashBot")
# The walks this slice adds are gated @hasRole(role: MODIFY) and reads @hasRole(role: READ), and
# no account that can authenticate holds what the walk needs: Lutrification has EDIT/READ/VOTE,
# StashBot BOT, [deleted user] nothing, and the only ADMIN is `root`, which no API key can reach.
#
# GIVING EVERY ACTOR THE SAME ROLE IS THE POINT, not a convenience. The first run granted MODIFY
# to the owner only, so the collaborator's listAddItem was refused by the ROLE gate -- which
# proves nothing about this slice's rule that a collaborator may write. With the collaborator
# holding MODIFY too, her add succeeds because of the collaborator fact alone, and the publish
# refusal is the owner rule rather than a missing role. The stranger gets READ, so that her
# refusal is the canView rule rather than the gateway.
GRANTS = [(OWNER, "MODIFY"), (COLAB, "MODIFY")]
# The stranger must be a real, authenticatable account that is NOT a member of the list.
STRANGERS = [u for u in (user_id(os.environ.get("WALK_STRANGER", "[deleted user]")),) if u and u not in (OWNER, COLAB)]
GRANTS += [(u, "READ") for u in STRANGERS]
_ALL = [OWNER, COLAB] + STRANGERS
HAD = {g: psql(f"select count(*) from user_roles where user_id = '{g[0]}' and role = '{g[1]}'") != "0"
       for g in GRANTS}
saved = {u: psql(f"select coalesce(api_key,'') from users where id = '{u}'") for u in _ALL}
TOKENS = {u: mint(u) for u in _ALL}          # minted ONCE -- see trap 1 in the docstring
FAILURES = []
LIST = None
STRANGER = None
try:
    for u in _ALL:
        psql(f"update users set api_key = '{TOKENS[u]}' where id = '{u}'")
    for (uid_g, role_g) in GRANTS:
        if not HAD[(uid_g, role_g)]:
            psql(f"insert into user_roles (user_id, role) values ('{uid_g}', '{role_g}')")
    subprocess.run(["systemctl", "--user", "restart", "stash-box"], check=True)
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
    print("   service restarted, so the auth cache is empty")
    for name, u in (("owner", OWNER), ("collaborator", COLAB)):
        status, body = gql(TOKENS[u], '{ me { id } }')
        if status != 200 or (isinstance(body, dict) and body.get("errors")):
            sys.exit(f"the {name} could not authenticate after the restart (http {status}) -- stopping")
        print(f"   auth ok for the {name}")
    for candidate in STRANGERS:
        status, body = gql(TOKENS[candidate], '{ me { id } }')
        if status == 200 and not (isinstance(body, dict) and body.get("errors")):
            STRANGER = candidate
            print("   auth ok for a non-member account (the stranger)")
            break

    print("=== A. owner creates a list ===")
    res = gql(TOKENS[OWNER], 'mutation { listCreate(input: {name: "8e live walk"}) { id name canEdit publishedAt } }')
    show("A listCreate", res)
    check("A", res, lambda b: b["data"]["listCreate"]["canEdit"] is True and b["data"]["listCreate"]["publishedAt"] is None,
          "the owner may edit her new list, and a new list is NOT published")
    LIST = res[1]["data"]["listCreate"]["id"] if isinstance(res[1], dict) and res[1].get("data") else None
    if LIST is None:
        sys.exit("the list was never created -- stopping")

    print("=== B. owner adds the collaborator (listAddCollaborator) ===")
    res_b = gql(TOKENS[OWNER], 'mutation ($l: ID!, $u: ID!) { listAddCollaborator(input: {listId: $l, userId: $u}) }', {"l": LIST, "u": COLAB})
    show("B", res_b)
    check("B", res_b, lambda b: b["data"]["listAddCollaborator"] is True, "the owner may add a collaborator")

    print("=== C. owner reads the write set back ===")
    res_c = gql(TOKENS[OWNER], 'query ($l: ID!) { list(id: $l) { id canEdit collaborators { id } } }', {"l": LIST})
    show("C", res_c)
    check("C", res_c, lambda b: [c["id"] for c in b["data"]["list"]["collaborators"]] == [COLAB],
          "the set she added reads back as exactly that member")

    perf = psql("select id from performers limit 1")
    print("=== D. the COLLABORATOR adds an item (her opened act) ===")
    res_d = gql(TOKENS[COLAB], 'mutation ($l: ID!, $p: ID!) { listAddItem(input: {listId: $l, entityType: PERFORMER, entityId: $p}) { id entityType } }', {"l": LIST, "p": perf})
    show("D", res_d)
    check("D", res_d, lambda b: b["data"]["listAddItem"]["entityType"] == "PERFORMER",
          "a collaborator may add an entry to a list she does not own")

    print("=== E. the collaborator reads the DRAFT she does not own (canView's new arm) ===")
    res_e = gql(TOKENS[COLAB], 'query ($l: ID!) { list(id: $l) { id name canEdit items { id } } }', {"l": LIST})
    show("E", res_e)
    check("E", res_e, lambda b: b["data"]["list"]["canEdit"] is True and b["data"]["list"]["id"] == LIST,
          "a collaborator READS a draft she does not own, and canEdit is true for her")

    print("=== F. the same user tries an OWNER-ONLY act (publish) ===")
    res_f = gql(TOKENS[COLAB], 'mutation ($l: ID!) { listPublish(id: $l) { id publishedAt } }', {"l": LIST})
    show("F", res_f)
    check("F", res_f, lambda b: bool(b.get("errors")), "a collaborator may NOT publish: the act is the owner's")
    show("F2 (listPublish with no id, to show the refusal is the rule and not a bad query)",
         gql(TOKENS[COLAB], 'mutation { listPublish(id: "00000000-0000-0000-0000-000000000000") { id } }'))

    print("=== G. the collaborator reads the write set (members may) ===")
    res_g = gql(TOKENS[COLAB], 'query ($l: ID!) { list(id: $l) { collaborators { id } } }', {"l": LIST})
    show("G", res_g)
    check("G", res_g, lambda b: [c["id"] for c in b["data"]["list"]["collaborators"]] == [COLAB],
          "a member may read the set, and it contains her")

    print("=== H. a STRANGER asks for the same draft ===")
    if STRANGER is None:
        print("H: NOT WALKED -- no second non-member account could authenticate on this instance")
        FAILURES.append("H")
    else:
        res_h1 = gql(TOKENS[STRANGER], 'query ($l: ID!) { list(id: $l) { id name } }', {"l": LIST})
        show("H1", res_h1)
        check("H1", res_h1, lambda b: b["data"]["list"] is None,
              "a stranger asking for the draft gets NULL -- the same answer as a missing id")
        res_h2 = gql(TOKENS[STRANGER], 'query ($u: ID!) { lists(userId: $u, perPage: 5) { id name } }', {"u": OWNER})
        show("H2 lists(userId=owner)", res_h2)
        check("H2", res_h2, lambda b: LIST not in [l["id"] for l in b["data"]["lists"]],
              "a stranger listing the owner's lists does not see the draft")

    print("=== I. the audit trail (recorded NOTHING before this slice) ===")
    res_i = gql(TOKENS[OWNER], 'query ($l: ID!) { list(id: $l) { auditTrail { action actor { id } } } }', {"l": LIST})
    show("I", res_i)
    trail = res_i[1]["data"]["list"]["auditTrail"] if isinstance(res_i[1], dict) and res_i[1].get("data") else []
    actors = {(row["actor"] or {}).get("id") for row in trail}
    actions = [row["action"] for row in trail]
    print(f"   actions: {actions}")
    check("I", res_i, lambda b: OWNER in actors and COLAB in actors
          and "ADD_ITEM" in actions and "COLLABORATOR_ADD" in actions,
          "the trail names BOTH actors and spells the actions as the enum does")

    print("=== J. cleanup: the owner deletes the list ===")
    show("J listDelete", gql(TOKENS[OWNER], 'mutation ($l: ID!) { listDelete(id: $l) }', {"l": LIST}))
    left = psql(f"select (select count(*) from lists where id = '{LIST}') || '|' "
                f"|| (select count(*) from list_audit where list_id = '{LIST}') || '|' "
                f"|| (select count(*) from list_collaborators where list_id = '{LIST}') || '|' "
                f"|| (select count(*) from list_items where list_id = '{LIST}')")
    print(f"   rows left (list|audit|collaborators|items): {left}")
    print("WALK COMPLETE" if not FAILURES else f"WALK FINISHED WITH FAILURES: {FAILURES}")
finally:
    # Put the granted roles back the way they were, then the keys.
    for (uid_g, role_g) in GRANTS:
        if not HAD[(uid_g, role_g)]:
            psql(f"delete from user_roles where user_id = '{uid_g}' and role = '{role_g}'")
    for u, old in saved.items():
        psql(f"update users set api_key = '{old}' where id = '{u}'")
    restored = all(psql(f"select coalesce(api_key,'') from users where id = '{u}'") == old for u, old in saved.items())
    print("RESTORE:", "every api_key is byte-identical to what it was" if restored
          else "MISMATCH -- a user's api_key was NOT restored")
    print("RESTORE: every granted role is back to its original state:",
          all((psql(f"select count(*) from user_roles where user_id = '{g[0]}' and role = '{g[1]}'") == "1") == HAD[g]
              for g in GRANTS))
    if LIST:
        residue = psql(f"select (select count(*) from lists where id = '{LIST}') || '|' "
                       f"|| (select count(*) from list_items where list_id = '{LIST}') || '|' "
                       f"|| (select count(*) from list_collaborators where list_id = '{LIST}')")
        print("residue after the walk (list|items|collaborators):", residue)
