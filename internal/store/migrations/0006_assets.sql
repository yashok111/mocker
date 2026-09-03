-- 0006_assets: uploaded files a mock can serve (A6, decisions.md
-- mocker-a6-assets D1; DESIGN §32.2).
--
-- One row per file, the bytes IN the row: §16's whole delivery story is one
-- file to back up, and a second object under /data would need its own
-- backup, orphan sweep and restore story. The natural key is
-- (workspace_id, name) — a bodyRef and an asset_url recipe address an asset
-- by NAME, never by id (§32.3), because a name survives delete-and-reupload
-- (the repair this product prescribes for a wrong object everywhere) and an
-- id does not; id exists for the same reason every table has one and is
-- never on the wire.
--
-- No CHECK on media_type: the executable-type refusal is
-- httpx.BrowserExecutableMediaType, a parser, not a list a CHECK could
-- spell, and it runs at BOTH the upload and the serve (§32.6) precisely so
-- that a row written some other way is refused where it would do harm.
-- ON DELETE CASCADE, like every per-workspace table: a deleted workspace
-- takes its files with it.
CREATE TABLE assets (
  id           INTEGER PRIMARY KEY,
  workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  name         TEXT    NOT NULL,
  media_type   TEXT    NOT NULL,
  size_bytes   INTEGER NOT NULL,
  sha256       TEXT    NOT NULL,
  data         BLOB    NOT NULL,
  created_at   INTEGER NOT NULL,
  updated_at   INTEGER NOT NULL
);
CREATE UNIQUE INDEX assets_ws_name ON assets(workspace_id, name);
