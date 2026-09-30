#!/usr/bin/env python3
"""Are the plans actually built, or do they only CLAIM to be?

A plan with steps marked DONE and commit hashes is a claim. This checks the claim
against the tree, using each plan's OWN citations -- the file paths and commit
hashes it names -- rather than a guess about where the code ought to live. The
first version of this file guessed `internal/service/access` and reported the
content-access gate MISSING while it sat in `internal/service/trust/`, which is
what a checker that invents its own expectations always does: it reports its own
wrong answer.

    python3 docs/plan-verify.py
"""
import re, subprocess, pathlib, sys

REPO = pathlib.Path(__file__).resolve().parent.parent
PLANS = sorted((REPO / "docs" / "plan").glob("feature-*.md"))


def sh(c):
    return subprocess.run(c, shell=True, cwd=REPO, capture_output=True, text=True).stdout


def cited_paths(text):
    """Paths the plan names in a 'File:' or '**File:**' or fenced-path position."""
    pats = [
        r"\*\*Files?:\*\*\s*`([^`]+)`",
        r"\*\*File:\*\*\s*`([^`]+)`",
        r"^\s*[-*]\s*`((?:internal|pkg|cmd|graphql|frontend)/[\w./-]+\.\w+)`",
        r"^\s*[-*]\s*`((?:internal|pkg|cmd)/[\w./-]+/)`",
    ]
    found = set()
    for p in pats:
        for m in re.finditer(p, text, re.M):
            found.add(m.group(1).rstrip("/"))
    return sorted(found)


def main():
    print("=" * 74)
    print("PLAN CLAIM VERIFICATION -- stash-box")
    print("=" * 74)

    problems = []
    total_paths = total_hashes = 0

    for plan in PLANS:
        text = plan.read_text()
        print(f"\n{plan.name}  ({len(text.splitlines())} lines)")

        # --- 1. commit hashes it cites must exist here -----------------------
        hashes = sorted(set(re.findall(r"`([0-9a-f]{7,40})`", text)))
        hashes = [h for h in hashes if not re.fullmatch(r"[0-9a-f]*[g-z][0-9a-z]*", h)]
        missing_h = [h for h in hashes
                     if subprocess.run(["git", "cat-file", "-e", h + "^{commit}"],
                                       cwd=REPO, capture_output=True).returncode != 0]
        total_hashes += len(hashes)
        if missing_h:
            problems.append(f"{plan.name}: cites {len(missing_h)} commit(s) not in this repo: "
                            + ", ".join(missing_h[:6]))
        print(f"  commits cited: {len(hashes)}, not in this repo: {len(missing_h)}")

        # --- 2. the files it names must exist --------------------------------
        paths = cited_paths(text)
        missing_p = []
        for rel in paths:
            # a plan may cite a path that only exists on the narrow branch
            if (REPO / rel).exists():
                continue
            on_branch = subprocess.run(f"git cat-file -e master:{rel}", shell=True,
                                       cwd=REPO, capture_output=True).returncode == 0
            if not on_branch:
                missing_p.append(rel)
        total_paths += len(paths)
        if missing_p:
            problems.append(f"{plan.name}: names {len(missing_p)} path(s) that exist nowhere: "
                            + ", ".join(missing_p[:6]))
        print(f"  paths cited:   {len(paths)}, exist in no branch: {len(missing_p)}")
        for rel in paths:
            here = (REPO / rel).exists()
            mark = "ok  " if here else "branch"
            if not here:
                mark = "MISS"
                print(f"      [{mark}] {rel}")

        # --- 3. the DONE markers must correspond to real code ----------------
        done = re.findall(r"^#{2,3} .*?\*\*(DONE|BUILT)[^*]*\*\*", text, re.M | re.I)
        print(f"  steps marked DONE/BUILT: {len(done)}")

        # --- 4. deviations ---------------------------------------------------
        m = re.search(r"^## Deviations\s*\n+(.*?)(?=\n## |\Z)", text, re.M | re.S)
        body = (m.group(1).strip() if m else "")
        if not body or body.startswith("_(none"):
            problems.append(f"{plan.name}: Deviations section is empty while "
                            f"{len(done)} step(s) are marked DONE")
            print("  deviations:  EMPTY  <- a DONE plan with no deviations recorded")
        else:
            print(f"  deviations:  {len(body.splitlines())} line(s) recorded")

    print("\n" + "=" * 74)
    print(f"checked {total_paths} cited path(s) and {total_hashes} commit hash(es) "
          f"across {len(PLANS)} plan(s)")
    if problems:
        print(f"\n{len(problems)} PROBLEM(S):")
        for p in problems:
            print(f"  - {p}")
        print("\nThe plans are partly aspirational. Build the missing work or mark")
        print("the steps as not-done before calling the plan implemented.")
        return 1
    print("\nAll plan claims hold against the tree.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
