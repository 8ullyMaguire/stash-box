DROP TABLE IF EXISTS list_audit;
DROP TABLE IF EXISTS list_items;
DROP TABLE IF EXISTS lists;

-- IF EXISTS on each, and in dependency order (children first). The order matters: dropping
-- `lists` while `list_items` still references it fails on the foreign key rather than
-- dropping, and the migration then half-applies -- leaving a schema whose state depends
-- on how far it got.