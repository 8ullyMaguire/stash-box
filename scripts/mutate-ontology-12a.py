#!/usr/bin/env python3
"""Mutation battery for the federation ontology declaration (phase 12, slice 12a).

Six probes, one per rule the slice is worth having. Each names the test that must redden; a probe whose
named test is not in the run filter, or whose mutation does not compile, is NOT a verdict and the script
says so rather than counting it.

    POSTGRES_DB='postgres:smoke_pw@127.0.0.1:55434/sbx-pr1284?sslmode=disable' \\
        python3 scripts/mutate-ontology-12a.py [M1 M2 ...]
"""

import os
import subprocess
import sys

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

SERVICE = "internal/service/federation/ontology.go"

DB = os.environ.get("POSTGRES_DB", "postgres:smoke_pw@127.0.0.1:55434/sbx-pr1284?sslmode=disable")

UNIT = ["go", "test", "-count=1", "-v", "./internal/service/federation/"]
INTEGRATION = ["go", "test", "-tags", "integration", "-count=1", "-p", "1", "-v", "./internal/api/"]

MUTATIONS = [
    (
        "M1 the field is allowed to a peer that cannot read it",
        SERVICE,
        "Allowed: mine && peer,",
        "Allowed: mine,",
        [UNIT + ["-run", "TestCompatibilityRefusesAV2FieldToAV1Peer"]],
        "the peer's half of the condition is what the whole slice is about: with it gone, a core 1 "
        "peer is sent a core 2 field and the refusal never happens",
    ),
    (
        "M2 the refusal stops naming which side is behind",
        SERVICE,
        'return fmt.Sprintf("introduced in core %d; the peer declares core %s only",\n\t\t\tf.since, versionList(theirs))',
        'return fmt.Sprintf("introduced in core %d; the peer is behind", f.since)',
        [UNIT + ["-run", "TestCompatibilityRefusesAV2FieldToAV1Peer"]],
        "a refusal that does not say who has to move leaves the operator to find out, which is the "
        "difference between a decision and a shrug",
    ),
    (
        "M3 the digest stops canonicalising the document",
        SERVICE,
        """	sort.Slice(edges, func(i, j int) bool {
		if edges[i].Tag != edges[j].Tag {
			return edges[i].Tag < edges[j].Tag
		}
		return edges[i].Implies < edges[j].Implies
	})""",
        """	sort.SliceStable(edges, func(i, j int) bool { return true })""",
        [UNIT + ["-run", "TestCanonicalDigestIgnoresTheOrderOfTheDocument"]],
        "two instances holding the same ontology must agree; without the sort they agree only when "
        "their rows came back in the same order",
    ),
    (
        "M4 an edge with no name is accepted instead of refused",
        SERVICE,
        """	for i, e := range edges {
		if strings.TrimSpace(e.Tag) == "" || strings.TrimSpace(e.Implies) == "" {
			return nil, fmt.Errorf("%w: edge %d has no tag or no implied tag", ErrOntologyUnreadable, i)
		}
	}""",
        """	for _, e := range edges {
		_ = e
	}""",
        [UNIT + ["-run", "TestCanonicalDigestRefusesARubbishDocumentByName"]],
        "a half-written edge digests as a value no honest document produces, so two peers agree "
        "about an implication nobody can resolve",
    ),
    (
        "M5 the hex check on a peer's digest is dropped",
        SERVICE,
        'if _, err := hex.DecodeString(d.Digest); err != nil {',
        'if _, err := hex.DecodeString(d.Digest); err != nil && d.Digest == "" {',
        [UNIT + ["-run", "TestValidateDeclarationRefusesWhatAPeerCouldSend"]],
        "a non-hex digest is then compared as a string and matches nothing, which reads as a "
        "different ontology rather than as a broken sender",
    ),
    (
        "M6 namedTags counts edges instead of names",
        SERVICE,
        """		seen[e.Tag] = struct{}{}
		seen[e.Implies] = struct{}{}""",
        """		seen[e.Tag] = struct{}{}""",
        [UNIT + ["-run", "TestLocalDeclarationIsReadPerCallNotCached"]],
        "the count a reader compares against the tags table must be the count the document supports; "
        "counting one side of each edge understates it and reads as a truncated ontology",
    ),
]


def run(cmd):
    proc = subprocess.run(cmd, cwd=REPO, capture_output=True, text=True, timeout=900,
                          env=dict(os.environ, POSTGRES_DB=DB))
    return proc.returncode, proc.stdout + proc.stderr


def verdict(out):
    if "build failed" in out or "[build failed]" in out or "declared and not used" in out:
        return "BROKEN"
    if "--- FAIL" in out or "panic:" in out:
        return "RED"
    if "ok  \t" in out or "--- PASS" in out:
        return "GREEN"
    return "NO-VERDICT"


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

        print(f"running: {name}")
        with open(full, "w") as f:
            f.write(original.replace(anchor, repl))

        verdicts = []
        try:
            for cmd in cmds:
                _, out = run(cmd)
                v = verdict(out)
                verdicts.append(v)
                if v == "RED":
                    reds = [l.strip() for l in out.splitlines() if l.strip().startswith("--- FAIL")]
                    print(f"   red: {'; '.join(reds[:3]) or 'panic'}")
                if v == "GREEN":
                    print("   green -- NOTHING NOTICED")
                if v == "BROKEN":
                    print(f"   BUILD BROKEN, not a verdict: {out.strip().splitlines()[-1][:160]}")
                if v == "NO-VERDICT":
                    print("   NO VERDICT: the run printed neither a pass nor a failure")
        finally:
            with open(full, "w") as f:
                f.write(original)
            assert open(full).read() == original, "RESTORE FAILED"

        if "BROKEN" in verdicts or "NO-VERDICT" in verdicts:
            overall = "BROKEN"
        elif all(v == "RED" for v in verdicts):
            overall = "KILLED"
        else:
            overall = "SURVIVED"

        print(f"{overall:8} {name}\n         expects: {expects}\n")
        results.append((name, overall, expects))

    print("==== summary ====")
    for name, v, _ in results:
        print(f"{v:12} {name}")
    killed = sum(1 for _, v, _ in results if v == "KILLED")
    broken = [n for n, v, _ in results if v in ("BROKEN", "ANCHOR-MISS")]
    print(f"\n{killed}/{len(results)} killed" + (f"; NOT VERDICTS: {broken}" if broken else ""))

    # The restored tree, run whole rather than narrowed: a narrowed run says the kill was restored,
    # not that the package is whole.
    _, out = run(UNIT)
    print("RESTORED SERVICE PACKAGE:",
          "green" if "FAIL" not in out else "RED -- A MUTATION IS STILL IN THE TREE")
    return 0 if killed == len(results) and "FAIL" not in out else 1


if __name__ == "__main__":
    sys.exit(main())
