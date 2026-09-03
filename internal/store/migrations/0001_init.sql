-- 0001_init: the full storage schema from DESIGN §13.
--
-- Everything is created at once, including tables no phase before P3 writes to.
-- The schema is designed as a whole and SQLite has no cheap ALTER; creating it
-- up front keeps later phases to data migrations instead of table rebuilds.
--
-- Table order is chosen so a reader can follow the dependency chain. SQLite
-- resolves foreign keys lazily (at DML time, not at CREATE), so the one
-- unavoidable cycle — workspaces.scenario_id <-> scenarios.workspace_id — is
-- fine as written.

-- ---------------------------------------------------------------- identity --

CREATE TABLE users (
  id            INTEGER PRIMARY KEY,
  -- Under shared-password auth the name IS the login: login is get-or-create by
  -- name (DESIGN §15). Without UNIQUE a stale cookie would silently mint a
  -- second "alex" and the first one's workspaces would vanish from their view.
  name          TEXT NOT NULL UNIQUE,
  email         TEXT,
  password_hash TEXT,
  external_id   TEXT UNIQUE,
  role          TEXT NOT NULL DEFAULT 'member',
  created_at    INTEGER NOT NULL
);

CREATE TABLE sessions (
  id         TEXT PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  csrf_token TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL
);
CREATE INDEX sessions_expiry ON sessions(expires_at);
CREATE INDEX sessions_user ON sessions(user_id);

-- -------------------------------------------------------------------- spec --

CREATE TABLE specs (
  id         INTEGER PRIMARY KEY,
  name       TEXT NOT NULL,
  version    TEXT,
  format     TEXT NOT NULL,                 -- oas31 | oas30 | swagger2
  source     TEXT NOT NULL,                 -- upload | bundle | url
  source_ref TEXT,
  base_path  TEXT NOT NULL DEFAULT '',      -- a HINT for the workspace default
  hash       TEXT NOT NULL UNIQUE,
  raw        BLOB NOT NULL,
  normalized BLOB NOT NULL,
  created_at INTEGER NOT NULL,
  created_by INTEGER REFERENCES users(id)
);

CREATE TABLE operations (
  id             INTEGER PRIMARY KEY,
  spec_id        INTEGER NOT NULL REFERENCES specs(id) ON DELETE CASCADE,
  method         TEXT NOT NULL,
  path           TEXT NOT NULL,             -- WITHOUT the base path (DESIGN §7)
  canonical_path TEXT NOT NULL,             -- parameters replaced by {}
  operation_id   TEXT,                      -- a label for humans, never a key
  summary        TEXT,
  tag            TEXT,
  source_order   INTEGER NOT NULL,
  pointer        TEXT NOT NULL,
  parse_error    TEXT,
  UNIQUE (spec_id, method, path)
);
CREATE INDEX operations_spec ON operations(spec_id, source_order);

CREATE TABLE operation_responses (
  id            INTEGER PRIMARY KEY,
  operation_id  INTEGER NOT NULL REFERENCES operations(id) ON DELETE CASCADE,
  selector      TEXT NOT NULL,              -- "200" | "2XX" | "default"
  http_status   INTEGER NOT NULL,           -- what we actually send
  is_default    INTEGER NOT NULL DEFAULT 0,
  media_type    TEXT,                       -- per status, not per operation
  status_origin TEXT NOT NULL,              -- numeric | 2XX | default | fallback
  schema_ptr    TEXT,
  UNIQUE (operation_id, selector)
);

CREATE TABLE resource_suggestions (
  id            INTEGER PRIMARY KEY,
  spec_id       INTEGER NOT NULL REFERENCES specs(id) ON DELETE CASCADE,
  gen           INTEGER NOT NULL DEFAULT 1, -- rederive adds a generation, never overwrites
  route_family  TEXT NOT NULL,
  name          TEXT NOT NULL,
  id_field      TEXT NOT NULL,
  parent_family TEXT,
  entity_schema TEXT NOT NULL,
  wrapper       TEXT,
  confidence    REAL NOT NULL,
  UNIQUE (spec_id, gen, route_family)
);

-- --------------------------------------------------------------- workspace --

CREATE TABLE workspaces (
  id           INTEGER PRIMARY KEY,
  slug         TEXT NOT NULL UNIQUE,
  name         TEXT NOT NULL,
  spec_id      INTEGER REFERENCES specs(id),
  owner_id     INTEGER REFERENCES users(id),
  forked_from  INTEGER REFERENCES workspaces(id),
  revision     INTEGER NOT NULL DEFAULT 1,  -- cache invalidation ONLY (DESIGN §12)
  scenario_id  INTEGER REFERENCES scenarios(id) ON DELETE SET NULL,
  settings     TEXT NOT NULL,
  created_at   INTEGER NOT NULL,
  updated_at   INTEGER NOT NULL
);
CREATE INDEX workspaces_owner ON workspaces(owner_id);

CREATE TABLE resource_decisions (
  workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  route_family TEXT NOT NULL,
  state        TEXT NOT NULL,               -- confirmed | declined
  PRIMARY KEY (workspace_id, route_family)
);

CREATE TABLE scenarios (
  id           INTEGER PRIMARY KEY,
  workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  name         TEXT NOT NULL,
  snapshot     BLOB NOT NULL,               -- same format as checkpoint and bundle
  created_at   INTEGER NOT NULL,
  UNIQUE (workspace_id, name)
);

-- ---------------------------------------------------------------- resource --

CREATE TABLE resources (
  id            INTEGER PRIMARY KEY,
  workspace_id  INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  route_family  TEXT NOT NULL,
  name          TEXT NOT NULL,
  id_field      TEXT NOT NULL DEFAULT 'id',
  id_strategy   TEXT NOT NULL DEFAULT 'seq',
  parent_id     INTEGER REFERENCES resources(id) ON DELETE CASCADE,
  scope_params  TEXT NOT NULL DEFAULT '[]',
  entity_schema TEXT NOT NULL,
  wrapper       TEXT,
  filter_map    TEXT NOT NULL DEFAULT '{}',
  write_form    TEXT,                       -- NULL = unrecognised, read-only
  seq           INTEGER NOT NULL DEFAULT 0,
  seed_count    INTEGER NOT NULL DEFAULT 10,
  UNIQUE (workspace_id, route_family)
);

CREATE TABLE entities (
  id               INTEGER PRIMARY KEY,
  resource_id      INTEGER NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
  parent_entity_id INTEGER REFERENCES entities(id) ON DELETE CASCADE,
  scope_key        TEXT NOT NULL DEFAULT '',
  entity_key       TEXT NOT NULL,
  data             TEXT NOT NULL,
  created_at       INTEGER NOT NULL,
  updated_at       INTEGER NOT NULL,
  UNIQUE (resource_id, scope_key, entity_key)
);
CREATE INDEX entities_list ON entities(resource_id, scope_key, id);
CREATE INDEX entities_parent ON entities(parent_entity_id);

-- ------------------------------------------------------------------- edits --

CREATE TABLE op_overrides (
  id             INTEGER PRIMARY KEY,
  workspace_id   INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  -- The natural key is (method, path): an edit must survive a re-import of the
  -- spec and a change of basePath, so it cannot hang off an operations row id.
  method         TEXT NOT NULL,
  path           TEXT NOT NULL,             -- WITHOUT the base path
  operation_id   INTEGER REFERENCES operations(id) ON DELETE SET NULL,  -- cache only
  override_on    INTEGER NOT NULL DEFAULT 1,
  route_off      INTEGER NOT NULL DEFAULT 0, -- operation disabled -> 404
  active_status  INTEGER,
  -- responses[status] = { mode, when[], body, body_encoding, media_type,
  --                       headers, schema_patch, recipes }
  -- mode lives INSIDE a variant, not on the row: the everyday case is a
  -- generated 200 next to a pinned 409, and one mode per operation makes that
  -- pair impossible. Switching status or mode never drops the other variants.
  responses      TEXT NOT NULL DEFAULT '{}',
  list_size      TEXT,
  delay_ms       TEXT,
  fail_directive TEXT,                      -- {status, mode: always|next_n|once, n}
  validate_req   INTEGER,
  resource_id    INTEGER REFERENCES resources(id) ON DELETE SET NULL,
  updated_at     INTEGER NOT NULL,
  UNIQUE (workspace_id, method, path)
);

CREATE TABLE custom_endpoints (
  id             INTEGER PRIMARY KEY,
  workspace_id   INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  method         TEXT NOT NULL,
  path           TEXT NOT NULL,
  canonical_path TEXT NOT NULL,
  source_order   INTEGER NOT NULL,
  override_on    INTEGER NOT NULL DEFAULT 1,
  route_off      INTEGER NOT NULL DEFAULT 0,
  active_status  INTEGER NOT NULL DEFAULT 200,
  responses      TEXT NOT NULL DEFAULT '{}',
  req_schema     TEXT,
  list_size      TEXT,
  delay_ms       TEXT,
  fail_directive TEXT,
  validate_req   INTEGER,
  resource_id    INTEGER REFERENCES resources(id) ON DELETE SET NULL,
  created_at     INTEGER NOT NULL,
  updated_at     INTEGER NOT NULL,
  UNIQUE (workspace_id, method, path),
  UNIQUE (workspace_id, method, canonical_path)
);

-- ----------------------------------------------------------------- history --

CREATE TABLE checkpoints (
  id           INTEGER PRIMARY KEY,
  workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  kind         TEXT NOT NULL,               -- auto | manual | pre-destructive
  label        TEXT NOT NULL,
  config_snap  BLOB NOT NULL,               -- settings + edits + endpoints + resources
  data_snap    BLOB,                        -- gzip'd entities, captured on EVERY write, even a workspace confirming nothing (P3d: not "only when asked for" -- CLAUDE.md's carve-out records the divergence from this comment's own original claim); NULL only when the capture itself degrades (over the probe budget, or the encoded document too large to compress)
  created_at   INTEGER NOT NULL,
  created_by   INTEGER REFERENCES users(id)
);
CREATE INDEX checkpoints_ws ON checkpoints(workspace_id, id DESC);

CREATE TABLE traffic (
  id           INTEGER PRIMARY KEY,
  workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  ts           INTEGER NOT NULL,
  method       TEXT NOT NULL,
  path         TEXT NOT NULL,
  peer_ip      TEXT,                        -- the immediate peer, always recorded
  fwd_ip       TEXT,                        -- from X-Forwarded-For, only if trusted
  matched_kind TEXT,                        -- operation | custom | none
  matched_id   INTEGER,
  status       INTEGER NOT NULL,
  duration_ms  REAL NOT NULL,
  req_headers  TEXT,                        -- secrets redacted before insert
  req_body     TEXT,                        -- bodies are redacted too (DESIGN §15)
  resp_body    TEXT,
  notes        TEXT,
  truncated    INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX traffic_ws ON traffic(workspace_id, id DESC);
