-- B4.1 metadata and source/proposal storage. Existing immutable documents are
-- intentionally not rewritten when the new protocol becomes available.

-- Annotations are metadata: graph membership is deliberately not a foreign key.
-- Migration0016's backend_revision_owner already supplies this owned revision key.
CREATE TABLE backend_annotations (
 id TEXT PRIMARY KEY,
 project_id TEXT NOT NULL REFERENCES backend_projects(id),
 record_type TEXT NOT NULL CHECK(record_type IN ('node','edge')),
 target_id TEXT NOT NULL,
 revision_id TEXT,
 body TEXT NOT NULL,
 author TEXT NOT NULL,
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL,
 deleted_at TEXT,
 FOREIGN KEY(project_id,revision_id) REFERENCES backend_revisions(project_id,id),
 CHECK(deleted_at IS NULL OR body='')
);
CREATE INDEX backend_annotations_project ON backend_annotations(project_id,id);
CREATE INDEX backend_annotations_target ON backend_annotations(project_id,record_type,target_id);

-- Source6 provider claims and retained legacy proof are immutable per revision.
CREATE TABLE backend_revision_assertions (
 project_id TEXT NOT NULL,
 revision_id TEXT NOT NULL,
 record_type TEXT NOT NULL CHECK(record_type IN ('node','edge')),
 record_id TEXT NOT NULL,
 repository_id TEXT NOT NULL REFERENCES backend_repositories(id),
 provider_namespace TEXT NOT NULL,
 external_key TEXT NOT NULL,
 assertion_hash TEXT NOT NULL,
 document TEXT NOT NULL CHECK(json_valid(document)),
 PRIMARY KEY(revision_id,record_type,record_id,repository_id,provider_namespace),
 UNIQUE(revision_id,repository_id,provider_namespace,record_type,external_key),
 FOREIGN KEY(project_id,revision_id) REFERENCES backend_revisions(project_id,id)
);
CREATE INDEX backend_assertions_subject ON backend_revision_assertions(project_id,revision_id,record_type,record_id);
CREATE TRIGGER backend_assertions_insert BEFORE INSERT ON backend_revision_assertions BEGIN
 SELECT CASE WHEN NOT EXISTS (SELECT 1 FROM backend_repositories WHERE id=NEW.repository_id AND project_id=NEW.project_id)
 OR NOT EXISTS (SELECT 1 FROM backend_graph_records WHERE project_id=NEW.project_id AND revision_id=NEW.revision_id AND record_type=NEW.record_type AND id=NEW.record_id)
 THEN RAISE(ABORT,'source assertion ownership mismatch') END;
END;
CREATE TRIGGER backend_assertions_update BEFORE UPDATE ON backend_revision_assertions BEGIN SELECT RAISE(ABORT,'source assertions are immutable'); END;
CREATE TRIGGER backend_assertions_delete BEFORE DELETE ON backend_revision_assertions BEGIN SELECT RAISE(ABORT,'source assertions are immutable'); END;
CREATE TABLE backend_revision_assertion_resolutions (
 project_id TEXT NOT NULL,
 revision_id TEXT NOT NULL,
 record_type TEXT NOT NULL CHECK(record_type IN ('node','edge')),
 record_id TEXT NOT NULL,
 property_key TEXT NOT NULL,
 conflict_hash TEXT NOT NULL,
 document TEXT NOT NULL CHECK(json_valid(document)),
 PRIMARY KEY(revision_id,record_type,record_id,property_key),
 FOREIGN KEY(project_id,revision_id) REFERENCES backend_revisions(project_id,id)
);
CREATE TRIGGER backend_assertion_resolutions_insert BEFORE INSERT ON backend_revision_assertion_resolutions BEGIN
 SELECT CASE WHEN NOT EXISTS (SELECT 1 FROM backend_revision_assertions WHERE project_id=NEW.project_id AND revision_id=NEW.revision_id AND record_type=NEW.record_type AND record_id=NEW.record_id AND repository_id=json_extract(NEW.document,'$.select.repositoryId') AND provider_namespace=json_extract(NEW.document,'$.select.providerNamespace') AND assertion_hash=json_extract(NEW.document,'$.select.assertionHash'))
 THEN RAISE(ABORT,'source resolution has no selected claim') END;
END;
CREATE TRIGGER backend_assertion_resolutions_update BEFORE UPDATE ON backend_revision_assertion_resolutions BEGIN SELECT RAISE(ABORT,'source resolutions are immutable'); END;
CREATE TRIGGER backend_assertion_resolutions_delete BEFORE DELETE ON backend_revision_assertion_resolutions BEGIN SELECT RAISE(ABORT,'source resolutions are immutable'); END;
CREATE TABLE backend_revision_legacy_proof_bases (
 project_id TEXT NOT NULL,
 revision_id TEXT NOT NULL,
 evidence_id TEXT NOT NULL,
 source_revision_id TEXT NOT NULL,
 basis_hash TEXT NOT NULL,
 document TEXT NOT NULL CHECK(json_valid(document)),
 PRIMARY KEY(revision_id,evidence_id),
 FOREIGN KEY(project_id,revision_id) REFERENCES backend_revisions(project_id,id),
 FOREIGN KEY(project_id,source_revision_id) REFERENCES backend_revisions(project_id,id)
);
CREATE TRIGGER backend_legacy_proof_bases_insert BEFORE INSERT ON backend_revision_legacy_proof_bases BEGIN
 SELECT CASE WHEN NOT EXISTS (SELECT 1 FROM backend_revisions r JOIN backend_graph_records e ON e.revision_id=r.id AND e.project_id=r.project_id AND e.record_type='evidence' AND e.id=NEW.evidence_id JOIN backend_graph_records s ON s.revision_id=r.id AND s.project_id=r.project_id AND s.record_type=json_extract(NEW.document,'$.recordType') AND s.id=json_extract(NEW.document,'$.recordId') WHERE r.id=NEW.source_revision_id AND r.project_id=NEW.project_id AND json_extract(r.document,'$.schemaVersion')='5' AND e.subject_id=s.id)
 OR NOT EXISTS (SELECT 1 FROM backend_graph_records e JOIN backend_revision_assertions a ON a.revision_id=e.revision_id AND a.project_id=e.project_id AND a.record_id=e.subject_id WHERE e.revision_id=NEW.revision_id AND e.project_id=NEW.project_id AND e.record_type='evidence' AND e.id=NEW.evidence_id AND EXISTS (SELECT 1 FROM json_each(a.document,'$.evidenceIds') WHERE value=NEW.evidence_id))
 THEN RAISE(ABORT,'legacy proof basis ownership mismatch') END;
END;
CREATE TRIGGER backend_legacy_proof_bases_update BEFORE UPDATE ON backend_revision_legacy_proof_bases BEGIN SELECT RAISE(ABORT,'legacy proof bases are immutable'); END;
CREATE TRIGGER backend_legacy_proof_bases_delete BEFORE DELETE ON backend_revision_legacy_proof_bases BEGIN SELECT RAISE(ABORT,'legacy proof bases are immutable'); END;
CREATE TABLE backend_import_source_decisions (
 session_id TEXT NOT NULL REFERENCES backend_import_sessions(id),
 decision_id TEXT NOT NULL,
 decision_kind TEXT NOT NULL CHECK(decision_kind IN ('claim_identity','resolve_assertion')),
 decision_key TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('active','superseded')),
 sequence INTEGER NOT NULL CHECK(sequence>0),
 document TEXT NOT NULL CHECK(json_valid(document)),
 input_hash TEXT NOT NULL,
 PRIMARY KEY(session_id,decision_id),
 UNIQUE(session_id,sequence)
);
CREATE UNIQUE INDEX backend_import_source_active_decisions ON backend_import_source_decisions(session_id,decision_kind,decision_key) WHERE state='active';

-- Full graph proposals are independent of source and legacy relational proposals.
CREATE TABLE backend_change_proposals (
 id TEXT PRIMARY KEY, project_id TEXT NOT NULL REFERENCES backend_projects(id),
 version INTEGER NOT NULL CHECK(version > 0), name TEXT NOT NULL,
 status TEXT NOT NULL CHECK(status='draft'),
 current_draft_revision_id TEXT NOT NULL, current_draft_hash TEXT NOT NULL,
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
 UNIQUE(project_id,id),
 FOREIGN KEY(id,current_draft_revision_id) REFERENCES backend_change_proposal_revisions(proposal_id,id) DEFERRABLE INITIALLY DEFERRED
);
CREATE TABLE backend_change_proposal_revisions (
 id TEXT PRIMARY KEY, project_id TEXT NOT NULL, proposal_id TEXT NOT NULL,
 parent_revision_id TEXT, base_revision_id TEXT NOT NULL,
 document TEXT NOT NULL CHECK(json_valid(document)),
 UNIQUE(proposal_id,id), UNIQUE(project_id,proposal_id,id),
 FOREIGN KEY(project_id,proposal_id) REFERENCES backend_change_proposals(project_id,id),
 FOREIGN KEY(project_id,base_revision_id) REFERENCES backend_revisions(project_id,id),
 FOREIGN KEY(proposal_id,parent_revision_id) REFERENCES backend_change_proposal_revisions(proposal_id,id),
 FOREIGN KEY(proposal_id,id) REFERENCES backend_change_proposal_batches(proposal_id,revision_id) DEFERRABLE INITIALLY DEFERRED
);
CREATE TABLE backend_change_proposal_events (
 project_id TEXT NOT NULL, proposal_id TEXT NOT NULL, revision_id TEXT NOT NULL,
 version INTEGER NOT NULL CHECK(version > 0), document TEXT NOT NULL CHECK(json_valid(document)),
 PRIMARY KEY(proposal_id,version),
 FOREIGN KEY(project_id,proposal_id,revision_id) REFERENCES backend_change_proposal_revisions(project_id,proposal_id,id)
);
CREATE TABLE backend_change_proposal_identities (
 project_id TEXT NOT NULL, proposal_id TEXT NOT NULL, id TEXT NOT NULL,
 record_type TEXT NOT NULL CHECK(record_type IN ('node','edge')), kind TEXT NOT NULL,
 first_revision_id TEXT NOT NULL, document TEXT NOT NULL CHECK(json_valid(document)),
 PRIMARY KEY(proposal_id,id),
 FOREIGN KEY(project_id,proposal_id,first_revision_id) REFERENCES backend_change_proposal_revisions(project_id,proposal_id,id)
);
CREATE TABLE backend_change_proposal_batches (
 project_id TEXT NOT NULL, proposal_id TEXT NOT NULL, revision_id TEXT NOT NULL,
 action TEXT NOT NULL CHECK(action IN ('create','apply','restore')),
 restore_revision_id TEXT, commands TEXT NOT NULL CHECK(json_valid(commands) AND json_type(commands)='array'),
 commands_hash TEXT NOT NULL, document TEXT NOT NULL CHECK(json_valid(document)),
 PRIMARY KEY(proposal_id,revision_id),
 CHECK((action='restore')=(restore_revision_id IS NOT NULL)),
 CHECK((action='apply' AND json_array_length(commands) BETWEEN 1 AND 100) OR (action!='apply' AND json_array_length(commands)=0)),
 FOREIGN KEY(project_id,proposal_id,revision_id) REFERENCES backend_change_proposal_revisions(project_id,proposal_id,id),
 FOREIGN KEY(proposal_id,restore_revision_id) REFERENCES backend_change_proposal_revisions(proposal_id,id)
);
CREATE TABLE backend_change_proposal_commands (
 proposal_id TEXT NOT NULL, command_id TEXT NOT NULL, revision_id TEXT NOT NULL,
 position INTEGER NOT NULL CHECK(position >= 0), document TEXT NOT NULL CHECK(json_valid(document)),
 PRIMARY KEY(proposal_id,command_id), UNIQUE(proposal_id,revision_id,position),
 FOREIGN KEY(proposal_id,revision_id) REFERENCES backend_change_proposal_batches(proposal_id,revision_id)
);
CREATE TRIGGER backend_change_command_matches_batch BEFORE INSERT ON backend_change_proposal_commands
WHEN NOT EXISTS (SELECT 1 FROM backend_change_proposal_batches b WHERE b.proposal_id=NEW.proposal_id AND b.revision_id=NEW.revision_id AND json_extract(b.commands,'$['||NEW.position||'].commandId')=NEW.command_id AND json(NEW.document)=json(json_extract(b.commands,'$['||NEW.position||']')))
BEGIN SELECT RAISE(ABORT,'change command must match immutable batch'); END;
CREATE TRIGGER backend_change_revision_no_update BEFORE UPDATE ON backend_change_proposal_revisions BEGIN SELECT RAISE(ABORT,'immutable change revision'); END;
CREATE TRIGGER backend_change_revision_no_delete BEFORE DELETE ON backend_change_proposal_revisions BEGIN SELECT RAISE(ABORT,'immutable change revision'); END;
CREATE TRIGGER backend_change_event_no_update BEFORE UPDATE ON backend_change_proposal_events BEGIN SELECT RAISE(ABORT,'immutable change event'); END;
CREATE TRIGGER backend_change_event_no_delete BEFORE DELETE ON backend_change_proposal_events BEGIN SELECT RAISE(ABORT,'immutable change event'); END;
CREATE TRIGGER backend_change_identity_no_update BEFORE UPDATE ON backend_change_proposal_identities BEGIN SELECT RAISE(ABORT,'immutable change identity'); END;
CREATE TRIGGER backend_change_identity_no_delete BEFORE DELETE ON backend_change_proposal_identities BEGIN SELECT RAISE(ABORT,'immutable change identity'); END;
CREATE TRIGGER backend_change_batch_no_update BEFORE UPDATE ON backend_change_proposal_batches BEGIN SELECT RAISE(ABORT,'immutable change batch'); END;
CREATE TRIGGER backend_change_batch_no_delete BEFORE DELETE ON backend_change_proposal_batches BEGIN SELECT RAISE(ABORT,'immutable change batch'); END;
CREATE TRIGGER backend_change_command_no_update BEFORE UPDATE ON backend_change_proposal_commands BEGIN SELECT RAISE(ABORT,'immutable change command'); END;
CREATE TRIGGER backend_change_command_no_delete BEFORE DELETE ON backend_change_proposal_commands BEGIN SELECT RAISE(ABORT,'immutable change command'); END;
