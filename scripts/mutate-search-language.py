#!/usr/bin/env python3
"""Mutation check for internal/service/search (the discovery query language).

A test that passes on unfixed code is no evidence. Each mutation below breaks one documented
behaviour of the language, and the script asserts three things:

  1. the mutation COMPILED -- a mutation that does not compile leaves the previous binary in
     place, and the "failures" it reports belong to the previous mutation;
  2. an expected test went RED, and it is one of the tests that names that behaviour;
  3. restoring the file makes the suite GREEN again -- the last step is what makes the red run
     mean anything, and skipping it is how a broken test is discovered months later.

Run from anywhere:  python3 scripts/mutate-search-language.py
"""

import pathlib
import re
import subprocess
import sys

REPO = pathlib.Path(__file__).resolve().parent.parent
PKG = "./internal/service/search/"
SRC = REPO / "internal/service/search"

# (file, text to replace, replacement, substrings of test names that MUST go red)
MUTATIONS = [
    (
        "lower.go",
        "out.NoMatch = len(out.Diagnostics) > 0",
        "out.NoMatch = false",
        [
            "TestAnUnknownNameMakesTheQueryEmptyAndSaysWhy",
            "TestNotYetTermsAreDiagnosedAndTheRestStillLowers",
        ],
    ),
    (
        "lower.go",
        """		if !ok {
			missing = append(missing, term)

			continue
		}""",
        """		if !ok {
			continue
		}""",
        [
            "TestAnUnknownNameMakesTheQueryEmptyAndSaysWhy",
            "TestAnUnknownExcludedNameIsAlsoReported",
        ],
    ),
    (
        "query_language.go",
        "		if !field.known() {",
        "		if false && !field.known() {",
        ["TestErrorsNameTheirOffset/"],
    ),
    (
        "query_language.go",
        '	if term.Field == FieldSort && strings.HasPrefix(value, "-") {',
        '	if false && term.Field == FieldSort && strings.HasPrefix(value, "-") {',
        ["TestBothSpellingsOfADescendingSortAreTheSameQuery"],
    ),
]


def run() -> tuple[int, str]:
    p = subprocess.run(
        ["go", "test", PKG, "-v", "-count=1"],
        capture_output=True, text=True, cwd=REPO,
    )

    return p.returncode, p.stdout + p.stderr


def failures(out: str) -> list[str]:
    return sorted({m for m in re.findall(r"--- FAIL: (\S+)", out)})


def main() -> int:
    rc, out = run()
    if rc != 0:
        print("BASELINE IS NOT GREEN -- fix that before mutating anything")
        print(out[-2000:])

        return 1
    print("baseline: green\n")

    problems = 0
    for name, old, new, expected in MUTATIONS:
        path = SRC / name
        original = path.read_text()
        if original.count(old) != 1:
            print(f"!! {name}: the text to mutate appears {original.count(old)} times, not once")

            return 1

        path.write_text(original.replace(old, new))
        rc, out = run()
        if rc == 2 or "build failed" in out:
            print(f"!! {name}: the mutation did NOT COMPILE -- its failures would mean nothing")
            print(out[-1500:])
            path.write_text(original)
            problems += 1

            continue

        got = failures(out)
        missing = [e for e in expected if not any(g.startswith(e) for g in got)]
        print(f"{name}: {old.strip().splitlines()[0][:60]!r} -> {new.strip()[:40]!r}")
        print(f"  red: {got if got else 'NOTHING -- the mutation was not caught'}")
        if missing:
            print(f"  !! expected these to be red and they were not: {missing}")
            problems += 1
        else:
            print("  ok: every expected test went red")

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
