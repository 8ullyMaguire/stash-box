#!/usr/bin/env python3
"""Mutation battery for the MCP server (plan phase 13 slice 13a).

Seven probes, one per rule this slice is worth having: the schema and the validator being ONE list, an
unknown argument refused rather than ignored, a notification answered with nothing, a bad line not ending
the session, the instance's refusal kept in its own words, the fingerprint argument submitted in the shape
the instance actually takes, and the API key reaching the instance at all.

Every probe compiles and every one is aimed at a NAMED test -- a mutation that does not build is not a
verdict, and a probe whose named test is not in the run filter is a false survivor (both lessons were paid
for in slices 11a-11c).

Run:  python3 scripts/mutate-mcp.py [M1 M2 ...]
Pure: no database and no network (the GraphQL side is an httptest stub).
"""

import os
import subprocess
import sys

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

TOOLS = "internal/mcp/tools.go"
SERVER = "internal/mcp/server.go"
GRAPHS = "internal/mcp/graphql.go"

FULL = ["go", "test", "-count=1", "-v", "./internal/mcp/"]


def one(name):
    return ["go", "test", "-count=1", "-v", "-run", name, "./internal/mcp/"]


MUTATIONS = [
    (
        "M1 an unknown argument is ignored instead of refused",
        TOOLS,
        "\t\tif !t.has(name) {",
        "\t\tif false && !t.has(name) {",
        [one("TestEveryToolPublishesExactlyTheArgumentsItValidates|TestAMissingRequiredArgumentIsNamed"),
         one("TestAnUnknownArgumentIsRefusedAndNeverReachesTheInstance")],
        "the schema/validator agreement, and the test that the instance is never asked",
    ),
    (
        # The guard on the OTHER side of the same rule: the validator would still refuse the argument, but
        # the tool would advertise one it does not take. A client then sends what it was told to send and
        # is refused -- which is why the published schema is derived from the arg list rather than written.
        "M2 a tool publishes an argument it does not validate",
        TOOLS,
        '\t\tproperties[a.name] = property',
        '\t\tproperties[a.name] = property\n\t\tproperties["ghost_argument"] = map[string]any{"type": "string"}',
        [one("TestEveryToolPublishesExactlyTheArgumentsItValidates")],
        "the published-vs-validated agreement, from the publishing side",
    ),
    (
        "M3 a notification is answered as if it were a request",
        SERVER,
        "\t\tif req.IsNotification() {",
        "\t\tif false && req.IsNotification() {",
        [one("TestANotificationGetsNoAnswer")],
        "'only the request is answered' -- the off-by-one the framing exists to prevent",
    ),
    (
        "M4 one bad line ends the session",
        SERVER,
        '\t\t\t\tError:   &RPCError{Code: codeParseError, Message: "this line is not a JSON-RPC message: " + err.Error()},\n\t\t\t}); err != nil {\n\t\t\t\treturn err\n\t\t\t}\n\t\t\tcontinue',
        '\t\t\t\tError:   &RPCError{Code: codeParseError, Message: "this line is not a JSON-RPC message: " + err.Error()},\n\t\t\t}); err != nil {\n\t\t\t\treturn err\n\t\t\t}\n\t\t\treturn nil',
        [one("TestOneBadLineDoesNotEndTheSession")],
        "'the parse error is answered AND the session continues'",
    ),
    (
        "M5 an instance's refusal becomes an empty success",
        GRAPHS,
        "\tif len(decoded.Errors) > 0 {",
        "\tif false && len(decoded.Errors) > 0 {",
        [one("TestAnInstanceRefusalKeepsItsOwnWording")],
        "the tool error that carries 'not authorized'",
    ),
    (
        "M6 the fingerprints are submitted as several files instead of one",
        TOOLS,
        'vars := map[string]any{"fingerprints": [][]map[string]string{entries}}',
        'vars := map[string]any{"fingerprints": entries}',
        [one("TestFingerprintsAreSubmittedAsOneFile")],
        "'one file, so one group' -- which of these is the file, versus which files are these",
    ),
    (
        # The trap this guards is silent: a decimal rendering is read as hexadecimal and comes back empty,
        # which looks like "not found".
        "M8 a non-hexadecimal hash is passed through to the instance",
        TOOLS,
        "\t\t\tif !isHex(e.Hash) {",
        "\t\t\tif false && !isHex(e.Hash) {",
        [one("TestAHashMustBeHexadecimal")],
        "'the refusal explains why the form matters', and that the instance is never asked",
    ),
    (
        "M7 the api key never reaches the instance",
        GRAPHS,
        '\treq.Header.Set("ApiKey", c.APIKey)',
        '\t_ = c.APIKey',
        [one("TestTheApiKeyTravelsInTheApiKeyHeader")],
        "'a key that never reaches the instance is a server that silently reads nothing'",
    ),
]


def run(cmd):
    p = subprocess.run(cmd, cwd=REPO, capture_output=True, text=True, timeout=900)
    return p.returncode, p.stdout + p.stderr


def verdict(out):
    if "[build failed]" in out or "build failed" in out or "declared and not used" in out or "no required module" in out:
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

        mutated = original.replace(anchor, repl)
        assert mutated != original, f"{name}: the replacement changed nothing"
        with open(full, "w") as f:
            f.write(mutated)

        verdicts = []
        try:
            for cmd in cmds:
                rc, out = run(cmd)
                verdicts.append(verdict(out))
                if verdicts[-1] == "RED":
                    reds = [l.strip() for l in out.splitlines() if l.strip().startswith("--- FAIL")]
                    print(f"   red: {'; '.join(reds[:3])}")
                if verdicts[-1] == "GREEN":
                    print(f"   green on {cmd[-3]} -- NOTHING NOTICED")
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
    for name, v, _ in results:
        print(f"{v:12} {name}")
    killed = sum(1 for _, v, _ in results if v == "KILLED")
    print(f"\n{killed}/{len(results)} killed")
    return 0 if killed == len(results) else 1


if __name__ == "__main__":
    sys.exit(main())
