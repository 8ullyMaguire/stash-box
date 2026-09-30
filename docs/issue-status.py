#!/usr/bin/env python3
"""The real per-issue state: docs/plans/*.md, one per issue, with Status: lines.

This repo already has the ledger I was about to build from scratch -- 28 per-issue
plans under docs/plans/, each with a Status and usually a commit. Reading that is
strictly better than probing the tree with greps: a grep for "alias" proves a
symbol exists, not that #778 is fixed. A plan that says SOLVED in `613f21ed6` and
a commit that exists is evidence; a grep hit is not.

So this script:
  1. reads every docs/plans/NNNN-*.md,
  2. extracts its Status and any commit it cites,
  3. VERIFIES each cited commit exists,
  4. cross-references the 30 [Bug Report]s against what has a plan and what
     does not.

    python3 docs/issue-status.py
"""
import json, pathlib, re, subprocess, sys

REPO = pathlib.Path(__file__).resolve().parent.parent
PLANS = REPO / "docs" / "plans"
CACHE = pathlib.Path("/home/alvaro/.hermes/profiles/coding-3/cache/scratch/sb-issues-full.json")


def commit_exists(h):
    return subprocess.run(["git", "cat-file", "-e", h + "^{commit}"],
                          cwd=REPO, capture_output=True).returncode == 0


def main():
    rows = []
    for p in sorted(PLANS.glob("[0-9]*.md")):
        num = int(p.name[:4])
        t = p.read_text()
        st = re.search(r"\*\*Status:?\s*([^*]+)\*\*", t) or re.search(r"^Status:.*$", t, re.M)
        status = (st.group(1) if st and st.lastindex else st.group(0) if st else "").strip()
        commits = sorted(set(re.findall(r"`([0-9a-f]{7,40})`", t)))
        bad = [c for c in commits if not commit_exists(c)]
        rows.append((num, p.name, status[:64], commits, bad))

    print("=" * 100)
    print("PER-ISSUE PLANS -- status and commit verification")
    print("=" * 100)
    for num, name, status, commits, bad in rows:
        mark = "ok " if not bad else "BAD"
        c = f"{len(commits)} commit(s)" + (f", {len(bad)} NOT IN REPO: {bad[:3]}" if bad else "")
        print(f"[{mark}] #{num:<5} {status:<66} {c}")

    solved = [r for r in rows if re.search(r"solved|implemented|done|shipped|complete",
                                           r[2], re.I)]
    print("-" * 100)
    print(f"plans: {len(rows)}  |  solved/implemented: {len(solved)}  |  "
          f"other: {len(rows) - len(solved)}")
    for r in rows:
        if r not in solved:
            print(f"   not marked solved: #{r[0]} {r[2][:60] or '(no Status)'}")

    # cross-reference the bug reports
    data = {int(k): v for k, v in json.loads(CACHE.read_text()).items()}
    bugs = [n for n in sorted(data)
            if re.match(r"\s*\[\s*bug\b", data[n].get("title") or "", re.I)]
    planned = {r[0] for r in rows}
    print("-" * 100)
    print(f"the {len(bugs)} [Bug Report]s:")
    print(f"  have a plan: {len([n for n in bugs if n in planned])}")
    print(f"  NO plan:     {len([n for n in bugs if n not in planned])}  "
          f"{[n for n in bugs if n not in planned]}")
    print("\n  the bug reports with NO plan -- this is the remaining work:")
    for n in bugs:
        if n not in planned:
            print(f"    #{n:<5} {data[n]['title'][:72]}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
