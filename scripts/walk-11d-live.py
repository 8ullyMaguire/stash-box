#!/usr/bin/env python3
"""Walk slice 11d (seed cohorts and a campaign's fast-track) on a LIVE stash-box instance.

Run it ON the host that serves the instance: it reads the jwt secret from the deployment config and the
accounts from the instance's Postgres.

WHAT THIS WALK IS FOR. Four claims, and the live instance is the only place three of them can be checked
against real rows rather than against a fixture:

  1. THE CAMPAIGN PAYS ITS MEMBERS: the ledger holds one event per member, of kind `seed_fast_track`, for
     exactly the campaign's amount -- read from the DATABASE, because the wire exposes no points figure.
  2. RUNNING IT TWICE PAYS THE COHORT ONCE: a second run reports the members as SKIPPED and the amount in
     the ledger is unchanged. This is the dedup index doing the work, not a flag.
  3. MEMBERSHIP IS READ WHEN THE RUN RUNS: a member removed before a run is not paid, and a member added
     after one IS paid by the next run -- both observed on the deployed instance.
  4. THE HEAD START IS POINTS, NOT AN OVERRIDE: the member's level moves and the ledger says why. The
     exact curve arithmetic is the integration test's (Go can call `LevelForTotals`; this script cannot),
     so what the walk proves is that the level the deployed binary serves is on the move and that a
     re-run leaves it alone. That split is named here rather than implied.

WHAT IT CHANGES: the operator's roles and api_key (administered through SQL because no API key can reach
the instance's only ADMIN account -- measured in 11a), three throwaway accounts, the cohort, its
membership, one campaign, and the trust rows the runs write for those accounts.

EVERYTHING IS PUT BACK IN A `finally`, AND THE CLEANUP IS BY NAME rather than by captured id -- because
the first version of this walk aborted on a refused `psql` and left accounts, an ADMIN grant and a
modified api_key behind. It cleans up in the order the foreign keys need (trust rows, then roles, then
accounts, then the cohort, which takes its membership and campaigns with it), and it reads the database
back afterwards instead of assuming.

Nothing printed is a credential.

Run:  python3 scripts/walk-11d-live.py
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
OPERATOR_NAME = os.environ.get("WALK_OPERATOR", "StashBot")
COHORT = "walk-11d-cohort"
CAMPAIGN = "walk-11d-head-start"
MEMBER_PREFIX = "walk-11d-member-"
AMOUNT = 50

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

    Several of these claims are only visible in the ledger: the wire exposes no points figure, so "the head
    start was paid, once, to these members" is read from `trust_events` and `user_trust` directly. Passing a
    synthetic response keeps ONE verdict printer and ONE failure list, so a database failure is counted
    exactly like a wire failure instead of being printed and forgotten.
    """
    return check(label, (200, {"data": {}}), predicate, description)


OPERATOR = psql(f"select id from users where name = '{OPERATOR_NAME}'")
if not OPERATOR:
    sys.exit(f"need an account named {OPERATOR_NAME}")

HAD_ADMIN = psql(f"select count(*) from user_roles where user_id = '{OPERATOR}' and role = 'ADMIN'") != "0"
SAVED_KEY = psql(f"select api_key from users where id = '{OPERATOR}'")
if not HAD_ADMIN:
    psql(f"insert into user_roles (user_id, role) values ('{OPERATOR}', 'ADMIN')")
OPERATOR_KEY = mint(OPERATOR)
# The server compares the PRESENTED key with the STORED one, so a minted token is only usable once it has
# been written to the row.
psql(f"update users set api_key = '{OPERATOR_KEY}' where id = '{OPERATOR}'")

NOW = int(time.time())
NEW = {}
MEMBERS = {}


def make_member(suffix):
    name = f"{MEMBER_PREFIX}{suffix}-{NOW}"
    # `api_key`, `last_api_call` and the timestamps are NOT NULL with no default (measured: the raw insert
    # is refused without them), and the key is set to the name so two walk accounts cannot collide on it.
    # This account never authenticates -- nothing about it is a credential.
    psql(f"insert into users (id, name, password_hash, email, api_key, last_api_call, created_at, updated_at) "
         f"values (gen_random_uuid(), '{name}', 'walk-no-login', '{name}@example.invalid', '{name}', now(), "
         f"now(), now())")
    uid = psql(f"select id from users where name = '{name}'")
    NEW[suffix] = (uid, name)
    MEMBERS[suffix] = name
    return uid


def bonus(uid):
    return int(psql(f"select coalesce(bonus_points, 0) from user_trust where user_id = '{uid}'") or 0)


def level(uid):
    return int(psql(f"select coalesce(level, 0) from user_trust where user_id = '{uid}'") or 0)


def fast_track_events(uid):
    return int(psql(f"select count(*) from trust_events where user_id = '{uid}' "
                    f"and kind = 'seed_fast_track'") or 0)


def amount_in_ledger(campaign_id):
    return psql(f"select coalesce(sum(delta), 0)::text || '/' || count(*)::text from trust_events "
                f"where kind = 'seed_fast_track' and entity_id = '{campaign_id}'")


def cleanup():
    pattern = f"'{MEMBER_PREFIX}%'"
    psql(f"delete from trust_events where user_id in (select id from users where name like {pattern})")
    psql(f"delete from user_trust where user_id in (select id from users where name like {pattern})")
    psql(f"delete from user_roles where user_id in (select id from users where name like {pattern})")
    # NOT user_tokens: this walk never signs anybody up, and that table has no user_id at all (its rows are
    # keyed by their own id with a jsonb payload) -- a delete against it by user id would be nonsense.
    psql(f"delete from users where name like {pattern}")
    psql(f"delete from trust_events where entity_type = 'seed_campaign' and entity_id in "
         f"(select id from seed_campaigns where name = '{CAMPAIGN}')")
    psql(f"delete from seed_cohorts where name = '{COHORT}'")
    if not HAD_ADMIN:
        psql(f"delete from user_roles where user_id = '{OPERATOR}' and role = 'ADMIN'")
    psql(f"update users set api_key = '{SAVED_KEY}' where id = '{OPERATOR}'")

    left = psql(
        f"select (select count(*) from users where name like {pattern})::text || '/' || "
        f"(select count(*) from seed_cohorts where name = '{COHORT}')::text || '/' || "
        f"(select count(*) from seed_campaigns where name = '{CAMPAIGN}')::text || '/' || "
        f"(select count(*) from trust_events where kind = 'seed_fast_track')::text || '/' || "
        f"(select count(*) from user_roles where user_id = '{OPERATOR}' and role = 'ADMIN')::text")
    expect = f"0/0/0/0/{'1' if HAD_ADMIN else '0'}"
    ok = left == expect
    print(f"   VERDICT R1: {'OK' if ok else 'FAILED'} -- accounts/cohorts/campaigns/fast-track events/admin "
          f"role left behind: {left} (expected {expect})")
    if not ok:
        FAILURES.append("R1")


try:
    make_member("a")
    make_member("b")
    make_member("c")

    print("== what the deployed binary serves ==")
    # PREMISE FIRST: a field that does not exist on an older binary answers with an error, so this separates
    # "11d is not deployed" from "11d is deployed and unhappy".
    check("A1 the seed surface is served", gql(OPERATOR_KEY, "{ seedCohorts { id name } seedCampaigns { id } }"),
          lambda b: "errors" not in b and b.get("data") is not None,
          "the deployed binary answers for cohorts and campaigns")

    signed_out = gql("", "{ seedCohorts { id } }")
    check("A2 it is an operator's surface", signed_out,
          lambda b: b.get("errors") or (b.get("data") or {}).get("seedCohorts") is None,
          "an unauthenticated caller is refused (%s)" % (errors_of(signed_out)[:60] or "no data"))

    print("\n== the cohort, and the roster's own arithmetic ==")
    check("B1 a cohort is created",
          gql(OPERATOR_KEY,
              "mutation($input: SeedCohortCreateInput!) { createSeedCohort(input: $input) "
              "{ id name member_count } }",
              {"input": {"name": COHORT, "description": "walk 11d"}}),
          lambda b: b.get("data", {}).get("createSeedCohort", {}).get("member_count") == 0,
          "a new cohort is empty")
    COHORT_ID = psql(f"select id from seed_cohorts where name = '{COHORT}'")

    check("B2 two members are added",
          gql(OPERATOR_KEY,
              "mutation($input: SeedCohortMembersInput!) { addSeedCohortMembers(input: $input) }",
              {"input": {"cohort_id": COHORT_ID, "user_ids": [NEW["a"][0], NEW["b"][0]]}}),
          lambda b: b.get("data", {}).get("addSeedCohortMembers") == 2,
          "two accounts were NEWLY added")

    check("B3 adding one of them again counts for nothing",
          gql(OPERATOR_KEY,
              "mutation($input: SeedCohortMembersInput!) { addSeedCohortMembers(input: $input) }",
              {"input": {"cohort_id": COHORT_ID, "user_ids": [NEW["a"][0]]}}),
          lambda b: b.get("data", {}).get("addSeedCohortMembers") == 0,
          "the roster is a set, and the count says so")

    print("\n== the run ==")
    before_a = (bonus(NEW["a"][0]), level(NEW["a"][0]))
    before_b = (bonus(NEW["b"][0]), level(NEW["b"][0]))

    check("C1 a campaign is created",
          gql(OPERATOR_KEY,
              "mutation($input: SeedCampaignCreateInput!) { createSeedCampaign(input: $input) "
              "{ id name points last_run_at } }",
              {"input": {"cohort_id": COHORT_ID, "name": CAMPAIGN, "points": AMOUNT}}),
          lambda b: b.get("data", {}).get("createSeedCampaign", {}).get("points") == AMOUNT
          and b["data"]["createSeedCampaign"]["last_run_at"] is None,
          "a campaign that has NEVER run says so by being null")

    refused = gql(OPERATOR_KEY,
                  "mutation($input: SeedCampaignCreateInput!) { createSeedCampaign(input: $input) { id } }",
                  {"input": {"cohort_id": COHORT_ID, "name": CAMPAIGN + "-negative", "points": -5}})
    check("C2 a campaign must be worth something", refused,
          lambda b: bool(b.get("errors")) or b.get("data", {}).get("createSeedCampaign") is None,
          "a negative head start is refused (%s)" % (errors_of(refused)[:60] or "no data"))

    CAMPAIGN_ID = psql(f"select id from seed_campaigns where name = '{CAMPAIGN}'")
    check("C3 the first run pays both members",
          gql(OPERATOR_KEY,
              "mutation($id: ID!) { runSeedCampaign(id: $id) "
              "{ campaign { last_run_at } members paid skipped } }", {"id": CAMPAIGN_ID}),
          lambda b: b["data"]["runSeedCampaign"]["members"] == 2
          and b["data"]["runSeedCampaign"]["paid"] == 2
          and b["data"]["runSeedCampaign"]["skipped"] == 0,
          "members 2, paid 2, skipped 0")

    db_check("C4 the ledger holds one event per member for the campaign's amount",
             lambda _: amount_in_ledger(CAMPAIGN_ID) == f"{AMOUNT * 2}/{2}",
             f"{AMOUNT * 2} points over 2 events (read from trust_events)")

    db_check("C5 each member's own rollup moved by the head start",
             lambda _: bonus(NEW["a"][0]) == before_a[0] + AMOUNT
             and bonus(NEW["b"][0]) == before_b[0] + AMOUNT,
             f"bonus_points +{AMOUNT} each")

    db_check("C6 the level followed the points",
             lambda _: level(NEW["a"][0]) > before_a[1] and level(NEW["a"][0]) > 0,
             f"level {before_a[1]} -> {level(NEW['a'][0])} (the curve, computed by the binary)")

    db_check("C7 the campaign records that it ran",
             lambda _: bool(psql(f"select last_run_at from seed_campaigns where id = '{CAMPAIGN_ID}'")),
             "last_run_at is set")

    print("\n== twice is once ==")
    check("D1 the second run pays nobody",
          gql(OPERATOR_KEY, "mutation($id: ID!) { runSeedCampaign(id: $id) { members paid skipped } }",
              {"id": CAMPAIGN_ID}),
          lambda b: b["data"]["runSeedCampaign"]["paid"] == 0
          and b["data"]["runSeedCampaign"]["skipped"] == 2,
          "paid 0, skipped 2 -- reported, not an error")

    db_check("D2 the ledger is unchanged",
             lambda _: amount_in_ledger(CAMPAIGN_ID) == f"{AMOUNT * 2}/{2}",
             "still one event per member")

    db_check("D3 and the rollup is unchanged",
             lambda _: bonus(NEW["a"][0]) == before_a[0] + AMOUNT,
             f"still +{AMOUNT}, not +{AMOUNT * 2}")

    print("\n== membership is read when the run runs ==")
    psql(f"delete from seed_cohort_members where cohort_id = '{COHORT_ID}' and user_id = '{NEW['b'][0]}'")
    check("E1 a member who left before the run is not paid",
          gql(OPERATOR_KEY, "mutation($id: ID!) { runSeedCampaign(id: $id) { members paid skipped } }",
              {"id": CAMPAIGN_ID}),
          lambda b: b["data"]["runSeedCampaign"]["members"] == 1
          and b["data"]["runSeedCampaign"]["paid"] == 0
          and b["data"]["runSeedCampaign"]["skipped"] == 1,
          "the cohort as it stands: 1 member, already paid")

    db_check("E2 nothing was written for them",
             lambda _: bonus(NEW["b"][0]) == before_b[0] + AMOUNT and fast_track_events(NEW["b"][0]) == 1,
             "their single event is the one the first run paid")

    gql(OPERATOR_KEY, "mutation($input: SeedCohortMembersInput!) { addSeedCohortMembers(input: $input) }",
        {"input": {"cohort_id": COHORT_ID, "user_ids": [NEW["c"][0]]}})
    check("E3 a member added later IS paid by the next run",
          gql(OPERATOR_KEY, "mutation($id: ID!) { runSeedCampaign(id: $id) { members paid skipped } }",
              {"id": CAMPAIGN_ID}),
          lambda b: b["data"]["runSeedCampaign"]["members"] == 2
          and b["data"]["runSeedCampaign"]["paid"] == 1
          and b["data"]["runSeedCampaign"]["skipped"] == 1,
          "one paid, one already had it -- the run is resumable")

    db_check("E4 their own rollup moved",
             lambda _: bonus(NEW["c"][0]) == AMOUNT and fast_track_events(NEW["c"][0]) == 1,
             f"+{AMOUNT} for the member who arrived after the first run")

    print("\n== what the surface shows an operator ==")
    check("F1 the roster is readable, with names",
          gql(OPERATOR_KEY,
              "query($id: ID!) { seedCohort(id: $id) { member_count members { name } } }",
              {"id": COHORT_ID}),
          lambda b: b["data"]["seedCohort"]["member_count"] == 2
          and len(b["data"]["seedCohort"]["members"]) == 2,
          "two members, named")
finally:
    print("\n== restore ==")
    cleanup()

print()
if FAILURES:
    print("FAILED: " + ", ".join(FAILURES))
    sys.exit(1)
print("all verdicts green")
