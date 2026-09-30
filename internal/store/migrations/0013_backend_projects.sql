CREATE TABLE backend_projects (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  version INTEGER NOT NULL CHECK (version > 0),
  current_revision_id TEXT NOT NULL REFERENCES backend_revisions(id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE backend_revisions (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL REFERENCES backend_projects(id) ON DELETE RESTRICT,
  document TEXT NOT NULL
);
CREATE INDEX backend_revisions_project ON backend_revisions(project_id, id);
CREATE INDEX backend_projects_revision ON backend_projects(current_revision_id);

CREATE TABLE backend_command_receipts (
  scope TEXT NOT NULL,
  key TEXT NOT NULL,
  request_hash TEXT NOT NULL,
  response TEXT NOT NULL,
  PRIMARY KEY (scope, key)
);
