CREATE TABLE backend_diagrams (
 project_id TEXT NOT NULL REFERENCES backend_projects(id), id TEXT NOT NULL,
 kind TEXT NOT NULL CHECK(kind IN ('architecture','interactions','lifecycle','business_map')),
 version INTEGER NOT NULL CHECK(version>0), target_json TEXT NOT NULL CHECK(json_valid(target_json)),
 PRIMARY KEY(project_id,id),
 FOREIGN KEY(project_id,id,version) REFERENCES backend_diagram_versions(project_id,diagram_id,version)
 DEFERRABLE INITIALLY DEFERRED
);
CREATE TABLE backend_diagram_versions (
 project_id TEXT NOT NULL, diagram_id TEXT NOT NULL, version INTEGER NOT NULL CHECK(version>0),
 content_hash TEXT NOT NULL, target_hash TEXT NOT NULL, document TEXT NOT NULL CHECK(json_valid(document)),
 author TEXT NOT NULL, created_at TEXT NOT NULL,
 provenance TEXT NOT NULL CHECK(json_valid(provenance)),
 provenance_hash TEXT NOT NULL CHECK(length(provenance_hash)=64),
 PRIMARY KEY(project_id,diagram_id,version),
 FOREIGN KEY(project_id,diagram_id) REFERENCES backend_diagrams(project_id,id)
);
CREATE TABLE backend_diagram_receipts (
 project_id TEXT NOT NULL REFERENCES backend_projects(id), operation TEXT NOT NULL,
 idempotency_key TEXT NOT NULL, request_hash TEXT NOT NULL, receipt TEXT NOT NULL CHECK(json_valid(receipt)),
 PRIMARY KEY(project_id,operation,idempotency_key)
);
CREATE TABLE backend_diagram_view_versions (
 project_id TEXT NOT NULL REFERENCES backend_projects(id), view_id TEXT NOT NULL,
 version INTEGER NOT NULL CHECK(version>0), document TEXT NOT NULL CHECK(json_valid(document)),
 PRIMARY KEY(project_id,view_id,version),
 FOREIGN KEY(project_id,view_id) REFERENCES backend_diagram_views(project_id,id)
);
CREATE TABLE backend_diagram_views (
 project_id TEXT NOT NULL REFERENCES backend_projects(id), id TEXT NOT NULL,
 version INTEGER NOT NULL CHECK(version>0), kind TEXT NOT NULL, name TEXT NOT NULL,
 PRIMARY KEY(project_id,id),
 FOREIGN KEY(project_id,id,version) REFERENCES backend_diagram_view_versions(project_id,view_id,version)
 DEFERRABLE INITIALLY DEFERRED
);
CREATE TABLE backend_diagram_catalog (
 project_id TEXT PRIMARY KEY REFERENCES backend_projects(id), version INTEGER NOT NULL CHECK(version>0)
);
CREATE TRIGGER backend_diagram_versions_no_update BEFORE UPDATE ON backend_diagram_versions BEGIN SELECT RAISE(ABORT,'immutable diagram version'); END;
CREATE TRIGGER backend_diagram_versions_no_delete BEFORE DELETE ON backend_diagram_versions BEGIN SELECT RAISE(ABORT,'immutable diagram version'); END;
CREATE TRIGGER backend_diagram_receipts_no_update BEFORE UPDATE ON backend_diagram_receipts BEGIN SELECT RAISE(ABORT,'immutable diagram receipt'); END;
CREATE TRIGGER backend_diagram_receipts_no_delete BEFORE DELETE ON backend_diagram_receipts BEGIN SELECT RAISE(ABORT,'immutable diagram receipt'); END;
CREATE TRIGGER backend_diagram_view_versions_no_update BEFORE UPDATE ON backend_diagram_view_versions BEGIN SELECT RAISE(ABORT,'immutable diagram view'); END;
CREATE TRIGGER backend_diagram_view_versions_no_delete BEFORE DELETE ON backend_diagram_view_versions BEGIN SELECT RAISE(ABORT,'immutable diagram view'); END;
CREATE TRIGGER backend_diagrams_advance BEFORE UPDATE ON backend_diagrams
WHEN NEW.project_id!=OLD.project_id OR NEW.id!=OLD.id OR NEW.kind!=OLD.kind OR NEW.target_json!=OLD.target_json OR NEW.version!=OLD.version+1
BEGIN SELECT RAISE(ABORT,'immutable diagram identity or invalid advance'); END;
CREATE TRIGGER backend_diagrams_no_delete BEFORE DELETE ON backend_diagrams BEGIN SELECT RAISE(ABORT,'immutable diagram identity'); END;
CREATE TRIGGER backend_diagram_views_advance BEFORE UPDATE ON backend_diagram_views
WHEN NEW.project_id!=OLD.project_id OR NEW.id!=OLD.id OR NEW.kind!=OLD.kind OR NEW.version!=OLD.version+1
BEGIN SELECT RAISE(ABORT,'immutable diagram view identity or invalid advance'); END;
CREATE TRIGGER backend_diagram_views_no_delete BEFORE DELETE ON backend_diagram_views BEGIN SELECT RAISE(ABORT,'immutable diagram view identity'); END;
