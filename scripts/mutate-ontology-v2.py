#!/usr/bin/env python3
"""Mutation battery for core v2 -- the ontology field a version declares (plan phase 12, slice 12g).

    python3 scripts/mutate-ontology-v2.py [M1 M2 .. M6]

Same procedure as mutate-image-gate.py, including the two rules that pass earned: every probe names the ONE
test that must notice, and after restoring the file the WHOLE package is run again, so "the neighbours stay
green" is a measurement rather than an assumption. A probe whose run says "no tests to run" is a bug in this
script -- `go test -run` exits ZERO when the package does not contain the named test -- and it stops rather
than reporting a survivor.
"""

import os
import subprocess
import sys

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
ONTOLOGY = os.path.join(REPO, "internal", "service", "federation", "ontology.go")
MAPPING = os.path.join(REPO, "internal", "service", "federation", "mapping.go")
FED = "./internal/service/federation/"

PROBES = [
    (
        "M1 a v1 document may carry a v2 field",
        ONTOLOGY, FED,
        "case r.Category != nil && version < CoreOntologyV2:",
        "case r.Category != nil && version < -1:",
        "TestAV1DocumentCarryingACategoryIsRefusedByName",
        "the category is dropped on the floor by json.Unmarshal's cousin, which is the silent mis-mapping "
        "the phase's clause names: the tag arrives, in the wrong branch, with nothing saying so",
    ),
    (
        "M2 a document claiming core 2 needs no categories",
        ONTOLOGY, FED,
        "case version >= CoreOntologyV2:",
        "case false:",
        "TestAV2DocumentWithoutCategoriesIsRefused",
        "a peer that declares v2 and sends v1 edges is accepted, so v2 is undetectable in exactly the case "
        "the version exists for",
    ),
    (
        "M3 an empty category counts as one",
        ONTOLOGY, FED,
        "if category == \"\" {",
        "if category == \"\" && false {",
        "TestAnEmptyCategoryIsRefusedAsItsOwnCase",
        "a writer who had the field and left it blank passes, and the edge ends up categorised as nothing "
        "while claiming to be categorised -- a different fix from absent, reported as neither",
    ),
    (
        "M4 two categories for one implication are allowed",
        ONTOLOGY, FED,
        "if _, dup := seen[pair]; dup {",
        "if _, dup := seen[pair]; dup && false {",
        "TestTwoCategoriesForOneImplicationIsRefused",
        "two answers to one question -- which category this implication belongs to -- and the winner is "
        "decided by sort order rather than by the document",
    ),
    (
        "M5 the category field stops being omitempty",
        ONTOLOGY, FED,
        'Category string `json:"category,omitempty"`',
        'Category string `json:"category"`',
        "TestTheCanonicalDigestOfAV1DocumentDidNotChangeWhenCategoryWasAdded",
        "adding a field changes the canonical bytes of every document written before it, so every digest "
        "this instance has ever computed changes and two instances that AGREE report a disagreement",
    ),
    (
        "M6 core 2 reports no vocabulary",
        MAPPING, FED,
        "case CoreOntologyV1, CoreOntologyV2:",
        "case CoreOntologyV1:",
        "TestCoreV2SpeaksTheSameVocabularyAsV1",
        "a v2 peer is told that this instance has no vocabulary at all, which reads as \"nothing crosses\" "
        "-- a claim about the mesh -- rather than as \"not implemented\", a claim about this instance",
    ),
]


def run(pkg, test=None):
    args = ["go", "test", pkg, "-count=1"]
    if test:
        args += ["-run", test]
    return subprocess.run(args, cwd=REPO, capture_output=True, text=True, timeout=900)


def main():
    wanted = [a for a in sys.argv[1:] if not a.startswith("-")]
    total = killed = 0
    for name, path, pkg, old, new, test, why in PROBES:
        if wanted and name.split()[0] not in wanted:
            continue
        total += 1
        original = open(path).read()
        if original.count(old) != 1:
            print(f"SKIP  {name}: the anchor occurs {original.count(old)} times")
            continue
        open(path, "w").write(original.replace(old, new, 1))
        try:
            result = run(pkg, test)
            if "no tests to run" in (result.stdout or ""):
                print(f"HARNESS-BUG  {name}: `go test {pkg} -run {test}` ran NO TEST (exit 0)")
                return 2
            if result.returncode == 0:
                print(f"SURVIVED  {name}\n          {why}")
                print("          the test did not notice, which is a statement about the test")
            elif "build failed" in result.stdout or "[build failed]" in result.stdout:
                print(f"BUILD-ERR  {name} (a lint trip is not a survivor and not a kill)")
                print(result.stdout[-600:])
            else:
                killed += 1
                print(f"KILLED  {name}  ({test})")
        finally:
            open(path, "w").write(original)
        back = run(pkg)
        if back.returncode != 0:
            print(f"RESTORE-FAIL  {name}: {pkg} is red after restoring the file")
            print(back.stdout[-800:])
            return 1
    print(f"\n{killed}/{total} killed, every restore green")
    return 0 if killed == total and total else 1


if __name__ == "__main__":
    sys.exit(main())
