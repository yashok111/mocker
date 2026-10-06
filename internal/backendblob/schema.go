package backendblob

// Schema is also used by the named Store27 migration hook and isolated tests.
const Schema = `
CREATE TABLE backend_payload_blobs (
 key TEXT PRIMARY KEY CHECK(length(key)=64), owner TEXT NOT NULL,
 schema_version TEXT NOT NULL, record_type TEXT NOT NULL,
 byte_length INTEGER NOT NULL CHECK(byte_length>=0), payload BLOB NOT NULL,
 CHECK(byte_length=length(payload))
);
CREATE TRIGGER backend_payload_blobs_update BEFORE UPDATE ON backend_payload_blobs BEGIN SELECT RAISE(ABORT,'immutable raw blob'); END;
CREATE TRIGGER backend_payload_blobs_delete BEFORE DELETE ON backend_payload_blobs BEGIN SELECT RAISE(ABORT,'immutable raw blob'); END;
CREATE TABLE backend_payload_pending (owner TEXT NOT NULL, owner_id TEXT NOT NULL, version TEXT NOT NULL, PRIMARY KEY(owner,owner_id,version));
CREATE TABLE backend_payload_manifests (
 owner TEXT NOT NULL, owner_id TEXT NOT NULL, version TEXT NOT NULL,
 member_count INTEGER NOT NULL CHECK(member_count>=0), digest TEXT NOT NULL,
 PRIMARY KEY(owner,owner_id,version)
);
CREATE TABLE backend_payload_members (
 owner TEXT NOT NULL, owner_id TEXT NOT NULL, version TEXT NOT NULL,
 member_type TEXT NOT NULL, member_id TEXT NOT NULL,
 payload_key TEXT NOT NULL REFERENCES backend_payload_blobs(key),
 metadata BLOB NOT NULL, project_id TEXT NOT NULL,
 PRIMARY KEY(owner,owner_id,version,member_type,member_id),
 FOREIGN KEY(owner,owner_id,version) REFERENCES backend_payload_manifests(owner,owner_id,version) DEFERRABLE INITIALLY DEFERRED
);
CREATE INDEX backend_payload_members_identity ON backend_payload_members(owner,member_id);
CREATE INDEX backend_payload_members_project ON backend_payload_members(project_id,owner,owner_id,version);
CREATE TRIGGER backend_payload_manifests_update BEFORE UPDATE ON backend_payload_manifests BEGIN SELECT RAISE(ABORT,'immutable payload manifest'); END;
CREATE TRIGGER backend_payload_manifests_delete BEFORE DELETE ON backend_payload_manifests BEGIN SELECT RAISE(ABORT,'immutable payload manifest'); END;
CREATE TRIGGER backend_payload_members_update BEFORE UPDATE ON backend_payload_members BEGIN SELECT RAISE(ABORT,'immutable payload membership'); END;
CREATE TRIGGER backend_payload_members_delete BEFORE DELETE ON backend_payload_members BEGIN SELECT RAISE(ABORT,'immutable payload membership'); END;
CREATE TRIGGER backend_payload_members_sealed BEFORE INSERT ON backend_payload_members
 WHEN EXISTS(SELECT 1 FROM backend_payload_manifests WHERE owner=NEW.owner AND owner_id=NEW.owner_id AND version=NEW.version)
 BEGIN SELECT RAISE(ABORT,'sealed payload manifest'); END;
`

var canonicalIndexes = []Object{
	{Kind: "index", Name: "backend_payload_members_identity", SQL: "CREATE INDEX backend_payload_members_identity ON backend_payload_members(owner,member_id)"},
	{Kind: "index", Name: "backend_payload_members_project", SQL: "CREATE INDEX backend_payload_members_project ON backend_payload_members(project_id,owner,owner_id,version)"},
}
