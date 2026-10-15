#!/usr/bin/env python3
"""Mutation battery for the rate limiter (plan phase 13 slice 13d).

Twelve probes, one per rule the slice is worth having. Each names the test it must redden, and the battery
FAILS if a probe that should be caught is not: a mutation that does not build is not a verdict, and a probe
whose named test is not in the run filter is a FALSE SURVIVOR (both lessons were paid for in earlier slices,
where a `-run` filter that matched nothing reported "ok").

M12 is the regression this slice's own test found: the first version of the limiter made room in its bounded
table on EVERY request, so at the cap every request evicted somebody -- which on a busy instance means the
table spends its time throwing away the credentials it is counting. The probe puts that back.

No database needed.

    python3 scripts/mutate-ratelimit.py [M1 M2 ...]
"""

import os
import subprocess
import sys

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

LIMITER = "internal/ratelimit/limiter.go"
MIDDLEWARE = "internal/ratelimit/middleware.go"
TEST = ["go", "test", "-count=1", "-v", "./internal/ratelimit/"]


def suite(name):
    return TEST + ["-run", name]


MUTATIONS = [
    (
        "M1 the bucket never refills",
        LIMITER,
        "\tif elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {",
        "\tif elapsed := now.Sub(b.last).Seconds(); elapsed < 0 {",
        [suite("TestTheBurstIsSpentAndThenRefillsAtTheStatedRate")],
        "the sustained rate is the half of a limit a burst-only test does not see",
    ),
    (
        "M2 every request refills the bucket to full",
        LIMITER,
        "\t\tb.tokens = min(float64(l.config.Burst), b.tokens+elapsed*l.rate)",
        "\t\tb.tokens = float64(l.config.Burst)",
        [suite("TestTheBurstIsSpentAndThenRefillsAtTheStatedRate")],
        "a limiter that refills on demand is a limiter that limits nothing",
    ),
    (
        "M3 the burst is not a cap",
        LIMITER,
        "\t\tb.tokens = min(float64(l.config.Burst), b.tokens+elapsed*l.rate)",
        "\t\tb.tokens = b.tokens + elapsed*l.rate",
        [suite("TestTheBurstIsSpentAndThenRefillsAtTheStatedRate")],
        "a minute's refill must not exceed the burst",
    ),
    (
        "M4 the refusal says retry immediately",
        LIMITER,
        "\treturn Decision{Allowed: false, Limit: limit, Remaining: 0, RetryAfter: wait.Truncate(time.Second)}",
        # `wait - wait` rather than dropping the field: the local would be unused and a package that does
        # not build is not a verdict (the same correction the earlier batteries needed twice).
        "\treturn Decision{Allowed: false, Limit: limit, Remaining: 0, RetryAfter: wait - wait}",
        [suite("TestRetryAfterIsNeverZeroAndNeverShorterThanTheWait")],
        "a Retry-After of zero invites a retry that is refused again, which reads as a broken limit",
    ),
    (
        "M5 a negative elapsed time mints tokens",
        LIMITER,
        "\t\tb.last = now\n\t}",
        "\t\tb.last = now\n\t}\n\tb.tokens = min(float64(l.config.Burst), b.tokens+1)",
        [suite("TestAClockGoingBackwardsAddsNoTokens")],
        "a corrected clock must not be an hour of credit",
    ),
    (
        "M6 the map is unbounded",
        LIMITER,
        "\t\tif len(l.buckets) >= l.maxBuckets {",
        "\t\tif len(l.buckets) >= l.maxBuckets*1000000 {",
        [suite("TestTheMapIsBounded")],
        "how much memory this process spends must not be decided by whoever sends the most credentials",
    ),
    (
        "M7 eviction forgets the most recently used credential",
        LIMITER,
        "\t\tif oldestKey == \"\" || b.last.Before(oldest) {",
        "\t\tif oldestKey == \"\" || b.last.After(oldest) {",
        [suite("TestCredentialsThatRefilledAreForgottenBeforeOnesStillSpending")],
        "the least recently used is the one that has been idle longest",
    ),
    (
        "M8 a credential that refilled is never swept",
        LIMITER,
        "\t\tif b.tokens+now.Sub(b.last).Seconds()*l.rate >= full {",
        "\t\tif b.tokens+now.Sub(b.last).Seconds()*l.rate >= full*1000 {",
        [suite("TestCredentialsThatRefilledAreForgottenBeforeOnesStillSpending")],
        "the first pass is what empties the table without costing anybody a bucket",
    ),
    (
        "M9 zero means a limit of zero requests",
        LIMITER,
        "\tif config.Disabled() {\n\t\treturn nil\n\t}",
        "\tif config.Disabled() && false {\n\t\treturn nil\n\t}",
        [suite("TestZeroMeansNoLimit")],
        "zero is the off switch, and a disabled limiter is nil rather than a limiter that refuses everything",
    ),
    (
        "M10 the refusal is not the shape this API returns",
        MIDDLEWARE,
        "\t\t\t\tbody := refusedBody(decision)",
        "\t\t\t\tbody := []byte(\"slow down\")",
        [suite("TestARefusalIsTheSameErrorShapeAsEveryOtherError")],
        "a client already parsing {\"errors\":[...]} has no second path for a bare string",
    ),
    (
        "M11 the limit is only stated when it is exceeded",
        MIDDLEWARE,
        "\t\t\tw.Header().Set(\"X-RateLimit-Limit\", strconv.Itoa(decision.Limit))\n"
        "\t\t\tw.Header().Set(\"X-RateLimit-Remaining\", strconv.Itoa(decision.Remaining))\n\n"
        "\t\t\tif !decision.Allowed {",
        "\t\t\tif !decision.Allowed {\n"
        "\t\t\t\tw.Header().Set(\"X-RateLimit-Limit\", strconv.Itoa(decision.Limit))\n"
        "\t\t\t\tw.Header().Set(\"X-RateLimit-Remaining\", strconv.Itoa(decision.Remaining))",
        [suite("TestARefusalIsTheSameErrorShapeAsEveryOtherError")],
        "a client that only learns the limit when it is refused will be refused again",
    ),
    (
        # The regression the slice's own test found, put back on purpose.
        "M12 room is made on every request, not only for a new credential",
        LIMITER,
        "\tb, ok := l.buckets[key]\n\tif !ok {\n\t\tif len(l.buckets) >= l.maxBuckets {\n\t\t\tl.makeRoomLocked(now)\n\t\t}\n",
        "\tif len(l.buckets) >= l.maxBuckets {\n\t\tl.makeRoomLocked(now)\n\t}\n\n"
        "\tb, ok := l.buckets[key]\n\tif !ok {\n",
        [suite("TestCredentialsThatRefilledAreForgottenBeforeOnesStillSpending")],
        "at the cap, a caller who is already tracked must not cost another caller its bucket",
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
    return 0 if killed == len(results) else 1


if __name__ == "__main__":
    sys.exit(main())
