#!/usr/bin/env python3
"""Mutation battery for the referral payment (plan phase 11 slice 11b).

WHY THIS FILE EXISTS. Every claim the slice makes -- one payment per invitee, at Registered and
not at signup, one level deep, recorded whenever a key was used -- is a claim about behaviour that
a passing test does not establish. This breaks one rule at a time and requires a NAMED test to go
red.

TWO THINGS IT DOES CAREFULLY, both learned from the harness's own past failures:

  - It asserts the mutation CHANGED the file (`str.replace` with a stale anchor is a silent no-op,
    and a probe that never mutated anything reads as "the test cannot catch this").
  - It reports BROKEN separately from KILLED and from SURVIVED. In Go a mutation that removes a
    binding leaves it unused, the package does not compile, and a harness that folds that into
    "killed" reports evidence it does not have.

Run:  python3 scripts/mutate-referral.py
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

PURE = ["go", "test", "-count=1", "./internal/service/invite/"]
WIRE = [
    "go",
    "test",
    "-tags=integration",
    "-count=1",
    "-p",
    "1",
    "./internal/api/",
]

# name, path, anchor, replacement, [commands], what the red should be
MUTATIONS = [
    (
        "M1 the inviter is only recorded when the instance REQUIRES invites (the old code)",
        "internal/service/user/service.go",
        "\t\tvar invitedBy *uuid.UUID\n\t\tswitch {\n\t\tcase data.InviteKey != nil:",
        "\t\tvar invitedBy *uuid.UUID\n\t\tswitch {\n\t\tcase data.InviteKey != nil && config.GetRequireInvite():",
        [WIRE],
        "TestSigningUpWithAKeyRecordsTheInviterWithoutRequiringInvites",
    ),
    (
        "M2 no trust service has the referral hook attached (the wiring, not the rule)",
        "internal/service/factory.go",
        "\tsvc := trust.NewTrust(queries.New(f.db), f.withTxn)\n\treturn svc.WithReferral(func(ctx context.Context, inviteeID uuid.UUID, level trust.LevelEnum) error {\n\t\treturn invite.NewReferral(queries.New(f.db), svc).Pay(ctx, inviteeID, level)\n\t})",
        "\tsvc := trust.NewTrust(queries.New(f.db), f.withTxn)\n\treturn svc",
        [WIRE],
        "every payment test (nothing pays at all)",
    ),
    (
        "M3 the payment goes through RecordEvent, so the referrer's own referrer is paid",
        "internal/service/invite/referral.go",
        "\tif _, err := r.trust.RecordEventWithoutChain(ctx, trust.Event{",
        "\tif _, err := r.trust.RecordEvent(ctx, trust.Event{",
        [WIRE],
        "TestTheReferralChainIsOneLevelDeep",
    ),
    (
        "M4 the payment is keyed to the REFERRER, so a member is paid only for their first invitee",
        "internal/service/invite/referral.go",
        "\t\tEntityType: entityTypeUser,\n\t\tEntityID:   &inviteeID,",
        "\t\tEntityType: entityTypeUser,\n\t\tEntityID:   invitedBy,",
        [WIRE],
        "TestAReferrerIsPaidForEachInviteeTheyBringIn",
    ),
    (
        "M5 eligibility is not checked, so an invitee pays at signup",
        "internal/service/invite/referral.go",
        "\tif level < trust.LevelRegistered {\n\t\treturn OutcomeNotYetEligible\n\t}",
        "\tif level < trust.LevelPublic {\n\t\treturn OutcomeNotYetEligible\n\t}",
        [PURE, WIRE],
        "the signup-pays-nothing tests, and the pure table's public cases",
    ),
    (
        # M5 above removes ONE of the rule's two implementations and correctly reddens only the
        # pure half: the trust service refuses to ask the payment at all below Registered, so the
        # wire path never sees the level the referral would have refused. A rule expressed twice
        # needs BOTH sites cut before the wire claim can lose -- which is what this entry is, and
        # why the surviving M5 is a finding about the code (two layers) rather than a weak test.
        "M5b BOTH implementations of eligibility are removed (the wire claim)",
        "internal/service/trust/service.go",
        "\tif rollup == nil || LevelEnum(rollup.Level) < LevelRegistered {",
        "\tif rollup == nil {",
        [WIRE],
        "a payment at signup: TestAReferralPaysNothingAtSignupAndOnceAtRegistered",
        # The second site, cut in the same run.
        (
            "internal/service/invite/referral.go",
            "\tif level < trust.LevelRegistered {",
            "\tif level < trust.LevelPublic {",
        ),
    ),
    (
        "M6 the self-invite guard is dropped",
        "internal/service/invite/referral.go",
        "\tif *invitedBy == inviteeID {\n\t\treturn OutcomeSelfInvited\n\t}",
        "\tif *invitedBy == uuid.Nil {\n\t\treturn OutcomeSelfInvited\n\t}",
        [PURE, WIRE],
        "TestASelfInvitedMemberPaysNobody and the pure table",
    ),
    (
        "M7 the referral is worth nothing",
        "internal/service/trust/models.go",
        "\tKindInviteActivated: 20,",
        "\tKindInviteActivated: 0,",
        [PURE],
        "TestTheReferralWeightCannotSubstituteForContributing",
    ),
    (
        "M8 the count is not scored: the kind is counted and worth nothing in the total",
        "internal/service/trust/models.go",
        "\t\tt.InvitesActivated*PointsPerKind[KindInviteActivated] +",
        "",
        [PURE],
        "TestTheReferralKindIsCountedAndScored",
    ),
    (
        "M9 the rollup's CASE arm does not match the kind, so the count never moves",
        # The GENERATED query, not the .sql: this is the statement that runs, and mutating the
        # source would need a sqlc regeneration before the tests saw anything.
        "internal/queries/trust.sql.go",
        "    CASE WHEN $2::TEXT = 'invite_activated' THEN $3::INTEGER ELSE 0 END,",
        "    CASE WHEN $2::TEXT = 'invite_activated_' THEN $3::INTEGER ELSE 0 END,",
        [WIRE],
        "every payment test (the count stays 0)",
    ),
    (
        "M10 the published count is hardcoded, so the field is not the rollup",
        "internal/api/resolver_model_user.go",
        "\t\tInvitesActivated:     rollup.InvitesActivated,",
        "\t\tInvitesActivated:     0,",
        [WIRE],
        "TestTheReferralCountIsPublishedOnTheOwnersTrustObject",
    ),
]


def run(cmd):
    env = dict(os.environ)
    env["POSTGRES_DB"] = DSN
    p = subprocess.run(
        cmd, cwd=REPO, env=env, capture_output=True, text=True, timeout=1200
    )
    return p.returncode, p.stdout + p.stderr


def verdict(out):
    if "[build failed]" in out or "cannot use" in out or "declared and not used" in out:
        return "BROKEN"
    if "build failed" in out or "setup failed" in out:
        return "BROKEN"
    return "RED" if ("--- FAIL" in out or "\nFAIL" in out or out.startswith("FAIL")) else "GREEN"


def main():
    only = [a for a in sys.argv[1:] if not a.startswith("-")]
    results = []
    for entry in MUTATIONS:
        name, path, anchor, repl, cmds, expects = entry[:6]
        extra = entry[6] if len(entry) > 6 else None
        if only and not any(name.startswith(o) for o in only):
            continue
        # An entry may carry a SECOND site to cut in the same run, which is what a rule that is
        # implemented twice needs: cutting one leaves the other enforcing it.
        sites = [(path, anchor, repl)]
        if extra:
            sites.append((extra[0], extra[1], extra[2]))

        restored = []
        try:
            for site_path, site_anchor, site_repl in sites:
                full = os.path.join(REPO, site_path)
                with open(full) as f:
                    original = f.read()

                hits = original.count(site_anchor)
                if hits != 1:
                    print(f"!! {name} [{site_path}]: anchor matched {hits} times, SKIPPING")
                    results.append((name, "ANCHOR-MISS", expects))
                    restored = []
                    break

                mutated = original.replace(site_anchor, site_repl)
                assert mutated != original, "the mutation did not change the file"
                with open(full, "w") as f:
                    f.write(mutated)
                restored.append((full, original))
            else:
                verdicts = []
                for cmd in cmds:
                    rc, out = run(cmd)
                    verdicts.append(verdict(out))
                    if verdicts[-1] == "RED":
                        reds = [
                            line.strip()
                            for line in out.splitlines()
                            if line.strip().startswith("--- FAIL")
                        ]
                        print(f"   red: {'; '.join(reds[:4])}")

                if "BROKEN" in verdicts:
                    overall = "BROKEN"
                elif all(v == "RED" for v in verdicts):
                    overall = "KILLED"
                else:
                    overall = "SURVIVED"

                print(f"{overall:8} {name}\n         expects: {expects}")
                results.append((name, overall, expects))
        finally:
            for full, original in restored:
                with open(full, "w") as f:
                    f.write(original)
                assert open(full).read() == original, f"RESTORE FAILED for {full}"
    print("\n==== summary ====")
    for name, v, expects in results:
        print(f"{v:12} {name}")
    killed = sum(1 for _, v, _ in results if v == "KILLED")
    print(f"\n{killed}/{len(results)} killed")
    return 0 if killed == len(results) else 1


if __name__ == "__main__":
    sys.exit(main())
