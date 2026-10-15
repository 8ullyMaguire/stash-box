-- invite_key_events: what a member's invitations DID, in a row that outlives the key.
--
-- WHY THIS TABLE EXISTS, and it is the whole design (plan phase 11 slice 11c, pre-code finding):
-- every row that could witness a scan, a signup or an activation is destroyed by the event it would
-- witness --
--
--   * `pending_activations`, which the plan's first draft named, was DROPPED by migration 37; a
--     signup today is a `user_tokens` row of type ACTIVATION, and `ActivateNewUser` deletes it;
--   * a key whose last use is consumed at activation is DELETED, so "what did this key produce" has
--     nothing left to group by.
--
-- So the counters cannot be derived from the rows the rest of the system keeps, and a counter column
-- is not the answer either: a scan is interest, a signup is an address and an activation is a
-- person, and a member needs to tell those apart. One row per event it is.
CREATE TABLE "invite_key_events" (
    "id" BIGSERIAL PRIMARY KEY,

    -- The key, as the event's own attribution, and NULLABLE because the key can be spent (and so
    -- deleted) before anybody reads this: the event must survive the thing it is about. This is the
    -- one departure from the foreign-key habit of this schema, and it is deliberate -- NO CASCADE,
    -- because a counter that dies with the key it counts reports ZERO for the one key a member
    -- actually asks about.
    "invite_key_id" UUID REFERENCES "invite_keys" ("id") ON DELETE SET NULL,

    -- WHO THE COUNTER BELONGS TO, denormalised onto the row for the reason above: this is what
    -- survives the key's deletion, and it is the column the member's own counters group by.
    "generated_by" UUID NOT NULL REFERENCES "users" ("id") ON DELETE CASCADE,

    -- 'scan', 'signup', 'activation'. Free text rather than an enum, like trust_events.kind: the set
    -- is small and closed today, and a new kind should not need a migration to be counted.
    "kind" TEXT NOT NULL,

    -- The account a signup or an activation BECAME, so a member can be told "3 signups, 1
    -- activation" rather than a number, and so an activation can be traced to the member it
    -- produced. NULL for a scan, which is nobody: an unauthenticated GET has no account, no address
    -- and no agent to record, and recording any of them would turn a counter into a log of visitors.
    "user_id" UUID REFERENCES "users" ("id") ON DELETE SET NULL,

    "created_at" TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The member's counters: every read groups by these two columns.
CREATE INDEX "invite_key_events_generated_by_kind_idx" ON "invite_key_events" ("generated_by", "kind");

-- One key's counters, for the per-key view and for the tests that assert a specific key's arm.
CREATE INDEX "invite_key_events_invite_key_id_idx" ON "invite_key_events" ("invite_key_id");
