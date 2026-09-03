-- 0002_edit_version: per-row compare-and-swap token for A3.
--
-- Four tables gain edit_version INTEGER NOT NULL DEFAULT 0, and workspaces
-- also gains edit_seq INTEGER NOT NULL DEFAULT 0, the per-workspace sequence
-- that hands out edit_version values (internal/store/editversion.go). The
-- DEFAULT is 0 and is never a live row's value: every create allocates, and
-- this migration back-fills every EXISTING row with a value distinct within
-- its workspace, leaving each workspace's edit_seq at the highest value it
-- handed out. Without the back-fill every pre-migration row would sit at the
-- DEFAULT while edit_seq sits at 0, and the first allocation on an upgraded
-- database would stamp a number a live row already carries -- a collision no
-- test over a freshly seeded workspace can see (D4).
--
-- CRASH WINDOW, ACCEPTED, REPAIR HERE: applyMigration (internal/store/store.go)
-- commits this file's statements and only then sets PRAGMA user_version in a
-- separate statement, because the pragma takes a literal and cannot be
-- parameterised. SQLite has no ADD COLUMN IF NOT EXISTS, so a crash between
-- the commit and the pragma leaves a database that fails every later start
-- with "duplicate column name" -- the loud failure this tree's migrations are
-- required to produce, not silent corruption. The repair is one statement:
--
--   PRAGMA user_version = 2;
--
-- Run it by hand against the affected file and the next start proceeds
-- normally; the ALTERs below must NOT be re-run (they are already applied).

ALTER TABLE op_overrides    ADD COLUMN edit_version INTEGER NOT NULL DEFAULT 0;
ALTER TABLE custom_endpoints ADD COLUMN edit_version INTEGER NOT NULL DEFAULT 0;
ALTER TABLE workspaces      ADD COLUMN edit_version INTEGER NOT NULL DEFAULT 0;
ALTER TABLE workspaces      ADD COLUMN edit_seq     INTEGER NOT NULL DEFAULT 0;
ALTER TABLE scenarios       ADD COLUMN edit_version INTEGER NOT NULL DEFAULT 0;

-- Back-fill: give every existing row of op_overrides, custom_endpoints,
-- workspaces and scenarios a value distinct within its workspace. ROW_NUMBER
-- over each workspace, ordered by id for a stable, reproducible assignment,
-- with the four tables' rows for one workspace numbered in one contiguous
-- sequence (UNION ALL) so no two rows across all four tables collide either.
WITH ranked AS (
  SELECT 'op_overrides' AS tbl, id, workspace_id
    FROM op_overrides
  UNION ALL
  SELECT 'custom_endpoints', id, workspace_id
    FROM custom_endpoints
  UNION ALL
  SELECT 'workspaces', id, id
    FROM workspaces
  UNION ALL
  SELECT 'scenarios', id, workspace_id
    FROM scenarios
),
numbered AS (
  SELECT tbl, id, workspace_id,
         ROW_NUMBER() OVER (PARTITION BY workspace_id ORDER BY tbl, id) AS n
    FROM ranked
)
UPDATE op_overrides
   SET edit_version = (
     SELECT n FROM numbered
      WHERE numbered.tbl = 'op_overrides' AND numbered.id = op_overrides.id
   );

WITH ranked AS (
  SELECT 'op_overrides' AS tbl, id, workspace_id
    FROM op_overrides
  UNION ALL
  SELECT 'custom_endpoints', id, workspace_id
    FROM custom_endpoints
  UNION ALL
  SELECT 'workspaces', id, id
    FROM workspaces
  UNION ALL
  SELECT 'scenarios', id, workspace_id
    FROM scenarios
),
numbered AS (
  SELECT tbl, id, workspace_id,
         ROW_NUMBER() OVER (PARTITION BY workspace_id ORDER BY tbl, id) AS n
    FROM ranked
)
UPDATE custom_endpoints
   SET edit_version = (
     SELECT n FROM numbered
      WHERE numbered.tbl = 'custom_endpoints' AND numbered.id = custom_endpoints.id
   );

WITH ranked AS (
  SELECT 'op_overrides' AS tbl, id, workspace_id
    FROM op_overrides
  UNION ALL
  SELECT 'custom_endpoints', id, workspace_id
    FROM custom_endpoints
  UNION ALL
  SELECT 'workspaces', id, id
    FROM workspaces
  UNION ALL
  SELECT 'scenarios', id, workspace_id
    FROM scenarios
),
numbered AS (
  SELECT tbl, id, workspace_id,
         ROW_NUMBER() OVER (PARTITION BY workspace_id ORDER BY tbl, id) AS n
    FROM ranked
)
UPDATE workspaces
   SET edit_version = (
     SELECT n FROM numbered
      WHERE numbered.tbl = 'workspaces' AND numbered.id = workspaces.id
   );

WITH ranked AS (
  SELECT 'op_overrides' AS tbl, id, workspace_id
    FROM op_overrides
  UNION ALL
  SELECT 'custom_endpoints', id, workspace_id
    FROM custom_endpoints
  UNION ALL
  SELECT 'workspaces', id, id
    FROM workspaces
  UNION ALL
  SELECT 'scenarios', id, workspace_id
    FROM scenarios
),
numbered AS (
  SELECT tbl, id, workspace_id,
         ROW_NUMBER() OVER (PARTITION BY workspace_id ORDER BY tbl, id) AS n
    FROM ranked
)
UPDATE scenarios
   SET edit_version = (
     SELECT n FROM numbered
      WHERE numbered.tbl = 'scenarios' AND numbered.id = scenarios.id
   );

-- edit_seq is left at the HIGHEST value each workspace handed out above. The
-- back-fill's ROW_NUMBER() is contiguous 1..count within each workspace
-- partition, so the highest value handed out equals the total row count
-- across all four tables for that workspace -- no window function needed
-- here, just a count. The next allocation
-- (UPDATE workspaces SET edit_seq = edit_seq + 1 ...) then continues the
-- sequence rather than restarting it.
UPDATE workspaces
   SET edit_seq = (
     SELECT COUNT(*) FROM (
       SELECT workspace_id FROM op_overrides
       UNION ALL
       SELECT workspace_id FROM custom_endpoints
       UNION ALL
       SELECT id AS workspace_id FROM workspaces AS w2
       UNION ALL
       SELECT workspace_id FROM scenarios
     ) counted
      WHERE counted.workspace_id = workspaces.id
   );
