#!/usr/bin/env python3
"""Mutation battery for the landing page, the shared eligibility rule and the QR (phase 11 slice 11c).

Each probe breaks ONE rule and requires the test NAMED FOR IT to go red. The probes are chosen at the
seams where this slice could be wrong in a way that looks right: a rule that is asked twice, a page
that answers a question it must not answer, a scan that records a person, and a QR that encodes
something other than the link beside it.

Run:  python3 scripts/mutate-invite-landing.py [P1 P2 ...]
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

PURE_RULE = ["go", "test", "-count=1", "-run", "TestUnusable|TestLinkURL", "-v", "./internal/service/invite/"]
PURE_QR = ["go", "test", "-count=1", "-v", "./internal/qr/"]
# The `-run` filter is coverage: a probe whose subject is not in this pattern cannot be killed by it,
# and reports SURVIVED for a reason that is about the battery rather than about the code. It cost one
# false survivor (the shared-address precedence test was written after this line, so P8 ran without it).
WIRE = ["go", "test", "-tags=integration", "-count=1", "-p", "1", "-run",
        "TestTheLandingPage|TestAnExpiredKeysScan|TestACodeThatIsNotAKey|TestAKeysLink"
        "|TestASpentInviteKey|TestAnExpiredInviteKey|TestTheSharedAddressPrefers",
        "-v", "./internal/api/"]

MUTATIONS = [
    (
        "P1 the shared rule stops checking the expiry",
        "internal/service/invite/eligibility.go",
        "\tif key.ExpireTime != nil && key.ExpireTime.Before(now) {",
        "\tif false && key.ExpireTime != nil && key.ExpireTime.Before(now) {",
        [PURE_RULE, WIRE],
        "the rule's own table, the landing page's expired-key test, AND the 11b refusal test -- the "
        "point of one rule with two callers is that breaking it reddens both",
    ),
    (
        "P2 the shared rule stops checking exhaustion",
        "internal/service/invite/eligibility.go",
        "\tif key.Uses != nil && pendingUses >= int(*key.Uses) {",
        "\tif false && key.Uses != nil && pendingUses >= int(*key.Uses) {",
        [PURE_RULE, WIRE],
        "the rule's own table and the spent-key refusal",
    ),
    (
        "P3 a dead key's visit is counted after all",
        "internal/service/invite/scan.go",
        "\t\tif Unusable(&key, len(pending), time.Now()) != nil {\n\t\t\treturn nil\n\t\t}",
        "\t\tif false && Unusable(&key, len(pending), time.Now()) != nil {\n\t\t\treturn nil\n\t\t}",
        [WIRE],
        "'a scan of an expired key is not counted' -- the plan's own sentence",
    ),
    (
        "P4 a scan records a PERSON (it must record nobody)",
        "internal/service/invite/scan.go",
        "\t\treturn RecordEvent(ctx, tx, key.ID, key.GeneratedBy, EventScan, nil)",
        "\t\treturn RecordEvent(ctx, tx, key.ID, key.GeneratedBy, EventScan, &key.GeneratedBy)",
        [WIRE],
        "the landing page test's NULL user assertion: a scan is a count, not a visitor",
    ),
    (
        "P5 the QR encodes something other than the link beside it",
        "internal/api/resolver_model_invite_key.go",
        "\treturn qr.SVG(invite.LinkURL(config.GetHostURL(), key.ID), 4)",
        "\treturn qr.SVG(config.GetHostURL(), 4)",
        [WIRE],
        "the wire test that decodes the QR back to `link`: two call sites building a URL is exactly "
        "how a code ends up sending somebody somewhere else",
    ),
    (
        "P6 the renderer stops requiring a quiet zone",
        "internal/qr/svg.go",
        "\tfor i := 0; i < QuietZone; i++ {",
        "\tfor i := 0; i < 0; i++ {",
        [PURE_QR],
        "the refusal test: a matrix with no margin must not be rendered",
    ),
    (
        # The defect that was LIVE on the deployed instance a moment before this probe was written: the
        # link was built only from `host_url`, which the default deployment does not set, so a member's
        # link read "/invite/<code>" -- a path, which cannot be handed to anybody. The walk's D verdict
        # FAILED on the deployment in that state (measured), and this probe makes the same defect
        # redden the test in the suite.
        "P8 the shared address stops falling back to the request's own address",
        "internal/api/resolver_model_invite_key.go",
        '\tif configured := config.GetHostURL(); configured != "" {',
        '\tif configured := config.GetHostURL(); true {',
        [WIRE],
        "the precedence test's fallback branch: a default deployment must still produce a usable link",
    ),
    (
        # The first aim at this probe was a BUILD BREAK, not a kill: dropping the TrimSuffix call left
        # the `strings` import unused, and a mutation that does not compile is not a verdict. This aim
        # keeps the import used and still stops the normalisation.
        "P7 the link keeps a trailing slash, so a configured host_url gets a double slash",
        "internal/service/invite/scan.go",
        '\treturn strings.TrimSuffix(baseURL, "/") + InvitePath + keyID.String()',
        '\treturn strings.TrimPrefix(baseURL, "/") + InvitePath + keyID.String()',
        [PURE_RULE],
        "the link table's trailing-slash case: one slash is one link, two are a 404",
    ),
]


def run(cmd):
    env = dict(os.environ)
    env["POSTGRES_DB"] = DSN
    p = subprocess.run(cmd, cwd=REPO, env=env, capture_output=True, text=True, timeout=1800)
    return p.returncode, p.stdout + p.stderr


def verdict(out):
    for marker in ("[build failed]", "build failed", "declared and not used", "cannot use"):
        if marker in out:
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
                    print(f"   red: {'; '.join(r[:110] for r in reds[:4])}")
                elif verdicts[-1] == "BROKEN":
                    print(f"   build: {[l for l in out.splitlines() if 'not used' in l][:1]}")
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
