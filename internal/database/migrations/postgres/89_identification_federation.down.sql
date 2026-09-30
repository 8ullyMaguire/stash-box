-- Reverse of 89_identification_federation.up.sql, in reverse creation order.
--
-- identification_foreign_candidates is dropped first because it holds foreign
-- keys into both other tables. Dropping federation_peers first would leave
-- those FKs dangling, and Postgres refuses the drop rather than cascading --
-- which is the correct behaviour and an unhelpful error message.

DROP TABLE IF EXISTS "identification_foreign_candidates";
DROP TABLE IF EXISTS "federation_peers";
