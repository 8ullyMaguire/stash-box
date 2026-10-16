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
import os
import re
import subprocess
import sys
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
UPSTREAM = "stashapp/stash-box"
MIG_DIR = "internal/database/migrations/postgres"

# The minimum number of packages that must be green. A run BELOW this is a
# regression, not a pass -- a suite that lost tests looks identical to one that
# never ran them.
#
# A floor, not a target: adding packages can never fail this clause, which is the
# point. `python3 docs/goal-check.py --update-baseline` rewrites it from the tree
# rather than from anyone's memory.
#
# The number counts packages WITHOUT `-tags=integration`, because that is what the
# clause below runs. Do not raise it to match the `make it` figure: that runs a
# different, larger set of packages, and using its count here made the clause report
# a regression that did not exist.
#
# It must however be the SAME metric the clause computes, which took two fixes.
# 29 was measured with an `ok`-only count while the clause counted `ok` AND `?`, so a
# suite that lost 27 packages still reported PASS. The clause was corrected; the
# --update-baseline WRITER was not, and it then wrote 31 against a clause measuring
# 60 -- the same decorative floor, reintroduced through the other door. Both now call
# count_packages(), because a floor is only a guard if it is measured the same way as
# the comparison, and two copies of that measurement is two chances to disagree.
BASELINE_PACKAGES = 60

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

    # A DECIDED row is one with an outcome: fixed (a commit is named), declined,
    # feature-request, or partially-fixed. A row marked `bug` or `spec-collision`
    # is real work that has not been done, and the goal is not complete while any
    # exist. This is the same disposition-vs-decided distinction as the PR clause:
    # "the queue is empty because I emptied it" is not a pass.
    # `partially-fixed` counts as DECIDED: the reported defect was fixed and the
    # rest is correct as written, both halves recorded with reasons (#583 — the
    # 64-BYTE bcrypt limit is deliberate; the misleading error message was the
    # defect). Outstanding work is only `bug` (a reported defect with no fix) and
    # `spec-collision` (a spec row with nothing built).
    DECIDED = ("fixed", "declined", "feature-request", "partially-fixed", "duplicate",
               "wontfix")
    OUTSTANDING = ("bug", "suspect-bug", "spec-collision", "triaged")
    rows5 = re.findall(r"^\|\s*(\d+)\s*\|([^|]*)\|([^|]*)\|([^|]*)\|([^|]*)\|",
                       text, re.M)
    if not rows5:
        add("C2 issues dispositioned", "FAIL",
            "the 5-column roster parsed to ZERO rows -- the ledger format changed")
        return
    outstanding = [(int(n), disp.strip()) for n, _, _, disp, _ in rows5
                   if disp.strip().lower() in OUTSTANDING]

    # Bulk reclassification is the one cheat this clause cannot otherwise see:
    # flipping every row to `declined` satisfies "every row has an outcome" while
    # changing nothing. So a non-fixed row must CARRY A REASON, and the reasons
    # must not be one string copied down the column. Measured: the real ledger has
    # many distinct reasons; a mass-decline has exactly one.
    reasonless = []
    reasons = {}
    for n, _, _, disp, why in rows5:
        if disp.strip().lower() == "fixed":
            continue
        w = (why or "").strip()
        if len(w) < 25:
            reasonless.append(f"#{n} ({disp.strip()})")
        reasons[w] = reasons.get(w, 0) + 1
    if reasonless:
        add("C2 reasons present", "FAIL",
            f"{len(reasonless)} non-fixed row(s) have no substantive reason: "
            + ", ".join(reasonless[:8]))
    elif reasons and max(reasons.values()) > 12:
        top, cnt = max(reasons.items(), key=lambda kv: kv[1])
        # A reason that CITES a written policy is not the same failure as one that
        # invents a justification per row and copies it. Uniform policy application
        # is the policy working as intended: 138 rows declining on "SPEC section 0
        # says feature requests are out of scope" is one decision applied 138
        # times, which is what a policy is FOR.
        #
        # What the clause must still catch is a uniform reason with no such
        # referent -- the signature of reclassifying a column to satisfy the
        # row-count check without anyone deciding anything.
        if re.search(r"\bSPEC\b|section \d|policy|§", top):
            # ...but only if the policy actually EXISTS. Citing a document that was
            # never written is the same failure wearing a citation's clothes, and it
            # is the obvious way to defeat the exemption above.
            spec = REPO / "docs" / "SPEC.md"
            has_policy = spec.exists() and re.search(
                r"^##\s*0\..*scope", spec.read_text(), re.I | re.M)
            if not has_policy:
                add("C2 reasons present", "FAIL",
                    f"{cnt} rows cite a scope policy, but docs/SPEC.md has no "
                    "section 0 defining one -- the citation points at nothing")
            else:
                add("C2 reasons present", "PASS",
                    f"{cnt} rows share one reason and it CITES a written policy "
                    f"(SPEC.md section 0), so it is uniform application, not "
                    f"bulk reclassification; {len(reasons)} distinct reasons overall")
        else:
            add("C2 reasons present", "FAIL",
                f"{cnt} rows share ONE identical reason that cites no policy -- "
                f"this is bulk reclassification, not {cnt} decisions: {top[:60]!r}")
    else:
        add("C2 reasons present", "PASS",
            f"{len(reasons)} distinct reasons across the non-fixed rows")
    from collections import Counter
    counts = Counter(disp.strip() for _, _, _, disp, _ in rows5)
    if outstanding:
        add("C2 issues dispositioned", "FAIL",
            f"{len(outstanding)} of {len(rows5)} rows are real work not yet done: "
            + ", ".join(f"#{n} ({disp})" for n, disp in outstanding[:10])
            + ("  -- fix each with a test, or record a decision" if len(outstanding) > 10
               else "  -- fix each with a test, or record a decision"))
        add("C2 breakdown", "", ", ".join(f"{k}={v}" for k, v in counts.most_common()))
    else:
        add("C2 issues dispositioned", "PASS",
            f"{len(rows5)} rows, every one decided: "
            + ", ".join(f"{k}={v}" for k, v in counts.most_common()))


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
        # THE STATUS MUST LEAD WITH ITS VERDICT, and this clause was measured getting it wrong.
        #
        # It used to be `\b(built|implemented|shipped|complete)\b` ANYWHERE on the line, and there are
        # two ways that lies. The first was measured here: five plan documents say "Status: **not
        # built**" to state their state honestly, `built` is in `not built`, and C3 went from "6/11 not
        # Built" to "1/11 not Built" across a commit that built NOTHING. The second is older: a status
        # reading "in progress -- the schema is built, the service is not" passes on the word `built`
        # three words in.
        #
        # The rule is now that the first word of the status IS the verdict, which is how every plan
        # document that really is built writes it ("Status: **Built, verified 2026-09-30.**"). A negation
        # needs no special case: "not built" and "unbuilt" do not LEAD with an affirmative word. The
        # clause reports 7 rather than 6 as a result, and that is the true number: phase 3a's plan is
        # partially built and its status says "in progress" first.
        value = status.split(":", 1)[1] if ":" in status else status
        # THE NEGATION MATTERS, and this clause was measured getting it wrong. The word after
        # "Status:" is often "not": five plan documents here say "Status: **not built**" to state
        # their state honestly, and the pattern below matched every one of them -- `\bbuilt\b` is in
        # "not built" too. C3 went from "6/11 not Built" to "1/11 not Built" across a commit that
        # built NOTHING, purely because the documents started saying so out loud. A status check
        # that cannot tell "built" from "not built" is the presence check this programme keeps
        # finding; the lookbehinds are the fix, and they are why the clause still reports 6.
        if not re.match(r"[\s*_`]*(built|implemented|shipped|complete)\b", value, re.I):
            unbuilt.append((p.name, status[:70]))

    if unbuilt:
        add("C3 spec/plan built", "FAIL",
            f"{len(unbuilt)}/{len(plans)} plan documents not Built: " +
            "; ".join(n for n, _ in unbuilt))
    else:
        add("C3 spec/plan built", "PASS", f"all {len(plans)} plan documents Built")


# ---------------------------------------------------------------------------
# C4 -- no branch in this repository is stranded
#
# This used to be "the narrow branch is fully merged into the wide one", comparing
# issue-fixes against master. Both names are gone. issue-fixes never held anything
# the wide branch could not take -- zero commits in its whole history were parked
# there -- and it cost a merge every time. It also cost a duplicate migration:
# 92_scene_title_text was the same statement as 88_scene_title_text and survived two
# attempts to remove it, because each removal landed on master while the copy on
# issue-fixes was untouched, and every promotion put it back. master is now `main`.
#
# So the property is: no branch still exists holding commits main does not have. A
# merged topic branch should be deleted, which is why merged passes and unmerged
# fails -- and a branch under active development also trips it. That is the same
# trade the old clause made, and it is a loud one.
def c4_narrow_merged():
    # The format string is QUOTED. Unquoted, `%(refname:short)` does not survive the
    # shell and for-each-ref returns NOTHING -- so this saw an empty branch list and
    # reported "main is the only branch" no matter how many branches existed. Caught
    # by mutation: a branch holding an unmerged commit still produced that PASS.
    rc, out, _ = sh("git for-each-ref '--format=%(refname:short)' refs/heads/")
    if rc != 0 or not out.strip():
        add("C4 narrow merged", "UNKNOWN",
            "could not enumerate branches -- refusing to read an empty list as PASS")
        return
    if "main" not in out.split():
        add("C4 narrow merged", "UNKNOWN",
            "main is absent from the branch list; wrong repository or bad HEAD?")
        return
    branches = [b for b in out.split() if b != "main"]
    if not branches:
        add("C4 narrow merged", "PASS",
            "main is the only branch -- nothing can be stranded")
        return

    stranded = []
    for b in branches:
        rc, counts, _ = sh(f"git rev-list --count main..{b}")
        n = counts.strip() if rc == 0 else "?"
        if n != "0":
            stranded.append(f"{b} (+{n})")
    if stranded:
        add("C4 narrow merged", "FAIL",
            "unmerged branch(es): " + "; ".join(stranded) +
            " -- finished work must reach main, or its branch be deleted")
    else:
        add("C4 narrow merged", "PASS",
            f"main plus {len(branches)} merged branch(es), none with unmerged commits")


# ---------------------------------------------------------------------------
# C5 -- migration numbering is coherent across both branches
# ---------------------------------------------------------------------------
def c5_migrations_coherent():
    """Two DIFFERENT migrations claiming one number is a hard startup failure.

    Not hypothetical: the #1183 port hit exactly it, which is why its migration was
    renumbered 73 -> 90, and #1262's hit it too (upstream 76 vs this fork's
    76_add_user_trust, renumbered to 88).

    Pairing up/down is the normal case, so the check keys on the up-file stem and
    only fires on a genuine name collision.

    The second half is the one this repo needed, and it has already paid for
    itself once. Two migrations with DIFFERENT numbers can still be the same
    statement: 88_scene_title_text and 92_scene_title_text were both
    `ALTER TABLE scenes ALTER COLUMN title TYPE text`, the second landing minutes
    after the first. Distinct numbers, distinct files, so the numbering half of this
    check passed and a reader had no way to know the second did nothing.

    Postgres accepts the repeated ALTER (verified against a column that already held
    data), so it was never a correctness bug -- it was a wasted migration version and
    a reader-trap, which is exactly the class of thing a predicate should notice and
    a count cannot. The duplicate was removed in 418bc701, which also replaced the
    test that could not have caught it; the example is written in the past tense for
    that reason, so the next reader does not go looking for a file that is gone.

    Normalise before comparing: strip comments and whitespace, lowercase, collapse
    runs of spaces. Two files whose statements differ only in formatting or a
    comment are the same migration doing the same work.
    """
    bad_numbers = []
    duplicate_sql = []
    # Read the checked-out branch from disk (catches uncommitted work); read the
    # other branch from git, since it lives in someone else's worktree.
    rc_b, CURRENT_BRANCH, _ = sh("git rev-parse --abbrev-ref HEAD")
    for branch in ("main",):
        rc, out, _ = sh(f"git ls-tree -r --name-only {branch} "
                        "-- internal/database/migrations/postgres/")
        if rc != 0:
            add("C5 migrations coherent", "UNKNOWN", f"cannot list migrations on {branch}")
            return

        nums = {}
        for path in out.splitlines():
            base = path.rsplit("/", 1)[-1]
            m = re.match(r"(\d+)_(.*)\.up\.sql$", base)
            if m:
                nums.setdefault(int(m.group(1)), set()).add(m.group(2))
        for n, names in sorted(nums.items()):
            if len(names) > 1:
                bad_numbers.append(f"{branch}:{n} ({', '.join(sorted(names))})")

        # Same statement, different numbers. Enumerate from the FILESYSTEM, not
        # from git: a migration that has been written but not yet committed is
        # exactly the one worth catching, and `git ls-tree` cannot see it. Verified
        # by mutation -- a duplicate planted as an untracked file slipped past the
        # git-based version of this check.
        #
        # For the branch that is NOT checked out here, fall back to git, because
        # there is no filesystem copy to read.
        files = sorted((REPO / MIG_DIR).glob("*.up.sql")) if (REPO / MIG_DIR).is_dir() else []
        if branch != CURRENT_BRANCH:
            rc2, out2, _ = sh(f"git ls-tree -r --name-only {branch} -- {MIG_DIR}/")
            files = [REPO / p for p in out2.splitlines() if p.endswith(".up.sql")]
        by_sql = {}
        for full in files:
            base = full.name
            m = re.match(r"(\d+)_(.*)\.up\.sql$", base)
            if not m:
                continue
            if not full.exists():
                continue
            sql = full.read_text()
            sql = re.sub(r"--[^\n]*", " ", sql)          # line comments
            sql = re.sub(r"/\*.*?\*/", " ", sql, flags=re.S)   # block comments
            # Identifier quoting is not semantic: `ALTER TABLE scenes` and
            # `ALTER TABLE "scenes"` are the same statement to Postgres. Verified by
            # mutation -- a duplicate differing only in quoting and spacing slipped
            # past without this.
            sql = sql.replace('"', "")
            sql = re.sub(r"\s+", " ", sql).strip().lower().rstrip(";")
            if not sql:
                continue
            by_sql.setdefault(sql, []).append(f"{branch}:{m.group(1)}_{m.group(2)}")

        for sql, where in by_sql.items():
            nums_only = {int(w.split(":")[1].split("_")[0]) for w in where}
            if len(where) > 1 and len(nums_only) > 1:
                duplicate_sql.append(f"{', '.join(sorted(where))} -> {sql[:44]}")

    problems = []
    if bad_numbers:
        problems.append("duplicate NUMBERS: " + "; ".join(bad_numbers[:4])
                        + " -- golang-migrate fails at startup on this")
    if duplicate_sql:
        problems.append("same STATEMENT under different numbers: "
                        + "; ".join(sorted(duplicate_sql)[:4]))
    if problems:
        add("C5 migrations coherent", "FAIL", " | ".join(problems))
    else:
        add("C5 migrations coherent", "PASS",
            "one migration per number, and no two migrations share a statement")


# ---------------------------------------------------------------------------
# C6 -- the full suite is green, at or above baseline
# ---------------------------------------------------------------------------
def count_packages(out):
    """Packages accounted for in one `go test` run: green AND no-test-files.

    Both `ok` and `?   [no test files]` count. A package with no tests is still a
    package the run visited, and excluding it made the count depend on how many
    packages happen to have tests -- so the floor moved every time a test file was
    added, for a reason that had nothing to do with tests being lost.
    """
    return sum(1 for l in out.splitlines()
               if l.startswith("ok") or l.startswith("?"))


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
    # Count BOTH `ok  pkg` and `?   pkg  [no test files]`. The second form is a
    # package that BUILDS and vets clean but has no untagged test -- which is the
    # normal state for a package whose only tests are integration-tagged. Counting
    # only "ok" made the baseline depend on that accident: adding an untagged test
    # to internal/service/scene would have moved the number up, and removing one
    # would have failed the clause for no reason a reader could care about.
    # What must not happen is a package FAILING or DISAPPEARING, so those are
    # checked separately and the floor is on total packages accounted for.
    npkg = count_packages(out)
    notests = sum(1 for l in out.splitlines()
                  if l.startswith("?") and "[no test files]" in l)
    if npkg < BASELINE_PACKAGES:
        add("C6 suite", "FAIL",
            f"only {npkg} packages accounted for, baseline is {BASELINE_PACKAGES} "
            f"({notests} with no untagged test) -- a suite that LOST packages is "
            "not a pass")
    else:
        add("C6 suite", "PASS",
            f"{npkg} packages accounted for, {notests} with no untagged test "
            f"(baseline {BASELINE_PACKAGES})")

    # The clause above runs WITHOUT -tags=integration, so it never executes the
    # integration tests -- and in this repo those hold the bulk of the suite,
    # including everything behind the R074 guards. A green verdict from that run
    # says almost nothing about the code this fork actually added.
    #
    # Three things this has to get right, each learned by getting it wrong:
    #
    # 1. -p 1 is not a performance setting. Packages share ONE database and one
    #    set of system users, so running them in parallel has them truncate each
    #    other's tables mid-test. The Makefile says so at `it:`. Without it four
    #    packages fail in about a second each, which reads exactly like a broken
    #    suite and is not one.
    #
    # 2. POSTGRES_DB is a DSN SUFFIX, not a URL. initPostgres prepends
    #    "postgres://", so a full URL yields
    #    "failed to connect to user=alvaro database=postgres://...".
    #
    # 3. THE DATABASE MUST BE THIS SESSION'S OWN. Every package's TestMain calls
    #    pgDropAll, which drops every table, so two runs against one database
    #    destroy each other mid-flight. The signature is
    #    `ERROR: relation "tags" does not exist (SQLSTATE 42P01)` in a package
    #    that passed seconds earlier, and the failure MOVES between packages and
    #    between runs. Measured: 3 failures, then 1, then 0 -- non-deterministic,
    #    which is what a collision looks like and never what a broken test looks
    #    like. Another session was working this same tree throughout.
    #
    # So the clause creates a database named for itself, runs against it, and
    # drops it after. If that is not possible (no container, no permission) it
    # reports UNKNOWN, never a pass.
    dsn, cleanup = isolated_test_db()
    if dsn is None:
        add("C6b integration suite", "UNKNOWN",
            "no integration database available (see the note above); UNKNOWN is "
            "not a pass")
        return
    try:
        rc2, out2, _ = sh(
            f"POSTGRES_DB='{dsn}' go test -tags=integration -count=1 -p 1 ./...",
            timeout=1800)
    finally:
        if cleanup:
            cleanup()
    if rc2 == 124:
        add("C6b integration suite", "UNKNOWN", "timed out")
    elif "is not available" in out2 and "extension" in out2:
        add("C6b integration suite", "UNKNOWN",
            "the database has no pg_search/bktree extension. That is an "
            "ENVIRONMENT gap, not a broken suite: the extensions live in the "
            "container built from docker/production/postgres/Dockerfile, not in "
            "the host's Postgres. UNKNOWN is not a pass.")
    elif rc2 != 0:
        fails = [l.split("\t")[1].split("/")[-1] for l in out2.splitlines()
                 if l.startswith("FAIL\t")]
        reason = ""
        for l in out2.splitlines():
            if "does not exist" in l or "already exists" in l or "SQLSTATE" in l:
                reason = l.strip()[:110]
                break
        add("C6b integration suite", "FAIL",
            f"{len(fails)} failing package(s): " + ", ".join(fails[:5])
            + (f" -- {reason}" if reason else ""))
    else:
        n = sum(1 for l in out2.splitlines() if l.startswith("ok"))
        add("C6b integration suite", "PASS", f"{n} packages green with -tags=integration")


def isolated_test_db():
    """Create a database named for this run; return (dsn_suffix, cleanup|None).

    Returns (None, None) when no suitable database can be reached, which the
    caller reports as UNKNOWN rather than as a pass.
    """
    import secrets
    # The defaults name THIS host's container as it stands, and the two ports are the
    # container's OWN (5432) and the HOST's published one (55434) -- see the note on `iport`
    # below for why they differ.
    #
    # They were `stashbox-pg-r074` on 5436, which is a container that no longer exists on this
    # machine: every clause reported UNKNOWN ("no integration database available") while a
    # perfectly good `sbx-pg` on 55434 was running, and the failure reads as an environment
    # problem rather than as a stale default. Override all five with INTEGRATION_CONTAINER,
    # INTEGRATION_PORT, INTEGRATION_INTERNAL_PORT, INTEGRATION_USER and INTEGRATION_PASSWORD
    # when the container is named or published differently.
    container = os.environ.get("INTEGRATION_CONTAINER", "sbx-pg")
    port = os.environ.get("INTEGRATION_PORT", "55434")
    user = os.environ.get("INTEGRATION_USER", "postgres")
    pw = os.environ.get("INTEGRATION_PASSWORD", "smoke_pw")
    # The port INSIDE the container, which is NOT the port the host publishes.
    #
    # `docker exec <c> psql -h 127.0.0.1 -p <host port>` is refused, because the host's
    # 55434 is a docker-proxy mapping and the server inside the container has never heard
    # of it. Measured against a container whose `docker ps` line reads
    # `127.0.0.1:55434->5432/tcp`:
    #   docker exec sbx-pg psql -h 127.0.0.1 -p 5432 ... -tAc 'SHOW port'  ->  5432
    #   docker exec sbx-pg psql -h 127.0.0.1 -p 55434 ...                 ->  connection refused
    # and the clause reported "no integration database available" for a database that was
    # up and holding six of them.
    #
    # The DSN handed to the suite further down keeps the HOST port, correctly: the suite
    # connects from OUTSIDE the container, where 55434 is the one that works. The two
    # numbers are different because the two callers are in different network namespaces,
    # and using one for both is what this line exists to stop.
    iport = os.environ.get("INTEGRATION_INTERNAL_PORT", "5432")
    # The prefix must identify THIS PROCESS, not this TOOL. Another session was
    # running its own integration suite against a `goalcheck_*` database while this
    # one swept the prefix -- and the symptom was
    #   "FATAL: terminating connection due to administrator command"
    # in five packages, i.e. this run dropping the other run's database mid-flight.
    # That is the same collision the per-run database exists to prevent, arriving
    # through the cleanup path instead of the create path.
    #
    # So the name carries the PID, and the sweep only touches databases whose name
    # starts with this PID's own tag. Residue from a KILLED run of this same PID is
    # unlikely (PIDs are not reused quickly), which is an acceptable trade for never
    # killing someone else's work.
    dbname = f"goalcheck_{os.getpid()}_{secrets.token_hex(4)}"
    myprefix = f"goalcheck_{os.getpid()}_"

    def psql_cmd(database="postgres"):
        # -h 127.0.0.1 -p <iport>, NOT the default unix socket.
        #
        # `psql` with no host connects over /var/run/postgresql/.s.PGSQL.<port>, and that
        # file exists only if the server inside the container was started with its socket
        # in the usual place. Connecting over TCP to the container's OWN port removes the
        # dependence on the socket path entirely.
        #
        # The port here is iport (inside the container), not the host's published port.
        # The earlier version of this comment claimed a container started with `-p 55434`
        # puts its socket at .s.PGSQL.55434 -- it does not, and the claim was the reason
        # the host port was used here, which is the bug that made this clause report
        # UNKNOWN against a running database.
        return ["sudo", "-n", "docker", "exec", container, "psql",
                "-h", "127.0.0.1", "-p", iport, "-U", user, "-d", database]

    def psql(sql, database="postgres"):
        return subprocess.run(
            psql_cmd(database) + ["-c", sql],
            capture_output=True, text=True, timeout=120)

    # Sweep THIS PROCESS's residue. A run killed mid-flight (a timeout, a Ctrl-C, a
    # context limit) never reaches its own cleanup. Scoped to the PID above so a
    # concurrent run's database is never touched.
    stale = subprocess.run(
        psql_cmd("postgres") + ["-tAc",
         f"SELECT datname FROM pg_database WHERE datname LIKE '{myprefix}%'"],
        capture_output=True, text=True, timeout=120)
    for old in stale.stdout.split():
        psql("SELECT pg_terminate_backend(pid) FROM pg_stat_activity "
             f"WHERE datname = '{old}' AND pid <> pg_backend_pid();")
        psql(f'DROP DATABASE IF EXISTS "{old}";')

    created = psql(f'CREATE DATABASE "{dbname}";')
    if created.returncode != 0:
        # Report WHY rather than letting the caller print a bare "no integration
        # database available", which reads as an environment problem when it may be an
        # authentication or connectivity one with a specific fix.
        print(f"[C6b] could not create {dbname}: "
              f"{(created.stderr or created.stdout).strip()[:300]}", flush=True)
        return None, None
    # CREATE DATABASE prints "CREATE DATABASE" to stdout even on some failure paths, so
    # confirm the database is really there before handing back a DSN.
    probe = subprocess.run(
        psql_cmd("postgres") + ["-tAc",
         f"SELECT 1 FROM pg_database WHERE datname = '{dbname}'"],
        capture_output=True, text=True, timeout=120)
    if probe.stdout.strip() != "1":
        print(f"[C6b] {dbname} not present after CREATE: "
              f"{(probe.stderr or probe.stdout).strip()[:300]}", flush=True)
        return None, None

    # The extensions must exist in the NEW database. CREATE DATABASE copies nothing from
    # template1 unless the extension was installed THERE, and this function creates an
    # empty database -- so every subsequent run found migration 56 failing on a missing
    # pg_search, and C6b reported UNKNOWN for a reason that has nothing to do with the
    # suite. Installing them per-database is what makes the isolation worth anything: a
    # shared database would have the extensions and would also share its tables.
    for ext in ("pg_search", "bktree"):
        made = psql(f'CREATE EXTENSION IF NOT EXISTS "{ext}";')
        if made.returncode != 0:
            print(f"[C6b] could not create extension {ext} in {dbname}: "
                  f"{(made.stderr or made.stdout).strip()[:300]}", flush=True)
            # Drop the database rather than handing back a DSN that will fail inside the
            # suite, where the error names a migration instead of the setup.
            psql(f'DROP DATABASE IF EXISTS "{dbname}";')
            return None, None

    def cleanup():
        # Terminate stragglers first: a still-open connection blocks DROP DATABASE
        # and leaves the next run with a database it did not create.
        psql("SELECT pg_terminate_backend(pid) FROM pg_stat_activity "
             f"WHERE datname = '{dbname}' AND pid <> pg_backend_pid();")
        psql(f'DROP DATABASE IF EXISTS "{dbname}";')

    return f"{user}:{pw}@127.0.0.1:{port}/{dbname}?sslmode=disable", cleanup


# ---------------------------------------------------------------------------
# C7 -- build, vet and gofmt are clean
# ---------------------------------------------------------------------------
def c7_build_clean():
    checks = [
        ("build", "go build ./..."),
        ("vet", "go vet ./..."),
        ("gofmt", "gofmt -l internal pkg cmd graphql 2>/dev/null"),
        # The tree's own invariant, and it belongs in "the tree is clean" rather than in a clause of
        # its own: this is not a claim about the programme's behaviour, it is the mechanical refusal
        # the respec of 2026-10-15 makes -- no player, no embed, no proxy, no transcode, no frame of
        # anybody else's video. See the script's docstring for what a hit means and why the pattern
        # list is checked to be empty of pre-existing hits.
        ("content plane", "python3 scripts/check-no-content-plane.py"),
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
        add("C7 build clean", "PASS", "build, vet, gofmt and the content-plane refusal all clean")


def update_baseline():
    """Rewrite BASELINE_PACKAGES from the packages currently green.

    The floor is a guard against a suite that LOST tests, which is invisible in a
    pass/fail verdict. It is also a number that drifts every time a package gains
    a test file, so leaving it to be edited by hand guarantees it will be stale and
    will report a figure that is no longer true.
    """
    rc, out, _ = sh("go test ./... -count=1", timeout=1800)
    # MUST match c6_suite() exactly. It counted `ok` only while the clause counts
    # `ok` AND `?  [no test files]`, so --update-baseline wrote 31 where the clause
    # measures 60 and the floor became decorative all over again -- the same failure
    # the comment on BASELINE_PACKAGES describes having already been fixed once.
    #
    # A floor is only a guard if it is the same metric as the comparison. Sharing one
    # function is the only way to keep it that way, so this calls it.
    npkg = count_packages(out)
    if rc != 0 or npkg == 0:
        print(f"refusing to update: rc={rc}, {npkg} packages green", file=sys.stderr)
        return 1
    src = Path(__file__).read_text()
    new = re.sub(r"BASELINE_PACKAGES = \d+", f"BASELINE_PACKAGES = {npkg}", src)
    if new != src:
        Path(__file__).write_text(new)
        print(f"BASELINE_PACKAGES -> {npkg}")
    else:
        print(f"BASELINE_PACKAGES already {npkg}")
    return 0


def c8_aggregator_programme():
    spec = REPO / "docs" / "spec" / "feature-content-aggregator.md"
    plan = REPO / "docs" / "plan" / "feature-content-aggregator.md"
    track = REPO / "docs" / "track" / "TRACK-content-aggregator.md"
    for p in (spec, plan, track):
        if not p.exists():
            add("C8 aggregator programme", "FAIL",
                f"{p.relative_to(REPO)} is missing")
            return

    # The main SPEC must point at all three, or a reader of the spec never learns
    # the programme exists -- which is how a spec acquires a second, unmentioned half.
    main_spec = (REPO / "docs" / "SPEC.md").read_text()
    for name in ("docs/spec/feature-content-aggregator.md",
                 "docs/plan/feature-content-aggregator.md",
                 "docs/track/TRACK-content-aggregator.md"):
        if name not in main_spec:
            add("C8 aggregator programme", "FAIL",
                f"docs/SPEC.md does not reference {name}")
            return

    phases = re.findall(r"^## Phase (\d+)\b", plan.read_text(), re.M)
    if not phases:
        add("C8 aggregator programme", "FAIL", "the plan names no phases")
        return

    rows = {}
    for line in track.read_text().splitlines():
        m = re.match(r"\|\s*(\d+)\s*\|([^|]*)\|([^|]*)\|([^|]*)\|", line)
        if m:
            rows[m.group(1)] = (m.group(3).strip().lower(), m.group(4).strip())

    undone, noevidence = [], []
    for n in phases:
        if n not in rows:
            undone.append(n)
            continue
        state, evidence = rows[n]
        if not state.startswith("done"):
            undone.append(n)
        elif evidence in ("", "—", "-", "(none)"):
            noevidence.append(n)

    if noevidence:
        add("C8 aggregator programme", "FAIL",
            f"phase(s) marked done with no evidence: {', '.join(noevidence)}")
    elif undone:
        add("C8 aggregator programme", "FAIL",
            f"{len(undone)}/{len(phases)} phases not done: {', '.join(undone)}")
    else:
        add("C8 aggregator programme", "PASS",
            f"all {len(phases)} phases done, each naming its evidence")


def c9_monetization_posture():
    """§18 as a posture the tree can be checked against, clause by clause.

    Every check here has a SUBJECT COUNT, and that is the lesson this programme keeps paying for: an assertion
    that scans a set and finds nothing wrong is also what an assertion over an EMPTY set looks like. "No schema
    directive mentions a subscription" is worth nothing if the scan read no directives, and "no shipped config
    turns monetization on" is worth nothing if it read no config files -- so both count what they examined and
    fail when the count is zero.
    """
    mon = REPO / "internal" / "service" / "monetization"
    alwaysfree = mon / "alwaysfree.go"
    gate = mon / "monetization.go"
    for p in (alwaysfree, gate):
        if not p.exists():
            add("C9 monetization posture", "FAIL", f"{p.relative_to(REPO)} is missing")
            return

    free_src = alwaysfree.read_text()
    gate_src = gate.read_text()
    fails, notes = [], []

    # 1. §18.2's seven items, by the names the gate refuses by -- not by §18.2's prose, which is what the code
    #    quotes beside them.
    ITEMS = ["search", "content", "tagging", "collections", "api_access", "data_export", "federation"]
    named = [n for n in ITEMS if f'Name:  "{n}"' in free_src or f'Name: "{n}"' in free_src]
    if len(named) != len(ITEMS):
        fails.append(f"always-free capabilities missing from alwaysfree.go: "
                     f"{', '.join(n for n in ITEMS if n not in named)}")
    else:
        notes.append(f"{len(named)} §18.2 capabilities in code")

    # 2. The gate asks §18.2 FIRST. Source order is the rule here: a paywall that checked its price list first
    #    would refuse a core path before the always-free list ever saw it.
    in_gate = gate_src.split("func Gate(")[1].split("\n}")[0] if "func Gate(" in gate_src else ""
    i_free, i_price = in_gate.find("alwaysFreeByName("), in_gate.find("premiumFeatureByName(")
    if not in_gate:
        fails.append("Gate is not in internal/service/monetization/monetization.go")
    elif not (0 <= i_free < i_price):
        fails.append("Gate does not consult alwaysFreeByName before the price list")
    else:
        notes.append("§18.2 consulted before §18.1 in Gate")

    # 3. Nothing in the served schema gates on a subscription. The directive list is the one place such a rule
    #    would have to appear to be enforced by the schema, so it is what this reads.
    schemas = sorted((REPO / "graphql").rglob("*.graphql"))
    directives, bad = [], []
    for s in schemas:
        directives += re.findall(r"^directive\s+@(\w+)", s.read_text(), re.M)
    WORDS = ("monetiz", "premium", "subscri", "entitle", "paywall")
    for d in directives:
        if any(w in d.lower() for w in WORDS):
            bad.append(d)
    if not directives:
        fails.append("no schema directives were found: the scan has no subject")
    elif bad:
        fails.append(f"schema directives that gate on a subscription: {', '.join(bad)}")
    else:
        notes.append(f"{len(directives)} schema directives, none a paywall")

    # 4. The limiter is handed no second limit unless the operator is selling the tier: the clause is the one
    #    that keeps the startup line from stating a premium rate nobody can reach.
    limits = gate_src.split("func Limits(")[1].split("\n}")[0] if "func Limits(" in gate_src else ""
    if "RequireStream(StreamPremiumSubscription)" not in limits or "ratelimit.Config{}" not in limits:
        fails.append("Limits does not refuse a premium limit while the premium stream is off")
    else:
        notes.append("the second limit exists only while the stream is on")

    # 5. Every configuration this repository ships leaves the switch off, or says nothing about it.
    configs = sorted(list(REPO.glob("*.yml")) + list(REPO.glob(".config-dev/*.yml")) +
                     list(REPO.glob("e2e/config/*.yml")))
    on = [c.relative_to(REPO).as_posix() for c in configs
          if re.search(r"^\s*monetization_enabled:\s*true", c.read_text(), re.M)]
    if len(configs) < 3:
        fails.append(f"only {len(configs)} configuration file(s) were scanned: the check has no subject")
    elif on:
        fails.append(f"shipped configuration(s) switch monetization on: {', '.join(on)}")
    else:
        notes.append(f"{len(configs)} shipped configuration(s), none switching it on")

    if fails:
        add("C9 monetization posture", "FAIL", "; ".join(fails))
    else:
        add("C9 monetization posture", "PASS", "; ".join(notes))


# ---------------------------------------------------------------------------
# C10/C11 -- phase 10 (the media plane) and phase 12 (federation) as surfaces
# ---------------------------------------------------------------------------
# Written by phase 17's own rule: "phase N's clause is written in phase N's commit, so the predicate is never
# behind the code". Each clause asserts what EXISTS today and names what does not as UNKNOWN -- never as PASS
# (this file's first rule) and never as FAIL, because a slice that has not been started is not a regression.
#
# Both clauses count their subjects, for C9's reason: "the outbound half has a caller" is worth nothing if the
# search read no files, and a grep over an empty set looks exactly like a grep that found nothing wrong.


def count_in(rel, pattern, exclude_tests=True):
    """Files under `rel` whose text matches `pattern`, plus the total scanned. Both numbers, always."""
    hits, scanned = [], 0
    for p in sorted((REPO / rel).rglob("*.go")):
        if exclude_tests and p.name.endswith("_test.go"):
            continue
        scanned += 1
        if re.search(pattern, p.read_text(), re.M):
            hits.append(p.relative_to(REPO).as_posix())
    return hits, scanned


def c10_media_plane_posture():
    """Phase 10: the posture is enforced by something, and the enforcement has a caller."""
    cfg = REPO / "internal" / "config" / "config.go"
    route = REPO / "internal" / "api" / "routes_image.go"
    plane = REPO / "internal" / "service" / "mediaplane" / "mediaplane.go"
    for p in (cfg, route, plane):
        if not p.exists():
            add("C10 media-plane posture", "FAIL", f"{p.relative_to(REPO)} is missing")
            return

    cfg_src, route_src, plane_src = cfg.read_text(), route.read_text(), plane.read_text()
    fails, notes = [], []

    # 1. The shipped defaults. The struct fields carry a type and the defaults carry a colon, so this reads the
    #    defaults without matching the declarations -- and a `true` anywhere is a FAIL, not a note.
    for key in ("ContentDelivery", "ImagesEnabled"):
        if re.search(rf"{key}:\s*true", cfg_src):
            fails.append(f"{key} defaults to true in the shipped config")
    defaults = len(re.findall(r"(?:ContentDelivery|ImagesEnabled):\s*false", cfg_src))
    if defaults != 2:
        fails.append(f"{defaults} of 2 posture defaults are false in the shipped config")
    else:
        notes.append("content_delivery and images_enabled both ship false")

    # 2. The gate is ON THE ROUTE, and BEFORE the routes it guards. Source order is the rule: a gate placed
    #    after the handlers is a gate the handlers never meet.
    routes = re.findall(r"^\s*r\.Get\(", route_src, re.M)
    gate_at = route_src.find("r.Use(requireImageBytes)")
    first_at = route_src.find("r.Get(")
    if not routes:
        fails.append("routes_image.go registers no routes: the scan has no subject")
    elif gate_at < 0:
        fails.append("the image router carries no gate")
    elif not (0 <= gate_at < first_at):
        fails.append("the image gate is registered after the routes it guards")
    else:
        notes.append(f"the gate guards {len(routes)} image route(s), and precedes them")

    # 3. The gate has a CALLER, and the refusal names the keys an operator can act on. This is the clause that
    #    would have failed before slice 10a-ii: the media plane's switch existed and nothing asked.
    callers, scanned_route = count_in("internal/api", r"requireImageBytes")
    if not callers:
        fails.append("nothing calls the image gate: a switch with no caller is a comment")
    elif len(callers) != 1:
        fails.append(f"the image gate is referenced from {len(callers)} files: {', '.join(callers)}")
    else:
        notes.append(f"the gate has exactly one caller ({callers[0]}, of {scanned_route} non-test files)")

    for key in ("images_enabled", "content_delivery"):
        if key not in plane_src:
            fails.append(f"the refusal does not name {key}, so an operator cannot find the line to change")
    if len(fails) == 0:
        notes.append("the refusal names both keys")

    # 4. The instruments exist: a live walk (the real binary, two arms) and a mutation battery.
    for script, label in (("scripts/walk-images-gate.py", "the live walk"),
                          ("scripts/mutate-image-gate.py", "the mutation battery")):
        if not (REPO / script).exists():
            fails.append(f"{label} ({script}) is missing")
    if not fails:
        notes.append("the walk and the battery are in the tree")

    add("C10 media-plane posture", "FAIL" if fails else "PASS",
        "; ".join(fails) if fails else "; ".join(notes))

    # 5. The slices that have no surface to assert. Named, so that a reader of this output learns what is left
    #    rather than reading a PASS over a phase three-quarters unbuilt.
    add("C10b media-plane slices", "UNKNOWN",
        "the variant ladder, hosting, the media toolchain, the players and the resize cache are not "
        "started: there is no surface to assert yet, and this clause will not report PASS for a phase "
        "whose remaining slices do not exist. What IS built and asserted elsewhere: slice 10a-ii (the "
        "gate) by C10, 10b-1 (the media directory) by C10c, 10b-2 (the export invariant) by C10d")


def c10c_media_directory():
    """Phase 10, slice 10b-1: the switch says WHETHER, the directory says WHERE FROM, and the second has a
    caller. The failure this clause exists for is an instance whose switch is on and whose directory is
    missing: it answers 404 for every image it holds a row for, which is indistinguishable from an instance
    that has no such image."""
    cfg = (REPO / "internal" / "config" / "config.go").read_text()
    plane = (REPO / "internal" / "service" / "mediaplane" / "mediaplane.go").read_text()
    store_path = REPO / "internal" / "service" / "mediaplane" / "store.go"
    fails, notes = [], []

    if 'MediaDir string `mapstructure:"media_dir"`' not in cfg:
        fails.append("`media_dir` is not a configuration key")
    if "MediaDir: \"\"" not in cfg:
        fails.append("`media_dir` does not default to empty: every instance would serve from a path it "
                     "never chose")
    else:
        notes.append("media_dir ships unset, so nothing serves from a guessed path")
    if "func GetMediaDir() string" not in cfg:
        fails.append("`media_dir` has no accessor: a key with no reader is a comment")

    if not store_path.exists():
        fails.append("there is no storage check: the switch is all there is")
    else:
        store = store_path.read_text()
        # The shape the code has, not the shape a reader remembers: ErrNoMediaDir is a var and
        # ErrMediaDirUnusable is a TYPE with a constructor-free struct literal, and the first version of
        # this line looked for `var` on both and failed a slice that was built correctly.
        if "ErrNoMediaDir =" not in store or "type ErrMediaDirUnusable struct" not in store:
            fails.append("the storage refusal is not by name")
        for want in ("the path does not exist", "the path is not a directory", "cannot be read"):
            if want not in store:
                fails.append(f"the storage check does not distinguish {want!r}")
        if "MediaDirError()" not in plane:
            fails.append("the image gate does not consult the storage: the switch is on and the directory "
                         "is missing is a state nothing refuses")
        else:
            notes.append("the gate consults the storage after the switch, and the refusal names the path")
    add("C10c media directory", "FAIL" if fails else "PASS",
        "; ".join(fails) if fails else "; ".join(notes))


def c10d_export_invariant():
    """Phase 10, slice 10b-2: a media directory inside an NFS export is shared with the LAN."""
    path = REPO / "internal" / "service" / "mediaplane" / "exports.go"
    if not path.exists():
        add("C10d export invariant", "FAIL", "there is no export invariant: exports.go is missing")
        return
    src = path.read_text()
    fails, notes = [], []

    for want, why in (("func ExportedPaths()", "the export table is not read"),
                      ("func ParseExportTable(", "the table is not parsed"),
                      ("func InsideAnExport(", "nothing decides whether a directory is inside an export"),
                      ("func MediaDirExportProblem(", "the invariant has no entry point"),
                      ("ErrMediaDirExported", "the refusal is not a named error")):
        if want not in src:
            fails.append(why)
    # THE FALSE PASS, as a checked invariant of the invariant: the table is read from the file exportfs
    # reads, because `exportfs -v` as an unprivileged user prints nothing and exits ZERO -- so a reader
    # that trusted it reported "this host exports nothing" on a host exporting four paths.
    if "/var/lib/nfs/etab" not in src:
        fails.append("the export table is not read from /var/lib/nfs/etab: `exportfs -v` without root "
                     "prints nothing and exits zero, which reports a host that exports four paths as "
                     "exporting none -- a false pass")
    else:
        notes.append("the table is read from the file exportfs reads, not from an unprivileged exportfs")
    if "UNKNOWN" not in src:
        fails.append("the reader does not say that an unreadable table leaves the answer unknown")
    walk = REPO / "scripts" / "walk-images-gate.py"
    if not walk.exists() or "/var/lib/nfs/etab" not in walk.read_text():
        fails.append("the walk does not read the real export table, so the invariant is never exercised "
                     "against a host that exports anything")
    else:
        notes.append("the walk exercises it against this host's real table")
    add("C10d export invariant", "FAIL" if fails else "PASS",
        "; ".join(fails) if fails else "; ".join(notes))


def c11_federation_posture():
    """Phase 12: both ends of the wire are wired, and the rule that decides what crosses has a reader."""
    server = REPO / "internal" / "api" / "server.go"
    answer = REPO / "internal" / "service" / "federation" / "answer.go"
    exchange = REPO / "internal" / "service" / "federation" / "exchange.go"
    mapping = REPO / "internal" / "service" / "federation" / "mapping.go"
    for p in (server, answer, exchange, mapping):
        if not p.exists():
            add("C11 federation posture", "FAIL", f"{p.relative_to(REPO)} is missing")
            return

    server_src = server.read_text()
    fails, notes = [], []

    # 1. Both peer-facing routes are registered. A route that exists as a handler and is not mounted answers
    #    nothing, which is the shape this programme keeps finding.
    for const, label in (("federation.AnswerPath", "the answer route"),
                         ("federation.TagsPath", "the tag table route")):
        if const not in server_src:
            fails.append(f"{label} ({const}) is not registered in server.go")
    if not fails:
        notes.append("POST /federation/identify and GET /federation/tags are registered")

    # 2. THE OUTBOUND HALF HAS A CALLER. This is the clause that would have failed for the whole of phase 12
    #    before slice 12f: `Client.Broadcast` was called from tests and from nowhere else, so the wire had one
    #    end wired and every test still passed.
    #    Three explicit places, rather than a fuzzy count: an earlier version of this clause searched the tree
    #    for "Broadcast|ExchangeOntologies" and reported "2 production callers" -- one of which was the
    #    definition of ExchangeOntologies in its own file. A count that includes a definition overstates by
    #    exactly the thing it is meant to prove.
    #    Subject counts on every scan, so "no caller" cannot be the output of reading no files.
    cron_callers, cron_scanned = count_in("internal/cron", r"federation\.ExchangeOntologies\(")
    client_makers, _ = count_in("internal/cron", r"federation\.NewClient\(")
    #    The call site is found by CALL SHAPE, not by the receiver's name: the first version of this line
    #    matched `c.Broadcast(` and reported "nothing calls it" against a file that calls
    #    `client.Broadcast(` -- a false FAIL, which is the one direction of error a predicate must not have,
    #    because it sends the reader to look for work already done. A `func ...) Broadcast(` definition is a
    #    declaration, so those lines are excluded rather than the variable being guessed.
    broadcast_callers, fed_scanned = [], 0
    for p in sorted((REPO / "internal" / "service" / "federation").rglob("*.go")):
        if p.name.endswith("_test.go"):
            continue
        fed_scanned += 1
        calls = [ln for ln in p.read_text().splitlines() if ".Broadcast(" in ln and not ln.strip().startswith("func ")]
        if calls:
            broadcast_callers.append(p.relative_to(REPO).as_posix())
    if not cron_callers:
        fails.append("the cron does not exchange ontologies: the outbound half has no production caller")
    if not client_makers:
        fails.append("the cron builds no client: the exchange cannot leave the process")
    if not broadcast_callers:
        fails.append("nothing calls Client.Broadcast outside the tests: the wire has one end wired")
    if not fails:
        notes.append(f"the outbound path has a production caller ({cron_callers[0]}, of {cron_scanned} "
                     f"cron files; Broadcast is called from {broadcast_callers[0]}, of {fed_scanned} "
                     f"federation files)")

    # 3. The answering half has its own caller, and the negotiation is field by field rather than a single
    #    boolean: a rule that answered one bit could not tell a peer which field to stop sending.
    if "FederationIdentify(" not in server_src:
        fails.append("the answering half is not mounted")
    wire = (REPO / "internal" / "service" / "federation" / "wire.go").read_text()
    if "Crossing []OntologyFieldCrossing" not in wire:
        fails.append("the answer carries no field-by-field crossing")
    else:
        notes.append("the answer carries a per-field crossing")

    # 4. The instruments.
    for script, label in (("scripts/walk-12b-live.py", "the live walk"),
                          ("scripts/mutate-answer-12b.py", "the answering battery"),
                          ("scripts/mutate-mapping-12e.py", "the mapping battery")):
        if not (REPO / script).exists():
            fails.append(f"{label} ({script}) is missing")
    if not fails:
        notes.append("the walk and both batteries are in the tree")

    add("C11 federation posture", "FAIL" if fails else "PASS",
        "; ".join(fails) if fails else "; ".join(notes))

    # 5. What phase 12 still owes, named. The identification question is REFUSED BY NAME today (an empty answer
    #    would be indistinguishable from "nothing to report"), and core v2 is a field name with no document
    #    behind it -- both are real work, and neither is a regression this clause should fail on.
    add("C11b federation slices", "UNKNOWN",
        "the identification half (refused by name rather than answered) is not built: no surface to assert. "
        "Core v2's READER is built and asserted by C11c; its writer is named by C11d")


def c10e_ladder_storage():
    """Phase 10, slice 10c-1: the variant ladder's layout and its two invariants -- with its producer NOT built."""
    ladder = REPO / "internal" / "service" / "mediaplane" / "ladder.go"
    route = REPO / "internal" / "api" / "routes_image.go"
    subset = REPO / "internal" / "api" / "routes_image_ladder_test.go"
    for p in (ladder, route, subset):
        if not p.exists():
            add("C10e ladder storage", "FAIL", f"{p.relative_to(REPO)} is missing")
            return
    src, route_src, tests = ladder.read_text(), route.read_text(), subset.read_text()
    fails, notes = [], []

    if "func (s *Store) VariantPath(" not in src:
        fails.append("no layout: nothing names where a variant lives")
    if "func ResolveStore()" not in src:
        fails.append("the store is not resolved once per process, so the filesystem comparison -- which is only "
                     "meaningful against the value taken when the process STARTED -- has nothing to compare to")
    if "func (s *Store) Check() error" not in src or "ErrMountChanged" not in src:
        fails.append("the mount check is missing: an unmounted disk leaves an ordinary empty directory behind, "
                     "which existence and readability both pass")
    else:
        notes.append("a directory on a different filesystem is refused by name")
    if "ErrDirsOverlap" not in src:
        fails.append("the cache-inside-the-store invariant is missing: one cleanup of the resize cache would "
                     "take the instance's images with it")
    else:
        notes.append("the resize cache and the store may not be each other")
    # The alignment invariant, which is the defect this slice found in itself.
    if "TestEveryStoredVariantWidthIsOneTheRouteCanAskFor" not in tests:
        fails.append("nothing checks that the widths the ladder stores are widths the route can ask for: the "
                     "first version of this list was the measurement's (320, 480) against the route's "
                     "(300, 600, 1280), i.e. files no request could ever ask for")
    if "mediaplane.IsVariantWidth(size)" not in route_src:
        fails.append("the image route does not consult the ladder, so the layout is a comment rather than a path "
                     "anything reads")
    else:
        notes.append("the route asks the ladder before the database, so the layout is exercised by requests")
    add("C10e ladder storage", "FAIL" if fails else "PASS",
        "; ".join(fails) if fails else "; ".join(notes))

    add("C10f ladder producer", "UNKNOWN",
        "nothing fetches an upstream image, re-encodes it or writes a variant: this instance SERVES a variant it "
        "is handed and PRODUCES none. The storage, its invariants and its caller exist; the thing that fills it "
        "does not, and it is named here rather than implied by a layout that looks finished")


def c11c_core_v2():
    """Phase 12, slice 12g: core v2 exists as a READER -- a version number that means something. Nothing in
    the tree writes a category yet, and this clause says so rather than letting the version look finished."""
    path = REPO / "internal" / "service" / "federation" / "ontology.go"
    mapping = REPO / "internal" / "service" / "federation" / "mapping.go"
    test_path = REPO / "internal" / "service" / "federation" / "ontology_v2_test.go"
    for p in (path, mapping, test_path):
        if not p.exists():
            add("C11c core v2", "FAIL", f"{p.relative_to(REPO)} is missing")
            return
    src, map_src, tests = path.read_text(), mapping.read_text(), test_path.read_text()
    fails, notes = [], []

    if "func parseCanonicalAt(snapshot string, version int)" not in src:
        fails.append("no reader takes a version: a document is parsed as whatever the code assumes")
    if "ErrFieldNotInVersion" not in src:
        fails.append("a field a version does not have is not refused by name")
    if "version < CoreOntologyV2" not in src or "version >= CoreOntologyV2" not in src:
        fails.append("the version rule is one-sided: both directions have to refuse, or a peer claiming v2 "
                     "while sending v1 edges is undetectable")
    # The digest-stability invariant, as a checked artefact: the field must be omitempty AND a recorded
    # digest must exist in the tests, since the claim is about bytes written before the field existed.
    if 'Category string `json:"category,omitempty"`' not in src:
        fails.append("the category field is not omitempty: adding it changes the canonical bytes of every "
                     "v1 document, so two instances that agree would report a disagreement")
    elif "428b05a671d8dc251663952622f556ae8250b3c838e6b43158e9db780396663a" not in tests:
        fails.append("no recorded digest pins the v1 canonical form")
    else:
        notes.append("the v1 digest is pinned by a recorded value, so the field cannot move it")
    if "func CanonicalDigestAt(" not in src:
        fails.append("the digest cannot be computed at a chosen version")
    if "case CoreOntologyV1, CoreOntologyV2:" not in map_src:
        fails.append("core v2 reports no vocabulary, which reads as 'nothing crosses' rather than as "
                     "'not implemented'")
    else:
        notes.append("core v2 resolves against v1's names, because v2 changes the edge and not the names")
    add("C11c core v2", "FAIL" if fails else "PASS",
        "; ".join(fails) if fails else "; ".join(notes))

    add("C11d core v2 writer", "UNKNOWN",
        "this instance SPEAKS v2 (it parses, validates, digests and refuses by name) and WRITES v1: the "
        "ontology tables hold {tag, implies} rows with no categories, so a v2 document cannot be emitted "
        "from them yet. Named rather than implied, because a version whose reader exists and whose writer "
        "does not is exactly the state a status line would otherwise round up")


def main():
    if "--update-baseline" in sys.argv:
        sys.exit(update_baseline())
    for fn in (c1_prs_decided, c2_issues_dispositioned, c3_plan_built,
               c4_narrow_merged, c5_migrations_coherent, c6_suite, c7_build_clean,
               c8_aggregator_programme, c9_monetization_posture,
               c10_media_plane_posture, c10c_media_directory, c10d_export_invariant,
               c11_federation_posture, c10e_ladder_storage, c11c_core_v2):
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
