#!/usr/bin/env python3
"""Mutation checks for scene similarity (feature-discovery spec section 8, phase 7 slice 5).

Each case breaks ONE behaviour and names the test that must go red for it, then restores the
file and confirms the same test goes green again. A mutation that reddens nothing means the
test does not cover the behaviour it claims to; leaving the mutation applied means the next
result is a lie, so the restore is checked too.

Two of the seven mutate the CANDIDATE QUERY rather than the Go, because the candidate set is
where a similarity feature is actually decided: a scene nobody selects cannot be scored, and no
amount of Go-side weighting recovers it.

Those two edit `internal/queries/scene_similar.sql.go` -- the GENERATED file -- and not the
`.sql` beside it. sqlc compiles the statement into a Go string literal at generation time, so
editing the `.sql` changes nothing that runs: the first version of this script mutated the
source and the affected case reported NOT CAUGHT while the file it edited was never read. The
generated file is what the process loads; the `.sql` is the source a regeneration would restore
it from, which is why the mutation is restored immediately afterwards.

The vector-bonus case is the one that already paid for itself. `score` is the sum of the
evidence, and the embedding similarity REORDERS without scoring -- that is what the schema
promises a client -- but the first implementation added the bonus into `score`, and only the
integration test noticed, reading 1.307 where the evidence said 0.6.
"""
import os
import subprocess
import sys
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
SCENE = "internal/service/similar/scene.go"
EXPLAIN = "internal/service/similar/explain.go"
SQL_GENERATED = "internal/queries/scene_similar.sql.go"

CASES = [
    {
        "name": "a shared performer is worth less than a tag (the weight order inverts)",
        "file": EXPLAIN,
        "old": "\tweightSharedPerformer = 5.0",
        "new": "\tweightSharedPerformer = 1.0",
        "test": "TestSimilarScenesNamesItsEvidenceAndRanks",
    },
    {
        "name": "the duration term decays over half the distance",
        "file": EXPLAIN,
        "old": "\tdurationScaleSeconds = 1800",
        "new": "\tdurationScaleSeconds = 900",
        "test": "TestSimilarScenesCountsTheStudioAndTheRuntime",
    },
    {
        "name": "the same-studio evidence is never turned into a reason",
        "file": SCENE,
        "old": """		ev.sameStudio = input.Subject.StudioID.Valid && sc.StudioID.Valid &&
			input.Subject.StudioID.UUID == sc.StudioID.UUID""",
        "new": "\t\tev.sameStudio = false",
        "test": "TestSimilarScenesCountsTheStudioAndTheRuntime",
    },
    {
        "name": "the vector bonus is added to the reported score instead of the ranking",
        "file": SCENE,
        "old": "\t\t\trec.rankingValue = scoreValue + vectorWeight*cosine",
        "new": "\t\t\trec.Score = scoreValue + vectorWeight*cosine",
        "test": "TestSimilarScenesAreReorderedByTheConfiguredVectorStore",
    },
    {
        "name": "the two ontology directions are swapped (an instance reads as a relative)",
        "file": EXPLAIN,
        "old": """		case te.direction == "down":""",
        "new": """		case te.direction == "up":""",
        "test": "TestSimilarScenesReadsTheTagOntologyBothWays",
    },
    {
        "name": "the candidate predicate stops walking the ontology towards the general tags",
        "file": SQL_GENERATED,
        "old": """        EXISTS (SELECT 1 FROM scene_tags cst WHERE cst.scene_id = c.id AND cst.tag_id IN (SELECT tag_id FROM reach_down))
     OR EXISTS (SELECT 1 FROM scene_tags cst WHERE cst.scene_id = c.id AND cst.tag_id IN (SELECT tag_id FROM reach_up))""",
        "new": """        EXISTS (SELECT 1 FROM scene_tags cst WHERE cst.scene_id = c.id AND cst.tag_id IN (SELECT tag_id FROM reach_down))""",
        "test": "TestSimilarScenesReadsTheTagOntologyBothWays",
    },
    {
        "name": "the candidate predicate ignores shared performers",
        "file": SQL_GENERATED,
        "old": """     OR EXISTS (SELECT 1 FROM scene_performers csp WHERE csp.scene_id = c.id AND csp.performer_id IN (SELECT performer_id FROM subject_performers))
""",
        "new": "",
        "test": "TestSimilarScenesNamesItsEvidenceAndRanks",
    },
]

ENV_TEST = ["go", "test", "-tags", "integration", "-count=1"]


def run_test(name: str) -> "tuple[bool, str]":
    """(passed, output) for the named test."""
    result = subprocess.run(
        ENV_TEST + ["-run", f"^{name}$", "./internal/api/"],
        cwd=REPO, capture_output=True, text=True, timeout=300,
        env={**os.environ,
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
