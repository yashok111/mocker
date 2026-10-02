CREATE TABLE backend_revision_api_artifacts (
 revision_id TEXT PRIMARY KEY REFERENCES backend_revisions(id) ON DELETE RESTRICT,
 source_content_hash TEXT NOT NULL,
 source_semantic_hash TEXT NOT NULL,
 document TEXT NOT NULL
);
CREATE TRIGGER backend_revision_api_artifacts_immutable_update BEFORE UPDATE ON backend_revision_api_artifacts
BEGIN SELECT RAISE(ABORT,'API artifact context is immutable'); END;
CREATE TRIGGER backend_revision_api_artifacts_immutable_delete BEFORE DELETE ON backend_revision_api_artifacts
BEGIN SELECT RAISE(ABORT,'API artifact context is immutable'); END;
