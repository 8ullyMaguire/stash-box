#!/usr/bin/env bash
# Push every branch to every publishing remote, and verify.
#
# Written as a real file rather than a `git config alias` because quoting went
# wrong twice doing it that way, and the second attempt silently stored an empty
# value -- an alias that pushes nothing while exiting 0.
#
# No `-q`, and `set -e`: the two failures that make a multi-remote push lie are
# suppressing the output (a no-op is indistinguishable from a push) and letting
# the LAST command's exit status stand in for the chain's (so the first remote
# failing is invisible). Echo the remote before each push, and let the first
# failure abort before the second remote is attempted.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

HEAD_SHA=$(git rev-parse HEAD)

for remote in "$@"; do
  echo "==> $remote: $(git remote get-url "$remote")"
  git push --all "$remote"
  git push --tags "$remote"
done

echo
echo "==> verifying every remote agrees with local HEAD ($HEAD_SHA)"
status=0
for remote in "$@"; do
  # `main` only. issue-fixes -- the narrow->wide staging branch -- is gone: it never
  # held anything the wide branch could not take, and it cost a merge every time.
  # It also cost a duplicate migration. 92_scene_title_text was the same statement
  # as 88_scene_title_text and survived two attempts to remove it, because each
  # removal landed on master while the copy on issue-fixes was untouched, and every
  # promotion put it back. Topic branches per PR still exist, merged into main.
  for ref in refs/heads/main; do
    remote_sha=$(git ls-remote "$remote" "$ref" | cut -f1)
    local_sha=$(git rev-parse "$ref")
    if [ "$remote_sha" = "$local_sha" ]; then
      echo "  OK    $remote $ref ${remote_sha:0:8}"
    else
      echo "  DRIFT $remote $ref remote=${remote_sha:0:8} local=${local_sha:0:8}"
      status=1
    fi
  done
done

exit "$status"
