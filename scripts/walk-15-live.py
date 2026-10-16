#!/usr/bin/env python3
"""Walk phase 15 — the monetization posture — on a LIVE instance, in BOTH positions of the switch.

Every verdict here is a claim about a RUNNING PROCESS and a real database, not about a test fixture. The
integration suite measures the same rules in-process; this walk is what shows they survive the config file,
the startup validation, the HTTP surface and the headers a client actually receives.

WHY IT STARTS TWO PROCESSES. The whole phase turns on one sentence: turning monetization ON must not change
anything a member can do (§18.2), while it must change exactly one thing (§18.1's higher API limit). A single
running instance cannot show both halves of that, because the switch is read once at startup from the config
file. So the walk starts the SAME BINARY twice against the SAME DATABASE -- once as it ships (off), once with
the premium tier sold to a real member -- and compares.

  A. OFF, as deployed. The page says the instance charges for nothing and names `monetization_enabled`; the
     JSON serves the same struct with all seven of §18.2's capabilities and at least one surface each, no
     member id anywhere in it, and both limits stated; every credential -- anonymous, a member's API key --
     is counted against the FREE limit, because nobody is premium while the switch is off.
  B. ON, with the premium API tier sold to that same member. The page says it charges; the JSON says one
     comp and still does not publish the id; THAT member's key is counted against the premium limit, an
     ungranted member's key is NOT, and neither is an anonymous caller's; and every §18.2 call that answered
     in A answers in B WITH THE SAME PAYLOAD -- the leak test, on the wire.

WHAT IT CHANGES, AND PUTS BACK. It writes two temporary config files, reads the database, and starts and stops
two servers on loopback ports. It writes NOTHING to the database: the member it comps is one that already
exists, and its API key is the one the instance itself minted and stored (which is also why the walk does not
mint a key of its own -- the server compares the presented key with the stored one, so a key signed by the
walk would be refused and the walk would measure the wrong thing).

Usage:  python3 scripts/walk-15-live.py [--db DSN] [--port 9997] [--binary ./stash-box]
"""

import argparse
import json
import os
import re
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

FAILURES = []
NOTES = []


def check(name, ok, detail=""):
    print(f"  {'PASS' if ok else 'FAIL'}  {name}" + (f" -- {detail}" if detail else ""))
    if not ok:
        FAILURES.append(f"{name}: {detail}")


def note(text):
    print(f"        {text}")
    NOTES.append(text)


PG = {"host": "127.0.0.1", "port": "55434", "user": "postgres"}


def psql(dsn, sql):
    """One query, one column, as text. The walk reads the database rather than inventing members.

    The host and port are passed explicitly: a bare database name makes psql look for a local socket, which is
    a different cluster on this machine (and, the first time this walk ran, no cluster at all).
    """
    env = dict(os.environ, PGPASSWORD=os.environ.get("WALK_PGPASSWORD", "smoke_pw"))
    out = subprocess.run(
        ["psql", "-h", PG["host"], "-p", PG["port"], "-U", PG["user"], dsn, "-tAc", sql],
        capture_output=True, text=True, env=env, timeout=60,
    )
    if out.returncode != 0:
        raise RuntimeError(f"psql failed: {out.stderr.strip()[:400]}")
    return out.stdout.strip()


def header(headers, name):
    """Read one header case-insensitively.

    This is not politeness: Go writes the canonical spelling of a key it has set, so the wire carries
    `X-Ratelimit-Limit` where the source and these scripts say `X-RateLimit-Limit`. HTTP header names are
    case-insensitive by definition, so a client that compares them as strings is the thing that is wrong --
    and the first version of this walk reported five failures because of it, all of them its own.
    """
    wanted = name.lower()
    for key, value in headers.items():
        if key.lower() == wanted:
            return value
    return None


def get(url, headers=None, timeout=20):
    req = urllib.request.Request(url, headers=headers or {})
    with urllib.request.urlopen(req, timeout=timeout) as res:
        return res.status, dict(res.headers), res.read().decode("utf-8", "replace")


def post(url, body, headers=None, timeout=20):
    data = json.dumps(body).encode()
    hdrs = {"Content-Type": "application/json"}
    hdrs.update(headers or {})
    req = urllib.request.Request(url, data=data, headers=hdrs, method="POST")
    try:
        with urllib.request.urlopen(req, timeout=timeout) as res:
            return res.status, dict(res.headers), json.loads(res.read().decode())
    except urllib.error.HTTPError as e:
        return e.code, dict(e.headers), json.loads(e.read().decode() or "{}")


def jwt_secret_from_like(path):
    """Read the signing key out of a config the database's members were created with.

    The walk presents the API KEY THE INSTANCE ITSELF MINTED AND STORED, because the server compares the
    presented key with the stored one -- so the walk has to sign nothing and cannot sign anything. What it does
    need is the same `jwt_secret_key` those keys were minted under: started with a different key, the instance
    answers 401 to a valid stored key and the walk measures the wrong thing (which is exactly what its first
    run did, reporting five failures that were all its own configuration).
    """
    if not path or not os.path.exists(path):
        return None
    m = re.search(r"^\s*jwt_secret_key:\s*(\S+)", open(path).read(), re.M)
    return m.group(1) if m else None


def write_config(path, port, dsn, jwt_secret=None, monetization=None):
    lines = [f"database: {dsn}", "host: 127.0.0.1", f"port: {port}", ""]
    if jwt_secret:
        lines.append(f"jwt_secret_key: {jwt_secret}")
        lines.append("")
    if monetization:
        lines += [
            "monetization_enabled: true",
            "monetization_streams:",
            f"  - {monetization['stream']}",
            "monetization_premium_members:",
            f"  - {monetization['member']}",
            f"monetization_premium_requests_per_minute: {monetization['per_minute']}",
            f"monetization_premium_burst: {monetization['burst']}",
            "",
        ]
    else:
        lines += ["monetization_enabled: false", ""]
    with open(path, "w") as f:
        f.write("\n".join(lines))


def start(binary, config, log):
    handle = open(log, "w")
    proc = subprocess.Popen([binary, "--config_file", config], cwd=REPO, stdout=handle, stderr=handle)
    return proc, handle


def wait_ready(base, proc, seconds=60):
    deadline = time.time() + seconds
    while time.time() < deadline:
        if proc.poll() is not None:
            return False
        try:
            status, _, _ = get(f"{base}/monetization")
            if status == 200:
                return True
        except Exception:
            time.sleep(0.5)
    return False


def graphql(base, query, api_key=None):
    headers = {"ApiKey": api_key} if api_key else {}
    return post(f"{base}/graphql", {"query": query}, headers)


# The two §18.2 calls the walk makes in both positions. They are chosen because they answer without any
# fixture: a search term nothing matches and a member's own list set are both stable, and a stable answer is
# what makes "the payload is the same in both modes" a comparable claim.
PROBES = {
    "searchScenes": '{ searchScenes(term: "walk-15-nothing-matches-this") { count } }',
    "lists": "{ lists { id } }",
}


def walk(base, api_key, label, expect_charging):
    print(f"\n-- {label} ({base})")

    status, _, page = get(f"{base}/monetization")
    check(f"{label}: GET /monetization is public", status == 200, f"HTTP {status}")
    check(f"{label}: the page states the posture it is in",
          ("charges</strong> for the streams" in page) == expect_charging,
          f"expected charging={expect_charging}")
    check(f"{label}: the page names the key that decides it", "monetization_enabled" in page)
    check(f"{label}: the page is not noindex", "noindex" not in page)
    check(f"{label}: the page does not repeat the other posture's sentence",
          ("charges for nothing" in page) != expect_charging)

    status, _, body = get(f"{base}/monetization/json")
    check(f"{label}: /monetization/json serves JSON", status == 200 and body.strip().startswith("{"))
    posture = json.loads(body)
    check(f"{label}: the JSON says what the page says",
          posture["monetization_enabled"] == expect_charging,
          f"monetization_enabled={posture['monetization_enabled']}")

    free = posture["always_free"]
    check(f"{label}: §18.2's seven capabilities are published", len(free) == 7, f"{len(free)} published")
    named = ", ".join(c["name"] for c in free)
    note(f"capabilities: {named}")
    without_surface = [c["name"] for c in free if not c.get("surfaces")]
    check(f"{label}: every capability names a surface that provides it", not without_surface,
          ", ".join(without_surface))
    words = [c for c in free if not c.get("words")]
    check(f"{label}: every capability carries §18.2's own words", not words,
          ", ".join(c["name"] for c in words))

    # Who pays is the operator's business and not the page's: the count is published, the ids are not.
    uuids = re.findall(r"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}", body)
    check(f"{label}: the JSON publishes no member id", not uuids, f"{len(uuids)} uuid(s) in the body")
    note(f"premium members: {posture['premium_members']}")

    # The headers on a request nobody authenticated: the free limit, in both positions of the switch.
    status, headers, _ = graphql(base, "{ __typename }")
    limit_anon = header(headers, "X-RateLimit-Limit")
    check(f"{label}: an anonymous request states the free limit",
          limit_anon == str(posture["free_limit"]["requests_per_minute"]),
          f"X-RateLimit-Limit={limit_anon}")

    # And the member's own key.
    status, headers, _ = graphql(base, "{ __typename }", api_key)
    limit_member = header(headers, "X-RateLimit-Limit")
    check(f"{label}: the presenting member is counted against a stated limit", bool(limit_member),
          f"X-RateLimit-Limit={limit_member}")

    payloads = {}
    for name, query in PROBES.items():
        status, _, payload = graphql(base, query, api_key)
        ok = status == 200 and not payload.get("errors")
        check(f"{label}: {name} answers", ok, json.dumps(payload)[:160])
        payloads[name] = json.dumps(payload.get("data"), sort_keys=True)
    return limit_member, payloads


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--db", default="postgres:smoke_pw@127.0.0.1:55434/stash-box-e2e?sslmode=disable",
                    help="the application's DSN (the form the config file takes)")
    ap.add_argument("--psql-db", default="stash-box-e2e")
    ap.add_argument("--pg-host", default="127.0.0.1")
    ap.add_argument("--pg-port", default="55434")
    ap.add_argument("--pg-user", default="postgres")
    ap.add_argument("--port", type=int, default=9997)
    ap.add_argument("--binary", default=os.path.join(REPO, "stash-box"))
    ap.add_argument("--like", default=os.path.join(REPO, "e2e", "config", "stash-box-e2e.yml"),
                    help="a config whose jwt_secret_key the database's stored API keys were minted under")
    args = ap.parse_args()
    PG.update(host=args.pg_host, port=args.pg_port, user=args.pg_user)

    if not os.path.exists(args.binary):
        print(f"building {args.binary} ...")
        subprocess.run(["go", "build", "-o", args.binary, "./cmd/stash-box"], cwd=REPO, check=True)

    # The member the walk comps is one that already exists, and its key is the one the instance minted: the
    # server compares the presented key with the stored one, so the walk must not invent either.
    member = psql(args.psql_db, "select id from users order by created_at limit 1")
    member_key = psql(args.psql_db, "select api_key from users order by created_at limit 1")
    other_key = psql(args.psql_db,
                     "select api_key from users where api_key <> '' and id <> (select id from users order by "
                     "created_at limit 1) order by created_at limit 1")
    if not member or not member_key:
        print("no member with an API key in this database: the walk needs one to comp")
        return 2
    print(f"comping member {member} (its stored key is the credential the server compares against)")
    if not other_key:
        print("WARNING: no second member to prove the ungranted case; that check will be skipped")

    tmp = tempfile.mkdtemp(prefix="walk-15-")
    off_cfg = os.path.join(tmp, "off.yml")
    on_cfg = os.path.join(tmp, "on.yml")
    jwt_secret = jwt_secret_from_like(args.like)
    if not jwt_secret:
        print(f"no jwt_secret_key in {args.like}: a stored API key will not authenticate. Pass --like <config>.")
    else:
        note(f"signing key taken from {os.path.relpath(args.like, REPO)} (the members' stored keys were minted under it)")

    write_config(off_cfg, args.port, args.db, jwt_secret)
    # The stream's NAME matters and the walk is where it was caught: the feature on this tree is
    # `premium_api_tier` and the stream that sells it is `premium_subscription`. A config naming the feature
    # instead of the stream is refused at startup, by name, with the list.
    write_config(on_cfg, args.port + 1, args.db, jwt_secret, {
        "stream": "premium_subscription", "member": member, "per_minute": 1200, "burst": 240,
    })
    note(f"config files: {off_cfg}, {on_cfg}")

    procs = []
    try:
        # ---- A: as it ships -------------------------------------------------------------------------------
        print("\n=== A. MONETIZATION OFF (the shipped posture) ===")
        proc, handle = start(args.binary, off_cfg, os.path.join(tmp, "off.log"))
        procs.append((proc, handle))
        base = f"http://127.0.0.1:{args.port}"
        if not wait_ready(base, proc):
            print(f"the instance did not start; see {tmp}/off.log")
            print(open(os.path.join(tmp, "off.log")).read()[-2000:])
            return 2
        off_limit, off_payloads = walk(base, member_key, "off", expect_charging=False)
        check("off: nobody is premium", off_limit == "600", f"the comped member's key reports {off_limit}")
        proc.terminate()
        proc.wait(timeout=30)
        handle.close()

        # ---- B: the same binary, selling the premium tier to the same member ------------------------------
        print("\n=== B. MONETIZATION ON (premium API tier sold to the member) ===")
        proc, handle = start(args.binary, on_cfg, os.path.join(tmp, "on.log"))
        procs.append((proc, handle))
        base = f"http://127.0.0.1:{args.port + 1}"
        if not wait_ready(base, proc):
            print(f"the instance did not start; see {tmp}/on.log")
            print(open(os.path.join(tmp, "on.log")).read()[-2000:])
            return 2
        on_limit, on_payloads = walk(base, member_key, "on", expect_charging=True)

        check("on: the comped member's key is counted against the PREMIUM limit", on_limit == "1200",
              f"X-RateLimit-Limit={on_limit}")
        if other_key:
            status, headers, _ = graphql(base, "{ __typename }", other_key)
            got = header(headers, "X-RateLimit-Limit")
            check("on: an ungranted member's key is STILL counted against the free limit", got == "600",
                  f"X-RateLimit-Limit={got}")

        # THE LEAK TEST, ON THE WIRE: the same calls, with the same credential, answer the same data.
        for name in PROBES:
            check(f"§18.2: {name} answers THE SAME with the switch on", off_payloads[name] == on_payloads[name],
                  f"off={off_payloads[name][:80]} on={on_payloads[name][:80]}")
        note(f"comped key limit: {off_limit} (off) -> {on_limit} (on)")

        # The startup log has to state the posture, both ways, or an operator cannot tell which one they are in.
        on_log = open(os.path.join(tmp, "on.log")).read()
        check("on: the startup line states the premium limit", "1200" in on_log,
              "the posture line does not name the premium rate")
        proc.terminate()
        proc.wait(timeout=30)
        handle.close()
    finally:
        for proc, handle in procs:
            if proc.poll() is None:
                proc.kill()
            try:
                handle.close()
            except Exception:
                pass
        for path in (off_cfg, on_cfg):
            try:
                os.unlink(path)
            except OSError:
                pass

    print("\n" + "=" * 60)
    if FAILURES:
        print(f"{len(FAILURES)} FAILURE(S):")
        for f in FAILURES:
            print(f"  - {f}")
        return 1
    print(f"WALK 15 PASSED ({len(NOTES)} measured facts).")
    return 0


if __name__ == "__main__":
    sys.exit(main())
