#!/usr/bin/env python3
"""Mutation battery for seed cohorts and campaigns (plan phase 11 slice 11d).

Each probe breaks ONE rule the slice claims and requires the test named for it to go red, with its
neighbour staying green: who the run pays, what it counts, what keeps two campaigns' payments apart,
which kind the head start arrives as, and the roster's own arithmetic.

THE ONE RULE DELIBERATELY NOT PROBED HERE, so that its absence is not read as an oversight: "a campaign's
head start must be positive" lives in TWO places -- a CHECK in migration 139 and a guard in the service --
and the database's is the rule while the service's is only the message. Neutering the guard alone leaves
the CHECK refusing and the test green, which is the correct behaviour of a two-place rule rather than a
survivor; a verdict would need both places broken at once, and a battery that mutated a migration would be
testing the test database's setup rather than the code. The integration suite asserts the error either
way (TestACampaignHasToBeWorthSomething).

Run:  python3 scripts/mutate-seed-campaign.py [M1 M2 ...]
Needs POSTGRES_DB exported (the integration suite's DSN).
"""

import os
import subprocess
import sys

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DSN = os.environ.get(
    "POSTGRES_DB",
    "postgres:smoke_pw@127.0.0.1:55434/stash-box-test?sslmode=disable",
)

CAMPAIGN = "internal/service/seed/campaign.go"
SERVICE = "internal/service/seed/service.go"
TRUST = "internal/service/trust/service.go"


def suite(*names):
    return ["go", "test", "-tags=integration", "-count=1", "-p", "1", "-run",
            "|".join(names), "-v", "./internal/service/seed/"]


MUTATIONS = [
    (
        "M1 the run pays the campaign's creator instead of its cohort",
        CAMPAIGN,
        "\tmemberIDs, err := s.queries.ListSeedCohortMemberIDs(ctx, campaign.CohortID)",
        "\tmemberIDs, err := []uuid.UUID{campaign.CreatedBy.UUID}, error(nil)",
        [suite("TestTheCampaignPaysItsMembersAndNobodyElse")],
        "'the operator was not paid' -- the assertion the fixture exists to make possible",
    ),
    (
        "M2 an already-paid member is reported as paid",
        CAMPAIGN,
        "\t\tif inserted {\n\t\t\tresult.Paid++\n\t\t} else {\n\t\t\tresult.Skipped++\n\t\t}",
        "\t\t_ = inserted\n\t\tresult.Paid++",
        [suite("TestRunningTheCampaignTwicePaysTheCohortOnce", "TestAMemberAddedLaterIsPaidByTheNextRun")],
        "the second run's paid count, and the one that pays only the late member",
    ),
    (
        # The id in entity_id is the ONLY thing separating one campaign's payment from another's: with
        # it gone both campaigns contend for one dedup slot and the second payment is silently dropped.
        "M3 the dedup key loses the campaign's id",
        CAMPAIGN,
        "\t\t\tEntityID:   &campaign.ID,",
        "\t\t\tEntityID:   nil,",
        [suite("TestTwoCampaignsOverOneCohortEachPayOnce")],
        "'one event per campaign' -- 10 + 15 points, not 10",
    ),
    (
        "M4 the head start arrives as a counted kind instead of points",
        CAMPAIGN,
        "\t\t\tKind:   trust.KindSeedFastTrack,",
        "\t\t\tKind:   trust.KindEditApproved,",
        [suite("TestTheHeadStartIsPointsAndTheLevelFollowsTheOneCurve",
               "TestTheCampaignPaysItsMembersAndNobodyElse")],
        "the head start's own amount, and the level-follows-the-curve assertion",
    ),
    (
        # The first aim at this one was a BUILD BREAK, not a kill: returning `len(userIDs)` left `after`
        # assigned and never used, and a mutation that does not compile is not a verdict (the same lesson
        # the 11a and 11c batteries each recorded once). Re-aimed so it compiles and still changes the
        # answer.
        "M5 adding a member reports the list's length rather than what changed",
        SERVICE,
        '\tbefore, err := s.queries.CountSeedCohortMembers(ctx, cohortID)\n\tif err != nil {\n\t\treturn 0, err\n\t}\n\n\terr = s.withTxn(func(tx *queries.Queries) error {\n\t\tfor _, userID := range userIDs {\n\t\t\tif err := tx.AddSeedCohortMember(ctx, queries.AddSeedCohortMemberParams{\n\t\t\t\tCohortID: cohortID,\n\t\t\t\tUserID:   userID,\n\t\t\t}); err != nil {\n\t\t\t\treturn err\n\t\t\t}\n\t\t}\n\t\treturn nil\n\t})\n\tif err != nil {\n\t\treturn 0, err\n\t}\n\n\tafter, err := s.queries.CountSeedCohortMembers(ctx, cohortID)\n\tif err != nil {\n\t\treturn 0, err\n\t}\n\treturn after - before, nil',
        '\treturn len(userIDs), s.withTxn(func(tx *queries.Queries) error {\n\t\tfor _, userID := range userIDs {\n\t\t\tif err := tx.AddSeedCohortMember(ctx, queries.AddSeedCohortMemberParams{\n\t\t\t\tCohortID: cohortID,\n\t\t\t\tUserID:   userID,\n\t\t\t}); err != nil {\n\t\t\t\treturn err\n\t\t\t}\n\t\t}\n\t\treturn nil\n\t})',
        [suite("TestAddingAMemberWhoIsAlreadyThereCountsForNothing")],
        "'the second add changed nothing, and must not report that it did'",
    ),
    (
        # The new public path in the trust service: the flag that says an event was NEW. Both of its
        # answers are load-bearing, so forcing either one has to be visible.
        "M6 the recording path reports every event as already paid",
        TRUST,
        "\t\tinserted = true",
        "\t\tinserted = false",
        [suite("TestTheCampaignPaysItsMembersAndNobodyElse",
               "TestAMemberAddedLaterIsPaidByTheNextRun")],
        "the first run's paid count",
    ),
]


def run(cmd):
    env = dict(os.environ)
    env["POSTGRES_DB"] = DSN
    p = subprocess.run(cmd, cwd=REPO, env=env, capture_output=True, text=True, timeout=1800)
    return p.returncode, p.stdout + p.stderr


def verdict(out):
    if "[build failed]" in out or "build failed" in out or "declared and not used" in out:
        return "BROKEN"
    if "--- FAIL" in out:
        return "RED"
    return "GREEN"


def main():
    only = [a for a in sys.argv[1:] if not a.startswith("-")]
    results = []
    for name, path, anchor, repl, cmds, expects in MUTATIONS:
        if only and not any(name.startswith(o) for o in only):
            continue

        full = os.path.join(REPO, path)
        with open(full) as f:
            original = f.read()

        hits = original.count(anchor)
        if hits != 1:
            print(f"!! {name}: anchor matched {hits} times, SKIPPING (not a verdict)")
            results.append((name, "ANCHOR-MISS", expects))
            continue

        with open(full, "w") as f:
            f.write(original.replace(anchor, repl))

        verdicts = []
        try:
            for cmd in cmds:
                rc, out = run(cmd)
                verdicts.append(verdict(out))
                if verdicts[-1] == "RED":
                    reds = [l.strip() for l in out.splitlines() if l.strip().startswith("--- FAIL")]
                    print(f"   red: {'; '.join(reds[:4])}")
                if verdicts[-1] == "GREEN":
                    print(f"   green: {' '.join(cmd[-3:])} -- NOTHING NOTICED")
        finally:
            with open(full, "w") as f:
                f.write(original)
            assert open(full).read() == original, "RESTORE FAILED"

        if "BROKEN" in verdicts:
            overall = "BROKEN"
        elif all(v == "RED" for v in verdicts):
            overall = "KILLED"
        else:
            overall = "SURVIVED"

        print(f"{overall:8} {name}\n         expects: {expects}")
        results.append((name, overall, expects))

    print("\n==== summary ====")
    for name, v, expects in results:
        print(f"{v:12} {name}")
    killed = sum(1 for _, v, _ in results if v == "KILLED")
    print(f"\n{killed}/{len(results)} killed")
    return 0 if killed == len(results) else 1


if __name__ == "__main__":
    sys.exit(main())
