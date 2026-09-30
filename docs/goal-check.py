#!/usr/bin/env python3
"""
goal-check.py -- the completion predicate for stash-box.

    cd ~/code-local/go/stash-box && python3 docs/goal-check.py

Exit 0  -> the goal is COMPLETE.
Exit 1  -> work remains; stdout names the clause.

Same design rules as the stash goal-check.py, and for the same reasons:

  * A check that can silently match nothing is worse than no check. Every clause
    that greps asserts it found something.
  * A clause that cannot be evaluated reports UNKNOWN, never PASS. A timeout
    (rc=124) produces no output and reads exactly like a clean run.
  * Never trust a count written in a document. Recompute it.

The clauses are deliberately shaped around what this fork actually is: a
second-brain web service with an upstream it tracks. "Every upstream PR merged"
is neither achievable nor desirable here -- 19 of 45 are declined, some for
cause, and that record IS the work. So the PR clause asks for a recorded
*decision*, not a merge.
"""

import json
import re
import subprocess
import sys
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
UPSTREAM = "stashapp/stash-box"

# The minimum test count this repo has ever been green at. A green run BELOW this
# is a regression, not a pass -- a suite that lost tests looks identical to a suite
# that never ran them.
BASELINE_PACKAGES = 29

results = []


def sh(cmd, cwd=REPO, timeout=1200):
    try:
        p = subprocess.run(cmd, shell=True, cwd=cwd, capture_output=True,
                           text=True, timeout=timeout)
        return p.returncode, p.stdout.strip(), p.stderr.strip()
    except subprocess.TimeoutExpired:
        return 124, "", f"timeout after {timeout}s"


def add(clause, status, detail):
    results.append((clause, status, detail))


# ---------------------------------------------------------------------------
# C1 -- every open upstream PR has a recorded decision
# ---------------------------------------------------------------------------
def c1_prs_decided():
    rc, out, err = sh(
        f"gh pr list --repo {UPSTREAM} --state open --limit 100 --json number")
    if rc != 0:
        add("C1 PRs decided", "UNKNOWN", f"gh failed (rc={rc}): {err[:80]}")
        return
    try:
        prs = [p["number"] for p in json.loads(out)]
    except json.JSONDecodeError:
        add("C1 PRs decided", "UNKNOWN", "gh returned unparseable JSON")
        return
    if not prs:
        add("C1 PRs decided", "UNKNOWN",
            "gh reports ZERO open PRs -- refusing to read that as PASS")
        return

    doc = REPO / "docs" / "plan" / "upstream-pr-port.md"
    if not doc.exists():
        add("C1 PRs decided", "FAIL", "docs/plan/upstream-pr-port.md is missing")
        return
    text = doc.read_text()

    undecided = [n for n in prs if not re.search(rf"(?<![\d]){n}(?![\d])", text)]
    if undecided:
        add("C1 PRs decided", "FAIL",
            f"{len(undecided)}/{len(prs)} open PRs undecided: "
            + ", ".join(f"#{n}" for n in sorted(undecided)[:12])
            + (" ..." if len(undecided) > 12 else ""))
    else:
        ported = len(re.findall(r"^\|\s*\d+\s*\|", text, re.M))
        add("C1 PRs decided", "PASS",
            f"all {len(prs)} open PRs decided ({ported} table rows recorded)")


# ---------------------------------------------------------------------------
# C2 -- every relevant issue is dispositioned
# ---------------------------------------------------------------------------
def c2_issues_dispositioned():
    ledger = REPO / "docs" / "ISSUES.md"
    if not ledger.exists():
        add("C2 issues dispositioned", "FAIL",
            "docs/ISSUES.md does not exist -- the 177 open issues are unclassified")
        return

    text = ledger.read_text()

    # The roster must actually parse. A table that matches nothing is the failure
    # mode this file exists to catch.
    rows = re.findall(r"^\|\s*(\d+)\s*\|([^|]*)\|([^|]*)\|([^|]*)\|", text, re.M)
    if not rows:
        add("C2 issues dispositioned", "FAIL",
            "docs/ISSUES.md roster parsed to ZERO rows -- refusing to call that PASS")
        return

    # A roster that does not cover the open issues is an incomplete ledger, not a
    # finished one.
    snapshot = REPO / "docs" / "track" / "issues-open.json"
    if snapshot.exists():
        try:
            open_nums = {i["number"] for i in json.loads(snapshot.read_text())}
        except (json.JSONDecodeError, KeyError, TypeError):
            open_nums = set()
        if open_nums:
            covered = {int(n) for n, _, _, _ in rows}
            missing = sorted(open_nums - covered)
            if missing:
                add("C2 issues dispositioned", "FAIL",
                    f"roster covers {len(open_nums) - len(missing)}/{len(open_nums)} "
                    f"open issues; missing " +
                    ", ".join(f"#{n}" for n in missing[:10])
                    + (" ..." if len(missing) > 10 else ""))
                return

    outstanding = [(int(n), t.strip(), s.strip()) for n, t, _, s in rows
                   if s.strip().lower() in ("relevant", "planned", "todo", "open")]
    if outstanding:
        add("C2 issues dispositioned", "FAIL",
            f"{len(outstanding)} rows still awaiting a decision: " +
            ", ".join(f"#{n}" for n, _, _ in outstanding[:10])
            + (" ..." if len(outstanding) > 10 else ""))
    else:
        add("C2 issues dispositioned", "PASS",
            f"{len(rows)} roster rows, none awaiting a decision")


# ---------------------------------------------------------------------------
# C3 -- spec/plan implemented: every plan document is Built
# ---------------------------------------------------------------------------
def c3_plan_built():
    plan_dir = REPO / "docs" / "plan"
    if not plan_dir.exists():
        add("C3 spec/plan built", "FAIL", "docs/plan/ does not exist")
        return
    plans = sorted(p for p in plan_dir.glob("*.md")
                   if p.name != "upstream-pr-port.md")
    if not plans:
        add("C3 spec/plan built", "FAIL", "no plan documents found in docs/plan/")
        return

    unbuilt = []
    for p in plans:
        t = p.read_text()
        m = re.search(r"^Status:.*$", t, re.M)
        status = m.group(0) if m else "(no Status: line)"
        if not re.search(r"status:.*\b(built|implemented|shipped|complete)", status, re.I):
            unbuilt.append((p.name, status[:70]))

    if unbuilt:
        add("C3 spec/plan built", "FAIL",
            f"{len(unbuilt)}/{len(plans)} plan documents not Built: " +
            "; ".join(n for n, _ in unbuilt))
    else:
        add("C3 spec/plan built", "PASS", f"all {len(plans)} plan documents Built")


# ---------------------------------------------------------------------------
# C4 -- the narrow branch is fully merged into the wide one
# ---------------------------------------------------------------------------
def c4_narrow_merged():
    rc, _, _ = sh("git rev-parse --verify issue-fixes")
    if rc != 0:
        add("C4 narrow merged", "UNKNOWN", "branch `issue-fixes` does not exist")
        return
    rc, _, _ = sh("git merge-base --is-ancestor issue-fixes master")
    if rc == 0:
        _, behind, _ = sh("git rev-list --left-right --count issue-fixes...master")
        add("C4 narrow merged", "PASS",
            f"issue-fixes is an ancestor of master (master is {behind.split()[0] if behind else '?'} commits ahead)")
    else:
        _, ab, _ = sh("git rev-list --left-right --count issue-fixes...master")
        add("C4 narrow merged", "FAIL",
            f"issue-fixes is NOT merged into master ({ab} ahead/behind) -- "
            "narrow-branch work is stranded")


# ---------------------------------------------------------------------------
# C5 -- migration numbering is coherent across both branches
# ---------------------------------------------------------------------------
def c5_migrations_coherent():
    """Two DIFFERENT migrations claiming the same number is a golang-migrate hard
    startup failure.

    Not hypothetical here: the #1183 port hit exactly it, which is why its
    migration was renumbered 73 -> 90. Pairing up/down is the normal case, so the
    check keys on the up-file stem and only fires on a genuine name collision.
    """
    bad = []
    for branch in ("issue-fixes", "master"):
        rc, out, _ = sh(f"git ls-tree -r --name-only {branch} "
                        "-- internal/database/migrations/postgres/")
        if rc != 0:
            add("C5 migrations coherent", "UNKNOWN", f"cannot list migrations on {branch}")
            return
        # Key on the UP file's stem: `1_initial.up.sql` and `1_initial.down.sql`
        # are the two halves of ONE migration, not two migrations. Counting them as
        # duplicates is a false positive -- and the checker's own rule is that a
        # check which cannot be evaluated honestly must say UNKNOWN, not invent a
        # failure. The real defect is two DIFFERENT migration names on one number.
        nums = {}
        for path in out.splitlines():
            base = path.rsplit("/", 1)[-1]
            m = re.match(r"(\d+)_(.*)\.up\.sql$", base)
            if m:
                nums.setdefault(int(m.group(1)), set()).add(m.group(2))
        for n, names in sorted(nums.items()):
            if len(names) > 1:
                bad.append(f"{branch}:{n} ({', '.join(sorted(names))})")

    if bad:
        add("C5 migrations coherent", "FAIL",
            "duplicate migration numbers: " + "; ".join(bad[:6])
            + " -- golang-migrate fails at startup on this")
    else:
        add("C5 migrations coherent", "PASS",
            "no duplicate migration numbers on either branch")


# ---------------------------------------------------------------------------
# C6 -- the full suite is green, at or above baseline
# ---------------------------------------------------------------------------
def c6_suite():
    rc, out, err = sh("go test ./... -count=1", timeout=1800)
    if rc == 124:
        add("C6 suite", "UNKNOWN", "go test timed out -- a killed run reads like a pass")
        return
    if rc != 0:
        fails = [l for l in out.splitlines() if l.startswith("FAIL")]
        add("C6 suite", "FAIL",
            f"{len(fails)} failing package(s): " + "; ".join(fails[:4]))
        return
    npkg = sum(1 for l in out.splitlines() if l.startswith("ok"))
    if npkg < BASELINE_PACKAGES:
        add("C6 suite", "FAIL",
            f"only {npkg} packages green, baseline is {BASELINE_PACKAGES} -- "
            "a suite that LOST tests is not a pass")
    else:
        add("C6 suite", "PASS", f"{npkg} packages green (baseline {BASELINE_PACKAGES})")


# ---------------------------------------------------------------------------
# C7 -- build, vet and gofmt are clean
# ---------------------------------------------------------------------------
def c7_build_clean():
    checks = [
        ("build", "go build ./..."),
        ("vet", "go vet ./..."),
        ("gofmt", "gofmt -l internal pkg cmd graphql 2>/dev/null"),
    ]
    dirty = []
    for label, cmd in checks:
        rc, out, _ = sh(cmd, timeout=900)
        if rc == 124:
            add("C7 build clean", "UNKNOWN", f"{label} timed out")
            return
        if label == "gofmt":
            files = [l for l in out.splitlines() if l.strip()]
            if files:
                dirty.append(f"gofmt: {len(files)} file(s) unformatted, e.g. {files[0]}")
        elif rc != 0:
            dirty.append(f"{label} failed (rc={rc}): {out.splitlines()[0][:80] if out else ''}")
    if dirty:
        add("C7 build clean", "FAIL", "; ".join(dirty))
    else:
        add("C7 build clean", "PASS", "build, vet and gofmt all clean")


def main():
    for fn in (c1_prs_decided, c2_issues_dispositioned, c3_plan_built,
               c4_narrow_merged, c5_migrations_coherent, c6_suite, c7_build_clean):
        try:
            fn()
        except Exception as e:  # a clause that crashes is not a passing clause
            add(fn.__name__, "UNKNOWN", f"clause raised {type(e).__name__}: {e}")

    w = max(len(c) for c, _, _ in results) + 2
    print("=" * 74)
    print("GOAL COMPLETION PREDICATE -- stash-box")
    print("=" * 74)
    for clause, status, detail in results:
        print(f"{clause:<{w}} [{status:^7}] {detail}")
    print("=" * 74)

    failed = [r for r in results if r[1] == "FAIL"]
    unknown = [r for r in results if r[1] == "UNKNOWN"]
    if not failed and not unknown:
        print("\nALL CLAUSES PASS. The goal is COMPLETE.\n")
        return 0
    if unknown:
        print(f"\n{len(unknown)} clause(s) UNKNOWN -- an unevaluated clause is not a pass.")
    print(f"{len(failed)} clause(s) outstanding. The goal is NOT complete. Keep going.\n")
    return 1


if __name__ == "__main__":
    sys.exit(main())
