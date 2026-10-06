CREATE TABLE IF NOT EXISTS metadata (singleton INTEGER PRIMARY KEY CHECK(singleton=1), isolation TEXT NOT NULL, epoch INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS runs (id TEXT PRIMARY KEY, epoch INTEGER NOT NULL UNIQUE, identity BLOB NOT NULL, authorization BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS requests (run_id TEXT NOT NULL REFERENCES runs(id), request_key TEXT NOT NULL, step_id TEXT NOT NULL, hash TEXT NOT NULL, status INTEGER NOT NULL, receipt BLOB NOT NULL, PRIMARY KEY(run_id,request_key), UNIQUE(run_id,step_id));
CREATE TABLE IF NOT EXISTS events (run_id TEXT NOT NULL REFERENCES runs(id), sequence INTEGER NOT NULL, document BLOB NOT NULL, PRIMARY KEY(run_id,sequence));
CREATE TABLE IF NOT EXISTS orders (run_id TEXT NOT NULL REFERENCES runs(id), business_key TEXT NOT NULL, document BLOB NOT NULL, PRIMARY KEY(run_id,business_key));
CREATE TABLE IF NOT EXISTS charges (run_id TEXT NOT NULL REFERENCES runs(id), payment_key TEXT NOT NULL, document BLOB NOT NULL, PRIMARY KEY(run_id,payment_key));
CREATE TABLE IF NOT EXISTS arms (run_id TEXT PRIMARY KEY REFERENCES runs(id), consumed INTEGER NOT NULL CHECK(consumed IN (0,1)));
