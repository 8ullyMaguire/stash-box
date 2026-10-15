#!/usr/bin/env python3
"""Mutation checks for the takedown path (migration 134, plan phase 20's guardrails).

Each case breaks ONE behaviour and names the integration test that must go red for it, then
restores the file and confirms the same test goes green again. A mutation that reddens nothing
means the test does not cover the behaviour it claims to; leaving the mutation applied means the
next result is a lie, so the restore is checked too.

Four of the cases mutate `internal/queries/sql/*.sql`, which is not what runs: sqlc generates Go
from those files, so a case with `regen` runs `sqlc generate` after the mutation AND after the
restore. Skipping that step would make every SQL case report "NOT CAUGHT" for a reason that has
nothing to do with the tests.
"""
import os
import subprocess
import sys
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent

CASES = [
    {
        "name": "the pipeline stops consulting the block list",
        "file": "internal/service/ingest/pipeline.go",
        "old": "\t} else if blocked {",
        "new": "\t} else if false && blocked {",
        "test": "TestTakedownOfAnUnrecordedURLRefusesTheSubmissionBeforeTheFetch",
    },
    {
        "name": "the pipeline stops deriving the host, so a host block refuses nothing",
        "file": "internal/service/ingest/pipeline.go",
        "old": '\tif host := urlkey.SiteHost(rawURL); host != "" {',
        "new": '\tif host := ""; host != "" {',
        "test": "TestBlockingAHostTakesDownEverySourceOnItAndRefusesNewOnes",
    },
    {
        "name": "the URL takedown stops matching the mirrored (keyless) record",
        "file": "internal/queries/sql/content_source.sql",
        "old": (
            "SET status = 'dmca'\nWHERE status <> 'dmca'\n"
            "  AND (url_normalized = @url_normalized OR LOWER(url) = LOWER(@url_normalized))"
        ),
        "new": (
            "SET status = 'dmca'\nWHERE status <> 'dmca'\n"
            "  AND (url_normalized = @url_normalized)"
        ),
        "regen": True,
        "test": "TestTakedownContentURLTakesDownEveryRecordOfTheURLAndBlocksIt",
    },
    {
        "name": "the takedown stops being idempotent, so a second notice reports a second removal",
        "file": "internal/queries/sql/content_source.sql",
        "old": (
            "WHERE status <> 'dmca'\n"
            "  AND (url_normalized = @url_normalized OR LOWER(url) = LOWER(@url_normalized))"
        ),
        "new": (
            "WHERE TRUE\n"
            "  AND (url_normalized = @url_normalized OR LOWER(url) = LOWER(@url_normalized))"
        ),
        "regen": True,
        "test": "TestTakedownContentURLTakesDownEveryRecordOfTheURLAndBlocksIt",
    },
    {
        "name": "the host pattern stops allowing the www form the raw column holds",
        "file": "internal/service/content/takedown.go",
        "old": "\treturn `^[a-z][a-z0-9+.-]*://([^/?#@]*@)?(www\\.)?` + regexp.QuoteMeta(host)",
        "new": "\treturn `^[a-z][a-z0-9+.-]*://([^/?#@]*@)?` + regexp.QuoteMeta(host)",
        "test": "TestBlockingAHostTakesDownEverySourceOnItAndRefusesNewOnes",
    },
    {
        "name": "the host match becomes case-sensitive, so a block fails open on a written host",
        "file": "internal/queries/sql/content_source.sql",
        "old": "  AND url ~* @host_pattern",
        "new": "  AND url ~ @host_pattern",
        "regen": True,
        "test": "TestBlockingAHostTakesDownEverySourceOnItAndRefusesNewOnes",
    },
    {
        "name": "an already-blocked key is reported as newly blocked",
        "file": "internal/service/content/takedown.go",
        "old": "\tif err == nil {\n\t\treturn existing, true, nil\n\t}",
        "new": "\tif err == nil {\n\t\treturn existing, false, nil\n\t}",
        "test": "TestTakedownContentURLTakesDownEveryRecordOfTheURLAndBlocksIt",
    },
    {
        "name": "the decision stops being recorded in the audit trail",
        "file": "internal/service/content/takedown.go",
        "old": "func createSourceAudit(ctx context.Context, tx *queries.Queries, actor uuid.UUID, action queries.ModAuditAction, block queries.ContentSourceBlock, data sourceDecisionAudit) error {",
        "new": "func createSourceAudit(ctx context.Context, tx *queries.Queries, actor uuid.UUID, action queries.ModAuditAction, block queries.ContentSourceBlock, data sourceDecisionAudit) error {\n\tif true {\n\t\treturn nil\n\t}",
        "test": "TestTakedownContentURLTakesDownEveryRecordOfTheURLAndBlocksIt",
    },
]

TEST_ARGS = ["go", "test", "-tags", "integration", "-count=1", "-p", "1"]


def env() -> dict:
    return {
        **os.environ,
        "POSTGRES_DB": "postgres:smoke_pw@127.0.0.1:55434/stash-box-test?sslmode=disable",
    }


def run_test(name: str) -> tuple:
    result = subprocess.run(
        TEST_ARGS + ["-run", f"^{name}$", "./internal/api/"],
        cwd=REPO, capture_output=True, text=True, env=env(),
    )
    return result.returncode == 0, result.stdout + result.stderr


def regenerate() -> None:
    subprocess.run(["sqlc", "generate"], cwd=REPO, capture_output=True, text=True, check=True)


def main() -> int:
    failures = 0
    for case in CASES:
        path = REPO / case["file"]
        original = path.read_text()
        if case["old"] not in original:
            print(f"SKIP (anchor not found): {case['name']} in {case['file']}")
            failures += 1
            continue

        generated = sorted(REPO.glob("internal/queries/*.sql.go"))
        generated_before = {p: p.read_text() for p in generated}

        try:
            path.write_text(original.replace(case["old"], case["new"], 1))
            if case.get("regen"):
                regenerate()
            passed_under_mutation, output = run_test(case["test"])
        finally:
            path.write_text(original)
            if case.get("regen"):
                regenerate()
            # The generated files are restored from their own before-image rather than by a second
            # regeneration: a regeneration that silently produced something different would leave
            # the tree dirty for the NEXT case, and the reports would be about the wrong code.
            for p, text in generated_before.items():
                if p.read_text() != text:
                    p.write_text(text)
                    print(f"  (restored generated file {p.relative_to(REPO)})")

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
