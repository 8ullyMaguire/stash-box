#!/usr/bin/env python3
"""Live walk of the image posture gate (plan phase 10, slice 10a-ii).

Two arms, one binary, the real router and the real handlers:

  1. `images_enabled: false` and `content_delivery: false` -- the deployed instance's own posture -- must
     answer every image request with 403 and a body that names both keys.
  2. `images_enabled: true` must NOT: the same request reaches the image handler, which answers from an
     empty table (this walk does not seed images, and the difference between 403 and a data answer is
     exactly what the gate is for).

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


def start(port, images_enabled, tag):
    """Start the real binary with the two posture keys set, and wait for a REQUEST to answer."""
    config_path = os.path.join(SCRATCH, f"walk-images-{tag}.yml")
    with open(config_path, "w") as f:
        f.write(f"host: {HOST}\nport: {port}\ndatabase: {DSN}\n")
        f.write(f"images_enabled: {'true' if images_enabled else 'false'}\n")
        f.write("content_delivery: false\n")
        f.write("instance_id: walk-images\n")

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

    # ARM 2 -- the same request, one key flipped. The control: a gate that refused unconditionally would
    # pass arm 1 and fail here.
    proc, log = start(13996, images_enabled=True, tag="on")
    try:
        status, raw = get(13996, "/images/" + UUID)
        check("W3 with images_enabled on, the request reaches the handler instead of the posture refusal",
              status != 403, f"{status} {raw[:160]!r}")
        check("W4 and the answer is not the refusal message",
              b"images_enabled" not in raw, raw[:160])
    finally:
        stop(proc, log)

    green = sum(1 for _, ok, _ in verdicts if ok)
    print(f"\n{green}/{len(verdicts)} verdicts green")
    return 0 if green == len(verdicts) and verdicts else 1


if __name__ == "__main__":
    sys.exit(main())
