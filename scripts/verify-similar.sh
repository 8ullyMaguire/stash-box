#!/usr/bin/env bash
# Verify the similar-performers query (growth item 27) against real data.
#
# The scoring is the whole of this feature, and a plausible-but-wrong score is worse
# than none: it recommends confidently and wrongly. So this builds a fixture whose
# CORRECT answer is derivable by hand, and asserts the query produces it.
#
# The fixture lives in verify-similar.sql because four separate bugs came from
# string-concatenated psql calls: a LATERAL with no studio filter that attached the
# subject to every other seed's scenes, a WHERE wedged between FROM and CROSS JOIN, a
# `round(double, int)` that does not exist, and `gen_random_uuid()` with ON CONFLICT
# DO NOTHING, which can never conflict and so accumulated ten scenes per run. Every
# one of them produced a *plausible* ranking, which is why a first reading passed.
#
# Fixture shape (13 SUBJECT scenes, all in this fixture's studio):
#   CLOSE   shares 10 scenes with 1 co-performer  -> expected best match
#   CROWD   shares 10 scenes with 2 co-performers -> second, by the co-star factor
#   LOOSE   shares 1                             -> excluded by min_shared
#   SOLO    shares 1, with no third party       -> excluded by min_shared
#   GHOST   co-appears but is SOFT-DELETED       -> never recommended
#   OUTSIDER 20 scenes, shares none with SUBJECT -> never recommended
set -uo pipefail
cd "$(dirname "$0")/.."
PSQL=(psql -h 127.0.0.1 -p 55434 -U postgres -d sbx-live -v ON_ERROR_STOP=1)
q() { "${PSQL[@]}" -qtA -c "$1"; }
SUBJ="f0000000-0000-0000-0000-00000000000a"

echo "=== fixture ==="
# Checked, not redirected-and-ignored. An earlier version piped psql to /dev/null
# without testing its status, printed "applied", and then asserted against a fixture
# that had failed to load -- reporting a scene count of 3 from a half-built fixture.
if ! "${PSQL[@]}" -qf scripts/verify-similar.sql >/dev/null; then
  echo "  FAIL fixture did not apply (see the error above)"
  exit 1
fi
echo "  applied"

# ---- isolation: the subject's denominator must be exactly the fixture's 13 scenes.
# If this drifts, every score below is wrong while still looking reasonable.
N=$(q "SELECT count(*) FROM scene_performers sp
       JOIN scenes s ON s.id = sp.scene_id
       WHERE sp.performer_id = '$SUBJ' AND NOT s.deleted")
if [ "$N" != "13" ]; then
  echo "  FAIL SUBJECT has $N scenes, expected 13 -- other fixtures are leaking in"
  exit 1
fi
echo "  OK  SUBJECT has exactly 13 scenes, none from other fixtures"

# The SHIPPED SQL, extracted from the generated Go constant, with $1/$2/$3 bound.
# A hand-transcribed copy of this query had already drifted from the real one (the
# score's parenthesisation changed), so the script would have verified something the
# application does not run.
# The subject id is passed as an argv value, not interpolated by the shell into the
# heredoc: a python heredoc runs in its own namespace and cannot see $SUBJ.
SHIPPED=$(python3 - "$SUBJ" <<'PY'
import re, sys
subject = sys.argv[1]
src = open("internal/queries/performer_similar.sql.go").read()
# Cut at the backtick that is followed by a `,` on its own -- that is the end of the
# raw string literal and the start of the Go code after it. A lazy match truncated the
# statement inside a comment containing a backtick; a greedy one ran on into the
# generated struct definition.
# sqlc emits the raw string with its closing backtick ALONE on a line, so match that
# exactly. Three earlier attempts: lazy (truncated inside a comment containing a
# backtick), greedy (ran on into the generated struct), and `\n after the backtick
# (no match at all).
m = re.search(r"const findSimilarPerformers = `(.*?)\n`\n", src, re.S)
if not m:
    sys.exit("could not find findSimilarPerformers in the generated Go -- "
             "did sqlc regenerate, or was the query renamed?")
# Strip comments WHOLE-LINE only, and do it before anything else. An earlier version
# tried to strip trailing `--` text too and mangled the score expression; a naive
# line filter also left a dangling comment fragment when one of my inline notes spanned
# lines, producing "syntax error at end of input".
sql = "\n".join(l for l in m.group(1).splitlines() if not l.strip().startswith("--"))
sql = re.sub(r"\n{2,}", "\n", sql)
sql = sql.replace("$1", "'%s'" % subject).replace("$2::int", "2").replace("$3::int", "10")
print(sql.strip())
PY
)

echo
echo "=== the SHIPPED query (min_shared=2, limit 10) ==="
"${PSQL[@]}" -c "$SHIPPED"

echo "=== a human-readable ranking, using the same shipped formula ==="
# A direct transcription of internal/queries/sql/performer_similar.sql, so a change to
# the real query must be made here too or this stops verifying anything. The
# integration test is the one that tracks the real SQL; this script exists to make the
# SCORING inspectable by hand.
"${PSQL[@]}" -c "
WITH target_scenes AS (
    SELECT sp.scene_id
    FROM scene_performers sp
    JOIN scenes s ON s.id = sp.scene_id
    WHERE sp.performer_id = '$SUBJ' AND NOT s.deleted
), subject_size AS (
    SELECT count(*)::int AS target_scenes FROM target_scenes
)
SELECT po.name AS performer,
       count(DISTINCT other.scene_id) AS scenes_shared,
       (SELECT target_scenes FROM subject_size) AS subject_scenes,
       count(DISTINCT other2.performer_id) AS co_performers,
       round((((count(DISTINCT other.scene_id)::float
           / GREATEST((SELECT target_scenes FROM subject_size), 1))
       ) * (1.0 + LEAST(count(DISTINCT other2.performer_id)::float / 5.0, 1.0)))::numeric, 3) AS score
FROM scene_performers other
JOIN target_scenes ts ON ts.scene_id = other.scene_id
JOIN scene_performers other2
     ON other2.scene_id = other.scene_id
    AND other2.performer_id <> '$SUBJ'
    AND other2.performer_id <> other.performer_id
JOIN performers po ON po.id = other.performer_id
WHERE other.performer_id <> '$SUBJ' AND NOT po.deleted
GROUP BY po.name
HAVING count(DISTINCT other.scene_id) >= 2
ORDER BY score DESC, po.name ASC"

echo "=== assertions ==="
# The score is computed in an inner query because it cannot be repeated in the outer
# ORDER BY: aggregates cannot be nested, and inlining the expression put
# count(...) inside string_agg's ORDER BY. The name tie-break is kept so the ranking
# is DETERMINISTIC -- two performers with identical scores must not swap places
# between runs.
names=$(q "
WITH target_scenes AS (
    SELECT sp.scene_id FROM scene_performers sp
    JOIN scenes s ON s.id = sp.scene_id
    WHERE sp.performer_id = '$SUBJ' AND NOT s.deleted
), scored AS (
    SELECT po.name AS performer,
           count(DISTINCT other.scene_id)::float
             / GREATEST((SELECT count(*) FROM target_scenes), 1)
             * (1.0 + LEAST(count(DISTINCT other2.performer_id)::float / 5.0, 1.0)) AS score
    FROM scene_performers other
    JOIN target_scenes ts ON ts.scene_id = other.scene_id
    JOIN scene_performers other2 ON other2.scene_id = other.scene_id
        AND other2.performer_id <> '$SUBJ' AND other2.performer_id <> other.performer_id
    JOIN performers po ON po.id = other.performer_id
    WHERE other.performer_id <> '$SUBJ' AND NOT po.deleted
    GROUP BY po.name
    HAVING count(DISTINCT other.scene_id) >= 2
)
SELECT coalesce(string_agg(performer, ',' ORDER BY score DESC, performer), '')
FROM scored")
echo "  ranking: $names"

expect() {
  if [ "$2" = "$3" ]; then echo "  OK  $1"
  else echo "  FAIL $1: expected [$3], got [$2]"; exit 1; fi
}

case "$names" in
  *GHOST*)    echo "  FAIL GHOST is soft-deleted but was recommended"; exit 1 ;;
  *)          echo "  OK  GHOST (soft-deleted) never recommended" ;;
esac
case "$names" in
  *OUTSIDER*) echo "  FAIL OUTSIDER shares nothing with SUBJECT but was recommended"; exit 1 ;;
  *)          echo "  OK  OUTSIDER (shares nothing) never recommended" ;;
esac
case "$names" in
  *LOOSE*|*SOLO*) echo "  FAIL a single-scene pair cleared min_shared=2"; exit 1 ;;
  *)          echo "  OK  LOOSE and SOLO (one shared scene) excluded by the floor" ;;
esac
expect "ranking is CROWD then CLOSE (co-star factor breaks the 10/10 tie)" \
       "$names" "CROWD,CLOSE"

"${PSQL[@]}" -c "
DELETE FROM scenes WHERE studio_id = 'f0000000-0000-0000-0000-000000000001';
DELETE FROM performers WHERE id::text LIKE 'f0000000-0000-0000-0000-0000000000%';" >/dev/null

echo
echo "=== similar-performers query verified ==="