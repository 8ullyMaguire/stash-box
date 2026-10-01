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
        if not re.search(r"status:.*\b(built|implemented|shipped|complete)", status, re.I):
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
    _, out, _ = sh("git for-each-ref --format=%(refname:short) refs/heads/")
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
    container = os.environ.get("INTEGRATION_CONTAINER", "stashbox-pg-r074")
    port = os.environ.get("INTEGRATION_PORT", "5436")
    user = os.environ.get("INTEGRATION_USER", "postgres")
    pw = os.environ.get("INTEGRATION_PASSWORD", "smoke_pw")
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

    def psql(sql, database="postgres"):
        return subprocess.run(
            ["sudo", "-n", "docker", "exec", container, "psql", "-U", user,
             "-d", database, "-c", sql],
            capture_output=True, text=True, timeout=120)

    # Sweep THIS PROCESS's residue. A run killed mid-flight (a timeout, a Ctrl-C, a
    # context limit) never reaches its own cleanup. Scoped to the PID above so a
    # concurrent run's database is never touched.
    stale = subprocess.run(
        ["sudo", "-n", "docker", "exec", container, "psql", "-U", user,
         "-d", "postgres", "-tAc",
         f"SELECT datname FROM pg_database WHERE datname LIKE '{myprefix}%'"],
        capture_output=True, text=True, timeout=120)
    for old in stale.stdout.split():
        psql("SELECT pg_terminate_backend(pid) FROM pg_stat_activity "
             f"WHERE datname = '{old}' AND pid <> pg_backend_pid();")
        psql(f'DROP DATABASE IF EXISTS "{old}";')

    if psql(f'CREATE DATABASE "{dbname}";').returncode != 0:
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


def main():
    if "--update-baseline" in sys.argv:
        sys.exit(update_baseline())
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
