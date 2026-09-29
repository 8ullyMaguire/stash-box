-- Webhooks (SPEC §7.11, phase 3 step 3).
--
-- Two tables, and the split is the delivery RECORD versus the delivery TARGET.
-- A user's endpoint is configuration; a delivery is a queue entry, and mixing them
-- means deleting an endpoint silently destroys the history of what it was told.

CREATE TABLE "webhook_endpoints" (
    "id" UUID NOT NULL PRIMARY KEY,
    "user_id" UUID NOT NULL REFERENCES "users" ("id") ON DELETE CASCADE,

    -- A HASH of the signing secret, never the secret.
    --
    -- bcrypt rather than a plain SHA-256, deliberately and against the usual
    -- advice for API keys. A webhook secret is high-entropy and randomly
    -- generated, so it is not guessable by dictionary attack and bcrypt's slowness
    -- buys nothing; what it DOES buy is that a database dump does not hand an
    -- attacker a working signing key for every endpoint on the instance. A fast
    -- hash over a random secret is still a plaintext-equivalent credential, and
    -- the whole reason to hash at all is to make a dump useless. The cost is that
    -- verifying a signature needs the plaintext, so the plaintext is NOT
    -- recoverable -- which is why the signature is verified by the CONSUMER and
    -- the box only ever SIGNS. See the note on secret_hash below.
    "secret_hash" TEXT NOT NULL,

    -- The URL the box will POST to. Validated at write time AND at delivery time;
    -- see the service for why both.
    "target_url" TEXT NOT NULL,

    -- Which events this endpoint wants. TEXT[] rather than a join table: the set is
    -- read whole on every dispatch and never queried by event type across
    -- endpoints, and an empty array means "nothing", which the service rejects.
    "event_types" TEXT[] NOT NULL,

    -- Disabled endpoints keep their row and their secret. Deleting on disable
    -- would mean a user who pauses an integration cannot tell whether their
    -- secret still works, and re-enabling would silently issue a new one.
    "disabled" BOOLEAN NOT NULL DEFAULT FALSE,

    "created_at" TIMESTAMP NOT NULL DEFAULT now(),
    "updated_at" TIMESTAMP NOT NULL DEFAULT now()
);

-- The dispatch lookup: which endpoints want this event type, and are live.
CREATE INDEX "webhook_endpoints_dispatch_idx"
    ON "webhook_endpoints" ("event_types") WHERE NOT "disabled";

CREATE INDEX "webhook_endpoints_user_idx"
    ON "webhook_endpoints" ("user_id", "created_at" DESC);

-- A user's own endpoints, for the settings page.
CREATE INDEX "webhook_endpoints_user_created_idx"
    ON "webhook_endpoints" ("user_id", "created_at" DESC);

CREATE TABLE "webhook_deliveries" (
    "id" UUID NOT NULL PRIMARY KEY,
    "endpoint_id" UUID NOT NULL REFERENCES "webhook_endpoints" ("id") ON DELETE CASCADE,

    "event_type" TEXT NOT NULL,
    "payload" JSONB NOT NULL,

    -- 0, 1, 2, 3 then abandoned. The bound is in the service, not here, because
    -- the backoff SCHEDULE is a policy and a policy that can be changed without a
    -- migration is worth more than one that cannot. What the column guarantees is
    -- only that the count is a real number and not NULL.
    "attempt" INTEGER NOT NULL DEFAULT 0,

    -- When this delivery becomes eligible. Now() on insert, so a new delivery is
    -- immediately eligible, and bumped on failure.
    "next_attempt_at" TIMESTAMP NOT NULL DEFAULT now(),

    -- The last error, TRUNCATED. A webhook target can return a megabyte of HTML
    -- and storing it whole makes the queue table the largest thing in the
    -- database. 500 characters is enough to read "connection refused" or "404".
    "last_error" TEXT,

    "delivered_at" TIMESTAMPTZ,
    "created_at" TIMESTAMP NOT NULL DEFAULT now()
);

-- THE dispatch query. Partial on delivered_at IS NULL so the index contains ONLY
-- pending work, which is what makes "give me the next hundred due deliveries"
-- touch a small index even after millions of successful deliveries have
-- accumulated. An index on the whole table would grow without bound and the
-- pending fraction of it would shrink to nothing.
CREATE INDEX "webhook_deliveries_due_idx"
    ON "webhook_deliveries" ("next_attempt_at")
    WHERE "delivered_at" IS NULL;

-- A user's delivery history, newest first.
CREATE INDEX "webhook_deliveries_endpoint_idx"
    ON "webhook_deliveries" ("endpoint_id", "created_at" DESC);
