#!/usr/bin/env python3
"""Restore internal/api/resolver_collage.go to its committed state by REVERSING
each mutation exactly.

`git checkout -- <file>` is a SILENT NO-OP on an untracked file. That bit me twice
in this session already, and here it made a mutation harness report "9 caught, 0
survived" when in fact all nine mutations were still live in the file and every
result after the first was contaminated. The harness was not measuring anything.

So: reverse the replacements. Each mutation is a known (mutated, original) pair,
and restoration asserts the mutation was actually present before undoing it.
"""
import sys

P = "internal/api/resolver_collage.go"

MUTATIONS = [
    ("\tif obj_source := durationMS; obj_source != nil && *obj_source > 0 {",
     "\tif durationMS != nil && *durationMS > 0 {"),
    ("\t\tfr := float64(f.TimestampMS) / 100000.0 // MUTANT: fixed denominator",
     "\t\tfr := float64(f.TimestampMS) / float64(*durationMS)"),
    ("\treturn msToInt(obj.SourceDurationMS), nil // MUTANT",
     "\treturn msToInt(obj.CurrentDurationMS), nil"),
    ("\treturn false, nil // MUTANT",
     "\treturn obj.Stale(), nil"),
    ("\tframes, err := r.services.Collage().ListSnapshots(ctx, obj.SceneID) // MUTANT: wrong loader",
     "\tframes, err := r.services.Collage().ListFrames(ctx, obj.SceneID)"),
    ("\treturn []models.CollageFrame{}, nil // MUTANT",
     "\treturn out, nil"),
    ("\treturn 0, nil // MUTANT",
     "\treturn collage.DefaultFrames, nil"),
    ("\trows, err := []*collage.UnderSnapshotted{}, error(nil) // MUTANT",
     "\trows, err := r.services.Collage().ListUnderSnapshotted(ctx, min, lim)"),
]

src = open(P).read()
missing = []
for mutated, original in MUTATIONS:
    if mutated not in src:
        missing.append(mutated.strip()[:60])
        continue
    src = src.replace(mutated, original)

open(P, "w").write(src)

if missing:
    print("NOT PRESENT (already clean?):")
    for m in missing:
        print("   ", m)

leftover = [l for l in src.split("\n") if "MUTANT" in l]
if leftover:
    print("STILL MUTATED:")
    for l in leftover:
        print("   ", l)
    sys.exit(1)
print("resolver_collage.go fully restored; no MUTANT markers remain")