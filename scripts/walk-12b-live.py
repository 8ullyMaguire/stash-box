#!/usr/bin/env python3
"""Live walk of the federation identify endpoint (phase 12, slabs 12b/12c).

Starts the REAL binary -- the real router, the real middleware chain, a real process -- against the
smoke database, asks it four questions with curl semantics, and recomputes the digest it publishes
INDEPENDENTLY, in Python, from the document the database holds. What the integration test cannot cover
is exactly this: the route registration, the middleware, and a process started from the environment.

    POSTGRES_DB='postgres:smoke_pw@127.0.0.1:55434/sbx-pr1284?sslmode=disable' \\
        python3 scripts/walk-12b-live.py

Exit 0 only if every verdict is green and the instance was stopped.
"""

import hashlib
import json
import os
import re
import signal
import subprocess
import sys
import time
import urllib.error
import urllib.request

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SCRATCH = os.environ.get("BH_AGENT_WORKSPACE", "/tmp")
BIN = os.path.join(SCRATCH, "stash-box-12b-walk")

DSN = os.environ.get("POSTGRES_DB", "postgres:smoke_pw@127.0.0.1:55434/sbx-pr1284?sslmode=disable")

# The DSN is `user:pass@host:port/db?params`. Parsed with one regex rather than by splitting on both
# separators in turn: the first version of this walk tried to walk the string and crashed on the DSN it
# is always given, which is a script that never reached its first verdict.
_dsn = re.match(r"^(?P<user>[^:]+):(?P<password>[^@]*)@(?P<host>[^:]+):(?P<port>\d+)/(?P<db>[^?]+)", DSN)
assert _dsn, f"could not read the DSN: {DSN}"
PGUSER, PGPASS = _dsn["user"], _dsn["password"]
PGHOST, PGPORT, PGDB = _dsn["host"], _dsn["port"], _dsn["db"]

HOST, PORT = "127.0.0.1", 13998
INSTANCE = "walk-12b"
URL = f"http://{HOST}:{PORT}{'/federation/identify'}"

verdicts = []


def check(name, ok, detail=""):
    verdicts.append((name, ok, detail))
    print(f"{'PASS' if ok else 'FAIL'}  {name}" + (f"\n      {detail}" if detail else ""))
    return ok


def post(payload, version="1"):
    """POST one body. Returns (status, decoded body). A 4xx is an ANSWER here, not an error."""
    body = json.dumps(payload).encode()
    req = urllib.request.Request(URL, data=body, method="POST")
    req.add_header("Content-Type", "application/json")
    if version:
        req.add_header("X-Stash-Federation-Version", version)
    try:
        with urllib.request.urlopen(req, timeout=10) as resp:
            return resp.status, json.loads(resp.read().decode())
    except urllib.error.HTTPError as e:
        return e.code, json.loads(e.read().decode())


def psql(sql):
    env = dict(os.environ, PGPASSWORD=PGPASS)
    out = subprocess.run(
        ["psql", "-h", PGHOST, "-p", PGPORT, "-U", PGUSER, "-d", PGDB, "-tAc", sql],
        capture_output=True, text=True, env=env, timeout=60)
    if out.returncode != 0:
        raise RuntimeError(out.stderr.strip())
    return out.stdout.strip()


def canonical_digest(snapshot):
    """The SAME rule as the Go side: parse, refuse an edge with no name, sort by (tag, implies),
    marshal compactly, sha256 hex. Written here independently rather than imported, because a walk that
    calls the code under test to check the code under test checks nothing."""
    edges = json.loads(snapshot or "[]")
    for e in edges:
        assert e["tag"].strip() and e["implies"].strip(), f"an edge with no name: {e}"
    edges.sort(key=lambda e: (e["tag"], e["implies"]))
    body = json.dumps([{"tag": e["tag"], "implies": e["implies"]} for e in edges],
                      separators=(",", ":")).encode()
    return hashlib.sha256(body).hexdigest()


def question(claim=None):
    q = {"QueryID": "3b3e7f6a-0000-4000-8000-00000000c12b", "TargetType": "", "Description": "",
         "CandidateNames": [], "Ontology": {"CoreVersions": [], "LocalVersion": 0, "Digest": "",
                                            "NamedTags": 0, "Implications": 0}}
    if claim:
        q["Ontology"] = claim
    return q


def claim_for(versions, digest, local=1, tags=1, implications=1):
    return {"CoreVersions": versions, "LocalVersion": local, "Digest": digest,
            "NamedTags": tags, "Implications": implications}


def main():
    print("building the binary the walk will run ...")
    build = subprocess.run(["go", "build", "-o", BIN, "./cmd/stash-box"], cwd=REPO,
                           capture_output=True, text=True, timeout=900)
    if build.returncode != 0:
        print("BUILD FAILED -- no verdicts are meaningful:\n" + build.stderr[-1500:])
        return 1

    # A config file for the three keys initConfig reads before anything else, with `instance_id`
    # DELIBERATELY ABSENT: it comes from the environment, which is the mechanism this walk is partly here
    # to verify. `viper.Unmarshal` does not consult AutomaticEnv for an unbound key, so without the
    # BindEnv in cmd/stash-box/init.go the endpoint answers 503 to an operator who has set exactly the
    # variable the message tells them to set -- and this arm is what turns that from a reading into a
    # verdict.
    config_path = os.path.join(SCRATCH, "walk-12b-config.yml")
    with open(config_path, "w") as f:
        f.write(f"host: {HOST}\nport: {PORT}\ndatabase: {DSN}\n")

    env = dict(os.environ, STASH_BOX_INSTANCE_ID=INSTANCE)
    log = open(os.path.join(SCRATCH, "walk-12b-server.log"), "w")
    proc = subprocess.Popen([BIN, "--config_file", config_path],
                            stdout=log, stderr=subprocess.STDOUT, env=env,
                            cwd=SCRATCH, start_new_session=True)
    try:
        # Readiness is a REQUEST THAT ANSWERS, not a sleep: a fixed wait races the process about as
        # often as it works.
        ready = False
        for _ in range(60):
            if proc.poll() is not None:
                break
            try:
                post(question(), version="1")
                ready = True
                break
            except Exception:
                time.sleep(0.5)
        if not check("the instance started and answered something", ready,
                     "the process log is the place to look when this fails"):
            print(open(os.path.join(SCRATCH, "walk-12b-server.log")).read()[-2000:])
            return 1

        # W1 -- the version header is required, and the refusal says which side is wrong.
        status, body = post(question(), version="")
        check("W1 a question with no protocol version is refused by name",
              status == 400 and "none sent" in body.get("error", ""), f"{status} {body}")

        # W2 -- an identification question is refused BY NAME rather than answered empty. This is the
        # arm the whole slice exists for: an empty candidate list would be indistinguishable from
        # "this instance has not built it".
        q = question()
        q["TargetType"] = "performer"
        q["Description"] = "who is this"
        status, body = post(q)
        check("W2 an identification question is refused, naming the unbuilt half",
              status == 422 and "identification" in body.get("error", ""), f"{status} {body}")

        # W3 -- the ontology question is answered, and the digest is checked against an INDEPENDENT
        # computation from the document the database holds.
        status, body = post(question(claim=claim_for([1], "ab" * 32)))
        answered = status == 200 and body.get("PeerInstanceID") == INSTANCE
        check("W3 an ontology-only question is answered by the running process", answered,
              f"{status} {body}")

        if answered:
            snapshot = psql("select snapshot::text from ontology_versions order by version desc limit 1")
            expected = canonical_digest(snapshot)
            check("W3b the published digest is the digest of the document in the database",
                  body["Ontology"]["Digest"] == expected,
                  f"published {body['Ontology']['Digest']}, computed {expected}")
            check("W3c the claim reports the document's own counts",
                  body["Ontology"]["Implications"] == len(json.loads(snapshot or "[]")),
                  f"claim says {body['Ontology']['Implications']}")

        # W4 -- THE NEGOTIATION, live: a peer that declares core 1 is told the v2-only field will not
        # cross, with the reason that names the peer as the side that has to move.
        crossing = {c["Name"]: c for c in body.get("Crossing", [])} if answered else {}
        check("W4 a core-1 peer is told the v2-only field will not cross, and why",
              crossing.get("category", {}).get("Allowed") is False
              and "peer declares core 1" in crossing.get("category", {}).get("Reason", ""),
              json.dumps(crossing.get("category", {})))
        check("W4b a core-1 peer is told the core-1 fields DO cross",
              crossing.get("tag", {}).get("Allowed") is True and
              crossing.get("implies", {}).get("Allowed") is True,
              json.dumps(crossing.get("tag", {})))

        # W5 -- a peer that speaks both versions is allowed everything: the control, without which W4
        # could be a rule that refuses unconditionally.
        status, body = post(question(claim=claim_for([1, 2], "cd" * 32)))
        both = {c["Name"]: c for c in body.get("Crossing", [])}
        check("W5 a peer that speaks both core versions is allowed every field",
              status == 200 and all(c["Allowed"] for c in both.values()) and len(both) == 3,
              json.dumps(both))

        # W6 -- a claim that is declared but malformed is refused rather than compared with nothing.
        status, body = post(question(claim=claim_for([1], "zz" * 32)))
        check("W6 a malformed peer claim is refused by name",
              status == 422 and "claim" in body.get("error", ""), f"{status} {body}")

    finally:
        proc.send_signal(signal.SIGTERM)
        try:
            proc.wait(timeout=20)
        except subprocess.TimeoutExpired:
            proc.kill()
        log.close()

    killed = sum(1 for _, ok, _ in verdicts if ok)
    print(f"\n{killed}/{len(verdicts)} verdicts green")
    return 0 if killed == len(verdicts) and verdicts else 1


if __name__ == "__main__":
    sys.exit(main())
