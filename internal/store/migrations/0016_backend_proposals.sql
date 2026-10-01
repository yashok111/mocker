-- Source ownership is part of every proposal baseline pin.
CREATE UNIQUE INDEX backend_revision_owner ON backend_revisions(project_id,id);
CREATE UNIQUE INDEX backend_repository_owner ON backend_repositories(project_id,id);

CREATE TABLE backend_proposals (
 id TEXT PRIMARY KEY,
 project_id TEXT NOT NULL REFERENCES backend_projects(id) ON DELETE RESTRICT,
 version INTEGER NOT NULL CHECK(version>0),
 name TEXT NOT NULL,
 status TEXT NOT NULL CHECK(status='draft'),
 base_revision_id TEXT NOT NULL,
 base_semantic_hash TEXT NOT NULL,
 repository_id TEXT NOT NULL,
 datastore_id TEXT NOT NULL,
 facet_key TEXT NOT NULL,
 draft_revision_id TEXT NOT NULL,
 draft_hash TEXT NOT NULL,
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL,
 FOREIGN KEY(project_id,base_revision_id) REFERENCES backend_revisions(project_id,id) ON DELETE RESTRICT,
 FOREIGN KEY(project_id,repository_id) REFERENCES backend_repositories(project_id,id) ON DELETE RESTRICT,
 FOREIGN KEY(id,draft_revision_id) REFERENCES backend_proposal_revisions(proposal_id,id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED
);
CREATE INDEX backend_proposals_project ON backend_proposals(project_id,id);
CREATE INDEX backend_proposals_base ON backend_proposals(project_id,base_revision_id,id);
CREATE INDEX backend_proposals_repository ON backend_proposals(project_id,repository_id);

CREATE TRIGGER backend_proposal_baseline_immutable
BEFORE UPDATE OF project_id,base_revision_id,base_semantic_hash,repository_id,datastore_id,facet_key,created_at ON backend_proposals
WHEN NEW.project_id IS NOT OLD.project_id
 OR NEW.base_revision_id IS NOT OLD.base_revision_id
 OR NEW.base_semantic_hash IS NOT OLD.base_semantic_hash
 OR NEW.repository_id IS NOT OLD.repository_id
 OR NEW.datastore_id IS NOT OLD.datastore_id
 OR NEW.facet_key IS NOT OLD.facet_key
 OR NEW.created_at IS NOT OLD.created_at
BEGIN SELECT RAISE(ABORT, 'Proposal baseline and selection are immutable'); END;

CREATE TABLE backend_proposal_revisions (
 id TEXT PRIMARY KEY,
 proposal_id TEXT NOT NULL REFERENCES backend_proposals(id) ON DELETE RESTRICT,
 parent_revision_id TEXT,
 document TEXT NOT NULL,
 UNIQUE(proposal_id,id),
 FOREIGN KEY(proposal_id,parent_revision_id) REFERENCES backend_proposal_revisions(proposal_id,id) ON DELETE RESTRICT
);
CREATE INDEX backend_proposal_revisions_parent ON backend_proposal_revisions(proposal_id,parent_revision_id);

CREATE TRIGGER backend_proposal_revision_immutable_update
BEFORE UPDATE ON backend_proposal_revisions
BEGIN SELECT RAISE(ABORT, 'Proposal revisions are immutable'); END;
CREATE TRIGGER backend_proposal_revision_immutable_delete
BEFORE DELETE ON backend_proposal_revisions
BEGIN SELECT RAISE(ABORT, 'Proposal revisions are immutable'); END;
