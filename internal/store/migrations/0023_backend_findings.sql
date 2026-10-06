CREATE TABLE backend_finding_checks (
 project_id TEXT NOT NULL REFERENCES backend_projects(id), job_id TEXT NOT NULL,
 result_version INTEGER NOT NULL, fingerprint TEXT NOT NULL, scope_key TEXT NOT NULL,
 status TEXT NOT NULL CHECK(status IN ('present','absent','unknown')),
 PRIMARY KEY(project_id,job_id,result_version,fingerprint),
 FOREIGN KEY(project_id,job_id,result_version) REFERENCES backend_analysis_manifests(project_id,job_id,result_version)
);
CREATE TABLE backend_finding_occurrences (
 project_id TEXT NOT NULL, job_id TEXT NOT NULL, result_version INTEGER NOT NULL,
 fingerprint TEXT NOT NULL, basis_hash TEXT NOT NULL, document TEXT NOT NULL CHECK(json_valid(document)),
 PRIMARY KEY(project_id,job_id,result_version,fingerprint),
 FOREIGN KEY(project_id,job_id,result_version,fingerprint) REFERENCES backend_finding_checks(project_id,job_id,result_version,fingerprint)
);
CREATE TABLE backend_finding_reviews (
 project_id TEXT NOT NULL REFERENCES backend_projects(id), fingerprint TEXT NOT NULL,
 version INTEGER NOT NULL CHECK(version>0), basis_hash TEXT NOT NULL,
 status TEXT NOT NULL CHECK(status IN ('open','accepted_risk','false_positive','resolved')),
 PRIMARY KEY(project_id,fingerprint)
);
CREATE TABLE backend_finding_events (
 project_id TEXT NOT NULL, fingerprint TEXT NOT NULL, version INTEGER NOT NULL,
 document TEXT NOT NULL CHECK(json_valid(document)),
 PRIMARY KEY(project_id,fingerprint,version),
 FOREIGN KEY(project_id,fingerprint) REFERENCES backend_finding_reviews(project_id,fingerprint)
);
CREATE TABLE backend_finding_receipts (
 project_id TEXT NOT NULL REFERENCES backend_projects(id), idempotency_key TEXT NOT NULL,
 request_hash TEXT NOT NULL, response TEXT NOT NULL CHECK(json_valid(response)),
 PRIMARY KEY(project_id,idempotency_key)
);
CREATE TRIGGER backend_finding_checks_update BEFORE UPDATE ON backend_finding_checks BEGIN SELECT RAISE(ABORT, 'immutable finding history'); END;
CREATE TRIGGER backend_finding_checks_delete BEFORE DELETE ON backend_finding_checks BEGIN SELECT RAISE(ABORT, 'immutable finding history'); END;
CREATE TRIGGER backend_finding_occurrences_update BEFORE UPDATE ON backend_finding_occurrences BEGIN SELECT RAISE(ABORT, 'immutable finding history'); END;
CREATE TRIGGER backend_finding_occurrences_delete BEFORE DELETE ON backend_finding_occurrences BEGIN SELECT RAISE(ABORT, 'immutable finding history'); END;
CREATE TRIGGER backend_finding_events_update BEFORE UPDATE ON backend_finding_events BEGIN SELECT RAISE(ABORT, 'immutable finding history'); END;
CREATE TRIGGER backend_finding_events_delete BEFORE DELETE ON backend_finding_events BEGIN SELECT RAISE(ABORT, 'immutable finding history'); END;
CREATE TRIGGER backend_finding_receipts_update BEFORE UPDATE ON backend_finding_receipts BEGIN SELECT RAISE(ABORT, 'immutable finding history'); END;
CREATE TRIGGER backend_finding_receipts_delete BEFORE DELETE ON backend_finding_receipts BEGIN SELECT RAISE(ABORT, 'immutable finding history'); END;
