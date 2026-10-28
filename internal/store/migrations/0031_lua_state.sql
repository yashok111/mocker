-- lua_state is A23's mock.state: the workspace-scoped key-value store a Lua
-- endpoint function reads and writes across requests (mock.state.get/set/
-- delete). It is DATA, exactly like entities: not a configuration layer, so
-- it is carried by no config_snap, covered by no checkpoint, tracked by no
-- workspaces.revision — and it is wiped by POST .../reset-data in both modes,
-- inside that verb's own transaction (resources.resetTx calls luastate.
-- WipeTx). A custom endpoint's route is not in any spec, which is precisely
-- why its state cannot live in entities: those require a confirmed resource
-- family from the bound spec.
--
-- value is the JSON bytes the helper stored, re-served byte for byte by
-- mock.state.get; json_valid keeps a stored value a JSON document the same
-- way the bundle tables do. The workspace FK gives the delete-workspace
-- cascade for free, the same way every workspace-owned table gets it.
CREATE TABLE lua_state (
  workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  key TEXT NOT NULL,
  value TEXT NOT NULL CHECK(json_valid(value)),
  updated_at INTEGER NOT NULL,
  PRIMARY KEY(workspace_id, key)
);
