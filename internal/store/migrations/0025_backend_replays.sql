-- M_REPLAY25: durable trusted-fixture replay, independent from analysis jobs.
CREATE TABLE backend_replay_profiles (
 project_id TEXT NOT NULL REFERENCES backend_projects(id), id TEXT NOT NULL,
 version INTEGER NOT NULL CHECK(version>0), target_id TEXT NOT NULL,
 config_version INTEGER NOT NULL CHECK(config_version>0), identity_hash TEXT NOT NULL CHECK(length(identity_hash)=64),
 content_hash TEXT NOT NULL CHECK(length(content_hash)=64), document_json TEXT NOT NULL CHECK(json_valid(document_json) AND length(document_json)<=65536),
 authorization_json TEXT NOT NULL CHECK(json_valid(authorization_json)), author TEXT NOT NULL, created_at TEXT NOT NULL,
 PRIMARY KEY(project_id,id,version)
);
CREATE TABLE backend_replay_revocations (
 project_id TEXT NOT NULL, profile_id TEXT NOT NULL, profile_version INTEGER NOT NULL,
 author TEXT NOT NULL, created_at TEXT NOT NULL,
 PRIMARY KEY(project_id,profile_id,profile_version),
 FOREIGN KEY(project_id,profile_id,profile_version) REFERENCES backend_replay_profiles(project_id,id,version)
);
CREATE TABLE backend_replay_packages (
 project_id TEXT NOT NULL REFERENCES backend_projects(id), id TEXT NOT NULL,
 version INTEGER NOT NULL CHECK(version>0), content_hash TEXT NOT NULL CHECK(length(content_hash)=64),
 document_json TEXT NOT NULL CHECK(json_valid(document_json) AND length(document_json)<=4194304),
 target_hash TEXT NOT NULL, profile_id TEXT NOT NULL, profile_version INTEGER NOT NULL,
 author TEXT NOT NULL, created_at TEXT NOT NULL,
 PRIMARY KEY(project_id,id,version),
 FOREIGN KEY(project_id,profile_id,profile_version) REFERENCES backend_replay_profiles(project_id,id,version)
);
CREATE TABLE backend_replay_runs (
 id TEXT PRIMARY KEY, project_id TEXT NOT NULL REFERENCES backend_projects(id), target_id TEXT NOT NULL,
 status TEXT NOT NULL CHECK(status IN ('queued','running','succeeded','failed','unverified','cancelled','interrupted')),
 input_hash TEXT NOT NULL CHECK(length(input_hash)=64), input_json TEXT NOT NULL CHECK(json_valid(input_json) AND length(input_json)<=4194304),
 version INTEGER NOT NULL CHECK(version>0), author TEXT NOT NULL,
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
 terminal_report_json TEXT CHECK(terminal_report_json IS NULL OR (json_valid(terminal_report_json) AND length(terminal_report_json)<=4194304))
);
CREATE INDEX backend_replay_run_queue ON backend_replay_runs(status,created_at,id);
CREATE INDEX backend_replay_run_project ON backend_replay_runs(project_id,created_at,id);
CREATE TABLE backend_replay_receipts (
 project_id TEXT NOT NULL REFERENCES backend_projects(id), action TEXT NOT NULL, key TEXT NOT NULL,
 input_hash TEXT NOT NULL CHECK(length(input_hash)=64), result_json TEXT NOT NULL CHECK(json_valid(result_json) AND length(result_json)<=4194304),
 PRIMARY KEY(project_id,action,key)
);
CREATE TABLE backend_replay_steps (
 run_id TEXT NOT NULL REFERENCES backend_replay_runs(id), step_id TEXT NOT NULL, request_key TEXT NOT NULL,
 endpoint TEXT NOT NULL CHECK(endpoint IN ('reset','failure','order')), request_hash TEXT NOT NULL CHECK(length(request_hash)=64),
 request_json BLOB NOT NULL CHECK(length(request_json)<=65536),
 PRIMARY KEY(run_id,step_id), UNIQUE(run_id,request_key)
);
CREATE TABLE backend_replay_evidence (
 run_id TEXT NOT NULL REFERENCES backend_replay_runs(id), sequence INTEGER NOT NULL CHECK(sequence>0),
 kind TEXT NOT NULL, content_hash TEXT NOT NULL CHECK(length(content_hash)=64),
 body BLOB NOT NULL CHECK(length(body)<=4194304), PRIMARY KEY(run_id,sequence)
);
CREATE TABLE backend_replay_target_leases (
 target_id TEXT PRIMARY KEY, run_id TEXT NOT NULL UNIQUE REFERENCES backend_replay_runs(id),
 state TEXT NOT NULL CHECK(state IN ('active','uncertain'))
);
CREATE TABLE backend_replay_configs (
 target_id TEXT NOT NULL, version INTEGER NOT NULL CHECK(version>0), fingerprint TEXT NOT NULL,
 PRIMARY KEY(target_id,version)
);
CREATE TRIGGER backend_replay_terminal_immutable BEFORE UPDATE ON backend_replay_runs WHEN OLD.status NOT IN ('queued','running') BEGIN SELECT RAISE(ABORT,'immutable replay terminal'); END;
CREATE TRIGGER backend_replay_input_immutable BEFORE UPDATE OF input_hash,input_json,author,project_id,target_id ON backend_replay_runs BEGIN SELECT RAISE(ABORT,'immutable replay input'); END;
CREATE TRIGGER backend_replay_profiles_update BEFORE UPDATE ON backend_replay_profiles BEGIN SELECT RAISE(ABORT,'immutable replay evidence'); END;
CREATE TRIGGER backend_replay_profiles_delete BEFORE DELETE ON backend_replay_profiles BEGIN SELECT RAISE(ABORT,'immutable replay evidence'); END;
CREATE TRIGGER backend_replay_revocations_update BEFORE UPDATE ON backend_replay_revocations BEGIN SELECT RAISE(ABORT,'immutable replay evidence'); END;
CREATE TRIGGER backend_replay_revocations_delete BEFORE DELETE ON backend_replay_revocations BEGIN SELECT RAISE(ABORT,'immutable replay evidence'); END;
CREATE TRIGGER backend_replay_packages_update BEFORE UPDATE ON backend_replay_packages BEGIN SELECT RAISE(ABORT,'immutable replay evidence'); END;
CREATE TRIGGER backend_replay_packages_delete BEFORE DELETE ON backend_replay_packages BEGIN SELECT RAISE(ABORT,'immutable replay evidence'); END;
CREATE TRIGGER backend_replay_receipts_update BEFORE UPDATE ON backend_replay_receipts BEGIN SELECT RAISE(ABORT,'immutable replay evidence'); END;
CREATE TRIGGER backend_replay_receipts_delete BEFORE DELETE ON backend_replay_receipts BEGIN SELECT RAISE(ABORT,'immutable replay evidence'); END;
CREATE TRIGGER backend_replay_steps_update BEFORE UPDATE ON backend_replay_steps BEGIN SELECT RAISE(ABORT,'immutable replay evidence'); END;
CREATE TRIGGER backend_replay_steps_delete BEFORE DELETE ON backend_replay_steps BEGIN SELECT RAISE(ABORT,'immutable replay evidence'); END;
CREATE TRIGGER backend_replay_evidence_update BEFORE UPDATE ON backend_replay_evidence BEGIN SELECT RAISE(ABORT,'immutable replay evidence'); END;
CREATE TRIGGER backend_replay_evidence_delete BEFORE DELETE ON backend_replay_evidence BEGIN SELECT RAISE(ABORT,'immutable replay evidence'); END;
CREATE TRIGGER backend_replay_configs_update BEFORE UPDATE ON backend_replay_configs BEGIN SELECT RAISE(ABORT,'immutable replay evidence'); END;
CREATE TRIGGER backend_replay_configs_delete BEFORE DELETE ON backend_replay_configs BEGIN SELECT RAISE(ABORT,'immutable replay evidence'); END;
