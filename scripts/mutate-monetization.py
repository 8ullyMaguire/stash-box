#!/usr/bin/env python3
"""Mutation battery for the monetization posture (plan phase 15, spec §18).

Fourteen probes, one per rule this slice is worth having. Each names the test it must redden, and the battery
FAILS if a probe that should be caught is not: a mutation that does not build is not a verdict, and a probe
whose named test is not in the run filter is a FALSE SURVIVOR (both lessons were paid for in earlier slices,
where a `-run` filter that matched nothing reported "ok").

M4 IS THE REGRESSION THIS SLICE'S OWN TEST FOUND. The first version of `RequireStream` answered nil for any
stream the tree could sell, without asking whether THIS OPERATOR had switched it on -- so a comped member was
granted the premium API limit on an instance whose only configured stream was `ads`. The entitlement test for
"a comped member is free while the premium stream is off" is what failed, and the probe puts the defect back.

M13 is the defect the TIER design had to avoid rather than one it had: with two limits, a sweep that measures
every bucket against the FREE burst discards buckets that are still spending -- silently, because the premium
caller simply gets a full bucket again.

No database needed: both suites are unit tests.

    python3 scripts/mutate-monetization.py [M1 M2 ...]
"""

import os
import subprocess
import sys

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

MONETIZATION = "internal/service/monetization/monetization.go"
ALWAYSFREE = "internal/service/monetization/alwaysfree.go"
LIMITER = "internal/ratelimit/limiter.go"

MON = ["go", "test", "-count=1", "-v", "./internal/service/monetization/"]
RL = ["go", "test", "-count=1", "-v", "./internal/ratelimit/"]


def suite(target, name):
    return target + ["-run", name]


MUTATIONS = [
    (
        "M1 the switch is ignored: the instance always charges",
        MONETIZATION,
        "\treturn config.GetMonetizationEnabled()",
        "\treturn true",
        [suite(MON, "TestAStreamCannotBeOnWhileTheSwitchIsOff")],
        "a stream on with the key false must be refused, not reported as the posture",
    ),
    (
        "M2 the gate stops consulting §18.2 before refusing",
        ALWAYSFREE,
        "\t\tif c.Name == name {",
        "\t\tif false && c.Name == name {",
        [suite(MON, "TestTheGateRefusesEveryAlwaysFreeCapability")],
        "the always-free list has to be the FIRST question the gate asks",
    ),
    (
        "M3 RequireStream does not care whether this operator sells the stream",
        MONETIZATION,
        "\tif !StreamOn(name) {\n"
        "\t\treturn fmt.Errorf(\n"
        '\t\t\t"%w: the stream %q exists on this tree, and this instance is not selling it -- it is not in "+\n'
        '\t\t\t\t"monetization_streams", ErrStreamOff, name)\n'
        "\t}",
        "\t_ = s",
        [suite(MON, "TestACompedMembersKeyIsPremiumWhileTheStreamIsOn")],
        "the defect the entitlement test found: implemented is not the same fact as switched on",
    ),
    (
        "M4 an unimplemented stream is reported as sellable",
        MONETIZATION,
        "\tif !s.implemented {\n"
        '\t\treturn fmt.Errorf("%w: the stream %q would need %s", ErrStreamNotImplemented, name, s.because)\n'
        "\t}",
        "\t_ = s",
        [suite(MON, "TestAnUnimplementedStreamIsRefusedWithTheWorkItWouldTake")],
        "a stream with no code behind it must say what building it would take",
    ),
    (
        "M5 an unknown stream name is accepted",
        MONETIZATION,
        "\tif !ok {\n"
        '\t\treturn fmt.Errorf("%w: %q (this version knows: %s)",\n'
        "\t\t\tErrUnknownStream, name, strings.Join(KnownStreams(), \", \"))\n"
        "\t}",
        "\tif !ok && false {\n"
        '\t\treturn fmt.Errorf("%w: %q", ErrUnknownStream, name)\n'
        "\t}",
        [suite(MON, "TestAnUnknownStreamIsRefusedWithTheKnownList")],
        "a typo in the configuration must be a named error listing the streams, not a stream that does nothing",
    ),
    (
        "M6 startup accepts a stream while the switch is off",
        MONETIZATION,
        # The longer anchor is not decoration: `len(configured) > 0` also appears in PostureOf, where it answers a
        # different question (does the file WANT streams). A mutation that hits the wrong one is not a verdict,
        # and the harness reports a 2-hit anchor as ANCHOR-MISS rather than guessing.
        "\tif !Enabled() {\n\t\tif len(configured) > 0 {\n\t\t\treturn fmt.Errorf(",
        "\tif !Enabled() {\n\t\tif len(configured) < 0 {\n\t\t\treturn fmt.Errorf(", 
        [suite(MON, "TestAStreamCannotBeOnWhileTheSwitchIsOff")],
        "the process must refuse a configuration that contradicts itself rather than pick a half of it",
    ),
    (
        "M7 startup accepts a premium limit that is not higher",
        MONETIZATION,
        "\t\tif premium.RequestsPerMinute <= free.RequestsPerMinute || premium.Burst <= free.Burst {",
        "\t\tif premium.RequestsPerMinute <= free.RequestsPerMinute && premium.Burst <= free.Burst {",
        [suite(MON, "TestAPremiumLimitThatIsNotHigherIsRefused")],
        "a \"higher limit\" that is not higher in EITHER half is a promise nothing honours",
    ),
    (
        "M8 the entitlement ignores whether the stream is on",
        MONETIZATION,
        "\tif err := Gate(FeaturePremiumAPITier); err != nil {\n\t\treturn false\n\t}",
        "\t_ = Gate(FeaturePremiumAPITier)",
        [suite(MON, "TestACompedMembersKeyIsPremiumWhileTheStreamIsOn")],
        "with the stream off nobody is premium, which is what makes the off-posture reachable for everybody",
    ),
    (
        "M9 the limiter is handed a premium limit whether or not the stream is on",
        MONETIZATION,
        "\tif err := RequireStream(StreamPremiumSubscription); err != nil {\n"
        "\t\treturn free, ratelimit.Config{}\n"
        "\t}",
        "\tif err := RequireStream(StreamPremiumSubscription); err != nil && false {\n"
        "\t\treturn free, ratelimit.Config{}\n"
        "\t}",
        [suite(MON, "TestThereIsNoSecondLimitUnlessTheStreamIsOn")],
        "the startup line must not state a premium tier on an instance where nobody can be premium",
    ),
    (
        "M10 every member is premium",
        MONETIZATION,
        "\treturn contains(config.GetMonetizationPremiumMembers(), member)",
        # The blank assignments rather than a bare `return true`: dropping the call would leave `member` unused
        # and the package would not build, which is not a verdict. (`true && contains(...)` was the first
        # version of this mutation and it SURVIVED, because `true && X` is `X` -- a mutation that changes
        # nothing is not a probe, it is a spelling of the original.)
        "\t_, _ = member, config.GetMonetizationPremiumMembers()\n\treturn true",
        [suite(MON, "TestACompedMembersKeyIsPremiumWhileTheStreamIsOn")],
        "the comp list is the grant: a member the operator did not comp is not premium",
    ),
    (
        "M11 the limiter's predicate is ignored: nobody is premium",
        LIMITER,
        "\tif l.premiumCredential(key) {\n\t\treturn TierPremium\n\t}",
        "\tif false && l.premiumCredential(key) {\n\t\treturn TierPremium\n\t}",
        [suite(RL, "TestAPremiumCredentialCountsAgainstThePremiumLimitAndAFreeOneDoesNot")],
        "the tier decision has to reach the limit the caller is counted against",
    ),
    (
        "M12 buckets are not keyed by tier",
        LIMITER,
        '\tkeyAt := tier + "\\x00" + key',
        "\tkeyAt := key",
        [suite(RL, "TestATierChangeStartsAFreshBucket")],
        "tokens counted against one limit answer a different question when the limit changes",
    ),
    (
        "M13 the sweep measures a bucket against the free limit",
        LIMITER,
        "\t\tif b.tokens+now.Sub(b.last).Seconds()*b.rate >= b.burst {",
        "\t\tif b.tokens+now.Sub(b.last).Seconds()*b.rate >= float64(l.config.Burst) {",
        [suite(RL, "TestTheSweepMeasuresEachBucketAgainstItsOwnLimit")],
        "a premium bucket that is still spending must not be discarded because it looks full to the free limit",
    ),
    (
        "M14 the bucket's refill rate is a constant instead of its own limit",
        LIMITER,
        "\t\trate:   float64(config.RequestsPerMinute) / 60.0,",
        "\t\trate:   1.0,",
        [suite(RL, "TestAPremiumBucketRefillsAtItsOwnRate")],
        "the sustained rate is half of what a limit means, and it belongs to the tier the bucket counts against",
    ),
]


def run(cmd):
    proc = subprocess.run(cmd, cwd=REPO, capture_output=True, text=True, timeout=900)
    return proc.returncode, proc.stdout + proc.stderr


def verdict(out):
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

    # The tree has to be green after the battery, or a mutation was left behind -- and a battery whose own
    # restore is unverified is the way a later verdict becomes nonsense.
    _, out = run(["go", "test", "-count=1", "./internal/service/monetization/", "./internal/ratelimit/"])
    print("RESTORED TREE:", "green" if "FAIL" not in out else "RED -- A MUTATION IS STILL IN THE TREE")
    return 0 if killed == len(results) and "FAIL" not in out else 1


if __name__ == "__main__":
    sys.exit(main())
