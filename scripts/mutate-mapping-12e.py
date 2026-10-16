#!/usr/bin/env python3
"""Mutation battery for the local-to-core tag mapping (phase 12, slice 12e).

Each probe breaks one rule, runs the test that owns it, and the probe is KILLED only if that test goes
red. A probe that leaves the suite green is a rule nothing checks -- which, for this file, is the same as
a name crossing the boundary unagreed.

    python3 scripts/mutate-mapping-12e.py [M1 M2 M3 ...]
"""

import subprocess
import sys
import os

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
MAPPING = os.path.join(REPO, "internal", "service", "federation", "mapping.go")
ALL_TEST = os.path.join(REPO, "internal", "service", "federation")

PROBES = [
    (
        "M1 a mapping pointing outside the vocabulary is passed through",
        MAPPING,
        "case IsCoreTag(target):",
        "case target != \"\":",
        ["-run", "TestResolveLocalTag|TestNoLocalNameEverCrosses"],
        "an unagreed name then crosses as if it were agreed, which is the silent mis-mapping this slice "
        "exists to refuse",
    ),
    (
        "M2 a tag with no mapping is dropped instead of refused",
        MAPPING,
        "\tif !declared {",
        "\tif !declared {\n\t\treturn TagResolution{Local: name}",
        ["-run", "TestResolveLocalTag|TestResolveLocalTagsIsOrderedAndDeduplicated"],
        "an unmapped tag becomes an empty row indistinguishable from 'this instance has no such tag': "
        "the refusal is the whole product of the resolver",
    ),
    (
        "M3 a chain is followed instead of refused",
        MAPPING,
        "if _, isLocal := mappings[target]; isLocal {",
        "if _, isLocal := mappings[target]; isLocal && false {",
        ["-run", "TestResolveLocalTag"],
        "following the chain means guessing which end the operator meant -- and here it would land on a "
        "name that IS agreed, so the guessing is invisible in the result",
    ),
]


def run(cmd):
    return subprocess.run(cmd, cwd=REPO, capture_output=True, text=True, timeout=900)


def main():
    wanted = [a for a in sys.argv[1:] if not a.startswith("-")]
    killed = 0
    total = 0
    for name, path, old, new, test_args, why in PROBES:
        if wanted and name.split()[0] not in wanted:
            continue
        total += 1
        original = open(path).read()
        if original.count(old) != 1:
            print(f"SKIP  {name}: the anchor occurs {original.count(old)} times")
            continue
        open(path, "w").write(original.replace(old, new, 1))
        try:
            result = run(["go", "test", "./internal/service/federation/", "-count=1"] + test_args)
            if result.returncode == 0:
                print(f"SURVIVED  {name}\n          {why}")
            else:
                # The kill has to NAME the rule: the output of the failing test is the evidence, and a
                # build failure is not a verdict about behaviour.
                bad = "build failed" in result.stdout or "[build failed]" in result.stdout
                verdict = "BUILD-ERR" if bad else "KILLED"
                if not bad:
                    killed += 1
                print(f"{verdict}  {name}")
                if bad:
                    print(result.stdout[-800:])
        finally:
            open(path, "w").write(original)
    print(f"\n{killed}/{total} killed")
    return 0 if killed == total else 1


if __name__ == "__main__":
    sys.exit(main())
