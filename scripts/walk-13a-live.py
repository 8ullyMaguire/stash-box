#!/usr/bin/env python3
"""Walk slice 13a (the MCP server) on a LIVE stash-box instance.

Run it ON the host that serves the instance: it launches the built binary over stdio and reads the
instance's Postgres to pick real records, so every tool is exercised against rows that exist.

WHAT THIS WALK IS FOR. The rule of the slice is that the MCP server ADDS NO AUTHORITY: every tool is a
translation to a query that already carries `@hasRole(role: READ)`, and the instance's own authorization is
the only one. So the verdicts are not "the tool returned JSON" -- they are:

  A. THE HANDSHAKE AND THE TOOLS: `initialize` names the revision this server speaks (not the caller's) and
     `tools/list` publishes four tools, each with a schema.
  B. A NOTIFICATION GETS NO ANSWER: one response for two messages, and it belongs to the request. A client
     whose next read is off by a message is broken in a way nothing else would reveal.
  C. AN UNKNOWN ARGUMENT IS REFUSED rather than ignored (`perpage` for `per_page` is the typo a client
     actually makes).
  D. A REAL SEARCH: the term comes from a row in the database, and the scene it names comes back.
  E. A REAL READ, and the ABSENCE: an id that exists returns the record; an id that does not returns
     `null` with NO error -- "it holds no such scene" and "you may not read it" have to be different
     answers, and that difference is what `isError` carries.
  F. IDENTIFICATION, END TO END: the fingerprints of a real scene, read from the database, are submitted,
     and the SCENE that holds them comes back. This is the tool the placement note calls the identification
     half, and it is the one worth walking on live data.
  G. NO KEY, NO READ: with no API key the instance refuses and the tool says so IN THE INSTANCE'S WORDS.
     Authorization is the instance's, which is the whole reason this server is a separate process.

WHAT IT CHANGES: the operator's `api_key`, which is set to a minted token for the run (the server compares
the presented key with the stored one) and restored afterwards. The tools are read-only, so nothing else is
written -- and the restore is verified rather than assumed.

Run:  python3 scripts/walk-13a-live.py [path-to-stash-box-mcp]
"""

import base64
import hashlib
import hmac
import json
import os
import subprocess
import sys
import time

URL = "http://127.0.0.1:9998/graphql"
DB = "stash-box"
CONFIG = "/home/alvaro/stash-box-deploy/config/stash-box-config.yml"
OPERATOR_NAME = os.environ.get("WALK_OPERATOR", "StashBot")

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


def verdict(label, ok, description):
    print(f"   VERDICT {label}: {'OK' if ok else 'FAILED'} -- {description}")
    if not ok:
        FAILURES.append(label)


def session(binary, api_key, messages, timeout=90):
    """Run the server over stdio with these messages and return its response objects.

    The whole batch goes in at once and stdin is closed afterwards: the transport ends at EOF, so this is
    deterministic (no reading of a pipe that the server is still holding open) and it is also the test that
    the server exits when its input ends.
    """
    payload = "".join(json.dumps(m) + "\n" for m in messages)
    env = dict(os.environ)
    if api_key is None:
        # The server falls back to this variable, so an empty environment is how "no key" is expressed.
        env.pop("STASH_BOX_API_KEY", None)
    proc = subprocess.run([binary, "--url", URL] + (["--api-key", api_key] if api_key else []),
                          input=payload, capture_output=True, text=True, timeout=timeout, env=env)
    if proc.returncode not in (0,):
        print(f"   (the server exited {proc.returncode}: {proc.stderr.strip()[:200]})")
    out = []
    for line in proc.stdout.splitlines():
        line = line.strip()
        if not line:
            continue
        try:
            out.append(json.loads(line))
        except json.JSONDecodeError:
            out.append({"unparseable": line[:200]})
    return out


def tool_result(response):
    result = response.get("result") or {}
    blocks = result.get("content") or []
    text = blocks[0].get("text") if blocks else ""
    return result.get("isError", False), (text or ""), response.get("error")


OPERATOR = psql(f"select id from users where name = '{OPERATOR_NAME}'")
if not OPERATOR:
    sys.exit(f"need an account named {OPERATOR_NAME}")

SAVED_KEY = psql(f"select api_key from users where id = '{OPERATOR}'")
KEY = mint(OPERATOR)
psql(f"update users set api_key = '{KEY}' where id = '{OPERATOR}'")

BINARY = sys.argv[1] if len(sys.argv) > 1 else os.path.expanduser("~/stash-box-deploy/bin/stash-box-mcp")
if not os.access(BINARY, os.X_OK):
    sys.exit(f"not executable: {BINARY}")

try:
    print(f"== the handshake and the tool list ({BINARY}) ==")
    responses = session(BINARY, KEY, [
        {"jsonrpc": "2.0", "id": 1, "method": "initialize",
         "params": {"protocolVersion": "1999-01-01", "capabilities": {}, "clientInfo": {"name": "walk-13a", "version": "1"}}},
        {"jsonrpc": "2.0", "method": "notifications/initialized"},
        {"jsonrpc": "2.0", "id": 2, "method": "tools/list"},
    ])
    # EVERY verdict below is reported rather than aborting the walk: the transport is a stream, so a
    # wrong NUMBER of responses is itself the finding and the remaining checks say what else worked.
    if len(responses) != 2:
        verdict("A0 one response per request",
                False, f"expected 2 responses (initialize and tools/list), got {len(responses)}: {str(responses)[:200]}")

    init = (responses[0].get("result") or {}) if responses else {}
    verdict("A1 initialize names the revision this server speaks",
            init.get("protocolVersion") not in (None, "", "1999-01-01"),
            f"protocolVersion={init.get('protocolVersion')} (the client asked for 1999-01-01)")
    verdict("A2 serverInfo names the server and its build",
            (init.get("serverInfo") or {}).get("name") == "stash-box"
            and bool((init.get("serverInfo") or {}).get("version")),
            f"serverInfo={json.dumps(init.get('serverInfo'))}")

    tools = ((responses[1].get("result") or {}).get("tools") or []) if len(responses) > 1 else []
    names = sorted(t.get("name") for t in tools)
    verdict("A3 four tools are published, each with a schema",
            names == ["find_scenes_by_fingerprint", "get_scene", "search_performers", "search_scenes"]
            and all(t.get("inputSchema", {}).get("type") == "object" for t in tools),
            f"tools={names}")

    print("\n== the framing ==")
    verdict("B1 a notification is answered with nothing",
            len(responses) == 2 and responses[1].get("id") == 2,
            "two messages in (one of them a notification), two responses out, the second belonging to the request")

    typo = session(BINARY, KEY, [{
        "jsonrpc": "2.0", "id": 3, "method": "tools/call",
        "params": {"name": "search_scenes", "arguments": {"term": "x", "perpage": 5}}}])
    is_error, text, rpc_error = tool_result(typo[0]) if typo else (False, "", None)
    verdict("B2 an unknown argument is refused",
            is_error and "perpage" in text,
            f"the typo `perpage` is an error rather than being silently dropped: {text[:80]!r}")

    print("\n== a real search, and a real read ==")
    # The fixtures are the DATABASE's: a term taken from a row that exists, so a pass cannot come from an
    # empty instance answering empty results.
    word = psql("select split_part(title, ' ', 1) from scenes where title is not null "
                "and length(split_part(title, ' ', 1)) > 3 limit 1")
    scene_id = psql("select id from scenes where title like '%" + word.replace("'", "''") + "%' limit 1")
    if not word or not scene_id:
        sys.exit("need at least one scene with a title to walk this")

    found = session(BINARY, KEY, [{
        "jsonrpc": "2.0", "id": 4, "method": "tools/call",
        "params": {"name": "search_scenes", "arguments": {"term": word, "per_page": 5}}}])
    is_error, text, rpc_error = tool_result(found[0])
    verdict("D1 a term from the database finds its scene",
            not is_error and rpc_error is None and word.lower() in text.lower(),
            f"searched {word!r}, got {len(text)} bytes of scenes")

    read = session(BINARY, KEY, [{
        "jsonrpc": "2.0", "id": 5, "method": "tools/call",
        "params": {"name": "get_scene", "arguments": {"id": scene_id}}}])
    is_error, text, rpc_error = tool_result(read[0])
    verdict("E1 a scene that exists is read back by id",
            not is_error and scene_id in text,
            f"findScene({scene_id[:8]}…) returned the record")

    missing = session(BINARY, KEY, [{
        "jsonrpc": "2.0", "id": 6, "method": "tools/call",
        "params": {"name": "get_scene", "arguments": {"id": "00000000-0000-0000-0000-000000000000"}}}])
    is_error, text, rpc_error = tool_result(missing[0])
    verdict("E2 an id that names nothing is an answer, not a failure",
            not is_error and rpc_error is None and "null" in text,
            "data.findScene is null with no error -- distinct from a refusal")

    print("\n== identification, on real fingerprints ==")
    # THE JOIN IS THE TABLE (`scene_fingerprints` links a row of `fingerprints` to a scene), and the hash
    # comes back through `to_hex`: the column is a bigint and the API's scalar is a HEX STRING, which is
    # the whole reason the tool refuses a decimal one (a decimal rendering asks for a different
    # fingerprint and answers "never seen").
    prints = psql("select to_hex(f.hash), f.algorithm from fingerprints f "
                  "join scene_fingerprints sf on sf.fingerprint_id = f.id "
                  "where sf.scene_id = '" + scene_id + "' limit 3")
    total = psql("select count(*) from fingerprints")
    if prints:
        entries = [dict(zip(("hash", "algorithm"), line.split("|"))) for line in prints.splitlines()]
        matched = session(BINARY, KEY, [{
            "jsonrpc": "2.0", "id": 7, "method": "tools/call",
            "params": {"name": "find_scenes_by_fingerprint", "arguments": {"fingerprints": entries}}}])
        is_error, text, rpc_error = tool_result(matched[0])
        verdict("F1 the hashes identify the scene that holds them",
                not is_error and rpc_error is None and scene_id in text,
                f"{len(entries)} fingerprint(s) submitted, the scene came back")
    else:
        # SKIPPED WITH ITS PREMISE MEASURED, not with a guess about why: the first version of this walk
        # printed "the scene has no fingerprints" from a query against a column that does not exist, which
        # is a false statement about the instance dressed as a skip.
        print(f"   SKIPPED F1: this instance holds {total} fingerprints in total, so there is nothing to "
              f"identify by. The tool and its translation are covered by the unit tests (the GraphQL side "
              f"stubbed), and this walk says so rather than inventing a fixture")

    print("\n== authorization is the instance's ==")
    anonymous = session(BINARY, None, [{
        "jsonrpc": "2.0", "id": 8, "method": "tools/call",
        "params": {"name": "search_scenes", "arguments": {"term": word}}}])
    is_error, text, rpc_error = tool_result(anonymous[0])
    verdict("G1 with no key the instance refuses, in its own words",
            is_error and ("not authorized" in text.lower() or "401" in text),
            f"the tool reported: {text[:80]!r}")
finally:
    print("\n== restore ==")
    psql(f"update users set api_key = '{SAVED_KEY}' where id = '{OPERATOR}'")
    restored = psql(f"select api_key from users where id = '{OPERATOR}'")
    verdict("R1 the key the walk replaced is back",
            restored == SAVED_KEY,
            "the operator's stored key is byte-identical to the one read before the run")

print()
if FAILURES:
    print("FAILED: " + ", ".join(FAILURES))
    sys.exit(1)
print("all verdicts green")
