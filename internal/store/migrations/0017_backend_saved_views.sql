CREATE TABLE backend_saved_views (
 id TEXT PRIMARY KEY,
 project_id TEXT NOT NULL REFERENCES backend_projects(id) ON DELETE RESTRICT,
 version INTEGER NOT NULL CHECK(version>0),
 name TEXT NOT NULL,
 kind TEXT NOT NULL CHECK(kind IN ('flow','database')),
 target_json TEXT NOT NULL,
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL,
 UNIQUE(project_id,id),
 FOREIGN KEY(id,version) REFERENCES backend_saved_view_versions(view_id,version) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED
);
CREATE INDEX backend_saved_views_project ON backend_saved_views(project_id,id);
CREATE TABLE backend_saved_view_versions (
 view_id TEXT NOT NULL REFERENCES backend_saved_views(id) ON DELETE RESTRICT,
 version INTEGER NOT NULL CHECK(version>0),
 document TEXT NOT NULL,
 PRIMARY KEY(view_id,version)
);
CREATE TRIGGER backend_saved_view_binding_immutable
BEFORE UPDATE OF id,project_id,kind,target_json,created_at ON backend_saved_views
WHEN NEW.id IS NOT OLD.id OR NEW.project_id IS NOT OLD.project_id OR NEW.kind IS NOT OLD.kind OR NEW.target_json IS NOT OLD.target_json OR NEW.created_at IS NOT OLD.created_at
BEGIN SELECT RAISE(ABORT,'Saved view binding is immutable'); END;
CREATE TRIGGER backend_saved_view_version_immutable_update BEFORE UPDATE ON backend_saved_view_versions
BEGIN SELECT RAISE(ABORT,'Saved view versions are immutable'); END;
CREATE TRIGGER backend_saved_view_version_immutable_delete BEFORE DELETE ON backend_saved_view_versions
BEGIN SELECT RAISE(ABORT,'Saved view versions are immutable'); END;
