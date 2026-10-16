-- SPEC §7.24.4: data-lint quests -- the detectors.
--
-- Migration 98 is the REGISTRY and says the SQL lives here: "the SQL lives in internal/queries/ as
-- a named Go function, and this row says which detectors exist, are enabled, and cost what."
-- So each detector below is a named query, and internal/service/lint maps a registry slug onto it
-- by name. A slug in the registry that names no query here is refused by the service at startup,
-- which is the only thing that keeps the two halves from disagreeing.
--
-- THE SAME SQL DOUBLES AS AN IMPORT VALIDATION PASS, which §7.24.4 calls "the more valuable half
-- of this item": every detector takes the entity as its own column so a caller can ask "did THIS
-- import introduce a problem", not only "what is wrong with the archive".
--
-- THREE RULES EVERY DETECTOR FOLLOWS, and each is a claim about what lint means:
--
--   ONE, a SOFT-DELETED ENTITY IS NOT LINT. `deleted` is how this schema retires things, and a
--   quest asking a curator to fix a performer nobody can read is a wasted work item. Every arm
--   filters it. (Redirects are not lint either -- an entity that was merged away has a redirect
--   row and no name collision to fix.)
--
--   TWO, ONE ROW PER ENTITY TO FIX, with the detail that says WHY. A detector that returned two
--   rows for one performer would double that quest's apparent size, and `LIMIT 5` would ask for
--   five performers and get three.
--
--   THREE, EVERY DETECTOR TAKES AN OPTIONAL ENTITY FILTER, which is what makes the import validation
--   pass exact rather than lucky. Filtering in Go after a bounded query would answer "what is wrong
--   with the archive, among the first N findings" -- an archive large enough to fill the limit would
--   silently stop reporting the import that was just made. `sqlc.narg(entity_ids)` is NULL for a
--   board run and a list of ids for a validation pass.
--
--   FOUR, THE LIMIT IS THE CALLER'S. Every query takes `$1` as its row cap, so a board that shows
--   five and a sweep that walks fifty thousand ask the same SQL the same question. A hard-coded
--   LIMIT inside a detector is how a lint silently stops finding things as the archive grows.
--
-- A detector's TRUE POSITIVE RATE is not knowable from here, and that is why the descriptions in
-- internal/service/lint are written as what is WRONG with the record rather than what the query
-- does: a curator decides whether to work a quest from the description, not from the SQL.

-- name: ListLintQuestDefinitions :many
SELECT * FROM lint_quest_definitions ORDER BY slug;

-- name: ListEnabledLintQuestDefinitions :many
SELECT * FROM lint_quest_definitions WHERE enabled ORDER BY slug;

-- name: GetLintQuestDefinition :one
SELECT * FROM lint_quest_definitions WHERE slug = $1;

-- name: InsertBuiltinLintQuestDefinition :one
-- Idempotent, and deliberately DO NOTHING rather than DO UPDATE: a builtin row an operator has
-- switched OFF or priced differently is an operator decision, and re-seeding it on every start
-- would quietly undo it. Returns no row when the definition already exists, which the service
-- reads as "already seeded" rather than as a failure.
INSERT INTO lint_quest_definitions (slug, description, bounty_points, is_builtin, authored_by)
VALUES ($1, $2, $3, TRUE, NULL)
ON CONFLICT (slug) DO NOTHING
RETURNING *;

-- name: LintDuplicateSceneURLs :many
-- The same URL recorded on two different scenes. One of them is usually a duplicate record that
-- belongs merged rather than a scene with a wrong URL, which is why the detail names how many
-- scenes share it: two is a pair to look at, forty is a studio's whole catalogue pointing at one
-- aggregator page.
SELECT su.scene_id AS entity_id,
       s.title AS entity_name,
       su.url AS detail,
       (SELECT count(DISTINCT x.scene_id)
          FROM scene_urls x
          JOIN scenes xs ON xs.id = x.scene_id AND NOT xs.deleted
         WHERE lower(x.url) = lower(su.url)) AS shared_with
  FROM scene_urls su
  JOIN scenes s ON s.id = su.scene_id AND NOT s.deleted
 WHERE EXISTS (SELECT 1
                 FROM scene_urls o
                 JOIN scenes os ON os.id = o.scene_id AND NOT os.deleted
                WHERE lower(o.url) = lower(su.url)
                  AND o.scene_id <> su.scene_id)
   -- A per-record answer returns the whole FINDING, not just the record it was asked about: with ids
   -- given, every record in a group one of those ids belongs to is in scope. A duplicate whose other
   -- half you cannot see is not actionable -- the merge it leads to needs both ids -- and it still
   -- answers about nothing else, because only groups that involve a given id are in scope at all.
   AND (sqlc.narg(entity_ids)::uuid[] IS NULL
        OR EXISTS (SELECT 1
                     FROM scene_urls q
                    WHERE q.scene_id = ANY(sqlc.narg(entity_ids)::uuid[])
                      AND lower(q.url) = lower(su.url)))
 ORDER BY lower(su.url), su.scene_id
 LIMIT $1;

-- name: LintDuplicatePerformerURLs :many
-- The same, for performers. Kept as its own detector rather than a parameterised one: the two
-- entity types have different consequences (a duplicated performer URL is usually one person
-- entered twice, a duplicated scene URL is usually one scene catalogued twice by two sites) and
-- a curator working one does not want the other mixed into the same quest.
SELECT pu.performer_id AS entity_id,
       p.name AS entity_name,
       pu.url AS detail,
       (SELECT count(DISTINCT x.performer_id)
          FROM performer_urls x
          JOIN performers xp ON xp.id = x.performer_id AND NOT xp.deleted
         WHERE lower(x.url) = lower(pu.url)) AS shared_with
  FROM performer_urls pu
  JOIN performers p ON p.id = pu.performer_id AND NOT p.deleted
 WHERE EXISTS (SELECT 1
                 FROM performer_urls o
                 JOIN performers op ON op.id = o.performer_id AND NOT op.deleted
                WHERE lower(o.url) = lower(pu.url)
                  AND o.performer_id <> pu.performer_id)
   -- A per-record answer returns the whole FINDING, not just the record it was asked about: with ids
   -- given, every record in a group one of those ids belongs to is in scope. A duplicate whose other
   -- half you cannot see is not actionable -- the merge it leads to needs both ids -- and it still
   -- answers about nothing else, because only groups that involve a given id are in scope at all.
   AND (sqlc.narg(entity_ids)::uuid[] IS NULL
        OR EXISTS (SELECT 1
                     FROM performer_urls q
                    WHERE q.performer_id = ANY(sqlc.narg(entity_ids)::uuid[])
                      AND lower(q.url) = lower(pu.url)))
 ORDER BY lower(pu.url), pu.performer_id
 LIMIT $1;

-- name: LintPerformerAliasCollisions :many
-- An ALIAS that is another performer's name or alias.
--
-- The filter is the load-bearing part: a name colliding with a name is NOT lint, because two
-- performers may legitimately share a name (disambiguation is how this schema separates them) and
-- a detector that flagged every "Alex" would bury the real findings. An alias claiming another
-- performer's identity is a different thing: it asserts a merge that nobody voted on. §7.24.4 is
-- explicit that this emits a quest and NEVER auto-merges -- the merge path is the consensus flow
-- in internal/service/edit, and a lint that merged would be an unattributable edit.
WITH other AS (
    SELECT id AS performer_id, name AS claim, 'name' AS kind FROM performers WHERE NOT deleted
    UNION ALL
    SELECT performer_id, alias, 'alias' FROM performer_aliases
),
-- A LOWERED CLAIM THAT ONLY ONE LIVE PERFORMER MAKES CANNOT BE A COLLISION, and saying so first is
-- what makes this detector usable. The first version compared every alias against every name and every
-- other alias; on a real archive (112,391 performers, 181,896 aliases) the planner chose a nested loop
-- that probed the index once per performer and the detector took 37.5s. This narrowing is an AGGREGATE,
-- which the planner cannot inline away or reorder around, so the join that follows only ever sees the
-- claims that are actually contested -- the handful a curator cares about.
--
-- count(DISTINCT performer_id), not count(*): one performer whose own alias matches their own name has
-- claimed nothing twice, and the `o.performer_id <> c.performer_id` condition below has always said so.
contended AS (
    SELECT lower(claim) AS lc
      FROM other
     GROUP BY lower(claim)
    HAVING count(DISTINCT performer_id) > 1
),
-- Only the ALIAS side is reported: an alias asserting somebody else's identity is the defect, while a
-- name that matches another performer's name is what disambiguation is for.
alias_claim AS (
    SELECT a.performer_id, a.alias AS claim
      FROM performer_aliases a
      JOIN contended c ON c.lc = lower(a.alias)
)
SELECT c.performer_id AS entity_id,
       p.name AS entity_name,
       (c.claim || ' is also the ' || o.kind || ' of ' || op.name)::text AS detail
  FROM alias_claim c
  JOIN performers p ON p.id = c.performer_id AND NOT p.deleted
  JOIN other o ON lower(o.claim) = lower(c.claim) AND o.performer_id <> c.performer_id
  JOIN performers op ON op.id = o.performer_id AND NOT op.deleted
 -- A per-record answer covers BOTH sides of the contested claim: the alias holder is the row that gets
 -- reported (see above), and the performer whose name or alias is being claimed is the case a record
 -- page must not stay silent about -- "nothing is wrong with this record" is wrong when somebody else's
 -- alias asserts this person's identity. The reported rows are still the alias side, so the candidate a
 -- curator sees is always the alias holder: the record to correct, and the other half of the merge.
 WHERE (sqlc.narg(entity_ids)::uuid[] IS NULL
        OR c.performer_id = ANY(sqlc.narg(entity_ids)::uuid[])
        OR lower(c.claim) IN (SELECT lower(o2.claim) FROM other o2
                               WHERE o2.performer_id = ANY(sqlc.narg(entity_ids)::uuid[])))
 ORDER BY lower(c.claim), c.performer_id
 LIMIT $1;

-- name: LintImpossibleSceneDates :many
-- A scene date that cannot be true: in the future, or before one of its performers was born.
--
-- Risk is allowed to trail reality (a scene dated tomorrow is a scheduled release), so the future
-- arm is not "any date after today" but "a date that has not happened and is not imminent" -- $2
-- days of slack, passed by the service rather than written here, because "how far ahead is a
-- mistake" is a policy an operator may want to tune.
--
-- BOTH DATES ARE TEXT, and it is not an oversight. Migration 42 dropped `date_accuracy` and folded
-- the precision into the value, so a date is 'YYYY', 'YYYY-MM' or 'YYYY-MM-DD' -- a scene "sometime in
-- 2015" is the string '2015'. That makes the textual comparison CORRECT (in this format shorter is
-- earlier, and '2015' < '2015-06-01' < '2016'), and it makes `s.date::date` WRONG: casting '2015'
-- fails at runtime, on a live database, for exactly the partial dates the schema exists to hold. The
-- first version of this query compared a text date to CURRENT_DATE and the database answered
-- `operator does not exist: text > date` -- which is the good version of that mistake, because a
-- cast would have passed every test whose fixtures happened to use full dates.
--
-- So the future arm compares against a string formatted the same way, and the birth-date arm compares
-- the two strings to each other. A year-accurate birthdate ('1990') is therefore never "after" any
-- scene in 1990, so this arm can be wrong in the direction of saying NOTHING, which is the direction
-- a lint should be wrong in.
--
-- The birth-date arm compares the two DATES as recorded, and its precision is the value's own:
-- migration 42 DROPPED `birthdate_accuracy` and folded the precision into the date, so '1990' is a
-- year-accurate birthdate stored as 1990-01-01. That means a scene from later in 1990 does not fire
-- (it is after January 1st), so this arm can be wrong in the direction of saying NOTHING -- and that
-- is the direction a lint should be wrong in. (An accuracy COLUMN would have been the natural thing
-- to write here; it does not exist, and reading a dropped column is a query that fails at runtime
-- on a live database rather than at review.)
-- TWO ARMS, WRITTEN AS ONE UNION RATHER THAN ONE OR, and the reason is measured. The first version
-- joined scenes to their performers and asked `future OR before-a-birthdate` in a single WHERE; on a
-- real archive (1,104,375 scenes) PostgreSQL answered that with a nested loop probing scene_performers
-- once per scene -- 1.1M index searches -- and the detector took 9.3s. Written as a union, each arm
-- gets the access path its own predicate deserves: the future arm is a scan of scenes by date, and the
-- birthdate arm is driven by whatever scene_performers holds.
--
-- DISTINCT because a scene with three performers all born after it is ONE finding, and the entity_ids
-- path (§7.24.4's import validation) would otherwise report the same scene several times. Two arms that
-- both fire on one scene stay two rows: they are two different reasons.
SELECT DISTINCT
       s.id AS entity_id,
       coalesce(s.title, '') AS entity_name,
       ('dated ' || s.date || ', in the future')::text AS detail
  FROM scenes s
 WHERE NOT s.deleted
   AND s.date IS NOT NULL
   AND s.date > to_char(CURRENT_DATE + sqlc.arg(days_ahead)::int, 'YYYY-MM-DD')
   AND (sqlc.narg(entity_ids)::uuid[] IS NULL OR s.id = ANY(sqlc.narg(entity_ids)::uuid[]))
UNION ALL
SELECT DISTINCT
       s.id AS entity_id,
       coalesce(s.title, '') AS entity_name,
       ('dated ' || s.date || ', before ' || p.name || ' born ' || p.birthdate)::text AS detail
  FROM scenes s
  JOIN scene_performers sp ON sp.scene_id = s.id
  JOIN performers p ON p.id = sp.performer_id AND NOT p.deleted
 WHERE NOT s.deleted
   AND s.date IS NOT NULL
   AND p.birthdate IS NOT NULL
   AND s.date < p.birthdate
   AND (sqlc.narg(entity_ids)::uuid[] IS NULL OR s.id = ANY(sqlc.narg(entity_ids)::uuid[]))
 ORDER BY entity_id
 LIMIT $1;

-- name: LintLowResolutionPerformerImages :many
-- A performer's picture whose SHORTER side is below $2 pixels. The shorter side rather than the
-- area, because a 4000x200 strip is not a usable performer image and an area threshold calls it
-- one.
--
-- `images.width`/`height` are NOT NULL in this schema, so "an image whose size was never measured"
-- is not a state a row can be in: the fixture that tried to build it was refused by the constraint.
-- The comparison is written without an explicit null guard because SQL already gives the answer a
-- lint wants -- `least(NULL, 5) < 300` is NULL, and a NULL predicate does not select the row -- and a
-- guard would be code claiming to handle a case the schema forbids. If that constraint is ever
-- relaxed, the behaviour is unchanged and this sentence becomes the note whoever relaxes it needs.
SELECT pi.performer_id AS entity_id,
       p.name AS entity_name,
       ('image ' || i.id::text || ' is ' || coalesce(i.width, 0)::text || 'x' ||
         coalesce(i.height, 0)::text)::text AS detail
  FROM performer_images pi
  JOIN performers p ON p.id = pi.performer_id AND NOT p.deleted
  JOIN images i ON i.id = pi.image_id
 WHERE least(i.width, i.height) < sqlc.arg(min_side)::int
   AND (sqlc.narg(entity_ids)::uuid[] IS NULL OR pi.performer_id = ANY(sqlc.narg(entity_ids)::uuid[]))
 ORDER BY least(i.width, i.height), pi.performer_id
 LIMIT $1;
