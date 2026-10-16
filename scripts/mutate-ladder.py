#!/usr/bin/env python3
"""Mutation battery for the variant ladder's storage (plan phase 10, slice 10c-1).

    python3 scripts/mutate-ladder.py [M1 M2 .. M5]

Same procedure as mutate-image-gate.py: every probe names the ONE test that must notice, the whole package is
re-run after each restore, and a probe whose run says "no tests to run" stops the battery as a HARNESS BUG --
`go test -run` exits ZERO when the package does not contain the named test, so a harness that reads only the
exit code cannot tell "the test did not notice" from "the test never ran".

M1 IS THE PROBE WORTH KEEPING. It re-introduces the defect this slice actually had -- VariantWidths taken from
the storage measurement (320, 480) instead of read off the route (300, 600, 1280) -- and it is killed by the
subset invariant, which is the test that exists because of it. A probe that reproduces the mistake is worth
more than one that merely breaks the code.
"""

import os
import subprocess
import sys

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
LADDER = os.path.join(REPO, "internal", "service", "mediaplane", "ladder.go")
ROUTE = os.path.join(REPO, "internal", "api", "routes_image.go")
MEDIAPLANE = "./internal/service/mediaplane/"
API = "./internal/api/"

PROBES = [
    (
        "M1 the stored widths are the ones the measurement suggested, not the ones the route accepts",
        LADDER, MEDIAPLANE,
        "var VariantWidths = []int{300, 600}",
        "var VariantWidths = []int{320, 480}",
        "TestEveryStoredVariantWidthIsOneTheRouteCanAskFor",
        "the producer would write files no request can ask for; nothing outside the instance could notice",
        API,
    ),
    (
        "M2 every width is a variant width",
        LADDER, MEDIAPLANE,
        "	for _, w := range VariantWidths {\n\t\tif w == width {\n\t\t\treturn true\n\t\t}\n\t}\n\treturn false",
        "	return width > 0",
        "TestOpenVariantAnswersWithBytesOrWithNothing",
        "the ladder answers for width 1280, so the fall-through to the instance's own image path is unreachable "
        "for exactly the large view that depends on it",
    ),
    (
        "M3 the two directories may overlap",
        LADDER, MEDIAPLANE,
        "if cache == dir || strings.HasPrefix(cache+string(filepath.Separator), dir+string(filepath.Separator)) ||",
        "if cache == dir && false ||",
        "TestTheResizeCacheMustNotOverlapTheMediaDir",
        "one cleanup of the resize cache takes the instance's images with it, and nothing says so until it "
        "happens",
    ),
    (
        "M4 the filesystem is not re-checked",
        LADDER, MEDIAPLANE,
        "	if now != s.device {\n\t\treturn ErrMountChanged\n\t}",
        "	if now != s.device && false {\n\t\treturn ErrMountChanged\n\t}",
        "TestAMountThatChangedIsRefused",
        "an unmounted disk leaves an ordinary empty directory behind, so the instance serves 404 for every "
        "image while its startup line still claims it is serving",
    ),
    (
        "M5 the variant header stops being cacheable",
        ROUTE, API,
        'w.Header().Set("Cache-Control", "max-age=604800000")',
        'w.Header().Set("Cache-Control", "no-store")',
        "TestAStoredVariantIsServedWithoutTouchingTheDatabase",
        "every client revalidates bytes that cannot change, on a store whose whole purpose is to avoid doing "
        "work twice",
    ),
    (
        "M6 the content type table says every format is webp",
        LADDER, API,
        '\tVariantFormatJPEG: "image/jpeg",',
        '\tVariantFormatJPEG: "image/webp",',
        "TestAStoredJPEGVariantIsServedAsJPEG",
        "a jpeg is served as webp, which the browser renders as a broken image -- and the webp fixtures alone "
        "would never have shown it",
    ),
    (
        "M7 the format is declared instead of sniffed",
        LADDER, MEDIAPLANE,
        "format, ok := SniffVariantFormat(data)",
        "format, ok := VariantFormatWebP, true",
        "TestProducingAVariantUsesTheInstancesOwnPipeline",
        "an upstream answer in an unexpected shape is written into the media directory under an extension it "
        "does not have, where everything that exists is served",
    ),
    (
        "M8 the sniffer says everything is webp",
        LADDER, MEDIAPLANE,
        'if len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP" {',
        "if len(data) >= 12 {",
        "TestProducingRefusesBytesThatAreNotAPicture",
        "an HTML error page or an SVG is stored as a webp and served as one",
    ),
]

# NOT PROBED, and said out loud rather than implied: the atomic write (temp file + rename) is asserted by
# construction. A probe would have to interrupt the process mid-write, and a test that only checks "the temp
# file is gone afterwards" passes for a direct write as well -- an absence assertion whose subject never
# existed.


def run(pkg, test=None, repo=REPO):
    args = ["go", "test", pkg, "-count=1"]
    if test:
        args += ["-run", test]
    return subprocess.run(args, cwd=repo, capture_output=True, text=True, timeout=900)


def main():
    wanted = [a for a in sys.argv[1:] if not a.startswith("-")]
    total = killed = 0
    for name, path, pkg, old, new, test, why, *rest in PROBES:
        test_pkg = rest[0] if rest else pkg
        if wanted and name.split()[0] not in wanted:
            continue
        total += 1
        original = open(path).read()
        if original.count(old) != 1:
            print(f"SKIP  {name}: the anchor occurs {original.count(old)} times")
            continue
        open(path, "w").write(original.replace(old, new, 1))
        try:
            result = run(test_pkg, test)
            if "no tests to run" in (result.stdout or ""):
                print(f"HARNESS-BUG  {name}: `go test {test_pkg} -run {test}` ran NO TEST (exit 0)")
                return 2
            if result.returncode == 0:
                print(f"SURVIVED  {name}\n          {why}")
                print("          the test did not notice, which is a statement about the test")
            elif "build failed" in result.stdout or "[build failed]" in result.stdout:
                print(f"BUILD-ERR  {name} (a lint trip is not a survivor and not a kill)")
                print(result.stdout[-600:])
            else:
                killed += 1
                print(f"KILLED  {name}  ({test})")
        finally:
            open(path, "w").write(original)
        for p in {pkg, test_pkg}:
            back = run(p)
            if back.returncode != 0:
                print(f"RESTORE-FAIL  {name}: {p} is red after restoring the file")
                print(back.stdout[-800:])
                return 1
    print(f"\n{killed}/{total} killed, every restore green")
    return 0 if killed == total and total else 1


if __name__ == "__main__":
    sys.exit(main())
