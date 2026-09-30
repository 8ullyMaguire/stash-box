#!/usr/bin/env python3
"""Mutation harness for the R074 receiving guard.

Reads a JSON spec so that backticks and Go regex literals need no escaping --
an earlier version of this file inlined them and could not parse at all, which
is the same class of error as a mutation that does not apply.

Every mutation must actually CHANGE the file before tests run, and the file is
restored in a finally block. The project's WORKLOG records two mutations
reporting SURVIVED when a python replace had silently not matched
gofmt-aligned source; this harness reports those as NOT APPLIED instead, which
is the honest outcome.

Usage: mutate_r074.py <repo-root> <spec.json>
"""
import json
import os
import subprocess
import sys

REPO = sys.argv[1]
SPEC = json.load(open(sys.argv[2]))
PKG = SPEC["package"]


def regenerate():
    """Re-run gqlgen so a schema mutation actually reaches the compiled code.

    Without this, mutating a .graphql file is scored against code that was
    generated from the ORIGINAL schema, so every schema mutation would report
    SURVIVED for the wrong reason -- the guard was never weakened, only the
    source of the thing being tested.
    """
    r = subprocess.run(["go", "run", "github.com/99designs/gqlgen"],
                       cwd=REPO, capture_output=True, text=True, env=os.environ)
    return r.returncode == 0, r.stdout + r.stderr


def run_tests(regex):
    # -tags is needed for integration specs: the wiring tests live behind
    # //go:build integration, and without the tag the mutation would be scored
    # against a suite that never compiled the file -- every mutation would
    # "survive" for the wrong reason.
    cmd = ["go", "test", "./" + PKG, "-run", regex, "-count=1"]
    if SPEC.get("tags"):
        cmd.insert(2, "-tags=" + SPEC["tags"])
    r = subprocess.run(cmd, cwd=REPO, capture_output=True, text=True, env=os.environ)
    return r.returncode, r.stdout + r.stderr


def main():
    code, out = run_tests(SPEC["baseline"])
    if code != 0:
        print("BASELINE RED -- aborting, mutations would prove nothing")
        print(out[-3000:])
        return 1
    print("BASELINE GREEN")

    survivors = []
    # Files mutated across the whole run, so the generated code and go.mod are
    # restored exactly as they were.
    touched = {}

    def restore_all():
        for path, content in touched.items():
            open(path, "w").write(content)
        regenerate()

    for m in SPEC["mutations"]:
        path = os.path.join(REPO, PKG, m["file"])
        if path not in touched:
            touched[path] = open(path).read()
        original = touched[path]

        if m["old"] not in original:
            print("NOT APPLIED (text not found): " + m["label"])
            survivors.append(m["label"] + " [edit did not land]")
            continue

        # A replace(..., 1) on text that appears in TWO functions mutates the
        # first one and the harness then scores the result against a test for the
        # second. That is a silent misattribution: the survivor it reports is
        # about code the mutation never touched. This actually happened here --
        # Create and Update carried byte-identical trust-weight checks, so the
        # "Update accepts weight 0" mutation was applied to Create.
        #
        # So a mutation whose text is ambiguous is REFUSED, and the spec must
        # anchor on text unique to the function under test. Forcing a choice here
        # would mean guessing which occurrence was meant, and a mutation scored
        # against the wrong function is worse than no mutation at all.
        if original.count(m["old"]) > 1:
            print("NOT APPLIED (ambiguous text, %d occurrences): %s"
                  % (original.count(m["old"]), m["label"]))
            survivors.append(m["label"] + " [edit was ambiguous]")
            continue

        mutated = original.replace(m["old"], m["new"], 1)
        if mutated == original:
            print("NOT APPLIED (no change): " + m["label"])
            survivors.append(m["label"] + " [edit did not land]")
            continue

        open(path, "w").write(mutated)
        # A migration mutation needs no codegen -- the test harness re-runs every
        # migration on entry to each package, so a changed .sql file takes effect
        # on the next go test. Nothing extra to do.
        needs_codegen = path.endswith(".graphql")
        if needs_codegen:
            ok, out = regenerate()
            if not ok:
                print("NOT APPLIED (codegen failed): " + m["label"])
                print(out[-1500:])
                restore_all()
                return 1
        try:
            code, _ = run_tests(m["test"])
        finally:
            open(path, "w").write(original)
            if needs_codegen:
                regenerate()

        if code != 0:
            print("KILLED   " + m["label"])
        else:
            print("SURVIVED " + m["label"])
            survivors.append(m["label"])

    restore_all()
    print()
    if survivors:
        print(str(len(survivors)) + " survivor(s):")
        for s in survivors:
            print("  - " + s)
        return 1
    print("all " + str(len(SPEC["mutations"])) + " mutations killed")
    return 0


if __name__ == "__main__":
    sys.exit(main())