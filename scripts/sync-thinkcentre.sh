#!/usr/bin/env bash
# sync-thinkcentre.sh -- the "add anything missing, then bring the rest up to date" button.
#
# DETERMINISTIC AND REPEATABLE: it does the same two things in the same order every time, it is safe to run at
# any moment, and it is the same code path the timers run. There is no second implementation that could drift
# from the scheduled one, which is the usual reason a "just run this script" workflow stops matching what the
# service does.
#
#   --incremental (default)  reconcile what changed since the last run, then add anything new among performers.
#                            Minutes to tens of minutes. This is what the 6-hourly timer runs.
#   --full                   the same, plus a full import walk of every entity. HOURS (the source has 112,733
#                            performers and 1,118,852 scenes, and the import is a page-at-a-time walk). This is
#                            what the weekly timer runs.
#   --dry-run                report what would change and write nothing. Passed through to both halves.
#
# Both halves are idempotent by construction -- the import by natural key, the sync by its watermark and its
# diff -- so running this twice in a row is free and running it after a failure resumes rather than duplicating.

set -euo pipefail

DEPLOY="${STASH_BOX_DEPLOY:-$HOME/stash-box-deploy}"
CONFIG="${STASH_BOX_CONFIG:-$DEPLOY/config/stash-box-config.yml}"
AUTH="${STASH_BOX_AUTH:-$HOME/.sdb_auth.env}"
LOG_DIR="$DEPLOY/import-logs"

MODE="--incremental"
DRY=""
# Flags valid for BOTH halves and flags valid only for the sync half are kept apart: `-sync-modes` is not an
# import flag, and passing it to the import half would make the script fail at its own convenience.
BOTH=()
SYNC_ONLY=()
while [ $# -gt 0 ]; do
  case "$1" in
    --incremental) MODE="--incremental" ;;
    --full)        MODE="--full" ;;
    --dry-run)     DRY="-dry-run" ;;
    --limit-performer|--limit-scene|--entities|--delay)
      BOTH+=("$1" "$2"); shift ;;
    --sync-modes|--sync-margin)
      SYNC_ONLY+=("$1" "$2"); shift ;;
    *) echo "sync-thinkcentre.sh: unknown argument $1" >&2; exit 2 ;;
  esac
  shift
done

[ -x "$DEPLOY/bin/sdbimport" ] || { echo "no sdbimport at $DEPLOY/bin/sdbimport" >&2; exit 1; }
[ -r "$CONFIG" ] || { echo "no config at $CONFIG (set STASH_BOX_CONFIG)" >&2; exit 1; }

# Credentials come from a file the service reads, NEVER from the command line: an argument is visible in `ps` to
# every user on the host, and `systemctl show` would print it.
if [ ! -r "$AUTH" ]; then
  echo "no credentials at $AUTH (set STASH_BOX_AUTH); see import-chain.sh for the expected keys" >&2
  exit 1
fi
set -a
# shellcheck disable=SC1090
. "$AUTH"
set +a

mkdir -p "$LOG_DIR"
STAMP="$(date +%Y%m%d-%H%M%S)"
JSON="$LOG_DIR/sync-$STAMP.json"

run() { "$DEPLOY/bin/sdbimport" -config "$CONFIG" "$@"; }

echo "== $(date -Is) sync-thinkcentre.sh $MODE $DRY =="

# HALF ONE: the reconcile. Cheap, watermarked, and first, because it is the half that must run every time.
# A held lock is a normal outcome (a manual run beside the timer's) and this is where that shows up.
set +e
run -sync -json "$JSON" $DRY ${BOTH[@]+"${BOTH[@]}"} ${SYNC_ONLY[@]+"${SYNC_ONLY[@]}"} 2>&1 | sed 's/^/  /' 
SYNC_RC=${PIPESTATUS[0]}
set -e
echo "  sync exit=$SYNC_RC  report=$JSON"

if [ "$MODE" = "--full" ]; then
  # HALF TWO: the import, which is what adds records the destination has never seen. The sync does not create
  # (by design: an id the instance has never seen is the import path's job), so without this half the timers
  # would keep an old mirror current instead of growing it.
  echo "  --full: importing every entity (hours on a cold database)"
  set +e
  run -entities tags,sites,studios,performers,scenes -json "$LOG_DIR/import-$STAMP.json" $DRY ${BOTH[@]+"${BOTH[@]}"} 2>&1 | tail -20 | sed 's/^/  /'
  IMPORT_RC=${PIPESTATUS[0]}
  set -e
  echo "  import exit=$IMPORT_RC  report=$LOG_DIR/import-$STAMP.json"
else
  # The incremental path still ADDS what is new, it just bounds the walk: performers only, which is the entity
  # the reconcile is defined for and the one where a person notices an absence.
  echo "  --incremental: adding new performers"
  set +e
  run -entities performers -json "$LOG_DIR/import-performers-$STAMP.json" $DRY ${BOTH[@]+"${BOTH[@]}"} 2>&1 | tail -6 | sed 's/^/  /'
  IMPORT_RC=${PIPESTATUS[0]}
  set -e
  echo "  import exit=$IMPORT_RC  report=$LOG_DIR/import-performers-$STAMP.json"
fi

echo "== done $(date -Is) =="

# The exit code is the sync's, because a failed reconcile is the thing an operator must hear about. A failed
# import is reported but does not mask it -- both are in the JSON and both are printed above.
[ "$SYNC_RC" -eq 0 ] || exit "$SYNC_RC"
exit "$IMPORT_RC"
