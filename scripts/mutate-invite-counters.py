#!/usr/bin/env python3
"""Mutation battery for the invite counters (plan phase 11 slice 11c).

Each probe breaks ONE rule the counters claim and requires the test named for it to go red:
the mapping that keeps the three kinds apart, the two write sites that make a row exist at all, and
the resolver that hands them to a client. The row-counting design's central claim -- that a member's
counters survive the deletion of the key they counted -- has its own kill: the last probe breaks the
lookup the counters are read from, and the survival test is the one that must notice.

Run:  python3 scripts/mutate-invite-counters.py [M1 M2 ...]
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

PURE = ["go", "test", "-count=1", "-run", "TestCountsFromRows|TestTheStoredKindStrings",
        "-v", "./internal/service/invite/"]
WIRE = ["go", "test", "-tags=integration", "-count=1", "-p", "1", "-run",
        "TestTheInviteCounters|TestInviteStats", "-v", "./internal/api/"]

MUTATIONS = [
    (
        "M1 the mapping loses the activation arm (a copy-paste that drops one of the three)",
        "internal/service/invite/journal.go",
        "\t\tcase EventActivation:\n\t\t\tout.Activations = row.Count\n",
        "",
        [PURE, WIRE],
        "the pure mapping test and the activation assertion",
    ),
    (
        # The first aim at this was a BUILD BREAK, not a kill: routing every row to "scan" left the
        # `kind` variable unused, and a mutation that does not compile is not a verdict (see the
        # mutation-gate notes). This aim compiles and swaps two arms instead.
        "M2 the mapping swaps the signup and activation arms",
        "internal/service/invite/journal.go",
        "\t\tcase EventSignup:\n\t\t\tout.Signups = row.Count\n\t\tcase EventActivation:\n\t\t\tout.Activations = row.Count",
        "\t\tcase EventSignup:\n\t\t\tout.Activations = row.Count\n\t\tcase EventActivation:\n\t\t\tout.Signups = row.Count",
        [PURE, WIRE],
        "the pure 'keeps the three kinds apart' test and the one-at-a-time wire test",
    ),
    (
        "M3 a signup stops being recorded",
        "internal/service/user/service.go",
        "\t\t\tif err := invite.RecordEvent(ctx, tx, key.ID, key.GeneratedBy, invite.EventSignup, nil); err != nil {",
        "\t\t\tif err := error(nil); err != nil {",
        [WIRE],
        "the signup counter and the survival test",
    ),
    (
        "M4 an activation stops being recorded",
        "internal/service/user/service.go",
        "\t\tif data.InviteKey != nil && invitedBy != nil {\n\t\t\tif err := invite.RecordEvent(ctx, tx, *data.InviteKey, *invitedBy, invite.EventActivation, &user.ID); err != nil {",
        "\t\tif false && data.InviteKey != nil && invitedBy != nil {\n\t\t\tif err := invite.RecordEvent(ctx, tx, *data.InviteKey, *invitedBy, invite.EventActivation, &user.ID); err != nil {",
        [WIRE],
        "the activation counter and the survival test",
    ),
    (
        "M5 the resolver swaps two counters on the way out",
        "internal/api/resolver_model_user.go",
        "\t\tSignups:     counts.Signups,\n\t\tActivations: counts.Activations,",
        "\t\tSignups:     counts.Activations,\n\t\tActivations: counts.Signups,",
        [WIRE],
        "'the counters move one at a time' -- the assertion that exists for exactly this",
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
