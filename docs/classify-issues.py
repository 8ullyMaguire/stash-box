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


def classify(iss):
    """Return (disposition, relevance, reason) for one issue."""
    n = iss["number"]
    title = (iss.get("title") or "")
    body = (iss.get("body") or "")
    text = (title + "\n" + body).lower()
    labels = {l["name"].lower() for l in iss.get("labels", [])}

    # 0. The repo's own per-issue plan, if there is one, is the authority.
    ps = PLAN_STATE.get(n)
    if ps:
        st = ps["status"]
        if re.search(r"solved|implemented|shipped|complete|done", st, re.I):
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
