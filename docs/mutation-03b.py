#!/usr/bin/env python3
"""The 03b mutation sweep, run for real.

The plan (docs/plan/feature-03b-content-access-and-vanguard-weighting.md, Step 1.3
and Step 2.3) names eight mutations and says every one must be killed. This applies
each one, runs the suite, records the result, and restores -- in a single process,
so a crash cannot leave a mutation applied in the tree.

A SURVIVED line is the finding. It means no test covers that line, which is
different from "a test is missing": the line is dead, redundant, or re-implemented
in the test rather than driven by it.

    python3 docs/mutation-03b.py
"""
import pathlib, re, shutil, subprocess, sys

REPO = pathlib.Path("/home/alvaro/code-local/go/stash-box")
CA = REPO / "internal/service/trust/contentaccess.go"
WT = REPO / "internal/service/elo/weight.go"
PKGS = "./internal/service/trust/ ./internal/service/elo/"

ORIG = {p: p.read_text() for p in (CA, WT)}
results = []


def restore():
    for p, txt in ORIG.items():
        p.write_text(txt)


def run_mutation(label, target, snip):
    """Apply snip to target, run the suite, restore. Returns (killed, detail)."""
    restore()
    before = target.read_text()
    new = snip(before)
    if new == before:
        # The anchor did not match. Reporting this as SURVIVED would be the exact
        # lie this file exists to prevent -- a mutation that never applied proves
        # nothing, in either direction.
        results.append((label, "NO-OP", "the anchor did not match; nothing was mutated"))
        return None
    target.write_text(new)
    try:
        p = subprocess.run(f"go test -count=1 {PKGS}", shell=True, cwd=REPO,
                           capture_output=True, text=True, timeout=900)
        out = p.stdout + p.stderr
        if p.returncode == 0:
            results.append((label, "SURVIVED",
                            "suite stayed GREEN -- no test covers this line"))
            return False
        n = len(re.findall(r"^--- FAIL", out, re.M))
        results.append((label, "KILLED", f"{n} failing test(s)"))
        return True
    finally:
        restore()


# --- Step 1.3: the content access gate ---------------------------------------
M = [
    ("1.1 invert the level check",
     CA, lambda d: d.replace("r.Level < LevelContentViewing", "r.Level >= LevelContentViewing")),
    ("1.2 make the level check pass always",
     CA, lambda d: d.replace(
         "if r.Level < LevelContentViewing && !r.IsVanguard && !r.AdminOverride {",
         "if false {")),
    ("1.3 drop the opt-in condition",
     CA, lambda d: d.replace("if !r.OptedIn {\n\t\tfail(ReasonOptIn)\n\t}\n\n", "")),
    ("1.4 drop the contribution threshold",
     CA, lambda d: d.replace(
         "if r.ContributionScore < r.MinContribution {\n\t\tfail(ReasonContribution)\n\t}\n\n", "")),
    ("1.5 drop the content-terms condition",
     CA, lambda d: d.replace("if !r.TermsAccepted {\n\t\tfail(ReasonTerms)\n\t}\n\n", "")),
    ("1.6 drop the abuse-flag condition",
     CA, lambda d: d.replace("if r.Flagged {\n\t\tfail(ReasonFlagged)\n\t}\n\n", "")),
    ("1.7 let the override bypass the opt-in too",
     CA, lambda d: d.replace(
         "if r.Level < LevelContentViewing && !r.IsVanguard && !r.AdminOverride {",
         "if r.Level < LevelContentViewing {")),
    ("1.8 return the LAST failure instead of the first",
     CA, lambda d: d.replace("Reason: failed[0]", "Reason: failed[len(failed)-1]")),
    ("1.9 allow an anonymous user through",
     CA, lambda d: d.replace("if r.Anonymous {\n\t\treturn fail(ReasonAnonymous)\n\t}",
                             "if r.Anonymous && false {\n\t\treturn fail(ReasonAnonymous)\n\t}")),
]

# --- Step 2.3: the vanguard elo weight ---------------------------------------
def _cap(d):
    """The contribution cap is an if-clamp on `scaled`, not a math.Min call.

    The first version of this file searched for `math.Min(` and reported NO-OP --
    which is the correct outcome for a mutation that never applied, and exactly
    why the sweep distinguishes NO-OP from SURVIVED rather than lumping them.
    """
    return d.replace("\tif scaled > 1.0 {\n\t\tscaled = 1.0\n\t}",
                     "\tif scaled > 999999.0 {\n\t\tscaled = 999999.0\n\t}", 1)

M += [
    ("2.1 remove the contribution cap",
     WT, _cap),
    # clampVoterWeight is a `switch`, not a chain of ifs -- the first version of
    # this mutation searched for an `if` and matched nothing.
    ("2.5 remove the MinVoterWeight floor",
     WT, lambda d: d.replace("\tcase weight < MinVoterWeight:\n\t\treturn MinVoterWeight\n", "", 1)),
    ("2.6 remove the MaxVoterWeight ceiling",
     WT, lambda d: d.replace("\tcase weight > MaxVoterWeight:\n\t\treturn MaxVoterWeight\n", "", 1)),
    # The NaN case has a comment claiming NaN "would sail through a naive bounds
    # check". That is a claim about behaviour, so it gets a mutation like any other.
    ("2.7 let NaN through the clamp",
     WT, lambda d: d.replace("\tcase math.IsNaN(weight):", "\tcase false && math.IsNaN(weight):", 1)),
    ("2.2 neutralise the vanguard multiplier",
     WT, lambda d: d.replace("const vanguardMultiplier = 1.25",
                             "const vanguardMultiplier = 1.0")),
    ("2.3 make VoterWeight ignore isVanguard",
     WT, lambda d: re.sub(r"func VoterWeight\(level int, isVanguard bool, contributionScore int64\) float64 \{",
                          "func VoterWeight(level int, isVanguard bool, contributionScore int64) float64 {\n\tisVanguard = false",
                          d, count=1)),
    ("2.4 make VoterWeight ignore contributionScore",
     WT, lambda d: re.sub(r"func VoterWeight\(level int, isVanguard bool, contributionScore int64\) float64 \{",
                          "func VoterWeight(level int, isVanguard bool, contributionScore int64) float64 {\n\tcontributionScore = 0",
                          d, count=1)),
]

for label, target, snip in M:
    run_mutation(label, target, snip)

restore()
# prove the tree is back exactly as it was
for p, txt in ORIG.items():
    assert p.read_text() == txt, f"RESTORE FAILED for {p}"

w = max(len(l) for l, _, _ in results) + 2
print("=" * 74)
print("03b MUTATION SWEEP -- applied, killed, restored")
print("=" * 74)
for label, status, detail in results:
    print(f"{label:<{w}} [{status:^8}] {detail}")
print("=" * 74)

killed = [r for r in results if r[1] == "KILLED"]
survived = [r for r in results if r[1] == "SURVIVED"]
noops = [r for r in results if r[1] == "NO-OP"]
print(f"{len(killed)} killed, {len(survived)} survived, {len(noops)} no-op")
if survived or noops:
    print("\nNOT every mutation was killed. For each survivor, one of these is true:")
    print("  - the guarded line is DEAD (nothing calls it)")
    print("  - the guarded line is REDUNDANT (something else already prevents it)")
    print("  - the test re-implements the logic instead of DRIVING the line")
    print("A survivor is not automatically 'a missing test'.")
sys.exit(1 if (survived or noops) else 0)
