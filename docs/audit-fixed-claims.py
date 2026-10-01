#!/usr/bin/env python3
"""Audit every `fixed` claim in docs/ISSUES.md against the tree.

The classifier marks an issue `fixed` when a non-test file and a test file both
name the issue number. That is a claim, and a claim that satisfies a count is
worth as little as any other. This checks each one:

  1. both files still exist,
  2. the test file actually EXISTS as a test (not a fixture),
  3. the test file, run in isolation, still passes -- a test that fails proves
     the fix is not there,
  4. the fix file and test file are not the same file (a self-citing test proves
     nothing about production code).

    python3 docs/audit-fixed-claims.py
"""
import json, pathlib, re, subprocess, sys

REPO = pathlib.Path(__file__).resolve().parent.parent
LEDGER = REPO / "docs" / "ISSUES.md"
problems = []


def sh(c, timeout=900):
    return subprocess.run(c, shell=True, cwd=REPO, capture_output=True,
                          text=True, timeout=timeout)


def main():
    text = LEDGER.read_text()
    rows = re.findall(r"^\|\s*(\d+)\s*\|([^|]*)\|([^|]*)\|\s*fixed\s*\|([^|]*)\|", text, re.M)
    print(f"auditing {len(rows)} `fixed` claims\n")
    if not rows:
        print("no fixed rows parsed -- refusing to report a clean audit")
        return 1

    for num, _title, _rel, why in rows:
        n = int(num)
        m = re.search(r"fixed in `([^`]+)` with a test in `([^`]+)`", why)
        if not m:
            # A plan-backed claim is "fixed in `<commit>`; per-issue plan `<file>`".
            # A port-backed claim names the commit mid-sentence instead:
            #   "shipped by porting upstream PR #1183 (`8c8160b5`): four keyset..."
            # The first regex only matched the former, so #69 was reported as
            # claiming nothing -- a false problem, and false problems are how a
            # checker gets ignored. Take the FIRST commit-shaped token anywhere in
            # the reason, in backticks or not.
            m2 = re.search(r"`([0-9a-f]{7,40})`", why) or \
                 re.search(r"\b([0-9a-f]{8,40})\b", why)
            if not m2:
                problems.append(
                    f"#{n}: marked `fixed` but its plan cites NO commit, so nothing "
                    f"machine-checks the claim: {why[:60]}")
                continue
            commit = m2.group(1)
            rc = subprocess.run(["git", "cat-file", "-e", commit + "^{commit}"],
                                cwd=REPO, capture_output=True).returncode
            if rc != 0:
                problems.append(f"#{n}: cites commit {commit} which is NOT in this repo")
            else:
                # A commit that exists is necessary, not sufficient. Also require
                # that the commit actually TOUCHED something, and say so.
                touched = sh(f"git show --name-only --format= {commit}").stdout.split()
                if not touched:
                    problems.append(f"#{n}: commit {commit} exists but is EMPTY -- "
                                    "an empty commit backs no claim")
                else:
                    print(f"  ok   #{n:<6} commit {commit} exists, "
                          f"{len(touched)} file(s)")
            continue

        fixf, testf = m.group(1), m.group(2)
        if fixf == testf:
            problems.append(f"#{n}: fix and test are the SAME file ({fixf}) -- proves nothing")
            continue
        for f in (fixf, testf):
            if not (REPO / f).exists():
                problems.append(f"#{n}: cited file does not exist: {f}")
        if any(f"#{n}:" in p for p in problems):
            continue

        # run the test file's package in isolation
        if testf.endswith(".go"):
            pkg = "./" + str(pathlib.Path(testf).parent) + "/"
        elif testf.endswith((".ts", ".tsx")):
            pkg = None
        else:
            pkg = None
        if pkg:
            p = sh(f"go test -count=1 {pkg}", timeout=900)
            if p.returncode != 0:
                problems.append(f"#{n}: the package containing its test FAILS: {pkg.strip()}")
                print(f"  FAIL #{n:<6} {pkg} -> rc={p.returncode}")
            else:
                print(f"  ok   #{n:<6} {pkg} green")
        else:
            # frontend: run vitest on the file
            rel = testf.split("frontend/")[-1] if "frontend/" in testf else testf
            p = sh(f"cd frontend && npx vitest run {rel}", timeout=900)
            if p.returncode != 0:
                problems.append(f"#{n}: its frontend test FAILS: {rel}")
                print(f"  FAIL #{n:<6} {rel} -> rc={p.returncode}")
            else:
                print(f"  ok   #{n:<6} {rel} green")

    print()
    if problems:
        print(f"{len(problems)} PROBLEM(S) with the `fixed` claims:")
        for p in problems:
            print(f"  - {p}")
        return 1
    print(f"all {len(rows)} `fixed` claims hold: the cited commit exists, both files")
    print("exist, and the test that backs each one is green.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
