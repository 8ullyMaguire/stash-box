#!/usr/bin/env python3
"""Mutation checks for the For You feed (feature-discovery spec section 9, phase 7 slice 6).

Each case breaks ONE behaviour and names the test that must go red for it, then restores the
file and confirms the same test goes green again. A mutation that reddens nothing means the test
does not cover the behaviour it claims to; leaving the mutation applied means the next result is
a lie, so the restore is checked too.

EIGHT OF THE ELEVEN MUTATE THE SQL, and they edit `internal/queries/taste.sql.go` -- the
GENERATED file -- not the `.sql` beside it. sqlc compiles each statement into a Go string literal
at generation time, so editing the `.sql` changes nothing that runs. (Slice 5's script learned
this the expensive way: the case reported NOT CAUGHT while the file it edited was never read.)
The anchors name the `ScoreScenesByAffinity` copies of the weights -- `AS affinity` -- and not
the `FindViewerTagAffinity` copies, because the feed scores candidates with the former and a
mutation of the latter would redden nothing while looking like a caught case.

THE WEIGHT CASES ARE NOT BOOKKEEPING. Each of the three signal weights is a claim about how much
a kind of signal is worth (owning > filing > opining), and each is separately checkable; a
mutation that loosens one is the failure mode of a feature that looks alive but has no opinion.
"""
import os
import subprocess
import sys
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
SERVICE = "internal/service/taste/service.go"
SQL_GENERATED = "internal/queries/taste.sql.go"

# The whole review arm, so the status and rating mutations anchor uniquely: the same text appears
# in FindViewerTagAffinity, and mutating that copy would change nothing any test reads.
REVIEW_ARM = """    SELECT st.tag_id, ((r.rating - 3) / 4.0)::float8 AS affinity
    FROM reviews r
    JOIN scene_tags st ON st.scene_id = r.entity_id
    WHERE r.author_id = $1::uuid
      AND r.entity_type = 'scene'
      AND r.status = 'published'
      AND r.rating IS NOT NULL"""

CASES = [
    {
        "name": "a fingerprint vote is worth half of what the spec says",
        "file": SQL_GENERATED,
        "old": "    SELECT st.tag_id, (1.0 * sf.vote)::float8 AS affinity",
        "new": "    SELECT st.tag_id, (0.5 * sf.vote)::float8 AS affinity",
        "test": "TestForYouWeighsEachKindOfSignalAndIgnoresTheRest",
    },
    {
        "name": "filing a scene outranks owning it (the weight order inverts)",
        "file": SQL_GENERATED,
        "old": "    SELECT st.tag_id, 0.8::float8 AS affinity",
        "new": "    SELECT st.tag_id, 1.5::float8 AS affinity",
        "test": "TestForYouWeighsEachKindOfSignalAndIgnoresTheRest",
    },
    {
        "name": "a review is worth as much as owning the scene",
        "file": SQL_GENERATED,
        "old": "    SELECT st.tag_id, ((r.rating - 3) / 4.0)::float8 AS affinity",
        "new": "    SELECT st.tag_id, ((r.rating - 3) / 2.0)::float8 AS affinity",
        "test": "TestForYouWeighsEachKindOfSignalAndIgnoresTheRest",
    },
    {
        "name": "a REMOVED review shapes the feed",
        "file": SQL_GENERATED,
        "old": REVIEW_ARM,
        "new": REVIEW_ARM.replace("      AND r.status = 'published'\n", ""),
        "test": "TestForYouWeighsEachKindOfSignalAndIgnoresTheRest",
    },
    {
        "name": "an UNRATED review is given an invented rating",
        "file": SQL_GENERATED,
        # The invented rating has to be a NON-NEUTRAL one: the first version of this case
        # coalesced the NULL to 3, which is (3 - 3) / 4 = 0 -- identical to contributing
        # nothing -- so the case was not caught and the TEST was not the problem. A mutation
        # that reproduces the defect's EFFECT has to pick a value the defect would show.
        "old": REVIEW_ARM,
        "new": REVIEW_ARM.replace("      AND r.rating IS NOT NULL", "").replace(
            "((r.rating - 3) / 4.0)", "((COALESCE(r.rating, 5) - 3) / 4.0)"),
        "test": "TestForYouWeighsEachKindOfSignalAndIgnoresTheRest",
    },
    {
        "name": "the hierarchy propagation reaches the parent at FULL weight",
        "file": SERVICE,
        "old": "\tpropagationFactor = 0.4",
        "new": "\tpropagationFactor = 1.0",
        "test": "TestForYouPropagatesToTheParentAtAFractionOfTheDirectWeight",
    },
    {
        "name": "the feed offers back the scenes the viewer already owns",
        "file": SQL_GENERATED,
        "old": "    SELECT sf.scene_id FROM scene_fingerprints sf WHERE sf.user_id = $1::uuid\n",
        "new": "",
        "test": "TestForYouNeverOffersBackWhatTheViewerAlreadyEngagedWith",
    },
    {
        "name": "the feed offers back the scenes the viewer already reviewed",
        "file": SQL_GENERATED,
        "old": "    SELECT r.entity_id AS scene_id\n",
        "new": "",
        "test": "TestForYouNeverOffersBackWhatTheViewerAlreadyEngagedWith",
    },
    {
        "name": "every tag a scene carries becomes a contribution, affinity or not",
        "file": SQL_GENERATED,
        "old": "WHERE s.weight <> 0",
        "new": "WHERE s.weight <> -99999",
        "test": "TestForYouRanksByTheViewersSignalsAndNamesThem",
    },
    {
        "name": "serendipity does nothing at all",
        "file": SERVICE,
        "old": "\t\tstride = int(math.Round(1 / serendipity))",
        "new": "\t\tstride = 0",
        "test": "TestForYouSerendipityZeroIsTheAffinityOrderAndOneIsNot",
    },
    {
        "name": "the stride rule is off by one slot (the mix drifts)",
        "file": SERVICE,
        "old": "\t\tif stride > 0 && (len(out)+1)%stride == 0 && oi < len(shuffled) {",
        "new": "\t\tif stride > 0 && len(out)%stride == 0 && oi < len(shuffled) {",
        "test": "TestSerendipitySetsTheStride",
        "unit": True,
    },
]

ENV_TEST = ["go", "test", "-count=1"]
INTEGRATION_ENV = {
    "POSTGRES_DB": "postgres:smoke_pw@127.0.0.1:55434/stash-box-test?sslmode=disable",
}


def run_test(case) -> "tuple[bool, str]":
    """(passed, output) for the case's test, integration or unit."""
    if case.get("unit"):
        command = ENV_TEST + ["-run", f"^{case['test']}$", "./internal/service/taste/"]
        env = dict(os.environ)
    else:
        command = ENV_TEST + ["-tags", "integration", "-run", f"^{case['test']}$", "./internal/api/"]
        # The suite provisions its own container; the DSN is what the harness reads.
        env = {**os.environ, **INTEGRATION_ENV}
    result = subprocess.run(command, cwd=REPO, capture_output=True, text=True, timeout=400, env=env)
    return result.returncode == 0, result.stdout + result.stderr


def main() -> int:
    # An optional substring selects cases, so a fixed case can be re-run without re-running the
    # eleven (each is two test runs, so the full sweep is minutes).
    selector = sys.argv[1] if len(sys.argv) > 1 else ""
    cases = [c for c in CASES if not selector or selector.lower() in c["name"].lower()]
    if not cases:
        print(f"no case matches {selector!r}")
        return 1

    failures = 0
    for case in cases:
        path = REPO / case["file"]
        original = path.read_text()
        if case["old"] not in original:
            print(f"SKIP (anchor not found): {case['name']} in {case['file']}")
            failures += 1
            continue

        path.write_text(original.replace(case["old"], case["new"], 1))
        try:
            passed_under_mutation, output = run_test(case)
        finally:
            path.write_text(original)

        passed_after_restore, _ = run_test(case)

        if not passed_under_mutation and passed_after_restore:
            print(f"CAUGHT   {case['test']}: {case['name']}")
        else:
            failures += 1
            verdict = "NOT CAUGHT (test stayed green under the mutation)" if passed_under_mutation \
                else "RESTORE FAILED (still red after restoring)"
            print(f"SUSPECT  {case['test']}: {case['name']} -- {verdict}")
            print(output[-2000:])

    print(f"\n{len(cases) - failures}/{len(cases)} mutations caught and restored")
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
