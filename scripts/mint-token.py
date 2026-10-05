#!/usr/bin/env python3
"""Mint a dev JWT for the local stash-box instance.

Reads the secret from the dev config rather than accepting it as an argument, so it
never appears in a shell history or a process listing. Prints only the token.

Usage: mint-token.py <config-path> [user-uuid]

The user-id is the users.id value -- a UUID string in this schema, not the integer the
pre-0.x stash-box used. Passing a number mints a token for a user that does not exist, and
the API answers 401 with an empty body, which is indistinguishable from a bad secret.
"""
import base64
import hashlib
import hmac
import json
import sys
import time

config = sys.argv[1] if len(sys.argv) > 1 else ".config-dev/config.yml"
# users.id is a UUID (gofrs v7, rendered with dashes). Kept as a STRING: the claim is
# json:"uid" -> string, and an int would fail to unmarshal into it -- another silent 401.
user_id = sys.argv[2] if len(sys.argv) > 2 else ""

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
# The claims must match internal/service/user/apikey.go's APIKeyClaims exactly:
#   UserID string `json:"uid"`   <- NOT "id", and a string even for a numeric uid
#   jwt.RegisteredClaims         <- so exp and sub come from RegisteredClaims
# My first version wrote {"id": user_id, "exp": ...}, which unmarshals to an empty
# UserID and an empty Subject -- so every minted token was rejected with a bare 401 that
# looks exactly like a wrong signing secret. There is no claim-level error to tell the
# two apart, which is why the fix needed to be made from the struct rather than by guessing.
now = int(time.time())
payload = b64(json.dumps(
    {
        "uid": user_id,
        "sub": "APIKey",
        "iat": now,
        "exp": now + 3600,
    },
    separators=(",", ":"),
).encode())
signing_input = f"{header}.{payload}"
sig = b64(hmac.new(secret.encode(), signing_input.encode(), hashlib.sha256).digest())
print(f"{signing_input}.{sig}")