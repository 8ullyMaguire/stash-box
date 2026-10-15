#!/usr/bin/env python3
"""Walk phase 16 slice 2 -- the instance metrics -- on a LIVE instance.

Run it ON the host that serves the instance (`ssh thinkcentre 'cd ~/stash-box-deploy && python3
/tmp/walk-16b-live.py'`), after deploying the binary that carries the query. Every verdict is a claim about the
DEPLOYED process and the DEPLOYED database.

WHAT A LIVE WALK ADDS HERE, and why a green suite is not the same thing. The integration test recomputes every
figure inside a package whose database it drops and migrates itself: the tables are EMPTY at the start, so most
of its figures are zero and a zero agrees with a zero. This instance holds over a million scenes and thousands
of tags, so the walk compares NUMBERS THAT COULD BE WRONG -- and it is the only place the query runs against
rows written by years of real use rather than by a fixture.

  A. AN ADMIN READS IT: every figure arrives with its definition, the response names the window, and the
     relations between the figures hold (nobody is active who does not exist; no scene has sources without
     being a scene).
  B. A MEMBER WITHOUT THE ROLE IS REFUSED BY NAME. "Not authorized" is the claim; an empty figure list would
     be a different and much quieter failure, so the refusal is asserted to be a refusal.
  C. EVERY FIGURE RECOMPUTED against the deployed database with SQL written here. The mismatch list is the
     verdict, so a figure that is wrong says which one and by how much.
  D. THE WINDOW IS REAL, and this arm cannot be faked: the walk plants a source checked 60 DAYS AGO and an
     account created 60 DAYS AGO into the live database, re-reads the figures, and requires the TOTALS to move
     while the WINDOWED ones stay still -- then removes them and requires the figures to come back to what
     they were.
  E. IT IS DERIVED, NOT INCREMENTED: the figures move ONLY because rows exist. The walk plants a row and sees
     the count follow, which is the property that lets a reader trust the number without trusting a counter.

WHAT IT CHANGES, AND PUTS BACK. Three rows in the live database (an aged source, an aged account, and the
accounts it mints to sign in), all named with a `walk-16b-` prefix, all deleted by name; the operator's `api_key`
is saved and restored; and the last verdicts re-read the figures and require them to be what they were.

Usage:  python3 walk-16b-live.py [base-url]
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
PREFIX = "walk-16b-"
AGED_DAYS = 60
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


def read_secret():
    with open(CONFIG) as fh:
        for line in fh:
            if line.strip().startswith("jwt_secret"):
                return line.split(":", 1)[1].strip().strip('"').strip("'")
    sys.exit("no jwt_secret in the deployed config")


SECRET = read_secret()


def mint(uid):
    """The token the server will accept for this account: it compares the PRESENTED key with the STORED one.

    Minted once per account and stored, because `iat`/`exp` come from the clock -- calling this twice returns
    two different strings, and the second one is refused by the account that holds the first.
    """
    b64 = lambda raw: base64.urlsafe_b64encode(raw).rstrip(b"=").decode()
    now = int(time.time())
    head = b64(json.dumps({"alg": "HS256", "typ": "JWT"}, separators=(",", ":")).encode())
    pay = b64(json.dumps({"uid": uid, "sub": "APIKey", "iat": now, "exp": now + 3600},
                         separators=(",", ":")).encode())
    sig = b64(hmac.new(SECRET.encode(), f"{head}.{pay}".encode(), hashlib.sha256).digest())
    return f"{head}.{pay}.{sig}"


def gql(key, query):
    body = json.dumps({"query": query}).encode()
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


def check(label, ok, description, why=""):
    if not ok and why:
        print(f"   (why: {why})")
    print(f"   VERDICT {label}: {'OK' if ok else 'FAILED'} -- {description}")
    if not ok:
        FAILURES.append(label)
    return ok


def create_account(suffix, role):
    name = f"{PREFIX}{RUN}-{suffix}"
    psql("insert into users (id, name, password_hash, email, api_key, last_api_call, created_at, updated_at) "
         f"values (gen_random_uuid(), '{name}', 'walk-no-login', '{name}@example.invalid', '', now(), now(), now())")
    uid = psql(f"select id from users where name = '{name}'")
    if not uid:
        sys.exit(f"could not create {name}")
    key = mint(uid)
    psql(f"update users set api_key = '{key}' where id = '{uid}'")
    psql(f"insert into user_roles (user_id, role) values ('{uid}', '{role}')")
    return uid, name, key


def figures(key):
    query = """query { instanceMetrics { generated_at since window_days metrics { key unit definition value } } }"""
    status, body = gql(key, query)
    if status != 200 or not isinstance(body, dict) or body.get("errors"):
        return None, errors_of((status, body)) or f"HTTP {status}"
    data = body.get("data", {}).get("instanceMetrics")
    if not data:
        return None, "no instanceMetrics in the response"
    return data, ""


# The walk's own arithmetic: for every key, the count the deployed database should produce.
EXPECTED = {
    "members_total": """select count(*) from users u where u.name <> '[deleted user]'
        and not exists (select 1 from user_roles r where r.user_id = u.id and r.role = 'BOT')""",
    "members_active_window": """select count(*) from users u where u.name <> '[deleted user]'
        and not exists (select 1 from user_roles r where r.user_id = u.id and r.role = 'BOT')
        and u.last_api_call >= now() - interval '30 days'""",
    "members_created_window": """select count(*) from users u where u.name <> '[deleted user]'
        and not exists (select 1 from user_roles r where r.user_id = u.id and r.role = 'BOT')
        and u.created_at >= now() - interval '30 days'""",
    "members_invited": """select count(*) from users u where u.name <> '[deleted user]'
        and not exists (select 1 from user_roles r where r.user_id = u.id and r.role = 'BOT')
        and u.invited_by is not null""",
    "scenes_total": "select count(*) from scenes s where not s.deleted",
    "scenes_created_window": "select count(*) from scenes s where not s.deleted and s.created_at >= now() - interval '30 days'",
    "scenes_with_sources": """select count(*) from scenes s where not s.deleted
        and exists (select 1 from content_sources cs where cs.entity_type = 'scene' and cs.entity_id = s.id)""",
    "scenes_with_fingerprint": """select count(*) from scenes s where not s.deleted
        and exists (select 1 from scene_fingerprints f where f.scene_id = s.id)""",
    "fingerprints_total": "select count(*) from scene_fingerprints",
    "tags_total": "select count(*) from tags",
    "tag_implications_total": "select count(*) from tag_implications",
    "sources_total": "select count(*) from content_sources",
    "sources_checked_window": "select count(*) from content_sources cs where cs.last_checked >= now() - interval '30 days'",
    "sources_unknown": "select count(*) from content_sources cs where cs.status = 'unknown'",
    "edits_pending": "select count(*) from edits e where e.status = 'PENDING'",
    "edits_decided_window": """select count(*) from edits e
        where e.closed_at is not null and e.closed_at >= now() - interval '30 days'""",
    "identification_candidates_pending": "select count(*) from identification_candidates",
    "invites_issued": "select count(*) from invite_keys",
    "invites_unused": "select count(*) from invite_keys k where k.uses = 0",
}


def main():
    operator = psql(f"select id from users where name = '{OPERATOR_NAME}'")
    if not operator:
        sys.exit(f"need an account named {OPERATOR_NAME}")

    saved_key = psql(f"select api_key from users where id = '{operator}'")
    operator_key = mint(operator)
    psql(f"update users set api_key = '{operator_key}' where id = '{operator}'")

    admin_id, admin_name, admin_key = create_account("admin", "ADMIN")
    member_id, member_name, member_key = create_account("member", "READ")

    planted_source = f"https://{PREFIX}{RUN}.invalid/watch/1"
    old_user = f"{PREFIX}{RUN}-aged"

    def cleanup():
        print("\n== restore ==")
        for name in (admin_name, member_name, old_user):
            psql(f"delete from user_roles where user_id in (select id from users where name = '{name}')")
            psql(f"delete from users where name = '{name}'")
        psql(f"delete from content_sources where url = '{planted_source}'")
        psql(f"update users set api_key = '{saved_key}' where id = '{operator}'")
        left = psql(f"select count(*) from users where name like '{PREFIX}%'")
        left_src = psql(f"select count(*) from content_sources where url like '%{PREFIX}%'")
        restored = psql(f"select api_key from users where id = '{operator}'")
        check("R1", left == "0", f"the {PREFIX} accounts are gone (left: {left})")
        check("R2", left_src == "0", f"the planted source row is gone (left: {left_src})")
        check("R3", restored == saved_key, "the operator's api_key is what it was before the walk")

    try:
        # A. An admin reads it.
        data, why = figures(admin_key)
        if not check("A1", data is not None, "an ADMIN reads the instance metrics", why):
            return
        by_key = {m["key"]: m for m in data["metrics"]}
        check("A2", data["window_days"] == 30,
              f"the response names its window ({data['window_days']} days)")
        check("A3", len(by_key) == len(EXPECTED),
              f"every figure is served once ({len(by_key)} of {len(EXPECTED)})")
        missing = [k for k in EXPECTED if k not in by_key]
        check("A4", not missing, f"no figure is missing from the response{'' if not missing else ': ' + str(missing)}")
        thin = [k for k, m in by_key.items() if len(m.get("definition") or "") <= 30]
        check("A5", not thin, f"every figure arrives with a definition that decides something{'' if not thin else ': ' + str(thin)}")

        # B. A member without the role is refused, BY NAME.
        status, body = gql(member_key, "query { instanceMetrics { metrics { key } } }")
        messages = errors_of((status, body))
        check("B1", "not authorized" in messages,
              "a member without the admin role is refused by name",
              messages or f"HTTP {status}: {str(body)[:200]}")

        # C. Every figure recomputed against the deployed database.
        mismatches = []
        for key, sql in EXPECTED.items():
            actual = psql(sql)
            served = (by_key.get(key) or {}).get("value")
            if actual == "" or served is None:
                mismatches.append(f"{key}: could not compare ({actual!r} vs {served!r})")
            elif int(actual) != int(served):
                mismatches.append(f"{key}: served {served}, the database says {actual}")
        check("C1", not mismatches,
              f"all {len(EXPECTED)} figures agree with the deployed database"
              + ("" if not mismatches else " -- " + "; ".join(mismatches[:6])))

        # C2: the figures are worth something. If the catalogue were empty, the previous verdict would be a
        # comparison of zeros and the walk would be reporting on nothing.
        check("C2", int(by_key["scenes_total"]["value"]) > 1000,
              f"the instance holds a real catalogue ({by_key['scenes_total']['value']} scenes), so these "
              "comparisons are about rows rather than about zeros")
        check("C3", int(by_key["tags_total"]["value"]) > 100,
              f"and a real vocabulary ({by_key['tags_total']['value']} tags)")

        # A6: the relations between the figures, which is what ONE statement buys.
        relations = [
            ("members_active_window", "members_total"),
            ("members_created_window", "members_total"),
            ("scenes_with_sources", "scenes_total"),
            ("scenes_with_fingerprint", "scenes_total"),
            ("sources_checked_window", "sources_total"),
            ("sources_unknown", "sources_total"),
            ("invites_unused", "invites_issued"),
        ]
        broken = [f"{a} ({by_key[a]['value']}) > {b} ({by_key[b]['value']})"
                  for a, b in relations if int(by_key[a]["value"]) > int(by_key[b]["value"])]
        check("A6", not broken,
              "the figures agree with each other, as one snapshot must" + ("" if not broken else ": " + ", ".join(broken)))

        # D. The window is a filter, not a word.
        before = dict(by_key)
        psql("insert into content_sources (entity_type, entity_id, url, status, last_checked) "
             f"values ('scene', gen_random_uuid(), '{planted_source}', 'live', now() - interval '{AGED_DAYS} days')")
        psql("insert into users (id, name, password_hash, email, api_key, last_api_call, created_at, updated_at) "
             f"values (gen_random_uuid(), '{old_user}', 'walk-no-login', '{old_user}@example.invalid', 'x', "
             f"now() - interval '{AGED_DAYS} days', now() - interval '{AGED_DAYS} days', "
             f"now() - interval '{AGED_DAYS} days')")

        aged_data, why = figures(admin_key)
        if not check("D0", aged_data is not None, "the figures can still be read after the plant", why):
            return
        aged = {m["key"]: m for m in aged_data["metrics"]}

        def delta(key):
            return int(aged[key]["value"]) - int(before[key]["value"])

        check("D1", delta("sources_total") == 1,
              f"the source total moved by one for a row planted {AGED_DAYS} days out (moved {delta('sources_total')})")
        check("D2", delta("members_total") == 1,
              f"the member total moved by one (moved {delta('members_total')})")
        check("D3", delta("sources_checked_window") == 0,
              f"a check {AGED_DAYS} days old is NOT inside the window (moved {delta('sources_checked_window')})")
        check("D4", delta("members_created_window") == 0,
              f"an account created {AGED_DAYS} days ago is NOT inside the window (moved {delta('members_created_window')})")
        check("D5", delta("members_active_window") == 0,
              f"an account last seen {AGED_DAYS} days ago is NOT inside the window (moved {delta('members_active_window')})")
        check("D6", delta("sources_unknown") == 0,
              f"a source carrying 'live' is not among the ones no check has looked at (moved {delta('sources_unknown')})")

        # D7: and the figures come back when the rows go.
        psql(f"delete from content_sources where url = '{planted_source}'")
        psql(f"delete from users where name = '{old_user}'")
        back_data, why = figures(admin_key)
        if check("D7", back_data is not None, "the figures can still be read after the cleanup", why):
            back = {m["key"]: m for m in back_data["metrics"]}
            moved = [k for k in EXPECTED if int(back[k]["value"]) != int(before[k]["value"])]
            check("D8", not moved,
                  "removing the planted rows put every figure back where it was"
                  + ("" if not moved else ": still moved " + ", ".join(moved)))
    finally:
        cleanup()

    print()
    if FAILURES:
        print(f"WALK FAILED: {len(FAILURES)} verdict(s): {', '.join(FAILURES)}")
        return 1
    print("WALK PASSED: every verdict above is about the deployed process and the deployed database.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
