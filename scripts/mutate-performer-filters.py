#!/usr/bin/env python3
"""Mutation battery for the ported performer filter indexes and URL filter (upstream PR #1284).

Four probes, one per rule the port is worth having. The battery FAILS if a probe that should be caught is
not: a mutation that does not build is not a verdict, and a probe whose named test is not in the run filter is
a FALSE SURVIVOR.

TWO OF THE FOUR PROBES EXIST BECAUSE THE PORT'S OWN TESTS WOULD OTHERWISE NOT COVER THE MIGRATION. The PR's
integration test (the URL filter) passes with the migration deleted -- a filter on a table this small is
sequential-scanned either way -- so the index test is the only thing standing between "the indexes exist" and
"the indexes were never applied". M2, M3 and M4 are what that test has to catch.

M3 AND M4 ARE NOT THE SAME PROBE, which is the point of having both: an index with the right NAME on the wrong
COLUMN, and an index with the right name and column and NO partial predicate. A name-only test passes both.

Needs a database: the URL probe runs the integration test.

    POSTGRES_DB='postgres:smoke_pw@127.0.0.1:55434/sbx-pr1284?sslmode=disable' \\
        python3 scripts/mutate-performer-filters.py [M1 M2 ...]
"""

import os
import subprocess
import sys

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

QUERY = "internal/service/performer/query.go"
MIGRATION = "internal/database/migrations/postgres/142_performer_filter_indexes.up.sql"

DB = os.environ.get("POSTGRES_DB", "postgres:smoke_pw@127.0.0.1:55434/sbx-pr1284?sslmode=disable")
INTEGRATION = ["go", "test", "-tags", "integration", "-count=1", "-p", "1", "-v", "./internal/api/"]


def suite(name):
    return INTEGRATION + ["-run", name]


MUTATIONS = [
    (
        "M1 the URL filter stops being case-insensitive",
        QUERY,
        'Where(sq.Expr("LOWER(performer_urls.url) = LOWER(?)", *input.URL))',
        'Where(sq.Eq{"performer_urls.url": *input.URL})',
        [suite("TestQueryPerformersURLCaseInsensitive")],
        "a URL is a spelling of an address, so a mixed-case stored path must be found by the lower-case form",
    ),
    (
        "M2 one index never gets created",
        MIGRATION,
        "CREATE INDEX performer_favorites_user_performer_idx ON performer_favorites (user_id, performer_id);",
        "-- dropped by the mutation battery",
        [suite("TestPerformerFilterIndexesExist")],
        "the favourite filter starts from the viewer: without this index it scans the whole join table",
    ),
    (
        "M3 an index is built on the wrong column",
        MIGRATION,
        "CREATE INDEX performers_height_idx ON performers (height) WHERE NOT deleted;",
        "CREATE INDEX performers_height_idx ON performers (waist_size) WHERE NOT deleted;",
        [suite("TestPerformerFilterIndexesExist")],
        "SURVIVED FIRST: the assertion was `Contains(def, \"height\")` and the index is named "
        "performers_height_idx, so a wrong column passed on the strength of the NAME. It is a regexp on the "
        "parenthesised column list now -- an index's name is not evidence about its column",
    ),
    (
        "M4 an index is not partial on live rows",
        MIGRATION,
        "CREATE INDEX performers_gender_idx ON performers (gender) WHERE NOT deleted;",
        "CREATE INDEX performers_gender_idx ON performers (gender);",
        [suite("TestPerformerFilterIndexesExist")],
        "a full index does not match the predicate the query applies, so the planner cannot use it for one",
    ),
]


def run(cmd):
    proc = subprocess.run(cmd, cwd=REPO, capture_output=True, text=True, timeout=900,
                          env=dict(os.environ, POSTGRES_DB=DB))
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

    _, out = run(INTEGRATION + ["-run", "TestPerformerFilterIndexesExist|TestQueryPerformersURLCaseInsensitive"])
    print("RESTORED TREE:", "green" if "FAIL" not in out else "RED -- A MUTATION IS STILL IN THE TREE")
    return 0 if killed == len(results) and "FAIL" not in out else 1


if __name__ == "__main__":
    sys.exit(main())
