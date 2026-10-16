#!/usr/bin/env bash
# The restore drill §7.24.11 asks for: back the instance up, verify the artifact,
# restore it into a scratch database, check what came back, and run the
# integration suite against the RESTORED database.
#
# Usage: scripts/backup-drill.sh -config <path to the instance's config file> [-keep]
#
#   -keep   leave the artifact and the scratch database behind for inspection
#
# WHY THE DSN IS NEVER AN ARGUMENT, here as well as in the command: a backup
# command that takes a connection string as a flag puts the instance's database
# password in `ps` output and in the shell history of whoever ran the drill. This
# script decomposes the DSN read from the config file into PGHOST/PGPORT/PGUSER/
# PGPASSWORD/PGDATABASE for psql, and for the restore it writes a TEMPORARY config
# file (mode 600, deleted on exit) with the scratch database's name substituted,
# so `backup -restore -config <temp>` is the only form that runs.
#
# WHAT THE SUITE RUN AT THE END DOES AND DOES NOT PROVE. The integration harness
# drops every table and re-runs all migrations before any integration package
# runs, so the suite passing against a restored database proves the restored
# database is one this build can run against -- it does NOT prove the restored
# DATA is intact, because the harness replaces it. The data claims are steps 4
# and 5: the restore's own migration-version and row-count comparison against the
# manifest, and the spot check this script does against a recorded count.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONFIG=""
KEEP=0

while [ $# -gt 0 ]; do
  case "$1" in
    -config) CONFIG="${2:-}"; shift 2 ;;
    -keep)   KEEP=1; shift ;;
    -h|--help) sed -n '2,30p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "backup-drill: unknown argument: $1" >&2; exit 2 ;;
  esac
done

if [ -z "$CONFIG" ]; then
  echo "backup-drill: -config is required (the instance's config file)" >&2
  exit 2
fi
if [ ! -f "$CONFIG" ]; then
  echo "backup-drill: no such config file: $CONFIG" >&2
  exit 2
fi

pass=0
fail=0
step() { printf '\n== %s\n' "$*"; }
ok()   { pass=$((pass+1)); printf '   ok   %s\n' "$*"; }
bad()  { fail=$((fail+1)); printf '   FAIL %s\n' "$*"; }

WORK="$(mktemp -d)"
TMPCFG="$WORK/config-with-scratch-db.yml"
chmod 700 "$WORK"

# The config's database line, which is a scheme-less DSN: user:password@host:port/db?params
DSN="$(sed -n 's/^[[:space:]]*database:[[:space:]]*"\?\([^"]*\)"\?[[:space:]]*$/\1/p' "$CONFIG" | head -1)"
if [ -z "$DSN" ]; then
  echo "backup-drill: $CONFIG sets no database" >&2
  exit 2
fi

# Decompose it into the variables libpq reads, so nothing printed and no argument
# carries the password.
USERINFO="${DSN%%@*}"
HOSTPART="${DSN#*@}"
DBPART="${HOSTPART#*/}"
HOSTPORT="${HOSTPART%%/*}"
PGUSER_="${USERINFO%%:*}"
PGPASSWORD_="${USERINFO#*:}"
PGHOST_="${HOSTPORT%%:*}"
PGPORT_="${HOSTPORT##*:}"
PGDATABASE_="${DBPART%%\?*}"
PARAMS=""
case "$DBPART" in *\?*) PARAMS="?${DBPART#*\?}" ;; esac
export PGHOST="$PGHOST_" PGPORT="$PGPORT_" PGUSER="$PGUSER_" PGPASSWORD="$PGPASSWORD_"

SCRATCH_DB="backup_drill_$(date +%s)_$$"
SCRATCH_DSN="${PGUSER_}:${PGPASSWORD_}@${PGHOST_}:${PGPORT_}/${SCRATCH_DB}${PARAMS}"

cleanup() {
  local rc=$?
  if [ "$KEEP" -eq 0 ]; then
    PGDATABASE=postgres psql -q -c "DROP DATABASE IF EXISTS \"$SCRATCH_DB\" WITH (FORCE)" >/dev/null 2>&1 || true
    rm -rf "$WORK"
  else
    echo "kept: $WORK (artifact) and database $SCRATCH_DB"
  fi
  rm -f "$TMPCFG"
  exit $rc
}
trap cleanup EXIT

step "build the command"
if ! go build -o "$WORK/stash-box-backup" "$REPO_ROOT/cmd/backup" 2>"$WORK/build.log"; then
  cat "$WORK/build.log" >&2
  bad "go build cmd/backup"
  exit 1
fi
BIN="$WORK/stash-box-backup"
ok "built"

step "1. take a backup of the instance in $CONFIG"
if ! "$BIN" -config "$CONFIG" -out "$WORK/artifact" >"$WORK/backup.log" 2>&1; then
  cat "$WORK/backup.log" >&2
  bad "backup failed"
  exit 1
fi
sed 's/^/   /' "$WORK/backup.log"
ok "artifact written to $WORK/artifact"

if [ -f "$WORK/artifact/MANIFEST.json" ]; then ok "MANIFEST.json exists"; else bad "no manifest"; fi
if [ -f "$WORK/artifact/db.dump" ]; then ok "db.dump exists"; else bad "no db.dump"; fi
if (cd "$WORK/artifact" && sha256sum -c CHECKSUMS.sha256 >/dev/null 2>&1); then
  ok "sha256sum -c CHECKSUMS.sha256 (the artifact checks out with a tool that has never heard of stash-box)"
else
  bad "CHECKSUMS.sha256 does not verify with sha256sum"
fi

step "2. -verify the artifact"
if "$BIN" -verify -dir "$WORK/artifact" | sed 's/^/   /'; then ok "-verify"; else bad "-verify"; fi

step "3. create a scratch database and restore into it"
if psql -q -c "CREATE DATABASE \"$SCRATCH_DB\"" 2>"$WORK/create.log"; then ok "created $SCRATCH_DB"; else
  cat "$WORK/create.log" >&2; bad "could not create the scratch database"; exit 1
fi
sed "s|^\([[:space:]]*database:[[:space:]]*\).*|\1${SCRATCH_DSN}|" "$CONFIG" >"$TMPCFG"
chmod 600 "$TMPCFG"
if [ "$(sed -n 's/^[[:space:]]*database:[[:space:]]*\(.*\)$/\1/p' "$TMPCFG" | head -1)" != "$SCRATCH_DSN" ]; then
  bad "the temporary config does not point at the scratch database: refusing to restore"
  exit 1
fi
ok "temporary config points at $SCRATCH_DB, not at the instance"

if "$BIN" -restore -dir "$WORK/artifact" -config "$TMPCFG" | sed 's/^/   /'; then
  ok "-restore (its own migration-version and row-count comparison against the manifest passed)"
else
  bad "-restore"
fi

step "4. a second restore into a database that holds tables is refused"
# The target is the SCRATCH database, never the instance's own: a drill that
# points a destructive command at the live database to see whether it refuses is
# a drill that destroys the instance the one time the guard is broken. The scratch
# database already holds the restore from step 3, which is exactly the state the
# guard exists for.
if "$BIN" -restore -dir "$WORK/artifact" -config "$TMPCFG" >"$WORK/refuse.log" 2>&1; then
  bad "a restore over a database that already holds tables was NOT refused"
else
  rc=$?
  if [ "$rc" -eq 2 ] && grep -q -- "-force" "$WORK/refuse.log"; then
    ok "refused with exit 2 and named -force"
  else
    bad "the second restore failed with exit $rc, want a refusal (exit 2): $(cat "$WORK/refuse.log")"
  fi
fi

step "5. spot-check restored rows against the manifest"
SPOT="$(python3 - "$WORK/artifact/MANIFEST.json" <<'PY'
import json, sys
m = json.load(open(sys.argv[1]))
tables = {t["table"]: t["rows"] for t in m["database"]["tables"]}
# A table with rows, so the check has a subject: comparing 0 to 0 proves nothing.
for name in ("performers", "scenes", "studios", "tags", "images", "edits", "users"):
    if tables.get(name, 0) > 0:
        print(f"{name} {tables[name]}")
        break
else:
    print("")
PY
)"
if [ -z "$SPOT" ]; then
  bad "the instance has no rows in any checked table: this drill would have compared 0 to 0"
else
  TBL="${SPOT%% *}"; WANT="${SPOT##* }"
  GOT="$(PGDATABASE="$SCRATCH_DB" psql -At -c "SELECT count(*) FROM \"$TBL\"")"
  if [ "$GOT" = "$WANT" ]; then ok "$TBL: $GOT rows, the manifest says $WANT"; else bad "$TBL: $GOT rows in the restore, $WANT in the manifest"; fi
  # An absence assertion needs its subject counted: a zero here would make the
  # next check pass about nothing.
  if [ "$WANT" -gt 0 ]; then
    ok "the spot check had a non-zero subject ($TBL = $WANT)"
  else
    bad "the spot check compared zero to zero"
  fi
fi

step "6. the integration suite against the RESTORED database"
SUITE="${BACKUP_DRILL_SUITE:-./internal/api/}"
if (cd "$REPO_ROOT" && POSTGRES_DB="$SCRATCH_DSN" go test -tags integration -count=1 -p 1 $SUITE 2>&1 | tail -20 | sed 's/^/   /'); then
  ok "integration suite green against the restored database (see the note in this script: the harness drops and re-migrates, so this proves the restored database is one this build runs against, not that the data is intact)"
else
  bad "integration suite failed against the restored database"
fi

printf '\n== drill result: %d ok, %d failed\n' "$pass" "$fail"
[ "$fail" -eq 0 ]
