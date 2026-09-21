CREATE TABLE design_scenario_runs (
  scenario_id INTEGER NOT NULL REFERENCES design_scenarios(id) ON DELETE RESTRICT,
  run_id TEXT NOT NULL,
  revision_id INTEGER NOT NULL REFERENCES design_scenario_revisions(id) ON DELETE RESTRICT,
  input_hash TEXT NOT NULL,
  version INTEGER NOT NULL,
  name TEXT NOT NULL,
  source TEXT NOT NULL CHECK(source IN ('ui','mcp')),
  status TEXT NOT NULL CHECK(status IN ('running','passed','failed','cancelled')),
  started_at INTEGER NOT NULL,
  finished_at INTEGER,
  reason TEXT NOT NULL DEFAULT '',
  report TEXT,
  PRIMARY KEY(scenario_id,run_id)
);

CREATE INDEX design_scenario_runs_recent ON design_scenario_runs(scenario_id,started_at DESC,run_id);
CREATE INDEX design_scenario_runs_revision ON design_scenario_runs(revision_id);
CREATE UNIQUE INDEX design_scenario_runs_active ON design_scenario_runs(scenario_id) WHERE status='running';
