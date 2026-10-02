#!/usr/bin/env python3
"""Mutation gate for SPEC §7.24.8's completion-delta preview.

§7.24.8: "It is a read of §7.7's existing completion score plus a ranking of missing fields by
marginal gain -- not a second, competing definition of completion."

That sentence is a prohibition, and a prohibition is exactly the kind of rule an implementation
satisfies while quietly doing the forbidden thing: keep a parallel "priority" list beside the
weights, and the preview promises a gain the completion bar never delivers. So each mutation
here breaks a different way of becoming that second definition.

A stale anchor returns non-zero BEFORE any count prints -- a probe file that skips its broken
entries under-reports as "all killed", which is the worst outcome because it looks like success.
"""
import pathlib
import subprocess
import sys

ROOT = pathlib.Path("/home/alvaro/code-local/go/stash-box")
SRC = ROOT / "internal/service/completion/delta.go"
ORIGINAL = SRC.read_text()

MUTATIONS = [
    # ---- the "second definition" family: rank by something other than Score's weights
    ("rank-in-declaration-order",
     "\t\tif candidates[i].weight != candidates[j].weight {\n\t\t\treturn candidates[i].weight > candidates[j].weight\n\t\t}\n\t\treturn candidates[i].field < candidates[j].field",
     "\t\treturn false",
     "sorting is stable no-op, so the ranking falls back to Result.Missing's DECLARATION "
     "order. That looks right for performers (whose list is roughly importance-ordered) and "
     "is an accident of where somebody typed the line everywhere else"),

    ("rank-alphabetically",
     "\t\t\treturn candidates[i].weight > candidates[j].weight",
     "\t\t\treturn candidates[i].field > candidates[j].field",
     "the alphabetical tie-break replaces the weight comparison, so every field is ranked by "
     "name -- a second priority definition that happens to compile"),

    ("every-field-equal",
     "\t\t\treturn candidates[i].weight > candidates[j].weight",
     "\t\t\treturn false",
     "all weights compare equal, so the tie-break alone orders the list"),

    # ---- the "suggest unreachable work" family
    ("no-zero-weight-filter",
     "\t\tif w == 0 {\n\t\t\t// Always-expected fields (the name) are not counted either way, so they\n\t\t\t// cannot be \"the next highest-value thing to fill in\".\n\t\t\tcontinue\n\t\t}",
     "\t\tif false {\n\t\t\tcontinue\n\t\t}",
     "always-present fields (name, weight 0) become suggestable. `name` is NOT NULL in every "
     "table, so the edit form would tell a curator to fill in a field that is always filled"),

    ("no-present-filter",
     "\t\tif present[f] {\n\t\t\tcontinue\n\t\t}",
     "\t\tif false {\n\t\t\tcontinue\n\t\t}",
     "present fields are suggested again, so a complete entity still gets a suggestion -- and "
     "Preview's own Next list would name a field the curator just filled"),

    # ---- the delta family
    ("delta-ignores-the-scores",
     "\t\tDeltaPercent: afterResult.Score - before.Score,",
     "\t\tDeltaPercent: 0,",
     "the preview always promises a zero gain, so §7.24.8's headline feature reports nothing"),

    ("delta-absolute-not-relative",
     "\t\tFromPercent:  before.Score,",
     "\t\tFromPercent:  afterResult.Score,",
     "\"from\" and \"to\" both report the post-edit score, so the preview reads 71% -> 71% and "
     "the curator is shown a bar that does not move"),

    ("preview-ignores-the-filled-field",
     "\tafter[filling] = true",
     "\t// the filled field is never recorded, so both scores are of the same state",
     "before and after are the same map, so every preview is a zero-gain no matter what it "
     "fills -- the shape of a second definition that reads nothing about the edit"),

    # ---- the guards
    ("unknown-field-accepted",
     "\tif !filling.ValidFor(entityType) {\n\t\treturn nil, fmt.Errorf(\"%w: field %q is not scored for %q\", ErrUnknownField, filling, entityType)\n\t}",
     # The replacement KEEPS a fmt call, because deleting the only use of fmt leaves the
     # import unused and the mutant does not compile -- which the gate reports as BROKEN
     # rather than as a kill. A probe that cannot compile proves nothing, so it has to be
     # written as a mutant that still builds. Same guard, wrong outcome.
     "\tif false {\n\t\treturn nil, fmt.Errorf(\"%w: %v\", ErrUnknownField, entityType)\n\t}",
     "a caller passing FieldRegex for a scene previews against the wrong weights, which is "
     "the same mistake §7.24.1's write path refuses for quests"),

    ("unknown-entity-type-accepted",
     "\tfields, err := Fields(entityType)\n\tif err != nil {\n\t\treturn nil, err\n\t}",
     "\tfields, err := Fields(entityType)\n\tif err != nil {\n\t\treturn nil, nil\n\t}",
     "a misspelled entity type returns no suggestions instead of an error, so a typo becomes "
     "an empty panel rather than a visible bug"),

    ("empty-returns-empty-slice",
     "\tif len(candidates) == 0 {\n\t\treturn nil, nil\n\t}",
     "\tif len(candidates) == 0 {\n\t\treturn []Field{}, nil\n\t}",
     "nil vs []Field{} -- both len 0 to a caller, but a JSON API emits null for one and [] for "
     "the other, and the edit form renders them differently"),
]


def run_tests():
    proc = subprocess.run(
        ["go", "test", "./internal/service/completion/", "-count=1"],
        cwd=ROOT, capture_output=True, text=True)
    out = proc.stdout + proc.stderr
    if "build failed" in out or "cannot use" in out or "undefined" in out:
        return "COMPILE_ERROR", out
    return proc.returncode == 0, out


def main():
    survivors, broken = [], []
    for label, find, replace, why in MUTATIONS:
        if find not in ORIGINAL:
            print("BROKEN ANCHOR  %s: no longer in source" % label)
            broken.append(label)
            continue
        SRC.write_text(ORIGINAL.replace(find, replace, 1))
        try:
            ok, out = run_tests()
        finally:
            SRC.write_text(ORIGINAL)
        if ok == "COMPILE_ERROR":
            print("BROKEN PROBE  %s: the mutant does not compile\n%s" % (label, out[-400:]))
            broken.append(label)
        elif ok:
            print("SURVIVED  %s: %s" % (label, why))
            survivors.append((label, why))
        else:
            print("killed    %s" % label)

    total = len(MUTATIONS)
    print()
    if broken:
        print("%d broken -- the probe file is stale, fix it before reading any kill count" % len(broken))
        return 1
    if survivors:
        print("%d survived of %d -- a real gap, not a masked one" % (len(survivors), total))
        return 1
    print("all %d mutations killed" % total)
    return 0


if __name__ == "__main__":
    sys.exit(main())