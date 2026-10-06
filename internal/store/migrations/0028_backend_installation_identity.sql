-- Older installations applied M_PORTABLE before installation identity was added
-- to its definition. Repair them without replacing any existing namespace UUID.
CREATE TABLE IF NOT EXISTS backend_installation_identity (
 singleton INTEGER PRIMARY KEY CHECK(singleton=1),
 installation_id TEXT NOT NULL UNIQUE CHECK(length(installation_id)=36)
);
INSERT INTO backend_installation_identity(singleton,installation_id)
 SELECT 1,lower(hex(randomblob(4)) || '-' || hex(randomblob(2)) || '-4' ||
 substr(hex(randomblob(2)),2) || '-8' || substr(hex(randomblob(2)),2) || '-' || hex(randomblob(6)))
 WHERE NOT EXISTS (SELECT 1 FROM backend_installation_identity WHERE singleton=1);
CREATE TRIGGER IF NOT EXISTS backend_installation_identity_update BEFORE UPDATE ON backend_installation_identity BEGIN SELECT RAISE(ABORT, 'immutable installation identity'); END;
CREATE TRIGGER IF NOT EXISTS backend_installation_identity_delete BEFORE DELETE ON backend_installation_identity BEGIN SELECT RAISE(ABORT, 'immutable installation identity'); END;
