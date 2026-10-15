#!/usr/bin/env python3
"""Walk slice 11c on a LIVE instance: the invitation's landing page, its counters, and its QR.

Run it ON the host that serves the instance. It borrows slice 11b's plumbing (mint/gql/psql) rather
than restating it, because two copies of a JWT minter are two places for the auth cache's rule to be
got wrong.

WHY THIS EXISTS. Three claims of slice 11c are only true if they are true through the deployed binary
and over the deployed database:

  A. A VISIT IS COUNTED. GET /invite/<live key> is served to an unauthenticated client (no
     credentials of any kind) and the member's `scans` counter moves by exactly one -- with `signups`
     and `activations` NOT moving, because a visit is not an address and an address is not a person.
  B. A VISIT RECORDS NOBODY. The row is a count, a kind and a time: `user_id` NULL. (A scan that
     recorded an account would be a page that records strangers.)
  C. THE PAGE CANNOT BE ASKED WHETHER A KEY IS GOOD, and a dead key's visit is not counted: a live
     key, an expired key and a code that was never a key get the same document and the same status,
     while only the live one adds a row.

  D. THE QR DECODES BACK TO THE LINK, on the deployed instance, with an INDEPENDENT decoder
     (SVG -> rsvg-convert -> zbarimg), and that decoded string is byte-identical to the `link` the
     same key reports. If these two ever disagree the QR sends somebody somewhere else.

WHAT IT CHANGES AND PUTS BACK: one api_key (the member's), the keys it mints, one `expire_time` it
moves into the past, and every event row it caused -- the journal is append-only history, so the walk
remembers where it ended (`max(id)`) and removes only what it added. Nothing printed is a credential.

See docs/plan/feature-content-aggregator.md, phase 11 slice 11c.
"""

import os
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from importlib.machinery import SourceFileLoader

_11b = SourceFileLoader("walk11b", os.path.join(os.path.dirname(os.path.abspath(__file__)),
                                                "walk-11b-live.py"))

psql = _11b.psql
gql = _11b.gql
show = _11b.show
MEMBER = _11b.MEMBER
OPERATOR = _11b.OPERATOR
TOKENS = _11b.TOKENS
STAMP = _11b.STAMP

# The landing page is served by the running instance, on the same port as the API.
BASE = os.environ.get("WALK_BASE", "http://127.0.0.1:9998")

FAILED = []


def check(label, ok, description, detail=""):
    print(f"   VERDICT {label}: {'OK' if ok else 'FAIL'} -- {description}")
    if detail:
        print(f"      {detail}")
    if not ok:
        FAILED.append(label)


def http_get(path):
    """An unauthenticated GET: no token, no cookie, no header of any kind beyond a User-Agent."""
    req = urllib.request.Request(BASE + path, headers={"User-Agent": "walk-11c"})
    try:
        with urllib.request.urlopen(req, timeout=20) as resp:
            return resp.status, resp.read().decode("utf-8", "replace"), dict(resp.headers)
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode("utf-8", "replace"), dict(e.headers)


def scans_now():
    status, body = gql(TOKENS[MEMBER], "{ me { invite_stats { scans signups activations } } }")
    if status != 200 or not isinstance(body, dict) or not body.get("data"):
        return None
    return body["data"]["me"]["invite_stats"]


def main():
    member_name = psql(f"select name from users where id = '{MEMBER}'")
    print(f"=== 11c walk: the landing page, the counters and the QR (member {member_name}) ===")

    before_max = int(psql("select coalesce(max(id), 0) from invite_key_events"))
    before_keys = psql(f"select count(*) from invite_keys where generated_by = '{MEMBER}'")
    api_key = psql(f"select api_key from users where id = '{MEMBER}'")
    if not api_key or api_key == "":
        sys.exit("the member has no api_key to restore; refusing to run")

    keys = []
    try:
        # --- A/B. a live key, visited twice, unauthenticated -------------------------------------
        status, body = gql(TOKENS[MEMBER],
                           "mutation { generateInviteKeys(input: { keys: 1 }) { id } }")
        if status != 200 or not isinstance(body, dict) or not body.get("data"):
            sys.exit(f"could not mint a key: http {status} {body}")
        key = body["data"]["generateInviteKeys"][0]["id"]
        keys.append(key)
        print(f"   minted key {key[:8]}...")

        baseline = scans_now()
        if baseline is None:
            sys.exit("could not read the member's counters")
        print(f"   baseline: {baseline}")

        code, page, headers = http_get("/invite/" + key)
        check("A", code == 200,
              "the landing page is served to an UNAUTHENTICATED request (no token, no cookie): a "
              "link that needs an account is not shareable", f"http {code}")
        check("A2", "noindex" in headers.get("X-Robots-Tag", ""),
              "and it is not indexable: a key in a search index is a key that is no longer private",
              headers.get("X-Robots-Tag", "(no header)"))
        check("A3", key in page and page.count(key) == 1,
              "the page echoes the key exactly once, where it was typed, and links nowhere",
              f"occurrences of the key: {page.count(key)}")

        first = scans_now()
        check("A4", first is not None and first["scans"] - baseline["scans"] == 1,
              "ONE visit moved `scans` by one",
              f"{baseline} -> {first}")
        check("A5", first is not None and first["signups"] == baseline["signups"]
              and first["activations"] == baseline["activations"],
              "and moved NOTHING else: a visit is not an address and an address is not a person")

        # B. the row records nobody.
        row = psql(f"select kind || '|' || coalesce(user_id::text, 'NULL') || '|' || "
                   f"coalesce(invite_key_id::text, 'NULL') from invite_key_events "
                   f"where id > {before_max} order by id desc limit 1")
        check("B", row == f"scan|NULL|{key}",
              "the row is a count, a kind and a time: NO user, and the key it was a visit to", row)

        http_get("/invite/" + key)
        second = scans_now()
        check("B2", second is not None and second["scans"] - baseline["scans"] == 2,
              "a second visit is a second scan: this counter counts VISITS, not visitors",
              f"{baseline} -> {second}")

        # --- C. a dead key and an invented code: same page, no row --------------------------------
        expired = psql(f"insert into invite_keys (id, generated_by, generated_at, uses, expire_time) "
                       f"values (gen_random_uuid(), '{MEMBER}', now(), 5, now() - interval '1 day') "
                       f"returning id")
        keys.append(expired)

        dead_code, dead_page, dead_headers = http_get("/invite/" + expired)
        garbage_code, garbage_page, _ = http_get("/invite/not-a-code-at-all")

        check("C", dead_code == 200 and garbage_code == 200,
              "an expired key and a code that was never a key are both SERVED, not refused "
              "(a 404 would answer the question this page must not answer)",
              f"expired http {dead_code}, invented http {garbage_code}")
        check("C2", dead_page.replace(expired, "<CODE>") == page.replace(key, "<CODE>")
              and garbage_page.replace("not-a-code-at-all", "<CODE>") == page.replace(key, "<CODE>"),
              "and they are the SAME DOCUMENT as a live key's, apart from the echo of what was typed")
        check("C3", dead_headers.get("X-Robots-Tag") == headers.get("X-Robots-Tag"),
              "with the same headers, which would be just as askable")

        third = scans_now()
        delta = third["scans"] - baseline["scans"] if third else "unreadable"
        check("C4", third is not None and delta == 2,
              "and NEITHER of them counted: 'a scan of an expired key is not counted', and an "
              "invented code is not an event",
              f"scans still {delta} above the baseline")

        rows = psql(f"select count(*) from invite_key_events where id > {before_max}")
        check("C5", rows == "2",
              "the journal holds exactly the two visits that really happened", f"{rows} row(s)")

        # --- D. the QR decodes back to the link, with an independent decoder ----------------------
        status, body = gql(TOKENS[MEMBER], "{ me { invite_codes { id link qr_svg } } }")
        shares = (body["data"]["me"]["invite_codes"]
                  if status == 200 and isinstance(body, dict) and body.get("data") else [])
        share = next((s for s in shares if s["id"] == key), None)
        check("D", share is not None and share["link"].endswith("/invite/" + key),
              "the key reports the LINK it is shared as, built from the instance's own address",
              share["link"] if share else "(no link for the minted key)")

        svg = share["qr_svg"] if share else ""
        check("D2", bool(svg) and svg.startswith("<svg ") and svg.endswith("</svg>")
              and (not share or share["link"] not in svg),
              "the QR is a standalone SVG document with the URL as GEOMETRY, not as text",
              f"{len(svg)} bytes")

        with tempfile.TemporaryDirectory() as tmp:
            svg_path = os.path.join(tmp, "key.svg")
            png_path = os.path.join(tmp, "key.png")
            with open(svg_path, "w") as f:
                f.write(svg)
            rendered = subprocess.run(["rsvg-convert", "-w", "600", svg_path, "-o", png_path],
                                      capture_output=True, text=True)
            if rendered.returncode != 0:
                check("D3", False, "rsvg-convert could not render the SVG", rendered.stderr.strip())
            else:
                decoded = subprocess.run(["zbarimg", "--quiet", "--raw", png_path],
                                         capture_output=True, text=True)
                payload = decoded.stdout.strip()
                check("D3", decoded.returncode == 0 and share is not None
                      and payload == share["link"],
                      "and it DECODES BACK to that same link with an independent decoder "
                      "(zbarimg, through rsvg-convert): if these disagree the code is a lie",
                      f"decoded: {payload}")

    finally:
        # THE RESTORE IS CODE. The journal is append-only history, so this walk removes exactly the
        # rows it added (above the max id it read first) and the keys it minted.
        for k in keys:
            psql(f"delete from invite_key_events where invite_key_id = '{k}'")
            psql(f"delete from invite_keys where id = '{k}'")
        psql(f"delete from invite_key_events where id > {before_max} and generated_by = '{MEMBER}'")
        psql(f"update users set api_key = '{api_key}' where id = '{MEMBER}'")

        print("\nRESTORE:")
        print("   event rows left behind:",
              psql(f"select count(*) from invite_key_events where id > {before_max}"))
        print("   the member's key count is back:",
              psql(f"select count(*) from invite_keys where generated_by = '{MEMBER}'") == before_keys)
        print("   the member's api_key is byte-identical:",
              psql(f"select api_key from users where id = '{MEMBER}'") == api_key)

    print("\n=== " + ("WALK COMPLETE" if not FAILED else f"WALK FAILED: {', '.join(FAILED)}") + " ===")
    return 1 if FAILED else 0


if __name__ == "__main__":
    sys.exit(main())
