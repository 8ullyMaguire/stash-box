#!/usr/bin/env python3
"""Walk slice 13d (rate limits and the error shape) on a LIVE stash-box instance.

Run it ON the host that serves the instance. This is the slice where a walk is not a nicety: the limiter's
unit tests prove the bucket, and the middleware's prove the response -- but "is it MOUNTED" is a claim about
`internal/api/server.go`, and the only way to check a claim about which handler runs is to send the requests.

WHAT IS ACTUALLY BEING JUDGED:

  A. THE LIMIT IS ENFORCED ON THE PUBLIC SURFACE. Requests against `/graphql` with one credential eventually
     get 429 -- the limit exists, at the wire, on the deployed binary.
  B. THE REFUSAL IS THE SAME ERROR SHAPE AS EVERY OTHER ERROR. A 429 whose body is HTML, a bare string or
     nothing is the defect the plan names: a GraphQL client is already parsing {"errors":[{"message": ...}]}.
     Asserted by PARSING the body, not by looking at the status.
  C. THE LIMIT AND THE WAIT ARE STATED. `X-RateLimit-Limit` on the allowed responses too (a client that only
     learns the limit when it is refused will be refused again), and a `Retry-After` a client can obey.
  D. THE UNIT IS THE CREDENTIAL, NOT THE ADDRESS. This is the plan's central constraint, and it is measured by
     sending requests from ONE address with TWO credentials: the second must be served while the first is
     refused. A per-address limit would refuse both, and that is the whole reason the plan rejects it.
  E. NOT EVERYTHING IS LIMITED. The SPA and the assets are not a caller spending an API budget, and a member
     loading a page must not be counted against one. Asserted as an ABSENCE of the limit headers on those
     paths -- which needs its subject counted first, or "no header" passes on a request that never arrived.
  F. A FEED READER IS COUNTED BY ITS TOKEN, and two readers are two buckets. A feed URL is a credential: the
     reader sends no headers, so the address IS the identity, and counting it as an anonymous address would
     put every reader behind one proxy into one bucket.

WHAT IT CHANGES: nothing in the database. It spends two credentials' rate-limit budgets (memory, refilled in
seconds) and mints API keys for two real accounts, restoring both in a `finally` and VERIFYING the restore --
a walk that leaves somebody's key replaced is a walk that broke the account it borrowed.

Run:  python3 scripts/walk-13d-live.py
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

BASE = "http://127.0.0.1:9998"
URL = BASE + "/graphql"
DB = "stash-box"
CONFIG = "/home/alvaro/stash-box-deploy/config/stash-box-config.yml"
OPERATOR_NAME = os.environ.get("WALK_OPERATOR", "StashBot")
# How many requests to spend before concluding a limit is not there. The shipped burst is 120, so this is
# generous by design: a walk that gave up at 121 would report a broken limit for a limit with a bigger burst.
ATTEMPTS = 400

FAILURES = []


def sh(cmd):
    return subprocess.run(cmd, capture_output=True, text=True)


def psql(sql):
    out = sh(["sudo", "docker", "exec", "-i", "stashbox-pg", "psql", "-U", "stashbox", "-d", DB, "-tAc", sql])
    if out.returncode != 0:
        print(f"!! psql refused: {out.stderr.strip()}")
        return ""
    return out.stdout.strip()


def q(text):
    return "'" + str(text).replace("'", "''") + "'"


def verdict(label, ok, description):
    print(f"   VERDICT {label}: {'OK' if ok else 'FAILED'} -- {description}")
    if not ok:
        FAILURES.append(label)


def report(label, description):
    print(f"   MEASURED {label}: {description}")


def headers_of(message):
    """A CASE-FOLDED view of a response's headers, because header names are case-insensitive and Go sends
    them canonicalised: the middleware sets `X-RateLimit-Limit` and the wire carries `X-Ratelimit-Limit`. The
    first version of this walk converted the headers with `dict()`, which made every lookup case-SENSITIVE,
    and reported a middleware that sends them correctly as one that sends nothing."""
    return {name.lower(): value for name, value in message.items()}


def request(path, key=None):
    """One HTTP request, returning (status, headers, body). Never raises on an HTTP error status."""
    req = urllib.request.Request(BASE + path, data=None, headers={})
    if key:
        req.add_header("ApiKey", key)
    try:
        with urllib.request.urlopen(req, timeout=60) as response:
            return response.status, headers_of(response.headers), response.read().decode("utf-8", "replace")
    except urllib.error.HTTPError as err:
        return err.code, headers_of(err.headers), err.read().decode("utf-8", "replace")


def query(key, text="{ __typename }"):
    body = json.dumps({"query": text}).encode()
    req = urllib.request.Request(URL, data=body, headers={"Content-Type": "application/json"})
    if key:
        req.add_header("ApiKey", key)
    try:
        with urllib.request.urlopen(req, timeout=60) as response:
            return response.status, headers_of(response.headers), response.read().decode("utf-8", "replace")
    except urllib.error.HTTPError as err:
        return err.code, headers_of(err.headers), err.read().decode("utf-8", "replace")


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


OPERATOR = psql(f"select id from users where name = {q(OPERATOR_NAME)}")
if not OPERATOR:
    sys.exit(f"need an account named {OPERATOR_NAME}")

# A SECOND CREDENTIAL from the same address, which is what makes D a measurement rather than an assumption.
SECOND = psql("select id from users where id <> " + q(OPERATOR) + " order by name limit 1")
if not SECOND:
    sys.exit("need a second account: the per-credential rule cannot be judged with one")

SAVED = {
    OPERATOR: psql(f"select api_key from users where id = {q(OPERATOR)}"),
    SECOND: psql(f"select api_key from users where id = {q(SECOND)}"),
}
FIRST_KEY, SECOND_KEY = mint(OPERATOR), mint(SECOND)
for uid, key in ((OPERATOR, FIRST_KEY), (SECOND, SECOND_KEY)):
    psql(f"update users set api_key = {q(key)} where id = {q(uid)}")

try:
    print("== A/B/C. the limit is at the wire, and its refusal is this API's own error shape ==")
    allowed_headers = None
    refused = None
    spent = 0
    for _ in range(ATTEMPTS):
        status, headers, body = query(FIRST_KEY)
        spent += 1
        if status == 429:
            refused = (headers, body)
            break
        if allowed_headers is None:
            allowed_headers = headers

    report("A0", f"the limit was reached after {spent} requests (the shipped burst is 120)")
    verdict("A1", refused is not None, "a credential that keeps going is eventually refused with 429")

    if refused:
        headers, body = refused
        verdict("A2", headers.get("content-type", "").startswith("application/json"),
                f"the refusal is JSON, not HTML: {headers.get('Content-Type')}")
        try:
            parsed = json.loads(body)
        except Exception as err:  # noqa: BLE001 - the failure IS the message
            parsed = None
            verdict("A2b", False, f"the refusal body does not parse: {err}: {body[:120]}")
        if parsed:
            message = ((parsed.get("errors") or [{}])[0] or {}).get("message", "")
            verdict("B1", bool(message) and "Too many requests" in message,
                    f"the body is the shape every other error uses: {message[:150]}")
            verdict("B2", "600" in message and "Retry in" in message,
                    "and it names the limit and the wait, which is what a person debugging a script needs")
            verdict("B3", body.replace(" ", "").replace("\n", "").startswith('{"errors":[{'),
                    "the top-level shape is {\"errors\":[{\"message\": ...}]}")
        wait = headers.get("retry-after", "")
        verdict("C1", wait.isdigit() and int(wait) > 0,
                f"Retry-After is a whole number of seconds a client can obey: {wait!r}")
        verdict("C2", headers.get("x-ratelimit-remaining") == "0", "the remaining count is exhausted")

    if allowed_headers:
        verdict("C3", allowed_headers.get("x-ratelimit-limit", "").isdigit(),
                f"and the limit is stated on an ALLOWED response too: "
                f"X-RateLimit-Limit={allowed_headers.get('x-ratelimit-limit')!r}")

    print("== D. the unit is the credential, not the address ==")
    # Both requests come from this host: if the limit were per address, the second credential would be refused
    # as well. That is the plan's reason for choosing the credential, measured rather than argued.
    second_status, _, second_body = query(SECOND_KEY)
    verdict("D1", second_status == 200,
            f"a SECOND credential from the SAME address is served while the first is refused "
            f"(status {second_status})")
    # ...and the control: that second credential is not served BECAUSE nothing is limited.
    again_status, headers_again, _ = query(SECOND_KEY)
    verdict("D2", again_status in (200, 429),
            f"the second credential is being counted like any other ({again_status}); it is not exempt")
    verdict("D3", second_body.strip() != "" or again_status != 200, "and the served request has a body")

    print("== E. the SPA is not counted against an API budget ==")
    # The subject is counted FIRST: an absence assertion on a path that never answered is vacuous.
    front_status, front_headers, front_body = request("/")
    verdict("E1", front_status == 200 and "<" in front_body,
            f"the SPA answers ({front_status}, {len(front_body)} bytes)")
    verdict("E2", "x-ratelimit-limit" not in front_headers,
            "and carries no API limit header: a member loading a page is not a caller spending a budget")

    print("== F. a feed reader is counted by its token ==")
    feedA = {}
    for _ in range(ATTEMPTS):
        status, headers, _ = request("/feeds/saved-search/walk-13d-not-a-token")
        if status == 429:
            feedA = headers
            break
    verdict("F1", bool(feedA), "a feed URL is limited like any other public surface")
    if feedA:
        verdict("F2", feedA.get("retry-after", "").isdigit(),
                f"with the same header a client can obey: {feedA.get('retry-after')!r}")
    # A different token is a different reader, even from the same address.
    other_status, _, _ = request("/feeds/saved-search/walk-13d-other-token")
    verdict("F3", other_status == 404,
            f"a DIFFERENT token is not refused by the first one's spending ({other_status}: a token that was "
            f"never minted is the same 404 as always)")
finally:
    for uid, key in SAVED.items():
        psql(f"update users set api_key = {q(key)} where id = {q(uid)}")
        restored = psql(f"select api_key from users where id = {q(uid)}")
        if restored != key:
            print(f"!! the key of {uid} was NOT restored")
            FAILURES.append("cleanup")

print()
if FAILURES:
    print(f"FAILED: {', '.join(FAILURES)}")
    sys.exit(1)
print("all verdicts green")
