#!/usr/bin/env python3
"""Mutation checks for the tag explorer's facet list (spec section 6, plan phase 7 slice 3).

Each case breaks ONE behaviour and names the integration test that must go red for it, then
restores the file and confirms the same test goes green again. A mutation that reddens nothing
means the test does not cover the behaviour it claims to; leaving the mutation applied means the
next result is a lie, so the restore is checked too.

The first case is the one that matters most: dropping the recursive arm of the closure leaves
counts that are still plausible (a small number next to a tag), so only the assertion that
compares the facet against the result the reader gets by clicking it can catch it.
"""
import os
import subprocess
import sys
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
FACETS = "internal/service/scene/facets.go"

CASES = [
    {
        "name": "facet counts stop walking the implication hierarchy (direct rows only)",
        "file": FACETS,
        "old": """closure AS (
    SELECT scene_id, tag_id FROM carried

    UNION

    SELECT c.scene_id, ti.implied_tag_id
    FROM closure c
    JOIN tag_implications ti ON ti.tag_id = c.tag_id
)""",
        "new": """closure AS (
    SELECT scene_id, tag_id FROM carried
)""",
        "test": "TestSceneFacetCountsMatchWhatTheFacetWouldReturn",
    },
    {
        "name": "the tags the filter already applies are offered back as facets",
        "file": FACETS,
        "old": "\tif filter.Tags != nil {\n\t\tapplied = filter.Tags.Value\n\t}",
        "new": "\tif false && filter.Tags != nil {\n\t\tapplied = filter.Tags.Value\n\t}",
        "test": "TestFacetsAreAboutTheResultSet",
    },
    {
        "name": "strength is reported as a raw count instead of a fraction of the instance",
        "file": FACETS,
        "old": "\t\tif total[id] > 0 {\n\t\t\tfacet.Strength = float64(counts[id]) / float64(total[id])\n\t\t}",
        "new": "\t\tfacet.Strength = float64(counts[id])",
        "test": "TestFacetStrengthIsAFractionOfTheInstance",
    },
    {
        "name": "the limit and min_count arguments never reach the statement",
        "file": FACETS,
        "old": '\targs = append(args, minCount)\n\tminPlaceholder := fmt.Sprintf("$%d", len(args))\n\n\targs = append(args, limit)\n\tlimitPlaceholder := fmt.Sprintf("$%d", len(args))',
        "new": '\targs = append(args, 1)\n\tminPlaceholder := fmt.Sprintf("$%d", len(args))\n\n\targs = append(args, 1000)\n\tlimitPlaceholder := fmt.Sprintf("$%d", len(args))',
        "test": "TestFacetLimitsApply",
    },
]

ENV_TEST = ["go", "test", "-tags", "integration", "-count=1"]


def run_test(name: str) -> "tuple[bool, str]":
    """(passed, output) for the named test."""
    result = subprocess.run(
        ENV_TEST + ["-run", f"^{name}$", "./internal/api/"],
        cwd=REPO, capture_output=True, text=True,
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
