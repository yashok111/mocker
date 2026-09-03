-- 0005_custom_endpoints_stream: a custom endpoint gains a kind, and a stream
-- definition when that kind is a stream (P6b, decisions.md
-- mocker-p6b-sse-mock D2; DESIGN §30.2).
--
-- kind is 'http' | 'sse' | 'ws' ('ws' is refused by the Go validator until
-- P6d), NOT NULL DEFAULT 'http' -- the default is what every row in every
-- existing database already is, so no row moves and no backfill is needed
-- (unlike 0002_edit_version.sql, whose DEFAULT 0 was never a live row's legal
-- value). stream is the whole streaming definition as one JSON document,
-- NULL exactly when kind = 'http'. That coupling is a SQL CHECK and not a
-- Go-only rule, because it is the one invariant a hand-run UPDATE or a
-- restore from backup could otherwise break silently; the document's SHAPE
-- stays validated in Go (internal/customep/stream.go), in the one place both
-- writers already run through.
--
-- SQLite cannot add a CHECK with ALTER TABLE, so this is the tree's second
-- REBUILD migration after 0004_traffic_autoincrement.sql, and it preserves
-- every row with its id: create the new table, copy every row across in id
-- order, drop the old one, rename. custom_endpoints.id is a bare INTEGER
-- PRIMARY KEY with no AUTOINCREMENT (0001_init.sql) and stays that way -- the
-- rows' ids are carried verbatim by the INSERT ... SELECT, which is all a
-- rebuild has to promise; nothing references custom_endpoints(id) by foreign
-- key (traffic.matched_id is a plain integer), and the table's own foreign
-- keys (workspaces, resources) keep the values that already satisfied them.
--
-- The two UNIQUE constraints are recreated EXACTLY as 0001 declared them and
-- kind joins neither: admitting it into (workspace_id, method,
-- canonical_path) would legalise two custom rows at the identical (GET,
-- /events) shape, and router.compareRoutes has no rule that separates them
-- -- which of the two a client got would be decided silently by source_order.
-- One path cannot be both an ordinary GET and a stream; an operator who wants
-- both authors two paths (§30.2).
--
-- CRASH WINDOW, ACCEPTED, REPAIR HERE: as for 0004, applyMigration commits
-- this file and only then sets PRAGMA user_version. A crash between the two
-- re-runs this file over a table that is ALREADY rebuilt — and a second run
-- is NOT harmless: the copy below selects the LITERALS 'http' and NULL for
-- kind and stream (the pre-0005 table has no such columns to read), so a
-- re-run over the rebuilt table resets every sse/ws row to an http row
-- with no stream document. Nothing can write between the commit and the
-- PRAGMA, so a first run is safe; if a start ever fails on this file, the
-- repair is `PRAGMA user_version = 5;` by hand, and do NOT re-run the
-- rebuild.

CREATE TABLE custom_endpoints_new (
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
  edit_version   INTEGER NOT NULL DEFAULT 0,
  kind           TEXT NOT NULL DEFAULT 'http',
  stream         TEXT,
  UNIQUE (workspace_id, method, path),
  UNIQUE (workspace_id, method, canonical_path),
  CHECK (kind IN ('http', 'sse', 'ws')),
  CHECK ((kind = 'http') = (stream IS NULL))
);

INSERT INTO custom_endpoints_new
  (id, workspace_id, method, path, canonical_path, source_order, override_on, route_off,
   active_status, responses, req_schema, list_size, delay_ms, fail_directive, validate_req,
   resource_id, created_at, updated_at, edit_version, kind, stream)
SELECT
   id, workspace_id, method, path, canonical_path, source_order, override_on, route_off,
   active_status, responses, req_schema, list_size, delay_ms, fail_directive, validate_req,
   resource_id, created_at, updated_at, edit_version, 'http', NULL
FROM custom_endpoints
ORDER BY id;

DROP TABLE custom_endpoints;

ALTER TABLE custom_endpoints_new RENAME TO custom_endpoints;
