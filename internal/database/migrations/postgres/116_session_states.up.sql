-- "Where I left off": opt-in server-side session state (discovery SPEC section 11, aggregator
-- phase 7 slice 7).
--
-- WHAT THIS IS: a place for a client to keep a cursor -- the position in a list, the last item
-- opened, the filters in force -- so a SECOND DEVICE can continue where the first stopped.
--
-- LOCAL-FIRST, AND THE SCHEMA IS WHERE THAT IS ENFORCED. The draft's `storage: enum [local,
-- synced]` is not a label: a `local` row holds NO PAYLOAD. `cursor` is NULL for it and the CHECK
-- below refuses anything else, so a client that keeps its state locally cannot cause the server
-- to hold a copy of it, however the service is later edited. "Nothing reads it unless the user
-- turned it on" is then a property of the data, not a promise about the code path.
--
-- A `local` row is therefore not a payload row at all: it RECORDS THE CHOICE -- "this surface
-- stays on this device" -- which is what makes the state observable (a support question, an
-- audit of what is synced) without storing what is being kept back.
--
-- WHY THE PRIMARY KEY IS (user_id, kind, key) AND NOT A SURROGATE ID: the cursor is addressed by
-- what it is for. "Where I left off in scene-list-A" is one fact; two rows for it would be two
-- answers to one question, and a client that upserts cannot create them (the write is ON
-- CONFLICT ... DO UPDATE, so a repeated save is idempotent rather than a growing list).
--
-- `kind` IS FREE TEXT WITH A LENGTH CAP, deliberately: the surface that stores a cursor and the
-- server that holds it are versioned separately, and an enum here would make a new surface a
-- migration. Unknown kinds are inert -- a row the reader of a different kind never looks at --
-- rather than rejected, which would be a migration in disguise. The cap is the thing that
-- matters, and it is a CHECK rather than only a service rule so an oversized row cannot exist.
--
-- NOT MODELLED: an expiry. The draft has none, and a TTL on "where I left off" would silently
-- forget a position a user came back for; the delete surface is explicit (`clearSessionState`).

CREATE TABLE "session_states" (
    "user_id" UUID NOT NULL REFERENCES "users" ("id") ON DELETE CASCADE,

    -- What the cursor is FOR ('scenes', 'performers', 'forum-thread', ...).
    "kind" VARCHAR(32) NOT NULL CHECK (length("kind") > 0),

    -- Which one of that kind ('browse', 'tag:abc', a saved search id).
    "key" VARCHAR(128) NOT NULL CHECK (length("key") > 0),

    -- OPAQUE to the server: the client decides what is in it, and the server stores and returns
    -- it without interpreting it. jsonb rather than text so it is queryable by an operator
    -- ("which kinds of cursor are actually in use") without a second format.
    "cursor" JSONB,

    -- 'local' | 'synced'. See the header: 'local' means the server keeps the choice and NOT the
    -- state, and the CHECK below makes that unrepresentable rather than merely intended.
    "storage" VARCHAR(10) NOT NULL DEFAULT 'local' CHECK ("storage" IN ('local', 'synced')),

    "updated_at" TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY ("user_id", "kind", "key"),

    -- THE RULE THE FEATURE IS BUILT ON, enforced where it cannot be forgotten:
    -- a synced row has a cursor, and a local row has none.
    CONSTRAINT "session_states_payload_matches_storage" CHECK (
        ("storage" = 'synced' AND "cursor" IS NOT NULL)
        OR ("storage" = 'local' AND "cursor" IS NULL)
    )
);
