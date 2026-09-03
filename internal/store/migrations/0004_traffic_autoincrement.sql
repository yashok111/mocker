-- 0004_traffic_autoincrement: traffic ids are never reissued (P6a, decisions.md
-- mocker-p6a-sse D20).
--
-- 0001_init.sql declared traffic.id as a bare INTEGER PRIMARY KEY, which in
-- SQLite is the rowid: once the HIGHEST rows are deleted, the next INSERT
-- reuses their ids. DELETE /api/workspaces/{id}/traffic deletes exactly the
-- newest rows of a workspace, so after a clear the next recorded request could
-- come back with an id BELOW a cursor another client still holds -- and
-- Repo.Since (id > since) would never return it. Before this slice that
-- client was one polling screen, which reopens without a cursor after its own
-- clear; with the SSE feed it is also every second tab, every curl -N, and the
-- browser's own reconnect racing the clear, each sitting on a connection that
-- looks alive and is permanently empty. AUTOINCREMENT makes SQLite track the
-- largest id ever used (sqlite_sequence) and never hand it out again, so a
-- cursor stays honest for the life of the database.
--
-- SQLite cannot add AUTOINCREMENT with ALTER TABLE, so this is the tree's first
-- REBUILD migration (0002 and 0003 are ADD COLUMN and cannot lose a row; this
-- one could, which is why every step below is spelled out). The rebuild
-- PRESERVES every row with its id unchanged: create the new table, copy every
-- row across in id order, drop the old table, rename, recreate the index. After
-- the copy sqlite_sequence sits at the highest id carried across, so the next
-- row continues above it rather than back at 1.
--
-- CRASH WINDOW, ACCEPTED, REPAIR HERE: applyMigration (internal/store/store.go)
-- commits this file's statements and only then sets PRAGMA user_version in a
-- separate statement. A crash between the two re-runs this whole file on the
-- next start, over a `traffic` that is ALREADY the rebuilt table: the second
-- run rebuilds it again, identically, and loses nothing -- so unlike 0003 the
-- re-run is idempotent rather than loud, and no manual repair is needed. If a
-- start ever fails on this file anyway, the repair is the same one statement
-- 0003 documents: `PRAGMA user_version = 4;` by hand, and do NOT re-run the
-- rebuild.
--
-- Foreign keys: traffic REFERENCES workspaces and nothing references traffic,
-- so dropping the old table breaks no constraint, and the copy keeps the
-- workspace_id values that already satisfied it.

CREATE TABLE traffic_new (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  ts           INTEGER NOT NULL,
  method       TEXT NOT NULL,
  path         TEXT NOT NULL,
  peer_ip      TEXT,
  fwd_ip       TEXT,
  matched_kind TEXT,
  matched_id   INTEGER,
  status       INTEGER NOT NULL,
  duration_ms  REAL NOT NULL,
  req_headers  TEXT,
  req_body     TEXT,
  resp_body    TEXT,
  notes        TEXT,
  truncated    INTEGER NOT NULL DEFAULT 0
);

INSERT INTO traffic_new
  (id, workspace_id, ts, method, path, peer_ip, fwd_ip, matched_kind, matched_id,
   status, duration_ms, req_headers, req_body, resp_body, notes, truncated)
SELECT
   id, workspace_id, ts, method, path, peer_ip, fwd_ip, matched_kind, matched_id,
   status, duration_ms, req_headers, req_body, resp_body, notes, truncated
FROM traffic
ORDER BY id;

DROP TABLE traffic;

ALTER TABLE traffic_new RENAME TO traffic;

-- The same index 0001_init.sql declared: (workspace_id, id DESC) is what
-- Repo.List, Repo.Since and pruneRetentionTx all walk.
CREATE INDEX traffic_ws ON traffic(workspace_id, id DESC);
