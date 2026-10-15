#!/usr/bin/env python3
"""Mutation battery for the Hydrus export (plan phase 13 slice 13c).

Ten probes, one per rule the slice is worth having. Each names the test it must redden, and the battery FAILS
if a probe that should be caught is not: a mutation that does not build is not a verdict, and a probe whose
named test is not in the run filter is a FALSE SURVIVOR (both lessons were paid for in earlier slices, where
a `-run` filter that matched nothing reported "ok").

M9 is deliberately NOT in this slice's own files. The claim that a denied record is absent from the export is
only worth anything if the absence comes from the instance's one gate rather than from a check written next to
the new feature -- so the probe neuters the gate at the loader, and the export test is what has to notice.

The integration probes need the database the integration suite uses. Run:

    export POSTGRES_DB='postgres:smoke_pw@127.0.0.1:55434/stash-box-test?sslmode=disable'
    python3 scripts/mutate-hydrus-export.py [M1 M2 ...]
"""

import os
import subprocess
import sys

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

SIDECAR = "internal/hydrus/sidecar.go"
EXPORT = "internal/api/resolver_export.go"
LOADER = "internal/service/scene/service.go"

IT = ["go", "test", "-tags=integration", "-count=1", "-v", "-p", "1", "./internal/api/"]
UNIT = ["go", "test", "-count=1", "-v", "./internal/hydrus/"]


def it(name):
    return IT + ["-run", name]


def unit(name):
    return UNIT + ["-run", name]


MUTATIONS = [
    (
        "M1 the record's own vocabulary is exported without a namespace",
        SIDECAR,
        "\t\t\town = append(own, TagSelf+clean(tag))",
        "\t\t\town = append(own, clean(tag))",
        [unit("TestEveryTagIsNamespacedAndTheRecordsOwnAreSorted")],
        "'tag:alpha' -- asserted as a prefix, because an unnamespaced tag cannot be filtered by an importer",
    ),
    (
        "M2 the title is routed into the tag list",
        SIDECAR,
        "\t\tnotes = append(notes, NoteTitle+clean(record.Title))",
        "\t\ttags = append(tags, NoteTitle+clean(record.Title))",
        [unit("TestTheTitleAndDescriptionAreNotesRatherThanTags")],
        "the file would get a tag for every sentence in its name",
    ),
    (
        "M3 tag text keeps its line breaks",
        SIDECAR,
        "\ttext = strings.ReplaceAll(text, \"\\r\\n\", \" \")\n"
        "\ttext = strings.ReplaceAll(text, \"\\n\", \" \")\n"
        "\ttext = strings.ReplaceAll(text, \"\\r\", \" \")\n"
        "\treturn strings.Join(strings.Fields(text), \" \")",
        "\treturn text",
        [unit("TestATagCannotCarryANewlineIntoSomebodyElsesDatabase")],
        "one tag must not arrive as two, one of which is a stray word",
    ),
    (
        "M4 the record's own tags are emitted in the order the loader returned them",
        SIDECAR,
        "\tsort.Strings(own)",
        # A no-op sort rather than deleting the call: removing it would leave `sort` imported and unused,
        # and a package that does not build is not a verdict (the lesson from the earlier slices).
        "\tsort.SliceStable(own, func(i, j int) bool { return false })",
        [unit("TestTheSameRecordRendersTheSameBytes")],
        "the same record must render the same bytes, whatever order its tags came back in",
    ),
    (
        "M5 the addresses are sorted, so the record's own order is lost",
        SIDECAR,
        "\turls := make([]string, 0, len(record.URLs))",
        "\turls := make([]string, 0, len(record.URLs))\n\tsort.Strings(record.URLs)",
        [unit("TestTheSameRecordRendersTheSameBytes")],
        "the order of a record's addresses is the record's, not the export's",
    ),
    (
        "M6 an empty list is emitted as null",
        SIDECAR,
        "\turls := make([]string, 0, len(record.URLs))",
        "\tvar urls []string",
        [unit("TestASidecarIsTheShapeHydrusDocuments")],
        "a sidecar whose `urls` is null is a file an importer has to guess about",
    ),
    (
        "M7 the record's addresses are dropped from its entry",
        EXPORT,
        # Emptying the map rather than writing `nil` into the entry: dropping the field leaves the loader's
        # result unused, and a package that does not build is not a verdict.
        "\turls := r.urlsByScene(ctx, present)",
        "\turls := map[uuid.UUID][]string{}",
        [it("TestAnExportedAddressIsSettledByThisInstancesOwnIngest")],
        "the one thing a round trip cannot survive losing: the exported address IS the round trip",
    ),
    (
        "M8 the bound is not enforced",
        EXPORT,
        "\tif len(ids) > maxExportRecords {",
        "\tif len(ids) > maxExportRecords+1000 {",
        [it("TestAnExportIsBoundedAndTheBoundIsNamed")],
        "an export is a rendering of records the caller already has",
    ),
    (
        "M9 nothing is reported as omitted",
        EXPORT,
        "\tomitted := make([]uuid.UUID, 0)\n"
        "\tfor _, id := range ids {\n"
        "\t\tif _, ok := document[id.String()]; !ok {\n"
        "\t\t\tomitted = append(omitted, id)\n"
        "\t\t}\n"
        "\t}",
        "\tomitted := make([]uuid.UUID, 0)",
        [it("TestADeniedRecordIsAbsentAndNamedInTheEnvelope")],
        "the envelope is where a caller learns a record was left out",
    ),
    (
        # Neutered at the LOADER, not next to the new feature: the absence has to come from the one gate.
        "M10 the denylist gate stops denying",
        LOADER,
        # Two call sites carry the bare statement, so the anchor is the one under the loader's own comment.
        "\t// slot the loader already uses for a missing id -- \"not found\" and \"not served\" are one answer.\n"
        "\tdenied, err := s.deniedSceneIDs(ctx, ids)",
        "\tdenied, err := s.deniedSceneIDs(ctx, ids)\n\tdenied = map[uuid.UUID]struct{}{}",
        [it("TestADeniedRecordIsAbsentAndNamedInTheEnvelope")],
        "a taken-down record must be absent from the export by the same code that hides it from a search",
    ),
]


def run(cmd):
    proc = subprocess.run(cmd, cwd=REPO, capture_output=True, text=True, timeout=900)
    return proc.returncode, proc.stdout + proc.stderr


def verdict(out):
    # A panic counts as RED: it is a real failure mode of a mutated resolver, and a crash reported as
    # "survived" would be the same mistake this battery exists to catch.
    if "build failed" in out or "[build failed]" in out or "declared and not used" in out:
        return "BROKEN"
    if "--- FAIL" in out or "panic:" in out or "\nFAIL" in out:
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
    broken = [n for n, v, _ in results if v in ("BROKEN", "ANCHOR-MISS")]
    print(f"\n{killed}/{len(results)} killed" + (f"; NOT VERDICTS: {broken}" if broken else ""))
    return 0 if killed == len(results) else 1


if __name__ == "__main__":
    sys.exit(main())
