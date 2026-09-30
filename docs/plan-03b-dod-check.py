#!/usr/bin/env python3
"""The 03b Definition of Done, clause by clause -- checked, not assumed.

The plan (docs/plan/feature-03b-content-access-and-vanguard-weighting.md) marks
three steps DONE and leaves Deviations as "_(none yet)_". A DONE plan with an
empty deviations section is a claim nobody checked, so this walks the definition
of done and reports each clause honestly -- including the ones that fail.

    python3 docs/plan-03b-dod-check.py
"""
import re, subprocess, pathlib, sys

REPO = pathlib.Path(__file__).resolve().parent.parent
results = []


def add(clause, status, detail):
    results.append((clause, status, detail))


def sh(c, timeout=900):
    r = subprocess.run(c, shell=True, cwd=REPO, capture_output=True, text=True,
                       timeout=timeout)
    return r.returncode, r.stdout.strip(), r.stderr.strip()


def has(path, pattern):
    p = REPO / path
    return bool(p.exists() and re.search(pattern, p.read_text(), re.M))


# The two plan documents, read once. A disposition may be recorded in EITHER
# plan or in the SPEC, so every clause that looks for one needs both.
PLAN = REPO / "docs/plan/feature-03b-content-access-and-vanguard-weighting.md"
PLAN04 = REPO / "docs/plan/feature-04-identification-federation.md"
SPEC = REPO / "docs/SPEC.md"
plan = PLAN.read_text() if PLAN.exists() else ""
plan04 = PLAN04.read_text() if PLAN04.exists() else ""
spec = SPEC.read_text() if SPEC.exists() else ""


# 1 -- build and vet clean
rc, out, _ = sh("go build ./... ")
add("build clean", "PASS" if rc == 0 else "FAIL", "" if rc == 0 else out[:120])
rc, out, _ = sh("go vet ./... ")
add("vet clean", "PASS" if rc == 0 else "FAIL", "" if rc == 0 else out[:120])

# 2 -- the suite is green INCLUDING the pre-existing ones
rc, out, _ = sh("go test ./... -count=1", timeout=1800)
if rc == 124:
    add("suite green", "UNKNOWN", "go test timed out -- a killed run reads like a pass")
else:
    # Same correction as goal-check C6, for the same reason: `go test` prints
    # "?   pkg  [no test files]" for a package that builds and vets clean but has no
    # untagged test, which is the normal state for one whose only tests are
    # integration-tagged. Counting only "ok" made this clause fail the moment
    # internal/service/scene's only test went behind //go:build integration -- 28
    # instead of 29, with nothing actually broken.
    npkg = sum(1 for l in out.splitlines()
               if l.startswith("ok") or l.startswith("?"))
    notests = sum(1 for l in out.splitlines()
                  if l.startswith("?") and "[no test files]" in l)
    fails = [l for l in out.splitlines() if l.startswith("FAIL")]
    add("suite green", "PASS" if rc == 0 and npkg >= 29 else "FAIL",
        f"{npkg} packages accounted for, {notests} with no untagged test"
        + (f"; FAIL: {fails[:2]}" if fails else ""))

# 3 -- the mutation sweep actually runs, so run it and report its result
# The plans list specific mutations. Rather than trust the prose, apply each one
# and confirm the named test catches it. A mutation the suite survives means the
# guarded line is dead, redundant, or re-implemented in the test.
rc, out, err = sh("python3 docs/mutation-03b.py", timeout=1800)
m = re.search(r"^(\d+) killed, (\d+) survived, (\d+) no-op", out, re.M)
if not m:
    add("mutation sweep", "UNKNOWN",
        "docs/mutation-03b.py produced no summary line -- it did not run")
else:
    killed, survived, noops = (int(g) for g in m.groups())
    add("mutation sweep", "PASS" if survived == 0 and noops == 0 and killed > 0 else "FAIL",
        f"{killed} killed, {survived} survived, {noops} no-op"
        + ("  <- a survivor is dead/redundant code or a test that re-implements it"
           if survived else ""))

# 4 -- SPEC rows D1-D8 each implemented or explicitly deferred WITH a reason.
#     The disposition may live in EITHER plan, or in the SPEC's own status table.
#     The first version of this clause searched only 03b, so D6 (deferred in plan
#     04) and D7 (deferred nowhere) both read as "not mentioned in the 03b plan" --
#     which is a fact about where the text is, not about whether the row was
#     decided. A row recorded in either plan has been decided.
d_rows = []
if spec:
    # The SPEC's row label is BARE -- `| D1 | **Vanguard trust-weighted
    # voting.** ...` with no asterisks around the D. A pattern expecting `**D1**`
    # parses zero rows, which the guard below correctly refuses to call a pass.
    d_rows = sorted(set(re.findall(r"^\|\s*\*{0,2}(D[1-8])\*{0,2}\s*\|", spec, re.M)))
if not d_rows:
    add("SPEC D1-D8 dispositioned", "FAIL",
        "the SPEC's D1-D8 table parsed to ZERO rows -- refusing to call that PASS")
else:
    # Scope the search to the DISPOSITION, not to any mention. Searching the whole
    # corpus for the letter D3 finds plan 04's prose and reports a row as decided
    # after its status cell was blanked -- verified by mutation. A disposition is
    # a table row `| **Dn** ... | **status** | where/why |`, so read that row.
    undispositioned = []
    for r in d_rows:
        row = re.search(rf"^\|\s*\*\*{r}\*\*[^\n]*$", plan, re.M)
        if not row:
            undispositioned.append(f"{r} (no row in the 03b disposition table)")
            continue
        cells = [c.strip() for c in row.group(0).strip("|").split("|")]
        status = cells[1] if len(cells) > 1 else ""
        why = cells[2] if len(cells) > 2 else ""
        if not re.search(r"implemented|deferred|partially|split", status, re.I):
            undispositioned.append(f"{r} (status cell is {status!r})")
        elif not why:
            undispositioned.append(f"{r} (no where/why)")
    add("SPEC D1-D8 dispositioned",
        "PASS" if not undispositioned else "FAIL",
        f"SPEC names {len(d_rows)} row(s) ({', '.join(d_rows)}); "
        + (f"NOT decided: " + "; ".join(undispositioned) if undispositioned
           else "each row has a status and a where/why"))

# 5 -- deviations recorded
m = re.search(r"^## Deviations\s*\n+(.*?)(?=\n## |\Z)", plan, re.M | re.S)
body = (m.group(1).strip() if m else "")
if not body or body.startswith("_(none"):
    add("deviations recorded", "FAIL",
        "Deviations is \"_(none yet)_\" while 3 steps are marked DONE -- "
        "either the work matched the plan exactly (unlikely) or nobody wrote it down")
else:
    add("deviations recorded", "PASS", f"{len(body.splitlines())} line(s)")

# 6 -- a Status: line, so goal-check C3 can read the state instead of guessing
m = re.search(r"^Status:.*$", plan, re.M)
add("status line", "PASS" if m else "FAIL",
    m.group(0) if m else "no 'Status:' line -- C3 cannot read this plan's state")


def main():
    w = max(len(c) for c, _, _ in results) + 2
    print("=" * 74)
    print("PLAN 03b -- DEFINITION OF DONE, CLAUSE BY CLAUSE")
    print("=" * 74)
    for clause, status, detail in results:
        print(f"{clause:<{w}} [{status:^7}] {detail}")
    print("=" * 74)
    fails = [r for r in results if r[1] == "FAIL"]
    unknown = [r for r in results if r[1] in ("UNKNOWN", "SEE")]
    if not fails and not unknown:
        print("\nEvery clause of the definition of done holds.\n")
        return 0
    print(f"\n{len(fails)} clause(s) FAIL, {len(unknown)} need a human/mechanical pass. "
          "The plan is NOT done.\n")
    return 1


if __name__ == "__main__":
    sys.exit(main())
