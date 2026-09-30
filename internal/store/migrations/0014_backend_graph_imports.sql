CREATE TABLE backend_import_sessions (
 id TEXT PRIMARY KEY,
 project_id TEXT NOT NULL REFERENCES backend_projects(id),
 state TEXT NOT NULL,
 version INTEGER NOT NULL CHECK(version>0),
 document TEXT NOT NULL
);
CREATE INDEX backend_import_sessions_project ON backend_import_sessions(project_id,id);
CREATE TABLE backend_import_identities (
 session_id TEXT NOT NULL REFERENCES backend_import_sessions(id),
 record_type TEXT NOT NULL,
 external_key TEXT NOT NULL,
 id TEXT NOT NULL,
 PRIMARY KEY(session_id,record_type,external_key),
 UNIQUE(session_id,id)
);
CREATE TABLE backend_import_records (
 session_id TEXT NOT NULL REFERENCES backend_import_sessions(id),
 record_type TEXT NOT NULL,
 external_key TEXT NOT NULL,
 document TEXT NOT NULL,
 PRIMARY KEY(session_id,record_type,external_key)
);
CREATE TABLE backend_import_batches (
 session_id TEXT NOT NULL REFERENCES backend_import_sessions(id),
 batch_id TEXT NOT NULL,
 payload_hash TEXT NOT NULL,
 request_hash TEXT NOT NULL,
 accepted_version INTEGER NOT NULL,
 receipt TEXT NOT NULL,
 PRIMARY KEY(session_id,batch_id)
);
CREATE TABLE backend_repositories (
 id TEXT PRIMARY KEY,
 project_id TEXT NOT NULL REFERENCES backend_projects(id),
 logical_name TEXT NOT NULL
);
CREATE INDEX backend_repositories_project ON backend_repositories(project_id,id);
CREATE TABLE backend_revision_sources (
 revision_id TEXT PRIMARY KEY REFERENCES backend_revisions(id),
 document TEXT NOT NULL
);
CREATE TABLE backend_graph_records (
 project_id TEXT NOT NULL REFERENCES backend_projects(id),
 revision_id TEXT NOT NULL REFERENCES backend_revisions(id),
 record_type TEXT NOT NULL,
 id TEXT NOT NULL,
 kind TEXT NOT NULL DEFAULT '',
 name TEXT NOT NULL DEFAULT '',
 parent_id TEXT NOT NULL DEFAULT '',
 from_id TEXT NOT NULL DEFAULT '',
 to_id TEXT NOT NULL DEFAULT '',
 subject_id TEXT NOT NULL DEFAULT '',
 document TEXT NOT NULL,
 PRIMARY KEY(revision_id,record_type,id)
);
CREATE INDEX backend_graph_query ON backend_graph_records(project_id,revision_id,record_type,id);
CREATE INDEX backend_graph_subject ON backend_graph_records(project_id,revision_id,record_type,subject_id,id);
CREATE INDEX backend_graph_endpoints ON backend_graph_records(project_id,revision_id,record_type,from_id,to_id,id);
