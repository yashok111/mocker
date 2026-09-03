-- 0007_fk_indexes: an index behind every foreign key that had none.
--
-- SQLite does not index a REFERENCES column on its own, and with
-- foreign_keys=ON every DELETE of a parent row scans each child table that
-- points at it to enforce the clause. Measured on the real schema
-- (2026-09-03 audit, EXPLAIN QUERY PLAN): `DELETE FROM resources WHERE id
-- = ?` — every decline and every reset — scanned custom_endpoints,
-- op_overrides and resources whole; `DELETE FROM specs` scanned workspaces
-- and then, per cascaded operations row, op_overrides again; `DELETE FROM
-- scenarios` scanned workspaces. All of these tables are cross-workspace,
-- so the cost grew with the installation, not with the workspace being
-- edited.
--
-- ADD-only: plain CREATE INDEX, no rebuild, and a re-run fails loudly on
-- the first duplicate name, which is what a migration that must never run
-- twice should do (contrast 0004/0005's own comments).
CREATE INDEX op_overrides_operation ON op_overrides(operation_id);
CREATE INDEX op_overrides_resource ON op_overrides(resource_id);
CREATE INDEX custom_endpoints_resource ON custom_endpoints(resource_id);
CREATE INDEX resources_parent ON resources(parent_id);
CREATE INDEX workspaces_spec ON workspaces(spec_id);
CREATE INDEX workspaces_scenario ON workspaces(scenario_id);
CREATE INDEX workspaces_forked_from ON workspaces(forked_from);
