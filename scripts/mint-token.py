#!/usr/bin/env python3
"""Mint a dev JWT for the local stash-box instance.

Reads the secret from the dev config rather than accepting it as an argument, so it
never appears in a shell history or a process listing. Prints only the token.

Usage: mint-token.py <config-path> [user-id]
"""
import base64
import hashlib
import hmac
import json
import sys
import time

config = sys.argv[1] if len(sys.argv) > 1 else ".config-dev/config.yml"
user_id = int(sys.argv[2]) if len(sys.argv) > 2 else 1

secret = None
with open(config) as fh:
    for line in fh:
        if line.startswith("jwt_secret_key:"):
            secret = line.split(":", 1)[1].strip()
            break
if not secret:
    sys.exit("no jwt_secret_key in " + config)


def b64(raw: bytes) -> str:
    return base64.urlsafe_b64encode(raw).rstrip(b"=").decode()


header = b64(json.dumps({"alg": "HS256", "typ": "JWT"}, separators=(",", ":")).encode())
payload = b64(json.dumps({"id": user_id, "exp": int(time.time()) + 3600},
                        separators=(",", ":")).encode())
signing_input = f"{header}.{payload}"
sig = b64(hmac.new(secret.encode(), signing_input.encode(), hashlib.sha256).digest())
print(f"{signing_input}.{sig}")