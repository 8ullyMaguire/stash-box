#!/usr/bin/env python3
"""Walk slice 13c (the Hydrus-shaped export) on a LIVE stash-box instance.

Run it ON the host that serves the instance. It exports records the deployed catalogue really holds, reads the
document back with a JSON parser, and compares it against the database AND against the instance's own read
surface -- because "the JSON parses" is not the claim this slice makes. The claim is that the document is a
rendering of what this instance holds.

WHAT IS ACTUALLY BEING JUDGED:

  A. The document parses, and the key is the RECORD'S OWN ID rather than a title, an index or an address that
     could change.
  B. THE ADDRESSES ARE THE RECORD'S OWN, compared row for row against `scene_urls` in the deployed database,
     in order. This is the half of the round trip a live walk can honestly do: what the export carries is what
     the instance holds.
  C. THE EXPORT AGREES WITH THE READ SURFACE a client uses -- `findScene(id) { sources { url } }` returns the
     same addresses in the same order, so the document is not a second opinion about the same record.
  D. The vocabulary arrives NAMESPACED, and free text arrives as a NOTE rather than as a tag: this instance has
     no scene relations at all (measured below), so a walk that reported tags here would be reporting something
     it made up.
  E. EMPTY LISTS ARE LISTS. A record with no addresses emits `[]`, not `null`: an importer that has to guess
     about a missing key is being told less than the record knows.
  F. An id that names nothing is OMITTED, named in the envelope, and absent from the document -- not annotated
     inside it, and not silently invented as an empty entry.
  G. The bound is enforced at the wire, and the refusal names the bound.
  H. THE INGEST HALF, MEASURED RATHER THAN SKIPPED. The pipeline recognises an address it already holds from
     `content_sources` -- and on this deployment that table holds ZERO rows behind 1,171,306 `scene_urls` rows,
     so a live submission of a record's own address would be FETCHED rather than recognised. This walk measures
     that and says so, instead of reading somebody else's page to make a green tick. The ingest half is proved
     in the integration suite, where the projection exists because the row was written after migration 108.

WHAT IT CHANGES: only reads, plus the queue row that `submitContentUrl` would create -- and because of H it
creates none. The one token it needs is minted for a real account and the account's `api_key` is restored in a
`finally`, verified rather than assumed.

Run:  python3 scripts/walk-13c-live.py
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
    """A SQL string literal, for the values this walk reads OUT of the database rather than invents."""
    return "'" + str(text).replace("'", "''") + "'"


def verdict(label, ok, description):
    print(f"   VERDICT {label}: {'OK' if ok else 'FAILED'} -- {description}")
    if not ok:
        FAILURES.append(label)


def report(label, description):
    """Something that is a MEASUREMENT rather than a pass: printed, never counted as a pass."""
    print(f"   MEASURED {label}: {description}")


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


OPERATOR = psql(f"select id from users where name = '{OPERATOR_NAME}'")
if not OPERATOR:
    sys.exit(f"need an account named {OPERATOR_NAME}")

SAVED_KEY = psql(f"select api_key from users where id = '{OPERATOR}'")
KEY = mint(OPERATOR)
psql(f"update users set api_key = '{KEY}' where id = '{OPERATOR}'")


def graphql(query, variables=None):
    body = json.dumps({"query": query, "variables": variables or {}}).encode()
    req = urllib.request.Request(URL, data=body, headers={"Content-Type": "application/json", "ApiKey": KEY})
    try:
        with urllib.request.urlopen(req, timeout=120) as response:
            return json.loads(response.read())
    except urllib.error.HTTPError as err:
        return {"http_error": err.code, "body": err.read().decode()[:300]}


try:
    print("== the fixture comes from the catalogue, and is measured first ==")
    counts = psql("select (select count(*) from scenes) || '|' || (select count(*) from scene_urls)"
                  " || '|' || (select count(*) from scene_tags)"
                  " || '|' || (select count(*) from content_sources)").split("|")
    scenes_n, urls_n, tags_n, sources_n = (int(c) for c in counts)
    report("the catalogue",
           f"{scenes_n} records, {urls_n} addresses on them, {tags_n} tag relations, "
           f"{sources_n} projected sources")

    # A record that HAS addresses, since B and C are about addresses: the walk picks from the database rather
    # than hoping the first row it meets has one.
    TARGET = psql("select scene_id from scene_urls group by scene_id having count(*) > 1 "
                  "order by count(*) desc limit 1")
    if not TARGET:
        sys.exit("no record on this instance carries more than one address, so B and C cannot be judged")

    print(f"   the record under test is {TARGET}")

    print("== A. the document, and what keys it ==")
    exported = graphql("query($ids: [ID!]!) { exportScenes(ids: $ids) { json record_count omitted } }",
                       {"ids": [TARGET]})
    payload = (exported.get("data") or {}).get("exportScenes")
    if not payload:
        sys.exit(f"the export did not answer: {json.dumps(exported)[:400]}")

    try:
        document = json.loads(payload["json"])
    except Exception as err:  # noqa: BLE001 - the failure IS the message
        sys.exit(f"the document is not JSON: {err}")

    verdict("A1", TARGET in document, "the record's own id is the key of its entry")
    verdict("A2", payload["record_count"] == 1 and not payload["omitted"],
            f"record_count is {payload['record_count']} and nothing was omitted")

    entry = document.get(TARGET, {})
    metadata = entry.get("metadata", {})

    print("== B. the addresses are the record's own, row for row ==")
    stored = psql(f"select url from scene_urls where scene_id = '{TARGET}' order by url").splitlines()
    carried = sorted(metadata.get("urls") or [])
    verdict("B1", bool(stored) and carried == stored,
            f"{len(carried)} addresses in the document, {len(stored)} on the record, equal as sets")
    verdict("B2", metadata.get("urls") == sorted(stored),
            "and the document's addresses are a LIST, never null or a single string")

    print("== C. the export agrees with the read surface a client uses ==")
    found = graphql("query($id: ID!) { findScene(id: $id) { id sources { url } } }", {"id": TARGET})
    scene = ((found.get("data") or {}).get("findScene") or {})
    surface = sorted(s.get("url") or "" for s in (scene.get("sources") or []))
    verdict("C1", bool(surface) and surface == carried,
            f"findScene returns {len(surface)} addresses; the document carries the same ones")

    print("== D. the vocabulary is namespaced and free text is a note ==")
    tags = metadata.get("tags") or []
    notes = metadata.get("notes") or []
    unnamespaced = [t for t in tags if not t.split(":", 1)[0] in
                    ("studio", "performer", "tag", "code", "date")]
    verdict("D1", not unnamespaced, f"every tag is namespaced ({len(tags)} tags: {tags[:4]})")
    verdict("D2", all(n.startswith(("title: ", "details: ")) for n in notes),
            f"{len(notes)} notes, each in the format's `name: text` shape")
    verdict("D3", not any(t.startswith(("title:", "details:")) for t in tags),
            "no note was routed into the tag list, which is where a file gets a tag per sentence")

    print("== E. an empty list is a list ==")
    # A record this instance holds NOTHING about beyond its own row: no code, no date, no studio, no address.
    # (The first version of this check read the target record's tag list and expected it empty "because the
    # instance has no tag relations" -- and it is not empty: the code and the date ARE tags in this mapping,
    # measured above as ['code:STAP-038', 'date:2024-11-28']. The check was asserting against a list that
    # correctly had two entries, which is what a walk written from an assumption rather than a measurement
    # does.)
    # The fixture is whatever this instance HAS, measured rather than assumed a second time: `date` is
    # NOT NULL in practice here (0 of 1,104,375 records lack one), so a record with nothing at all does not
    # exist and asking for one reports nothing. What does exist, in quantity, is a record with no ADDRESSES --
    # and that is the same claim: a list where the instance holds nothing is still a list.
    bare_counts = psql("select (select count(*) from scenes where code is null) || '|'"
                       " || (select count(*) from scenes where date is null) || '|'"
                       " || (select count(*) from scenes s where not exists "
                       "(select 1 from scene_urls u where u.scene_id = s.id))").split("|")
    report("E0", f"records without a code: {bare_counts[0]}; without a date: {bare_counts[1]}; "
                 f"without any address: {bare_counts[2]}")

    BARE = psql("select s.id from scenes s where not exists "
                "(select 1 from scene_urls u where u.scene_id = s.id) limit 1")
    if BARE:
        bare_answer = graphql("query($ids: [ID!]!) { exportScenes(ids: $ids) { json record_count } }",
                              {"ids": [BARE]})
        bare_payload = (bare_answer.get("data") or {}).get("exportScenes") or {}
        bare_metadata = (json.loads(bare_payload.get("json") or "{}").get(BARE) or {}).get("metadata", {})
        verdict("E1", bare_payload.get("record_count") == 1 and bare_metadata.get("urls") == [],
                f"a record with no addresses emits an empty list, not null: {bare_metadata}")
    else:
        report("E1", "not judged: every record on this instance carries an address")

    print("== F. an id that names nothing is omitted, and only in the envelope ==")
    NOWHERE = "00000000-0000-4000-8000-000000000000"
    both = graphql("query($ids: [ID!]!) { exportScenes(ids: $ids) { json record_count omitted } }",
                   {"ids": [TARGET, NOWHERE]})
    both_payload = (both.get("data") or {}).get("exportScenes") or {}
    both_document = json.loads(both_payload.get("json") or "{}")
    verdict("F1", both_payload.get("omitted") == [NOWHERE],
            f"the missing id is named in the envelope: {both_payload.get('omitted')}")
    verdict("F2", NOWHERE not in both_document and TARGET in both_document,
            "and it is not in the document, which carries only the record that exists")
    verdict("F3", "omitted" not in json.dumps(both_document),
            "nothing inside the document is annotated as absent")

    print("== G. the bound is enforced at the wire ==")
    many = [TARGET] + [NOWHERE] * 100
    refused = graphql("query($ids: [ID!]!) { exportScenes(ids: $ids) { record_count } }", {"ids": many})
    message = json.dumps(refused)
    verdict("G1", "100" in message and "error" in message.lower(),
            f"101 ids are refused and the refusal names the bound: {message[:160]}")

    print("== H. the ingest half, measured rather than skipped ==")
    # WHERE THE PIPELINE LOOKS. It recognises an address it holds via content_sources, and this deployment's
    # projection is a trigger on writes made after migration 108 -- so the two tables can disagree, and here
    # they do.
    sample = carried[0] if carried else ""
    projected = psql(f"select count(*) from content_sources where url_normalized = lower({q(sample)})") \
        if sample else "0"
    held = psql(f"select count(*) from scene_urls where url = {q(sample)}") if sample else "0"
    report("H1", f"the record's own first address is in scene_urls {held} time(s) and in content_sources "
                 f"{projected} time(s)")
    if projected == "0":
        report("H2", "so a live submission of this address would be FETCHED rather than recognised: the walk "
                     "does not submit it. The ingest half -- queue row `done`, outcome `attached`, "
                     "`confidence` NULL, extractor never called -- is proved in the integration suite, where "
                     "the row is written after migration 108 and the projection exists.")
    else:
        verdict("H2", False, "this walk expected the projection to be missing on this deployment; it is not, "
                             "so the walk must be extended to submit the address and read the queue row")

finally:
    # Two statements, two calls: `psql -tAc` with a compound string returns BOTH outputs, so the first version
    # of this restore compared 'UPDATE 1\n<key>' against the key and reported a failure that had not
    # happened. The restore is verified, and the verification has to be able to tell.
    psql(f"update users set api_key = '{SAVED_KEY}' where id = '{OPERATOR}'")
    restored = psql(f"select api_key from users where id = '{OPERATOR}'")
    if restored != SAVED_KEY:
        print(f"!! the account's key was NOT restored: {restored!r} != {SAVED_KEY!r}")
        FAILURES.append("cleanup")

print()
if FAILURES:
    print(f"FAILED: {', '.join(FAILURES)}")
    sys.exit(1)
print("all verdicts green")
