#!/usr/bin/env bash
# Generate the schema file list sqlc uses, in NUMERIC migration order.
#
# WHY THIS EXISTS. `sqlc generate` replays the migration directory in LEXICOGRAPHIC
# order, so every 3-digit migration sorts before the 2-digit ones:
#
#     106_tag_category_nesting.up.sql   <-- sorts here
#     10_tag_categories.up.sql          <-- and here, i.e. LATER
#
# Migration 106 ALTERs `tag_categories`, which 10 creates, so sqlc reports
# "relation tag_categories does not exist". Migrations 100, 102, 103 and 105 never hit
# this only because their SQL happens not to depend on ordering -- 105 references
# `scenes`, created in 01, so its misordering is invisible. 106 is the first migration
# that actually depends on order, and it is the one that exposes the bug.
#
# golang-migrate -- the applier that matters -- sorts NUMERICALLY, so the real database
# is fine; `schema_migrations` on the live instance reads 105 with no trouble. This is a
# code-generation problem only.
#
# WHY A LIST AND NOT A RENAMED FILE. Renaming 106 to something that sorts last would
# collide with the already-applied version 106 on every deployed database.
# `sqlc.yaml`'s `schema` key accepts an explicit list, applied in the order given, so the
# fix is to hand it the list the applier actually uses.
#
# WHY IT IS GENERATED RATHER THAN HAND-WRITTEN. A hand-maintained list is a second source
# of truth that silently goes stale the moment a migration is added -- and a stale list
# fails as a confusing codegen error, not as an obvious one. This regenerates from the
# directory every time, so adding a migration needs no second edit.
#
# It ALSO rewrites the `schema:` block in sqlc.yaml. That is the part I got wrong the
# first time: I wrote the generator to emit a list file and then INLINED that list into
# sqlc.yaml by hand, so adding a migration still needed a second manual edit -- and
# migration 107 needed exactly that edit while the file's own comment claimed otherwise.
# Now the generator owns both, and running it is the whole procedure.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

DIR="internal/database/migrations/postgres"
OUT="internal/database/sqlc-schema-files.txt"
YAML="sqlc.yaml"

# Sort on the leading integer with `sort -n` on a version key, then strip it. The `.up.sql`
# files only: `.down.sql` is the inverse and replaying it would UNDO the schema.
find "$DIR" -name '*.up.sql' -printf '%f\n' \
  | sed -E 's/^([0-9]+)_.*/\1 &/' \
  | sort -k1,1n -k2 \
  | cut -d' ' -f2- \
  | sed "s|^|$DIR/|" > "$OUT"

COUNT="$(wc -l < "$OUT")"
echo "wrote $OUT: $COUNT migrations in numeric order"
echo "first: $(head -1 "$OUT")"
echo "last:  $(tail -1 "$OUT")"

# Fail loudly if the order is not what we claimed, because a wrong order here produces a
# codegen error that says nothing about ordering.
FIRST_TWO="$(head -2 "$OUT" | sed 's|.*/||' | tr '\n' ' ')"
case "$FIRST_TWO" in
  "01_"*) ;;
  *) echo "FAIL: expected the 01 migration first, got: $FIRST_TWO" >&2; exit 1 ;;
esac
if ! sed -n '10p' "$OUT" | grep -q '/10_tag_categories.up.sql$'; then
  echo "NOTE: migration 10 is not at line 10 -- listing is $(sed -n '10p' "$OUT")"
fi

# Rewrite sqlc.yaml's `schema:` block from $OUT.
#
# Done with awk rather than sed or a YAML round trip: sed's multiline replacement is exactly
# the kind of thing that silently mangles a config file, and a YAML library would reformat
# comments we want kept. awk replaces only the lines between `schema:` and the next
# top-level key, which is the only region that changes.
awk -v out="$OUT" '
  /^    schema:/ {
    print
    while ((getline line < out) > 0) print "      - " line
    close(out)
    skipping = 1
    next
  }
  skipping && /^    [a-z_]+:/ { skipping = 0 }
  skipping { next }
  { print }
' "$YAML" > "$YAML.tmp"
mv "$YAML.tmp" "$YAML"

echo "OK: sqlc.yaml regenerated with $(wc -l < "$OUT") schema paths"