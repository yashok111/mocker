-- Session reservations can acknowledge multiple aliases of the same immutable UUID.
ALTER TABLE backend_import_identities RENAME TO backend_import_identities_v1;
CREATE TABLE backend_import_identities (
 session_id TEXT NOT NULL REFERENCES backend_import_sessions(id),
 record_type TEXT NOT NULL,
 external_key TEXT NOT NULL,
 id TEXT NOT NULL,
 PRIMARY KEY(session_id,record_type,external_key)
);
INSERT INTO backend_import_identities SELECT * FROM backend_import_identities_v1;
DROP TABLE backend_import_identities_v1;
CREATE TABLE backend_identity_bindings (
 project_id TEXT NOT NULL REFERENCES backend_projects(id),
 repository_id TEXT NOT NULL REFERENCES backend_repositories(id),
 provider_namespace TEXT NOT NULL,
 record_type TEXT NOT NULL,
 external_key TEXT NOT NULL,
 id TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('active','retired','deleted','reserved')),
 revision_id TEXT NOT NULL REFERENCES backend_revisions(id),
 PRIMARY KEY(project_id,repository_id,provider_namespace,record_type,external_key)
);
CREATE UNIQUE INDEX backend_identity_active ON backend_identity_bindings(project_id,repository_id,provider_namespace,record_type,id) WHERE state='active';
CREATE TABLE backend_import_decisions (
 session_id TEXT NOT NULL REFERENCES backend_import_sessions(id),
 record_type TEXT NOT NULL,
 external_key TEXT NOT NULL,
 document TEXT NOT NULL,
 PRIMARY KEY(session_id,record_type,external_key)
);
CREATE TABLE backend_import_previews (
 session_id TEXT PRIMARY KEY REFERENCES backend_import_sessions(id),
 version INTEGER NOT NULL,
 document TEXT NOT NULL,
 details TEXT NOT NULL
);
CREATE TABLE backend_revision_decisions (
 revision_id TEXT PRIMARY KEY REFERENCES backend_revisions(id),
 document TEXT NOT NULL
);
-- Bootstrap only committed allocations with a matching original receipt and source.
-- Conflicting legacy ownership or IDs fail the migration's unique/FK constraints.
CREATE TABLE backend_reconciliation_upgrade_guard (invalid INTEGER CHECK(invalid=0));
INSERT INTO backend_reconciliation_upgrade_guard
SELECT CASE WHEN EXISTS (
 SELECT 1 FROM backend_import_sessions s WHERE s.state='committed' AND (
  (SELECT count(*) FROM backend_command_receipts c WHERE c.scope='import:'||s.project_id||':'||s.id||':commit')<>1
  OR (SELECT count(*) FROM backend_repositories repo WHERE repo.project_id=s.project_id)<>1
  OR NOT EXISTS (
   SELECT 1 FROM backend_command_receipts c
   JOIN backend_revisions r ON r.id=json_extract(c.response,'$.revision.id') AND r.project_id=s.project_id
   JOIN backend_revision_sources src ON src.revision_id=r.id
   JOIN backend_repositories repo ON repo.id=json_extract(s.document,'$.repositoryId') AND repo.project_id=s.project_id
   WHERE c.scope='import:'||s.project_id||':'||s.id||':commit'
    AND json_extract(c.response,'$.sessionId')=s.id
    AND json_array_length(json_extract(src.document,'$.snapshots'))=1
    AND json_extract(src.document,'$.snapshots[0].repositoryId')=repo.id
    AND json_extract(src.document,'$.snapshots[0].id')=json_extract(s.document,'$.snapshotId')
    AND json_extract(src.document,'$.snapshots[0].provider.namespace')=json_extract(s.document,'$.manifest.provider.namespace')
    AND json_extract(src.document,'$.snapshots[0].provider.name')=json_extract(s.document,'$.manifest.provider.name')
    AND json_extract(src.document,'$.snapshots[0].provider.version')=json_extract(s.document,'$.manifest.provider.version')
    AND json_extract(src.document,'$.snapshots[0].provider.method')=json_extract(s.document,'$.manifest.provider.method')
    AND json_extract(src.document,'$.snapshots[0].provider.profiles')=json_extract(s.document,'$.manifest.provider.profiles')
    AND NOT EXISTS (
      SELECT 1 FROM backend_graph_records g WHERE g.revision_id=r.id AND NOT EXISTS (
        SELECT 1 FROM backend_import_identities i WHERE i.session_id=s.id
         AND i.record_type=g.record_type AND i.id=g.id AND i.external_key=json_extract(g.document,'$.externalKey')
      )
    )
  )
 )) THEN 1 ELSE 0 END;
DROP TABLE backend_reconciliation_upgrade_guard;
INSERT INTO backend_identity_bindings
SELECT s.project_id, json_extract(s.document,'$.repositoryId'),
 json_extract(s.document,'$.manifest.provider.namespace'), i.record_type,i.external_key,i.id,
 CASE WHEN g.id IS NULL THEN 'reserved' ELSE 'active' END,
 json_extract(c.response,'$.revision.id')
FROM backend_import_sessions s
JOIN backend_import_identities i ON i.session_id=s.id
JOIN backend_command_receipts c ON c.scope='import:'||s.project_id||':'||s.id||':commit'
JOIN backend_revision_sources src ON src.revision_id=json_extract(c.response,'$.revision.id')
 AND json_array_length(json_extract(src.document,'$.snapshots'))=1
 AND json_extract(src.document,'$.snapshots[0].repositoryId')=json_extract(s.document,'$.repositoryId')
 AND json_extract(src.document,'$.snapshots[0].id')=json_extract(s.document,'$.snapshotId')
LEFT JOIN backend_graph_records g ON g.revision_id=src.revision_id AND g.record_type=i.record_type AND g.id=i.id
WHERE s.state='committed';
CREATE TABLE backend_import_aliases (
 session_id TEXT NOT NULL REFERENCES backend_import_sessions(id),
 record_type TEXT NOT NULL,
 external_key TEXT NOT NULL,
 source_key TEXT NOT NULL,
 id TEXT NOT NULL,
 PRIMARY KEY(session_id,record_type,external_key)
);
