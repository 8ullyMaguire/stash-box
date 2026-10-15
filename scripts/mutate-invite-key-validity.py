#!/usr/bin/env python3
"""Mutation battery for the invitation-integrity fix (plan phase 11, slice 11b's code path).

WHAT THIS FIX IS: `validateInviteKey` wrapped ALL of its checks in `if config.GetRequireInvite()`,
so an instance that does not require invites accepted a key that had already signed somebody up and
enforced no expiry; and the key's CONSUMPTION was behind the same flag, so such an instance never
spent a key at all. Plus the schema's documented default use count ("one use" when unset) was not
implemented -- the column was left NULL, which every query reads as UNLIMITED.

WHY THE BATTERY IS SHORT AND IS STILL RUN: the pre-fix red run is already on the record for three of
these (three tests failed on the unfixed tree before the fix was written, which is stronger evidence
than a mutation). What the battery adds is the OTHER direction: that each half of the fix is
individually load-bearing, and that no test passes for a reason unrelated to the rule it names.

Run:  python3 scripts/mutate-invite-key-validity.py [M1 M2 ...]
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

T = [
    "go",
    "test",
    "-tags=integration",
    "-count=1",
    "-p",
    "1",
    "-run",
    "TestASpentInviteKey|TestAnExpiredInviteKey|TestNoKeyIsStillAccepted|"
    "TestTheRefusalsAreNamed|TestAKeyMintedWithoutAUseCount",
    "-v",
    "./internal/api/",
]

# name, path, anchor, replacement, extra site or None, what going red proves
MUTATIONS = [
    (
        "M1 the expiry and use checks go back inside the require_invite guard (the original code)",
        "internal/service/user/validate.go",
        "\tif inviteKey == nil {\n\t\tif config.GetRequireInvite() {",
        "\tif !config.GetRequireInvite() {\n\t\treturn nil\n\t}\n\tif inviteKey == nil {\n\t\tif config.GetRequireInvite() {",
        None,
        "the two 'even when invites are not required' tests",
    ),
    (
        "M2 an unresolvable key is ignored again instead of refused by name",
        "internal/service/user/validate.go",
        "\tif errors.Is(err, pgx.ErrNoRows) {\n\t\treturn ErrInvalidInviteKey\n\t}",
        "\tif errors.Is(err, pgx.ErrNoRows) {\n\t\treturn nil\n\t}",
        None,
        "the spent-by-activation refusals (the key is DELETED, so this is the path they take)",
    ),
    (
        "M3 an unqualified key stores NULL again, which every reader treats as unlimited",
        "internal/service/user/invite.go",
        "\t\tnewKey.Uses = &uses\n",
        "\t\t_ = uses // the original behaviour: the column was left NULL\n",
        None,
        "the documented-default test",
    ),
    (
        "M4 consuming a presented key is gated on require_invite again",
        "internal/service/user/service.go",
        "\t\tif data.InviteKey != nil {\n\t\t\tusesLeft, err := tx.InviteKeyUsed(ctx, *data.InviteKey)",
        "\t\tif data.InviteKey != nil && config.GetRequireInvite() {\n\t\t\tusesLeft, err := tx.InviteKeyUsed(ctx, *data.InviteKey)",
        None,
        "the third-signup assertion: with the flag off a spent key would still be alive",
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
    for name, path, anchor, repl, extra, expects in MUTATIONS:
        if only and not any(name.startswith(o) for o in only):
            continue

        sites = [(path, anchor, repl)]
        if extra:
            sites.append(extra)

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
                rc, out = run(T)
                v = verdict(out)
                if v == "RED":
                    reds = [l.strip() for l in out.splitlines() if l.strip().startswith("--- FAIL")]
                    print(f"   red: {'; '.join(reds[:4])}")
                print(f"{v:8} {name}\n         expects: {expects}")
                results.append((name, v, expects))
        finally:
            for full, original in restored:
                with open(full, "w") as f:
                    f.write(original)
                assert open(full).read() == original, f"RESTORE FAILED for {full}"

    print("\n==== summary ====")
    for name, v, expects in results:
        print(f"{v:12} {name}")
    killed = sum(1 for _, v, _ in results if v == "RED")
    print(f"\n{killed}/{len(results)} killed (RED)")
    return 0 if killed == len(results) else 1


if __name__ == "__main__":
    sys.exit(main())
