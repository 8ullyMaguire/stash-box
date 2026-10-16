#!/usr/bin/env python3
"""Mutation battery for the federation answering half (phase 12, slices 12b and 12c).

Four probes. The rules worth having here are all REFUSALS, and a refusal is the easiest thing in a
codebase to delete without any test noticing -- so each probe targets the condition, not the message,
except M2 which targets the message alone.

    POSTGRES_DB='postgres:smoke_pw@127.0.0.1:55434/sbx-pr1284?sslmode=disable' \\
        python3 scripts/mutate-answer-12b.py [M1 M2 ...]
"""

import os
import subprocess
import sys

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

ANSWER = "internal/service/federation/answer.go"
ASK = "internal/service/federation/ask.go"

DB = os.environ.get("POSTGRES_DB", "postgres:smoke_pw@127.0.0.1:55434/sbx-pr1284?sslmode=disable")

UNIT = ["go", "test", "-count=1", "-v", "./internal/service/federation/"]
INTEGRATION = ["go", "test", "-tags", "integration", "-count=1", "-p", "1", "-v", "./internal/api/"]

MUTATIONS = [
    (
        "M1 an instance with no identity answers anyway",
        ANSWER,
        '''	if strings.TrimSpace(selfInstanceID) == "" {
		return Answer{}, ErrNoInstanceID
	}''',
        '''	if strings.TrimSpace(selfInstanceID) == "" {
		selfInstanceID = "anonymous"
	}''',
        [UNIT + ["-run", "TestAnswerRefusesWithoutAnIdentity"],
         INTEGRATION + ["-run", "TestFederationIdentifyIntegration"]],
        "the identity is what a peer files the answer under; an invented one files it under a row that "
        "is not yours, silently",
    ),
    (
        "M2 the identity refusal stops naming the config key",
        ANSWER,
        'ErrNoInstanceID = errors.New("this instance has no instance_id, so it cannot answer a peer")',
        'ErrNoInstanceID = errors.New("this instance cannot answer a peer")',
        [UNIT + ["-run", "TestAnswerRefusesWithoutAnIdentity"]],
        "the message is the whole deliverable of a fail-closed setting: the key's absence is what an "
        "operator has to see to fix it",
    ),
    (
        "M3 an identification question is answered with an empty candidate list",
        ANSWER,
        '''	if q.TargetType != "" || q.Description != "" || len(q.CandidateNames) > 0 {
		return Answer{}, fmt.Errorf("%w (target type %q)", ErrIdentifyUnserved, q.TargetType)
	}''',
        '''	if false && q.TargetType != "" {
		return Answer{}, fmt.Errorf("%w (target type %q)", ErrIdentifyUnserved, q.TargetType)
	}''',
        [UNIT + ["-run", "TestAnswerRefusesAnIdentificationQuestionByName"],
         INTEGRATION + ["-run", "TestFederationIdentifyIntegration"]],
        "THIS IS THE MUTATION THE SLICE EXISTS FOR: with it, 'this instance has nothing' and 'this "
        "instance has not built it' become the same answer, and a peer records the empty list as evidence",
    ),
    (
        "M4 a peer's declared claim is not validated",
        ANSWER,
        '''	if q.Ontology.Declared() {
		decl := declarationFrom(q.Ontology)
		if err := ValidateDeclaration(decl); err != nil {
			return Answer{}, fmt.Errorf("%w: %v", ErrClaimRejected, err)
		}
	}''',
        '''	if false && q.Ontology.Declared() {
		decl := declarationFrom(q.Ontology)
		if err := ValidateDeclaration(decl); err != nil {
			return Answer{}, fmt.Errorf("%w: %v", ErrClaimRejected, err)
		}
	}''',
        [UNIT + ["-run", "TestAnswerRefusesAClaimThatIsDeclaredButMalformed"],
         INTEGRATION + ["-run", "TestFederationIdentifyIntegration"]],
        "a half-written claim compares equal to nothing, so the peer's ontology reads as 'a different "
        "one' rather than as 'a confused sender'",
    ),
    (
        "M5 a question with neither a target type nor an ontology is accepted",
        ASK,
        '''	if strings.TrimSpace(q.TargetType) == "" && !q.Ontology.Declared() {
		return fmt.Errorf("a question needs a target type")
	}''',
        '''	if strings.TrimSpace(q.TargetType) == "" && !q.Ontology.Declared() && q.QueryID == uuid.Nil {
		return fmt.Errorf("a question needs a target type")
	}''',
        [UNIT + ["-run", "TestValidateAllowsAQuestionThatOnlyDeclaresAnOntology"]],
        "the arm slice 12b narrowed: a question that names nothing is a question that asks nothing, and "
        "the refusal is the only thing that says so",
    ),
    (
        "M6 the crossing is decided against OUR versions instead of the peer's",
        ANSWER,
        "compat := Compatibility(local.CoreVersions, q.Ontology.CoreVersions)",
        "compat := Compatibility(local.CoreVersions, local.CoreVersions)",
        [UNIT + ["-run", "TestAnswerDecidesWhatMayCrossBackToTheAsker"],
         INTEGRATION + ["-run", "TestFederationIdentifyIntegration"]],
        "the peer's SIDE of the comparison is the whole point of a negotiation: against our own versions "
        "every instance is told it can send everything, which is the silent mis-mapping by another route",
    ),
    (
        "M7 the answer lists only the fields that may cross",
        ANSWER,
        "for _, f := range c.Fields {",
        "for _, f := range c.Fields {\n\t\tif !f.Allowed {\n\t\t\tcontinue\n\t\t}",
        [UNIT + ["-run", "TestAnswerDecidesWhatMayCrossBackToTheAsker"],
         INTEGRATION + ["-run", "TestFederationIdentifyIntegration"]],
        "a peer that receives only the allowed fields cannot tell whether the others were allowed or "
        "never discussed -- the closed list is the difference between a decision and a filter",
    ),
    (
        "M8 every field is reported as allowed",
        ANSWER,
        'Allowed: f.Allowed,',
        'Allowed: true,',
        [UNIT + ["-run", "TestAnswerDecidesWhatMayCrossBackToTheAsker"],
         INTEGRATION + ["-run", "TestFederationIdentifyIntegration"]],
        "the answer tells the peer it can send a field this instance cannot read, which is the failure "
        "the field-by-field decision exists to prevent",
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
                if v != "RED":
                    print(f"   {v}: {out.strip().splitlines()[-1][:150]}")
        finally:
            with open(full, "w") as f:
                f.write(original)
            assert open(full).read() == original, "RESTORE FAILED"

        if any(v in ("BROKEN", "NO-VERDICT") for v in verdicts):
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

    # The restored tree, run WHOLE rather than narrowed: a narrowed run says the kill was restored, not
    # that the package is whole.
    _, out = run(UNIT)
    print("RESTORED SERVICE PACKAGE:", "green" if "FAIL" not in out else "RED -- A MUTATION IS STILL IN THE TREE")
    return 0 if killed == len(results) and "FAIL" not in out else 1


if __name__ == "__main__":
    sys.exit(main())
