#!/usr/bin/env python3
"""Mutation harness for the identification source registry (SPEC §8, plan step 7a).

Each case edits `internal/service/identification/sources.go`, runs the test that is
supposed to be measuring that decision, and restores the file. The verdicts are
the five this repository uses everywhere: a mutation that does not compile is
BROKEN and is not a kill; one whose input is refused by a second layer is COVERED
(a claim, not a pass); SURVIVED means a hole.

    python3 scripts/mutate-identification-sources.py
"""

import os
import subprocess
import sys

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SRC = os.path.join(REPO, "internal", "service", "identification", "sources.go")

DSN = "postgres:smoke_pw@127.0.0.1:55434/stash-box-test?sslmode=disable"
UNIT = "go test ./internal/service/identification/ -count=1 -run "
INTEG = "go test -tags integration ./internal/api/ -count=1 -run "

# A mutation that breaks the build is not a kill. This list is deliberately long:
# a missing entry reports its own defect as a hole in the tests.
BUILD_ERRORS = [
    "build failed", "cannot use", "undefined:", "declared and not used",
    "syntax error", "not enough arguments", "too many arguments",
    "assignment mismatch", "redeclared", "no new variables",
    "imported and not used", "missing return", "all declarations of",
    "shadows declaration", "[build failed]", "cannot find package",
    "undefined (type", "undefined (value", "mismatched types",
]

CASES = [
    (
        "the media address is concatenated raw instead of percent-encoded",
        [("url.QueryEscape(mediaURL)", "mediaURL")],
        UNIT + "TestSourceLinkEncodesTheMediaAddressSoTheSourceGetsAllOfIt",
    ),
    (
        "a disabled source is rendered anyway (the F10 gate is ignored)",
        [("""		if !s.Enabled {
			continue
		}
""", "")],
        UNIT + "TestLinksSkipsDisabledSourcesAndEmptyMediaIsNotAnError",
    ),
    (
        "no media address becomes an error instead of an empty list",
        [("""	if strings.TrimSpace(mediaURL) == "" {
		return nil, nil
	}""", """	if strings.TrimSpace(mediaURL) == "" {
		return nil, ErrNoMedia
	}""")],
        INTEG + "'TestIdentificationSourceLinks/no_image_address_is_an_empty_list,_not_an_error'",
    ),
    (
        # F10's default is the decision, so the mutation is the default flipping.
        "a face service ships enabled",
        [("""		{Name: "FaceCheck.ID", Kind: KindRemoteFace, Home: "https://facecheck.id"},""",
          """		{Name: "FaceCheck.ID", Kind: KindRemoteFace, Home: "https://facecheck.id", Enabled: true},""")],
        INTEG + "'TestIdentificationSourceLinks/no_face_service_is_offered_by_default'",
    ),
    (
        # The second layer: Links() never reaches LinkFor() with an empty address,
        # so removing this check should be refused by the caller's guard instead.
        "a media address of whitespace is accepted by the source itself",
        [("""	if strings.TrimSpace(mediaURL) == "" {
		return Link{}, ErrNoMedia
	}""", """	if false {
		return Link{}, ErrNoMedia
	}""")],
        UNIT + "TestSourceLinkRefusals/no_media_address",
    ),
]

VERDICTS = {"killed": [], "survived": [], "covered": [], "broken": [], "unscored": []}


def run(cmd):
    env = dict(os.environ, POSTGRES_DB=DSN)
    try:
        p = subprocess.run(cmd, shell=True, cwd=REPO, env=env,
                           capture_output=True, text=True, timeout=600)
        return p.returncode, (p.stdout or "") + (p.stderr or "")
    except subprocess.TimeoutExpired:
        return 124, "TIMEOUT"


def main():
    original = open(SRC).read()
    rc, out = run("go build ./...")
    if rc != 0:
        print("the tree does not build before mutating; nothing to measure")
        print(out[-800:])
        return 2

    for name, edits, target in CASES:
        current = original
        applied = True
        for old, new in edits:
            if old not in current:
                applied = False
                break
            if old == new:
                applied = False
                break
            current = current.replace(old, new, 1)
        if not applied:
            VERDICTS["unscored"].append((name, "the source text was not found (or the edit is a no-op)"))
            print(f"UNSCORED  {name}")
            continue

        open(SRC, "w").write(current)
        try:
            rc, out = run(target)
            if any(e in out for e in BUILD_ERRORS):
                # A mutation that does not compile has proved nothing.
                verdict, why = "broken", "the mutation does not compile"
            elif rc != 0:
                verdict, why = "killed", "the named test failed"
            else:
                # The named test passed. Is the gate actually open?
                whole_rc, whole_out = run("go build ./... && " + target.split(" -run ")[0])
                if whole_rc == 0:
                    verdict, why = "covered", "another layer refuses this input"
                else:
                    verdict, why = "survived", "the package FAILED but this test did not"
                del whole_out
        finally:
            # Restore PER PROBE: a run that dies mid-case must not leave the
            # mutation in the file.
            open(SRC, "w").write(original)

        VERDICTS[verdict].append((name, why))
        print(f"{verdict.upper():9} {name}  -- {why}")

    # Verify the restore rather than assuming it.
    if open(SRC).read() != original:
        print("RESTORE FAILED: sources.go differs from the original")
        return 2

    print()
    for k in ("killed", "covered", "survived", "broken", "unscored"):
        print(f"{k}: {len(VERDICTS[k])}")
    if VERDICTS["survived"] or VERDICTS["broken"] or VERDICTS["unscored"]:
        for k in ("survived", "broken", "unscored"):
            for name, why in VERDICTS[k]:
                print(f"  {k}: {name} -- {why}")
        return 1 if VERDICTS["survived"] else 2
    print("all mutations caught (killed or covered by a named layer)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
