#!/usr/bin/env python3
"""Mutation checks for scene freshness (spec section 7, plan phase 7 slice 4).

Four of these mutate the MIGRATION rather than Go code, because the score is computed in the
database: the decay, the weights and the window are all literals in `114_scene_freshness.up.sql`,
and mutating Go would prove nothing about any of them. The test harness drops every table and
re-runs the migrations at setup, so a mutated migration is genuinely applied on the next run --
which is the property that makes this script worth anything.

Each case breaks ONE behaviour and names the test that must go red for it, then restores the file
and confirms the same test goes green again. A mutation that reddens nothing means the test does
not cover the behaviour it claims to; leaving the mutation applied means the next result is a lie,
so the restore is checked too.
"""
import os
import subprocess
import sys
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
MIGRATION = "internal/database/migrations/postgres/114_scene_freshness.up.sql"
QUERY = "internal/service/scene/query.go"

CASES = [
    {
        "name": "the decay half-life is not the specified 48 hours",
        "file": MIGRATION,
        "old": "weight / power(1.0 + age_hours / 48.0, 1.5)",
        "new": "weight / power(1.0 + age_hours / 96.0, 1.5)",
        "test": "TestFreshnessDecaysWithAge",
    },
    {
        "name": "the review weight is doubled instead of rating - 3",
        "file": MIGRATION,
        "old": "(r.rating - 3)::double precision",
        "new": "(2 * (r.rating - 3))::double precision",
        "test": "TestFreshnessSumsItsSignals",
    },
    {
        "name": "the fingerprint vote is ignored, so a withdrawal counts as a submission",
        "file": MIGRATION,
        "old": "(3 * sf.vote)::double precision AS weight",
        "new": "3::double precision AS weight",
        "test": "TestAWithdrawnFingerprintIsANegativeSignal",
    },
    {
        "name": "the 90-day window is widened to 180 days",
        "file": MIGRATION,
        "old": "WHERE occurred_at > now() - interval '90 days'",
        "new": "WHERE occurred_at > now() - interval '180 days'",
        "test": "TestFreshnessWindowExcludesOlderSignals",
    },
    {
        "name": "RISING totals the two days instead of comparing them",
        "file": QUERY,
        "old": '"COALESCE(scene_freshness.recent_score, 0) - COALESCE(scene_freshness.earlier_score, 0) %s, scenes.id %s",',
        "new": '"COALESCE(scene_freshness.recent_score, 0) + COALESCE(scene_freshness.earlier_score, 0) %s, scenes.id %s",',
        "test": "TestRisingComparesThisDayWithTheOneBefore",
    },
]

ENV_TEST = ["go", "test", "-tags", "integration", "-count=1"]


def run_test(name: str) -> "tuple[bool, str]":
    """(passed, output) for the named test."""
    result = subprocess.run(
        ENV_TEST + ["-run", f"^{name}$", "./internal/api/"],
        cwd=REPO, capture_output=True, text=True, timeout=300,
        env={**os.environ,
             "POSTGRES_DB": "postgres:smoke_pw@127.0.0.1:55434/stash-box-test?sslmode=disable"},
    )
    return result.returncode == 0, result.stdout + result.stderr


def main() -> int:
    failures = 0
    for case in CASES:
        path = REPO / case["file"]
        original = path.read_text()
        if case["old"] not in original:
            print(f"SKIP (anchor not found): {case['name']} in {case['file']}")
            failures += 1
            continue

        path.write_text(original.replace(case["old"], case["new"], 1))
        try:
            passed_under_mutation, output = run_test(case["test"])
        finally:
            path.write_text(original)

        passed_after_restore, _ = run_test(case["test"])

        if not passed_under_mutation and passed_after_restore:
            print(f"CAUGHT   {case['test']}: {case['name']}")
        else:
            failures += 1
            verdict = "NOT CAUGHT (test stayed green under the mutation)" if passed_under_mutation \
                else "RESTORE FAILED (still red after restoring)"
            print(f"SUSPECT  {case['test']}: {case['name']} -- {verdict}")
            print(output[-2000:])

    print(f"\n{len(CASES) - failures}/{len(CASES)} mutations caught and restored")
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
