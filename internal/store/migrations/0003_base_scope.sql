-- 0003_base_scope: entities gain the SECOND half of their scope key, the
-- declared value of a parameterised settings.basePath (P3h).
--
-- Before this migration an entity row was addressed by
-- (resource_id, scope_key, entity_key) alone, where scope_key is the ancestor
-- tuple inside a nested family (P3e/P3g). That leaves no way to tell apart two
-- requests that differ only in a basePath parameter -- /orgs/7/quizzes and
-- /orgs/8/quizzes glue into one compiled route (router.Build) and read the
-- SAME rows. base_scope_key is the tuple of declared base-path values a row
-- belongs to, encoded by the same resources.EncodeScope every other scope
-- tuple already goes through -- see internal/resources for the write side.
--
-- CRASH WINDOW, ACCEPTED, REPAIR HERE: applyMigration (internal/store/store.go)
-- commits this file's statements and only then sets PRAGMA user_version in a
-- separate statement, because the pragma takes a literal and cannot be
-- parameterised. SQLite has no ADD COLUMN IF NOT EXISTS, so a crash between
-- the commit and the pragma leaves a database that fails every later start
-- with "duplicate column name" -- the loud failure this tree's migrations are
-- required to produce, not silent corruption. The repair is one statement:
--
--   PRAGMA user_version = 3;
--
-- Run it by hand against the affected file and the next start proceeds
-- normally; the ALTER below must NOT be re-run (it is already applied).

ALTER TABLE entities ADD COLUMN base_scope_key TEXT NOT NULL DEFAULT '';

-- The DEFAULT is the empty base tuple, which is exactly what every request
-- against an unparameterised basePath computes (D3.1) -- so every row that
-- exists today, in every database, lands in the base scope it is already
-- served from and nothing observable changes. A workspace whose basePath DOES
-- carry a parameter (reachable without a manual edit -- an imported spec whose
-- servers[].url has an undefaulted {variable}, D5.4) has its existing rows
-- land in a base scope no parameterised request ever computes: they become
-- unreachable, and stay -- the same class of orphan P3e and P3g already leave
-- behind a decline. Guessing a base value here would be inventing operator
-- intent, and deleting rows would be the only irreversible loss in this tree
-- with no confirmSlug and no checkpoint in front of it (D5.3). The repair is
-- the one this project already prescribes for a wrong resource: decline the
-- family and confirm it again.

-- The UNIQUE (resource_id, scope_key, entity_key) table constraint is left
-- exactly as 0001_init.sql declared it -- NOT widened to include
-- base_scope_key. It is an inline table constraint, so SQLite backs it with
-- an implicit index no DROP INDEX can remove; widening it would mean the
-- documented create/copy/drop/rename rebuild of the one table in this schema
-- that holds operator data, and the rebuild buys nothing: entity_key is
-- minted from resources.seq, ONE counter per family across every scope
-- (P3g's rule), so two rows of one family never share an entity_key at all,
-- in any scope -- (resource_id, entity_key) is already unique in fact, and
-- the narrower constraint cannot be violated by correct code. A slice that
-- ever makes entity_key per-scope must widen this constraint first, and this
-- comment is where it will look.

-- The NAMED index moves, because it is droppable and it is what the new read
-- path uses: every list read is now scoped by base value first, ancestor
-- tuple second. entities_parent is untouched -- parent_entity_id stays NULL,
-- unmoved by this slice (D15).
DROP INDEX entities_list;
CREATE INDEX entities_list ON entities(resource_id, base_scope_key, scope_key, id);
