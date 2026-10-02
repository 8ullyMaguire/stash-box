-- SPEC §7.24.1 -- verified-unknown markers. The prerequisite for §7.24.2 through
-- §7.24.10: nothing in 95 migrations can express "this field is confirmed-absent", so
-- completion can never legitimately reach 100%, gaps that are unanswerable get
-- bountied forever, and quests recycle.
--
-- WHY A TABLE AND NOT A COLUMN ON EVERY ENTITY
--
-- The gap lives per (entity, FIELD), and the field set differs per entity type, so a
-- column per field would be ~40 nullable booleans replicated onto four tables, four
-- GraphQL types, and four edit-data structs. A row per assertion keeps the set open --
-- §0.3's point, that a CHECK list is a closed set with a documented extension procedure --
-- and means adding a field is a registry row rather than a migration.
--
-- WHY THE FIELD NAME IS A REGISTRY ROW AND NOT A CHECK CONSTRAINT
--
-- "birthdate" and "birth_date" are both plausible spellings, and a CHECK list on the
-- assertion table would have to enumerate every (entity_type, field) pair the instance
-- ever asserts on. That is exactly the set the instance extends at runtime, so a CHECK
-- cannot validate it. The registry is what constrains it: an assertion must name a
-- registered field for its entity type, and the FK below makes that a schema-level
-- guarantee rather than a service convention a future code path can forget.
--
-- THE TWO SCHEMA RULES, AND WHY THEY ARE NOT NEGOTIABLE
--
-- 1. EVERYTHING IS NULLABLE, and absence means MISSING. A row is an *assertion*: somebody
--    said "this value is not knowable". If these columns were NOT NULL, migrating an
--    existing database would have to invent assertions, and every invented "unknown" would
--    be a fact nobody verified -- the exact failure this feature exists to prevent. So a
--    scene with no rows here has every field MISSING, which is the correct reading of a
--    database that has never been told otherwise.
--
-- 2. reason_code IS NOT NULL. A verified-unknown without a reason is a way to clear a
--    gap without doing the work: assert "unknown" and the gap leaves the missing-field
--    list. §7.24.1 is explicit that "not publicly knowable" and "exists but nobody has
--    looked" are DIFFERENT FACTS, and a free-text or absent reason collapses them -- which
--    would make unanswerable gaps suppress themselves while remaining farmable.
--
-- asserted_by is NOT NULL for the same reason: an assertion with no author cannot be
-- weighed against trust, and §7.24.1 deliberately routes these through the edit/consensus
-- machinery so they earn trust like any other edit. That routing lives in the service --
-- this table is what it writes to.
--
-- citation_url is a plain text URL, not a FK, and that is a considered departure from the
-- url tables this schema otherwise uses (scene_urls, performer_urls, studio_urls).
--
-- The url tables are PER-ENTITY citations with their own lifecycle: a url row is a claim
-- about an entity, gets its own edit trail, and is what §9's address-shaped columns serve
-- from. An assertion's citation is not that -- it is a note about why a field is
-- unverifiable, so it has no entity to hang off (the performer may not even have a
-- performer_urls row for the page that says nothing about them) and adding one would put a
-- "this page does not mention height" claim into a table whose every other row asserts the
-- opposite kind of thing.
--
-- It is also NULLABLE and deliberately so: an assertion is a curator's judgement, not a
-- citation. "Nobody has published this performer's height" needs no URL at all. §7.24.2's
-- expected totals are the opposite case, and that is why 97 makes ITS source NOT NULL --
-- an unsourced total silently deflates every completion score, whereas an unsourced
-- unknown suppresses one gap and is a normal thing to record.
--
-- ON DELETE CASCADE, unlike most of the edit machinery. An assertion about a deleted
-- entity is not a historical record anyone needs: it describes a field of an entity that
-- no longer exists, and keeping it would let a re-created entity of the same name inherit
-- confidence nobody gave it.
-- The registered reason codes. A table rather than a CHECK so §0.3's extension procedure
-- applies: adding a reason is an INSERT, and the CHECK alternative would need a migration.
--
-- The three codes are the minimum that keeps §7.24.1's distinction alive. Notably
-- NOT_YET_LOOKED is the farmable one, and the service refuses XP for an assertion made by
-- someone who just edited the same entity -- which is only enforceable because the reason
-- is recorded at all: without it, "I assert this is unknown" and "I assert nobody has
-- checked" are the same row.
CREATE TABLE "field_verification_reasons" (
  "code" varchar(50) PRIMARY KEY,
  -- Machine-readable only. The human wording is i18n (SPEC §23), not a database string,
  -- because a stored English sentence cannot be translated for a reader.
  "description" text not null,
  -- Whether this reason SUPPRESSES the completion gap. A reason that does not suppress
  -- records a curator's belief without changing any score, and that is a legitimate thing
  -- to want to say -- so it is expressed here rather than inferred at the call site.
  --
  -- False for NOT_YET_LOOKED is the load-bearing value: the gap STAYS in the missing list,
  -- which is what stops an unanswerable question from being closed by assertion.
  "suppresses_gap" boolean not null,
  -- Whether a self-affirming assertion (same user, same entity, recently edited) is allowed.
  -- False everywhere today; the column exists because §7.24.1 makes this a rule with a
  -- reason ("the cheapest XP-per-minute farm in the system"), and a rule expressed only in
  -- service code is a rule a later code path can forget. If a reason ever becomes
  -- self-affirming-able, this is where it is expressed -- deliberately not by removing a
  -- check elsewhere.
  "allows_self_affirming" boolean not null
);

INSERT INTO "field_verification_reasons" ("code", "description", "suppresses_gap", "allows_self_affirming") VALUES
  -- The value is unanswerable from outside: not public, not released, does not exist.
  -- Suppresses the gap, because there is nothing left to do about it.
  ('not_publicly_knowable', 'The value is not publicly available, so no curator can supply it', true, false),
  -- The value exists and is knowable, and nobody has looked yet. Records a curator's
  -- belief, leaves the gap in place so it stays actionable, and still earns trust for
  -- having said so -- which is what makes it the FARMABLE one the XP rule targets.
  ('not_yet_looked', 'The value is knowable but has not been researched yet', false, false),
  -- A deliberate refusal: an owner has said this field is not to be catalogued. Suppresses
  -- the gap, because re-raising it every cycle is exactly the quest-recycling §7.24.1
  -- names. Distinct from not_publicly_knowable: this is a decision, not a fact about the
  -- world, and the two must not collapse into one another.
  ('declined_by_owner', 'The entity owner has declined this field being catalogued', true, false);

CREATE TABLE "field_verification_states" (
  "id" uuid PRIMARY KEY,
  -- The entity the claim is about. Polymorphic rather than four nullable FKs: a claim is
  -- about one entity of one type, and four nullable columns would permit a row claiming a
  -- scene and a studio simultaneously, which is not a fact about the world.
  "entity_type" varchar(50) not null,
  "entity_id" uuid not null,
  -- The field name as the edit data spells it (jsonb key on the entity's edit struct), not
  -- the column name. Constrained by the registry rather than a CHECK, see the header.
  "field" varchar(100) not null,
  -- §7.24.1: a reason CODE, never free text. See rule 2 above.
  "reason_code" varchar(50) not null REFERENCES "field_verification_reasons"("code") ON DELETE RESTRICT,
  "asserted_by" uuid not null REFERENCES "users"("id") ON DELETE CASCADE,
  "asserted_at" timestamp with time zone not null,
  -- NULL means "no citation offered", which is legitimate. See rule 2 in the header.
  "citation_url" text,
  -- The edit that carried this assertion, so the consensus trail is reachable from the
  -- assertion without a join back through edits.data (which is jsonb and unindexable).
  -- NULLABLE: a moderator applying a verified-unknown directly has no edit row, and §7.24.1
  -- requires the edit path to be the norm rather than the only path.
  "edit_id" uuid REFERENCES "edits"("id") ON DELETE SET NULL
);

-- One assertion per (entity, field). Without this, two curators asserting the same
-- unknown produce two rows and the gap-suppression count double-counts, so completion
-- can exceed what it should -- and §7.24.1's whole point is that a gap is suppressed ONCE
-- by ONE assertion.
CREATE UNIQUE INDEX "field_verification_states_entity_field_key"
  ON "field_verification_states" ("entity_type", "entity_id", "field");

-- The suppression query reads by (entity_type, entity_id) to list every assertion for one
-- entity, which the unique index above already serves as a prefix -- so this is for the
-- per-field lookups that ask "is this one field verified-unknown".
CREATE INDEX "field_verification_states_entity_field_idx"
  ON "field_verification_states" ("entity_type", "entity_id", "field");

-- "who asserted this, and when" is the moderator's audit question (§7.24.8's per-field
-- provenance). Not covered by the unique index, whose leading columns are the entity.
CREATE INDEX "field_verification_states_asserted_by_idx"
  ON "field_verification_states" ("asserted_by");
