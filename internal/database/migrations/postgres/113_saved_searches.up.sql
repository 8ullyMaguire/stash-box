-- Saved queries. One row is a search a reader wants to keep -- a standing question they can
-- re-run, and optionally be told about.
--
-- THE TEXT IS THE CANONICAL FORM, and the filter is DERIVED by parsing it on every run
-- (docs/spec/feature-discovery.md section 3.2). A jsonb snapshot of the resolved filter would
-- be a second source of truth: it disagrees with the text the first time either is edited, and
-- it makes the stored row unreadable, unshareable and uneditable by the person who owns it.
-- The cost is stated in the spec and paid in the resolver: a tag merge or rename is not
-- rewritten into the stored text, and resolution follows the redirect instead
-- (internal/service/scene/query_language.go).
CREATE TABLE IF NOT EXISTS saved_searches (
  id               uuid NOT NULL DEFAULT gen_random_uuid(),
  user_id          uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,

  -- Per owner, not global: two people may each keep "unwatched anal" and mean different things
  -- by it, and a global namespace would make one of them a rename.
  name             text NOT NULL,
  query            text NOT NULL,

  -- Subscribe to it. The job writes ONE notification per run that found something new, never
  -- one per match: a standing query over a busy instance is a feed, not a mailbox.
  notify           boolean NOT NULL DEFAULT false,

  -- The cursor the notification job reads: it reports matches with a `created_at` after this
  -- instant and then moves it forward. NULL means "never notified", which is not the same as
  -- the epoch -- a search saved a year ago must not announce a year of matches on its first run.
  last_notified_at timestamp,

  created_at       timestamp NOT NULL DEFAULT now(),
  updated_at       timestamp NOT NULL DEFAULT now(),

  PRIMARY KEY (id),

  -- A blank name or query is not a search: the name is how it is found again, and an empty
  -- query is every scene, which is a browse and not a standing question.
  CONSTRAINT saved_searches_name_not_blank CHECK (btrim(name) <> ''),
  CONSTRAINT saved_searches_query_not_blank CHECK (btrim(query) <> ''),

  CONSTRAINT saved_searches_user_name_key UNIQUE (user_id, name)
);

-- The list view's access path: one owner's searches, most recently touched first.
CREATE INDEX IF NOT EXISTS saved_searches_user_updated_idx
  ON saved_searches (user_id, updated_at DESC);

-- The notification job's own path. Partial, because the job only ever wants the subscribed
-- rows and most saved searches are not subscribed -- a full index would be mostly dead weight
-- on a table whose whole purpose is to be read one owner at a time.
CREATE INDEX IF NOT EXISTS saved_searches_notifying_idx
  ON saved_searches (updated_at) WHERE notify;

COMMENT ON TABLE saved_searches IS
  'A stored search-language query per user. The query TEXT is canonical and the filter is derived by parsing it on every run (spec section 3.2); last_notified_at is the notification cursor.';

-- One new notification kind. `ADD VALUE` has no "IF NOT EXISTS" in older Postgres and is
-- idempotent here, which matters because this file runs on every instance once and on a
-- re-run of the migration harness in tests.
ALTER TYPE notification_type ADD VALUE IF NOT EXISTS 'SAVED_SEARCH_MATCH';
