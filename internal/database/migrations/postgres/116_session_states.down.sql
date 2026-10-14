-- Reverse of 116_session_states.up.sql.
--
-- The cursors go with the table, and that is the intended meaning of this down migration rather
-- than a side effect: the payloads are positions, which a client can regenerate by using the
-- surface again, and there is no copy of them anywhere else -- they were only ever written here
-- by a client that opted in.
--
-- No data-preserving alternative is offered on purpose. Keeping a copy in the down migration
-- would be keeping synced state after the table that promised it could be dropped, which is the
-- opposite of what a local-first surface should do.

DROP TABLE IF EXISTS "session_states";
