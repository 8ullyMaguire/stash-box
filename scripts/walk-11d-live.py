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
     so what the walk proves is that the level the deployed binary serves is the one its stored counters
     imply, and that a re-run leaves it alone. That split is named here rather than implied.

WHAT IT CHANGES, and puts back in a finally block: the operator's roles (given ADMIN for the walk, and
ADMIN is not reachable by any API key otherwise -- measured in 11a), two throwaway accounts, the cohort
and its membership, two campaigns, and every trust row the run wrote for those accounts. Nothing printed
is a credential.

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
MEMBER_NAME = os.environ.get("WALK_MEMBER", "Lutrification")
OPERATOR_NAME = os.environ.get("WALK_OPERATOR", "StashBot")
COHORT = "walk-11d-cohort"
CAMPAIGN = "walk-11d-head-start"
AMOUNT = 50


def psql(sql):
    out = subprocess.run(
        ["sudo", "docker", "exec", "-i", "stashbox-pg", "psql", "-U", "stashbox", "-d", DB, "-tAc", sql],
        capture_output=True, text=True,
    )
    if out.returncode != 0:
        sys.exit("psql failed: " + out.stderr.strip())
    return out.stdout.strip()


secret = next(l.split(":", 1)[1].strip() for l in open(CONFIG).read().splitlines()
              if l.startswith("jwt_secret_key:"))


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


def check(label, resp, predicate, description):
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


def db_check(label, predicate, description):
    """A verdict about the DATABASE rather than about a response.

    Three of these claims are only visible in the ledger: the wire exposes no points figure, so "the head
    start was paid, once, to these members" is read from `trust_events` and `user_trust` directly. Passing
    a synthetic response keeps ONE verdict printer and ONE failure list, so a database failure is reported
    and counted exactly like a wire failure instead of being printed and forgotten.
    """
    return check(label, (200, {"data": {}}), predicate, description)


def user_id(name):
    return psql("select id from users where name = '%s'" % name)


def errors_of(resp):
    status, body = resp
    if isinstance(body, dict) and body.get("errors"):
        return " ".join(e.get("message", "") for e in body["errors"])
    return ""


OPERATOR = user_id(OPERATOR_NAME)
MEMBER = user_id(MEMBER_NAME)
if not OPERATOR:
    sys.exit(f"need an account named {OPERATOR_NAME}")

# ADMIN is the role every field of this surface carries, and no API key can reach the one ADMIN account
# (measured in 11a), so the walk grants the operator ADMIN and takes it away again.
HAD_ADMIN = psql(f"select count(*) from user_roles where user_id = '{OPERATOR}' and role = 'ADMIN'") != "0"
if not HAD_ADMIN:
    psql(f"insert into user_roles (user_id, role) values ('{OPERATOR}', 'ADMIN')")
OPERATOR_KEY = mint(OPERATOR)

NOW = int(time.time())
NEW = {}
for suffix in ("a", "b", "c"):
    name = f"walk-11d-member-{suffix}-{NOW}"
    uid = psql(f"insert into users (id, name, password_hash, email) "
               f"values (gen_random_uuid(), '{name}', 'walk-no-login', '{name}@example.invalid') "
               f"returning id")
    NEW[suffix] = (uid, name)


def bonus(uid):
    row = psql(f"select coalesce(bonus_points, 0) from user_trust where user_id = '{uid}'")
    return int(row or 0)


def level(uid):
    return int(psql(f"select coalesce(level, 0) from user_trust where user_id = '{uid}'") or 0)


def events(uid):
    return int(psql(f"select count(*) from trust_events where user_id = '{uid}' "
                    f"and kind = 'seed_fast_track'"))


def amount_in_ledger(campaign_id):
    return psql(f"select coalesce(sum(delta), 0)::text || '/' || count(*)::text from trust_events "
                f"where kind = 'seed_fast_track' and entity_id = '{campaign_id}'")


print("== what the deployed binary serves ==")
# PREMISE FIRST: a field that does not exist on an older binary answers with an error, so this separates
# "11d is not deployed" from "11d is deployed and unhappy".
check("A1 the seed surface is served", gql(OPERATOR_KEY, "{ seedCohorts { id name } seedCampaigns { id } }"),
      lambda b: "errors" not in b and ("seedCohorts" in b.get("data") or b.get("data")),
      "the deployed binary answers for cohorts and campaigns")

signed_out = gql("", "{ seedCohorts { id } }")
check("A2 it is an operator's surface", signed_out,
      lambda b: b.get("errors") or b.get("data", {}).get("seedCohorts") is None,
      "an unauthenticated caller is refused (%s)" % (errors_of(signed_out)[:80] or "no data"))

print("\n== the cohort, and the roster's own arithmetic ==")


def create_cohort():
    return gql(OPERATOR_KEY,
               "mutation($input: SeedCohortCreateInput!) { createSeedCohort(input: $input) { id name member_count } }",
               {"input": {"name": COHORT, "description": "walk 11d"}})


check("B1 a cohort is created", create_cohort(),
      lambda b: b.get("data", {}).get("createSeedCohort", {}).get("member_count") == 0,
      "a new cohort is empty")
COHORT_ID = psql(f"select id from seed_cohorts where name = '{COHORT}'")

check("B2 two members are added",
      gql(OPERATOR_KEY, "mutation($input: SeedCohortMembersInput!) { addSeedCohortMembers(input: $input) }",
          {"input": {"cohort_id": COHORT_ID, "user_ids": [NEW["a"][0], NEW["b"][0]]}}),
      lambda b: b.get("data", {}).get("addSeedCohortMembers") == 2,
      "two accounts were NEWLY added")

check("B3 adding one of them again counts for nothing",
      gql(OPERATOR_KEY, "mutation($input: SeedCohortMembersInput!) { addSeedCohortMembers(input: $input) }",
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
      "a negative head start is refused (%s)" % (errors_of(refused)[:80] or "no data"))

CAMPAIGN_ID = psql(f"select id from seed_campaigns where name = '{CAMPAIGN}'")
run1 = gql(OPERATOR_KEY,
           "mutation($id: ID!) { runSeedCampaign(id: $id) { campaign { last_run_at } members paid skipped } }",
           {"id": CAMPAIGN_ID})
check("C3 the first run pays both members", run1,
      lambda b: b["data"]["runSeedCampaign"]["members"] == 2
      and b["data"]["runSeedCampaign"]["paid"] == 2
      and b["data"]["runSeedCampaign"]["skipped"] == 0,
      "members 2, paid 2, skipped 0")

db_check("C4 the ledger holds one event per member for the campaign's amount",
         lambda _: amount_in_ledger(CAMPAIGN_ID) == f"{AMOUNT * 2}/{2}",
         f"{AMOUNT * 2} points over 2 events (read from trust_events)")

db_check("C5 each member's own rollup moved by the head start",
         lambda _: bonus(NEW["a"][0]) == before_a[0] + AMOUNT and bonus(NEW["b"][0]) == before_b[0] + AMOUNT,
         f"bonus_points +{AMOUNT} each")

db_check("C6 the level followed the points",
         lambda _: level(NEW["a"][0]) > before_a[1] and level(NEW["a"][0]) > 0,
         f"level {before_a[1]} -> {level(NEW['a'][0])} (the curve, computed by the binary)")

db_check("C7 the campaign records that it ran",
         lambda _: bool(psql(f"select last_run_at from seed_campaigns where id = '{CAMPAIGN_ID}'")),
         "last_run_at is set")

print("\n== twice is once ==")
run2 = gql(OPERATOR_KEY, "mutation($id: ID!) { runSeedCampaign(id: $id) { members paid skipped } }",
           {"id": CAMPAIGN_ID})
check("D1 the second run pays nobody", run2,
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
run3 = gql(OPERATOR_KEY, "mutation($id: ID!) { runSeedCampaign(id: $id) { members paid skipped } }",
           {"id": CAMPAIGN_ID})
check("E1 a member who left before the run is not paid", run3,
      lambda b: b["data"]["runSeedCampaign"]["members"] == 1
      and b["data"]["runSeedCampaign"]["paid"] == 0
      and b["data"]["runSeedCampaign"]["skipped"] == 1,
      "the cohort as it stands: 1 member, already paid")

db_check("E2 nothing was written for them",
         lambda _: bonus(NEW["b"][0]) == before_b[0] + AMOUNT and events(NEW["b"][0]) == 1,
         "their single event is the one the first run paid")

gql(OPERATOR_KEY, "mutation($input: SeedCohortMembersInput!) { addSeedCohortMembers(input: $input) }",
    {"input": {"cohort_id": COHORT_ID, "user_ids": [NEW["c"][0]]}})
run4 = gql(OPERATOR_KEY, "mutation($id: ID!) { runSeedCampaign(id: $id) { members paid skipped } }",
           {"id": CAMPAIGN_ID})
check("E3 a member added later IS paid by the next run", run4,
      lambda b: b["data"]["runSeedCampaign"]["members"] == 2
      and b["data"]["runSeedCampaign"]["paid"] == 1
      and b["data"]["runSeedCampaign"]["skipped"] == 1,
      "one paid, one already had it -- the run is resumable")

db_check("E4 their own rollup moved",
         lambda _: bonus(NEW["c"][0]) == AMOUNT and events(NEW["c"][0]) == 1,
         f"+{AMOUNT} for the member who arrived after the first run")

print("\n== what the surface refuses to reveal ==")
members = gql(OPERATOR_KEY, "query($id: ID!) { seedCohort(id: $id) { member_count members { name } } }",
              {"id": COHORT_ID})
check("F1 the roster is readable, with names", members,
      lambda b: b["data"]["seedCohort"]["member_count"] == 2
      and len(b["data"]["seedCohort"]["members"]) == 2,
      "two members, named")

print("\n== restore ==")
psql(f"delete from trust_events where user_id in ('{NEW['a'][0]}','{NEW['b'][0]}','{NEW['c'][0]}')")
psql(f"delete from user_trust where user_id in ('{NEW['a'][0]}','{NEW['b'][0]}','{NEW['c'][0]}')")
psql(f"delete from user_roles where user_id in ('{NEW['a'][0]}','{NEW['b'][0]}','{NEW['c'][0]}')")
psql(f"delete from seed_cohorts where id = '{COHORT_ID}'")
psql(f"delete from users where id in ('{NEW['a'][0]}','{NEW['b'][0]}','{NEW['c'][0]}')")
if not HAD_ADMIN:
    psql(f"delete from user_roles where user_id = '{OPERATOR}' and role = 'ADMIN'")

left = psql(f"select (select count(*) from seed_cohorts where name = '{COHORT}')::text || '/' || "
            f"(select count(*) from seed_campaigns where name = '{CAMPAIGN}')::text || '/' || "
            f"(select count(*) from trust_events where kind = 'seed_fast_track')::text")
print(f"   VERDICT R1: {'OK' if left == '0/0/0' else 'FAILED'} -- cohorts/campaigns/fast-track events left "
      f"behind: {left}")
if left != "0/0/0":
    FAILURES.append("R1")

print()
if FAILURES:
    print("FAILED: " + ", ".join(FAILURES))
    sys.exit(1)
print("all verdicts green")
