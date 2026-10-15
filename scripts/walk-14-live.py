#!/usr/bin/env python3
"""Walk phase 14 — the published policy, and the appeal a DIFFERENT moderator hears — on a LIVE instance.

Run it ON the host that serves the instance (`ssh thinkcentre 'cd ~/stash-box-deploy && python3
/tmp/walk-14-live.py'`), after deploying the binary that carries migration 141. Every verdict is a claim
about the DEPLOYED process and the DEPLOYED database, not about a test fixture.

WHAT THIS WALK IS FOR. Slice 14.2's rule is a person: the appeal against a refusal is decided by a moderator
who is NEITHER the operator who recorded the block NOR the member who filed it, and that is asserted by
identity. A unit test can compare two uuids; only a live run can show that the rule survives the wire, the
directives, the session and the roles the deployed instance actually has. So the walk plants the exact
people the rule excludes and reads the refusal in the instance's own words.

  A. THE POLICY IS PUBLISHED, to somebody with no account at all, and it NAMES ITS OWN STATE: an instance
    whose operator has not set `abuse_contact` says so instead of showing a blank where an address goes.
    Both arms are a pass, and which one fired is printed -- this is the phase 14 gate made visible.
  B. A REFUSAL, THEN A CONTEST: an operator blocks a host, a member appeals against the HOST (what they
    were refused, not a row id), and the appeal is filed with the blocker carried onto it.
  C. BOTH EXCLUDED PEOPLE ARE REFUSED, in the live process and in its own wording: the operator who
    recorded the block, and the member who filed the appeal. BOTH HOLD ADMIN for the run -- deliberately,
    because a non-admin would be stopped by the role directive first and the identity rule would never be
    reached. That is the whole reason the fixture looks wrong and is right.
  D. A THIRD MODERATOR GRANTS IT: the block is gone from the database, the appeal survives with its
    reference null and its blocker still named, and the decision is final.
  E. THE TRAIL: `SOURCE_APPEAL` and `SOURCE_APPEAL_DECISION` rows exist in `mod_audit` against the appeal.

WHAT IT CHANGES, AND PUTS BACK. It writes one block row, one appeal and three throwaway accounts, and it
restores the operator's `api_key` (minted for the run, since the server compares the presented key with the
stored one). The cleanup is BY NAME in foreign-key order and its result is its own verdict -- a walk that
leaves rows behind on a live instance is a walk that changes the thing it was measuring.

Usage:  python3 walk-14-live.py [graphql-url]
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
import uuid

URL = sys.argv[1] if len(sys.argv) > 1 else "http://127.0.0.1:9998/graphql"
BASE = URL.rsplit("/graphql", 1)[0]
DB = "stash-box"
CONFIG = "/home/alvaro/stash-box-deploy/config/stash-box-config.yml"
OPERATOR_NAME = os.environ.get("WALK_OPERATOR", "StashBot")

RUN = uuid.uuid4().hex[:8]
# Deliberately `.invalid`, and named so the cleanup can find it by prefix: this is a REAL block row on a live
# instance, so it must be removable by name and never collide with anything an operator wrote.
BLOCK_HOST = f"walk-14-{RUN}.invalid"
MEMBER_PREFIX = "walk-14-"

FAILURES = []


def psql(sql):
    out = subprocess.run(
        ["sudo", "docker", "exec", "-i", "stashbox-pg", "psql", "-U", "stashbox", "-d", DB, "-tAc", sql],
        capture_output=True, text=True,
    )
    if out.returncode != 0:
        print(f"!! psql refused: {out.stderr.strip()}")
        return ""
    return out.stdout.strip()


secret = None
for line in open(CONFIG).read().splitlines():
    if line.startswith("jwt_secret_key:"):
        secret = line.split(":", 1)[1].strip()
if not secret:
    sys.exit("no jwt_secret_key in " + CONFIG)


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


def http_get(url):
    req = urllib.request.Request(url, headers={"Accept": "text/html"})
    try:
        with urllib.request.urlopen(req) as r:
            return r.status, dict(r.headers), r.read().decode()
    except urllib.error.HTTPError as e:
        return e.code, dict(e.headers), e.read().decode()


def errors_of(resp):
    status, body = resp
    if isinstance(body, dict) and body.get("errors"):
        return " ".join(e.get("message", "") for e in body["errors"]).strip()
    return ""


def check(label, resp, predicate, description):
    status, body = resp
    ok = False
    if status == 200:
        try:
            ok = bool(predicate(body))
        except Exception as exc:  # a missing field IS the failure being asserted about
            print(f"   (the predicate could not read the response: {exc})")
    if not ok:
        print(f"   (why: {errors_of(resp) or str(body)[:200]})")
    print(f"   VERDICT {label}: {'OK' if ok else 'FAILED'} -- {description}")
    if not ok:
        FAILURES.append(label)
    return body


def db_check(label, predicate, description):
    """A verdict about the DATABASE rather than about a response.

    The surviving appeal and the deleted block are only visible in the rows -- the queue shows the appeal,
    but "the block row is gone" and "the appeal still names its blocker" are facts about a table. One verdict
    printer and one failure list, so a database failure is counted exactly like a wire failure.
    """
    return check(label, (200, {"data": {}}), predicate, description)


def refused_with(fragment):
    """A predicate for a REFUSAL: the response carries a GraphQL error whose message names the reason.

    Written as a predicate rather than as `lambda b: False` plus a printed reason, because the wording IS the
    claim: "the identity rule refused this person" is only true if the instance said so, and a role-directive
    refusal would arrive here with different words.
    """
    def predicate(body):
        return any(fragment in (e.get("message") or "") for e in (body.get("errors") or []))
    return predicate


def create_account(suffix):
    """Make a throwaway account for the run: it never signs in, and it is deleted by name afterwards.

    `api_key`, `last_api_call` and the timestamps are NOT NULL with no default (measured), and the key is set
    to the minted token so the account can present a credential the server will accept -- the server compares
    the presented key with the STORED one.
    """
    name = f"{MEMBER_PREFIX}{RUN}-{suffix}"
    psql("insert into users (id, name, password_hash, email, api_key, last_api_call, created_at, updated_at) "
         f"values (gen_random_uuid(), '{name}', 'walk-no-login', '{name}@example.invalid', '', now(), now(), now())")
    uid = psql(f"select id from users where name = '{name}'")
    if not uid:
        sys.exit(f"could not create {name}")
    # MINTED ONCE, and the SAME string is stored and presented. The server compares the presented key with
    # the stored one, and `mint` stamps `iat`/`exp` from the clock -- so calling it twice returns two
    # different tokens and the account's own key is refused. Measured: the first run of this walk reported
    # every authenticated mutation as a failure with an EMPTY body, which is what a mismatched key looks
    # like from a client.
    key = mint(uid)
    psql(f"update users set api_key = '{key}' where id = '{uid}'")
    psql(f"insert into user_roles (user_id, role) values ('{uid}', 'ADMIN')")
    return uid, name, key


OPERATOR = psql(f"select id from users where name = '{OPERATOR_NAME}'")
if not OPERATOR:
    sys.exit(f"need an account named {OPERATOR_NAME}")

SAVED_KEY = psql(f"select api_key from users where id = '{OPERATOR}'")
OPERATOR_KEY = mint(OPERATOR)
psql(f"update users set api_key = '{OPERATOR_KEY}' where id = '{OPERATOR}'")

BLOCKER_ID, BLOCKER_NAME, BLOCKER_KEY = create_account("blocker")
FILER_ID, FILER_NAME, FILER_KEY = create_account("filer")
DECIDER_ID, DECIDER_NAME, DECIDER_KEY = create_account("decider")

FIELDS = "id block_id kind value statement outcome decision_note released filed_by { id } blocked_by { id } decided_by { id }"


def cleanup():
    print("\n== restore ==")
    pattern = f"'{MEMBER_PREFIX}{RUN}%'"
    # FOREIGN-KEY ORDER, by name: the appeal names users and the block names an operator, so the rows that
    # reference accounts go first.
    psql(f"delete from content_source_block_appeal where value = '{BLOCK_HOST}'")
    psql(f"delete from content_source_block where value = '{BLOCK_HOST}'")
    psql(f"delete from mod_audit where user_id in (select id from users where name like {pattern})")
    psql(f"delete from user_roles where user_id in (select id from users where name like {pattern})")
    psql(f"delete from users where name like {pattern}")
    psql(f"update users set api_key = '{SAVED_KEY}' where id = '{OPERATOR}'")

    restored = psql(f"select api_key from users where id = '{OPERATOR}'")
    db_check("R1 the key the walk replaced is back", lambda _: restored == SAVED_KEY,
             "the operator's stored key is byte-identical to the one read before the run")
    left = (psql(f"select count(*) from users where name like {pattern}") or "?")
    blocks = (psql(f"select count(*) from content_source_block where value = '{BLOCK_HOST}'") or "?")
    appeals = (psql(f"select count(*) from content_source_block_appeal where value = '{BLOCK_HOST}'") or "?")
    db_check("R2 the run left nothing behind", lambda _: (left, blocks, appeals) == ("0", "0", "0"),
             f"accounts={left} blocks={blocks} appeals={appeals} (all read back after the deletes)")


try:
    print("== the policy is published, to somebody with no account ==")
    status, headers, page = http_get(BASE + "/policy")
    # Counted in one pass so a page that lost three commitments fails with a list rather than on the first.
    commitments = {
        "the acknowledgement window": "24 hours",
        "the appeal path": "contest a refusal",
        "who hears the appeal": "moderator who did not make the",
        "what the instance is": "does not store the works themselves",
        "the metadata survives": "The metadata stays",
    }
    missing = [f"{k} ({v})" for k, v in commitments.items() if v not in page]
    check("A1 the policy is served to anybody, with its commitments intact",
          (status, {"data": {"page": page}}),
          lambda b: b["data"]["page"] == page,
          f"GET /policy -> {status}, {len(page)} bytes, missing={missing or 'nothing'}")
    if missing:
        FAILURES.append("A1-commitments")
    has_contact = "mailto:" in page
    names_gap = "has not published an address for notices" in page
    check("A2 the policy is deliberately indexable (no noindex)",
          (status, {"data": {"tag": headers.get("X-Robots-Tag", "")}}),
          lambda b: "noindex" not in b["data"]["tag"],
          "the invite landing page is noindex; the notice path is the one page an instance WANTS found")
    check("A3 the policy is honest about its own contact state",
          (status, {"data": {"contact": has_contact, "gap": names_gap}}),
          lambda b: b["data"]["contact"] != b["data"]["gap"],
          ("it names a configured address" if has_contact else
           "it says the instance has not published one (so `abuse_contact` is unset on this instance)"))

    print("\n== a refusal, and the appeal against it ==")
    blocked = check("B1 an operator blocks a host",
                    gql(BLOCKER_KEY, "mutation($input: BlockContentHostInput!) { blockContentHost(input: $input) "
                                     "{ block { id kind value } changed already_blocked } }",
                        {"input": {"host": BLOCK_HOST, "reason": "walk-14 live"}}),
                    lambda b: b["data"]["blockContentHost"]["block"]["kind"] == "host"
                    and b["data"]["blockContentHost"]["block"]["value"] == BLOCK_HOST,
                    f"the refused host is {BLOCK_HOST} (a host block refuses every address on it)")

    filed = check("B2 a member appeals against the HOST, not a row id",
                  gql(FILER_KEY, "mutation($input: FileContentSourceAppealInput!) { fileContentAppeal(input: $input) "
                                 "{ " + FIELDS + " } }",
                      {"input": {"kind": "host", "value": BLOCK_HOST,
                                 "statement": "walk-14: this host is one I submit from"}}),
                  lambda b: b["data"]["fileContentAppeal"]["value"] == BLOCK_HOST
                  and (b["data"]["fileContentAppeal"]["blocked_by"] or {}).get("id") == BLOCKER_ID,
                  "filed, with the operator who recorded the block carried onto the appeal")
    appeal_id = (((filed.get("data") or {}).get("fileContentAppeal") or {}).get("id")) if isinstance(filed, dict) else None
    if not appeal_id:
        sys.exit("no appeal id -- the rest of the walk has nothing to decide")

    print("\n== the two people the rule excludes ==")
    check("C1 the operator who recorded the block is refused the decision",
          gql(BLOCKER_KEY, "mutation($id: ID!, $outcome: String!) { decideContentAppeal(id: $id, outcome: $outcome) "
                           "{ id } }", {"id": appeal_id, "outcome": "denied"}),
          refused_with("you recorded the block"),
          "the refusal names the arm: the operator who recorded this block cannot decide the appeal against it")
    check("C2 the member who filed the appeal is refused the decision",
          gql(FILER_KEY, "mutation($id: ID!, $outcome: String!) { decideContentAppeal(id: $id, outcome: $outcome) "
                         "{ id } }", {"id": appeal_id, "outcome": "granted"}),
          refused_with("you filed this appeal"),
          "both refusals come from the identity rule and not the role directive: BOTH accounts hold ADMIN")

    print("\n== a moderator who did neither grants it ==")
    queue_before = gql(DECIDER_KEY, "query { contentAppeals(pending_only: true) { id outcome } }")
    check("D1 the appeal is in the queue, pending",
          queue_before, lambda b: any(a["id"] == appeal_id and a["outcome"] is None
                                      for a in b["data"]["contentAppeals"]),
          f"the moderator's queue lists {appeal_id} with no outcome")

    granted = check("D2 a third moderator grants it, and the release is reported",
                    gql(DECIDER_KEY, "mutation($id: ID!, $outcome: String!, $note: String) { "
                                     "decideContentAppeal(id: $id, outcome: $outcome, note: $note) { " + FIELDS + " } }",
                        {"id": appeal_id, "outcome": "granted", "note": "walk-14: accepted on review"}),
                    lambda b: b["data"]["decideContentAppeal"]["outcome"] == "granted"
                    and b["data"]["decideContentAppeal"]["released"] is True
                    and (b["data"]["decideContentAppeal"]["decided_by"] or {}).get("id") == DECIDER_ID,
                    "granted by somebody other than the blocker and the appellant, and the block was released")

    db_check("D3 the block row is gone from the database",
             lambda _: psql(f"select count(*) from content_source_block where value = '{BLOCK_HOST}'") == "0",
             "a grant releases through the operator's own path, so the row is deleted and not flagged")

    survived = psql("select kind || '|' || value || '|' || coalesce(block_id::text, 'NULL') || '|' || "
                    "coalesce(blocked_by_id::text, 'NULL') || '|' || coalesce(outcome, 'NULL') "
                    f"from content_source_block_appeal where id = '{appeal_id}'")
    parts = survived.split("|") if survived else []
    db_check("D4 the appeal survives the block it contested",
             lambda _: len(parts) == 5 and parts[0] == "host" and parts[1] == BLOCK_HOST
             and parts[2] == "NULL" and parts[3] == BLOCKER_ID and parts[4] == "granted",
             f"row reads kind|value|block_id|blocked_by_id|outcome = {survived or '(nothing)'} "
             f"-- the block reference is null and the blocker is still named, which is what makes the rule "
             f"hold for a pending appeal after a release")

    print("\n== the decision is final, and recorded ==")
    check("E1 a second decision is refused",
          gql(DECIDER_KEY, "mutation($id: ID!, $outcome: String!) { decideContentAppeal(id: $id, outcome: $outcome) "
                           "{ id } }", {"id": appeal_id, "outcome": "denied"}),
          refused_with("already been decided"),
          "the first outcome is the outcome: a second decision matches no row")
    actions = psql("select string_agg(action::text, ',' order by created_at) from mod_audit "
                   f"where target_id = '{appeal_id}'")
    db_check("E2 filing and deciding are both in mod_audit against the appeal",
             lambda _: actions == "SOURCE_APPEAL,SOURCE_APPEAL_DECISION",
             f"mod_audit actions = {actions or '(nothing)'} (target_type SOURCE_APPEAL, not the block: the "
             f"block can be deleted, and a trail pointed at it would vanish with it)")
finally:
    cleanup()

print()
if FAILURES:
    print("FAILED: " + ", ".join(sorted(set(FAILURES))))
    sys.exit(1)
print("all verdicts green")
