#!/usr/bin/env python3
"""Mutation battery for the image pass (docs/plan/feature-sync-service.md, step 4).

    python3 scripts/mutate-images.py [M1 M2 .. M7]

WHY THIS ONE DOES NOT NEED A DATABASE. The unit under test is the FETCHER's decisions -- what counts as an
image, what counts as polite, what a refusal is -- and every one of those is a decision this package can be
asked about with an httptest server. The storing half goes through the destination's own image service, which is
exercised live on thinkcentre instead, because that is where the destination's database and media directory are.

Same procedure as the other batteries: each probe names the ONE test that must notice, the whole package is
re-run after each restore, and a run that says "no tests to run" is a HARNESS BUG rather than a survivor --
`go test -run` exits ZERO when the package does not contain the named test.

EVERY PROBE BELOW WAS CHOSEN BECAUSE IT COULD HAVE BEEN LEFT OUT OF THE CODE. The two decode probes exist
because the first version of the test suite could not tell a whole image from half of one: the truncated-PNG
case was caught by the content-type check instead, so deleting the decoder left the suite green. A mutation that
survives usually means the SUBJECT is missing, not the assertion.
"""

import os
import subprocess
import sys

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
IMAGES = os.path.join(REPO, "internal", "sdbimport", "images.go")
ROBOTS = os.path.join(REPO, "internal", "sdbimport", "robots.go")
PKG = "./internal/sdbimport/"

PROBES = [
    (
        "M1 the content-type check is dropped, so an HTML error page is stored as a picture",
        IMAGES,
        '\tif !strings.HasPrefix(kind, "image/") {',
        '\tif false && !strings.HasPrefix(kind, "image/") {',
        "TestAFileThatIsNotAnImageIsRefusedByName",
        "every CDN that answers 200 with an HTML error page puts a page into the images table, and the card "
        "that renders it is blank with no error anywhere",
    ),
    (
        "M2 the decode is weakened to DecodeConfig, which reads the header and not the pixels",
        IMAGES,
        "\tif _, _, err := image.Decode(bytes.NewReader(body)); err != nil {",
        "\tif _, _, err := image.DecodeConfig(bytes.NewReader(body)); err != nil {",
        "TestATruncatedImageIsRefusedByTheDecoderAndNotByTheSniffer",
        "HALF an image passes: the sniffer says image/png and the header parses, so a truncated download is "
        "stored as a picture and every later reader fails on it instead of this one record being skipped",
    ),
    (
        "M3 the size cap is checked but an over-cap body is stored anyway",
        IMAGES,
        "\tif int64(len(body)) > f.MaxBytes {\n\t\treturn nil, fmt.Errorf(\"%w: %s is larger than %d bytes\", ErrImageRefused, raw, f.MaxBytes)\n\t}",
        "\tif false {\n\t\treturn nil, fmt.Errorf(\"%w: %s is larger than %d bytes\", ErrImageRefused, raw, f.MaxBytes)\n\t}\n\t_ = f.MaxBytes",
        "TestAnImageLargerThanTheCapIsRefused",
        "a host that answers with a stream reads until the connection closes, which is how a mirror becomes a "
        "decompression bomb's first victim",
    ),
    (
        "M4 a path disallow is cached as a refusal of the whole HOST",
        IMAGES,
        "\t\treturn fmt.Errorf(\"%w: %s: robots.txt disallows %s\", ErrImageRefused, u.Host, u.Path)",
        "\t\terr := fmt.Errorf(\"%w: %s: robots.txt disallows %s\", ErrImageRefused, u.Host, u.Path)\n\t\tf.refuse(u.Host, err)\n\t\treturn err",
        "TestARobotsDisallowRefusesAndIsAskedOnlyOnce",
        "one disallowed prefix makes a host's every other image unfetchable for the rest of the run -- this "
        "exact mutation WAS the first version of this code, and the test's allowed-path case is what caught it",
    ),
    (
        "M5 an unreadable robots.txt is treated as permission",
        IMAGES,
        "\t\treturn fmt.Errorf(\"%w: %s: robots.txt could not be read: %v\", ErrImageRefused, u.Host, err)",
        "\t\treturn nil // MUTANT: proceed",
        "TestARobotsThatCannotBeReadIsARefusal",
        "the safe reading of \"I could not ask\" is \"do not proceed\"; treating an error as consent is the "
        "behaviour robots.txt exists to prevent",
    ),
    (
        "M6 an unknown source type is passed through instead of falling back",
        IMAGES,
        "\t\tif knownImageTypes[models.ImageTypeEnum(key)] {",
        "\t\tif true {",
        "TestImageTypesComeFromTheSourceAndFallBackToDimensions",
        "the destination's validator refuses the whole image for a type it does not define, so one unknown "
        "string from the source silently loses the picture",
    ),
    (
        "M7 the per-host interval is ignored",
        IMAGES,
        "\t\tif wait := f.Interval - time.Since(last); wait > 0 {",
        "\t\tif wait := 0*time.Second - time.Since(last); wait > 0 {",
        "TestTheIntervalIsPerHostAndNotPerRequest",
        "a walk of a hundred thousand records becomes a hundred thousand concurrent requests to one community "
        "asset host",
    ),
]


def run(test=None):
    args = ["go", "test", "-count=1", PKG]
    if test:
        args += ["-run", test]
    return subprocess.run(args, cwd=REPO, capture_output=True, text=True, timeout=900)


def main():
    wanted = [a for a in sys.argv[1:] if not a.startswith("-")]
    total = killed = 0
    for name, path, old, new, test, why in PROBES:
        if wanted and name.split()[0] not in wanted:
            continue
        total += 1
        original = open(path).read()
        if original.count(old) != 1:
            print(f"SKIP  {name}: the anchor occurs {original.count(old)} times in {os.path.basename(path)}")
            continue
        open(path, "w").write(original.replace(old, new, 1))
        try:
            result = run(test)
            out = (result.stdout or "") + (result.stderr or "")
            if "no tests to run" in out:
                print(f"HARNESS-BUG  {name}: `go test -run {test}` ran NO TEST (exit 0)")
                return 2
            if "[build failed]" in out or "build failed" in out:
                print(f"BUILD-ERR  {name} (a lint trip is not a survivor and not a kill)")
                print(out[-400:])
            elif result.returncode == 0:
                print(f"SURVIVED  {name}\n          {why}")
                print("          the test did not notice, which is a statement about the test")
            else:
                killed += 1
                print(f"KILLED  {name}  ({test})")
        finally:
            open(path, "w").write(original)
        back = run()
        if back.returncode != 0:
            print(f"RESTORE-FAIL  {name}: the package is red after restoring the file")
            print(((back.stdout or "") + (back.stderr or ""))[-700:])
            return 1
    print(f"\n{killed}/{total} killed, every restore green")
    return 0 if killed == total and total else 1


if __name__ == "__main__":
    sys.exit(main())
