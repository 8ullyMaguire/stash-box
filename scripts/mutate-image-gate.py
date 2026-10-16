#!/usr/bin/env python3
"""Mutation battery for the image posture gate (plan phase 10, slice 10a-ii).

    python3 scripts/mutate-image-gate.py [M1 M2 M3 ...]
"""

import os
import subprocess
import sys

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
GATE = os.path.join(REPO, "internal", "api", "routes_image.go")
PLANE = os.path.join(REPO, "internal", "service", "mediaplane", "mediaplane.go")

PROBES = [
    (
        "M1 the middleware never refuses",
        GATE,
        "\t\tif err := mediaplane.RequireImageBytes(); err != nil {",
        "\t\tif err := mediaplane.RequireImageBytes(); err != nil && false {",
        ["-run", "TestImageRoutesRefuseWhenNeitherSwitchIsOn"],
        "the route serves image bytes whatever the switches say, which is the defect this slice was "
        "written to close -- an instance carrying images_enabled: false and serving every image anyway. "
        "The first version of this probe deleted the call instead of neutering it and came back a BUILD "
        "ERROR (the import went unused), which is why the mutation keeps the call and falsifies the "
        "condition: a probe that does not compile is not a verdict",
    ),
    (
        "M2 the OR becomes an AND",
        PLANE,
        "return config.GetImagesEnabled() || DeliveryEnabled()",
        "return config.GetImagesEnabled() && DeliveryEnabled()",
        ["-run", "TestImageGateOpensWhenEitherSwitchIsOn"],
        "an operator who has accepted content delivery is then refused images, and the pair becomes a "
        "puzzle rather than two ways of saying the same thing",
    ),
    (
        "M3 the refusal stops naming the keys",
        PLANE,
        '"this instance does not serve image bytes (`images_enabled` and `content_delivery` are both off)"',
        '"this instance does not serve image bytes"',
        ["-run", "TestImageRoutesRefuseWhenNeitherSwitchIsOn"],
        "the operator reads that images are off with no way to find the line to change, which is the "
        "reason the message names the key at all",
    ),
]


def run(args):
    return subprocess.run(["go", "test", "./internal/api/", "-count=1"] + args, cwd=REPO,
                          capture_output=True, text=True, timeout=900)


def main():
    wanted = [a for a in sys.argv[1:] if not a.startswith("-")]
    total = killed = 0
    for name, path, old, new, args, why in PROBES:
        if wanted and name.split()[0] not in wanted:
            continue
        total += 1
        original = open(path).read()
        if original.count(old) != 1:
            print(f"SKIP  {name}: the anchor occurs {original.count(old)} times")
            continue
        open(path, "w").write(original.replace(old, new, 1))
        try:
            result = run(args)
            if result.returncode == 0:
                print(f"SURVIVED  {name}\n          {why}")
                print("          the test did not notice, which is a statement about the test")
            elif "build failed" in result.stdout or "[build failed]" in result.stdout:
                print(f"BUILD-ERR  {name} (a lint trip is not a survivor and not a kill)")
                print(result.stdout[-600:])
            else:
                killed += 1
                print(f"KILLED  {name}")
        finally:
            open(path, "w").write(original)
    print(f"\n{killed}/{total} killed")
    return 0 if killed == total and total else 1


if __name__ == "__main__":
    sys.exit(main())
