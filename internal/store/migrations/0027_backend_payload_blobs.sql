-- M_BLOBS. The named backend_payload_blobs Go hook creates the compiled
-- registry DDL, streams lossless raw backfill, seals canonical manifests and
-- verifies counts/bytes/FKs in this same transaction before user_version=27.
-- No existing migration is changed. Never run an old binary on Store27.
SELECT 1;
