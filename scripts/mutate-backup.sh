#!/usr/bin/env bash
# Mutation-verifies the backup command's guards.
#
# Each mutation breaks ONE behaviour and the named test must fail. A mutation that
# leaves the suite green means the test does not check what its name claims -- and
# for a backup tool that is the whole question: a check nobody has seen fail is a
# check nobody knows runs.
#
# EVERY MUTATION MUST COMPILE, and the marker that proves the restore worked lives
# in a COMMENT. The first version of this harness wrote `if false MUTANT && ...`,
# which does not compile: eleven of thirteen mutations came back broken. The harness
# reporting that as its own outcome -- rather than as eleven survivors -- is the
# only reason the mistake was visible at all, and a harness that counts a
# non-compiling mutation as killed is worse than none, because it manufactures
# confidence.
#
# RESTORATION IS PROOF, NOT BOOKKEEPING: every mutation is applied to a snapshot of
# the file, the file is restored after the run, and the harness ABORTS if a MUTANT
# marker survives.
#
# ONE GUARD HAS NO MUTATION, deliberately: DumpDatabase's `--snapshot=` argument.
# Removing it leaves every test green, because telling "the counts and the dump are
# one snapshot" from "they are two reads a moment apart" needs a write concurrent
# with the dump, and no test here can create one deterministically. It is named
# rather than faked: a survivor whose reason is "the test cannot see it" is a gap in
# the tests, and pretending otherwise is how a mutation gate becomes decoration.
set -uo pipefail
cd "$(dirname "$0")/.."

export POSTGRES_DB="${POSTGRES_DB:-postgres:smoke_pw@127.0.0.1:55434/stash-box-test?sslmode=disable}"

TARGETS="internal/backup/images.go internal/backup/checksums.go internal/backup/backup.go internal/backup/postgres.go"
SNAPS=$(mktemp -d /tmp/backup-mut.XXXXXX)
for f in $TARGETS; do
  cp "$f" "$SNAPS/$(basename "$f")"
done

cleanup() { for f in $TARGETS; do cp "$SNAPS/$(basename "$f")" "$f"; done; rm -rf "$SNAPS"; }
trap cleanup EXIT

restore() {
  for f in $TARGETS; do cp "$SNAPS/$(basename "$f")" "$f"; done
  if grep -rq 'MUTANT' $TARGETS; then
    echo "  *** RESTORE FAILED -- a MUTANT marker survived. Aborting: everything after"
    echo "      this point would be measured on contaminated code."
    exit 1
  fi
}

# mutate <file> <original text> <mutated text>. Refuses when the text is absent: a
# harness that silently mutates nothing reports a false survivor, which is worse
# than a crash.
mutate() {
  if ! python3 - "$1" "$2" "$3" <<'PY'
import sys
path, old, new = sys.argv[1], sys.argv[2], sys.argv[3]
text = open(path).read()
if old not in text:
    print(f"  *** the text to mutate is not in {path}: this mutation would do nothing", file=sys.stderr)
    sys.exit(3)
open(path, "w").write(text.replace(old, new, 1))
PY
  then
    echo "  *** aborting: a mutation that does nothing would be reported as a survivor" >&2
    exit 9
  fi
}

pass=0
survived=0
broken=0

# check <name> <test regex> -- the test that must go RED for the live mutation.
check() {
  local name="$1" pattern="$2"
  local out
  out=$(go test -tags integration -count=1 -run "$pattern" ./internal/backup/ 2>&1)
  if echo "$out" | grep -qE 'build failed|undefined:|syntax error|cannot find package'; then
    echo "  BROKEN    $name -- the mutation did not compile, so it proves nothing"
    broken=$((broken+1))
  elif echo "$out" | grep -q '^ok'; then
    echo "  SURVIVED  $name -- $pattern stayed green: the test does not check this"
    survived=$((survived+1))
  else
    echo "  killed    $name (by $pattern)"
    pass=$((pass+1))
  fi
  restore
}

echo "== baseline: everything green before any mutation"
if go test -tags integration -count=1 ./internal/backup/ 2>&1 | grep -q '^ok'; then
  echo "   ok   the suite is green, so a red result below means the mutation"
else
  echo "   FAIL the suite is already red -- fix that first, or every result below is noise"
  exit 4
fi

echo
echo "== an image whose bytes no longer match its row must fail the backup"
mutate internal/backup/images.go \
  'if !strings.EqualFold(sum, row.Checksum) {' \
  'if false && !strings.EqualFold(sum, row.Checksum) { // MUTANT'
check "the md5 comparison" 'TestVerifyImagesFailsOnAChangedByte'

echo
echo "== a symlink in the store must not be walked"
mutate internal/backup/images.go \
  'if d.Type()&os.ModeSymlink != 0 {' \
  'if false && d.Type()&os.ModeSymlink != 0 { // MUTANT'
check "the symlink refusal" 'TestEachStoreFileRefusesASymlink'

echo
echo "== Verify must refuse a file nobody recorded"
mutate internal/backup/checksums.go \
  'if _, ok := listed[f.Path]; !ok {' \
  'if _, ok := listed[f.Path]; false && !ok { // MUTANT'
check "the unlisted-file rule" 'TestVerifyFailsOnAFileNobodyRecorded'

echo
echo "== Verify must compare hashes, not sizes"
mutate internal/backup/checksums.go \
  'if !strings.EqualFold(sum, f.SHA256) {' \
  'if false && !strings.EqualFold(sum, f.SHA256) { // MUTANT'
check "the sha256 comparison" 'TestVerifyFailsOnAnAlteredFile'

echo
echo "== Verify must notice a missing file"
mutate internal/backup/checksums.go \
  'problems = append(problems, fmt.Sprintf("%s: missing", f.Path))
			continue' \
  'continue // MUTANT'
check "the missing-file rule" 'TestVerifyFailsOnAMissingFile'

echo
echo "== the manifest must list the checksum file it just wrote (a bug this once had)"
mutate internal/backup/backup.go \
  'collected, err = CollectFiles(dir)
	if err != nil {
		return err
	}
	m.Files = collected' \
  'if err != nil { // MUTANT
		return err
	}'
check "the second collection" 'TestWriteArtifactTailListsEveryFileItWrote'

echo
echo "== a table the restore dropped must be reported, not skipped"
mutate internal/backup/backup.go \
  'problems = append(problems, fmt.Sprintf("%s: in the manifest, absent after the restore", w.Table))
			continue' \
  'continue // MUTANT'
check "the absent-table branch" 'TestCompareVersionAndCounts'

echo
echo "== the migration version must be compared"
mutate internal/backup/backup.go \
  'if got != want {' \
  'if false && got != want { // MUTANT'
check "the migration-version comparison" 'TestCompareVersionAndCounts'

echo
echo "== the connection string must not be handed over whole"
mutate internal/backup/postgres.go \
  'env = append(env, "PGPASSWORD="+pass)' \
  'env = append(env, "PGPASSWORD=") // MUTANT'
check "PGPASSWORD" 'TestConnEnv'

echo
echo "== RedactDSN must redact"
mutate internal/backup/backup.go \
  'return scheme + dsnPassword.ReplaceAllString(rest, "$1:[REDACTED]@")' \
  'return scheme + rest // MUTANT'
check "RedactDSN" 'TestRedactDSNHidesThePasswordBothWays'

echo
echo "== a backup must refuse a directory that already holds one"
mutate internal/backup/backup.go \
  'if len(entries) > 0 {' \
  'if false && len(entries) > 0 { // MUTANT'
check "the non-empty-directory guard" 'TestSnapshotRefusesToWriteIntoANonEmptyDirectory'

echo
echo "== OpenPool must NOT run migrations"
# Two edits, because database.Initialize needs the import; the marker is the import
# line, so the restore check covers both.
mutate internal/backup/postgres.go \
  '	"github.com/jackc/pgx/v5/pgxpool"
)' \
  '	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/stashapp/stash-box/internal/database" // MUTANT
)'
# database.Initialize returns one value (it logger.Fatal's instead of returning an
# error), so the mutated body keeps `err` used rather than leaving it declared and
# unused -- a mutation that does not compile proves nothing.
mutate internal/backup/postgres.go \
  'pool, err := pgxpool.New(ctx, "postgres://"+dsn)' \
  'pool := database.Initialize(dsn) // MUTANT
	var err error'
check "OpenPool without migrations" 'TestSnapshotRestoreDrill'

echo
echo "== a restore over a database that holds tables must be refused"
mutate internal/backup/backup.go \
  'if has && !force {' \
  'if has && force { // MUTANT: refuses the wrong way round'
check "the destructive-restore refusal" 'TestSnapshotRestoreDrill'

echo
printf '\n== result: %d killed, %d survived, %d broken\n' "$pass" "$survived" "$broken"
[ "$survived" -eq 0 ] && [ "$broken" -eq 0 ]
