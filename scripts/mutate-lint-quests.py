#!/usr/bin/env python3
"""Mutation battery for the data-lint quests (SPEC §7.24.4, plan phase 3a).

    python3 scripts/mutate-lint-quests.py [M1 M2 .. M7]

TWO KINDS OF PROBE, because this feature has two kinds of claim. M1-M5 are decisions in Go -- which
registry row is refused, whether the entity filter reaches the query, whether a limit is clamped -- and
the package's fake querier can answer them. M6 and M7 are claims about SQL against a real database, so
those probes run the integration build (POSTGRES_DB must point at a scratch database, and the package
must be run with -p 1: the tests share one database).

EVERY PROBE BELOW NAMES THE TEST THAT MUST NOTICE, and one of them was written BECAUSE a probe
survived: deleting the alias arm of the collision query left every test green, because the fixtures
only ever collided an alias with a name. That is the whole point of running this -- a survivor is a
statement about the test suite, and the fix is usually a missing SUBJECT rather than a missing
assertion.
"""

import os
import subprocess
import sys

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SERVICE = os.path.join(REPO, "internal", "service", "lint", "service.go")
SQL = os.path.join(REPO, "internal", "queries", "sql", "lint.sql")
# The GENERATED file, not the .sql source, and that is the lesson this battery learned the hard way:
# `sqlc generate` embeds each query as a Go string constant, so editing internal/queries/sql/lint.sql
# changes NOTHING that compiles until codegen runs. The first run of this battery mutated the .sql file
# and reported two SQL probes SURVIVED -- both had been killed in fact, and the "survivor" was the
# artefact pipeline. Same family as a stale binary: when a mutation appears to have no effect, suspect
# the build before the test. This mutates what the compiler reads.
GENERATED = os.path.join(REPO, "internal", "queries", "lint.sql.go")
UNIT_PKG = "./internal/service/lint/"
INTEG_PKG = "./internal/api/"

PROBES = [
    (
        "M9 an empty registry is served as a clean board",
        SERVICE,
        "\tif len(defs) == 0 {",
        "\tif len(defs) < 0 {",
        "TestValidateRefusesAnEmptyRegistry",
        None,
        "an instance whose seeding never ran answers with a board that has no rows on it, which reads "
        "exactly like a board whose every detector found nothing",
    ),
    (
        "M8 the board renders without checking the registry",
        SERVICE,
        "\tif err := s.Validate(ctx); err != nil {\n\t\treturn nil, err\n\t}\n\tdefs, err := s.q.ListEnabledLintQuestDefinitions(ctx)",
        "\tdefs, err := s.q.ListEnabledLintQuestDefinitions(ctx)",
        "TestTheBoardRefusesARegistryThisBuildDoesNotImplement",
        None,
        "a caller gets a board with one fewer detector on it and no way to tell -- the failure mode the "
        "live walk found, where the board returned 5 detectors instead of 6",
    ),
    (
        "M1 Validate stops refusing an enabled row with no implementation",
        SERVICE,
        "\t\tif !known[def.Slug] {",
        "\t\tif false && !known[def.Slug] {",
        "TestValidateRefusesAnEnabledSlugThisBuildDoesNotImplement",
        None,
        "the instance starts with a detector row nobody implements, and the board skips it -- which "
        "looks exactly like a detector that found nothing",
    ),
    (
        "M2 the enabled direction is flipped, so a RETIRED row is refused and a live one is not",
        SERVICE,
        "\t\tif !def.Enabled {\n\t\t\tcontinue\n\t\t}",
        "\t\tif def.Enabled {\n\t\t\tcontinue\n\t\t}",
        "TestValidateAllowsADisabledSlugThisBuildDoesNotImplement",
        None,
        "a retired detector's row stops an instance from starting, making a rollback impossible",
    ),
    (
        "M3 a detector is asked about an entity type it does not speak",
        SERVICE,
        "\t\tif len(typed) == 0 {\n\t\t\tcontinue\n\t\t}",
        "\t\tif false && len(typed) == 0 {\n\t\t\tcontinue\n\t\t}",
        "TestValidateEntitiesDoesNotAskASceneDetectorAboutPerformers",
        None,
        "a performer-only import runs the scene detectors with performer ids: they match nothing, and "
        "an unpoliced import reads as clean",
    ),
    (
        "M4 a detector that filled its limit stops saying so",
        SERVICE,
        "\tif int(f.Limit) > 0 && len(cands) == int(f.Limit) {",
        "\tif false && int(f.Limit) > 0 && len(cands) == int(f.Limit) {",
        "TestADetectorThatFillsItsLimitSaysSo",
        None,
        "a board showing exactly its limit of a detector that found thousands reads as an archive in "
        "good shape",
    ),
    (
        "M5 the caller's limit is no longer clamped",
        SERVICE,
        "\tif limit > HardLimit {\n\t\tlimit = HardLimit\n\t}",
        "\tif false {\n\t\tlimit = HardLimit\n\t}",
        "TestTheLimitIsClampedAndZeroMeansTheDefault",
        None,
        "a caller can hand the database an unbounded scan of the url tables",
    ),
    (
        "M6 the deleted-performer filter is dropped from the duplicate-URL detector",
        GENERATED,
        "  JOIN performers p ON p.id = pu.performer_id AND NOT p.deleted\n",
        "  JOIN performers p ON p.id = pu.performer_id AND TRUE\n",
        "TestDuplicatePerformerURLsIgnoresDeletedPerformers",
        "-tags integration -p 1 -run TestDuplicatePerformerURLsIgnoresDeletedPerformers",
        "every merged-away performer -- this schema's merge leaves a deleted row -- reappears on the "
        "board as a duplicate-URL finding",
    ),
    (
        "M7 the collision query stops considering ALIASES as the other side",
        GENERATED,
        "    SELECT performer_id, alias, 'alias' FROM performer_aliases\n",
        "    SELECT performer_id, alias, 'alias' FROM performer_aliases WHERE FALSE\n",
        "TestAliasCollisionFiresWhenTwoPerformersShareAnAlias",
        "-tags integration -p 1 -run TestAliasCollisionFiresWhenTwoPerformersShareAnAlias",
        "two performers claiming the same alias stop being a finding, and the detail can no longer say "
        "'alias of' -- THIS PROBE SURVIVED the first run of this battery, which is why that test exists",
    ),
]


def run(test, extra):
    if extra:
        args = ["go", "test"] + extra.split() + [INTEG_PKG]
    else:
        args = ["go", "test", "-count=1", "-run", test, UNIT_PKG]
    env = dict(os.environ)
    env.setdefault("POSTGRES_DB", "postgres:smoke_pw@127.0.0.1:55434/stash-box-test?sslmode=disable")
    return subprocess.run(args, cwd=REPO, capture_output=True, text=True, timeout=1800, env=env)


def main():
    wanted = [a for a in sys.argv[1:] if not a.startswith("-")]
    total = killed = 0
    for name, path, old, new, test, extra, why in PROBES:
        if wanted and name.split()[0] not in wanted:
            continue
        total += 1
        original = open(path).read()
        if original.count(old) != 1:
            print(f"SKIP  {name}: the anchor occurs {original.count(old)} times in {os.path.basename(path)}")
            continue
        open(path, "w").write(original.replace(old, new, 1))
        try:
            result = run(test, extra)
            out = (result.stdout or "") + (result.stderr or "")
            if "no tests to run" in out:
                print(f"HARNESS-BUG  {name}: `go test -run {test}` ran NO TEST (exit 0)")
                return 2
            if "[build failed]" in out or "build failed" in out:
                print(f"BUILD-ERR  {name} (a lint trip is not a survivor and not a kill)")
                print(out[-400:])
            elif result.returncode == 0:
                print(f"SURVIVED  {name}\n          {why}")
                print("          the test did not notice, which is a statement about the test")
            else:
                killed += 1
                print(f"KILLED  {name}  ({test})")
        finally:
            open(path, "w").write(original)
        back = run("", extra)
        if back.returncode != 0:
            print(f"RESTORE-FAIL  {name}: the package is red after restoring the file")
            print(((back.stdout or "") + (back.stderr or ""))[-700:])
            return 1
    print(f"\n{killed}/{total} killed, every restore green")
    return 0 if killed == total and total else 1


if __name__ == "__main__":
    sys.exit(main())
