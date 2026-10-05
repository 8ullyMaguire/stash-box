#!/usr/bin/env bash
# Publish main to every writable remote and verify by SHA, not by exit status.
#
# Learned the hard way: `git push -q a && git push -q b` exits 0 having pushed
# nothing on a no-op, and the trailing && makes $? the SECOND push's status, so
# the first failing is invisible. Real file, set -e, echo the remote before each
# push, then compare SHAs.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."
BRANCH="$(git branch --show-current)"
REMOTES=(origin forgejo)

echo "branch: $BRANCH"
LOCAL="$(git rev-parse "$BRANCH")"
echo "local:   $LOCAL"

for r in "${REMOTES[@]}"; do
  echo "--- pushing $r ---"
  git push "$r" "$BRANCH"
done

echo
echo "--- verifying by SHA ---"
fail=0
for r in "${REMOTES[@]}"; do
  REMOTE_SHA="$(git ls-remote "$r" "refs/heads/$BRANCH" | cut -f1)"
  if [ "$REMOTE_SHA" = "$LOCAL" ]; then
    echo "  OK   $r  $REMOTE_SHA"
  else
    echo "  FAIL $r  remote=$REMOTE_SHA local=$LOCAL"
    fail=1
  fi
done
exit "$fail"
