-- Member vouches (SPEC §5.4 as the roadmap has it, plan phase 6 slice 6d).
--
-- A vouch is one member attesting to another's standing. The local counterpart of
-- the mesh attestations in SPEC §7.17, and deliberately NOT the same thing: those
-- cross instances and are signed, these are one account saying "I know this one".
--
-- THE CAP IS THE FEATURE. A member may hold at most three ACTIVE vouches, and the
-- fourth is refused -- which is why the table is a ledger of accepted vouches
-- rather than a count: a count cannot say when one started, so it cannot expire,
-- cannot be withdrawn, and cannot be the thing an operator audits.
--
-- ACTIVE, NOT PRESENT, and the difference is three separate facts: not withdrawn,
-- not expired, and one per pair. Expiry is what makes this an attestation rather
-- than a permanent mark: SPEC §7.17 says attestation weight DECAYS and requires
-- re-signing, so a vouch is good for VouchValidityDays and then has to be given
-- again by someone who still means it. Nothing has to run for that to be true --
-- "active" is derived from expires_at at read time, the same way the streak is
-- derived from the event log.
--
-- Withdrawn vouches STAY in the table. "Three vouches were withdrawn" is a fact
-- about the instance that a member considering a fourth deserves to be able to
-- see, and deleting the row is how a pattern of drive-by vouching becomes
-- invisible.
CREATE TABLE "member_vouches" (
    "id" UUID NOT NULL PRIMARY KEY,
    "voucher_id" UUID NOT NULL REFERENCES "users" ("id") ON DELETE CASCADE,
    "target_id" UUID NOT NULL REFERENCES "users" ("id") ON DELETE CASCADE,
    "created_at" TIMESTAMPTZ NOT NULL DEFAULT now(),
    "expires_at" TIMESTAMPTZ NOT NULL,
    "withdrawn_at" TIMESTAMPTZ,

    -- Nobody vouches for themselves. The service refuses it too, and the
    -- constraint is here because "who vouched for this person" would otherwise
    -- have a row whose answer is "they did".
    CONSTRAINT "member_vouches_not_self" CHECK ("voucher_id" <> "target_id")
);

-- ONE ACTIVE VOUCH PER PAIR. Partial, so a withdrawn or expired vouch does not
-- block a fresh one from the same member -- which is the point of withdrawing.
CREATE UNIQUE INDEX "member_vouches_active_pair_idx"
    ON "member_vouches" ("voucher_id", "target_id")
    WHERE "withdrawn_at" IS NULL;

-- The cap query counts a target's active vouches on every attempt, and the list
-- a member sees is the same prefix.
CREATE INDEX "member_vouches_target_idx" ON "member_vouches" ("target_id", "expires_at") WHERE "withdrawn_at" IS NULL;
