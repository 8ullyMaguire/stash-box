-- content_items -- the content kinds that are not video (aggregator spec section 3.1,
-- aggregator plan phase 3).
--
-- WHAT THIS IS: one table for image, gallery, animation, text, audio and comic, plus its
-- chapters, its media URLs and its tags. A SCENE stays a scene: video keeps its own table,
-- its own fingerprinting, its own collage machinery and its own public GraphQL type, because
-- every existing client depends on those and the programme's no-loss rule is binding.
--
-- WHY ONE TABLE AND ONE ENTITY TYPE RATHER THAN SIX ENTITY TYPES. Every polymorphic surface in
-- this schema is keyed on (entity_type, entity_id): reviews, elo_ratings, list_items,
-- trust_events, denylist, identification, expected_totals, authored_quest_items. A new KIND of
-- thing therefore costs one EntityType member and no new plumbing on any of them, whereas six
-- entity types would have meant six rows in every Go vocabulary (the completion weights, the
-- elo type list, the archive counts) and six chances for one of them to be forgotten -- and a
-- forgotten vocabulary entry is invisible: the row is written and simply never counted.
--
-- Migration 108 already anticipated this shape. Its content_sources_entity_type_check accepts
-- ('scene', 'content_item') TODAY, because the source layer was built in phase 1 against this
-- decision. So adding the kind needs no constraint change anywhere -- measured, not assumed.
--
-- WHY `kind` AND NOT SIX TABLES: the six kinds share every column that matters and differ only
-- in their media handling, which lives in the two child tables (chapters for the reading kinds,
-- media rows for the variants). Six tables would also make "the newest item" and "items carrying
-- this tag" six queries each, and would put the shared columns in six places able to drift.
--
-- WHAT THIS TABLE DELIBERATELY DOES NOT HAVE: no `entity_type` column of its own -- a content
-- item IS the entity, and the polymorphism is on the OTHER side of the relationship. No
-- denormalised counts (tag_count, chapter_count): a count is derived and this schema does not
-- store derived values, because a stored count is a second answer able to drift from the rows
-- that justify it.
CREATE TABLE "content_items" (
  "id" uuid not null primary key,
  -- The kind is closed and enforced here rather than by six tables: an unknown kind would be a
  -- row nothing can render, nothing can filter and nothing counts.
  "kind" varchar(20) not null,
  "title" varchar(255) not null,
  "details" text,
  "release_date" date,
  -- Soft delete, added here rather than in a later migration even though the plan's column list
  -- does not name it. `scenes` has carried `deleted` since the initial schema, and every read in
  -- this repo filters it; retrofitting that filter across N queries later is precisely the class
  -- of omission this codebase keeps finding (a rule enforced in one place and forgotten in the
  -- next). A moderation phase that cannot hide a row deletes it, and a delete takes the tags and
  -- the identifications with it.
  "deleted" boolean not null default false,
  "created_at" timestamp not null default now(),
  "updated_at" timestamp not null default now(),
  CONSTRAINT "content_items_kind_check" CHECK (
    "kind" IN ('image', 'gallery', 'animation', 'text', 'audio', 'comic')
  ),
  -- A title is what every list, search result and backlink shows. An item without one is a row
  -- whose label would have to be invented by the renderer, differently in each renderer.
  CONSTRAINT "content_items_title_not_blank" CHECK (btrim("title") <> '')
);

-- The two orders the phase asks for: the newest items, and the items of one kind.
-- `created_at DESC` rather than the release date, for the reason phase 7 slice 6 recorded the
-- hard way: a bounded pool ordered by a NULLABLE field never contains the rows that lack it, so
-- undated items would silently not exist on any browsable page.
CREATE INDEX "content_items_created_at_idx" ON "content_items" ("created_at" DESC);
CREATE INDEX "content_items_kind_created_at_idx" ON "content_items" ("kind", "created_at" DESC);

-- Chapters: for the reading kinds (text, comic) and for a serialised animation. `number` is the
-- chapter's own position, which is what a reader resumes at; it is unique per item so a feed
-- cannot show two "chapter 4"s and leave a reader guessing which one they had reached.
CREATE TABLE "content_item_chapters" (
  "id" uuid not null primary key,
  "content_item_id" uuid not null,
  "number" integer not null,
  "title" varchar(255),
  -- Both optional and mutually exclusive in practice: a comic chapter has pages, an episode has
  -- seconds. Neither is required, because a chapter row that is just "chapter 5 exists" is still
  -- progress a reader can record.
  "page_count" integer,
  "duration_seconds" integer,
  "created_at" timestamp not null default now(),
  "updated_at" timestamp not null default now(),
  foreign key ("content_item_id") references "content_items" ("id") ON DELETE CASCADE,
  unique ("content_item_id", "number"),
  CONSTRAINT "content_item_chapters_number_positive" CHECK ("number" > 0),
  CONSTRAINT "content_item_chapters_page_count_positive" CHECK ("page_count" IS NULL OR "page_count" > 0),
  CONSTRAINT "content_item_chapters_duration_positive" CHECK ("duration_seconds" IS NULL OR "duration_seconds" > 0)
);

CREATE INDEX "content_item_chapters_item_idx" ON "content_item_chapters" ("content_item_id", "number");

-- Media variants: the URL this item can actually be fetched from, and what each URL IS.
-- Phase 10 fills this table (the storage plane); the table lands here, empty, because the shape
-- of the media row is what decides whether phase 10 needs a new one, and a missing table would
-- have made that decision by accident.
--
-- ON DELETE CASCADE on the item, so removing an item cannot leave fetchable URLs pointing at
-- something the archive no longer lists.
CREATE TABLE "content_item_media" (
  "id" uuid not null primary key,
  "content_item_id" uuid not null,
  "url" text not null,
  -- `original` is the item itself; the rest are derived or alternate renditions.
  "kind" varchar(20) not null,
  "width" integer,
  "height" integer,
  "duration_seconds" integer,
  "created_at" timestamp not null default now(),
  foreign key ("content_item_id") references "content_items" ("id") ON DELETE CASCADE,
  unique ("content_item_id", "url"),
  CONSTRAINT "content_item_media_kind_check" CHECK (
    "kind" IN ('original', 'preview', 'stream', 'download', 'archive')
  ),
  CONSTRAINT "content_item_media_url_not_blank" CHECK (btrim("url") <> ''),
  CONSTRAINT "content_item_media_dimensions_positive" CHECK (
    ("width" IS NULL OR "width" > 0) AND ("height" IS NULL OR "height" > 0)
  )
);

CREATE INDEX "content_item_media_item_idx" ON "content_item_media" ("content_item_id");

-- Tags, mirroring `scene_tags` from migration 01.
--
-- WHY A SECOND TAG-LINK TABLE RATHER THAN A GENERIC `entity_tags`: `scene_tags` is the hot path
-- for every existing tag query, every facet count and the implication closure, so replacing it
-- with a polymorphic table would rewrite all of those for one new kind of row. The cost of this
-- choice is stated rather than hidden: a query that wants "everything carrying this tag" now has
-- to name both tables, and there are exactly two of them. A third kind of taggable entity would
-- be the point at which a generic table becomes the right answer; it is not this slice.
CREATE TABLE "content_item_tags" (
  "content_item_id" uuid not null,
  "tag_id" uuid not null,
  foreign key ("content_item_id") references "content_items" ("id") ON DELETE CASCADE,
  foreign key ("tag_id") references "tags" ("id") ON DELETE CASCADE,
  unique ("content_item_id", "tag_id")
);

-- The reverse direction (tag -> items) is the one the tag page needs, and the unique constraint
-- above covers only (item, tag).
CREATE INDEX "content_item_tags_tag_idx" ON "content_item_tags" ("tag_id", "content_item_id");
