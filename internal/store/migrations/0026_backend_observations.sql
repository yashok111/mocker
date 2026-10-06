CREATE TABLE backend_observation_sets (
 project_id TEXT NOT NULL REFERENCES backend_projects(id), id TEXT NOT NULL,
 version INTEGER NOT NULL CHECK(version>0), name TEXT NOT NULL, context TEXT NOT NULL,
 logical_bytes INTEGER NOT NULL CHECK(logical_bytes>=0), record_count INTEGER NOT NULL,
 PRIMARY KEY(project_id,id),
 FOREIGN KEY(project_id,id,version) REFERENCES backend_observation_versions(project_id,set_id,version) DEFERRABLE INITIALLY DEFERRED
);
CREATE TABLE backend_observation_versions (
 project_id TEXT NOT NULL, set_id TEXT NOT NULL, version INTEGER NOT NULL,
 content_hash TEXT NOT NULL, document TEXT NOT NULL CHECK(json_valid(document)),
 PRIMARY KEY(project_id,set_id,version),
 FOREIGN KEY(project_id,set_id) REFERENCES backend_observation_sets(project_id,id)
);
CREATE TABLE backend_observation_blobs (
 hash TEXT PRIMARY KEY, document TEXT NOT NULL CHECK(json_valid(document))
);
CREATE TABLE backend_observation_members (
 project_id TEXT NOT NULL, set_id TEXT NOT NULL, record_id TEXT NOT NULL,
 hash TEXT NOT NULL REFERENCES backend_observation_blobs(hash), introduced_version INTEGER NOT NULL,
 PRIMARY KEY(project_id,set_id,record_id),
 FOREIGN KEY(project_id,set_id,introduced_version) REFERENCES backend_observation_versions(project_id,set_id,version) DEFERRABLE INITIALLY DEFERRED
);
CREATE INDEX backend_observation_page ON backend_observation_members(project_id,set_id,introduced_version,record_id);
CREATE TABLE backend_observation_receipts (
 project_id TEXT NOT NULL REFERENCES backend_projects(id), action TEXT NOT NULL, key TEXT NOT NULL,
 request_hash TEXT NOT NULL, document TEXT NOT NULL, PRIMARY KEY(project_id,action,key)
);
CREATE TABLE backend_observation_batches (
 project_id TEXT NOT NULL, set_id TEXT NOT NULL, batch_id TEXT NOT NULL, request_hash TEXT NOT NULL,
 PRIMARY KEY(project_id,set_id,batch_id), FOREIGN KEY(project_id,set_id) REFERENCES backend_observation_sets(project_id,id)
);
CREATE TABLE backend_observation_correlations (
 project_id TEXT NOT NULL, set_id TEXT NOT NULL, version INTEGER NOT NULL,
 content_hash TEXT NOT NULL, document TEXT NOT NULL CHECK(json_valid(document)),
 PRIMARY KEY(project_id,set_id,version), FOREIGN KEY(project_id,set_id) REFERENCES backend_observation_sets(project_id,id)
);
CREATE TRIGGER backend_observation_versions_update BEFORE UPDATE ON backend_observation_versions BEGIN SELECT RAISE(ABORT,'immutable observations'); END;
CREATE TRIGGER backend_observation_versions_delete BEFORE DELETE ON backend_observation_versions BEGIN SELECT RAISE(ABORT,'immutable observations'); END;
CREATE TRIGGER backend_observation_blobs_update BEFORE UPDATE ON backend_observation_blobs BEGIN SELECT RAISE(ABORT,'immutable observations'); END;
CREATE TRIGGER backend_observation_blobs_delete BEFORE DELETE ON backend_observation_blobs BEGIN SELECT RAISE(ABORT,'immutable observations'); END;
CREATE TRIGGER backend_observation_members_update BEFORE UPDATE ON backend_observation_members BEGIN SELECT RAISE(ABORT,'immutable observations'); END;
CREATE TRIGGER backend_observation_members_delete BEFORE DELETE ON backend_observation_members BEGIN SELECT RAISE(ABORT,'immutable observations'); END;
CREATE TRIGGER backend_observation_receipts_update BEFORE UPDATE ON backend_observation_receipts BEGIN SELECT RAISE(ABORT,'immutable observations'); END;
CREATE TRIGGER backend_observation_receipts_delete BEFORE DELETE ON backend_observation_receipts BEGIN SELECT RAISE(ABORT,'immutable observations'); END;
CREATE TRIGGER backend_observation_batches_update BEFORE UPDATE ON backend_observation_batches BEGIN SELECT RAISE(ABORT,'immutable observations'); END;
CREATE TRIGGER backend_observation_batches_delete BEFORE DELETE ON backend_observation_batches BEGIN SELECT RAISE(ABORT,'immutable observations'); END;
CREATE TRIGGER backend_observation_correlations_update BEFORE UPDATE ON backend_observation_correlations BEGIN SELECT RAISE(ABORT,'immutable observations'); END;
CREATE TRIGGER backend_observation_correlations_delete BEFORE DELETE ON backend_observation_correlations BEGIN SELECT RAISE(ABORT,'immutable observations'); END;
CREATE TRIGGER backend_observation_sets_update BEFORE UPDATE ON backend_observation_sets WHEN NEW.project_id<>OLD.project_id OR NEW.id<>OLD.id OR NEW.context<>OLD.context OR NEW.name<>OLD.name OR NEW.version<>OLD.version+1 BEGIN SELECT RAISE(ABORT,'immutable observation context'); END;
