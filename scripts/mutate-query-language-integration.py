#!/usr/bin/env python3
"""Mutation check for the query-language INTEGRATION path (the api package, build tag
`integration`).

`scripts/mutate-search-language.py` covers the language's own rules with no database. This one
covers the wiring: the same three claims the integration tests make are each broken here, and the
script asserts the mutation COMPILED, reddened a test that NAMES that behaviour, and that
restoring the file returned the suite to green.

It needs the scratch database, exactly as the integration tests do:

    POSTGRES_DB="postgres:smoke_pw@127.0.0.1:55434/stash-box-test?sslmode=disable" \
        python3 scripts/mutate-query-language-integration.py
"""

import os
import pathlib
import re
import subprocess
import sys

REPO = pathlib.Path(__file__).resolve().parent.parent
PKG = "./internal/api/"

# (file, text, replacement, count, the test that MUST go red)
MUTATIONS = [
    (
        "internal/service/search/lower.go",
        """		out.Filter.ExcludeTags = &models.MultiIDCriterionInput{
			Modifier: models.CriterionModifierExcludes,
			Value:    excludeTagIDs,
		}""",
        """		_ = excludeTagIDs""",
        1,
        "TestTheQueryLanguageReturnsWhatTheFilterItLowersIntoReturns",
    ),
    (
        "internal/service/scene/query.go",
        """	if input.Duration != nil {
		query = queryhelper.ApplyIntCriterion(query, "scenes.duration", input.Duration)
	}""",
        """	if false && input.Duration != nil {
		query = queryhelper.ApplyIntCriterion(query, "scenes.duration", input.Duration)
	}""",
        1,
        "TestADurationComparisonReachesTheQuery",
    ),
    (
        # Drops the redirect fallback: the name-only lookup that made the merged-tag test fail
        # the first time it ran.
        "internal/service/scene/query_language.go",
        "merged, err := r.queries.FindTagByNameOrAliasUnfiltered(ctx, name)",
        "merged, err := r.queries.FindTagByNameOrAlias(ctx, name)",
        1,
        "TestAQueryOverAMergedTagStillMeansSomething",
    ),
    (
        "internal/service/scene/query.go",
        "	if noMatch {",
        "	if false && noMatch {",
        2,
        "TestAnUnapplicableTermEmptiesTheResultAndIsReported",
    ),
]


def run() -> tuple[int, str]:
    env = dict(os.environ)
    p = subprocess.run(
        ["go", "test", "-tags", "integration", "-count=1", "-p", "1",
         "-run", "Language|Unapplicable|DurationComparison|MergedTag", PKG],
        capture_output=True, text=True, cwd=REPO, env=env,
    )

    return p.returncode, p.stdout + p.stderr


def failures(out: str) -> list[str]:
    return sorted({m for m in re.findall(r"--- FAIL: (\S+)", out)})


def main() -> int:
    if not os.environ.get("POSTGRES_DB"):
        print("POSTGRES_DB is not set: the integration tests need the scratch database")
        print('POSTGRES_DB="postgres:smoke_pw@127.0.0.1:55434/stash-box-test?sslmode=disable" ...')

        return 1

    rc, out = run()
    if rc != 0:
        print("BASELINE IS NOT GREEN -- fix that before mutating anything")
        print(out[-2500:])

        return 1
    print("baseline: green\n")

    problems = 0
    for name, old, new, count, expected in MUTATIONS:
        path = REPO / name
        original = path.read_text()
        if original.count(old) != count:
            print(f"!! {name}: the text to mutate appears {original.count(old)} times, not {count}")

            return 1

        path.write_text(original.replace(old, new))
        rc, out = run()
        if rc == 2 or "build failed" in out or "cannot use" in out:
            print(f"!! {name}: the mutation did NOT COMPILE -- its failures would mean nothing")
            print(out[-1500:])
            path.write_text(original)
            problems += 1

            continue

        got = failures(out)
        caught = any(g.startswith(expected) for g in got)
        print(f"{name}: mutate {old.strip().splitlines()[0][:60]!r}")
        print(f"  red: {got if got else 'NOTHING -- the mutation was not caught'}")
        if not caught:
            print(f"  !! {expected} was expected to go red and did not")
            problems += 1
        else:
            print(f"  ok: {expected} went red")

        path.write_text(original)
        rc, out = run()
        if rc != 0:
            print(f"!! {name}: RESTORING did not return the suite to green: {failures(out)}")
            problems += 1
        else:
            print("  ok: restoring the file makes the suite green again")
        print()

    print("ALL MUTATIONS CAUGHT" if problems == 0 else f"{problems} PROBLEM(S)")

    return 1 if problems else 0


if __name__ == "__main__":
    sys.exit(main())
