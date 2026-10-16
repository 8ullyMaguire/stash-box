#!/usr/bin/env python3
"""Mutation battery for the image posture gate and the media directory (plan phase 10, slices 10a-ii and 10b-1).

    python3 scripts/mutate-image-gate.py [M1 M2 .. M11]

Every probe names the ONE test that must notice, runs it, and then -- step 5 of the procedure this
programme writes down -- RESTORES the file and runs the whole package again, so that "the neighbours stay
green" is a measurement rather than an assumption. A probe whose restored run is red has proved nothing
about the test it aimed at; a probe that comes back a BUILD ERROR is not a verdict either way.
"""

import os
import subprocess
import sys

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
GATE = os.path.join(REPO, "internal", "api", "routes_image.go")
PLANE = os.path.join(REPO, "internal", "service", "mediaplane", "mediaplane.go")
STORE = os.path.join(REPO, "internal", "service", "mediaplane", "store.go")
EXPORTS = os.path.join(REPO, "internal", "service", "mediaplane", "exports.go")

API = "./internal/api/"
MP = "./internal/service/mediaplane/"

PROBES = [
    (
        "M1 the middleware never refuses",
        GATE, API,
        "\t\tif err := mediaplane.RequireImageBytes(); err != nil {",
        "\t\tif err := mediaplane.RequireImageBytes(); err != nil && false {",
        "TestImageRoutesRefuseWhenNeitherSwitchIsOn",
        "the route serves image bytes whatever the switches say, which is the defect slice 10a-ii was "
        "written to close -- an instance carrying images_enabled: false and serving every image anyway. "
        "The first version of this probe deleted the call instead of neutering it and came back a BUILD "
        "ERROR (the import went unused), which is why the mutation keeps the call and falsifies the "
        "condition: a probe that does not compile is not a verdict",
    ),
    (
        "M2 the OR becomes an AND",
        PLANE, API,
        "return config.GetImagesEnabled() || DeliveryEnabled()",
        "return config.GetImagesEnabled() && DeliveryEnabled()",
        "TestImageGateOpensWhenEitherSwitchIsOn",
        "an operator who has accepted content delivery is then refused images, and the pair becomes a "
        "puzzle rather than two ways of saying the same thing",
    ),
    (
        "M3 the refusal stops naming the keys",
        PLANE, API,
        '"this instance does not serve image bytes (`images_enabled` and `content_delivery` are both off)"',
        '"this instance does not serve image bytes"',
        "TestImageRoutesRefuseWhenNeitherSwitchIsOn",
        "the operator reads that images are off with no way to find the line to change, which is the "
        "reason the message names the key at all",
    ),
    (
        "M4 the gate stops consulting the storage",
        PLANE, MP,
        "\treturn MediaDirError()\n}",
        "\treturn nil\n}",
        "TestTheGateFollowsTheMediaDirBothWays",
        "the switch is on and the directory does not exist: every image request answers 404 from a "
        "handler with nothing to read, which is indistinguishable from an instance that has no such "
        "image -- the state slice 10b-1 exists to make visible. TestTheSwitchIsReportedBeforeTheStorage "
        "stays green, which is the evidence this probe aimed where it says it did",
    ),
    (
        "M5 the export check becomes a string prefix",
        EXPORTS, MP,
        "if abs == ex || strings.HasPrefix(abs, ex+string(filepath.Separator)) {",
        "if abs == ex || strings.HasPrefix(abs, ex) {",
        "TestASiblingWhoseNameStartsWithAnExportIsNotInsideIt",
        "/mnt/disk-important/backups2 is then called inside the export /mnt/disk-important/backups, so "
        "the invariant refuses a media directory that is shared with nobody -- a check that refuses the "
        "wrong thing is as wrong as one that misses",
    ),
    (
        "M6 the storage is reported before the posture",
        PLANE, MP,
        "\tif !ImageBytesEnabled() {\n\t\treturn ErrNoImageBytes\n\t}\n\treturn MediaDirError()",
        "\tif err := MediaDirError(); err != nil {\n\t\treturn err\n\t}\n\tif !ImageBytesEnabled() {\n\t\treturn ErrNoImageBytes\n\t}\n\treturn nil",
        "TestTheSwitchIsReportedBeforeTheStorage",
        "an operator who never enabled images is told their media directory is missing, which sends them "
        "to fix the second of two things they have not agreed to -- the same rule as the gate running "
        "before the uuid is parsed",
    ),
    (
        "M7 whitespace stops counting as unset",
        STORE, MP,
        'if strings.TrimSpace(dir) == "" {',
        'if strings.TrimSpace(dir) == "" && false {',
        "TestAWhitespaceOnlyMediaDirIsTreatedAsUnset",
        "media_dir: \" \" becomes a path that stats as missing, so an operator who left the key blank "
        "reads a directory problem they cannot see in their own config",
    ),
    (
        "M8 a directory that cannot be read is accepted",
        STORE, MP,
        '\tif err != nil {\n\t\treturn ErrMediaDirUnusable{Path: abs, Reason: "the directory cannot be read: " + err.Error()}\n\t}',
        '\tif err != nil && false {\n\t\treturn ErrMediaDirUnusable{Path: abs, Reason: "the directory cannot be read: " + err.Error()}\n\t}',
        "TestADirectoryThatCannotBeReadIsRefused",
        "stat succeeds on mode 000, so a check that only stats accepts a directory nothing can be served "
        "from",
    ),
    (
        "M9 the unusable states stop naming the path and the reason",
        STORE, MP,
        'return ErrMediaDirUnusable{Path: abs, Reason: "the path does not exist"}',
        'return ErrMediaDirUnusable{Path: "", Reason: "unusable"}',
        "TestTheThreeUnusableStatesNameThePathAndTheReason",
        "an operator with several instances cannot tell which config to open, and cannot tell a missing "
        "directory from a file where a directory was expected",
    ),
    (
        "M10 the startup line stops carrying the storage state",
        PLANE, MP,
        '\tif err := MediaDirError(); err != nil {\n\t\treturn "Image bytes: ENABLED BUT REFUSED AT THE STORAGE',
        '\tif err := MediaDirError(); err != nil && false {\n\t\treturn "Image bytes: ENABLED BUT REFUSED AT THE STORAGE',
        "TestTheImageServingLineNamesAllThreeStates",
        "an instance whose switch is on and whose directory is missing logs the same line as a working "
        "one -- the log is where an operator finds out which posture they are in",
    ),
    (
        "M11 the export parser reads the wrapped client line as a path",
        EXPORTS, MP,
        "\t\t\tcontinue // an indented continuation: the client list of the entry above",
        "\t\t\t_ = line",
        "TestParsingExportfsCountsItsSubjects",
        "one export in the real table is wrapped, so the client address on the next line is read as an "
        "exported path -- six paths instead of four, which is how an invariant ends up checking a list "
        "that is not the list",
    ),
]


def run(pkg, test=None):
    args = ["go", "test", pkg, "-count=1"]
    if test:
        args += ["-run", test]
    return subprocess.run(args, cwd=REPO, capture_output=True, text=True, timeout=900)


def main():
    wanted = [a for a in sys.argv[1:] if not a.startswith("-")]
    total = killed = 0
    for name, path, pkg, old, new, test, why in PROBES:
        if wanted and name.split()[0] not in wanted:
            continue
        total += 1
        original = open(path).read()
        if original.count(old) != 1:
            print(f"SKIP  {name}: the anchor occurs {original.count(old)} times")
            continue
        open(path, "w").write(original.replace(old, new, 1))
        try:
            result = run(pkg, test)
            if "no tests to run" in (result.stdout or ""):
                # THE HARNESS'S OWN DEFECT, found by M2 and M3 reporting SURVIVED against tests that go red
                # when the same mutation is applied by hand. `go test -run` against a package that does not
                # contain the named test prints "no tests to run" and exits ZERO, so a probe aimed at the
                # wrong package reads exactly like a test that failed to notice. It is the same shape as a
                # shell pipeline reporting grep's status, and it is the one way this script could lie.
                print(f"HARNESS-BUG  {name}: `go test {pkg} -run {test}` ran NO TEST (exit 0)")
                print("          the probe names a test this package does not contain; fix the probe")
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
        # Step 5 of the procedure, measured rather than assumed: restored, the whole package is green.
        back = run(pkg)
        if back.returncode != 0:
            print(f"RESTORE-FAIL  {name}: {pkg} is red after restoring the file")
            print(back.stdout[-800:])
            return 1
    print(f"\n{killed}/{total} killed, every restore green")
    return 0 if killed == total and total else 1


if __name__ == "__main__":
    sys.exit(main())
