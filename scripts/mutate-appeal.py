#!/usr/bin/env python3
"""The mutation ledger for phase 14 slice 2 (the appeal) and the policy page.

Each row: mutate ONE rule, run the test that claims to enforce it, and require that test to go RED while
the run is otherwise clean. Then restore and require the file to be byte-identical to what it was, because
a mutation that was not restored invalidates every run after it.

Why this exists rather than a green suite: a test that has never been observed failing is a test whose
subject is unknown. The identity rule is the whole point of the slice, and "a different moderator decides"
is exactly the kind of claim that a test can appear to make while never reaching the line.
"""
import subprocess
import sys
from pathlib import Path

ROOT = Path("/home/alvaro/code-local/go/stash-box")
DSN = "postgres:smoke_pw@127.0.0.1:55434/sbx-appeal-141?sslmode=disable"

APPEAL = ROOT / "internal/service/content/appeal.go"
MIGRATION = ROOT / "internal/database/migrations/postgres/141_content_source_appeal.up.sql"
POLICY = ROOT / "internal/api/routes_policy.go"

UNIT = ["go", "test", "-count=1", "-run", "TheDeciderIsNeitherTheOperatorWhoBlockedNorTheMemberWhoFiled", "./internal/service/content/"]
WIRE_IDENTITY = ["go", "test", "-tags", "integration", "-count=1", "-p", "1", "-run", "TestAnAppealIsDecidedBySomebodyWhoNeitherBlockedNorFiledIt", "./internal/api/"]
WIRE_FILING = ["go", "test", "-tags", "integration", "-count=1", "-p", "1", "-run", "TestAnAppealNeedsALiveBlockAStatementAndOnePerMember", "./internal/api/"]
WIRE_SURVIVES = ["go", "test", "-tags", "integration", "-count=1", "-p", "1", "-run", "TestAGrantedAppealReleasesTheBlockAndTheAppealOutlivesIt", "./internal/api/"]
WIRE_DENIED = ["go", "test", "-tags", "integration", "-count=1", "-p", "1", "-run", "TestADeniedAppealLeavesTheBlockInForceAndTheDecisionRecorded", "./internal/api/"]
POLICY_SERVED = ["go", "test", "-count=1", "-run", "TestThePolicyIsServedToAnybodyWithNoAccountAtAll", "./internal/api/"]
POLICY_CONTACT = ["go", "test", "-count=1", "-run", "TestThePolicyNamesTheConfiguredAddressForNotices", "./internal/api/"]
POLICY_UNSET = ["go", "test", "-count=1", "-run", "TestThePolicySaysOutLoudWhenNoAddressForNoticesIsConfigured", "./internal/api/"]

MUTATIONS = [
    dict(
        name="arm 1 inverted: the operator who recorded the block may decide",
        path=APPEAL,
        old="if appeal.BlockedByID.Valid && appeal.BlockedByID.UUID == actor {",
        new="if appeal.BlockedByID.Valid && appeal.BlockedByID.UUID != actor {",
        cmd=UNIT, expect_red="the unit table's blocker case",
    ),
    dict(
        name="arm 2 inverted: the member who filed may decide",
        path=APPEAL,
        old="if appeal.FiledByID.Valid && appeal.FiledByID.UUID == actor {",
        new="if appeal.FiledByID.Valid && appeal.FiledByID.UUID != actor {",
        cmd=UNIT, expect_red="the unit table's appellant case",
    ),
    dict(
        name="the blocker is not carried onto the appeal",
        path=APPEAL,
        old="BlockedByID: block.CreatedByID,",
        new="BlockedByID: uuid.NullUUID{},",
        cmd=WIRE_IDENTITY, expect_red="the wire test's blocked_by assertion (and arm 1, which can no longer fire)",
    ),
    dict(
        name="the unique-violation branch never fires (a second appeal is a 500, not a named refusal)",
        path=APPEAL,
        old="\treturn pgErr.Code == \"23505\"",
        new="\t_ = pgErr\n\treturn false",
        cmd=WIRE_FILING, expect_red="the wire test's 'already been appealed by this member'",
    ),
    dict(
        name="a grant does not release the block it is about",
        path=APPEAL,
        old="released, err = releaseKey(ctx, tx, appeal.Kind, appeal.Value, actor)",
        new="released, err = releaseKey(ctx, tx, appeal.Kind, appeal.Value+\"-not-this-one\", actor)",
        cmd=WIRE_SURVIVES, expect_red="the wire test's Released, and the re-block finding the old block",
    ),
    dict(
        name="the release deletes the appeal with the block (CASCADE, not SET NULL)",
        path=MIGRATION,
        old='"block_id" UUID REFERENCES "content_source_block" ("id") ON DELETE SET NULL,',
        new='"block_id" UUID REFERENCES "content_source_block" ("id") ON DELETE CASCADE,',
        cmd=WIRE_SURVIVES, expect_red="the wire test's survival read (the appeal would be gone)",
    ),
    dict(
        name="the policy stops promising the acknowledgement window",
        path=POLICY,
        old="<strong>Acknowledgement within 24 hours.</strong>",
        new="<strong>We read notices eventually.</strong>",
        cmd=POLICY_SERVED, expect_red="the served-policy commitment check",
    ),
    dict(
        name="the policy shows an empty mailto instead of naming the missing contact",
        path=POLICY,
        old="\tif contact != \"\" {",
        new="\tif contact != \"\" || true {",
        cmd=POLICY_UNSET, expect_red="the unset-state test: with the branch forced, the page shows an empty mailto instead of naming the gap",
    ),
]


def run(cmd):
    proc = subprocess.run(
        cmd, cwd=ROOT,
        env={**__import__("os").environ, "POSTGRES_DB": DSN},
        capture_output=True, text=True, timeout=900,
    )
    return proc.returncode, proc.stdout + proc.stderr


def main():
    results = []
    for m in MUTATIONS:
        path = m["path"]
        original = path.read_text()
        if m["old"] not in original:
            print(f"SKIP (anchor not found): {m['name']}")
            results.append((m["name"], "ANCHOR-MISSING"))
            continue
        if original.count(m["old"]) != 1:
            print(f"SKIP (anchor appears {original.count(m['old'])}x, not once): {m['name']}")
            results.append((m["name"], "ANCHOR-AMBIGUOUS"))
            continue

        path.write_text(original.replace(m["old"], m["new"]))
        code, output = run(m["cmd"])
        path.write_text(original)

        restored = path.read_text() == original
        if not restored:
            print(f"RESTORE FAILED for {m['name']} -- stopping")
            sys.exit(1)

        if code == 0:
            verdict = "SURVIVED (the test passed on the mutant: it does not enforce this)"
        elif "build failed" in output or "cannot use" in output or "undefined:" in output or "syntax error" in output:
            verdict = "COMPILE-ERR (not a verdict)"
        else:
            failing = [l.strip() for l in output.splitlines() if l.strip().startswith("--- FAIL")]
            verdict = "KILLED by " + (", ".join(failing) if failing else "an unlabelled failure")
        print(f"{m['name']}\n    -> {verdict}\n    (expected red: {m['expect_red']})")
        results.append((m["name"], verdict))

    print("\n=== LEDGER ===")
    for name, verdict in results:
        print(f"{verdict.split(' ')[0]:12} {name}")

    code, output = run(["go", "test", "-count=1", "-run", "TestTheDecider", "./internal/service/content/"])
    print(f"\nRESTORED TREE, unit test: exit {code}")
    return 0 if all("KILLED" in v for _, v in results) else 1


if __name__ == "__main__":
    sys.exit(main())
