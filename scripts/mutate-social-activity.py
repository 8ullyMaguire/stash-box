#!/usr/bin/env python3
"""Mutation checks for social discovery and session state (feature-discovery spec sections 10-11,
aggregator phase 7 slice 7).

Each case breaks ONE behaviour and names the test that must go red for it, then restores the file
and confirms the same test goes green again. A mutation that reddens nothing means the test does
not cover the behaviour it claims to; leaving it applied means the next result is a lie, so the
restore is checked too.

MOST CASES MUTATE THE GENERATED SQL, NOT THE .sql BESIDE IT. sqlc compiles each statement into a
Go string literal at generation time, so editing `internal/queries/sql/*.sql` changes nothing that
runs -- slice 5's script reported NOT CAUGHT for a case whose file was never read. The anchors
below are therefore in `internal/queries/*.sql.go`.

THE TWO SQL CASES THAT MATTER MOST are the two halves of the visibility rule, and they are separate
because a feature can get one right and the other wrong:

  - the grant: who the author published to (per-follower, or everyone), and
  - the default: that an untouched row publishes to nobody.

A mutation that publishes an untouched row is the failure this whole slice exists to make
impossible, so that case is first.

Run with an optional substring to re-run one case, e.g.:

    python3 scripts/mutate-social-activity.py "default"
"""
import os
import subprocess
import sys
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
SERVICE = "internal/service/activity/service.go"
FOLLOW_SERVICE = "internal/service/follow/service.go"
SESSION_SERVICE = "internal/service/sessionstate/service.go"
FOLLOW_SQL = "internal/queries/follow.sql.go"
SESSION_SQL = "internal/queries/session_state.sql.go"
MUTATION = "internal/database/migrations/postgres/116_session_states.up.sql"

CASES = [
    {
        "name": "following someone publishes their whole activity (the default is not private)",
        "file": FOLLOW_SQL,
        "old": 'INSERT INTO user_follows ("follower_id", "followed_id")\nVALUES ($1, $2)',
        "new": ('INSERT INTO user_follows ("follower_id", "followed_id", "activity_visibility")\n'
                "VALUES ($1, $2, 'public')"),
        "test": "TestActivityFeedIsEmptyUntilTheAuthorPublishes",
    },
    {
        "name": "private activity is visible to the follower anyway",
        "file": FOLLOW_SQL,
        "old": "AND uf.\"activity_visibility\" IN ('followers', 'public')",
        "new": "AND uf.\"activity_visibility\" IN ('followers', 'public', 'private')",
        "test": "TestActivityFeedIsEmptyUntilTheAuthorPublishes",
    },
    {
        "name": "a 'followers' grant reaches readers the follow does not name",
        "file": FOLLOW_SQL,
        # The "give me the actors who granted ME something" filter, loosened to "give me everyone
        # who granted ANYONE something". This is the difference between a per-follow grant and a
        # per-user one, and it is the failure that reads as "the feature works" from one account.
        "old": 'WHERE uf."follower_id" = $1',
        "new": 'WHERE $1 IS NOT NULL',
        "test": "TestFollowersVisibilityReachesTheNamedFollowerAndNoOneElse",
    },
    {
        "name": "a public grant is no longer broadcast",
        "file": FOLLOW_SQL,
        "old": 'pub."activity_visibility" = \'public\'',
        "new": 'pub."activity_visibility" = \'private\'',
        "test": "TestPublicGrantReachesReadersWhoFollowNobody",
    },
    {
        "name": "a withdrawn fingerprint counts as having the scene",
        "file": FOLLOW_SQL,
        "old": 'WHERE fp."vote" = 1',
        "new": 'WHERE fp."vote" <> 0',
        "test": "TestFeedReportsEachKindWithItsOwnFields",
    },
    {
        "name": "an unpublished list's name is reported in the feed",
        "file": SERVICE,
        "old": "func HideUnpublishedList(published bool) bool { return !published }",
        "new": "func HideUnpublishedList(published bool) bool { return false }",
        "test": "TestFeedReportsAListNameOnlyForPublishedLists",
    },
    {
        "name": "the feed shows the viewer their own activity",
        "file": SERVICE,
        "old": "\t\tif item.ActorID == viewerID {\n\t\t\tcontinue\n\t\t}\n",
        "new": "",
        "test": "TestFeedExcludesTheViewersOwnActivity",
    },
    {
        "name": "the feed is ordered oldest first",
        "file": SERVICE,
        "old": "return items[i].CreatedAt.After(items[j].CreatedAt)",
        "new": "return items[i].CreatedAt.Before(items[j].CreatedAt)",
        "test": "TestActivityFeedShowsFollowedActorsNewestFirstByTimestamp",
    },
    {
        "name": "the page is not cut to the requested size (the arms decide it)",
        "file": SERVICE,
        "old": "\tif len(items) > limit {\n\t\titems = items[:limit]\n\t}\n",
        "new": "",
        "test": "TestFeedPagesByKeysetCursor",
    },
    {
        "name": "a local save stores the payload it was given",
        "file": SESSION_SERVICE,
        # The service is where the local write's payload is DROPPED; the column guard means a
        # service that kept it still stores NULL, so the observable difference is the validation:
        # a local save must not have its cursor checked, because there is nothing to store.
        "old": "\tpayload := json.RawMessage(nil)\n\tif storage == StorageSynced {",
        "new": "\tpayload := json.RawMessage(nil)\n\tif true {",
        "test": "TestSessionStateRejectsBadInput",
    },
    {
        "name": "the database accepts a local row with a payload",
        "file": MUTATION,
        "old": 'OR ("storage" = \'local\' AND "cursor" IS NULL)',
        "new": 'OR ("storage" = \'local\')',
        "test": "TestTheDatabaseRefusesALocalRowWithAPayload",
    },
]

ENV_TEST = ["go", "test", "-count=1"]
INTEGRATION_ENV = {
    "POSTGRES_DB": "postgres:smoke_pw@127.0.0.1:55434/stash-box-test?sslmode=disable",
}


def run_test(case) -> "tuple[bool, str]":
    """(passed, output) for the case's test, unit or integration."""
    if case.get("unit"):
        command = ENV_TEST + ["-run", f"^{case['test']}$", "./internal/service/activity/"]
        env = dict(os.environ)
    else:
        command = ENV_TEST + ["-tags", "integration", "-run", f"^{case['test']}$", "./internal/api/"]
        # The suite provisions its own schema per run; the DSN is what the harness reads.
        env = {**os.environ, **INTEGRATION_ENV}
    result = subprocess.run(command, cwd=REPO, capture_output=True, text=True, timeout=500, env=env)
    return result.returncode == 0, result.stdout + result.stderr


def main() -> int:
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
