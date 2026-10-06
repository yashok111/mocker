-- M_PORTABLE: shared B5.2 foundation. No route or worker is enabled here.
CREATE TABLE backend_installation_identity (
 singleton INTEGER PRIMARY KEY CHECK(singleton=1),
 installation_id TEXT NOT NULL UNIQUE CHECK(length(installation_id)=36)
);
INSERT INTO backend_installation_identity(singleton,installation_id)
 SELECT 1,lower(hex(randomblob(4)) || '-' || hex(randomblob(2)) || '-4' ||
 substr(hex(randomblob(2)),2) || '-8' || substr(hex(randomblob(2)),2) || '-' || hex(randomblob(6)));
CREATE TRIGGER backend_installation_identity_update BEFORE UPDATE ON backend_installation_identity BEGIN SELECT RAISE(ABORT, 'immutable installation identity'); END;
CREATE TRIGGER backend_installation_identity_delete BEFORE DELETE ON backend_installation_identity BEGIN SELECT RAISE(ABORT, 'immutable installation identity'); END;
CREATE TABLE backend_materializations (
 project_id TEXT NOT NULL REFERENCES backend_projects(id), id TEXT NOT NULL,
 profile_version TEXT NOT NULL CHECK(profile_version='backend-http-draft-v1'),
 request_hash TEXT NOT NULL CHECK(length(request_hash)=64),
 candidate_hash TEXT NOT NULL CHECK(length(candidate_hash)=64),
 target_count INTEGER NOT NULL CHECK(target_count BETWEEN 1 AND 5),
 command_count INTEGER NOT NULL CHECK(command_count BETWEEN 0 AND 100),
 byte_count INTEGER NOT NULL CHECK(byte_count BETWEEN 0 AND 1048576),
 document TEXT NOT NULL CHECK(json_valid(document)), created_at INTEGER NOT NULL,
 PRIMARY KEY(project_id,id)
);
CREATE TABLE backend_materialization_receipts (
 project_id TEXT NOT NULL, idempotency_key TEXT NOT NULL,
 materialization_id TEXT NOT NULL, request_hash TEXT NOT NULL CHECK(length(request_hash)=64),
 response TEXT NOT NULL CHECK(json_valid(response)),
 PRIMARY KEY(project_id,idempotency_key),
 FOREIGN KEY(project_id,materialization_id) REFERENCES backend_materializations(project_id,id)
);
CREATE TABLE backend_portable_sessions (
 id TEXT PRIMARY KEY, direction TEXT NOT NULL CHECK(direction IN ('import','export')),
 version INTEGER NOT NULL CHECK(version>0),
 state TEXT NOT NULL CHECK(state IN ('staging','ready','committed','aborted')),
 manifest_hash TEXT NOT NULL CHECK(length(manifest_hash)=64),
 manifest TEXT NOT NULL CHECK(json_valid(manifest)),
 candidate_hash TEXT CHECK(candidate_hash IS NULL OR length(candidate_hash)=64),
 preview TEXT CHECK(preview IS NULL OR json_valid(preview)),
 project_id TEXT REFERENCES backend_projects(id),
 byte_count INTEGER NOT NULL CHECK(byte_count BETWEEN 0 AND 268435456),
 chunk_count INTEGER NOT NULL CHECK(chunk_count BETWEEN 1 AND 262144),
 created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
 CHECK(state!='committed' OR project_id IS NOT NULL)
);
CREATE TABLE backend_portable_chunks (
 session_id TEXT NOT NULL REFERENCES backend_portable_sessions(id),
 chunk_index INTEGER NOT NULL CHECK(chunk_index>=0),
 content_hash TEXT NOT NULL CHECK(length(content_hash)=64),
 record_count INTEGER NOT NULL CHECK(record_count BETWEEN 1 AND 500),
 byte_count INTEGER NOT NULL CHECK(byte_count BETWEEN 1 AND 1048576),
 body BLOB NOT NULL CHECK(length(body)=byte_count),
 PRIMARY KEY(session_id,chunk_index)
);
CREATE TABLE backend_portable_receipts (
 scope TEXT NOT NULL, operation TEXT NOT NULL CHECK(operation IN ('begin','put','preview','commit','abort','export')),
 idempotency_key TEXT NOT NULL, session_id TEXT NOT NULL REFERENCES backend_portable_sessions(id),
 request_hash TEXT NOT NULL CHECK(length(request_hash)=64),
 response TEXT NOT NULL CHECK(json_valid(response)),
 PRIMARY KEY(scope,operation,idempotency_key)
);
CREATE TABLE backend_portable_id_maps (
 project_id TEXT NOT NULL REFERENCES backend_projects(id),
 session_id TEXT NOT NULL REFERENCES backend_portable_sessions(id),
 identity_kind TEXT NOT NULL, origin_key TEXT NOT NULL, local_key TEXT NOT NULL,
 document TEXT NOT NULL CHECK(json_valid(document)),
 PRIMARY KEY(project_id,identity_kind,origin_key),
 UNIQUE(project_id,identity_kind,local_key)
);
CREATE TABLE backend_portable_origins (
 project_id TEXT NOT NULL REFERENCES backend_projects(id),
 record_kind TEXT NOT NULL, local_key TEXT NOT NULL,
 origin_pin TEXT NOT NULL CHECK(json_valid(origin_pin)),
 origin_hash TEXT NOT NULL CHECK(length(origin_hash)=64),
 document TEXT NOT NULL CHECK(json_valid(document)),
 PRIMARY KEY(project_id,record_kind,local_key)
);
CREATE TRIGGER backend_materializations_update BEFORE UPDATE ON backend_materializations BEGIN SELECT RAISE(ABORT, 'immutable B52 record'); END;
CREATE TRIGGER backend_materializations_delete BEFORE DELETE ON backend_materializations BEGIN SELECT RAISE(ABORT, 'immutable B52 record'); END;
CREATE TRIGGER backend_materialization_receipts_update BEFORE UPDATE ON backend_materialization_receipts BEGIN SELECT RAISE(ABORT, 'immutable B52 record'); END;
CREATE TRIGGER backend_materialization_receipts_delete BEFORE DELETE ON backend_materialization_receipts BEGIN SELECT RAISE(ABORT, 'immutable B52 record'); END;
CREATE TRIGGER backend_portable_receipts_update BEFORE UPDATE ON backend_portable_receipts BEGIN SELECT RAISE(ABORT, 'immutable B52 record'); END;
CREATE TRIGGER backend_portable_receipts_delete BEFORE DELETE ON backend_portable_receipts BEGIN SELECT RAISE(ABORT, 'immutable B52 record'); END;
CREATE TRIGGER backend_portable_id_maps_update BEFORE UPDATE ON backend_portable_id_maps BEGIN SELECT RAISE(ABORT, 'immutable B52 record'); END;
CREATE TRIGGER backend_portable_id_maps_delete BEFORE DELETE ON backend_portable_id_maps BEGIN SELECT RAISE(ABORT, 'immutable B52 record'); END;
CREATE TRIGGER backend_portable_origins_update BEFORE UPDATE ON backend_portable_origins BEGIN SELECT RAISE(ABORT, 'immutable B52 record'); END;
CREATE TRIGGER backend_portable_origins_delete BEFORE DELETE ON backend_portable_origins BEGIN SELECT RAISE(ABORT, 'immutable B52 record'); END;
CREATE TRIGGER backend_portable_chunks_update BEFORE UPDATE ON backend_portable_chunks BEGIN SELECT RAISE(ABORT, 'immutable portable chunk'); END;
CREATE TRIGGER backend_portable_chunks_insert BEFORE INSERT ON backend_portable_chunks BEGIN
 SELECT CASE WHEN NOT EXISTS (SELECT 1 FROM backend_portable_sessions WHERE id=NEW.session_id AND state='staging' AND NEW.chunk_index<chunk_count) THEN RAISE(ABORT, 'invalid portable chunk state or index') END;
 SELECT CASE WHEN COALESCE((SELECT sum(byte_count) FROM backend_portable_chunks WHERE session_id=NEW.session_id),0)+NEW.byte_count>268435456 THEN RAISE(ABORT, 'portable bundle quota') END;
END;
