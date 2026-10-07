package store

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestFoundationMigration23To24(t *testing.T) {
	db, err := Open(t.Context(), filepath.Join(t.TempDir(), "foundation.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range migrations {
		if m.version <= 23 {
			if err := db.applyMigration(t.Context(), m); err != nil {
				t.Fatal(err)
			}
		}
	}
	migration21Version(t, db, 23)
	if err := db.Migrate(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	// Was a literal 24 (the head when B5.2 landed); Migrate goes to the head.
	migration21Version(t, db, latestMigration(t))
	for _, name := range []string{"backend_materializations", "backend_materialization_receipts", "backend_portable_sessions", "backend_portable_chunks", "backend_portable_receipts", "backend_portable_id_maps", "backend_portable_origins"} {
		var n int
		if err := db.R.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", name).Scan(&n); err != nil || n != 1 {
			t.Fatal(name, n, err)
		}
	}
	if _, err := db.W.Exec(`INSERT INTO backend_portable_sessions(id,direction,version,state,manifest_hash,manifest,byte_count,chunk_count,created_at,updated_at) VALUES ('session','import',1,'staging',?,'{}',0,2,1,1)`, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.W.Exec(`INSERT INTO backend_portable_chunks(session_id,chunk_index,content_hash,record_count,byte_count,body) VALUES ('session',0,?,1,2,x'7b7d')`, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.W.Exec(`UPDATE backend_portable_chunks SET body=x'00'`); err == nil {
		t.Fatal("mutable chunk")
	}
	if _, err := db.W.Exec(`INSERT INTO backend_portable_chunks(session_id,chunk_index,content_hash,record_count,byte_count,body) VALUES ('session',1,?,501,2,x'7b7d')`, strings.Repeat("a", 64)); err == nil {
		t.Fatal("record cap missing")
	}
	if _, err := db.W.Exec(`INSERT INTO backend_portable_chunks(session_id,chunk_index,content_hash,record_count,byte_count,body) VALUES ('session',2,?,1,2,x'7b7d')`, strings.Repeat("a", 64)); err == nil {
		t.Fatal("out-of-range chunk index")
	}
	migration21ForeignKeys(t, db)
}
