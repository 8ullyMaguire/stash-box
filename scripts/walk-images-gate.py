#!/usr/bin/env python3
"""Live walk of the image posture gate (plan phase 10, slice 10a-ii).

Two arms, one binary, the real router and the real handlers:

  1. `images_enabled: false` and `content_delivery: false` -- the deployed instance's own posture -- must
     answer every image request with 403 and a body that names both keys.
  2. `images_enabled: true` must NOT: the same request reaches the image handler, which answers from an
     empty table (this walk does not seed images, and the difference between 403 and a data answer is
     exactly what the gate is for).
  3. `images_enabled: true` with NO `media_dir` -- the state slice 10b-1 exists for -- must be refused with
     a body naming `media_dir`, and the STARTUP LINE must say the images are enabled but refused at the
     storage: an operator reads the log, and an instance in this state would otherwise log the same line
     as a working one.
  4. the same, with a directory that does not exist: the refusal carries the path.
  5. the control -- a real directory -- where the request reaches the handler.
  6. the export invariant against this host's live `exportfs -v`, reported UNKNOWN where it cannot run.

    POSTGRES_DB='postgres:smoke_pw@127.0.0.1:55434/sbx-pr1284?sslmode=disable' \
        python3 scripts/walk-images-gate.py

Exit 0 only if every verdict is green and both instances were stopped.
"""

import json
import os
import re
import signal
import subprocess
import sys
import time
import urllib.error
import urllib.request

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SCRATCH = os.environ.get("BH_AGENT_WORKSPACE", "/tmp")
BIN = os.path.join(SCRATCH, "stash-box-image-gate-walk")
DSN = os.environ.get("POSTGRES_DB", "postgres:smoke_pw@127.0.0.1:55434/sbx-pr1284?sslmode=disable")

HOST = "127.0.0.1"
UUID = "00000000-0000-4000-8000-000000000000"
verdicts = []


def check(name, ok, detail=""):
    verdicts.append((name, ok, detail))
    print(f"{'PASS' if ok else 'FAIL'}  {name}" + (f"\n      {detail}" if detail else ""))
    return ok


def start(port, images_enabled, tag, media_dir=None):
    """Start the real binary with the posture keys set, and wait for a REQUEST to answer.

    `media_dir` is written only when it is given: an ABSENT key and a key set to the empty string are two
    different configurations an operator can be in, and the walk asserts the absent one for the state that
    matters most (the switch on, nothing configured, everywhere).
    """
    config_path = os.path.join(SCRATCH, f"walk-images-{tag}.yml")
    with open(config_path, "w") as f:
        f.write(f"host: {HOST}\nport: {port}\ndatabase: {DSN}\n")
        f.write(f"images_enabled: {'true' if images_enabled else 'false'}\n")
        f.write("content_delivery: false\n")
        f.write("instance_id: walk-images\n")
        if media_dir is not None:
            f.write(f"media_dir: {media_dir}\n")

    log = open(os.path.join(SCRATCH, f"walk-images-{tag}.log"), "w")
    proc = subprocess.Popen([BIN, "--config_file", config_path],
                            stdout=log, stderr=subprocess.STDOUT,
                            env=dict(os.environ), cwd=SCRATCH, start_new_session=True)
    for _ in range(60):
        if proc.poll() is not None:
            break
        try:
            urllib.request.urlopen(f"http://{HOST}:{port}/graphql", timeout=5)
            return proc, log
        except urllib.error.HTTPError:
            return proc, log
        except Exception:
            time.sleep(0.5)
    print(open(os.path.join(SCRATCH, f"walk-images-{tag}.log")).read()[-1500:])
    raise SystemExit(f"the instance on {port} never answered")


def stop(proc, log):
    proc.send_signal(signal.SIGTERM)
    try:
        proc.wait(timeout=20)
    except subprocess.TimeoutExpired:
        proc.kill()
    log.close()


def get(port, path):
    """GET one path. A 4xx is an ANSWER here, so the status and the decoded body are both returned."""
    try:
        with urllib.request.urlopen(f"http://{HOST}:{port}{path}", timeout=10) as resp:
            return resp.status, resp.read()
    except urllib.error.HTTPError as e:
        return e.code, e.read()


def main():
    build = subprocess.run(["go", "build", "-o", BIN, "./cmd/stash-box"], cwd=REPO,
                           capture_output=True, text=True, timeout=900)
    if build.returncode != 0:
        print("BUILD FAILED -- no verdicts are meaningful:\n" + build.stderr[-1500:])
        return 1

    # ARM 1 -- the posture every shipped config ships with, and the one the deployed instance runs.
    proc, log = start(13997, images_enabled=False, tag="off")
    try:
        for path, label in (("/images/" + UUID, "an image by id"),
                            (f"/images/site/{UUID}", "a site image"),
                            ("/images/not-a-uuid", "a malformed image path")):
            status, raw = get(13997, path)
            body = {}
            try:
                body = json.loads(raw)
            except Exception:
                pass
            check(f"W1 {label} is refused with 403 while the switches are off", status == 403,
                  f"{status} {raw[:120]!r}")
            check(f"W2 the refusal for {label} names the key an operator must change",
                  body.get("error", "").find("images_enabled") >= 0
                  and body.get("error", "").find("content_delivery") >= 0,
                  repr(body.get("error", ""))[:160])
    finally:
        stop(proc, log)

    # ARM 2 -- the same request, one key flipped, and a directory to serve from. The control: a gate that
    # refused unconditionally would pass arm 1 and fail here.
    #
    # The directory is part of this arm since slice 10b-1, and the FIRST RUN OF THIS WALK is why: with the
    # switch on and no media_dir the request is refused (correctly), which made the old W3 fail while its
    # companion W4 -- "the answer does not name the wrong key" -- still passed, because the new refusal
    # names neither of the keys W4 was looking for. An absence assertion over the wrong subject passes
    # whatever happens; the two facts are one arm now.
    on_dir = os.path.join(SCRATCH, "walk-media-dir")
    os.makedirs(on_dir, exist_ok=True)
    proc, log = start(13996, images_enabled=True, tag="on", media_dir=on_dir)
    try:
        status, raw = get(13996, "/images/" + UUID)
        check("W3 with images_enabled on and a directory to serve from, the request reaches the handler "
              "rather than the posture refusal",
              status != 403 and b"posture" not in raw and b"media_dir" not in raw, f"{status} {raw[:160]!r}")
    finally:
        stop(proc, log)

    # ARM 3 -- the switch ON and no directory: the state slice 10b-1 exists for. The startup line has to
    # say so (an operator reads the log, not the code) and the request has to be refused BY NAME rather
    # than falling through to a handler with nothing to read.
    proc, log = start(13995, images_enabled=True, tag="nodir")
    try:
        status, raw = get(13995, "/images/" + UUID)
        body = {}
        try:
            body = json.loads(raw)
        except Exception:
            pass
        check("W5 with the switch on and no media_dir, the request is refused with 403", status == 403,
              f"{status} {raw[:160]!r}")
        check("W6 and the refusal names media_dir rather than a switch the operator already set",
              body.get("error", "").find("media_dir") >= 0, repr(body.get("error", ""))[:200])
    finally:
        stop(proc, log)
    line = open(os.path.join(SCRATCH, "walk-images-nodir.log")).read()
    check("W7 the startup line says the images are ENABLED BUT REFUSED AT THE STORAGE",
          "ENABLED BUT REFUSED AT THE STORAGE" in line,
          next((l for l in line.splitlines() if "media" in l.lower()), "")[:200])

    # ARM 4 -- a directory that does not exist. The refusal has to carry the PATH, because "media_dir is
    # unusable" does not tell an operator with several instances which config to open.
    proc, log = start(13994, images_enabled=True, tag="missing", media_dir="/nonexistent/stash-box-media")
    try:
        status, raw = get(13994, "/images/" + UUID)
        body = {}
        try:
            body = json.loads(raw)
        except Exception:
            pass
        check("W8 a configured directory that does not exist is refused, naming the path",
              status == 403 and "/nonexistent/stash-box-media" in body.get("error", ""),
              f"{status} {body.get('error', '')[:200]!r}")
    finally:
        stop(proc, log)

    # ARM 5 -- the control: a real directory, and the same request reaches the handler. Without this arm
    # arms 3 and 4 would be satisfied by a gate that refused whenever a media directory was mentioned.
    served_dir = os.path.join(SCRATCH, "walk-media-dir")
    os.makedirs(served_dir, exist_ok=True)
    proc, log = start(13993, images_enabled=True, tag="serving", media_dir=served_dir)
    try:
        status, raw = get(13993, "/images/" + UUID)
        check("W9 with a real directory the request reaches the handler (404 from an empty table is the "
              "data answer, not the posture refusal)", status == 404,
              f"{status} {raw[:160]!r}")
    finally:
        stop(proc, log)
    line = open(os.path.join(SCRATCH, "walk-images-serving.log")).read()
    check("W10 and the startup line says which directory it serves from", served_dir in line,
          next((l for l in line.splitlines() if "Image bytes" in l), "")[:200])

    # ARM 6 -- the export invariant, against THIS host's live export table. On a host without exportfs the
    # verdict is UNKNOWN and is reported as such: a check that could not run is not a check that passed,
    # which is the rule the goal-check spells out for the same reason.
    # THE TABLE IS READ DIRECTLY, and the first version of this arm did not. `exportfs -v` as an
    # unprivileged user prints NOTHING and exits ZERO on the host this was written against (it cannot take
    # /var/lib/nfs/.etab.lock), so parsing its output found zero exports and this arm reported the media
    # directory "outside every export" on a host exporting five paths -- a FALSE PASS, in the one direction
    # an invariant must not be wrong. /var/lib/nfs/etab is the file exportfs reads, and it is world-readable.
    exports = None
    try:
        with open("/var/lib/nfs/etab") as fh:
            exports = [l.split()[0] for l in fh.read().splitlines()
                       if l.strip() and l == l.lstrip()]
    except FileNotFoundError:
        out = subprocess.run(["exportfs", "-v"], capture_output=True, text=True, timeout=20)
        if out.returncode == 0 and not out.stderr.strip():
            exports = [l.split()[0] for l in out.stdout.splitlines()
                       if l.strip() and l == l.lstrip()]
    except Exception:
        exports = None
    if exports is None:
        print("UNKNOWN  W11 this host's export table could not be read, so the export invariant could not "
              "be evaluated here (run this arm on the host that serves the media directory)")
    else:
        inside = [e for e in exports
                  if served_dir == e or served_dir.startswith(e.rstrip("/") + "/")]
        check(f"W11 the media directory is not inside any of this host's {len(exports)} NFS export(s)",
              not inside, f"exports: {exports}" if not inside else f"inside {inside}")

    green = sum(1 for _, ok, _ in verdicts if ok)
    print(f"\n{green}/{len(verdicts)} verdicts green")
    return 0 if green == len(verdicts) and verdicts else 1


if __name__ == "__main__":
    sys.exit(main())
