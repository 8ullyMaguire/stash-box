#!/usr/bin/env python3
"""The mutation ledger for phase 16 slice 16a (the transparency report).

Each row: mutate ONE rule, run the test that claims to enforce it, and require that test to go RED while the
run is otherwise clean. Then restore and require the file to be byte-identical, because a mutation that was
not restored invalidates every run after it.

Why this exists rather than a green suite: the report's whole value is that its figures are TRUE, and a
figure is exactly the kind of thing a test can appear to check while never reaching the line. The first run
of the integration test found a real defect this way (a granted appeal's decision row was being counted as a
second release), which is the argument for the ledger in one sentence.

Two mutations attack the GENERATED query file rather than the .sql source: that is the artifact which runs,
and rewriting the SQL would need a `sqlc generate` inside the gate (a step that can silently fail and leave
the previous statement in place -- the exact stale-artifact trap this programme has been bitten by).
"""
import subprocess
import sys
from pathlib import Path

ROOT = Path("/home/alvaro/code-local/go/stash-box")
DSN = "postgres:smoke_pw@127.0.0.1:55434/sbx-appeal-141?sslmode=disable"

REPORT = ROOT / "internal/service/content/report.go"
QUERY = ROOT / "internal/queries/content_report.sql.go"
PAGE = ROOT / "internal/api/routes_transparency.go"

# The integration test recomputes every figure from the tables, so it is the only test that can see a
# mutation in the service or in the generated statement.
FIGURES = ["go", "test", "-tags", "integration", "-count=1", "-p", "1", "-run",
           "TestTheTransparencyReportRecomputesEveryFigureFromTheInstancesOwnTables", "./internal/api/"]
WINDOW_PAGE = ["go", "test", "-count=1", "-run", "TestTheTransparencyPageTellsTheTruthAboutItsOwnWindow",
               "./internal/api/"]
POSTURE_PAGE = ["go", "test", "-count=1", "-run",
                "TestTheTransparencyPageShowsThePostureTheProcessLogsAndEveryFigureTheReportCarries",
                "./internal/api/"]
GROUPING = ["go", "test", "-count=1", "-run", "TestFiguresAreGroupedSoASixFigureCountCanBeRead",
            "./internal/api/"]

MUTATIONS = [
    dict(
        name="the window is dropped: every figure counts the whole history",
        path=REPORT,
        old="\tsince := now.AddDate(0, 0, -windowDays)",
        new="\tsince := time.Unix(0, 0).UTC()",
        cmd=FIGURES,
        expect_red="the deltas: refusals 5 becomes 6, and the 120-day-old refusal stops being outside the window",
    ),
    dict(
        name="the in-force block count is read from the WINDOWED figure (the two kinds of number merged)",
        path=REPORT,
        old="\t\t\tBlocks:              row.BlocksInForce,",
        new="\t\t\tBlocks:              row.BlocksRecordedInWindow,",
        cmd=FIGURES,
        expect_red="the state delta (3 blocks in force, of which one is the planted 120-day-old refusal)",
    ),
    dict(
        name="granted and denied are swapped",
        path=REPORT,
        old="\t\t\tAppealsGranted:      row.AppealsGranted,",
        new="\t\t\tAppealsGranted:      row.AppealsDenied,",
        cmd=FIGURES,
        expect_red="the granted delta (2) against the denied delta (1)",
    ),
    dict(
        name="refusals and releases are swapped",
        path=REPORT,
        old="\t\t\tRefusals:       row.RefusalsInWindow,",
        new="\t\t\tRefusals:       row.ReleasesInWindow,",
        cmd=FIGURES,
        expect_red="the refusal delta (5) and the release delta (3)",
    ),
    dict(
        name="a negative retention becomes a window that ends in the future",
        path=REPORT,
        old="\tif retentionDays > 0 {\n\t\treturn retentionDays, true, false\n\t}",
        new="\tif retentionDays != 0 {\n\t\treturn retentionDays, true, false\n\t}",
        cmd=FIGURES,
        expect_red="the negative-retention arm (it would report a bounded window of -1 days and count nothing)",
    ),
    dict(
        name="a granted appeal's own decision row is counted again as a release (the defect the first run found)",
        path=QUERY,
        old="""        WHERE m."action" IN ('SOURCE_TAKEDOWN', 'SOURCE_BLOCK')
          AND (m."data" ->> 'released') = 'true'""",
        new="""        WHERE TRUE
          AND (m."data" ->> 'released') = 'true'""",
        cmd=FIGURES,
        expect_red="the release delta (3 becomes 5) and the 'larger by exactly 2' measurement",
    ),
    dict(
        name="the record count stops meaning 'out of service' and counts every record",
        path=QUERY,
        old='WHERE s."status" = ' + "'dmca'",
        new='WHERE s."status" = s."status"',
        cmd=FIGURES,
        expect_red="the absolute comparison, plus the fixture guards requiring out of service to be a "
                   "SUBSET (an 'available' mutation survived until those guards existed)",
    ),
    dict(
        name="the page claims the records are kept forever whatever the retention says",
        path=PAGE,
        old="\tif report.RecordsKeptForever {",
        new="\tif true {",
        cmd=WINDOW_PAGE,
        expect_red="the bounded case's window sentence and its NotContains of the indefinite wording",
    ),
    dict(
        name="the posture line is not rendered (a claim about the instance that nothing substantiates)",
        path=PAGE,
        old="html.EscapeString(mediaplane.PostureLine())",
        new="html.EscapeString(mediaplane.PostureLine()[:0])",
        cmd=POSTURE_PAGE,
        expect_red="the page-against-log comparison with mediaplane.PostureLine()",
    ),
    dict(
        name="figures are printed ungrouped",
        path=PAGE,
        old="""	digits := strconv.FormatInt(value, 10)""",
        new="""	return strconv.FormatInt(value, 10)
	digits := strconv.FormatInt(value, 10)""",
        cmd=GROUPING,
        expect_red="the grouping cases (1000, 12,345, 1,234,567)",
    ),
]


def run(cmd):
    proc = subprocess.run(
        cmd, cwd=ROOT,
        env={**__import__("os").environ, "POSTGRES_DB": DSN},
        capture_output=True, text=True, timeout=900,
    )
    return proc.returncode, proc.stdout + proc.stderr


def main():
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
        code, output = run(m["cmd"])
        path.write_text(original)

        if path.read_text() != original:
            print(f"RESTORE FAILED for {m['name']} -- stopping")
            sys.exit(1)

        if code == 0:
            verdict = "SURVIVED (the test passed on the mutant: it does not enforce this)"
        elif "build failed" in output or "cannot use" in output or "undefined:" in output or "syntax error" in output:
            verdict = "COMPILE-ERR (not a verdict)"
        else:
            failing = [l.strip() for l in output.splitlines() if l.strip().startswith("--- FAIL")]
            verdict = "KILLED by " + (", ".join(failing) if failing else "an unlabelled failure")
        print(f"{m['name']}\n    -> {verdict}\n    (expected red: {m['expect_red']})")
        results.append((m["name"], verdict))

    print("\n=== LEDGER ===")
    for name, verdict in results:
        print(f"{verdict.split(' ')[0]:12} {name}")

    code, output = run(FIGURES)
    print(f"\nRESTORED TREE, the figures test: exit {code}")
    return 0 if all("KILLED" in v for _, v in results) else 1


if __name__ == "__main__":
    sys.exit(main())
