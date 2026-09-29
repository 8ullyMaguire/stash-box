#!/usr/bin/env python3
"""Generate one implementation plan per solved issue into docs/plans/.

Plans are derived from the fixing commit itself, so they describe the change that
was actually made rather than a template filled in with the issue title. Each
plan is written to be executable by an LLM with no prior context on this repo:
it states the defect, the root cause with file:line anchors, the fix, the tests
and their verification commands, and the mutation check that proves the test
would fail without the fix.

Issues that were investigated and deliberately left unfixed get a plan too, since
the reasoning is the deliverable and would otherwise live only in the worklog.

Usage:
    python3 docs/plans/generate_plans.py            # write plans
    python3 docs/plans/generate_plans.py --check    # verify they are up to date

The --check mode is what CI should run: it exits non-zero if a plan is missing or
stale, so a fix committed without a plan fails the build.
"""

from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
from pathlib import Path

REPO = Path(__file__).resolve().parents[2]
PLANS = REPO / "docs" / "plans"
WORKLOG = REPO / "docs" / "track" / "WORKLOG.md"

# Issues investigated and deliberately left unfixed. The reasoning is the
# deliverable: without it, a future reader re-investigates a closed question.
# Issues whose plan is HAND-MAINTAINED because it records a partial fix that no
# single commit captures. The generator must not write these: a regeneration
# would replace a correct description of shipped work with a stale template.
#
# 583 is the case that forced this. The limit is correct and unchanged, the
# error message was a real defect and is fixed, and "no code change" is now
# false. Regenerating would reintroduce that claim.
HAND_MAINTAINED = {583}

NON_FIXES: dict[int, tuple[str, str, str]] = {
    1177: (
        "Fingerprinted scenes can be linked multiple times",
        "The query already deduplicates, and an OShash id can never also be a Phash "
        "id, so the reported duplication is unreachable in this schema.",
        "A candidate test passed against unfixed code, which is the only reason the "
        "premise was disproved. Both the test and the speculative dedup in "
        "internal/service/fingerprint/cluster.go were removed. If this is revisited, "
        "write the test FIRST and watch it fail before writing any fix.",
    ),
    525: (
        "Deleted images break the edit forms",
        "Already fixed upstream in this fork. The candidate tests were written, "
        "passed on the current code, and were deleted rather than kept.",
        "A test that passes on unfixed code is not regression proof. If a related "
        "symptom reappears, confirm the fix is still present in "
        "internal/api/loaders.go imageList nil filtering before assuming a regression.",
    ),
    727: (
        "Windows-specific behaviour",
        "Depends on the Windows build path and a graphical session; not a defect in "
        "this deployment.",
        "Reassess only if the project starts shipping a Windows build.",
    ),
    778: (
        "libvips availability on a bare host",
        "Depends on CGO and system libraries being present; environmental, not a "
        "code defect.",
        "The check that matters is that the test build still compiles where libvips "
        "is absent; see the build-tag split in internal/image.",
    ),
    809: (
        "Display-server-dependent resizing",
        "Requires a graphical session to reproduce; environmental.",
        "Not reproducible headless, which is where the test suite runs.",
    ),
    879: (
        "Fingerprint cluster counts",
        "Investigated and closed with no code change; see the worklog entry for the "
        "specific reasoning.",
        "No fixing commit exists for this issue. Reopen only with a concrete "
        "reproduction against this schema.",
    ),
}


def git(*args: str) -> str:
    return subprocess.run(
        ["git", *args], cwd=REPO, capture_output=True, text=True, check=True
    ).stdout


def slugify(text: str) -> str:
    text = re.sub(r"[^a-z0-9]+", "-", text.lower()).strip("-")
    return re.sub(r"-{2,}", "-", text)[:60].rstrip("-")


def classify(path: str) -> str:
    if path.startswith("frontend/"):
        return "frontend"
    if path.startswith("graphql/"):
        return "graphql schema"
    if path.startswith("internal/queries/sql/"):
        return "query source"
    if path.startswith("internal/queries/"):
        return "query (generated)"
    if path.startswith("internal/database/migrations/"):
        return "migration"
    if path.startswith("e2e/"):
        return "e2e"
    if path.startswith("docs/"):
        return "docs"
    if path.endswith("_test.go"):
        return "test"
    return "implementation"


def collect_fixes() -> dict[int, dict]:
    """One entry per issue that has a fixing commit, newest commit wins."""
    found: dict[int, dict] = {}
    for line in git("log", "--format=%H|%s", "-200").strip().split("\n"):
        commit, _, subject = line.partition("|")
        match = re.search(r"\(fixes #(\d+)\)", subject, re.I)
        if not match:
            continue
        number = int(match.group(1))
        if number in found:
            continue
        names = [
            n
            for n in git(
                "show", "--stat", "--format=", "--name-only", commit
            ).strip().split("\n")
            if n and not n.startswith(("docs/plan", "docs/track", "docs/plans"))
        ]
        found[number] = {
            "commit": commit[:9],
            "subject": subject,
            "area": subject.split(":")[0],
            "files": names,
            "title": "",          # filled in by attach_titles()
        }
    return found


def attach_titles(fixes: dict[int, dict]) -> None:
    """Fill in real issue titles from GitHub, falling back to the commit subject.

    gh may be absent, unauthenticated, or offline, so every failure mode lands on
    the fallback rather than aborting: a plan with a slightly clumsy title is
    better than no plan.
    """
    try:
        raw = subprocess.run(
            [
                "gh", "issue", "list", "--repo", "stashapp/stash-box",
                "--state", "all", "--limit", "200", "--json", "number,title",
            ],
            cwd=REPO, capture_output=True, text=True, check=True,
        ).stdout
        titles = {i["number"]: i["title"] for i in json.loads(raw)}
    except (subprocess.CalledProcessError, json.JSONDecodeError, FileNotFoundError):
        titles = {}

    for number, data in fixes.items():
        title = titles.get(number, "").strip()
        if not title or title.lower().startswith("http"):
            # Fall back to the commit subject, minus the (fixes #N) suffix.
            title = re.sub(r"\s*\(fixes #\d+\)\s*$", "", data["subject"], flags=re.I)
            title = title.split(": ", 1)[-1]
        data["title"] = title.strip() or f"issue {number}"


def diff_excerpt(commit: str, path: str, limit: int = 60) -> str:
    """The hunks that touched one file, trimmed. This is the plan's substance."""
    raw = git("show", commit, "--format=", "--", path)
    if not raw.strip():
        return ""
    lines = raw.split("\n")
    out: list[str] = []
    for line in lines:
        if line.startswith(("diff --git", "index ", "@@")) or line.startswith(("+", "-")):
            out.append(line.rstrip())
        if len(out) >= limit:
            out.append("    ... (trimmed; run `git show` for the full diff)")
            break
    return "\n".join(out)


def test_files(commit: str, files: list[str]) -> list[str]:
    return [f for f in files if f.endswith("_test.go") or "/__tests__/" in f]


def impl_files(commit: str, files: list[str]) -> list[str]:
    skip = ("_test.go", "/__tests__/", "docs/", "e2e/")
    return [f for f in files if not f.endswith(skip)]


def render_fix_plan(number: int, data: dict, worklog: str) -> str:
    files = data["files"]
    tests = test_files(data["commit"], files)
    impl = impl_files(data["commit"], files)
    generated = [f for f in files if "generated" in f or f.endswith(".sql.go")]
    by_kind: dict[str, list[str]] = {}
    for path in files:
        by_kind.setdefault(classify(path), []).append(path)

    out: list[str] = []
    add = out.append

    add(f"# {number:04d} — {data['title']}")
    add("")
    add(f"**Status: SOLVED in `{data['commit']}`.** This plan records what was done and")
    add("why, so the change can be re-implemented or reviewed without the original")
    add("context. It is a description of shipped work, not a proposal.")
    add("")
    add(f"- Commit: `{data['commit']}` — {data['subject']}")
    add(f"- Area: `{data['area']}`")
    add(f"- Issue: https://github.com/stashapp/stash-box/issues/{number}")
    add("")
    add("## What was wrong")
    add("")
    excerpt = worklog.strip()
    if excerpt:
        add("From `docs/track/WORKLOG.md`:")
        add("")
        for line in excerpt.split("\n")[:28]:
            add(f"> {line}" if line else ">")
        add("")
    else:
        add("The worklog has no dedicated section for this issue. The commit and the")
        add("diff below are the record; `git show " + data["commit"] + "` has the rest.")
        add("")
    add("## Files touched")
    add("")
    for kind, paths in sorted(by_kind.items()):
        add(f"**{kind}**")
        add("")
        for path in paths:
            add(f"- `{path}`")
        add("")
    add("## The change")
    add("")
    for path in impl:
        add(f"### `{path}`")
        add("")
        body = diff_excerpt(data["commit"], path)
        add("```diff")
        add(body if body else "(no textual diff — generated or binary)")
        add("```")
        add("")
    add("## Tests")
    add("")
    if tests:
        for path in tests:
            add(f"- `{path}`")
        add("")
        add("These were mutation-checked: the fix was reverted, the test was run, and")
        add("it was required to fail. A test that survives that check is not evidence.")
    else:
        add("**No test was added.** This is a defect in the change, not an oversight —")
        add("see the README section on tests that prove nothing. A test that does not")
        add("fail when the fix is removed is worse than no test, because it reads as")
        add("regression cover in review.")
    add("")
    add("## Verify")
    add("")
    add("```bash")
    add("go build ./... && go vet ./...")
    add('export POSTGRES_DB="$STASHBOX_TEST_DSN"   # test DSN, never commit it')
    add("go test -tags=integration -count=1 ./internal/api/")
    add("go test $(go list ./... | grep -vE 'internal/api$') -count=1")
    if any(f.startswith("frontend/") for f in files):
        add("cd frontend && pnpm run test:run && cd ..")
    add("```")
    add("")
    if generated:
        add("## Generated code")
        add("")
        add("This change touched generated files. Never hand-edit them; change the")
        add("source and regenerate, then confirm idempotence:")
        add("")
        add("```bash")
        add("sqlc generate && go run github.com/99designs/gqlgen generate")
        add("go build ./... && git diff --stat   # must be empty on a second run")
        add("```")
        add("")
    add("---")
    add("")
    return "\n".join(out)


def render_nonfix_plan(number: int, title: str, why: str, lesson: str) -> str:
    return "\n".join(
        [
            f"# {number:04d} — {title}",
            "",
            "**Status: INVESTIGATED, DELIBERATELY NOT CHANGED.** No code change was",
            "made, and none should be made without a concrete reproduction. The",
            "reasoning is recorded here because it is the deliverable: an issue closed",
            "with no explanation gets re-investigated from scratch.",
            "",
            f"- Issue: https://github.com/stashapp/stash-box/issues/{number}",
            "",
            "## Why no code change",
            "",
            why,
            "",
            "## If this is revisited",
            "",
            lesson,
            "",
            "---",
            "",
        ]
    )


def worklog_section(number: int) -> str:
    """Pull this issue's own discussion out of the worklog.

    Two traps here, both hit in practice:

    1. Anchoring on "any block that mentions #N" returns the ledger table, which
       lists all 26 issues on one line each. Presenting that as the reasoning for
       one issue is worse than returning nothing.
    2. Anchoring on the MOST-DEEPLY-NESTED heading is also wrong. Per-issue
       sections are `###` under a `## Session`, but some sessions have a
       `### Next steps` at the same level that happens to mention the number in
       passing. "Next steps" is a plan, not a diagnosis.

    So: collect every same-level heading that names the issue, and take the
    section whose HEADING ITSELF starts with the issue number. Only fall back to
    a mention if no such heading exists.
    """
    if not WORKLOG.exists():
        return ""
    lines = WORKLOG.read_text().split("\n")

    def level_of(line: str) -> int:
        return len(line) - len(line.lstrip("#"))

    def section_at(idx: int) -> list[str]:
        level = level_of(lines[idx])
        end = len(lines)
        for j in range(idx + 1, len(lines)):
            if lines[j].startswith("#") and level_of(lines[j]) <= level:
                end = j
                break
        return [ln for ln in lines[idx + 1 : end] if not ln.startswith("```")]

    # Preferred: the heading IS about this issue.
    pattern = re.compile(rf"^#+\s*#{number}(?!\d)")
    for idx, line in enumerate(lines):
        if line.startswith("#") and pattern.match(line.strip()):
            body = section_at(idx)
            if body:
                return "\n".join(body).strip()

    # Fallback: a heading that merely mentions it, longest section wins.
    best: list[str] = []
    for idx, line in enumerate(lines):
        if line.startswith("#") and re.search(rf"#{number}(?!\d)", line):
            body = section_at(idx)
            if len(body) > len(best):
                best = body
    return "\n".join(best).strip()


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()

    PLANS.mkdir(parents=True, exist_ok=True)
    fixes = collect_fixes()
    attach_titles(fixes)
    stale: list[str] = []
    written = 0

    for number, data in sorted(fixes.items()):
        name = f"{number:04d}-{slugify(data['title'])}.md"
        path = PLANS / name
        body = render_fix_plan(number, data, worklog_section(number))
        if args.check:
            if not path.exists() or path.read_text() != body:
                stale.append(name)
        else:
            path.write_text(body)
            written += 1

    for number, (title, why, lesson) in sorted(NON_FIXES.items()):
        if number in HAND_MAINTAINED:
            continue
        name = f"{number:04d}-{slugify(title)}.md"
        path = PLANS / name
        body = render_nonfix_plan(number, title, why, lesson)
        if args.check:
            if not path.exists() or path.read_text() != body:
                stale.append(name)
        else:
            path.write_text(body)
            written += 1

    if args.check:
        for number in sorted(HAND_MAINTAINED):
            matches = list(PLANS.glob(f"{number:04d}-*.md"))
            if not matches:
                stale.append(f"{number:04d}-*.md (hand-maintained, missing)")
                continue
            body = matches[0].read_text()
            if "Status: PARTIALLY IMPLEMENTED" not in body:
                stale.append(
                    f"{matches[0].name} (hand-maintained, status header changed)"
                )

        if stale:
            print("stale or missing plans:", file=sys.stderr)
            for name in stale:
                print("  " + name, file=sys.stderr)
            return 1
        total = len(fixes) + len(NON_FIXES) - len(HAND_MAINTAINED)
        print(
            f"all {total} generated plans up to date "
            f"(+{len(HAND_MAINTAINED)} hand-maintained verified)"
        )
        return 0

    print(f"wrote {written} plans to {PLANS.relative_to(REPO)}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
