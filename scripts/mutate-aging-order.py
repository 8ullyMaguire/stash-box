#!/usr/bin/env python3
"""Is the aging spec EVIDENCE, or does it pass either way?

The ordering claim lives in one prop (pages/edits/Edits.tsx defaultDirection: ASC).
Flipping it to DESC must turn the ORDERING test red while the waiting test stays
green -- and because this project embeds the frontend build in the Go binary at
compile time, the mutation needs both rebuilds before the browser can see it. A run
without them would test the previous artefact and report a false survivor, which is
the stale-artefact trap this repo has paid for twice.

Verdicts come from EXIT CODES, not from parsed output: a colour-aware parser already
reported GREEN for a run whose failures were on screen.

Usage: STASH_BOX_DATABASE=... python3 scripts/mutate-aging-order.py
"""
import os
import re
import subprocess
import sys

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
FE = os.path.join(REPO, "frontend")
E2E = os.path.join(REPO, "e2e")
SPEC = "review-queue-aging.spec.ts"
PAGE = os.path.join(FE, "src/pages/edits/Edits.tsx")
DSN = os.environ.get("STASH_BOX_DATABASE", "")

ANCHOR = "      defaultDirection={SortDirectionEnum.ASC}"
MUTANT = "      defaultDirection={SortDirectionEnum.DESC} // MUTANT"
ORDERING = "--grep=opens on the oldest end"

if not DSN:
    sys.exit("STASH_BOX_DATABASE is required")


def sh(cmd, cwd, timeout=900):
    return subprocess.run(cmd, cwd=cwd, capture_output=True, text=True, timeout=timeout, env=dict(os.environ))


def rebuild():
    """Both artefacts: the binary embeds the built frontend."""
    v = sh(["node", "node_modules/vite/bin/vite.js", "build"], FE)
    if v.returncode != 0:
        print("!! vite build failed:", v.stdout[-400:], v.stderr[-400:])
        return False
    g = sh(["go", "build", "-o", "./stash-box", "./cmd/stash-box"], REPO)
    if g.returncode != 0:
        print("!! go build failed:", g.stderr[-400:])
        return False
    return True


def run_spec(grep=None):
    args = ["pnpm", "test", SPEC]
    if grep:
        args.append(grep)
    p = sh(args, E2E, timeout=900)
    out = re.sub(r"\x1b\[[0-9;]*m", "", p.stdout + p.stderr)
    passed = re.search(r"(\d+) passed", out)
    failed = re.search(r"(\d+) failed", out)
    return p.returncode, (passed.group(1) if passed else "0"), (failed.group(1) if failed else "0")


original = open(PAGE).read()
if original.count(ANCHOR) != 1:
    sys.exit(f"anchor matched {original.count(ANCHOR)} times -- not a verdict")

try:
    open(PAGE, "w").write(original.replace(ANCHOR, MUTANT, 1))
    if not rebuild():
        sys.exit("could not build the mutant -- not a verdict")
    rc_order, ok_o, bad_o = run_spec(ORDERING)
    rc_wait, ok_w, bad_w = run_spec("--grep=says how long it has been waiting")
    print(f"MUTANT defaultDirection=DESC")
    print(f"  ordering spec : exit={rc_order} passed={ok_o} failed={bad_o}  -> {'RED (killed)' if rc_order != 0 else 'GREEN (SURVIVED)'}")
    print(f"  waiting spec  : exit={rc_wait} passed={ok_w} failed={bad_w}  -> {'RED (neighbour also caught it)' if rc_wait != 0 else 'GREEN (neighbour unaffected, as it should be)'}")
finally:
    open(PAGE, "w").write(original)
    assert open(PAGE).read() == original, "RESTORE FAILED"
    if rebuild():
        rc, ok, bad = run_spec()
        print(f"AFTER RESTORE: exit={rc} passed={ok} failed={bad}  -> {'GREEN' if rc == 0 else 'STILL RED'}")
