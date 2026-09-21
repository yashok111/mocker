CREATE TABLE design_scenarios (
  id                INTEGER PRIMARY KEY AUTOINCREMENT,
  name              TEXT NOT NULL,
  version           INTEGER NOT NULL DEFAULT 1,
  draft_revision_id INTEGER REFERENCES design_scenario_revisions(id) ON DELETE RESTRICT,
  created_at        INTEGER NOT NULL,
  updated_at        INTEGER NOT NULL
);

CREATE TABLE design_scenario_revisions (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  scenario_id INTEGER NOT NULL REFERENCES design_scenarios(id) ON DELETE RESTRICT,
  version     INTEGER NOT NULL,
  parent_id   INTEGER REFERENCES design_scenario_revisions(id) ON DELETE RESTRICT,
  hash        TEXT NOT NULL,
  document    TEXT NOT NULL,
  form_drafts TEXT NOT NULL,
  source      TEXT NOT NULL CHECK(source IN ('ui','mcp')),
  summary     TEXT NOT NULL,
  created_at  INTEGER NOT NULL,
  UNIQUE(scenario_id, version)
);

CREATE INDEX design_scenarios_draft_revision
  ON design_scenarios(draft_revision_id);
CREATE INDEX design_scenario_revisions_parent
  ON design_scenario_revisions(parent_id);
