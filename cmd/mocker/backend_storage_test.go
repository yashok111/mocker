package main

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/backendblob"
	"github.com/yashok111/mocker/internal/store"
)

func TestBackendStorageCLIRejectsMissingAndInvalidTargets(t *testing.T) {
	for _, args := range [][]string{nil, {"verify"}, {"rebuild", "--db", "missing", "--project", "bad"}, {"verify", "--db", "missing", "--project", "ignored"}, {"verify", "--db", "missing", "extra"}} {
		var out bytes.Buffer
		if err := runBackendStorage(t.Context(), args, &out, &out); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	path := filepath.Join(t.TempDir(), "absent.db")
	var out bytes.Buffer
	if err := runBackendStorage(t.Context(), []string{"verify", "--db", path}, &out, &out); err == nil {
		t.Fatal("created target")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("CLI created database", err)
	}
}

// review 2026-10-06, F165: --project was validated with uuid.Parse (which
// accepts upper case, braces, urn:uuid: and bare hex) and then passed on
// RAW, so an existing project spelled in upper case was reported as
// "unknown project". The parsed, canonical form is what must reach the store.
func TestBackendStorageRebuildCanonicalizesProjectUUID(t *testing.T) {
	const id = "6ba7b810-9dad-11d1-80b4-00c04fd430c8"
	path := filepath.Join(t.TempDir(), "store27.db")
	db, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Migrate(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	err = db.Write(t.Context(), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(t.Context(), `INSERT INTO backend_projects VALUES(?,'P',1,'r','now','now')`, id); err != nil {
			return err
		}
		if _, err := backendblob.Exec(t.Context(), tx, `INSERT INTO backend_revisions(id,project_id,document) VALUES('r',?,'{"schemaVersion":"6"}')`, id); err != nil {
			return err
		}
		return backendblob.Seal(t.Context(), tx)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	for _, spelling := range []string{strings.ToUpper(id), "{" + id + "}", "urn:uuid:" + id} {
		var out bytes.Buffer
		if err := runBackendStorage(t.Context(), []string{"rebuild", "--db", path, "--project", spelling}, &out, &out); err != nil {
			t.Fatalf("%s: %v", spelling, err)
		}
	}
}
