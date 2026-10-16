#!/usr/bin/env python3
"""Do the two instruments actually catch the contrast defect?

The fix made the sweep green, which is the weakest possible evidence: a green gate
that cannot go red says nothing. So revert the value to #db3737 -- the colour axe
measured at 2.31:1 (light) and 2.83:1 (dark) -- and require BOTH instruments to fail:

  * theme.test.ts, in about a second, because it holds the token to 4.5:1 on the
    surfaces it can land on, including the card footer
  * the browser sweep, which measures the real page -- the instrument whose summary
    line hid the sibling nodes that made the first run look like one defect

The sweep needs a rebuild to see it: this project embeds the frontend build in the
Go binary at compile time, so a mutation that only touches the SCSS would be tested
against the previous artefact and read as a survivor.

Usage: STASH_BOX_DATABASE=... python3 scripts/mutate-danger-contrast.py
"""
import os
import re
import subprocess
import sys

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
FE = os.path.join(REPO, "frontend")
E2E = os.path.join(REPO, "e2e")
SCSS = os.path.join(FE, "src/styles/theme.scss")
TS = os.path.join(FE, "src/theme.ts")
DSN = os.environ.get("STASH_BOX_DATABASE", "")

if not DSN:
    sys.exit("STASH_BOX_DATABASE is required")

ALTERED = [("\"#ff9e9e\"", "\"#db3737\"", TS), ("--sb-danger-text: #ff9e9e;", "--sb-danger-text: #db3737;", SCSS),
           ("\"#7d1616\"", "\"#db3737\"", TS), ("--sb-danger-text: #7d1616;", "--sb-danger-text: #db3737;", SCSS)]


def sh(cmd, cwd, timeout=900):
    return subprocess.run(cmd, cwd=cwd, capture_output=True, text=True, timeout=timeout, env=dict(os.environ))


def rebuild():
    if sh(["node", "node_modules/vite/bin/vite.js", "build"], FE).returncode != 0:
        return False
    return sh(["go", "build", "-o", "./stash-box", "./cmd/stash-box"], REPO).returncode == 0


originals = {p: open(p).read() for p in (SCSS, TS)}
for old, new, path in ALTERED:
    body = open(path).read()
    if body.count(old) != 1:
        sys.exit(f"anchor {old!r} matched {body.count(old)} times in {path} -- not a verdict")
    open(path, "w").write(body.replace(old, new, 1))

try:
    unit = sh(["pnpm", "test:run", "src/__tests__/theme.test.ts"], FE, timeout=300)
    unit_out = re.sub(r"\x1b\[[0-9;]*m", "", unit.stdout + unit.stderr)
    failed = re.search(r"Tests\s+(\d+) failed", unit_out)
    print(f"MUTANT --sb-danger-text=#db3737 (the colour axe measured at 2.31/2.83)")
    print(f"  theme.test.ts : exit={unit.returncode} failed={failed.group(1) if failed else '0'} -> {'RED (killed)' if unit.returncode != 0 else 'GREEN (SURVIVED)'}")

    if not rebuild():
        print("  browser sweep : could not build the mutant -- not a verdict")
    else:
        sweep = sh(["pnpm", "test", "a11y.spec.ts", "--workers=2"], E2E, timeout=600)
        out = re.sub(r"\x1b\[[0-9;]*m", "", sweep.stdout + sweep.stderr)
        failed = re.search(r"(\d+) failed", out)
        routes = re.findall(r"(\d+ of \d+) routes have violations", out)
        print(f"  browser sweep : exit={sweep.returncode} failed={failed.group(1) if failed else '0'} routes={routes or 'none'} -> {'RED (killed)' if sweep.returncode != 0 else 'GREEN (SURVIVED)'}")
finally:
    for path, body in originals.items():
        open(path, "w").write(body)
        if open(path).read() != body:
            print(f"!! RESTORE FAILED for {path}")
    print("restored; rebuilding", "OK" if rebuild() else "FAILED")
