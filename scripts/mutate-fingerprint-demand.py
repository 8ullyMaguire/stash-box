#!/usr/bin/env python3
"""Can the demand board's tests fail? Three mutations, one rule each.

  A. count(DISTINCT user_id) -> count(user_id)
     "rows are not holders" can only be false if the two are told apart, and the
     fixture gives one member two rows of one hash.
  B. the `vote = 1` filter removed
     a report becomes demand, and the report test's 2 becomes 3.
  C. a `scene_id` field added to the response type
     the gate the plan names is about the FIELD SET, so the firewall has to fail
     when a locator is added even though every value stays correct.

A and B need `sqlc generate` before the test can see them; C needs `gqlgen`.
Both generators are part of the round, not an afterthought -- the same
stale-artefact rule this repo has paid for twice.

Verdicts come from EXIT CODES, not from parsed output.

Usage: INTEGRATION_PG=... python3 scripts/mutate-fingerprint-demand.py
"""
import os
import subprocess
import sys

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SQL = os.path.join(REPO, "internal/queries/sql/fingerprint.sql")
SCHEMA = os.path.join(REPO, "graphql/schema/types/fingerprint_demand.graphql")
PG = os.environ.get("INTEGRATION_PG", "")

if not PG:
    sys.exit("INTEGRATION_PG is required")

MUTATIONS = [
    (
        "A count(DISTINCT user_id) -> count(user_id)",
        SQL,
        "    count(DISTINCT sf.user_id)::INTEGER AS holder_count",
        "    count(sf.user_id)::INTEGER AS holder_count",
        ["sqlc"],
        "TestFingerprintDemandCountsHoldersNotSubmissions",
    ),
    (
        "B the vote = 1 filter removed",
        SQL,
        "WHERE sf.vote = 1",
        "WHERE (sf.vote = 1 OR sf.vote = -1)",
        ["sqlc"],
        "TestFingerprintDemandExcludesReports",
    ),
    (
        "C a scene_id added to the response type",
        SCHEMA,
        "  hash: FingerprintHash!",
        "  hash: FingerprintHash!\n  scene_id: ID",
        ["gqlgen"],
        "TestFingerprintDemandAPIResponseCarriesNoLocator",
    ),
]


def sh(cmd, cwd=REPO, timeout=900):
    return subprocess.run(cmd, cwd=cwd, capture_output=True, text=True, timeout=timeout, env=dict(os.environ))


def regenerate(which):
    if "sqlc" in which:
        r = sh(["go", "run", "github.com/sqlc-dev/sqlc/cmd/sqlc", "generate"])
        if r.returncode != 0:
            return f"sqlc generate failed: {r.stderr[-300:]}"
    if "gqlgen" in which:
        r = sh(["go", "generate", "./internal/models/"])
        if r.returncode != 0:
            return f"gqlgen failed: {r.stderr[-300:]}"
    return None


def run_named_test(name):
    r = sh([
        "env", f"INTEGRATION_PG={PG}", "./scripts/integration-tests.sh",
        "-tags", "integration", "-p", "1", "-run", name, "./internal/api/",
    ], timeout=900)
    return r.returncode, (r.stdout + r.stderr)[-600:]


originals = {p: open(p).read() for p in (SQL, SCHEMA)}
try:
    for label, path, old, new, regen, test_name in MUTATIONS:
        body = open(path).read()
        if body.count(old) != 1:
            print(f"{label}: anchor matched {body.count(old)} times -- not a verdict")
            continue
        open(path, "w").write(body.replace(old, new, 1))
        try:
            problem = regenerate(regen)
            if problem:
                print(f"{label}: {problem} -- not a verdict")
                continue
            rc, tail = run_named_test(test_name)
            verdict = "RED (killed)" if rc != 0 else "GREEN (SURVIVED)"
            print(f"{label}\n  {test_name}: exit={rc} -> {verdict}")
            if rc == 0:
                print("  tail:", tail.replace("\n", " ")[-300:])
            # Neighbour check only for the mutation whose neighbour is cheap.
            if label.startswith("B"):
                rc2, _ = run_named_test("TestFingerprintDemandCountsHoldersNotSubmissions")
                print(f"  neighbour (holders-not-rows): exit={rc2} -> "
                      f"{'GREEN' if rc2 == 0 else 'RED (the report filter moved a test it should not)'}")
        finally:
            open(path, "w").write(body)
            if open(path).read() != body:
                print(f"{label}: !! RESTORE FAILED")
finally:
    for path, body in originals.items():
        open(path, "w").write(body)
    print("restored; regenerating", regenerate(["sqlc", "gqlgen"]) or "OK")
    rc, _ = run_named_test("TestFingerprintDemand")
    print(f"AFTER RESTORE: exit={rc} -> {'GREEN' if rc == 0 else 'STILL RED'}")
