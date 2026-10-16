#!/usr/bin/env python3
"""Mutation battery for the sync's durable watermark (docs/plan/feature-sync-service.md, step 2).

    POSTGRES_DB='postgres:smoke_pw@127.0.0.1:55434/stash-box-test?sslmode=disable' \
        python3 scripts/mutate-sync-watermark.py [M1 M2 .. M8]

WHY THIS ONE NEEDS A REAL DATABASE. Two of the three properties the store guarantees -- that a mark never moves
backwards, and that two sources do not share one -- are STATEMENTS (`ON CONFLICT ... WHERE`, and the composite
key). A fake with the same shape would assert what the test file already believes, so the probes below run the
integration build against Postgres, the same way the tests do.

Same procedure as the other batteries: each probe names the ONE test that must notice, the whole package is
re-run after each restore, and a run that says "no tests to run" is a HARNESS BUG rather than a survivor --
`go test -run` exits ZERO when the package does not contain the named test.
"""

import os
import subprocess
import sys

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
STORE = os.path.join(REPO, "internal", "sdbimport", "watermark_store.go")
PKG = "./internal/sdbimport/"

PROBES = [
    (
        "M1 the source is dropped from the read, so a second source reads the first one's position",
        "SELECT watermark FROM sync_watermarks WHERE source = $1 AND entity = $2",
        "SELECT watermark FROM sync_watermarks WHERE $1 IS NOT NULL AND entity = $2",
        "TestTwoSourcesKeepSeparateMarks",
        "the second source reads the first one's mark and silently skips everything older than it -- a whole "
        "source imported as nothing",
    ),
    (
        "M2 a mark may move backwards",
        "\t\t   SET watermark = EXCLUDED.watermark, updated_at = now()\n\t\t WHERE sync_watermarks.watermark < EXCLUDED.watermark",
        "\t\t   SET watermark = EXCLUDED.watermark, updated_at = now()",
        "TestAMarkNeverMovesBackwards",
        "a stale writer -- a manual run beside a timer run, or a crash and a retry -- sends the next walk past "
        "records that were never written, and nothing ever goes back",
    ),
    (
        "M3 a near-miss entity name gets its own row",
        "\tif !IsSyncEntity(entity) {\n\t\treturn false, fmt.Errorf(\"%w: %q (the sync walks %s)\", ErrUnknownSyncEntity, entity, strings.Join(SyncEntities, \", \"))\n\t}",
        "\tif false {\n\t\treturn false, fmt.Errorf(\"%w: %q\", ErrUnknownSyncEntity, entity)\n\t}",
        "TestAnUnknownEntityIsRefusedAndWritesNothing",
        "`performer` or `performers ` gets a row that no walk reads, while the real entity walks in full on "
        "every run -- a stale row in the table looking like state",
    ),
    (
        "M4 'never synced' is reported as 'synced to the beginning of time'",
        "\tif errors.Is(err, pgx.ErrNoRows) {\n\t\treturn time.Time{}, false, nil\n\t}",
        "\tif errors.Is(err, pgx.ErrNoRows) {\n\t\treturn time.Time{}, true, nil\n\t}",
        "TestAnEntityWithNoMarkIsAbsentRatherThanZero",
        "a first run believes it has already seen everything and stops at the zero time",
    ),
    (
        "M5 a walk that read nothing saves a position",
        "\tif at.IsZero() {\n\t\treturn false, errors.New(",
        "\tif at.IsZero() && false {\n\t\treturn false, errors.New(",
        "TestAZeroMarkIsRefused",
        "the next run reads the mark as a completed walk that found the source empty",
    ),
    (
        "M6 a blank source becomes a usable key",
        "\tif strings.TrimSpace(source) == \"\" {\n\t\treturn errors.New(",
        "\tif false {\n\t\treturn errors.New(",
        "TestAnEmptySourceIsRefused",
        "the key is (entity, empty), which the first second source then shares",
    ),
    (
        "M7 the key is the whole endpoint instead of its host",
        "\treturn host, nil\n}",
        "\treturn e, nil\n}",
        "TestSourceKeyComesFromTheEndpointHost",
        "two spellings of one source become two sources with two marks, and neither ever sees the other's",
    ),
    (
        "M8 the walk order stops matching the importer's",
        'var SyncEntities = []string{"tags", "sites", "studios", "performers", "scenes"}',
        'var SyncEntities = []string{"scenes", "performers", "studios", "sites", "tags"}',
        "TestTheSyncEntityListIsWhatTheRunnerWalks",
        "a scene is walked before the performers it resolves against, which is the reason the importer's order "
        "is fixed in the first place",
    ),
]


def run(test=None):
    args = ["go", "test", "-tags", "integration", "-count=1", "-p", "1", PKG]
    if test:
        args += ["-run", test]
    env = dict(os.environ)
    env.setdefault("POSTGRES_DB", "postgres:smoke_pw@127.0.0.1:55434/stash-box-test?sslmode=disable")
    return subprocess.run(args, cwd=REPO, capture_output=True, text=True, timeout=900, env=env)


def main():
    if "POSTGRES_DB" not in os.environ:
        print("POSTGRES_DB is not set: these probes run the integration build, which needs a scratch database.")
        return 3
    wanted = [a for a in sys.argv[1:] if not a.startswith("-")]
    total = killed = 0
    for name, old, new, test, why in PROBES:
        if wanted and name.split()[0] not in wanted:
            continue
        total += 1
        original = open(STORE).read()
        if original.count(old) != 1:
            print(f"SKIP  {name}: the anchor occurs {original.count(old)} times")
            continue
        open(STORE, "w").write(original.replace(old, new, 1))
        try:
            result = run(test)
            if "no tests to run" in (result.stdout or ""):
                print(f"HARNESS-BUG  {name}: `go test -run {test}` ran NO TEST (exit 0)")
                return 2
            if result.returncode == 0:
                print(f"SURVIVED  {name}\n          {why}")
                print("          the test did not notice, which is a statement about the test")
            elif "build failed" in result.stdout or "[build failed]" in result.stdout:
                print(f"BUILD-ERR  {name} (a lint trip is not a survivor and not a kill)")
                print(result.stdout[-500:])
            else:
                killed += 1
                print(f"KILLED  {name}  ({test})")
        finally:
            open(STORE, "w").write(original)
        back = run()
        if back.returncode != 0:
            print(f"RESTORE-FAIL  {name}: the package is red after restoring the file")
            print(back.stdout[-700:])
            return 1
    print(f"\n{killed}/{total} killed, every restore green")
    return 0 if killed == total and total else 1


if __name__ == "__main__":
    sys.exit(main())
