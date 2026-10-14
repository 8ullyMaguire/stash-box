#!/usr/bin/env python3
"""Mutation checks for the saved-search slice (spec section 5, plan phase 7 slice 2b).

Each case breaks ONE behaviour and names the integration test that must go red for it, then
restores the file and confirms the same test goes green again. A mutation that reddens nothing
means the test does not cover the behaviour it claims to; leaving the mutation applied means the
next result is a lie, so the restore is checked too.
"""
import subprocess
import sys
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent

CASES = [
    {
        "name": "the notification cursor stops filtering (created_since dropped)",
        "file": "internal/service/scene/query.go",
        "old": "\tif input.CreatedSince != nil {\n\t\tquery = query.Where(sq.GtOrEq{\"scenes.created_at\": *input.CreatedSince})\n\t}",
        "new": "\tif false && input.CreatedSince != nil {\n\t\tquery = query.Where(sq.GtOrEq{\"scenes.created_at\": *input.CreatedSince})\n\t}",
        "test": "TestSavedSearchNotificationsOnePerRun",
    },
    {
        "name": "results ignores its paging arguments",
        "file": "internal/api/resolver_saved_search.go",
        "old": "\tif page != nil {\n\t\tinput.Page = *page\n\t}",
        "new": "\tif page != nil {\n\t\tinput.Page = 1\n\t}",
        "test": "TestSavedSearchResultsHonourTheirPagingArguments",
    },
    {
        "name": "a subscribed search with no cursor announces its whole history",
        "file": "internal/service/savedsearch/notify.go",
        "old": "\t\tif savedSearch.LastNotifiedAt == nil {",
        "new": "\t\tif false && savedSearch.LastNotifiedAt == nil {",
        "test": "TestSavedSearchNotificationsOnePerRun",
    },
    {
        "name": "delete stops checking ownership and reports success anyway",
        "file": "internal/service/savedsearch/service.go",
        "old": "\t\tif _, err := tx.FindSavedSearchForUser(ctx, queries.FindSavedSearchForUserParams{\n\t\t\tID:     id,\n\t\t\tUserID: userID,\n\t\t}); err != nil {\n\t\t\tif err := errutil.IgnoreNotFound(err); err != nil {\n\t\t\t\treturn err\n\t\t\t}\n\t\t\treturn nil\n\t\t}\n\t\tfound = true",
        "new": "\t\tfound = true",
        "test": "TestAnotherUsersSavedSearchIsNotReachable",
    },
]

ENV_TEST = [
    "go", "test", "-tags", "integration", "-count=1",
]


def run_test(name: str) -> "tuple[bool, str]":
    """(passed, output) for the named test."""
    result = subprocess.run(
        ENV_TEST + ["-run", f"^{name}$", "./internal/api/"],
        cwd=REPO, capture_output=True, text=True,
        env={**__import__("os").environ,
             "POSTGRES_DB": "postgres:smoke_pw@127.0.0.1:55434/stash-box-test?sslmode=disable"},
    )
    return result.returncode == 0, result.stdout + result.stderr


def main() -> int:
    failures = 0
    for case in CASES:
        path = REPO / case["file"]
        original = path.read_text()
        if case["old"] not in original:
            print(f"SKIP (anchor not found): {case['name']} in {case['file']}")
            failures += 1
            continue

        path.write_text(original.replace(case["old"], case["new"], 1))
        try:
            passed_under_mutation, output = run_test(case["test"])
        finally:
            path.write_text(original)

        passed_after_restore, _ = run_test(case["test"])

        if not passed_under_mutation and passed_after_restore:
            print(f"CAUGHT   {case['test']}: {case['name']}")
        else:
            failures += 1
            verdict = "NOT CAUGHT (test stayed green under the mutation)" if passed_under_mutation \
                else "RESTORE FAILED (still red after restoring)"
            print(f"SUSPECT  {case['test']}: {case['name']} -- {verdict}")
            print(output[-2000:])

    print(f"\n{len(CASES) - failures}/{len(CASES)} mutations caught and restored")
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
