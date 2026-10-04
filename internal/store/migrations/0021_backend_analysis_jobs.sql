-- B4.2 startup rebuild. migration21.go pins the writer and disables foreign keys
-- before BEGIN; never rename the old referenced tables before dropping them.
CREATE TABLE backend_change_proposals_v21 (
 id TEXT PRIMARY KEY, project_id TEXT NOT NULL REFERENCES backend_projects(id),
 version INTEGER NOT NULL CHECK(version > 0), name TEXT NOT NULL,
 status TEXT NOT NULL CHECK(status IN ('draft','ready','implemented','archived')),
 current_draft_revision_id TEXT NOT NULL, current_draft_hash TEXT NOT NULL,
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
 ready_reference TEXT CHECK(ready_reference IS NULL OR (json_valid(ready_reference) AND json_type(ready_reference)='object')),
 UNIQUE(project_id,id),
 FOREIGN KEY(id,current_draft_revision_id) REFERENCES backend_change_proposal_revisions(proposal_id,id) DEFERRABLE INITIALLY DEFERRED
);
CREATE TABLE backend_change_proposal_batches_v21 (
 project_id TEXT NOT NULL, proposal_id TEXT NOT NULL, revision_id TEXT NOT NULL,
 action TEXT NOT NULL CHECK(action IN ('create','apply','restore','rebase')),
 restore_revision_id TEXT, commands TEXT NOT NULL CHECK(json_valid(commands) AND json_type(commands)='array'),
 commands_hash TEXT NOT NULL, document TEXT NOT NULL CHECK(json_valid(document)),
 PRIMARY KEY(proposal_id,revision_id),
 CHECK((action='restore')=(restore_revision_id IS NOT NULL)),
 CHECK((action='apply' AND json_array_length(commands) BETWEEN 1 AND 100) OR (action IN ('create','restore') AND json_array_length(commands)=0) OR (action='rebase' AND json_array_length(commands) BETWEEN 0 AND 100)),
 FOREIGN KEY(project_id,proposal_id,revision_id) REFERENCES backend_change_proposal_revisions(project_id,proposal_id,id),
 FOREIGN KEY(proposal_id,restore_revision_id) REFERENCES backend_change_proposal_revisions(proposal_id,id)
);
INSERT INTO backend_change_proposals_v21(id,project_id,version,name,status,current_draft_revision_id,current_draft_hash,created_at,updated_at)
 SELECT id,project_id,version,name,status,current_draft_revision_id,current_draft_hash,created_at,updated_at FROM backend_change_proposals;
INSERT INTO backend_change_proposal_batches_v21(project_id,proposal_id,revision_id,action,restore_revision_id,commands,commands_hash,document)
 SELECT project_id,proposal_id,revision_id,action,restore_revision_id,commands,commands_hash,document FROM backend_change_proposal_batches;
DROP TRIGGER backend_change_command_matches_batch;
DROP TABLE backend_change_proposals;
DROP TABLE backend_change_proposal_batches;
ALTER TABLE backend_change_proposals_v21 RENAME TO backend_change_proposals;
ALTER TABLE backend_change_proposal_batches_v21 RENAME TO backend_change_proposal_batches;
CREATE TRIGGER backend_change_command_matches_batch BEFORE INSERT ON backend_change_proposal_commands
WHEN NOT EXISTS (SELECT 1 FROM backend_change_proposal_batches b WHERE b.proposal_id=NEW.proposal_id AND b.revision_id=NEW.revision_id AND json_extract(b.commands,'$['||NEW.position||'].commandId')=NEW.command_id AND json(NEW.document)=json(json_extract(b.commands,'$['||NEW.position||']')))
BEGIN SELECT RAISE(ABORT,'change command must match immutable batch'); END;
CREATE TRIGGER backend_change_batch_no_update BEFORE UPDATE ON backend_change_proposal_batches BEGIN SELECT RAISE(ABORT,'immutable change batch'); END;
CREATE TRIGGER backend_change_batch_no_delete BEFORE DELETE ON backend_change_proposal_batches BEGIN SELECT RAISE(ABORT,'immutable change batch'); END;

-- Input and publication payloads are immutable. Byte fields are retained storage
-- accounting, reservations belong to the mutable job and include terminal space.
CREATE TABLE backend_analysis_inputs (
 project_id TEXT NOT NULL REFERENCES backend_projects(id),
 input_hash TEXT NOT NULL,
 document TEXT NOT NULL CHECK(json_valid(document)),
 input_bytes INTEGER NOT NULL CHECK(input_bytes >= 0),
 PRIMARY KEY(project_id,input_hash)
);
CREATE TABLE backend_analysis_jobs (
 id TEXT PRIMARY KEY,
 project_id TEXT NOT NULL REFERENCES backend_projects(id),
 input_hash TEXT NOT NULL,
 -- Storage deliberately reserves future analysis kinds. B4.2 service admits
 -- only diff/impact before admission; persisted kinds are never reinterpreted.
 kind TEXT NOT NULL CHECK(length(kind) BETWEEN 1 AND 64),
 status TEXT NOT NULL CHECK(status IN ('queued','running','completed','failed','cancelled','interrupted')),
 version INTEGER NOT NULL CHECK(version > 0),
 result_version INTEGER CHECK(result_version IS NULL OR result_version > 0),
 progress_states INTEGER NOT NULL DEFAULT 0 CHECK(progress_states >= 0),
 progress_dependency_visits INTEGER NOT NULL DEFAULT 0 CHECK(progress_dependency_visits >= 0),
 progress_findings INTEGER NOT NULL DEFAULT 0 CHECK(progress_findings >= 0),
 progress_records INTEGER NOT NULL DEFAULT 0 CHECK(progress_records >= 0),
 diagnostic TEXT CHECK(diagnostic IS NULL OR json_valid(diagnostic)),
 worker_token TEXT,
 result_bytes INTEGER NOT NULL DEFAULT 0 CHECK(result_bytes >= 0),
 reserved_output_bytes INTEGER NOT NULL DEFAULT 0 CHECK(reserved_output_bytes >= 0),
 reserved_terminal_bytes INTEGER NOT NULL DEFAULT 0 CHECK(reserved_terminal_bytes >= 0 AND reserved_terminal_bytes <= reserved_output_bytes),
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL,
 UNIQUE(project_id,id),
 FOREIGN KEY(project_id,input_hash) REFERENCES backend_analysis_inputs(project_id,input_hash),
 FOREIGN KEY(project_id,id,result_version) REFERENCES backend_analysis_manifests(project_id,job_id,result_version)
);
CREATE INDEX backend_analysis_jobs_queue ON backend_analysis_jobs(status,created_at,id);
CREATE INDEX backend_analysis_jobs_project ON backend_analysis_jobs(project_id,created_at,id);
CREATE TABLE backend_analysis_chunks (
 project_id TEXT NOT NULL,
 job_id TEXT NOT NULL,
 sequence INTEGER NOT NULL CHECK(sequence > 0),
 section TEXT NOT NULL CHECK(section IN ('changes','findings','witnesses','checks','gaps')),
 content_hash TEXT NOT NULL,
 items_json TEXT NOT NULL CHECK(json_valid(items_json) AND json_type(items_json)='array'),
 record_count INTEGER NOT NULL CHECK(record_count >= 0),
 chunk_bytes INTEGER NOT NULL CHECK(chunk_bytes >= 0),
 PRIMARY KEY(project_id,job_id,sequence),
 FOREIGN KEY(project_id,job_id) REFERENCES backend_analysis_jobs(project_id,id)
);
CREATE INDEX backend_analysis_chunks_section ON backend_analysis_chunks(project_id,job_id,section,sequence);
CREATE TABLE backend_analysis_manifests (
 project_id TEXT NOT NULL,
 job_id TEXT NOT NULL,
 result_version INTEGER NOT NULL CHECK(result_version > 0),
 -- Empty partial terminal reports have no chunks and therefore high-water zero.
 high_water_sequence INTEGER NOT NULL CHECK(high_water_sequence >= 0),
 result_hash TEXT NOT NULL,
 document TEXT NOT NULL CHECK(json_valid(document)),
 manifest_bytes INTEGER NOT NULL CHECK(manifest_bytes >= 0),
 PRIMARY KEY(project_id,job_id,result_version),
 FOREIGN KEY(project_id,job_id) REFERENCES backend_analysis_jobs(project_id,id)
);
CREATE TABLE backend_analysis_receipts (
 project_id TEXT NOT NULL REFERENCES backend_projects(id),
 action TEXT NOT NULL CHECK(action IN ('start','retry','cancel')),
 key TEXT NOT NULL,
 job_id TEXT NOT NULL,
 request_hash TEXT NOT NULL,
 response TEXT NOT NULL CHECK(json_valid(response)),
 response_bytes INTEGER NOT NULL CHECK(response_bytes >= 0),
 PRIMARY KEY(project_id,action,key),
 FOREIGN KEY(project_id,job_id) REFERENCES backend_analysis_jobs(project_id,id)
);
CREATE TRIGGER backend_analysis_input_no_update BEFORE UPDATE ON backend_analysis_inputs BEGIN SELECT RAISE(ABORT,'immutable analysis input'); END;
CREATE TRIGGER backend_analysis_input_no_delete BEFORE DELETE ON backend_analysis_inputs BEGIN SELECT RAISE(ABORT,'immutable analysis input'); END;
CREATE TRIGGER backend_analysis_chunk_no_update BEFORE UPDATE ON backend_analysis_chunks BEGIN SELECT RAISE(ABORT,'immutable analysis chunk'); END;
CREATE TRIGGER backend_analysis_chunk_no_delete BEFORE DELETE ON backend_analysis_chunks BEGIN SELECT RAISE(ABORT,'immutable analysis chunk'); END;
CREATE TRIGGER backend_analysis_manifest_no_update BEFORE UPDATE ON backend_analysis_manifests BEGIN SELECT RAISE(ABORT,'immutable analysis manifest'); END;
CREATE TRIGGER backend_analysis_manifest_no_delete BEFORE DELETE ON backend_analysis_manifests BEGIN SELECT RAISE(ABORT,'immutable analysis manifest'); END;
CREATE TRIGGER backend_analysis_receipt_no_update BEFORE UPDATE ON backend_analysis_receipts BEGIN SELECT RAISE(ABORT,'immutable analysis receipt'); END;
CREATE TRIGGER backend_analysis_receipt_no_delete BEFORE DELETE ON backend_analysis_receipts BEGIN SELECT RAISE(ABORT,'immutable analysis receipt'); END;
