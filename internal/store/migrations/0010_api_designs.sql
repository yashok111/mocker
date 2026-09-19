CREATE TABLE api_designs (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 name TEXT NOT NULL,
 version INTEGER NOT NULL DEFAULT 1,
 draft_workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE RESTRICT,
 published_workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE RESTRICT,
 draft_revision_id INTEGER REFERENCES api_design_revisions(id) ON DELETE RESTRICT,
 published_revision_id INTEGER REFERENCES api_design_revisions(id) ON DELETE RESTRICT,
 latest_review_id INTEGER REFERENCES api_design_reviews(id) ON DELETE RESTRICT,
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL
);
CREATE TABLE api_design_workspaces (
 workspace_id INTEGER PRIMARY KEY REFERENCES workspaces(id) ON DELETE RESTRICT,
 design_id INTEGER NOT NULL REFERENCES api_designs(id) ON DELETE RESTRICT,
 role TEXT NOT NULL CHECK(role IN ('draft','published')),
 UNIQUE(design_id,role)
);
CREATE TABLE api_design_revisions (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 design_id INTEGER NOT NULL REFERENCES api_designs(id) ON DELETE RESTRICT,
 version INTEGER NOT NULL,
 parent_id INTEGER REFERENCES api_design_revisions(id) ON DELETE RESTRICT,
 spec_id INTEGER NOT NULL REFERENCES specs(id) ON DELETE RESTRICT,
 hash TEXT NOT NULL,
 document TEXT NOT NULL,
 source TEXT NOT NULL CHECK(source IN ('ui','mcp')),
 summary TEXT NOT NULL,
 change_set_id INTEGER REFERENCES api_design_change_sets(id) ON DELETE RESTRICT,
 created_at INTEGER NOT NULL,
 UNIQUE(design_id,version)
);
CREATE TABLE api_design_change_sets (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 design_id INTEGER NOT NULL REFERENCES api_designs(id) ON DELETE RESTRICT,
 title TEXT NOT NULL,
 base_revision_id INTEGER NOT NULL REFERENCES api_design_revisions(id) ON DELETE RESTRICT,
 status TEXT NOT NULL CHECK(status IN ('open','closed')),
 source TEXT NOT NULL CHECK(source IN ('ui','mcp')),
 created_at INTEGER NOT NULL
);
CREATE TABLE api_design_reviews (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 design_id INTEGER NOT NULL REFERENCES api_designs(id) ON DELETE RESTRICT,
 revision_id INTEGER NOT NULL REFERENCES api_design_revisions(id) ON DELETE RESTRICT,
 base_revision_id INTEGER NOT NULL REFERENCES api_design_revisions(id) ON DELETE RESTRICT,
 status TEXT NOT NULL CHECK(status IN ('pending','published','superseded')),
 summary TEXT NOT NULL,
 source TEXT NOT NULL CHECK(source IN ('ui','mcp')),
 created_at INTEGER NOT NULL
);
CREATE TABLE api_design_releases (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 design_id INTEGER NOT NULL REFERENCES api_designs(id) ON DELETE RESTRICT,
 revision_id INTEGER NOT NULL REFERENCES api_design_revisions(id) ON DELETE RESTRICT,
 review_id INTEGER NOT NULL UNIQUE REFERENCES api_design_reviews(id) ON DELETE RESTRICT,
 number INTEGER NOT NULL,
 created_at INTEGER NOT NULL,
 UNIQUE(design_id,number)
);
