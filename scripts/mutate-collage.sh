#!/usr/bin/env bash
# Mutation-verifies the collage GraphQL integration tests.
#
# Each mutation breaks ONE behaviour, and each test covering that behaviour must
# fail. A mutation that leaves the suite green means the test does not check what
# its name claims.
#
# RESTORATION IS THE POINT. The first version of this harness used
# `git checkout -- internal/api/resolver_collage.go`, which is a SILENT NO-OP on an
# untracked file. All nine mutations therefore stayed live, each contaminated the
# next, and the harness cheerfully reported "9 caught, 0 survived" while measuring
# nothing at all. It now snapshots the file and restores from that snapshot, and
# FAILS LOUDLY if the restore did not take -- because a mutation harness that
# cannot prove its own cleanup is worse than none: it manufactures confidence.
set -uo pipefail
cd "$(dirname "$0")/.."

export POSTGRES_DB="postgres@127.0.0.1:55434/sbx-scratch?sslmode=disable&password=smoke_pw"
TESTS='TestCollage|TestSceneSnapshots|TestAddSnapshot|TestUnderSnapshotted'
# TWO files are snapshotted, not one: the negative-timestamp rule lives in the
# service while everything else lives in the resolver, so a harness that only
# restores the resolver would leave a service mutation live and contaminate
# everything after it. Exactly that happened in the first version of this script.
TARGETS="internal/api/resolver_collage.go internal/service/collage/collage.go"
SNAPS=$(mktemp -d /tmp/collage-mut.XXXXXX)
for f in $TARGETS; do
  cp "$f" "$SNAPS/$(basename "$f")"
done

cleanup() { for f in $TARGETS; do cp "$SNAPS/$(basename "$f")" "$f"; done; rm -rf "$SNAPS"; }
trap cleanup EXIT

restore() {
  for f in $TARGETS; do cp "$SNAPS/$(basename "$f")" "$f"; done
  if grep -q MUTANT $TARGETS; then
    echo "  *** RESTORE FAILED -- a MUTANT marker survived. Aborting: every result"
    echo "      from here on would be contaminated."
    exit 1
  fi
}

# mutate <original> <mutated>. Refuses if the target text is absent: a harness
# that silently does nothing reports a false survivor, which is worse than a crash.
mutate() { python3 - "$1" "$2" $TARGETS <<'PY'
import sys
original, mutated = sys.argv[1], sys.argv[2]
for p in sys.argv[3:]:
    s = open(p).read()
    if original in s:
        open(p, "w").write(s.replace(original, mutated, 1))
        sys.exit(0)
print("MUTATION TARGET NOT FOUND in any target file -- the harness is stale:")
print("   ", original[:90])
sys.exit(2)
PY
}

pass=0; fail=0; survivors=""

run() {
  mutate "$2" "$3" || exit 1
  if go test -tags=integration -count=1 -p 1 -run "$TESTS" ./internal/api/ >/tmp/mut.log 2>&1; then
    echo "  *** SURVIVED: $1"
    survivors="$survivors"$'\n'"    - $1"
    fail=$((fail+1))
  else
    echo "  caught: $1"
    pass=$((pass+1))
  fi
  restore
}

echo "=== baseline ==="
if go test -tags=integration -count=1 -p 1 -run "$TESTS" ./internal/api/ >/tmp/mut.log 2>&1; then
  echo "  GREEN before mutating"
else
  echo "  BASELINE IS RED -- fix that before trusting any result below"
  grep -E "^--- FAIL|Error:|Messages:" /tmp/mut.log | head -12
  exit 1
fi

run "stale always false" \
    "	return obj.Stale(), nil" \
    "	return false, nil // MUTANT"

run "stale always true" \
    "	return obj.Stale(), nil" \
    "	return true, nil // MUTANT"

run "duration reports the stale source value" \
    "	return msToInt(obj.CurrentDurationMS), nil" \
    "	return msToInt(obj.SourceDurationMS), nil // MUTANT"

run "fraction computed against a frozen denominator" \
    "		fr := float64(f.TimestampMS) / float64(*durationMS)" \
    "		fr := float64(f.TimestampMS) / 100000.0 // MUTANT"

run "fraction guessed as 0.0 when the duration is unknown" \
    "	var fraction *float64
	if durationMS != nil && *durationMS > 0 {" \
    "	var fraction *float64
	if true { // MUTANT
		z := 0.0
		fraction = &z
	}
	if durationMS != nil && *durationMS > 0 {"

run "frames always empty" \
    "	return out, nil
}

func (r *Resolver) CollageFrame()" \
    "	return []models.CollageFrame{}, nil // MUTANT
}

func (r *Resolver) CollageFrame()"

run "frames loaded from the wrong service method" \
    "	frames, err := r.services.Collage().ListFrames(ctx, obj.SceneID)" \
    "	frames, err := r.services.Collage().ListSnapshots(ctx, obj.SceneID) // MUTANT"

# The negative-timestamp rule lives in the SERVICE, so that is what gets mutated.
# Mutating a duplicate guard in the resolver instead produces an equivalent mutant:
# the service still rejects the value, so the suite stays green while proving
# nothing about the resolver.
run "negative timestamps accepted (service guard weakened)" \
    "	if timestampMS < 0 {
		return nil, fmt.Errorf(\"a snapshot timestamp cannot be negative (got %d)\", timestampMS)
	}" \
    "	if timestampMS < -1000 { // MUTANT: service guard weakened
		return nil, fmt.Errorf(\"a snapshot timestamp cannot be negative (got %d)\", timestampMS)
	}"

run "under-snapshotted minimum always 0" \
    "	return collage.DefaultFrames, nil" \
    "	return 0, nil // MUTANT"

run "under-snapshotted always empty" \
    "	rows, err := r.services.Collage().ListUnderSnapshotted(ctx, min, lim)" \
    "	rows, err := []*collage.UnderSnapshotted{}, error(nil) // MUTANT"

run "sceneCollage invents an empty collage instead of null" \
    "	return collageToModel(c), nil" \
    "	if c == nil {
		return &models.Collage{FrameCount: 0}, nil // MUTANT
	}
	return collageToModel(c), nil"

echo
echo "=== summary: $pass caught, $fail survived ==="
if [ "$fail" -gt 0 ]; then
  printf "survivors:%s\n" "$survivors"
  exit 1
fi
echo "every mutation was caught, and the file was restored between each"