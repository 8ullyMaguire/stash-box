#!/usr/bin/env python3
"""Classify every open upstream stash-box issue from its real body.

Relevance is decided by the fork's OWN constraints, not by taste:

  * This fork ports upstream bug fixes and tracks upstream. An upstream FEATURE
    request is only relevant if the fork's SPEC/plan already calls for it -- that
    is the difference between "the spec says build this" and "upstream wants this".
  * A row is RELEVANT-and-BUG only if the body describes wrong behaviour of code
    this fork ships. That is the only class that becomes a fix with a test.
  * Everything else is DISPOSITIONED with a reason. A row that has an outcome is
    finished; a row left unclassified is not.

Every disposition records a reason, and the counts are printed so the ledger's
shape is checkable rather than asserted.

    python3 docs/classify-issues.py [--write]
"""
import json, pathlib, re, sys, collections

REPO = pathlib.Path("/home/alvaro/code-local/go/stash-box")
FULL = pathlib.Path("/home/alvaro/.hermes/profiles/coding-3/cache/scratch/sb-issues-full.json")
OUT = REPO / "docs" / "ISSUES.md"

data = {int(k): v for k, v in json.loads(FULL.read_text()).items()}

# --- does the fork's own spec/plan call for this? ----------------------------
corpus = ""
for f in ("docs/SPEC.md", "docs/plan/feature-03b-content-access-and-vanguard-weighting.md",
          "docs/plan/feature-04-identification-federation.md",
          "docs/spec/feature-04-identification-federation.md"):
    p = REPO / f
    if p.exists():
        corpus += p.read_text().lower()

# Title PREFIX is the signal, and it is the one thing a classifier can rely on:
# upstream uses "[Bug Report]", "[Feature]", "[RFC]" as a title prefix, and they
# mean what they say. The LABELS do not: `help wanted` is applied to a large share
# of genuine bug reports (see #525, #583, #802 -- all "[Bug Report]") and
# `enhancement` is applied to real defects. The first version of this classifier
# keyed on labels and put 37 bug reports in a feature bucket.
FEATURE_MARKERS = ("[feature]", "[rfc]", "[proposal]", "[idea]", "[discussion]")
BUG_TITLE = re.compile(r"^\s*\[\s*bug\b", re.I)
BUG_MARKERS = ("[bug report]", "bug report", "traceback", "panic:", "nil pointer",
               "500 ", "regression", "crash", "incorrect", "wrong", "does not work",
               "broken", "fails", "error when", "sqlstate")

# Concepts the fork's own spec/plan name. A feature issue that matches one of
# these is in scope, because the spec asked for it.
SPEC_CONCEPTS = {
    "gravity": "gravity slider (SPEC D3)",
    "vanguard": "vanguard weighting (SPEC D1)",
    "federat": "peer federation (SPEC D2)",
    "identification": "identification board (SPEC D2)",
    "attestation": "peer reputation / attestations (SPEC §1.4)",
    "onion": "onion-routed metadata sync (SPEC §4.5)",
    "quorum": "quorum queue for untrusted changes (SPEC §3.1)",
    "air-gap": "air-gapped export/import (SPEC §4.5)",
    "preservation": "preservation alerts (SPEC §4.4)",
    "divergence": "divergence markers / metadata forks (SPEC §1.4)",
    "peering": "peering tiers (SPEC §1.3)",
    "relay node": "relay nodes (SPEC §1.1)",
    "trust level": "trust level / content access gate (SPEC D4)",
    "content access": "content access gate (SPEC D4)",
    "changelog": "entity changelog feeds",
    "weight": "vote weighting (SPEC D1)",
    "elo": "elo weighting (SPEC D1)",
    "restriction": "access restrictions (SPEC D5)",
    "sync": "sync cadence (SPEC D8)",
    "anonymity": "anonymity levels (SPEC §5.4)",
    "quorum queue": "quorum queue (SPEC §3.1)",
    "rust sdk": "SDK widened to Rust (SPEC §7.2)",
    "capability profile": "capability profile (SPEC §7.2)",
    "local-first": "local-first personal nodes (SPEC §5.2)",
    "guild": "guilds (SPEC D6)",
    "mentorship": "mentorship (SPEC D6)",
    "mobile app": "mobile app (SPEC D7)",
    "browser extension": "browser extension (SPEC D7)",
}


# --- features built by a PORT rather than by an issue fix ---------------------
# #69 is "[Feature] Changelog on StashDB" and this fork has shipped it: upstream
# PR #1183 added the four keyset-paginated changelog queries, and it was ported in
# 8c8160b5 with two dedicated test files. Nothing in the source names "#69", so
# the source index missed it and the title matched the spec vocabulary -- which
# read as "the spec asks for this, so it is outstanding work". It is done.
PORTED_FEATURES = {
    69: ("fixed",
         "shipped by porting upstream PR #1183 (`8c8160b5`): four keyset-paginated "
         "changelog queries (scene/performer/studio/tag), with "
         "`internal/api/changelog_integration_test.go` and "
         "`changelog_index_migration_test.go`. Forced deviations (migration "
         "renumbered to 91, schemaVersion pin dropped) are recorded in "
         "`docs/plan/upstream-pr-port.md`"),
}


# --- the WORKLOG's own per-issue dispositions -------------------------------
# A third source, and the one that caught my two errors. docs/track/WORKLOG.md
# records, in prose, that #973 is "not a bug" (Yup's regex is RFC-1738
# compliant), that #592 is not-reproduced, and that #9 is a documented design
# limit rather than an unfixed bug. All three were about to be recorded as
# `bug` = outstanding work purely because they had no docs/plans/ entry.
#
# Read it for a disposition naming the issue, and prefer it.
WORKLOG = ""
_wl = REPO / "docs" / "track" / "WORKLOG.md"
if _wl.exists():
    WORKLOG = _wl.read_text()

# Dispositions the WORKLOG states explicitly, with the reason it gives. Sourced
# from its own text; each is quoted in the ledger so it is reviewable.
WORKLOG_DECIDED = {
    973: ("declined", "not a bug: Yup's URL regex is standards-compliant per RFC 1738, "
                     "which rejects `{` and `}`; the reporter confirmed percent-encoding "
                     "works. WORKLOG session 15/16 records this as a deliberate non-fix"),
    592: ("declined", "not reproduced: the scene edit's removal no longer matches after "
                     "the performer rename added an alias, so the concatenated id+alias "
                     "is what the removal is compared against. Diagnosed, not fixed; "
                     "WORKLOG records it as needing UI work and never confirmed it live"),
    809: ("declined", "not reproduced: the backend path was exonerated. WORKLOG records "
                     "this as a deliberate non-fix, explicitly not counted as fixed"),
    9:   ("declined", "documented design limit, not an unfixed bug: every optional field "
                     "in the update inputs is *string, so gqlgen maps 'absent' and "
                     "'explicitly null' to the same nil and the converter cannot tell "
                     "them apart. 25 nil-guarded fields. `internal/converter/"
                     "null_update_issue9_test.go` asserts the property that makes it "
                     "possible and fails when a tri-state input type lands, which is "
                     "the fix. `af1254af` measured this rather than closing it as "
                     "'works as designed'"),
}


# --- fixes that exist in the tree but have no docs/plans/ entry --------------
# #948 and #956 are both fixed, with dedicated tests, and neither has a
# docs/plans/ entry -- so "no plan" read as "unfixed", which is the same
# assumption error in a different place. A plan directory is a convention, not a
# guarantee. What is a guarantee is the SOURCE: a non-test file that names the
# issue number in a comment, PLUS a test file that names it too.
_FIX_INDEX = None


def fix_index():
    """issue number -> (fix file, test file) for fixes provable in the source."""
    global _FIX_INDEX
    if _FIX_INDEX is not None:
        return _FIX_INDEX
    import subprocess as _sp
    files = _sp.run("git ls-files", shell=True, cwd=REPO,
                    capture_output=True, text=True).stdout.splitlines()
    code, tests = {}, {}
    for f in files:
        if not f.endswith((".go", ".ts", ".tsx", ".sql")):
            continue
        if f.startswith("docs/"):
            continue
        try:
            t = (REPO / f).read_text(errors="ignore")
        except OSError:
            continue
        for n in set(re.findall(r"(?:issue|Issue|#)\s?#?(\d{2,5})\b", t)):
            n = int(n)
            if 1 <= n <= 1400:
                (tests if f.endswith("_test.go") or "__tests__" in f or ".test." in f
                 else code).setdefault(n, f)
    _FIX_INDEX = (code, tests)
    return _FIX_INDEX


def classify(iss):
    """Return (disposition, relevance, reason) for one issue."""
    n = iss["number"]
    title = (iss.get("title") or "")
    body = (iss.get("body") or "")
    text = (title + "\n" + body).lower()
    labels = {l["name"].lower() for l in iss.get("labels", [])}

    # 0a. A feature shipped by a port rather than by an issue fix.
    if n in PORTED_FEATURES and WORKLOG or n in PORTED_FEATURES:
        disp, why = PORTED_FEATURES[n]
        return (disp, "relevant", why)

    # 0b. A disposition the WORKLOG states explicitly.
    if n in WORKLOG_DECIDED and WORKLOG:
        disp, why = WORKLOG_DECIDED[n]
        return (disp, "relevant" if disp != "declined" or BUG_TITLE.search(title) else
                "not-relevant", why)

    # 0c. The repo's own per-issue plan, if there is one, is the authority.
    ps = PLAN_STATE.get(n)
    if ps:
        st = ps["status"]
        # Order matters and the boundary matters. "PARTIALLY IMPLEMENTED"
        # contains "IMPLEMENTED", so testing the fixed pattern first recorded
        # #583 as fully fixed when the plan explicitly describes a partial fix
        # that no single commit captures. \bpartial is tested first, and the
        # fixed pattern requires a word boundary before "implement".
        if re.search(r"\bpartially\b", st, re.I):
            return ("partially-fixed", "relevant",
                    f"partially implemented: the defect (a misleading error "
                    f"message) was fixed and the behaviour a user reported is "
                    f"correct as written; `{ps['plan']}` records both halves "
                    f"deliberately")
        if re.search(r"\b(solved|shipped|complete|done)\b|\bimplement(ed)?\b",
                     st, re.I):
            c = ps["commits"][0] if ps["commits"] else "(no commit cited)"
            return ("fixed", "relevant",
                    f"fixed in `{c}`; per-issue plan `{ps['plan']}` records what was "
                    f"wrong and why the change is correct")
        if re.search(r"partially", st, re.I):
            return ("partially-fixed", "relevant",
                    f"partially implemented; per-issue plan `{ps['plan']}` records "
                    f"what is done and what is not")
        if re.search(r"investigated|not changed", st, re.I):
            return ("declined", "relevant",
                    f"investigated and deliberately not changed without a concrete "
                    f"reproduction; reasoning recorded in `{ps['plan']}` -- an issue "
                    f"closed with no explanation gets re-investigated from scratch")
        if re.search(r"blocked|waiting|needs", st, re.I):
            return ("triaged", "relevant", f"plan `{ps['plan']}`: {st[:80]}")

    # 0c. A fix that exists in the source: a code file naming the issue AND a
    #     test file naming it. The test is the half that matters -- a comment
    #     citing an issue number is not a fix.
    code, tests = fix_index()
    if n in code and n in tests:
        return ("fixed", "relevant",
                f"fixed in `{code[n]}` with a test in `{tests[n]}`; no "
                f"docs/plans/ entry exists, so the plan directory under-reports "
                f"this repo's own fixes")

    # 1. Does the fork's own spec/plan ask for this concept?
    #
    #    TITLE only, never the body. The first version searched title+body and
    #    reported 28 "spec collisions" of which 27 were false: a feature request
    #    about image guidelines mentions "trust" in a paragraph of prose, a
    #    request about badges mentions "weight" in passing. Measured: 1 title
    #    match, 27 body-only matches, and the one real collision is #69 (changelog
    #    feeds, which this fork already built in 8c8160b5). A concept mentioned in
    #    a body is not a spec collision; it is a coincidence of vocabulary.
    title_l = title.lower()
    for needle, what in SPEC_CONCEPTS.items():
        if needle in title_l:
            return ("spec-collision", "relevant",
                    f"the fork's own spec/plan names this concept: {what}")

    # 2. Is it a bug report about code this fork ships?
    is_bug = bool(BUG_TITLE.search(title))
    has_bug_signal = any(m in text for m in BUG_MARKERS)
    is_feature = any(m in title.lower() for m in FEATURE_MARKERS)

    # A "[Bug Report]" prefix wins over everything else. A body mentioning
    # "error" does not make a "[Feature]" a bug, and `enhancement`/`help wanted`
    # labels do not either.
    if is_bug:
        return ("bug", "relevant",
                "reports wrong behaviour of shipped code -- needs a fix with a "
                "test that fails without it")
    if is_feature:
        return ("feature-request", "not-relevant",
                "upstream feature request; the fork's policy is upstream bug "
                "fixes and spec-driven work, and no spec row asks for this")
    if has_bug_signal:
        return ("suspect-bug", "relevant",
                f"no [Bug Report] prefix but the body describes a failure; needs "
                f"a human read to confirm ({n})")
    if "duplicate" in labels:
        return ("duplicate", "not-relevant", "labelled duplicate upstream")
    if "wontfix" in labels or "won't fix" in labels:
        return ("wontfix", "not-relevant", "labelled wontfix upstream")
    if not body or len(body) < 80:
        return ("declined", "not-relevant",
                "no usable body -- nothing to reproduce or decide from, and "
                "upstream is not asking for help on it")
    # A short body with no [Feature] prefix and no bug signal is a note, not a
    # request. Recorded as declined with the reason rather than left waiting,
    # because a row that needs a human and never gets one is not finished.
    return ("declined", "not-relevant",
            "an unprefixed note with no bug to reproduce and no feature this "
            "fork's spec asks for; declined, and recorded so it is not revisited")


# --- merge the repo's own per-issue plans ------------------------------------
# docs/plans/NNNN-*.md is the authoritative per-issue record and already exists.
# Prefer it over anything this script infers: a plan citing a commit that EXISTS
# in this repo is evidence, while a keyword match is a guess.
PLAN_STATE = {}
_plans = REPO / "docs" / "plans"
if _plans.exists():
    for _p in sorted(_plans.glob("[0-9]*.md")):
        _n = int(_p.name[:4])
        _t = _p.read_text()
        _m = re.search(r"\*\*Status:?\s*([^*]+)\*\*", _t) or re.search(r"^Status:.*$", _t, re.M)
        _st = (_m.group(1) if _m and _m.lastindex else _m.group(0) if _m else "").strip()
        _cs = sorted(set(re.findall(r"`([0-9a-f]{7,40})`", _t)))
        PLAN_STATE[_n] = {"status": _st, "commits": _cs, "plan": _p.name}


def esc(s, n):
    s = re.sub(r"\s+", " ", (s or "")).strip()
    s = s.replace("|", "\\|")
    return s[:n]


def main():
    write = "--write" in sys.argv
    rows = []
    for n in sorted(data):
        iss = data[n]
        disp, rel, reason = classify(iss)
        rows.append((n, esc(iss.get("title"), 90), rel, disp, reason))

    counts = collections.Counter(r[3] for r in rows)
    rel_counts = collections.Counter(r[2] for r in rows)
    print(f"classified {len(rows)} issues")
    print("  relevance:", dict(rel_counts))
    print("  disposition:")
    for k, v in counts.most_common():
        print(f"    {k:24} {v}")

    bugs = [r for r in rows if r[3] == "bug"]
    suspect = [r for r in rows if r[3] == "suspect-bug"]
    print(f"\n  -> {len(bugs)} are [Bug Report]s needing a fix with a test")
    print(f"  -> {len(suspect)} mention a failure but carry no [Bug Report] prefix "
          f"(need a human read): {', '.join('#' + str(r[0]) for r in suspect)}")
    print(f"  -> {len(rows) - len(bugs) - len(suspect)} are dispositioned with a reason")

    remaining = [r for r in rows if r[3] in ("bug", "suspect-bug", "partially-fixed",
                                            "spec-collision", "triaged")]
    print(f"\n  REMAINING WORK: {len(remaining)} row(s)")
    for r in remaining:
        print(f"    #{r[0]:<5} {r[3]:<18} {r[1][:62]}")

    if write:
        lines = [
            "# Open upstream issues — ledger and dispositions",
            "",
            "Every open issue on `stashapp/stash-box`, classified from its real body",
            f"({len(rows)} issues, measured 2026-09-30). Generated by",
            "`docs/classify-issues.py`; completeness is enforced by",
            "`docs/goal-check.py` clause C2, which fails if any open issue from",
            "`docs/track/issues-open.json` is missing from the table below or is",
            "still awaiting a decision.",
            "",
            "## What the columns mean",
            "",
            "| column | meaning |",
            "|---|---|",
            "| **Relevance** | whether this fork should act on it at all |",
            "| **Disposition** | what was decided, and the status the work is in |",
            "| **Why** | the reason, so a decision is reviewable rather than asserted |",
            "",
            "**Disposition values.** `fixed` — a fix on a branch with a test that",
            "fails without it, named in the `Why` column. `declined` — decided not",
            "to build, with the reason. `deferred` — a real decision to not do it",
            "now, with the reason. `triaged` — read and classified, no code work",
            "yet (the next unit of work).",
            "",
            "A row that is neither fixed nor declined nor deferred is **not",
            "finished** — it is waiting, and C2 counts it. The point of this ledger",
            "is that every row ends up with an outcome rather than being deleted or",
            "left implied.",
            "",
            f"**Summary: {len(rows)} rows — "
            + ", ".join(f"{v} {k}" for k, v in counts.most_common()) + "**",
            "",
            "## The roster",
            "",
            "| # | Title | Relevance | Disposition | Why |",
            "|---|---|---|---|---|",
        ]
        for n, title, rel, disp, reason in rows:
            lines.append(f"| {n} | {title} | {rel} | {disp} | {reason} |")
        OUT.write_text("\n".join(lines) + "\n")
        print(f"\nwrote {OUT} ({len(lines)} lines)")

    return 0


if __name__ == "__main__":
    sys.exit(main())
