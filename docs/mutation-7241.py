#!/usr/bin/env python3
"""Mutation gate for the SPEC §7.24.1 XP rule.

§7.24.1: "No XP for asserting one on an entity you just edited. Otherwise verified-unknown
becomes the cheapest XP-per-minute farm in the system — the same shape §7.20 refused for
vanguards, applied here for the same reason."

A rule with no test is a comment. This gate exists to prove each clause of the predicate can
fail, because the failure mode here is a farm that pays out silently and looks like generous
curation rather than a bug.

A stale anchor returns non-zero BEFORE any count prints: a probe file that skips its broken
entries under-reports as "all killed", which is the worst outcome because it looks like
success.
"""
import pathlib
import subprocess
import sys

ROOT = pathlib.Path("/home/alvaro/code-local/go/stash-box")
SRC = ROOT / "internal/service/edit/verified_unknown_xp.go"
ORIGINAL = SRC.read_text()

MUTATIONS = [
    ("no-comparison-at-all",
     "\treturn recentEditor == assertedAbout",
     "\treturn false",
     "the rule is gone: every self-affirming assertion pays XP, which is the farm"),
    ("always-true",
     "\treturn recentEditor == assertedAbout",
     "\treturn true",
     "the rule inverts into 'nobody may ever earn XP for an assertion', so a verified-unknown " +
     "is never worth anything and the feature is unused -- a farm is the opposite failure, " +
     "but this one is just as wrong and just as quiet"),
    # NOT PROBES -- removed as provably equivalent, with the check recorded inline.
    #
    # `recentEditor == assertedAbout` vs `assertedAbout == recentEditor`: equality is
    # symmetric, so no input distinguishes them. Verified over Nil/Nil, Nil/uuid, uuid/Nil
    # and four uuid pairs -- 0 mismatches. A probe that cannot fail either fakes a gap or
    # gets counted as a kill nobody earned.
    #
    # Dropping the `assertedAbout == uuid.Nil` half of the guard: the editor half fires first
    # for Nil/Nil, and for editor!=Nil with assertedAbout==Nil the comparison is Nil==Nil,
    # which is false anyway -- the same answer the guard gives. Verified over the same 12
    # cases, 0 mismatches. The guard is written with both halves anyway, because
    # "reason this check fired" is easier to read one condition at a time.

    ("zero-uuid-guard-removed",
     "\tif recentEditor == uuid.Nil || assertedAbout == uuid.Nil {\n\t\treturn false\n\t}",
     "\tif false {\n\t\treturn false\n\t}",
     "two independently-missing ids now compare equal, so every anonymous edit looks " +
     "self-affirming. Those edits earn no XP anyway, but the predicate stops being the " +
     "thing that stops them, and it is a reusable helper"),
    ("carries-check-inverted",
     "\treturn len(data.New.VerifiedUnknowns) > 0",
     "\treturn len(data.New.VerifiedUnknowns) == 0",
     "the rule fires on ordinary edits and not on assertions: every ordinary edit looks " +
     "self-affirming and earns nothing, and the farm is wide open"),
    ("nil-new-not-guarded",
     "\tif data == nil || data.New == nil {\n\t\treturn false\n\t}",
     "\tif false {\n\t\treturn false\n\t}",
     "an edit with no new_data panics on the XP path, taking down curation"),
    ("rule-inverted-in-the-combined-call",
     "\treturn !selfAffirming(recentEditor, assertedAbout)",
     "\treturn selfAffirming(recentEditor, assertedAbout)",
     "the combined decision inverts: XP is refused for assertions about OTHER people's " +
     "entities and paid for self-affirming ones, which is precisely backwards"),
    ("ordinary-edit-also-refused",
     "\tif !editCarriesVerifiedUnknown(data) {\n\t\treturn true // an ordinary edit: nothing about §7.24.1 applies\n\t}",
     "\tif false {\n\t\treturn true\n\t}",
     "an ordinary edit carrying no assertions falls through to selfAffirming, so a curator " +
     "editing their own entity stops earning XP for it -- every self-edit becomes worthless"),
]


def run_tests():
    proc = subprocess.run(
        ["go", "test", "./internal/service/edit/", "-run",
         "VerifiedUnknown|SelfAffirming|AssertionEarns", "-count=1"],
        cwd=ROOT, capture_output=True, text=True,
    )
    out = proc.stdout + proc.stderr
    # A compile error is not a behavioural kill: it means the mutant cannot be tested.
    if "build failed" in out or "cannot use" in out or "undefined" in out:
        return "COMPILE_ERROR", out
    return proc.returncode == 0, out


def main():
    survivors, broken = [], []
    for label, find, replace, why in MUTATIONS:
        if find not in ORIGINAL:
            print(f"BROKEN ANCHOR  {label}: no longer in source")
            broken.append(label)
            continue
        SRC.write_text(ORIGINAL.replace(find, replace, 1))
        try:
            ok, _ = run_tests()
        finally:
            SRC.write_text(ORIGINAL)
        if ok == "COMPILE_ERROR":
            print(f"BROKEN PROBE  {label}: the mutant does not compile")
            broken.append(label)
        elif ok:
            print(f"SURVIVED  {label}: {why}")
            survivors.append((label, why))
        else:
            print(f"killed    {label}")

    total = len(MUTATIONS)
    print()
    if broken:
        print(f"{len(broken)} broken -- the probe file is stale, fix before reading any "
              f"kill count")
        return 1
    if survivors:
        print(f"{len(survivors)} survived of {total} -- a real gap, not a masked one")
        return 1
    print(f"all {total} mutations killed")
    return 0


if __name__ == "__main__":
    sys.exit(main())