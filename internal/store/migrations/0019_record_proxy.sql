-- Live outbound controls deliberately stay outside portable workspace snapshots.
-- Empty allocation table: sqlite_sequence survives deletion, so a proxy
-- config token is never reused even when a workspace ID is recycled.
CREATE TABLE proxy_versions (id INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE TABLE proxy_configs (
 workspace_id INTEGER PRIMARY KEY REFERENCES workspaces(id) ON DELETE CASCADE,
 version INTEGER NOT NULL,
 config TEXT NOT NULL
);
CREATE TABLE proxy_recordings (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
 request_key TEXT NOT NULL,
 method TEXT NOT NULL,
 path TEXT NOT NULL,
 status INTEGER NOT NULL,
 content_type TEXT NOT NULL,
 body BLOB NOT NULL,
 redacted INTEGER NOT NULL,
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL,
 UNIQUE(workspace_id, request_key)
);
