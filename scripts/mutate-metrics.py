#!/usr/bin/env python3
"""The mutation ledger for phase 16 slice 2 (the instance metrics).

Each row mutates ONE rule, runs the test that claims to enforce it, and requires that test to go RED while the
run is otherwise clean. Then it restores and requires the file to be byte-identical, because a mutation that
was not restored invalidates every run after it.

WHY A LEDGER RATHER THAN A GREEN SUITE. Every figure here is a COUNT, and a count is exactly the kind of thing
a test can appear to check while never reaching the line: an assertion that `scenes_total` equals the service's
own arithmetic would be green for a service that counts the wrong table twice. This ledger's answer is to break
each rule and watch a NAMED test go red.

MOST MUTATIONS ATTACK THE GENERATED STATEMENT, not the .sql source: that is the artifact which runs, and
rewriting the source would need a `sqlc generate` inside the gate -- a step that can silently fail and leave
the previous statement in place. The one mutation that cannot avoid a generator is the ADMIN directive, because
the role check lives in the SCHEMA: that row runs `gqlgen generate` for the mutation and again for the restore,
and then requires `git status` to be clean, so a half-restored generated file cannot pass as green.

TWO OF THESE ROWS EXIST BECAUSE OF A FIXTURE, and that is the ledger telling on the test rather than the code:
`invites_unused` and `edits_pending` differ from their neighbours only by a predicate, and on a database where
every invite key has been used and no edit exists, swapping the predicate changes NO value -- so the mutation
survives until the test plants a row that separates them. The fixtures were written for those two rows.
"""
import os
import subprocess
import sys
from pathlib import Path

ROOT = Path("/home/alvaro/code-local/go/stash-box")
DSN = "postgres:smoke_pw@127.0.0.1:55434/sbx-appeal-141?sslmode=disable"

SERVICE = ROOT / "internal/service/metrics/metrics.go"
QUERY = ROOT / "internal/queries/instance_metrics.sql.go"
SCHEMA = ROOT / "graphql/schema/types/instance_metrics.graphql"

GRAPHQL = ["go", "test", "-tags", "integration", "-count=1", "-p", "1", "-run",
           "InstanceMetrics|WindowIsAFilter|FigureAgrees|InviteFigures|PendingEdit", "./internal/api/"]
UNIT = ["go", "test", "-count=1", "-run", "MetricList|EveryMetric|WindowedMetric", "./internal/service/metrics/"]

# The window over members: the arm that plants an account 60 days old.
DROP_MEMBERS_WINDOW = (
    "          AND u.last_api_call >= $1) AS members_active_window,",
    "          AND TRUE) AS members_active_window,",
)
# And over sources, the other direction of the same rule.
DROP_SOURCES_WINDOW = (
    "WHERE cs.last_checked >= $1) AS sources_checked_window,",
    "WHERE TRUE) AS sources_checked_window,",
)

MUTATIONS = [
    dict(
        name="the members window is dropped: every account counts as active",
        path=QUERY, old=DROP_MEMBERS_WINDOW[0], new=DROP_MEMBERS_WINDOW[1],
        cmd=GRAPHQL,
        expect_red="TestTheWindowIsAFilterRatherThanAWord -- the 60-day-old account moves "
                   "members_active_window, which the arm requires to stay still",
    ),
    dict(
        name="the source window is dropped: a check from 60 days ago counts as checked in the window",
        path=QUERY, old=DROP_SOURCES_WINDOW[0], new=DROP_SOURCES_WINDOW[1],
        cmd=GRAPHQL,
        expect_red="TestTheWindowIsAFilterRatherThanAWord and the recomputation of sources_checked_window",
    ),
    dict(
        name="the status predicate is inverted: 'unknown' counts the sources the sweep HAS looked at",
        path=QUERY,
        old="WHERE cs.status = 'unknown') AS sources_unknown,",
        new="WHERE cs.status <> 'unknown') AS sources_unknown,",
        cmd=GRAPHQL,
        expect_red="the recomputation of sources_unknown against the table",
    ),
    dict(
        name="the unused-invite predicate is inverted: 'unused' counts the keys that were used",
        path=QUERY,
        old="WHERE k.uses = 0) AS invites_unused",
        new="WHERE k.uses > 0) AS invites_unused",
        cmd=GRAPHQL,
        expect_red="TestTheTwoInviteFiguresMeasureDifferentThings -- the planted unused key must move "
                   "invites_unused by one, and under this mutant it moves nothing",
    ),
    dict(
        name="the pending-edit status is read as a decided one",
        path=QUERY,
        old="WHERE e.status = 'PENDING') AS edits_pending,",
        new="WHERE e.status = 'ACCEPTED') AS edits_pending,",
        cmd=GRAPHQL,
        expect_red="TestAPendingEditIsPendingRatherThanDecided -- the planted PENDING edit must move "
                   "edits_pending by one",
    ),
    dict(
        name="the fingerprint figure counts scenes instead of fingerprints",
        path=QUERY,
        old="(SELECT COUNT(*) FROM scene_fingerprints) AS fingerprints_total,",
        new="(SELECT COUNT(*) FROM scenes) AS fingerprints_total,",
        cmd=GRAPHQL,
        expect_red="the recomputation of fingerprints_total against the table it names",
    ),
    dict(
        name="one figure is read from another figure's column (members_total from the active count)",
        path=SERVICE,
        old='metric("members_total", row.MembersTotal,',
        new='metric("members_total", row.MembersActiveWindow,',
        cmd=GRAPHQL,
        expect_red="the recomputation of members_total; the unit test cannot see this, which is why the "
                   "recomputation exists",
    ),
    dict(
        name="a figure is dropped from the served list",
        path=SERVICE,
        old="""		metric("invites_unused", row.InvitesUnused,
			"Invite keys with no use recorded."),
""",
        new="",
        cmd=UNIT,
        expect_red="the golden key sequence and the length check against the statement's own columns",
    ),
    dict(
        name="a figure is served without its definition",
        path=SERVICE,
        old='			"Invite keys with no use recorded."),',
        new='			""),',
        cmd=UNIT,
        expect_red="the definition assertions -- a bare number is not a measurement",
    ),
    dict(
        name="the window is a month rather than the declared days",
        path=SERVICE,
        old="	since := now.AddDate(0, 0, -WindowDays)",
        new="	since := now.AddDate(0, -WindowDays, 0)",
        cmd=GRAPHQL,
        expect_red="the `since` assertion and the window arm (a -30 MONTH start puts every planted row "
                   "inside the window)",
    ),
    dict(
        name="the response stops naming the window it used",
        path=SERVICE,
        old="		WindowDays:  WindowDays,",
        new="		WindowDays:  WindowDays + 1,",
        cmd=GRAPHQL,
        expect_red="the windowDays assertion in TestInstanceMetricsServesEveryFigureWithItsDefinition",
    ),
    dict(
        name="the role check is removed from the schema (the generator runs inside this row)",
        path=SCHEMA,
        old="  instanceMetrics: InstanceMetrics! @hasRole(role: ADMIN)",
        new="  instanceMetrics: InstanceMetrics!",
        cmd=GRAPHQL,
        generate=True,
        expect_red="TestInstanceMetricsIsRefusedToAMemberWithoutTheRole -- a member without the role reads "
                   "every figure",
    ),
]


def run(cmd):
    proc = subprocess.run(
        cmd, cwd=ROOT,
        env={**os.environ, "POSTGRES_DB": DSN},
        capture_output=True, text=True, timeout=1200,
    )
    return proc.returncode, proc.stdout + proc.stderr


def generate():
    """gqlgen, for the one mutation that lives in the schema. Its failure is fatal rather than a SKIP: a
    generator that fails silently leaves the previous artifact in place, and the row would then report on code
    that was never mutated."""
    proc = subprocess.run(["go", "run", "github.com/99designs/gqlgen", "generate"],
                          cwd=ROOT, capture_output=True, text=True, timeout=900)
    return proc.returncode, proc.stdout + proc.stderr


def git_clean():
    proc = subprocess.run(["git", "status", "--porcelain"], cwd=ROOT, capture_output=True, text=True)
    return proc.stdout.strip()


def main():
    pre_existing = git_clean()
    if pre_existing:
        print("REFUSING TO RUN: the tree is not clean before the gate, so a mutation's restore cannot be "
              "distinguished from the state that was already there:\n" + pre_existing)
        return 2

    results = []
    for m in MUTATIONS:
        path = m["path"]
        original = path.read_text()
        if m["old"] not in original:
            print(f"SKIP (anchor not found): {m['name']}")
            results.append((m["name"], "ANCHOR-MISSING"))
            continue
        if original.count(m["old"]) != 1:
            print(f"SKIP (anchor appears {original.count(m['old'])}x, not once): {m['name']}")
            results.append((m["name"], "ANCHOR-AMBIGUOUS"))
            continue

        path.write_text(original.replace(m["old"], m["new"]))
        if m.get("generate"):
            code, output = generate()
            if code != 0:
                path.write_text(original)
                generate()
                print(f"{m['name']}\n    -> GENERATOR FAILED (no verdict)\n{output[-1500:]}")
                results.append((m["name"], "GENERATOR-FAILED"))
                continue

        code, output = run(m["cmd"])

        # Restore FIRST, and the generated artifact too: a mutation left in a generated file is invisible to
        # the next row's anchors and to the operator.
        path.write_text(original)
        if m.get("generate"):
            generate()

        if path.read_text() != original:
            print(f"RESTORE FAILED for {m['name']} -- stopping")
            sys.exit(1)
        dirty = git_clean()
        if dirty:
            print(f"RESTORE LEFT THE TREE DIRTY after {m['name']}:\n{dirty}\n-- stopping")
            sys.exit(1)

        if code == 0:
            verdict = "SURVIVED (the tests passed on the mutant: nothing here enforces this rule)"
        elif "build failed" in output or "cannot use" in output or "undefined:" in output or "syntax error" in output:
            verdict = "COMPILE-ERR (not a verdict)"
        else:
            failing = [l.strip() for l in output.splitlines() if l.strip().startswith("--- FAIL")]
            verdict = "KILLED by " + (", ".join(failing) if failing else "an unlabelled failure")
        print(f"{m['name']}\n    -> {verdict}\n    (expected red: {m['expect_red']})")
        results.append((m["name"], verdict))

    print("\n=== LEDGER ===")
    for name, verdict in results:
        print(f"{verdict.split(' ')[0]:16} {name}")

    code, output = run(GRAPHQL)
    print(f"\nRESTORED TREE, the metrics tests: exit {code}")
    code_unit, _ = run(UNIT)
    print(f"RESTORED TREE, the metrics unit tests: exit {code_unit}")
    return 0 if all("KILLED" in v for _, v in results) else 1


if __name__ == "__main__":
    sys.exit(main())
