#!/usr/bin/env python3
"""Walk phase 16 slice 16a -- the transparency report -- on a LIVE instance.

Run it ON the host that serves the instance (`ssh thinkcentre 'cd ~/stash-box-deploy && python3
/tmp/walk-16-live.py'`), after deploying the binary that carries the report. Every verdict is a claim about the
DEPLOYED process and the DEPLOYED database.

WHAT THIS WALK IS FOR, and why a green suite is not the same thing. The report's whole value is that a reader
can CHECK it, so the walk does what a reader does: it fetches `GET /transparency/json` and recomputes every
single figure with its own SQL against the deployed database. A number that agrees with a test fixture and not
with the instance is the one failure mode a test cannot see.

  A. IT IS SERVED to somebody with no account at all -- public, unauthenticated, indexable, and stating this
    instance's content posture IN THE WORDS THE PROCESS LOGS (read from the same switch, not restated).
  B. EVERY FIGURE RECOMPUTED, each one compared against the deployed database by SQL written here. The
    mismatch list is the verdict, so a report that is wrong about one number says which one.
  C. THE WINDOW IS REAL, and this is the arm that cannot be faked: the walk plants a refusal and an audit row
    120 DAYS OLD into the live database, re-reads the report, and requires the IN-FORCE figure to move while
    the WINDOWED ones do not -- then removes them and requires the figures to come back.
  D. THE PAGE AND THE JSON AGREE. The HTML is for a person, the JSON for a checker, and they are computed by
    the same call; a page that disagrees with its own JSON is a page that will be misread.
  E. THE POLICY AND THE REPORT ARE A PAIR: the policy points at the report and the report points back at the
    policy, because one says what will happen and the other says what did.

WHAT IT CHANGES, AND PUTS BACK. Two rows in the live database (a refusal and its audit row), both named with a
`walk-16-` prefix and `sql-injected` reasons, both deleted by name; the run's last verdicts re-read the
figures and require them to be what they were before it started.

Usage:  python3 walk-16-live.py [base-url]
"""

import json
import os
import re
import subprocess
import sys
import urllib.error
import urllib.request
import uuid

BASE = sys.argv[1] if len(sys.argv) > 1 else "http://127.0.0.1:9998"
DB = "stash-box"
CONFIG = "/home/alvaro/stash-box-deploy/config/stash-box-config.yml"

RUN = uuid.uuid4().hex[:8]
# A host and a value that are unmistakably this run's, so the cleanup can find them by prefix and they can
# never collide with something an operator wrote. `.invalid` is reserved and can never resolve.
OLD_HOST = f"walk-16-{RUN}-old.invalid"
OLD_AGE_DAYS = 120
PREFIX = "walk-16-"

FAILURES = []


def psql(sql):
    out = subprocess.run(
        ["sudo", "docker", "exec", "-i", "stashbox-pg", "psql", "-U", "stashbox", "-d", DB, "-tAc", sql],
        capture_output=True, text=True,
    )
    if out.returncode != 0:
        print(f"!! psql refused: {out.stderr.strip()}")
        return ""
    return out.stdout.strip()


def http_get(url, accept="text/html"):
    req = urllib.request.Request(url, headers={"Accept": accept})
    try:
        with urllib.request.urlopen(req) as r:
            return r.status, dict(r.headers), r.read().decode()
    except urllib.error.HTTPError as e:
        return e.code, dict(e.headers), e.read().decode()


def check(label, ok, description):
    print(f"   VERDICT {label}: {'OK' if ok else 'FAILED'} -- {description}")
    if not ok:
        FAILURES.append(label)
    return ok


def report_json():
    status, _, body = http_get(BASE + "/transparency/json", accept="application/json")
    if status != 200:
        return None, f"HTTP {status}"
    try:
        return json.loads(body), ""
    except Exception as exc:
        return None, f"unparseable: {exc}"


def fmt(value):
    """The page's own grouping, so a figure and the page can be compared."""
    return f"{value:,}"


# The figures, each with the SQL that is its definition. Written HERE rather than imported, because a walk
# that imported the query under test would agree with it by construction -- it would recompute the answer on
# the same line, and a wrong report would be confirmed.
def figures(report):
    since = report["since"]
    in_force = report["in_force"]
    activity = report["activity"]
    return [
        ("blocks_in_force", in_force["blocks_in_force"],
         'select count(*) from content_source_block'),
        ("records_out_of_service", in_force["records_out_of_service"],
         "select count(*) from content_sources where status = 'dmca'"),
        ("appeals_filed", in_force["appeals_filed"],
         'select count(*) from content_source_block_appeal'),
        ("appeals_pending", in_force["appeals_pending"],
         'select count(*) from content_source_block_appeal where decided_at is null'),
        ("appeals_decided", in_force["appeals_decided"],
         'select count(*) from content_source_block_appeal where decided_at is not null'),
        ("appeals_granted", in_force["appeals_granted"],
         "select count(*) from content_source_block_appeal where outcome = 'granted'"),
        ("appeals_denied", in_force["appeals_denied"],
         "select count(*) from content_source_block_appeal where outcome = 'denied'"),
        ("blocks_recorded", activity["blocks_recorded"],
         f"select count(*) from content_source_block where created_at >= '{since}'::timestamptz"),
        ("appeals_filed_in_window", activity["appeals_filed"],
         f"select count(*) from content_source_block_appeal where filed_at >= '{since}'::timestamptz"),
        ("appeals_decided_in_window", activity["appeals_decided"],
         "select count(*) from content_source_block_appeal where decided_at is not null "
         f"and decided_at >= '{since}'::timestamptz"),
        ("refusals", activity["refusals"],
         "select count(*) from mod_audit where action in ('SOURCE_TAKEDOWN','SOURCE_BLOCK') "
         f"and (data ->> 'released') is distinct from 'true' and created_at >= '{since}'::timestamptz"),
        ("releases", activity["releases"],
         "select count(*) from mod_audit where action in ('SOURCE_TAKEDOWN','SOURCE_BLOCK') "
         f"and (data ->> 'released') = 'true' and created_at >= '{since}'::timestamptz"),
        ("the same window counted WITHOUT the action restriction", activity["releases"],
         "select count(*) from mod_audit where (data ->> 'released') = 'true' "
         f"and created_at >= '{since}'::timestamptz", "at_least"),
    ]


def retention_from_config():
    """The deployed instance's own retention, read from the file it runs with."""
    try:
        text = open(CONFIG).read()
    except OSError as exc:
        return None, f"could not read {CONFIG}: {exc}"
    match = re.search(r"^mod_audit_retention_days:\s*(-?\d+)\s*$", text, re.M)
    if not match:
        return None, "not configured"
    return int(match.group(1)), ""


def delivery_from_config():
    try:
        text = open(CONFIG).read()
    except OSError:
        return None
    match = re.search(r"^content_delivery:\s*(\S+)\s*$", text, re.M)
    if not match:
        return False
    return match.group(1).lower() in ("true", "yes", "1", "on")


def plant_old_refusal():
    """A refusal and its audit row, 120 days old -- the fixture that makes the window measurable.

    Written directly, and named for the cleanup: the subject is the AGE of the row, and nothing in the
    application can backdate a decision.
    """
    psql("insert into content_source_block (kind, value, reason, created_at) "
         f"values ('host', '{OLD_HOST}', 'sql-injected by walk-16', now() - interval '{OLD_AGE_DAYS} days')")
    psql("insert into mod_audit (id, action, target_type, target_id, data, created_at) "
         "select gen_random_uuid(), 'SOURCE_BLOCK', 'SOURCE_BLOCK', b.id, '{}'::jsonb, "
         f"b.created_at from content_source_block b where b.value = '{OLD_HOST}'")


def remove_planted():
    psql(f"delete from mod_audit where target_id in "
         f"(select id from content_source_block where value = '{OLD_HOST}')")
    psql(f"delete from content_source_block where value = '{OLD_HOST}'")


try:
    print("== the report is served, to somebody with no account ==")
    status, headers, page = http_get(BASE + "/transparency")
    check("A1 the report is served to anybody", status == 200 and len(page) > 500,
          f"GET /transparency -> {status}, {len(page)} bytes")
    check("A2 it is deliberately indexable (no noindex)",
          "noindex" not in headers.get("X-Robots-Tag", ""),
          "the invite page is noindex; a report nobody can find is not a report")

    delivery = delivery_from_config()
    posture_expected = "content_delivery is ON" if delivery else "content_delivery is off"
    check("A3 the page states this instance's posture, in the process's own words",
          posture_expected in page,
          f"content_delivery={'true' if delivery else 'false'} in {CONFIG} -> the page says "
          f"'{posture_expected}' (the same switch the startup line reads, so the two cannot disagree)")

    status_json, _, raw = http_get(BASE + "/transparency/json", accept="application/json")
    check("A4 the same figures are served as JSON", status_json == 200 and '"in_force"' in raw,
          f"GET /transparency/json -> {status_json}: a report nobody can check mechanically is a report that "
          f"can drift")

    print("\n== every figure, recomputed against the deployed database ==")
    report, why = report_json()
    if report is None:
        check("B0 the JSON parses", False, f"the walk cannot recompute anything: {why}")
        raise SystemExit(1)
    mismatches = []
    for entry in figures(report):
        name, claimed, sql = entry[0], entry[1], entry[2]
        mode = entry[3] if len(entry) > 3 else "equal"
        actual = int(psql(sql) or "-1")
        if mode == "at_least":
            # The double-count check: counted WITHOUT the action restriction the figure must be at least as
            # large, and on an instance where an appeal has been granted it is strictly larger. Reported as a
            # verdict of its own rather than folded in, so the number is visible either way.
            check("B2 the release figure excludes a granted appeal's decision row", actual >= claimed,
                  f"the same period counted without the block-side restriction = {actual}, the figure = "
                  f"{claimed} (larger means the restriction is doing something on this instance)")
            continue
        if actual != claimed:
            mismatches.append(f"{name}: the report says {claimed}, the database says {actual}")
        print(f"      {name} = {claimed}")
    check("B1 every figure the report carries is the count the database returns", not mismatches,
          f"{len(figures(report)) - 1} figures recomputed; mismatches: {mismatches or 'none'}")

    print("\n== the page and the JSON agree ==")
    missing_on_page = [name for name, value, _ in
                       [(f[0], f[1], f[2]) for f in figures(report) if len(f) == 3]
                       if fmt(value) not in page]
    check("D1 every figure in the JSON is on the page", not missing_on_page,
          f"missing from the HTML: {missing_on_page or 'nothing'} (the two are computed by one call, so a "
          f"difference means the page was written by hand somewhere)")
    check("D2 the policy and the report point at each other",
          'href="/transparency"' in http_get(BASE + "/policy")[2] and 'href="/policy"' in page,
          "the policy says what will happen, the report says what did")

    print("\n== the window is real: a refusal 120 days old ==")
    before = report
    plant_old_refusal()
    after, why = report_json()
    if after is None:
        check("C0 the report is still served after the plant", False, why)
    else:
        moved = after["in_force"]["blocks_in_force"] - before["in_force"]["blocks_in_force"]
        check("C1 a 120-day-old refusal IS in force", moved == 1,
              f"blocks_in_force moved by {moved}: a refusal does not expire, so the state figure has no window")
        window_moved = [
            name for name, key in (("blocks_recorded", "blocks_recorded"), ("refusals", "refusals"))
            if after["activity"][key] != before["activity"][key]
        ]
        check("C2 and it is NOT in the window", not window_moved,
              f"the windowed figures that moved: {window_moved or 'none'} (its audit row is 120 days old and "
              f"the retention on this instance is {after['window_days']} days)")
    remove_planted()
    restored, _ = report_json()
    check("C3 removing it restores the figures exactly",
          restored is not None and restored["in_force"]["blocks_in_force"] == before["in_force"]["blocks_in_force"]
          and restored["activity"] == before["activity"],
          "the run left the report as it found it, which is also what makes C1/C2 a measurement of the row "
          "and not of the run")

    print("\n== the window statement matches the deployed configuration ==")
    retention, note = retention_from_config()
    if restored is None:
        check("E1 the report names the retention it ran with", False,
              "the report is not being served any more, so its claim cannot be checked")
    elif retention is None:
        # THE KEY BEING ABSENT IS A STATE, NOT A GAP IN THE WALK. The built-in default is what the instance
        # actually runs with (`config.C.ModAuditRetentionDays: 30`), so the report must report THAT -- and a
        # walk that called this uncheckable would leave the commonest deployed state unverified. Measured: the
        # deployed config does not set the key, and the report's window was 30.
        check("E1 no configured retention is reported as the built-in default",
              restored["retention_bounded"] is True and restored["window_days"] == 30
              and "mod_audit_retention_days" in page,
              f"{CONFIG} does not set mod_audit_retention_days ({note}), so the default 30 applies -> "
              f"window_days = {restored['window_days']}, retention_bounded = {restored['retention_bounded']}, "
              f"and the page names the key a reader would have to change")
    elif retention > 0:
        check("E1 a bounded retention is reported as one, with the number the instance runs",
              restored["retention_bounded"] is True and restored["window_days"] == retention
              and f"mod_audit_retention_days" in page,
              f"{CONFIG} has mod_audit_retention_days: {retention} -> window_days = "
              f"{restored['window_days']}, retention_bounded = {restored['retention_bounded']}")
    else:
        check("E1 no retention is reported as records kept indefinitely",
              restored["records_kept_forever"] is True and restored["retention_bounded"] is False
              and "keeps its moderation records indefinitely" in page,
              f"{CONFIG} has mod_audit_retention_days: {retention} -> the page says the records are kept, "
              f"rather than pretending to a window it does not have")
finally:
    print("\n== restore ==")
    remove_planted()
    left = psql(f"select count(*) from content_source_block where value like '{PREFIX}%'")
    audits = psql(f"select count(*) from mod_audit where target_id in "
                  f"(select id from content_source_block where value like '{PREFIX}%')")
    check("R1 the run left nothing behind", (left, audits) == ("0", "0"),
          f"blocks={left} audits={audits} (read back after the deletes)")

print()
if FAILURES:
    print("FAILED: " + ", ".join(sorted(set(FAILURES))))
    sys.exit(1)
print("all verdicts green")
