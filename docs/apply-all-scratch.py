#!/usr/bin/env python3
"""Apply every migration in order to a scratch database, with the pg_search extension shimmed
out, and verify the named view/table behaviours against a real database.

Local verification only. The shim is never written back to the repo: migrations 56 and 58
CREATE EXTENSION pg_search and build USING bm25 indexes, and this host has no pg_search. The
shim rewrites those two statements so the REST of the chain resolves, which is all that is
needed to prove a later migration applies against the schema its predecessors produce.

Usage: python3 docs/apply-all-scratch.py <dbname> [--through N]
"""
import glob
import os
import re
import subprocess
import sys

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
MIG = os.path.join(REPO, "internal/database/migrations/postgres")
HOST = os.environ.get("VERIFY_HOST", "127.0.0.1")
# `gravity` was hardcoded here AND in verify-724-schema.py, and it is another project's
# role on this host. Both scripts now read the same three variables, so pointing them at a
# scratch PostgreSQL means setting them once instead of editing two files -- and they can no
# longer disagree about which database they are talking to.
PORT = os.environ.get("VERIFY_PORT", "5432")
USER = os.environ.get("VERIFY_USER", "postgres")
# PGPASSWORD comes from the environment when set. The default is the local development
# password, and it is deliberately NOT hardcoded as a fallback: an earlier version read
# os.environ.get("PGPASSWORD", "smoke_pw") here and in the parent script, so a run with a
# different password failed with "password authentication failed for user gravity" while the
# parent had it right -- two sources of truth for the same credential.
ENV = dict(os.environ)
if not ENV.get("PGPASSWORD"):
    sys.exit("set PGPASSWORD for the local postgres role (e.g. PGPASSWORD=... python3 %s)"
             % os.path.basename(sys.argv[0]))


def shim(sql: str) -> str:
    """Neutralise pg_search so the schema can be built without the extension."""
    sql = sql.replace("CREATE EXTENSION IF NOT EXISTS pg_search;", "")
    sql = sql.replace("CREATE EXTENSION IF NOT EXISTS pdb;", "")
    sql = sql.replace("(gender::pdb.literal)", "gender")

    def plain_index(m):
        cols = [c.strip() for c in m.group(3).split(",") if c.strip()]
        return "CREATE INDEX %s ON %s (%s);" % (m.group(1), m.group(2), ", ".join(cols))

    sql = re.sub(
        r"CREATE\s+INDEX\s+(\w+)\s+ON\s+(\w+)\s+USING\s+bm25\s*\((.*?)\)\s*WITH\s*\(.*?\)\s*;",
        plain_index, sql, flags=re.S | re.I)
    if "USING bm25" in sql.lower():
        raise SystemExit("a bm25 index survived the shim -- the regex needs widening")
    return sql


def psql(db, sql, quiet=True):
    r = subprocess.run(
        ["psql", "-h", HOST, "-p", PORT, "-U", USER, "-d", db, "-v", "ON_ERROR_STOP=1", "-q", "-c", sql],
        env=ENV, capture_output=True, text=True)
    if r.returncode and not quiet:
        sys.stderr.write(r.stderr)
    return r.returncode == 0, r.stderr.strip()


def recreate(db):
    subprocess.run(["dropdb", "-h", HOST, "-p", PORT, "-U", USER, "--if-exists", db], env=ENV,
                   capture_output=True)
    r = subprocess.run(["createdb", "-h", HOST, "-p", PORT, "-U", USER, db], env=ENV, capture_output=True, text=True)
    if r.returncode:
        sys.exit("createdb failed: " + r.stderr.strip())


def main():
    db = sys.argv[1] if len(sys.argv) > 1 else "sb_scratch"
    # Default to the HIGHEST migration that exists, not a hardcoded number. The default was 99
    # and silently skipped 100 when it was added -- the script reported "applied through 99" and
    # looked like it had done its job. A verification harness with a pinned ceiling goes stale
    # quietly, which is the one thing it must not do.
    through = max(int(os.path.basename(f).split("_")[0])
                  for f in glob.glob(os.path.join(MIG, "*.up.sql")))
    if "--through" in sys.argv:
        through = int(sys.argv[sys.argv.index("--through") + 1])

    recreate(db)
    files = sorted(glob.glob(os.path.join(MIG, "*.up.sql")),
                   key=lambda f: int(os.path.basename(f).split("_")[0]))
    applied = []
    for path in files:
        n = int(os.path.basename(path).split("_")[0])
        if n > through:
            break
        name = os.path.basename(path)
        ok, err = psql(db, shim(open(path).read()))
        if not ok:
            print("FAILED at %s" % name)
            print(err[:400])
            return 1
        applied.append(n)

    # Report the HIGHEST NUMBER, not the file count. Number 92 does not exist in this repo, so
    # "applied 98 migrations" reads as though 92 was covered when it was not -- and a reader
    # checking the count against the spec would be checking the wrong thing.
    highest = max(applied) if applied else 0
    gaps = [n for n in range(1, highest + 1) if n not in applied]
    print("applied through %d (%d files) to %s" % (highest, len(applied), db))
    if gaps:
        print("note: no migration file exists for %s -- absent upstream, not skipped here"
              % ", ".join(str(g) for g in gaps))
    return 0


if __name__ == "__main__":
    sys.exit(main())