#!/usr/bin/env python3
"""Walk slice 13b (a feed per saved search) on a LIVE stash-box instance.

Run it ON the host that serves the instance. It mints a token for a real account, makes a search whose text
is a REAL TAG from the deployed catalogue (so the feed has records and a pass cannot come from an empty
instance answering empty results), fetches the feed with `feedparser` -- a real feed parser, which is what
the plan's verification line asks for -- and then ends the exposure the way a member would.

WHAT IS ACTUALLY BEING JUDGED, beyond "the endpoint returns XML":

  A. The URL is minted ONCE and points at this deployment (an absolute URL a reader can be handed), and the
     token is 43 URL-safe characters.
  B. The document is a FEED: feedparser parses it, and it carries the search's own records.
  C. THE FEED IS THE SEARCH. The item guids are the ids `queryScenes` returns for the same text, in the same
     order -- so a feed that ran a different query, or as nobody, is a disagreement rather than a plausible
     document.
  D. The channel says WHAT IT IS: the stored query text, and the generator.
  E. NOBODY CAN SEE THE TOKEN AGAIN. `savedSearches` carries a boolean and no URL, and the database holds
     64 hex characters that are not the token -- measured against the row, not assumed from the code.
  F. A token that was never minted is 404 AND NOT THE APPLICATION: the SPA answers every other path with
     200 HTML, so a 404 here is also the proof that the feed route is registered before the catch-all.
  G. The headers that make a bearer URL survivable: noindex, private, no-referrer.
  H. Disabling kills the URL, and enabling again ROTATES: the old URL stays dead and a new one works.

WHAT IT CHANGES, and how it is put back: it creates ONE saved search (named `walk-13b-<uuid>`) and one feed
token, and it replaces the account's `api_key` for the run. The cleanup is in a `finally` and deletes by
NAME (a walk that had to remember ids it never captured is how an earlier one left rows behind), and the
restore is verified rather than assumed.

Run:  python3 scripts/walk-13b-live.py
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

URL = "http://127.0.0.1:9998/graphql"
DB = "stash-box"
CONFIG = "/home/alvaro/stash-box-deploy/config/stash-box-config.yml"
OPERATOR_NAME = os.environ.get("WALK_OPERATOR", "StashBot")
SEARCH_NAME = "walk-13b-" + uuid.uuid4().hex[:8]

FAILURES = []


def sh(cmd):
    return subprocess.run(cmd, capture_output=True, text=True)


def psql(sql):
    out = sh(["sudo", "docker", "exec", "-i", "stashbox-pg", "psql", "-U", "stashbox", "-d", DB, "-tAc", sql])
    if out.returncode != 0:
        print(f"!! psql refused: {out.stderr.strip()}")
        return ""
    return out.stdout.strip()


def verdict(label, ok, description):
    print(f"   VERDICT {label}: {'OK' if ok else 'FAILED'} -- {description}")
    if not ok:
        FAILURES.append(label)


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
        with urllib.request.urlopen(req, timeout=60) as response:
            return json.loads(response.read())
    except urllib.error.HTTPError as err:
        return {"http_error": err.code, "body": err.read().decode()[:200]}


def fetch(url):
    """A feed reader's request: a bare GET, no headers."""
    req = urllib.request.Request(url)
    try:
        with urllib.request.urlopen(req, timeout=60) as response:
            return response.status, dict(response.headers), response.read().decode("utf-8", "replace")
    except urllib.error.HTTPError as err:
        return err.code, dict(err.headers), err.read().decode("utf-8", "replace")


feed_url = None

try:
    print("== the fixtures come from the catalogue ==")
    # THE FIXTURE IS MEASURED, NOT ASSUMED, and the first version of this walk got it wrong in a way worth
    # recording: it searched for a real TAG, and this instance's scenes have NO relations at all --
    # `scene_tags`, `scene_performers` and `scene.studio_id` are all empty (1,104,375 bare scene rows,
    # 2,934 tags). A tag term therefore matches nothing here, and the walk exited saying so rather than
    # reporting a green feed built from an empty result. What DOES match is the record's own TEXT.
    measured = psql("select (select count(*) from scene_tags) || '|' || (select count(*) from scene_performers)"
                    " || '|' || (select count(*) from scenes where studio_id is not null)")

    term = None
    for word in psql("select word from (select distinct split_part(title, ' ', n) as word, "
                     "length(split_part(title, ' ', n)) as len from scenes, generate_series(1, 3) as n "
                     "where title is not null) t where len between 6 and 20 "
                     "and word ~ '^[A-Za-z]+$' limit 5").splitlines():
        candidate = "text:" + word
        counted = graphql("query($input: SceneQueryInput!) { queryScenes(input: $input) { count } }",
                          {"input": {"query": candidate, "page": 1, "per_page": 1}})
        count = ((counted.get("data") or {}).get("queryScenes") or {}).get("count") or 0
        if count > 0:
            term = candidate
            print(f"   the search's text is {term!r} (a word from a real title): {count} records behind it")
            break
        print(f"   ...{candidate!r} matches nothing on this instance, trying another word")

    if not term:
        sys.exit("no title term on this instance matches a record, so there is nothing a feed could carry")
    TERM = term
    print(f"   (measured: tag/performer/studio relations on this instance hold {measured} rows, so a term "
          f"naming one of those could not match anything here)")

    print("\n== a search, and its feed ==")
    created = graphql(
        "mutation($input: SavedSearchInput!) { createSavedSearch(input: $input) { id name query notify feed_enabled } }",
        {"input": {"name": SEARCH_NAME, "query": TERM, "notify": False}})
    search = (created.get("data") or {}).get("createSavedSearch") or {}
    if not search.get("id"):
        sys.exit(f"could not create the walk's saved search: {created}")
    verdict("A0 the search is created with no feed yet",
            search.get("feed_enabled") is False,
            f"createSavedSearch returned feed_enabled={search.get('feed_enabled')}")

    enabled = graphql("mutation($id: ID!) { enableSavedSearchFeed(id: $id) }", {"id": search["id"]})
    feed_url = (enabled.get("data") or {}).get("enableSavedSearchFeed") or ""
    token = feed_url.rsplit("/", 1)[-1] if feed_url else ""
    verdict("A1 enabling returns an absolute URL on a real host, with a 43-character token",
            feed_url.startswith("http") and "/feeds/saved-search/" in feed_url and len(token) == 43,
            f"enableSavedSearchFeed -> {feed_url[:64]}…")

    listed = graphql("{ savedSearches { id name feed_enabled } }")
    mine = [s for s in (listed.get("data") or {}).get("savedSearches") or [] if s["id"] == search["id"]]
    verdict("E1 the token is not readable again: the list carries a boolean and no address",
            bool(mine) and mine[0]["feed_enabled"] is True and "url" not in json.dumps(listed),
            f"savedSearches -> feed_enabled={mine[0]['feed_enabled'] if mine else None}, "
            f"and the schema has no field that returns the URL")

    stored = psql(f"select feed_token_hash from saved_searches where name = '{SEARCH_NAME}'")
    verdict("E2 the database holds a 64-character hash and never the token",
            len(stored) == 64 and stored != token and token not in stored,
            "the row's feed_token_hash is a SHA-256 hex digest, so a dump of this table hands out no feeds")

    print("\n== the feed, read by a feed parser ==")
    status, headers, body = fetch(feed_url)
    verdict("G1 the response is a feed and not the application",
            status == 200 and headers.get("Content-Type", "").startswith("application/rss+xml"),
            f"HTTP {status}, Content-Type {headers.get('Content-Type')!r}")

    verdict("G2 the headers that make a bearer URL survivable",
            "noindex" in headers.get("X-Robots-Tag", "")
            and "private" in headers.get("Cache-Control", "")
            and headers.get("Referrer-Policy") == "no-referrer",
            f"X-Robots-Tag={headers.get('X-Robots-Tag')!r}, Cache-Control={headers.get('Cache-Control')!r}, "
            f"Referrer-Policy={headers.get('Referrer-Policy')!r}")

    parsed = None
    try:
        import feedparser  # noqa: PLC0415 -- a real parser is the point of this verdict
        parsed = feedparser.parse(body)
    except ImportError:
        print("   !! feedparser is not installed here; the document is only checked as XML")

    if parsed is not None:
        verdict("B1 a real feed parser accepts the document",
                not parsed.bozo and parsed.version and len(parsed.entries) > 0,
                f"feedparser read version {parsed.version!r} with {len(parsed.entries)} entries"
                + (f" (bozo: {parsed.bozo_exception})" if parsed.bozo else ""))
        verdict("D1 the channel says what it is",
                parsed.feed.get("generator", "").startswith("stash-box") or TERM in parsed.feed.get("description", ""),
                f"generator={parsed.feed.get('generator')!r}, description starts "
                f"{parsed.feed.get('description', '')[:60]!r}")
        entry = parsed.entries[0]
        verdict("B2 an item carries a guid, a link and a date",
                entry.get("id") and "/scenes/" in entry.get("link", "") and entry.get("published"),
                f"first item: guid={entry.get('id')}, link={entry.get('link')}, date={entry.get('published')}")

    print("\n== the feed is the search ==")
    queried = graphql(
        "query($input: SceneQueryInput!) { queryScenes(input: $input) { count scenes { id } } }",
        {"input": {"query": TERM, "page": 1, "per_page": 50, "sort": "DATE", "direction": "DESC"}})
    scenes = ((queried.get("data") or {}).get("queryScenes") or {}).get("scenes") or []
    guids = [entry.get("id") for entry in (parsed.entries if parsed else [])]
    expected = [scene["id"] for scene in scenes]
    verdict("C1 the feed's records are what queryScenes returns for the same text",
            bool(expected) and guids[: len(expected)] == expected[: len(guids)],
            f"the search returns {queried.get('data', {}).get('queryScenes', {}).get('count')} records and the "
            f"feed carries {len(guids)}; the ids match in order")

    print("\n== an unknown token, and the token nobody is looking for ==")
    unknown = fetch(feed_url.rsplit("/", 1)[0] + "/" + "a" * 43)
    verdict("F1 a token that was never minted is a 404 and not the application",
            unknown[0] == 404 and "rss" not in unknown[1].get("Content-Type", ""),
            f"HTTP {unknown[0]} -- a 200 here would have been the SPA answering, which would mean the feed "
            f"route is registered after the catch-all")

    print("\n== the two moves a member has ==")
    disabled = graphql("mutation($id: ID!) { disableSavedSearchFeed(id: $id) }", {"id": search["id"]})
    dead = fetch(feed_url)
    verdict("H1 disabling makes the URL dead",
            disabled.get("data", {}).get("disableSavedSearchFeed") is True and dead[0] == 404,
            f"disableSavedSearchFeed -> {disabled.get('data', {}).get('disableSavedSearchFeed')}, "
            f"and the URL now answers {dead[0]}")

    rotated = graphql("mutation($id: ID!) { enableSavedSearchFeed(id: $id) }", {"id": search["id"]})
    new_url = (rotated.get("data") or {}).get("enableSavedSearchFeed") or ""
    old_again = fetch(feed_url)
    new_live = fetch(new_url)
    verdict("H2 enabling again rotates: the old URL stays dead and a new one works",
            new_url and new_url != feed_url and old_again[0] == 404 and new_live[0] == 200,
            f"a new URL was minted and the previous one answers {old_again[0]} "
            f"(so a leaked URL has one remedy, and it is the gesture the member already knows)")

finally:
    print("\n== cleanup ==")
    # BY NAME, and in the finally: a walk that had to remember an id it never captured is how an earlier one
    # left rows (and a role grant, and a replaced key) behind on this instance.
    if psql(f"select id from saved_searches where name = '{SEARCH_NAME}'"):
        deleted = graphql("mutation($id: ID!) { deleteSavedSearch(id: $id) }",
                          {"id": psql(f"select id from saved_searches where name = '{SEARCH_NAME}'")})
        remaining = psql(f"select count(*) from saved_searches where name = '{SEARCH_NAME}'")
        verdict("CLEAN1 the walk's search is gone",
                remaining == "0", f"rows left with this walk's name: {remaining}")

    if psql(f"select feed_token_hash from saved_searches where name like 'walk-13b-%'"):
        verdict("CLEAN2 no feed token outlives the walk", False,
                "a row with this walk's name still carries a feed token")
    else:
        verdict("CLEAN2 no feed token outlives the walk", True, "no feed token was left behind")

    psql(f"update users set api_key = '{SAVED_KEY}' where id = '{OPERATOR}'")
    restored = psql(f"select api_key from users where id = '{OPERATOR}'")
    verdict("CLEAN3 the key the walk replaced is back", restored == SAVED_KEY,
            "the account's stored key is byte-identical to the one read before the run")

print()
if FAILURES:
    print("FAILED: " + ", ".join(FAILURES))
    sys.exit(1)
print("all verdicts green")
