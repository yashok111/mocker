-- A retired resource ID can remain in an in-flight request's runtime.
-- Never hand it to a replacement family, even in a different workspace.
-- Foreign keys remain enabled throughout this rebuild. Save the dependent
-- data first: dropping resources cascades entities and nulls both endpoint
-- references. The replacement's self-reference points to the NEW table, so
-- dropping the old parent cannot cascade into the copied resource rows.
CREATE TEMP TABLE resource_identity_entities AS SELECT * FROM entities;
CREATE TEMP TABLE resource_identity_overrides AS SELECT id, resource_id FROM op_overrides WHERE resource_id IS NOT NULL;
CREATE TEMP TABLE resource_identity_endpoints AS SELECT id, resource_id FROM custom_endpoints WHERE resource_id IS NOT NULL;

CREATE TABLE resources_new (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  workspace_id  INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  route_family  TEXT NOT NULL,
  name          TEXT NOT NULL,
  id_field      TEXT NOT NULL DEFAULT 'id',
  id_strategy   TEXT NOT NULL DEFAULT 'seq',
  parent_id     INTEGER REFERENCES resources_new(id) ON DELETE CASCADE,
  scope_params  TEXT NOT NULL DEFAULT '[]',
  entity_schema TEXT NOT NULL,
  wrapper       TEXT,
  filter_map    TEXT NOT NULL DEFAULT '{}',
  write_form    TEXT,
  seq           INTEGER NOT NULL DEFAULT 0,
  seed_count    INTEGER NOT NULL DEFAULT 10,
  UNIQUE (workspace_id, route_family)
);
INSERT INTO resources_new SELECT * FROM resources;

-- Preserve the high-water mark even on a deliberate replay after deleting
-- the highest row (or every row). The first upgrade has no resources entry.
INSERT INTO sqlite_sequence(name, seq)
  SELECT 'resources_new', seq FROM sqlite_sequence
  WHERE name = 'resources' AND NOT EXISTS (SELECT 1 FROM sqlite_sequence WHERE name = 'resources_new');
UPDATE sqlite_sequence SET seq = max(seq, COALESCE((SELECT seq FROM sqlite_sequence WHERE name = 'resources'), 0))
  WHERE name = 'resources_new';

DROP TABLE resources;
ALTER TABLE resources_new RENAME TO resources;
CREATE INDEX resources_parent ON resources(parent_id);

-- A single INSERT restores self-referencing entity rows together; their
-- original IDs, base scopes, ancestor scopes and bytes remain unchanged.
INSERT INTO entities SELECT * FROM resource_identity_entities;
UPDATE op_overrides SET resource_id = (SELECT resource_id FROM resource_identity_overrides WHERE id = op_overrides.id)
  WHERE id IN (SELECT id FROM resource_identity_overrides);
UPDATE custom_endpoints SET resource_id = (SELECT resource_id FROM resource_identity_endpoints WHERE id = custom_endpoints.id)
  WHERE id IN (SELECT id FROM resource_identity_endpoints);
DROP TABLE resource_identity_entities;
DROP TABLE resource_identity_overrides;
DROP TABLE resource_identity_endpoints;
