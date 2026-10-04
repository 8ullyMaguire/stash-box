#!/usr/bin/env python3
"""Behaviour checks for §7.24.1 (migration 96) and §7.24.6 (migration 99) against a real
database.

Why a script rather than Go integration tests: `pg_search` is unavailable on this host, so
`go test -tags=integration` cannot run at all here -- the repo's own TestEloMigrationApplied
fails identically, which is why docs/goal-check.py reports C6b as UNKNOWN rather than PASS. So
the schema is built by docs/apply-all-scratch.py and the behaviours are observed with psql.

A check that FAILS is a finding. A check that fails for a reason unrelated to the property
under test is worse than useless, so every check prints the error text and the caller reads it.
That is not decoration: five checks in this phase first reported the right answer for the
wrong reason (a syntax error, a duplicate fixture id, a stale user name).

Usage:
    python3 docs/apply-all-scratch.py sb_check
    python3 docs/verify-724-schema.py sb_check
"""
import os
import subprocess
import sys
import uuid

HOST = os.environ.get("VERIFY_HOST", "127.0.0.1")
# The port and role were hardcoded to `gravity`, which is another project's database on
# this host. The scratch database this script checks is created by
# docs/apply-all-scratch.py in whatever PostgreSQL the caller points it at -- so the two
# disagreed, and the failure named a role rather than a missing database.
PORT = os.environ.get("VERIFY_PORT", "5432")
USER = os.environ.get("VERIFY_USER", "postgres")
# One source of truth for the credential: the environment. See the note in
# apply-all-scratch.py -- a hardcoded fallback here made the parent and the child disagree.
ENV = dict(os.environ)
if not ENV.get("PGPASSWORD"):
    sys.exit("set PGPASSWORD for the local postgres role")

GREEN, RED, YELLOW, RESET = "\033[32m", "\033[31m", "\033[33m", "\033[0m"


def run(db, sql):
    r = subprocess.run(
        ["psql", "-h", HOST, "-p", PORT, "-U", USER, "-d", db, "-v", "ON_ERROR_STOP=1", "-q", "-c", sql],
        env=ENV, capture_output=True, text=True)
    return r.returncode == 0, (r.stderr or "").strip().split("\n")[0]


class Checks:
    def __init__(self, db):
        self.db = db
        self.failures = []

    def expect(self, label, sql, should_pass, setup=""):
        """Run one statement (plus optional setup) and report whether it was accepted.

        %U% in either argument is replaced with a fresh unique user name, so a check never
        collides with a row an earlier check or an earlier run left behind. Substituting here
        rather than at each call site is what stopped the repeated "checks passed because they
        refused on users_name_key" failures.
        """
        token = "k" + uuid.uuid4().hex[:10]
        passed, err = run(self.db, (setup + sql).replace("%U%", token))
        ok = passed == should_pass
        mark = GREEN + "ok  " + RESET if ok else RED + "BAD " + RESET
        got = "accepted" if passed else "refused"
        want = "accepted" if should_pass else "refused"
        print("%s %-58s %s (want %s)" % (mark, label, got, want))
        if not ok:
            self.failures.append(label)
        elif err and passed:
            # Accepted but noisy is worth seeing; it means a notice fired, not a refusal.
            print("     %s%s%s" % (YELLOW, err[:100], RESET))
        return passed

    def equal(self, label, sql, want, setup=""):
        # `setup` exists because §7.24.1's cascade checks need a user row to exist before
        # the statement runs (asserted_by is NOT NULL REFERENCES users), and a check that
        # inlines its own INSERT has to repeat that INSERT in three places. Returning early
        # on a failed setup rather than asserting: a missing fixture that then produces the
        # right count by accident is the failure mode this harness exists to prevent.
        if setup:
            r0 = subprocess.run(
                ["psql", "-h", HOST, "-p", PORT, "-U", USER, "-d", self.db, "-tAq",
                 "-v", "ON_ERROR_STOP=1", "-c", setup],
                env=ENV, capture_output=True, text=True)
            if r0.returncode != 0:
                print(RED + "BAD " + RESET + " %-58s setup failed: %s"
                      % (label, (r0.stderr or r0.stdout).strip()[:160]))
                self.failures.append(label + " (setup)")
                return False
        r = subprocess.run(["psql", "-h", HOST, "-p", PORT, "-U", USER, "-d", self.db, "-tAq", "-c", sql],
                           env=ENV, capture_output=True, text=True)
        got = r.stdout.strip()
        ok = got == str(want)
        mark = GREEN + "ok  " + RESET if ok else RED + "BAD " + RESET
        print("%s %-58s %r (want %r)" % (mark, label, got, want))
        if not ok:
            self.failures.append(label)
        return ok


def user_setup(name):
    return ("INSERT INTO users (id,name,password_hash,email,api_key,last_api_call,"
            "created_at,updated_at) VALUES (gen_random_uuid(),'%s','h','%s@e.invalid',"
            "'k-%s',NOW(),NOW(),NOW());" % (name, name, name))


def section(title):
    print("\n== %s" % title)


def main():
    """Rebuild the scratch database every run, then check.

    The rebuild is not optional. An earlier version took the database name as an argument and
    checked whatever was already there, so re-running it produced 17 failures that looked like
    schema regressions and were all stale rows: the UNIQUE keys correctly refused a second
    'duplicate_url' detector and a second assertion for an entity that already had one. The
    checks were right and the database was wrong.

    A verification script that is only correct on a virgin database is a trap, because the
    failures it then reports are indistinguishable from real ones.
    """
    db = sys.argv[1] if len(sys.argv) > 1 else "sb_check"
    # The child needs PGPASSWORD in ITS environment. Passing env= here and relying on the
    # parent's own environment leaves the child without it when the parent was invoked with the
    # password set in a way os.environ does not see -- and the symptom is "password
    # authentication failed for user gravity", which reads as a database problem rather than a
    # plumbing one.
    child_env = {**os.environ, "PGPASSWORD": ENV["PGPASSWORD"]}
    rebuild = subprocess.run([sys.executable, os.path.join(os.path.dirname(os.path.abspath(__file__)),
                                                           "apply-all-scratch.py"), db],
                             env=child_env, capture_output=True, text=True)
    print(rebuild.stdout.strip())
    if rebuild.returncode:
        print(RED + "could not build the schema -- every check below would be meaningless"
              + RESET)
        print(rebuild.stderr.strip()[:400])
        return 1
    c = Checks(db)

    # ---------------------------------------------------------------- §7.24.1
    section("§7.24.1 verified-unknown (migration 96)")

    U = lambda n: "(SELECT id FROM users WHERE name='%s')" % n
    FV = ("INSERT INTO field_verification_states "
          "(id,entity_type,entity_id,field,reason_code,asserted_by,asserted_at) ")

    c.expect("unknown reason code refused (FK to the registry)",
             FV + "SELECT gen_random_uuid(),'performer',gen_random_uuid(),'height',"
                  "'because_i_said_so'," + U("c1") + ",now();", False, user_setup("c1"))
    c.expect("NULL reason refused",
             FV + "SELECT gen_random_uuid(),'performer',gen_random_uuid(),'height',NULL,"
                  + U("c2") + ",now();", False, user_setup("c2"))
    c.expect("NULL asserted_by refused",
             FV + "SELECT gen_random_uuid(),'performer',gen_random_uuid(),'height',"
                  "'not_publicly_knowable',NULL,now();", False)
    c.expect("unknown asserted_by refused (FK to users)",
             FV + "SELECT gen_random_uuid(),'performer',gen_random_uuid(),'height',"
                  "'not_publicly_knowable',gen_random_uuid(),now();", False)
    c.expect("a well-formed assertion accepted",
             FV + "SELECT gen_random_uuid(),'performer',gen_random_uuid(),'height',"
                  "'not_publicly_knowable'," + U("c5") + ",now();", True, user_setup("c5"))
    # The duplicate needs ONE pinned entity_id: a per-row gen_random_uuid() would make the two
    # inserts different entities, so there is no duplicate and the check passes for free.
    c.expect("duplicate (entity_type, entity_id, field) refused",
             "CREATE TEMP TABLE _e AS SELECT gen_random_uuid() AS eid;\n"
             + FV + "SELECT gen_random_uuid(),'performer',eid,'height','not_publicly_knowable',"
             + U("c6") + ",now() FROM _e;\n"
             + FV + "SELECT gen_random_uuid(),'performer',eid,'height','not_yet_looked',"
             + U("c6") + ",now() FROM _e;", False, user_setup("c6"))
    c.expect("two different fields on one entity accepted",
             "CREATE TEMP TABLE _e AS SELECT gen_random_uuid() AS eid;\n"
             + FV + "SELECT gen_random_uuid(),'performer',eid,'height','not_publicly_knowable',"
             + U("c7") + ",now() FROM _e UNION ALL SELECT gen_random_uuid(),'performer',eid,"
             "'birth_date','not_publicly_knowable'," + U("c7") + ",now() FROM _e;",
             True, user_setup("c7"))
    c.expect("same field on two entities accepted",
             FV + "SELECT gen_random_uuid(),'performer',gen_random_uuid(),'height',"
                  "'not_publicly_knowable'," + U("c8") + ",now() UNION ALL SELECT "
                  "gen_random_uuid(),'performer',gen_random_uuid(),'height',"
                  "'not_publicly_knowable'," + U("c8") + ",now();", True, user_setup("c8"))
    c.expect("same field+id under two entity_types accepted",
             FV + "SELECT gen_random_uuid(),'performer',gen_random_uuid(),'height',"
                  "'not_publicly_knowable'," + U("c9") + ",now() UNION ALL SELECT "
                  "gen_random_uuid(),'scene',gen_random_uuid(),'height',"
                  "'not_publicly_knowable'," + U("c9") + ",now();", True, user_setup("c9"))
    c.expect("cited assertion accepted",
             "INSERT INTO field_verification_states (id,entity_type,entity_id,field,"
             "reason_code,asserted_by,asserted_at,citation_url) SELECT gen_random_uuid(),"
             "'performer',gen_random_uuid(),'height','not_publicly_knowable',"
             + U("c10") + ",now(),'https://example.org/x';", True, user_setup("c10"))
    c.expect("uncited assertion accepted (citation is optional by design)",
             FV + "SELECT gen_random_uuid(),'performer',gen_random_uuid(),'height',"
                  "'not_publicly_knowable'," + U("c11") + ",now();", True, user_setup("c11"))

    # The farm rule: not_yet_looked must NOT suppress, the other two MUST.
    c.equal("farm rule: exactly one reason has suppresses_gap = false",
            "SELECT count(*) FROM field_verification_reasons "
            "WHERE suppresses_gap IS DISTINCT FROM (code <> 'not_yet_looked');", 0)
    c.equal("not_yet_looked does not suppress",
            "SELECT suppresses_gap FROM field_verification_reasons WHERE code='not_yet_looked';",
            "f")
    c.equal("the closed reason set is exactly 3 codes",
            "SELECT count(*) FROM field_verification_reasons;", 3)

    # ------------------------------------------------ §7.24.1 cascade (migration 102)
    section("§7.24.1 entity cascade (migration 102)")

    # 96's header claims ON DELETE CASCADE from the entity, and `entity_id` being
    # polymorphic means no foreign key can carry it. These three checks are what that claim
    # looks like when it is actually true.
    #
    # Each needs its OWN entity ids. A single id reused across the checks would pass for
    # free: once the first delete has cascaded, there is nothing left for the second to
    # cascade, so a broken trigger would still see the count fall.
    c.equal("deleting a performer removes its assertion",
            "CREATE TEMP TABLE _p1 AS SELECT gen_random_uuid() AS pid;\n"
            "INSERT INTO performers (id,name,created_at,updated_at) "
            "SELECT pid,'cascade-probe-1',now(),now() FROM _p1;\n"
            + FV + "SELECT gen_random_uuid(),'performer',pid,'height',"
            "'not_publicly_knowable'," + U("k1") + ",now() FROM _p1;\n"
            "DELETE FROM performers WHERE id = (SELECT pid FROM _p1);\n"
            "SELECT count(*) FROM field_verification_states "
            "WHERE entity_type='performer' AND entity_id=(SELECT pid FROM _p1);",
            0, user_setup("k1"))

    # The discriminator. A trigger that ignored entity_type, or that matched on the id
    # alone, would delete this scene's assertion when the performer went -- and the check
    # above would still pass.
    c.equal("deleting a performer leaves another type's assertion alone",
            "CREATE TEMP TABLE _x AS SELECT gen_random_uuid() AS pid, "
            "gen_random_uuid() AS sid;\n"
            "INSERT INTO performers (id,name,created_at,updated_at) "
            "SELECT pid,'cascade-probe-2',now(),now() FROM _x;\n"
            "INSERT INTO scenes (id,title,created_at,updated_at) "
            "SELECT sid,'cascade-probe-2',now(),now() FROM _x;\n"
            + FV + "SELECT gen_random_uuid(),'performer',pid,'height',"
            "'not_publicly_knowable'," + U("k2") + ",now() FROM _x;\n"
            + FV + "SELECT gen_random_uuid(),'scene',sid,'title',"
            "'not_publicly_knowable'," + U("k2") + ",now() FROM _x;\n"
            "DELETE FROM performers WHERE id = (SELECT pid FROM _x);\n"
            "SELECT count(*) FROM field_verification_states "
            "WHERE entity_type='scene' AND entity_id=(SELECT sid FROM _x);",
            1, user_setup("k2"))

    c.equal("deleting the author still cascades (asserted_by, from 96)",
            "CREATE TEMP TABLE _a AS SELECT gen_random_uuid() AS aid;\n"
            "INSERT INTO users (id,name,password_hash,email,api_key,last_api_call,"
            "created_at,updated_at) SELECT aid,'cascade-author','x','ca@t.io',"
            "'k'||aid::text,now(),now(),now() FROM _a;\n"
            + FV + "SELECT gen_random_uuid(),'performer',gen_random_uuid(),'height',"
            "'not_publicly_knowable',aid,now() FROM _a;\n"
            "DELETE FROM users WHERE id = (SELECT aid FROM _a);\n"
            "SELECT count(*) FROM field_verification_states "
            "WHERE asserted_by=(SELECT aid FROM _a);",
            0)

    # TRUNCATE fires no row-level trigger. The integration suite truncates between
    # packages, so without a truncate trigger the assertions outlive every entity in the
    # table and the next package starts with rows pointing at nothing.
    c.equal("TRUNCATE performers clears assertions (no row-level trigger fires)",
            "CREATE TEMP TABLE _t AS SELECT gen_random_uuid() AS pid;\n"
            "INSERT INTO performers (id,name,created_at,updated_at) "
            "SELECT pid,'cascade-probe-3',now(),now() FROM _t;\n"
            + FV + "SELECT gen_random_uuid(),'performer',pid,'height',"
            "'not_publicly_knowable'," + U("k3") + ",now() FROM _t;\n"
            "TRUNCATE performers CASCADE;\n"
            "SELECT count(*) FROM field_verification_states;",
            0, user_setup("k3"))

    # ---------------------------------------------------------------- §7.24.2
    section("§7.24.2 expected totals (migration 97)")

    def total(kind, n, u, etype="studio"):
        return ("INSERT INTO expected_totals (entity_type,entity_id,kind,total,"
                "source_entity_type,source_url,asserted_by,asserted_at) SELECT '%s',"
                "gen_random_uuid(),'%s',%s,'studio','https://s.example/c',%s,now();"
                % (etype, kind, n, U(u)))

    c.expect("zero total refused (a zero is not a denominator)",
             total("scenes", "0", "t1"), False, user_setup("t1"))
    c.expect("negative total refused", total("scenes", "-5", "t2"), False, user_setup("t2"))
    c.expect("NULL total refused", total("scenes", "NULL", "t4"), False, user_setup("t4"))
    c.expect("good total accepted", total("scenes", "412", "t3"), True, user_setup("t3"))
    c.expect("NULL source_url refused (the rumour §7.24.2 bans)",
             total("scenes", "412", "t5").replace("'https://s.example/c'", "NULL"),
             False, user_setup("t5"))
    c.expect("NULL asserted_by refused",
             total("scenes", "412", "t6").replace(U("t6"), "NULL"), False, user_setup("t6"))
    c.expect("same studio + same kind refused (a correction must replace, not accumulate)",
             "CREATE TEMP TABLE _s AS SELECT gen_random_uuid() AS eid;\n"
             "INSERT INTO expected_totals (entity_type,entity_id,kind,total,"
             "source_entity_type,source_url,asserted_by,asserted_at) SELECT 'studio',eid,"
             "'scenes',412,'studio','https://s.example/c'," + U("t7") + ",now() FROM _s;\n"
             "INSERT INTO expected_totals (entity_type,entity_id,kind,total,"
             "source_entity_type,source_url,asserted_by,asserted_at) SELECT 'studio',eid,"
             "'scenes',380,'studio','https://s.example/c'," + U("t7") + ",now() FROM _s;",
             False, user_setup("t7"))
    c.expect("same studio, two kinds accepted (scenes and performers are not one number)",
             "CREATE TEMP TABLE _s AS SELECT gen_random_uuid() AS eid;\n"
             "INSERT INTO expected_totals (entity_type,entity_id,kind,total,"
             "source_entity_type,source_url,asserted_by,asserted_at) SELECT 'studio',eid,"
             "'scenes',412,'studio','https://s.example/c'," + U("t8") + ",now() FROM _s;\n"
             "INSERT INTO expected_totals (entity_type,entity_id,kind,total,"
             "source_entity_type,source_url,asserted_by,asserted_at) SELECT 'studio',eid,"
             "'performers',60,'studio','https://s.example/c'," + U("t8") + ",now() FROM _s;",
             True, user_setup("t8"))

    # ---------------------------------------------------------------- §7.24.4
    section("§7.24.4 lint quest definitions (migration 98)")

    def det(slug, builtin="TRUE", author="NULL", desc="fix the thing"):
        return ("INSERT INTO lint_quest_definitions (slug,description,is_builtin,authored_by) "
                "VALUES ('%s','%s',%s,%s);\n" % (slug, desc, builtin, author))

    c.expect("builtin detector with no author accepted", det("duplicate_url"), True)
    c.expect("authored detector with an author accepted",
             det("alias_collision", "FALSE", U("d2")), True, user_setup("d2"))
    c.expect("builtin WITH an author refused (builtins are nobody's)",
             det("dup_a", "TRUE", U("d3")), False, user_setup("d3"))
    c.expect("authored WITHOUT an author refused (a rule nobody stands behind)",
             det("local_rule", "FALSE"), False)
    c.expect("blank description refused", det("blank_desc", desc="   "), False)
    c.expect("NULL description refused", det("null_desc", desc="x").replace("'x'", "NULL"), False)
    c.expect("uppercase slug refused (a rename orphans every candidate row)",
             det("DuplicateUrl"), False)
    c.expect("dashed slug refused (the slug names a Go function)",
             det("dup-url"), False)
    c.expect("duplicate slug refused (a curator must not shadow a shipped detector)",
             det("duplicate_url"), False)
    c.expect("negative bounty refused",
             det("neg_bounty") + "UPDATE lint_quest_definitions SET bounty_points=-1 "
             "WHERE slug='neg_bounty';", False)
    c.expect("absurd bounty refused (a typo must not pay a year of reputation)",
             det("big_bounty") + "UPDATE lint_quest_definitions SET bounty_points=999999 "
             "WHERE slug='big_bounty';", False)
    c.expect("zero bounty accepted ('no bounty' is an answer, not a missing value)",
             det("free_rule") + "UPDATE lint_quest_definitions SET bounty_points=0 "
             "WHERE slug='free_rule';", True)

    # ---------------------------------------------------------------- §7.24.3
    section("§7.24.3 bounty pricing audit (migration 100)")

    Q = "(SELECT id FROM authored_quests LIMIT 1)"

    def price(sug, fin, ovr, src, u, quest=None):
        return ("INSERT INTO bounty_pricing_audit (quest_id,suggested_points,final_points,"
                "was_overridden,price_source,priced_by) VALUES (%s,%s,%s,%s,%s,%s);\n"
                % (quest or Q, sug, fin, ovr, src, U(u)))

    quest_setup = ("INSERT INTO authored_quests (id,entity_type,field,target,reason,created_at) "
                   "VALUES (gen_random_uuid(),'performer','birthdate',5,'for the archive',NOW());")

    c.expect("author_declared, not overridden accepted",
             price("0", "42", "FALSE", "'author_declared'", "%U%"), True,
             user_setup("%U%") + quest_setup)
    c.expect("author_overrode, flagged, points differ accepted",
             price("40", "45", "TRUE", "'author_overrode'", "%U%"), True,
             user_setup("%U%") + quest_setup)
    c.expect("generated, not overridden, points equal accepted",
             price("40", "40", "FALSE", "'generated'", "%U%"), True,
             user_setup("%U%") + quest_setup)
    # The next three ARE §7.24.3's clause: "the evidence that the generator never wrote a bounty."
    c.expect("generated but flagged overridden REFUSED (a generator cannot be overridden)",
             price("40", "45", "TRUE", "'generated'", "%U%"), False,
             user_setup("%U%") + quest_setup)
    c.expect("generated with points CHANGED REFUSED (the generator wrote a bounty)",
             price("40", "45", "FALSE", "'generated'", "%U%"), False,
             user_setup("%U%") + quest_setup)
    c.expect("author_overrode but NOT flagged REFUSED (the override is the fact calibration reads)",
             price("40", "45", "FALSE", "'author_overrode'", "%U%"), False,
             user_setup("%U%") + quest_setup)
    c.expect("author_declared but flagged overridden REFUSED",
             price("40", "45", "TRUE", "'author_declared'", "%U%"), False,
             user_setup("%U%") + quest_setup)
    c.expect("unknown price_source REFUSED (the set is closed, not free text)",
             price("40", "40", "FALSE", "'whatever'", "%U%"), False,
             user_setup("%U%") + quest_setup)
    c.expect("NULL price_source REFUSED",
             price("40", "40", "FALSE", "NULL", "%U%"), False,
             user_setup("%U%") + quest_setup)
    c.expect("NULL priced_by REFUSED (who decided must be answerable)",
             price("40", "40", "FALSE", "'generated'", "NULL"), False, quest_setup)
    c.expect("absurd final_points REFUSED",
             price("40", "99999", "TRUE", "'author_overrode'", "%U%"), False,
             user_setup("%U%") + quest_setup)
    c.expect("unknown quest_id REFUSED",
             price("40", "40", "FALSE", "'generated'", "%U%", quest="gen_random_uuid()"),
             False, user_setup("%U%"))

    # ---------------------------------------------------------------- §7.24.6
    section("§7.24.6 fingerprint corroboration (migration 99)")
    # The view is pure derivation, so these checks are about the LOGIC: each of §7.24.6's two
    # stated conditions must be necessary, and satisfying both must be sufficient.

    def scene(title):
        # The parens are load-bearing: this string is concatenated onto "WHERE scene_id=" and a
        # bare "SELECT id FROM scenes WHERE title=..." is a syntax error there. The first run of
        # this script reported six §7.24.6 failures against a view that was in fact correct on
        # all three fixtures, because the check never reached it.
        return "(SELECT id FROM scenes WHERE title='%s')" % title

    setup_fx = (
        "INSERT INTO users (id,name,password_hash,email,api_key,last_api_call,created_at,"
        "updated_at) VALUES (gen_random_uuid(),'fx','h','fx@e.invalid','kfx',NOW(),NOW(),NOW()) "
        "ON CONFLICT DO NOTHING;\n"
        "INSERT INTO fingerprints (id,hash,algorithm) VALUES "
        "(901,901,'phash'),(902,902,'phash'),(903,903,'dhash'),(904,904,'dhash') "
        "ON CONFLICT (id) DO NOTHING;\n"
        "INSERT INTO scenes (id,title,details,date,created_at,updated_at) VALUES "
        "(gen_random_uuid(),'fx_one_user_two_algo','',NULL,NOW(),NOW()),"
        "(gen_random_uuid(),'fx_six_users_one_algo','',NULL,NOW(),NOW()),"
        "(gen_random_uuid(),'fx_two_users_two_algo','',NULL,NOW(),NOW());\n")

    # Fixture A: ONE user, FOUR submissions, TWO algorithms. This is §7.24.6's first weak
    # reading -- "only one hash algorithm" is false here, so a detector that checked only
    # algorithms would call it corroborated. It must not be.
    #
    # The fingerprint ids come FROM the VALUES list. An earlier version listed them in a VALUES
    # clause and then joined a subquery that returned all four of them, which made v.fid
    # uncorrelated and emitted all sixteen pairs -- then died on the unique key. A fixture that
    # cannot be built proves nothing about the schema it was meant to probe.
    setup_fx += (
        "INSERT INTO scene_fingerprints (fingerprint_id,scene_id,user_id,duration) "
        "SELECT v.fid, s.id, u.id, 100 "
        "FROM (VALUES (901),(902),(903),(904)) v(fid) "
        "JOIN fingerprints f ON f.id = v.fid "
        "CROSS JOIN LATERAL (SELECT id FROM users WHERE name='fx') u "
        "CROSS JOIN (SELECT id FROM scenes WHERE title='fx_one_user_two_algo') s;\n")
    # Fixture B: SIX users, ONE algorithm -- §7.24.6's other weak reading.
    setup_fx += (
        "INSERT INTO scene_fingerprints (fingerprint_id,scene_id,user_id,duration) "
        "SELECT 901, s.id, u.id, 100 FROM generate_series(1,6) n, "
        "LATERAL (SELECT id FROM users ORDER BY name LIMIT 1 OFFSET (n-1)) u, "
        "(SELECT id FROM scenes WHERE title='fx_six_users_one_algo') s;\n")
    # Fixture C: TWO users, TWO algorithms -- genuine corroboration, must be true.
    setup_fx += (
        "INSERT INTO scene_fingerprints (fingerprint_id,scene_id,user_id,duration) "
        "SELECT v.fid, s.id, u.id, 100 FROM (VALUES (901,1),(903,2)) v(fid,uidx) "
        "JOIN fingerprints f ON f.id = v.fid "
        "CROSS JOIN LATERAL (SELECT id FROM users ORDER BY name LIMIT 1 OFFSET (v.uidx-1)) u "
        "CROSS JOIN (SELECT id FROM scenes WHERE title='fx_two_users_two_algo') s;\n")

    # Fixture rows are keyed by explicit ids and scene titles, so the script is re-runnable
    # against the same scratch database: drop any previous fixture first. Without this the
    # second run dies on scene_fingerprints_scene_user_fp_key and reports a schema failure
    # that is actually a dirty fixture -- which is the exact confusion this file exists to
    # avoid.
    run(db, "DELETE FROM scene_fingerprints WHERE scene_id IN "
            r"(SELECT id FROM scenes WHERE title LIKE 'fx\_%');")
    run(db, r"DELETE FROM scenes WHERE title LIKE 'fx\_%';")
    run(db, "DELETE FROM fingerprints WHERE id BETWEEN 901 AND 904;")

    ok, err = run(db, setup_fx)
    if not ok:
        print(RED + "BAD " + RESET + "could not build the corroboration fixture: " + err[:200])
        c.failures.append("corroboration fixture")
    else:
        c.equal("one user, two algorithms -> NOT corroborated",
                "SELECT is_corroborated FROM fingerprint_corroboration "
                "WHERE scene_id=" + scene("fx_one_user_two_algo") + ";", "f")
        c.equal("  ...and it has algorithm_count = 2, so the flag is not just 'one algorithm'",
                "SELECT algorithm_count FROM fingerprint_corroboration "
                "WHERE scene_id=" + scene("fx_one_user_two_algo") + ";", "2")
        c.equal("six users, one algorithm -> NOT corroborated",
                "SELECT is_corroborated FROM fingerprint_corroboration "
                "WHERE scene_id=" + scene("fx_six_users_one_algo") + ";", "f")
        c.equal("  ...and it has submission_count = 6, so the flag is not just 'one submission'",
                "SELECT submission_count FROM fingerprint_corroboration "
                "WHERE scene_id=" + scene("fx_six_users_one_algo") + ";", "6")
        c.equal("two users, two algorithms -> corroborated",
                "SELECT is_corroborated FROM fingerprint_corroboration "
                "WHERE scene_id=" + scene("fx_two_users_two_algo") + ";", "t")
        # A scene with no fingerprint rows gets NO row from the view -- it is not a row with
        # is_corroborated = false. That distinction is §7.24.1's "absence means missing" rule
        # applied to a derived value: the caller must be able to tell "unknown" from "no", and
        # a view that emitted false for every scene would erase that difference.
        run(db, "INSERT INTO scenes (id,title,details,date,created_at,updated_at) VALUES "
                "(gen_random_uuid(),'fx_no_fingerprints','',NULL,NOW(),NOW());")
        c.equal("a scene with no fingerprints gets no row at all (absent, not false)",
                "SELECT count(*) FROM fingerprint_corroboration WHERE scene_id="
                "(SELECT id FROM scenes WHERE title='fx_no_fingerprints');", "0")

    print()
    if c.failures:
        print(RED + "%d check(s) disagree with the spec:" % len(c.failures) + RESET)
        for f in c.failures:
            print("  - " + f)
        return 1
    print(GREEN + "every behaviour matches the spec" + RESET)
    return 0


if __name__ == "__main__":
    sys.exit(main())