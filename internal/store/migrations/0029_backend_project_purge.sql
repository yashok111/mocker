-- Nonsensitive erasure receipts deliberately survive their erased project.
CREATE TABLE backend_project_purge_receipts (
 project_id TEXT PRIMARY KEY,
 policy TEXT NOT NULL,
 confirmation_hash TEXT NOT NULL CHECK(length(confirmation_hash)=64),
 erased_at TEXT NOT NULL,
 rows_deleted INTEGER NOT NULL CHECK(rows_deleted>=0),
 payload_bytes INTEGER NOT NULL CHECK(payload_bytes>=0)
);
CREATE TRIGGER backend_project_purge_receipts_no_update BEFORE UPDATE ON backend_project_purge_receipts
 BEGIN SELECT RAISE(ABORT,'immutable erasure receipt'); END;
CREATE TRIGGER backend_project_purge_receipts_no_delete BEFORE DELETE ON backend_project_purge_receipts
 BEGIN SELECT RAISE(ABORT,'immutable erasure receipt'); END;
